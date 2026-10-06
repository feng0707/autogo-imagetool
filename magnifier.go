package main

import (
	"fmt"
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// 放大镜组件
type MagnifierWidget struct {
	widget.BaseWidget
	sourceImage   image.Image
	gridImage     *image.NRGBA // 使用NRGBA而不是RGBA
	gridRaster    *canvas.Raster
	gridSize      int
	cellSize      int
	infoText      *canvas.Text
	background    *canvas.Rectangle
	visible       bool
	mouseX        int     // 鼠标在图像中的X坐标（用于取色）
	mouseY        int     // 鼠标在图像中的Y坐标（用于取色）
	cursorX       float32 // 鼠标在可见区域中的X坐标（用于定位放大镜）
	cursorY       float32 // 鼠标在可见区域中的Y坐标（用于定位放大镜）
	containerSize fyne.Size
}

// 创建新的放大镜组件
func NewMagnifierWidget() *MagnifierWidget {
	m := &MagnifierWidget{
		gridSize: 15,
		cellSize: 15,
		visible:  false,
	}

	// 创建背景
	m.background = canvas.NewRectangle(color.NRGBA{40, 40, 40, 230})
	m.background.StrokeWidth = 2
	m.background.StrokeColor = color.NRGBA{200, 200, 200, 255}

	// 创建用于绘制网格的图像
	gridPixelSize := m.gridSize * m.cellSize
	m.gridImage = image.NewNRGBA(image.Rect(0, 0, gridPixelSize, gridPixelSize)) // 使用NRGBA而不是RGBA

	// 创建Raster来显示网格
	m.gridRaster = canvas.NewRaster(func(w, h int) image.Image {
		return m.gridImage
	})
	m.gridRaster.ScaleMode = canvas.ImageScalePixels

	// 创建信息文本
	m.infoText = canvas.NewText("X:0 Y:0 RGB:#000000", color.White)
	m.infoText.TextSize = 12
	m.infoText.Alignment = fyne.TextAlignCenter

	// 初始化时隐藏所有元素
	m.background.Hide()
	m.gridRaster.Hide()
	m.infoText.Hide()

	m.ExtendBaseWidget(m)
	return m
}

// 快速获取像素颜色（直接访问Pix数组，图像始终是NRGBA）
func getPixelColorFast(img image.Image, x, y int) (r, g, b uint8) {
	bounds := img.Bounds()
	if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
		return 0, 0, 0 // 超出范围返回黑色
	}

	// 图像始终是NRGBA
	imgTyped := img.(*image.NRGBA)
	idx := (y-bounds.Min.Y)*imgTyped.Stride + (x-bounds.Min.X)*4
	return imgTyped.Pix[idx], imgTyped.Pix[idx+1], imgTyped.Pix[idx+2]
}

// 更新放大镜显示 - 完全复刻参考代码的逻辑
func (m *MagnifierWidget) Update(img image.Image, imageX, imageY int, cursorX, cursorY float32) {
	if img == nil {
		return
	}

	m.sourceImage = img
	m.mouseX = imageX
	m.mouseY = imageY
	m.cursorX = cursorX
	m.cursorY = cursorY
	m.visible = true

	halfGrid := m.gridSize / 2

	// 获取中心点颜色并更新信息文本
	r8, g8, b8 := getPixelColorFast(img, imageX, imageY)
	colorStr := fmt.Sprintf("#%02X%02X%02X", r8, g8, b8)
	// 添加十进制RGB值显示
	m.infoText.Text = fmt.Sprintf("X:%d Y:%d RGB:%s (%d,%d,%d)", imageX, imageY, colorStr, r8, g8, b8)

	// 绘制网格到图像
	borderColor := color.RGBA{80, 80, 80, 255}

	for y := 0; y < m.gridSize; y++ {
		for x := 0; x < m.gridSize; x++ {
			pixelX := imageX - halfGrid + x
			pixelY := imageY - halfGrid + y

			// 快速获取像素颜色
			pr, pg, pb := getPixelColorFast(img, pixelX, pixelY)
			pixelColor := color.RGBA{pr, pg, pb, 255}

			// 填充格子区域
			for dy := 0; dy < m.cellSize; dy++ {
				for dx := 0; dx < m.cellSize; dx++ {
					imgX := x*m.cellSize + dx
					imgY := y*m.cellSize + dy
					gridIdx := imgY*m.gridImage.Stride + imgX*4

					// 绘制格子边框
					if dx == 0 || dy == 0 {
						m.gridImage.Pix[gridIdx] = borderColor.R
						m.gridImage.Pix[gridIdx+1] = borderColor.G
						m.gridImage.Pix[gridIdx+2] = borderColor.B
						m.gridImage.Pix[gridIdx+3] = borderColor.A
					} else {
						m.gridImage.Pix[gridIdx] = pixelColor.R
						m.gridImage.Pix[gridIdx+1] = pixelColor.G
						m.gridImage.Pix[gridIdx+2] = pixelColor.B
						m.gridImage.Pix[gridIdx+3] = pixelColor.A
					}
				}
			}

			// 中心位置添加特殊标识
			if x == halfGrid && y == halfGrid {
				// 使用中心像素的反色作为边框颜色
				centerColor := color.RGBA{pr, pg, pb, 255}
				inverseColor := getInverseColor(centerColor)
				r, g, b, _ := inverseColor.RGBA()
				borderR, borderG, borderB := uint8(r>>8), uint8(g>>8), uint8(b>>8)

				// 绘制加粗的边框
				for i := 0; i < m.cellSize; i++ {
					imgX := x * m.cellSize
					imgY := y * m.cellSize

					// 顶部和底部
					for _, dy := range []int{0, 1, m.cellSize - 2, m.cellSize - 1} {
						idx := (imgY+dy)*m.gridImage.Stride + (imgX+i)*4
						m.gridImage.Pix[idx] = borderR
						m.gridImage.Pix[idx+1] = borderG
						m.gridImage.Pix[idx+2] = borderB
						m.gridImage.Pix[idx+3] = 255
					}

					// 左侧和右侧
					for _, dx := range []int{0, 1, m.cellSize - 2, m.cellSize - 1} {
						idx := (imgY+i)*m.gridImage.Stride + (imgX+dx)*4
						m.gridImage.Pix[idx] = borderR
						m.gridImage.Pix[idx+1] = borderG
						m.gridImage.Pix[idx+2] = borderB
						m.gridImage.Pix[idx+3] = 255
					}
				}
			}
		}
	}

	m.Refresh()
}

// 隐藏放大镜
func (m *MagnifierWidget) Hide() {
	m.visible = false
	m.Refresh()
}

// 显示放大镜
func (m *MagnifierWidget) Show() {
	m.visible = true
	m.Refresh()
}

// 创建渲染器
func (m *MagnifierWidget) CreateRenderer() fyne.WidgetRenderer {
	return &magnifierRenderer{
		magnifier: m,
	}
}

// 放大镜渲染器
type magnifierRenderer struct {
	magnifier *MagnifierWidget
}

func (r *magnifierRenderer) MinSize() fyne.Size {
	// 不占用空间，因为是浮动在上层的
	return fyne.NewSize(0, 0)
}

func (r *magnifierRenderer) Layout(size fyne.Size) {
	// 保存容器尺寸
	r.magnifier.containerSize = size

	if !r.magnifier.visible {
		return
	}

	// 计算放大镜的尺寸
	gridWidth := float32(r.magnifier.gridSize * r.magnifier.cellSize)
	gridHeight := float32(r.magnifier.gridSize * r.magnifier.cellSize)
	infoHeight := float32(20)
	totalWidth := gridWidth + 10
	totalHeight := gridHeight + infoHeight + 15

	// 完全复刻参考代码的逻辑：放大镜位置 = 鼠标位置 + 偏移(20, 20)
	offsetX := float32(20)
	offsetY := float32(20)

	posX := r.magnifier.cursorX + offsetX
	posY := r.magnifier.cursorY + offsetY

	// 边界检查：如果会超出右边界，放到鼠标左边
	if posX+totalWidth > size.Width {
		posX = r.magnifier.cursorX - totalWidth - offsetX
	}

	// 边界检查：如果会超出下边界，放到鼠标上边
	if posY+totalHeight > size.Height {
		posY = r.magnifier.cursorY - totalHeight - offsetY
	}

	// 最终边界保护
	if posX < 5 {
		posX = 5
	}
	if posY < 5 {
		posY = 5
	}
	if posX+totalWidth > size.Width-5 {
		posX = size.Width - totalWidth - 5
	}
	if posY+totalHeight > size.Height-5 {
		posY = size.Height - totalHeight - 5
	}

	// 布局背景
	r.magnifier.background.Move(fyne.NewPos(posX, posY))
	r.magnifier.background.Resize(fyne.NewSize(totalWidth, totalHeight))

	// 布局网格（现在在顶部）
	gridX := posX + 5
	gridY := posY + 5
	r.magnifier.gridRaster.Move(fyne.NewPos(gridX, gridY))
	r.magnifier.gridRaster.Resize(fyne.NewSize(gridWidth, gridHeight))

	// 布局信息文本（现在在底部）
	infoY := posY + gridHeight + 10
	r.magnifier.infoText.Move(fyne.NewPos(posX+5, infoY))
	r.magnifier.infoText.Resize(fyne.NewSize(totalWidth-10, infoHeight))
}

func (r *magnifierRenderer) Refresh() {
	if !r.magnifier.visible {
		r.magnifier.background.Hide()
		r.magnifier.infoText.Hide()
		r.magnifier.gridRaster.Hide()
	} else {
		// 每次刷新时重新计算位置（跟随鼠标）
		if r.magnifier.containerSize.Width > 0 && r.magnifier.containerSize.Height > 0 {
			r.Layout(r.magnifier.containerSize)
		}

		r.magnifier.background.Show()
		r.magnifier.infoText.Show()
		r.magnifier.gridRaster.Show()
		r.magnifier.gridRaster.Refresh()
		r.magnifier.infoText.Refresh()
		r.magnifier.background.Refresh()
	}
}

func (r *magnifierRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.magnifier.background, r.magnifier.infoText, r.magnifier.gridRaster}
}

func (r *magnifierRenderer) Destroy() {}

// PickMagnifierPanel: 智能取色弹窗专用固定放大镜，钉在右侧不跟随鼠标
type PickMagnifierPanel struct {
	widget.BaseWidget
	srcImage    image.Image
	gridImg     *image.NRGBA
	raster      *canvas.Raster
	infoText    *canvas.Text
	bg          *canvas.Rectangle
	gridSize    int // 显示区域格数（如15表示15x15）
	cellSize    int // 每格像素大小（如20表示每格20x20像素）
	curX        int
	curY        int
	showInfo    bool          // 底部坐标/颜色信息行（紧凑正方形版关闭）
	focusTarget fyne.Focusable // 点击面板时回焦到这个对象
}

func NewPickMagnifierPanel() *PickMagnifierPanel {
	p := &PickMagnifierPanel{
		gridSize: 15,
		cellSize: 20,
		curX:     0,
		curY:     0,
		showInfo: true,
	}
	p.bg = canvas.NewRectangle(color.NRGBA{40, 40, 40, 230})
	p.bg.StrokeWidth = 2
	p.bg.StrokeColor = color.NRGBA{200, 200, 200, 255}

	gridPx := p.gridSize * p.cellSize
	p.gridImg = image.NewNRGBA(image.Rect(0, 0, gridPx, gridPx))
	p.raster = canvas.NewRaster(func(w, h int) image.Image { return p.gridImg })
	p.raster.ScaleMode = canvas.ImageScalePixels

	p.infoText = canvas.NewText("X:0 Y:0 RGB:#000000", color.White)
	p.infoText.TextSize = 12
	p.infoText.Alignment = fyne.TextAlignCenter

	p.ExtendBaseWidget(p)
	return p
}

// NewPickMagnifierPanelCompact 紧凑**正方形**放大镜（嵌入工具窗口左栏，无信息行）。
// 总尺寸 = gridSize*cellSize；左栏内容宽 155-2*10=135 ⇒ NewPickMagnifierPanelCompact(15, 9)。
func NewPickMagnifierPanelCompact(gridSize, cellSize int) *PickMagnifierPanel {
	p := NewPickMagnifierPanel()
	p.gridSize = gridSize
	p.cellSize = cellSize
	p.showInfo = false
	gridPx := gridSize * cellSize
	p.gridImg = image.NewNRGBA(image.Rect(0, 0, gridPx, gridPx))
	p.infoText.Hide() // 紧凑版不放信息行
	return p
}

func (p *PickMagnifierPanel) Update(img image.Image, cx, cy int) {
	if img == nil {
		return
	}
	p.srcImage = img
	p.curX = cx
	p.curY = cy

	half := p.gridSize / 2
	borderColor := color.RGBA{80, 80, 80, 255}

	for gy := 0; gy < p.gridSize; gy++ {
		for gx := 0; gx < p.gridSize; gx++ {
			px := cx - half + gx
			py := cy - half + gy
			pr, pg, pb := getPixelColorFast(img, px, py)
			pixelColor := color.RGBA{pr, pg, pb, 255}

			for dy := 0; dy < p.cellSize; dy++ {
				for dx := 0; dx < p.cellSize; dx++ {
					imgX := gx*p.cellSize + dx
					imgY := gy*p.cellSize + dy
					idx := imgY*p.gridImg.Stride + imgX*4
					if dx == 0 || dy == 0 {
						p.gridImg.Pix[idx] = borderColor.R
						p.gridImg.Pix[idx+1] = borderColor.G
						p.gridImg.Pix[idx+2] = borderColor.B
						p.gridImg.Pix[idx+3] = borderColor.A
					} else {
						p.gridImg.Pix[idx] = pixelColor.R
						p.gridImg.Pix[idx+1] = pixelColor.G
						p.gridImg.Pix[idx+2] = pixelColor.B
						p.gridImg.Pix[idx+3] = pixelColor.A
					}
				}
			}

			// 中心格加粗边框
			if gx == half && gy == half {
				inv := getInverseColor(pixelColor)
				r, g, b, _ := inv.RGBA()
				br, bg2, bb := uint8(r>>8), uint8(g>>8), uint8(b>>8)
				for i := 0; i < p.cellSize; i++ {
					baseX := gx * p.cellSize
					baseY := gy * p.cellSize
					for _, off := range []int{0, 1, p.cellSize - 2, p.cellSize - 1} {
						idx1 := (baseY+off)*p.gridImg.Stride + (baseX+i)*4
						p.gridImg.Pix[idx1] = br
						p.gridImg.Pix[idx1+1] = bg2
						p.gridImg.Pix[idx1+2] = bb
						p.gridImg.Pix[idx1+3] = 255
						idx2 := (baseY+i)*p.gridImg.Stride + (baseX+off)*4
						p.gridImg.Pix[idx2] = br
						p.gridImg.Pix[idx2+1] = bg2
						p.gridImg.Pix[idx2+2] = bb
						p.gridImg.Pix[idx2+3] = 255
					}
				}
			}
		}
	}

	r8, g8, b8 := getPixelColorFast(img, cx, cy)
	p.infoText.Text = fmt.Sprintf("X:%d Y:%d RGB:#%02X%02X%02X (%d,%d,%d)", cx, cy, r8, g8, b8, r8, g8, b8)
	p.Refresh()
}

// 点击面板时把焦点还给 hoverArea，保证方向键可用
func (p *PickMagnifierPanel) Tapped(*fyne.PointEvent) {
	if p.focusTarget != nil {
		if c := fyne.CurrentApp().Driver().CanvasForObject(p); c != nil {
			c.Focus(p.focusTarget)
		}
	}
}

func (p *PickMagnifierPanel) CreateRenderer() fyne.WidgetRenderer {
	return &pickMagRenderer{panel: p}
}

type pickMagRenderer struct {
	panel *PickMagnifierPanel
}

func (r *pickMagRenderer) MinSize() fyne.Size {
	gridW := float32(r.panel.gridSize * r.panel.cellSize)
	gridH := float32(r.panel.gridSize * r.panel.cellSize)
	if !r.panel.showInfo {
		return fyne.NewSize(gridW, gridH) // 紧凑版：纯正方形
	}
	return fyne.NewSize(gridW+10, gridH+30)
}

func (r *pickMagRenderer) Layout(size fyne.Size) {
	gridW := float32(r.panel.gridSize * r.panel.cellSize)
	gridH := float32(r.panel.gridSize * r.panel.cellSize)

	if !r.panel.showInfo {
		r.panel.bg.Resize(fyne.NewSize(gridW, gridH))
		r.panel.raster.Move(fyne.NewPos(0, 0))
		r.panel.raster.Resize(fyne.NewSize(gridW, gridH))
		return
	}

	infoH := float32(20)
	totalW := gridW + 10
	totalH := gridH + infoH + 15

	r.panel.bg.Resize(fyne.NewSize(totalW, totalH))

	r.panel.raster.Move(fyne.NewPos(5, 5))
	r.panel.raster.Resize(fyne.NewSize(gridW, gridH))

	r.panel.infoText.Move(fyne.NewPos(5, gridH+10))
	r.panel.infoText.Resize(fyne.NewSize(totalW-10, infoH))
}

func (r *pickMagRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.panel.bg, r.panel.raster, r.panel.infoText}
}

func (r *pickMagRenderer) Refresh() {
	if r.panel.Size().Width > 0 && r.panel.Size().Height > 0 {
		r.Layout(r.panel.Size())
	}
	r.panel.raster.Refresh()
	r.panel.infoText.Refresh()
	r.panel.bg.Refresh()
}

func (r *pickMagRenderer) Destroy() {}

// pickDotGrid: 智能取色弹窗专用点阵图组件，完全独立实现
type pickDotGrid struct {
	widget.BaseWidget
	srcImage image.Image
	scale    float32
	imgX     int
	imgY     int
	hasPos   bool
	panel    *PickMagnifierPanel
	dotImg   *image.NRGBA // 渲染后的点阵图
	onEnter  func()       // 回车键回调
}

func newPickDotGrid(src image.Image, scale float32, panel *PickMagnifierPanel) *pickDotGrid {
	p := &pickDotGrid{
		srcImage: src,
		scale:    scale,
		imgX:     src.Bounds().Dx() / 2,
		imgY:     src.Bounds().Dy() / 2,
		hasPos:   true,
		panel:    panel,
	}
	p.ExtendBaseWidget(p)
	return p
}

func (p *pickDotGrid) SetImage(img *image.NRGBA) {
	p.dotImg = img
	p.Refresh()
}

func (p *pickDotGrid) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.NRGBA{30, 30, 30, 255})
	raster := canvas.NewRaster(func(w, h int) image.Image {
		if p.dotImg != nil {
			return p.dotImg
		}
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	})
	raster.ScaleMode = canvas.ImageScalePixels
	return &pickDotGridRenderer{grid: p, bg: bg, raster: raster}
}

type pickDotGridRenderer struct {
	grid   *pickDotGrid
	bg     *canvas.Rectangle
	raster *canvas.Raster
}

func (r *pickDotGridRenderer) MinSize() fyne.Size {
	return r.grid.MinSize()
}

func (r *pickDotGridRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.raster.Resize(size)
}

func (r *pickDotGridRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.raster}
}

func (r *pickDotGridRenderer) Refresh() {
	r.raster.Refresh()
}

func (r *pickDotGridRenderer) Destroy() {}

func (p *pickDotGrid) MinSize() fyne.Size {
	if p.dotImg != nil {
		b := p.dotImg.Bounds()
		return fyne.NewSize(float32(b.Dx()), float32(b.Dy()))
	}
	return fyne.NewSize(100, 100)
}

func (p *pickDotGrid) updatePanel() {
	if p.panel != nil && p.srcImage != nil {
		p.panel.Update(p.srcImage, p.imgX, p.imgY)
	}
}

func (p *pickDotGrid) setPosFromMouse(pos fyne.Position) {
	bounds := p.srcImage.Bounds()
	// 用点阵图尺寸换算，不依赖 widget 实际渲染尺寸
	if p.dotImg != nil {
		dotW := float32(p.dotImg.Bounds().Dx())
		dotH := float32(p.dotImg.Bounds().Dy())
		if dotW > 0 && dotH > 0 {
			p.imgX = int(float32(pos.X) / dotW * float32(bounds.Dx()))
			p.imgY = int(float32(pos.Y) / dotH * float32(bounds.Dy()))
		}
	} else if p.scale > 0 {
		p.imgX = int(pos.X / p.scale)
		p.imgY = int(pos.Y / p.scale)
	}
	if p.imgX < bounds.Min.X {
		p.imgX = bounds.Min.X
	}
	if p.imgY < bounds.Min.Y {
		p.imgY = bounds.Min.Y
	}
	if p.imgX >= bounds.Max.X {
		p.imgX = bounds.Max.X - 1
	}
	if p.imgY >= bounds.Max.Y {
		p.imgY = bounds.Max.Y - 1
	}
	p.hasPos = true
	p.updatePanel()
}

func (p *pickDotGrid) MouseIn(e *desktop.MouseEvent)    { p.setPosFromMouse(e.Position) }
func (p *pickDotGrid) MouseMoved(e *desktop.MouseEvent) { p.setPosFromMouse(e.Position) }
func (p *pickDotGrid) MouseOut()                        {}
func (p *pickDotGrid) Dragged(e *fyne.DragEvent)        { p.setPosFromMouse(e.Position) }
func (p *pickDotGrid) DragEnd()                         {}

// 点击时设置位置（ModalPopUp 里 Dragged 比 MouseMoved 可靠）
func (p *pickDotGrid) Tapped(e *fyne.PointEvent) {
	p.setPosFromMouse(e.Position)
	if c := fyne.CurrentApp().Driver().CanvasForObject(p); c != nil {
		c.Focus(p)
	}
}

func (p *pickDotGrid) FocusGained()     {}
func (p *pickDotGrid) FocusLost()       {}
func (p *pickDotGrid) TypedRune(r rune) {}

func (p *pickDotGrid) TypedKey(key *fyne.KeyEvent) {
	bounds := p.srcImage.Bounds()
	if !p.hasPos {
		p.imgX = (bounds.Min.X + bounds.Max.X) / 2
		p.imgY = (bounds.Min.Y + bounds.Max.Y) / 2
		p.hasPos = true
	}
	switch key.Name {
	case fyne.KeyUp:
		p.imgY--
	case fyne.KeyDown:
		p.imgY++
	case fyne.KeyLeft:
		p.imgX--
	case fyne.KeyRight:
		p.imgX++
	case fyne.KeyReturn:
		if p.onEnter != nil {
			p.onEnter()
		}
	default:
		return
	}
	if p.imgX < bounds.Min.X {
		p.imgX = bounds.Min.X
	}
	if p.imgY < bounds.Min.Y {
		p.imgY = bounds.Min.Y
	}
	if p.imgX >= bounds.Max.X {
		p.imgX = bounds.Max.X - 1
	}
	if p.imgY >= bounds.Max.Y {
		p.imgY = bounds.Max.Y - 1
	}
	p.updatePanel()
}

// 放大镜悬停区域：鼠标悬停驱动放大镜，支持方向键逐像素移动
type magnifierHoverArea struct {
	widget.BaseWidget
	content    fyne.CanvasObject
	magnifier  *MagnifierWidget
	srcImage   image.Image // 原始图像（NRGBA）
	scale      float32     // 显示尺寸 = 原始尺寸 * scale
	imgX, imgY int         // 当前像素坐标（原始图像空间）
	hasPos     bool
}

func newMagnifierHoverArea(content fyne.CanvasObject, magnifier *MagnifierWidget, src image.Image, scale float32) *magnifierHoverArea {
	h := &magnifierHoverArea{content: content, magnifier: magnifier, srcImage: src, scale: scale}
	h.ExtendBaseWidget(h)
	return h
}

func (h *magnifierHoverArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(h.content)
}

// 用当前 imgX/imgY 更新放大镜（光标显示位置按 scale 换算）
func (h *magnifierHoverArea) updateMagnifier() {
	h.magnifier.Update(h.srcImage, h.imgX, h.imgY, float32(h.imgX)*h.scale, float32(h.imgY)*h.scale)
}

// 把鼠标显示坐标换算成原始图像坐标并更新放大镜
func (h *magnifierHoverArea) setPosFromMouse(pos fyne.Position) {
	bounds := h.srcImage.Bounds()
	h.imgX = int(pos.X / h.scale)
	h.imgY = int(pos.Y / h.scale)
	if h.imgX < bounds.Min.X {
		h.imgX = bounds.Min.X
	}
	if h.imgY < bounds.Min.Y {
		h.imgY = bounds.Min.Y
	}
	if h.imgX >= bounds.Max.X {
		h.imgX = bounds.Max.X - 1
	}
	if h.imgY >= bounds.Max.Y {
		h.imgY = bounds.Max.Y - 1
	}
	h.hasPos = true
	h.updateMagnifier()
}

func (h *magnifierHoverArea) MouseIn(e *desktop.MouseEvent) {
	h.setPosFromMouse(e.Position)
}

func (h *magnifierHoverArea) MouseMoved(e *desktop.MouseEvent) {
	h.setPosFromMouse(e.Position)
}

func (h *magnifierHoverArea) MouseOut() {
	h.magnifier.Hide()
}

// Dragged: ModalPopUp 下 MouseMoved 可能被拦截，用拖拽事件作为备用通道
func (h *magnifierHoverArea) Dragged(e *fyne.DragEvent) {
	h.setPosFromMouse(e.Position)
}

func (h *magnifierHoverArea) DragEnd() {}

// 点击重新获取焦点，保证方向键可用
func (h *magnifierHoverArea) Tapped(*fyne.PointEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(h); c != nil {
		c.Focus(h)
	}
}

// Focusable 接口
func (h *magnifierHoverArea) FocusGained()     {}
func (h *magnifierHoverArea) FocusLost()       {}
func (h *magnifierHoverArea) TypedRune(r rune) {}

// 方向键逐像素移动放大镜
func (h *magnifierHoverArea) TypedKey(key *fyne.KeyEvent) {
	bounds := h.srcImage.Bounds()
	// 还没有位置时从图像中心开始
	if !h.hasPos {
		h.imgX = (bounds.Min.X + bounds.Max.X) / 2
		h.imgY = (bounds.Min.Y + bounds.Max.Y) / 2
		h.hasPos = true
	}

	switch key.Name {
	case fyne.KeyUp:
		h.imgY--
	case fyne.KeyDown:
		h.imgY++
	case fyne.KeyLeft:
		h.imgX--
	case fyne.KeyRight:
		h.imgX++
	default:
		return
	}

	// 限制在图像范围内
	if h.imgX < bounds.Min.X {
		h.imgX = bounds.Min.X
	}
	if h.imgY < bounds.Min.Y {
		h.imgY = bounds.Min.Y
	}
	if h.imgX >= bounds.Max.X {
		h.imgX = bounds.Max.X - 1
	}
	if h.imgY >= bounds.Max.Y {
		h.imgY = bounds.Max.Y - 1
	}
	h.updateMagnifier()
}
