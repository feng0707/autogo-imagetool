package main

// 异形涂抹窗口（画笔）2026-09-30 —— 点阵模式，照搬智能取色的渲染/交互。
// 用法：主窗口框选 → 本窗口「获取选区」→ 画笔涂抹不要的部分 → 保存 PNG。
// 涂抹规则对齐大漠/OP：模板四角同色（≥50%）⇒ 该色为背景，找图时只比前景像素。
// 进入涂抹时自动取四角最常见色为涂抹色并自动补涂四角；撤销/恢复原图可回退。

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	// nativedialog 已移除（保存改直接落目录；导入走 PowerShell 文件夹选择）
)

// ── 涂抹原语（regionImg 上直接改像素，返回涂到的点用于同步点阵图）─────────

func paintCircleNRGBA(img *image.NRGBA, cx, cy, d int, c color.RGBA) []image.Point {
	if img == nil {
		return nil
	}
	if d < 1 {
		d = 1
	}
	if d > 64 {
		d = 64
	}
	half := d / 2
	cxc := float32((d-1)%2) * 0.5
	cyc := cxc
	r2 := float32(d) * float32(d) / 4.0
	b := img.Bounds()
	var pts []image.Point
	for dy := -half; dy <= d-half-1; dy++ {
		y := cy + dy
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		ody := float32(dy) - cyc
		for dx := -half; dx <= d-half-1; dx++ {
			x := cx + dx
			if x < b.Min.X || x >= b.Max.X {
				continue
			}
			odx := float32(dx) - cxc
			if odx*odx+ody*ody <= r2 {
				off := img.PixOffset(x, y)
				img.Pix[off+0] = c.R
				img.Pix[off+1] = c.G
				img.Pix[off+2] = c.B
				img.Pix[off+3] = 255
				pts = append(pts, image.Point{X: x, Y: y})
			}
		}
	}
	return pts
}

func paintStrokeNRGBA(img *image.NRGBA, x1, y1, x2, y2, d int, c color.RGBA) []image.Point {
	dx, dy := x2-x1, y2-y1
	dist := sqrtF(float64(dx*dx + dy*dy))
	step := d / 2
	if step < 1 {
		step = 1
	}
	n := int(dist) / step
	if n > 512 {
		n = 512
	}
	var pts []image.Point
	for i := 0; i <= n; i++ {
		t := 0.0
		if n > 0 {
			t = float64(i) / float64(n)
		}
		pts = append(pts, paintCircleNRGBA(img, x1+int(float64(dx)*t), y1+int(float64(dy)*t), d, c)...)
	}
	return pts
}

func sqrtF(v float64) float64 { /* 牛顿迭代两步足够，避免为 sqrt 单独引依赖 */
	if v <= 0 {
		return 0
	}
	r := v
	for i := 0; i < 24; i++ {
		r = (r + v/r) / 2
	}
	return r
}

// cornerBgColorNRGBA 取四角像素中出现最多的颜色（默认涂抹背景色）
func cornerBgColorNRGBA(img *image.NRGBA) color.RGBA {
	if img == nil {
		return color.RGBA{255, 0, 255, 255}
	}
	b := img.Bounds()
	corners := [4][2]int{
		{b.Min.X, b.Min.Y}, {b.Max.X - 1, b.Min.Y},
		{b.Min.X, b.Max.Y - 1}, {b.Max.X - 1, b.Max.Y - 1},
	}
	var best color.RGBA
	bestCt := -1
	for i, c := range corners {
		ct := 1
		for j := i + 1; j < 4; j++ {
			if corners[j] == c {
				ct++
			}
		}
		if ct > bestCt {
			bestCt = ct
			px := img.NRGBAAt(c[0], c[1])
			best = color.RGBA{px.R, px.G, px.B, 255}
		}
	}
	return best
}

// dotPaintCells 把涂到的图像像素同步到点阵渲染图上（每像素一个 dotCellSize 色块）
func dotPaintCells(dotImg *image.NRGBA, pts []image.Point, c color.RGBA) {
	if dotImg == nil {
		return
	}
	stride := dotCellSize + 1
	for _, p := range pts {
		cx := p.X*stride + 1
		cy := p.Y*stride + 1
		for dy := 0; dy < dotCellSize; dy++ {
			for dx := 0; dx < dotCellSize; dx++ {
				off := (cy+dy)*dotImg.Stride + (cx+dx)*4
				dotImg.Pix[off+0] = c.R
				dotImg.Pix[off+1] = c.G
				dotImg.Pix[off+2] = c.B
				dotImg.Pix[off+3] = 255
			}
		}
	}
}

// eraseCircleNRGBA 橡皮擦：把圆内像素恢复为 orig（获取选区时的原图）内容
func eraseCircleNRGBA(dst, orig *image.NRGBA, cx, cy, d int) []image.Point {
	if dst == nil || orig == nil {
		return nil
	}
	if d < 1 {
		d = 1
	}
	if d > 64 {
		d = 64
	}
	half := d / 2
	cxc := float32((d-1)%2) * 0.5
	cyc := cxc
	r2 := float32(d) * float32(d) / 4.0
	b := dst.Bounds()
	var pts []image.Point
	for dy := -half; dy <= d-half-1; dy++ {
		y := cy + dy
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		ody := float32(dy) - cyc
		for dx := -half; dx <= d-half-1; dx++ {
			x := cx + dx
			if x < b.Min.X || x >= b.Max.X {
				continue
			}
			odx := float32(dx) - cxc
			if odx*odx+ody*ody <= r2 {
				off := dst.PixOffset(x, y)
				op := orig.PixOffset(x, y)
				dst.Pix[off+0] = orig.Pix[op+0]
				dst.Pix[off+1] = orig.Pix[op+1]
				dst.Pix[off+2] = orig.Pix[op+2]
				dst.Pix[off+3] = 255
				pts = append(pts, image.Point{X: x, Y: y})
			}
		}
	}
	return pts
}

// dotSyncCells 把点阵图上 pts 的色块刷新为 regionImg 当前的像素色（擦除后颜色会变）
func dotSyncCells(dotImg, regionImg *image.NRGBA, pts []image.Point) {
	if dotImg == nil || regionImg == nil {
		return
	}
	stride := dotCellSize + 1
	b := regionImg.Bounds()
	for _, p := range pts {
		if p.X < b.Min.X || p.X >= b.Max.X || p.Y < b.Min.Y || p.Y >= b.Max.Y {
			continue
		}
		px := regionImg.NRGBAAt(p.X, p.Y)
		cx := p.X*stride + 1
		cy := p.Y*stride + 1
		for dy := 0; dy < dotCellSize; dy++ {
			for dx := 0; dx < dotCellSize; dx++ {
				off := (cy+dy)*dotImg.Stride + (cx+dx)*4
				dotImg.Pix[off+0] = px.R
				dotImg.Pix[off+1] = px.G
				dotImg.Pix[off+2] = px.B
				dotImg.Pix[off+3] = 255
			}
		}
	}
}

// ★ 单实例：重复点按钮只聚焦已开的窗口（用户要求保持一个）
var paintWin fyne.Window

func openPaintWindow(parentWindow fyne.Window) {
	if paintWin != nil {
		paintWin.RequestFocus()
		return
	}
	a := fyne.CurrentApp()
	w := a.NewWindow("luatouch 裁剪画笔涂抹（异形图）")
	paintWin = w
	w.SetOnClosed(func() { paintWin = nil })
	w.Resize(fyne.NewSize(1200, 700))
	w.CenterOnScreen()

	var regionImg *image.NRGBA // 涂抹源（真数据）
	var dotImg *image.NRGBA    // 点阵渲染图（显示用）
	var undoStack []*image.NRGBA
	brushSize := 3
	var brushColor = color.RGBA{255, 0, 255, 255}
	eyedropperMode := false
	lastPaintX, lastPaintY := -1, -1
	lastPaintTime := time.Time{}
	strokeActive := false
	imgDir := loadToolPaths().PaintImgDir // ★ 图片目录持久化（tool_paths.json；启动即恢复）
	eraserMode := false             // ★ 橡皮擦模式：把涂过的区域恢复成原图像素
	var origImg *image.NRGBA        // 获取选区时的原始图像（擦除的数据来源）

	dotCanvasImg := canvas.NewImageFromImage(nil)
	dotCanvasImg.ScaleMode = canvas.ImageScalePixels
	// ★ ImageFillStretch：图像精确填满锁定尺寸的框（配合 noStretchLayout）。
	//   原 FillOriginal 在窗口最大化时会把图像居中绘制 + Stack 拉伸覆盖层 ⇒ 坐标错位。
	dotCanvasImg.FillMode = canvas.ImageFillStretch

	// ★ 紧凑正方形版，放左栏「获取选区」上面（原右侧大面板已移除）
	magPanel := NewPickMagnifierPanelCompact(15, 9)
	infoLabel := widget.NewLabel("请先点「获取选区」从主窗口拉入框选图像")
	infoLabel.Wrapping = fyne.TextWrapWord

	// ★ 背景占比实时检测（引擎规则：四角同色 + 占比 ≥30% 即判背景）
	bgLabel := widget.NewLabel("背景: --")
	bgLabel.Wrapping = fyne.TextWrapWord
	updateBgLabel := func() {
		if regionImg == nil {
			bgLabel.SetText("背景: --")
			return
		}
		bgColor := cornerBgColorNRGBA(regionImg)
		b := regionImg.Bounds()
		corners := [4][2]int{
			{b.Min.X, b.Min.Y}, {b.Max.X - 1, b.Min.Y},
			{b.Min.X, b.Max.Y - 1}, {b.Max.X - 1, b.Max.Y - 1},
		}
		uniform := true
		for _, c := range corners {
			px := regionImg.NRGBAAt(c[0], c[1])
			if px.R != bgColor.R || px.G != bgColor.G || px.B != bgColor.B {
				uniform = false
			}
		}
		ct, total := 0, 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				px := regionImg.NRGBAAt(x, y)
				total++
				if px.R == bgColor.R && px.G == bgColor.G && px.B == bgColor.B {
					ct++
				}
			}
		}
		pct := float32(ct) * 100 / float32(total)
		uni := "四角一致 ✓"
		if !uniform {
			uni = "四角不一致 ✗"
		}
		verdict := "✓ 够"
		if pct < 30 || !uniform {
			verdict = "✗ 不够（多涂背景或裁小一圈）"
		}
		bgLabel.SetText(fmt.Sprintf("背景 %02X%02X%02X 占比 %.1f%% | %s | %s",
			bgColor.R, bgColor.G, bgColor.B, pct, uni, verdict))
	}

	// ===== 色板（图色板）：预设色块点选 + 吸管取图色 =====
	colorIndicator := canvas.NewRectangle(brushColor)
	colorIndicator.SetMinSize(fyne.NewSize(28, 18))
	colorIndicator.StrokeWidth = 1
	colorIndicator.StrokeColor = color.Gray{Y: 120}
	setBrushColor := func(c color.RGBA) {
		brushColor = c
		colorIndicator.FillColor = c
		colorIndicator.Refresh()
	}
	presetColors := []color.RGBA{
		{255, 0, 255, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255},
		{0, 255, 255, 255}, {255, 0, 0, 255}, {0, 0, 0, 255}, {255, 255, 255, 255},
	}
	swatchObjects := make([]fyne.CanvasObject, 0, len(presetColors))
	for _, c := range presetColors {
		cc := c
		swatchRect := canvas.NewRectangle(cc)
		swatchRect.SetMinSize(fyne.NewSize(14, 30)) // ★ 加厚：好点（原来自适应太小）
		swatchRect.CornerRadius = 2
		swatchRect.StrokeWidth = 1
		swatchRect.StrokeColor = color.Gray{Y: 110}
		swatchObjects = append(swatchObjects,
			container.NewStack(swatchRect, newTappableArea(func(fyne.Position) {
				setBrushColor(cc)
			})))
	}
	swatchRow := container.NewGridWithColumns(len(presetColors), swatchObjects...)

	var eraserBtn *widget.Button // 前置声明（吸管回调里要引用）
	pickerBtn := widget.NewButtonWithIcon("吸管取色", theme.ColorChromaticIcon(), func() {
		eyedropperMode = true
		eraserMode = false
		eraserBtn.Importance = widget.MediumImportance
		eraserBtn.Refresh()
		infoLabel.SetText("吸管模式：在图上点一下，取该像素为画笔色")
	})
	eraserBtn = widget.NewButtonWithIcon("橡皮擦", theme.ContentClearIcon(), func() {
		eraserMode = !eraserMode
		eyedropperMode = false
		if eraserMode {
			eraserBtn.Importance = widget.HighImportance
			infoLabel.SetText("橡皮擦模式：拖动把涂过的区域恢复成原图")
		} else {
			eraserBtn.Importance = widget.MediumImportance
			infoLabel.SetText("已退出橡皮擦")
		}
		eraserBtn.Refresh()
	})
	eraserBtn.Importance = widget.MediumImportance

	// ===== 涂抹核心：图像像素 + 点阵图同步 =====
	applyPaint := func(cx, cy int) {
		if regionImg == nil || eyedropperMode {
			return
		}
		now := time.Now()
		if !strokeActive || now.Sub(lastPaintTime) > 800*time.Millisecond {
			// 新的一笔：拍快照（撤销粒度 = 一笔/一次落点）
			cp := *regionImg
			cp.Pix = append([]uint8(nil), regionImg.Pix...)
			undoStack = append(undoStack, &cp)
			if len(undoStack) > 5 {
				undoStack = undoStack[1:]
			}
			strokeActive = true
		}
		lastPaintTime = now
		var pts []image.Point
		if eraserMode && origImg != nil {
			pts = eraseCircleNRGBA(regionImg, origImg, cx, cy, brushSize)
		} else {
			pts = paintCircleNRGBA(regionImg, cx, cy, brushSize, brushColor)
		}
		dotSyncCells(dotImg, regionImg, pts)
		dotCanvasImg.Refresh()
		updateBgLabel()
	}

	strokePaint := func(x1, y1, x2, y2 int) {
		dx, dy := x2-x1, y2-y1
		dist := sqrtF(float64(dx*dx + dy*dy))
		step := brushSize / 2
		if step < 1 {
			step = 1
		}
		n := int(dist) / step
		if n > 512 {
			n = 512
		}
		for i := 0; i <= n; i++ {
			t := 0.0
			if n > 0 {
				t = float64(i) / float64(n)
			}
			applyPaint(x1+int(float64(dx)*t), y1+int(float64(dy)*t))
		}
	}

	// ===== 装图公共尾部（获取选区 / 右栏图片浏览共用）=====
	applyPaintImage := func(img *image.NRGBA) {
		regionImg = cloneToNRGBA(img)
		origImg = cloneToNRGBA(regionImg) // 橡皮擦的数据来源（进入时未涂抹的原始状态）
		undoStack = nil
		strokeActive = false
		brushColor = cornerBgColorNRGBA(regionImg)
		colorIndicator.FillColor = brushColor
		colorIndicator.Refresh()
		// 进入时自动补涂四角（异形贴到角落时四角检测才不会失效）
		b := regionImg.Bounds()
		paintCircleNRGBA(regionImg, b.Min.X, b.Min.Y, brushSize, brushColor)
		paintCircleNRGBA(regionImg, b.Max.X-1, b.Min.Y, brushSize, brushColor)
		paintCircleNRGBA(regionImg, b.Min.X, b.Max.Y-1, brushSize, brushColor)
		paintCircleNRGBA(regionImg, b.Max.X-1, b.Max.Y-1, brushSize, brushColor)

		dotImg = renderOriginalDotMatrix(regionImg)
		dotCanvasImg.Image = dotImg
		dotCanvasImg.SetMinSize(fyne.NewSize(float32(dotImg.Bounds().Dx()), float32(dotImg.Bounds().Dy())))
		dotCanvasImg.Refresh()
		updateBgLabel()
	}

	// ===== 获取选区：从主窗口拉框选区域（同智能取色的做法）=====
	getSelBtn := widget.NewButtonWithIcon("获取选区", theme.VisibilityIcon(), func() {
		if imageViewer == nil || imageViewer.image == nil {
			dialog.ShowInformation("提示", "主窗口没有图片，请先截图或载入", w)
			return
		}
		if len(imageViewer.markRects) == 0 {
			dialog.ShowInformation("提示", "请先在主窗口图像上拖拽框选模板区域，然后再点此按钮", w)
			return
		}
		rect := imageViewer.markRects[0]
		selRect := image.Rect(
			min(rect.X1, rect.X2), min(rect.Y1, rect.Y2),
			max(rect.X1, rect.X2), max(rect.Y1, rect.Y2),
		)
		applyPaintImage(cloneToNRGBA(cropImage(imageViewer.image, selRect)))
		b := regionImg.Bounds()
		infoLabel.SetText(fmt.Sprintf("选区: %d×%d px | 涂抹色已自动取四角色（四角已补涂），直接开涂",
			b.Dx(), b.Dy()))
	})
	getSelBtn.Importance = widget.HighImportance

	// ===== 撤销 / 恢复原图 =====
	undoBtn := widget.NewButtonWithIcon("撤销", theme.ContentUndoIcon(), func() {
		if len(undoStack) == 0 || regionImg == nil {
			return
		}
		regionImg = undoStack[len(undoStack)-1]
		undoStack = undoStack[:len(undoStack)-1]
		dotImg = renderOriginalDotMatrix(regionImg)
		dotCanvasImg.Image = dotImg
		dotCanvasImg.Refresh()
		updateBgLabel()
	})
	undoBtn.Importance = widget.MediumImportance

	restoreBtn := widget.NewButtonWithIcon("恢复原图", theme.MediaReplayIcon(), func() {
		if regionImg == nil {
			dialog.ShowInformation("提示", "还没有获取过选区", w)
			return
		}
		if origImg != nil {
			regionImg = cloneToNRGBA(origImg) // ★ 真恢复原图（之前误恢复成空白图）
		}
		undoStack = nil
		strokeActive = false
		dotImg = renderOriginalDotMatrix(regionImg)
		dotCanvasImg.Image = dotImg
		dotCanvasImg.Refresh()
		updateBgLabel()
	})
	restoreBtn.Importance = widget.MediumImportance

	// ===== 笔刷大小 =====
	brushSizeLabel := widget.NewLabel("笔刷: 3")
	brushSlider := widget.NewSlider(1, 40)
	brushSlider.SetValue(3)
	brushSlider.Step = 1
	brushSlider.OnChanged = func(f float64) {
		brushSize = int(f + 0.5)
		brushSizeLabel.SetText("笔刷: " + itoa(brushSize))
	}

	// ===== 点阵画布 + 覆盖层（坐标 = pos / stride）=====
	sp := newSpHoverArea(
		func(pos fyne.Position) { // onMouseMove：只更新放大镜
			stride := float32(dotCellSize + 1)
			ix, iy := int(pos.X/stride), int(pos.Y/stride)
			if regionImg != nil {
				b := regionImg.Bounds()
				if ix < 0 {
					ix = 0
				}
				if iy < 0 {
					iy = 0
				}
				if ix >= b.Dx() {
					ix = b.Dx() - 1
				}
				if iy >= b.Dy() {
					iy = b.Dy() - 1
				}
				magPanel.Update(regionImg, ix, iy)
			}
		},
		func(pos fyne.Position) { // onDrag：涂抹
			stride := float32(dotCellSize + 1)
			ix, iy := int(pos.X/stride), int(pos.Y/stride)
			if regionImg == nil {
				return
			}
			b := regionImg.Bounds()
			if ix < 0 || iy < 0 || ix >= b.Dx() || iy >= b.Dy() {
				return
			}
			// 与上一次落点间隔 >800ms ⇒ 视为新的一笔，不从旧位置连线
			if time.Since(lastPaintTime) > 800*time.Millisecond {
				lastPaintX, lastPaintY = -1, -1
			}
			if eyedropperMode {
				px := regionImg.NRGBAAt(ix, iy)
				setBrushColor(color.RGBA{px.R, px.G, px.B, 255})
				eyedropperMode = false
				infoLabel.SetText("已取色: #" + itoa2hex(px.R, px.G, px.B))
				return
			}
			if lastPaintX < 0 {
				applyPaint(ix, iy)
			} else {
				strokePaint(lastPaintX, lastPaintY, ix, iy)
			}
			lastPaintX, lastPaintY = ix, iy
			magPanel.Update(regionImg, ix, iy)
		},
		func(pos fyne.Position) { // onTap：单点涂抹 / 吸管
			stride := float32(dotCellSize + 1)
			ix, iy := int(pos.X/stride), int(pos.Y/stride)
			if regionImg == nil {
				return
			}
			b := regionImg.Bounds()
			if ix < 0 || iy < 0 || ix >= b.Dx() || iy >= b.Dy() {
				return
			}
			if eyedropperMode {
				px := regionImg.NRGBAAt(ix, iy)
				setBrushColor(color.RGBA{px.R, px.G, px.B, 255})
				eyedropperMode = false
				infoLabel.SetText("已取色: #" + itoa2hex(px.R, px.G, px.B))
				return
			}
			applyPaint(ix, iy)
			lastPaintX, lastPaintY = -1, -1 // 单点不参与后续连线
			magPanel.Update(regionImg, ix, iy)
		},
		func(key *fyne.KeyEvent) { // onKey
		},
	)

	// ★ noStretchLayout：把点阵图与吸管覆盖层都钉在 (0,0)、锁定为点阵图原始尺寸——
	//   窗口最大化时 Scroll/Stack 会把内容拉伸到视口大小，覆盖层坐标就会错位
	previewCanvas := container.New(&noStretchLayout{}, dotCanvasImg, sp)
	previewScroll := container.NewScroll(previewCanvas)

	// ===== 左栏底部：规则/背景检测描述（从右栏挪来，定义须在 leftPanel 之前）=====
	rightDesc := container.NewVBox(
		widget.NewLabel("规则: 四角同色判背景"),
		widget.NewLabel("背景占比 ≥30% 即透明"),
		bgLabel,
	)

	// ===== 左侧控制面板（照智能取色的固定宽度布局）=====
	leftPanel := container.New(&fixedWidthLayout{width: 155, padding: 10, verticalSpacing: 4},
		magPanel,
		getSelBtn,
		widget.NewSeparator(),
		eraserBtn, // ★ 橡皮擦放画笔（吸管/色板）上面
		container.NewHBox(colorIndicator, pickerBtn),
		swatchRow,
		widget.NewSeparator(),
		brushSizeLabel,
		brushSlider,
		widget.NewSeparator(),
		undoBtn,
		restoreBtn,
		// ★ 保存 PNG 挪到右栏「导入」旁边（弹窗填名字），左栏不再放
		rightDesc,
		layout.NewSpacer(),
	)

	// ===== 右侧面板：图片浏览（字库风格）——导入目录 → 列出全部图片 → 点击载入涂抹 =====
	var imgFiles []string // 当前目录下的图片绝对路径
	imgCountLabel := widget.NewLabelWithStyle("图片列表 (0)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	// ★ 当前目录小字（导入后显示；保存直接存这里）
	imgDirLabel := canvas.NewText("", color.NRGBA{130, 130, 140, 255})
	imgDirLabel.TextSize = 9
	var imgList *widget.List
	refreshImgHeader := func() {
		imgCountLabel.SetText(fmt.Sprintf("图片列表 (%d)", len(imgFiles)))
	}
	loadPaintFile := func(fp string) {
		data, err := os.ReadFile(fp)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			dialog.ShowError(fmt.Errorf("解码失败 %s: %v", filepath.Base(fp), err), w)
			return
		}
		applyPaintImage(convertToNRGBA(img))
		infoLabel.SetText(fmt.Sprintf("已载入: %s (%d×%d)", filepath.Base(fp),
			regionImg.Bounds().Dx(), regionImg.Bounds().Dy()))
	}
	// ★ 选文件夹 = 主界面同款 fyne 文件夹对话框（ShowFolderOpen）；选中后列出该目录全部图片
	var scanImgDir func(dir string) // 前向声明（保存回调等处也用）
	scanImgDir = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		imgFiles = imgFiles[:0]
		for _, n := range names {
			imgFiles = append(imgFiles, filepath.Join(dir, n))
		}
		imgDir = dir
		saveToolPaths(func(p *toolPaths) { p.PaintImgDir = dir }) // ★ 持久化
		imgDirLabel.Text = dir
		if run := []rune(imgDirLabel.Text); len(run) > 30 {
			imgDirLabel.Text = string(run[:14]) + "…" + string(run[len(run)-15:])
		}
		imgDirLabel.Refresh()
		refreshImgHeader()
		imgList.Refresh()
	}
	imgImportBtn := widget.NewButtonWithIcon("导入", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if lu == nil {
				return
			}
			scanImgDir(lu.Path())
		}, w)
	})
	imgList = widget.NewList(
		func() int { return len(imgFiles) },
		func() fyne.CanvasObject {
			t := canvas.NewText("", getTextColor(isDarkTheme))
			t.TextSize = 11
			delBtn := widget.NewButtonWithIcon("", theme.ContentClearIcon(), nil)
			return container.NewBorder(nil, nil, nil, delBtn, t)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(imgFiles) {
				return
			}
			box := item.(*fyne.Container)
			var t *canvas.Text
			var delBtn *widget.Button
			for _, o := range box.Objects { // ⛔ 按类型取，不按下标（Border 的 Objects 顺序脆弱）
				switch v := o.(type) {
				case *canvas.Text:
					t = v
				case *widget.Button:
					delBtn = v
				}
			}
			if t == nil || delBtn == nil {
				return
			}
			t.Text = filepath.Base(imgFiles[id])
			t.Color = getTextColor(isDarkTheme)
			t.Refresh()
			delBtn.OnTapped = func() {
				if id >= len(imgFiles) {
					return
				}
				fp := imgFiles[id]
				dialog.ShowConfirm("删除图片", fmt.Sprintf("确定删除 %s ？\n（同时从磁盘删除该文件）", filepath.Base(fp)), func(ok bool) {
					if !ok {
						return
					}
					if err := os.Remove(fp); err != nil {
						dialog.ShowError(err, w)
						return
					}
					imgFiles = append(imgFiles[:id], imgFiles[id+1:]...)
					refreshImgHeader()
					imgList.Refresh()
				}, w)
			}
		},
	)
	imgList.OnSelected = func(id widget.ListItemID) {
		if id < len(imgFiles) {
			loadPaintFile(imgFiles[id])
		}
	}
	// ★ 启动恢复：配置里有目录就自动列出该目录的图片
	if imgDir != "" {
		if st, err := os.Stat(imgDir); err == nil && st.IsDir() {
			scanImgDir(imgDir)
		}
	}

	// ★ 保存 PNG（与导入并排在底部）：弹窗填名字 → 存进当前图片目录 → 刷新列表
	saveBtn := widget.NewButtonWithIcon("保存", theme.DocumentSaveIcon(), func() {
		if regionImg == nil {
			dialog.ShowInformation("提示", "当前没有可保存的图像", w)
			return
		}
		if imgDir == "" {
			dialog.ShowInformation("提示", "还没有图片路径 —— 请先点「导入」选择图片文件夹", w)
			return
		}
		nameEntry := widget.NewEntry()
		nameEntry.SetText(fmt.Sprintf("template_%s", time.Now().Format("20060102_150405")))
		d := dialog.NewForm("保存 PNG", "保存", "取消",
			[]*widget.FormItem{{Text: "文件名", Widget: nameEntry}},
			func(ok bool) {
				if !ok {
					return
				}
				name := strings.TrimSpace(nameEntry.Text)
				if name == "" {
					name = fmt.Sprintf("template_%s", time.Now().Format("20060102_150405"))
				}
				if !strings.HasSuffix(strings.ToLower(name), ".png") {
					name += ".png"
				}
				filePath := filepath.Join(imgDir, name)
				file, err := os.Create(filePath)
				if err != nil {
					dialog.ShowError(fmt.Errorf("创建文件失败: %v", err), w)
					return
				}
				if err := png.Encode(file, regionImg); err != nil {
					file.Close()
					dialog.ShowError(fmt.Errorf("保存失败: %v", err), w)
					return
				}
				file.Close()
				scanImgDir(imgDir) // 新文件进列表
				dialog.ShowInformation("已保存", filePath, w)
			}, w)
		d.Resize(fyne.NewSize(430, 160)) // ★ 加宽：长文件名完整可见（默认尺寸挤成一条缝）
		d.Show()
	})

	rightBg := canvas.NewRectangle(color.Transparent)
	rightBg.SetMinSize(fyne.NewSize(240, 0))
	// ★ 字库式三段：标题（上）/ 列表（中）/ 导入+保存 并排（最下）
	rightPanel := container.NewStack(rightBg, container.NewPadded(
		container.NewBorder(
			container.NewVBox(imgCountLabel, imgDirLabel),
			container.NewVBox(widget.NewSeparator(),
				container.NewGridWithColumns(2, imgImportBtn, saveBtn)),
			nil, nil,
			imgList,
		)))

	mainContent := container.NewBorder(nil, nil, leftPanel, rightPanel,
		container.NewBorder(infoLabel, nil, nil, nil, previewScroll))
	w.SetContent(mainContent)
	w.Show()
}

// itoa / itoa2hex：小工具
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func itoa2hex(r, g, b uint8) string {
	const hexd = "0123456789ABCDEF"
	return string([]byte{
		hexd[r>>4], hexd[r&0xF],
		hexd[g>>4], hexd[g&0xF],
		hexd[b>>4], hexd[b&0xF],
	})
}

// noStretchLayout：所有子对象锁定为「最大 MinSize」并钉在 (0,0)，绝不随容器拉伸。
// （普通 Stack 会把子对象撑满容器 ⇒ 窗口最大化时覆盖层与图像错位）
type noStretchLayout struct{}

func (l *noStretchLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, o := range objects {
		s := o.MinSize()
		if s.Width > w {
			w = s.Width
		}
		if s.Height > h {
			h = s.Height
		}
	}
	return fyne.NewSize(w, h)
}

func (l *noStretchLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	ms := l.MinSize(objects)
	w, h := ms.Width, ms.Height
	for _, o := range objects {
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos(0, 0))
	}
}
