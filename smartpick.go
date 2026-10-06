package main

import (
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
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
)

// ==================== 智能取色窗口（照搬字库制作窗口） ====================

// ★ 单实例：重复点按钮只聚焦已开的窗口（用户要求保持一个）
var smartPickWin fyne.Window

func openSmartPickWindow(parentWindow fyne.Window) {
	if smartPickWin != nil {
		smartPickWin.RequestFocus()
		return
	}
	a := fyne.CurrentApp()
	w := a.NewWindow("AutoGo 智能取色")
	smartPickWin = w
	w.SetOnClosed(func() { smartPickWin = nil })
	w.Resize(fyne.NewSize(1200, 700))
	w.CenterOnScreen()

	var charCells []CharCell
	var charNameEntries []*widget.Entry
	var charHexCache []string
	var charWpCache []int
	var charMatchedLib []bool
	var binaryRegion *image.NRGBA
	var regionImg image.Image
	var fontLibChars []FontChar
	showingOriginal := false
	var hoverImgX, hoverImgY int
	var hoverHasPos bool
	ignoreMouseHover := false // 方向键移动后短暂屏蔽鼠标事件
	var spHover *spHoverArea  // 前向声明

	// ===== 放大镜 + 取色输入（智能取色新增）=====
	// ★ 紧凑正方形版，放左栏「获取选区」上面（原右侧大面板已移除）
	magPanel := NewPickMagnifierPanelCompact(15, 9)
	spColorEntry := widget.NewEntry()
	spColorEntry.SetPlaceHolder("取色颜色值，回车填入 | 空格添加到主窗口")

	updateSpMag := func() {
		var src image.Image = regionImg
		if binaryRegion != nil {
			src = binaryRegion
		}
		if src != nil && hoverHasPos {
			magPanel.Update(src, hoverImgX, hoverImgY)
		}
	}

	fgColorEntry := widget.NewEntry()
	fgColorEntry.SetText("000000-101010")
	fgColorEntry.SetPlaceHolder("如: 000000-101010")

	// spColorEntry回车时把颜色填入前景色输入框
	spColorEntry.OnSubmitted = func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		cur := fgColorEntry.Text
		if idx := strings.Index(cur, "-"); idx >= 0 {
			fgColorEntry.SetText(s + cur[idx:])
		} else {
			fgColorEntry.SetText(s)
		}
		// 回车后把焦点还给spHover，继续方向键操作
		if spHover != nil {
			w.Canvas().Focus(spHover)
		}
	}

	var offset15Check, offset32Check, offset40Check, offset60Check *widget.Check
	offset15Check = widget.NewCheck("偏移15", nil)
	offset32Check = widget.NewCheck("偏移32", nil)
	offset40Check = widget.NewCheck("偏移40", nil)
	offset60Check = widget.NewCheck("偏移60", nil)

	// 偏移值对应的容差
	offsetTolerance := map[*widget.Check]string{
		offset15Check: "151515",
		offset32Check: "323232",
		offset40Check: "404040",
		offset60Check: "606060",
	}
	updateOffsetTolerance := func(check *widget.Check) {
		fyne.Do(func() {
			check.Refresh()
			tol, ok := offsetTolerance[check]
			if !ok || !check.Checked {
				return
			}
			// 更新fgColorEntry的容差部分
			cur := fgColorEntry.Text
			if idx := strings.Index(cur, "-"); idx >= 0 {
				fgColorEntry.SetText(cur[:idx+1] + tol)
			} else {
				fgColorEntry.SetText(cur + "-" + tol)
			}
		})
	}
	offset15Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset15Check) }
	offset32Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset32Check) }
	offset40Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset40Check) }
	offset60Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset60Check) }

	previewCanvasImg := canvas.NewImageFromImage(nil)
	previewCanvasImg.ScaleMode = canvas.ImageScalePixels
	previewCanvasImg.FillMode = canvas.ImageFillOriginal

	infoLabel := widget.NewLabel("请先获取选区或加载图片")
	infoLabel.Wrapping = fyne.TextWrapWord

	// 坐标标签放在右侧面板「字库内容」下方的空白区域
	coordsLabel := widget.NewLabel("")
	coordsLabel.Wrapping = fyne.TextWrapWord

	var spPointList *widget.List // 主界面取色点镜像表（右栏；前向声明供上方添加点回调刷新）

	charCardHolder := container.NewHBox()
	scrollBarPad := canvas.NewRectangle(color.Transparent)
	scrollBarPad.SetMinSize(fyne.NewSize(1, 12))
	charCardInner := container.NewVBox(charCardHolder, scrollBarPad)
	charCardScroll := container.NewHScroll(charCardInner)

	quickFillEntry := widget.NewEntry()
	quickFillEntry.SetPlaceHolder("快速填入: 主题壁纸")
	quickFillBtn := widget.NewButtonWithIcon("快速填入", theme.ConfirmIcon(), func() {
		chars := []rune(strings.TrimSpace(quickFillEntry.Text))
		for i := 0; i < len(charNameEntries) && i < len(chars); i++ {
			charNameEntries[i].SetText(string(chars[i]))
		}
	})
	quickFillBtn.Importance = widget.HighImportance

	// ===== 右侧字库列表 =====
	libListBox := container.NewVBox()
	// libListScroll 已随「可选颜色组」面板一起移出布局（右栏整给取色点镜像），构建保留防引用错
	libListScroll := container.NewVScroll(libListBox)
	_ = libListScroll
	libHeaderLabel := widget.NewLabel("可选颜色组 (0)")
	libHeaderLabel.TextStyle = fyne.TextStyle{Bold: true}

	var rebuildLibList func()
	rebuildLibList = func() {
		libListBox.RemoveAll()
		for i, ch := range fontLibChars {
			idx := i
			previewImg := canvas.NewImageFromImage(createCharPreview(ch.Bitmap, 2))
			previewImg.ScaleMode = canvas.ImageScalePixels
			previewImg.FillMode = canvas.ImageFillContain
			previewImg.SetMinSize(fyne.NewSize(26, 26))

			nameText := canvas.NewText(ch.Char, getTextColor(isDarkTheme))
			nameText.TextSize = 14
			nameText.TextStyle = fyne.TextStyle{Bold: true}

			sizeText := canvas.NewText(fmt.Sprintf("%dx%d", ch.Width, ch.Height), color.NRGBA{140, 140, 140, 255})
			sizeText.TextSize = 10

			delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				fontLibChars = append(fontLibChars[:idx], fontLibChars[idx+1:]...)
				rebuildLibList()
			})
			delBtn.Importance = widget.LowImportance

			leftInfo := container.NewHBox(previewImg, container.NewCenter(nameText), container.NewCenter(sizeText))
			scrollPad := canvas.NewRectangle(color.Transparent)
			scrollPad.SetMinSize(fyne.NewSize(12, 1))
			row := container.NewBorder(nil, nil, leftInfo, container.NewHBox(delBtn, scrollPad))
			libListBox.Add(row)
		}
		libListBox.Refresh()
		libHeaderLabel.SetText(fmt.Sprintf("可选颜色组 (%d)", len(fontLibChars)))
	}

	// ===== 核心：开始切割 =====
	doSlice := func() {
		if regionImg == nil {
			return
		}
		showingOriginal = false
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		// 根据勾选的偏移复选框确定间距值
		offset := 1
		if offset60Check.Checked {
			offset = 60
		} else if offset40Check.Checked {
			offset = 40
		} else if offset32Check.Checked {
			offset = 32
		} else if offset15Check.Checked {
			offset = 15
		}
		cg := offset
		rg := offset

		fullBinary := createBinaryPreview(regionImg, fgHex)
		bboxes := findCharacterBBoxes(fullBinary, cg, rg)
		if len(bboxes) > 0 {
			unionRect := bboxes[0]
			for _, bb := range bboxes[1:] {
				if bb.Min.X < unionRect.Min.X {
					unionRect.Min.X = bb.Min.X
				}
				if bb.Min.Y < unionRect.Min.Y {
					unionRect.Min.Y = bb.Min.Y
				}
				if bb.Max.X > unionRect.Max.X {
					unionRect.Max.X = bb.Max.X
				}
				if bb.Max.Y > unionRect.Max.Y {
					unionRect.Max.Y = bb.Max.Y
				}
			}
			pad := 2
			imgW, imgH := fullBinary.Bounds().Dx(), fullBinary.Bounds().Dy()
			x0 := max(0, unionRect.Min.X-pad)
			y0 := max(0, unionRect.Min.Y-pad)
			x1 := min(imgW, unionRect.Max.X+pad)
			y1 := min(imgH, unionRect.Max.Y+pad)
			expanded := image.Rect(x0, y0, x1, y1)
			cropped := image.NewNRGBA(image.Rect(0, 0, expanded.Dx(), expanded.Dy()))
			for y := 0; y < expanded.Dy(); y++ {
				for x := 0; x < expanded.Dx(); x++ {
					cropped.SetNRGBA(x, y, fullBinary.NRGBAAt(expanded.Min.X+x, expanded.Min.Y+y))
				}
			}
			binaryRegion = cropped
		} else {
			binaryRegion = fullBinary
		}

		dotImg, cells := renderDotMatrix(binaryRegion, cg, rg)
		charCells = cells

		previewCanvasImg.Image = dotImg
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(dotImg.Bounds().Dx()), float32(dotImg.Bounds().Dy())))
		previewCanvasImg.Refresh()

		bw := binaryRegion.Bounds().Dx()
		bh := binaryRegion.Bounds().Dy()
		infoLabel.SetText(fmt.Sprintf("选区: %d×%d px | 检测到 %d 个字符 | 列间距:%d 行间距:%d",
			bw, bh, len(charCells), cg, rg))

		charCardHolder.RemoveAll()
		charNameEntries = make([]*widget.Entry, len(charCells))
		charHexCache = make([]string, len(charCells))
		charWpCache = make([]int, len(charCells))
		charMatchedLib = make([]bool, len(charCells))

		var cards []fyne.CanvasObject
		for i, cell := range charCells {
			hexData, wp := encodeBitmapHex(cell.Bitmap)
			charHexCache[i] = hexData
			charWpCache[i] = wp
			matchedName := ""
			for _, lc := range fontLibChars {
				if lc.HexData == hexData {
					matchedName = lc.Char
					charMatchedLib[i] = true
					break
				}
			}

			previewImg := canvas.NewImageFromImage(createCharPreview(cell.Bitmap, 2))
			previewImg.ScaleMode = canvas.ImageScalePixels
			previewImg.FillMode = canvas.ImageFillContain
			pw := float32(cell.BBox.Dx() * 2)
			ph := float32(cell.BBox.Dy() * 2)
			if pw < 20 {
				pw = 20
			}
			if pw > 70 {
				pw = 70
			}
			if ph < 20 {
				ph = 20
			}
			if ph > 50 {
				ph = 50
			}
			previewImg.SetMinSize(fyne.NewSize(pw, ph))

			idText := canvas.NewText(fmt.Sprintf("#%d  %dx%d", i, cell.BBox.Dx(), cell.BBox.Dy()), color.NRGBA{130, 170, 230, 255})
			idText.TextSize = 10

			nameEntry := widget.NewEntry()
			nameEntry.SetPlaceHolder("字符")
			if matchedName != "" {
				nameEntry.SetText(matchedName)
			}
			charNameEntries[i] = nameEntry

			cardContent := container.NewBorder(
				idText, nameEntry, nil, nil,
				container.NewCenter(previewImg),
			)

			cardBg := canvas.NewRectangle(color.NRGBA{30, 30, 38, 255})
			if !isDarkTheme {
				cardBg = canvas.NewRectangle(color.NRGBA{235, 237, 242, 255})
			}
			cardMinW := canvas.NewRectangle(color.Transparent)
			cardMinW.SetMinSize(fyne.NewSize(100, 0))
			card := container.NewStack(cardMinW, cardBg, container.NewPadded(cardContent))
			cards = append(cards, card)
		}

		if len(cards) > 0 {
			row := container.NewHBox(cards...)
			charCardHolder.Add(row)
		}
		charCardHolder.Refresh()
		// 切割后更新放大镜到二值图
		if hoverHasPos {
			updateSpMag()
		}
	}
	_ = doSlice // 保留供后续按钮调用

	// ===== 添加到字库 =====
	addToLibBtn := widget.NewButtonWithIcon("添加到字库", theme.ContentAddIcon(), func() {
		added := 0
		for i, cell := range charCells {
			if i >= len(charNameEntries) || i >= len(charMatchedLib) {
				break
			}
			if charMatchedLib[i] {
				continue
			}
			name := strings.TrimSpace(charNameEntries[i].Text)
			if name == "" {
				continue
			}
			bm := cell.Bitmap
			if len(bm) == 0 || len(bm[0]) == 0 {
				continue
			}
			hexData := charHexCache[i]
			wp := charWpCache[i]
			newChar := FontChar{
				Char: name, Width: len(bm[0]), Height: len(bm),
				HexData: hexData, WhitePixels: wp, Bitmap: bm,
			}
			fontLibChars = append(fontLibChars, newChar)
			added++
		}
		if added > 0 {
			rebuildLibList()
		}
	})
	addToLibBtn.Importance = widget.HighImportance

	// ===== 按钮 =====
	getSelBtn := widget.NewButtonWithIcon("获取选区", theme.VisibilityIcon(), func() {
		if imageViewer == nil || imageViewer.image == nil {
			dialog.ShowInformation("提示", "主窗口没有图片，请先截图或载入", w)
			return
		}
		// ★ 每次获取选区 ⇒ 清空主界面取色点（新一轮选区重新取，旧坐标不作数）
		colorPoints = nil
		if tableContent != nil {
			tableContent.Refresh()
		}
		if spPointList != nil {
			spPointList.Refresh()
		}
		if len(imageViewer.markRects) == 0 {
			dialog.ShowInformation("提示", "请先在主窗口图像上拖拽框选文字区域，然后再点此按钮", w)
			return
		}
		rect := imageViewer.markRects[0]
		selRect := image.Rect(
			min(rect.X1, rect.X2), min(rect.Y1, rect.Y2),
			max(rect.X1, rect.X2), max(rect.Y1, rect.Y2),
		)
		regionImg = cropImage(imageViewer.image, selRect)
		showingOriginal = true
		// 初始化放大镜
		magPanel.Update(regionImg, regionImg.Bounds().Dx()/2, regionImg.Bounds().Dy()/2)
		dotPreview := renderOriginalDotMatrix(regionImg)
		previewCanvasImg.Image = dotPreview
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(dotPreview.Bounds().Dx()), float32(dotPreview.Bounds().Dy())))
		previewCanvasImg.Refresh()
		infoLabel.SetText(fmt.Sprintf("选区: %d×%d px | 请点击「开始切割」进行二值化分割",
			regionImg.Bounds().Dx(), regionImg.Bounds().Dy()))
	})
	getSelBtn.Importance = widget.HighImportance

	// ===== 布局 =====
	leftPanel := container.New(&fixedWidthLayout{width: 155, padding: 10, verticalSpacing: 4},
		layout.NewSpacer(),
		magPanel,
		getSelBtn,
		widget.NewSeparator(),
		widget.NewLabel("文字颜色:"),
		fgColorEntry,
		widget.NewSeparator(),
		offset15Check,
		offset32Check,
		offset40Check,
		offset60Check,
		coordsLabel, // ★ 从右栏挪来：坐标(图内) 显示在「偏移60」下面
		layout.NewSpacer(),
	)

	gridBg := newGridBgWidget()

	// ===== 智能取色：鼠标跟踪 + 方向键 + 放大镜 =====
	setHoverPos := func(pos fyne.Position) {
		if ignoreMouseHover {
			return
		}
		if !showingOriginal || regionImg == nil {
			return
		}
		stride := float32(dotCellSize + 1)
		hoverImgX = int(pos.X / stride)
		hoverImgY = int(pos.Y / stride)
		// 优先用 binaryRegion（切割后），否则用 regionImg（切割前）
		var src image.Image = regionImg
		if binaryRegion != nil {
			src = binaryRegion
		}
		b := src.Bounds()
		if hoverImgX < 0 {
			hoverImgX = 0
		}
		if hoverImgY < 0 {
			hoverImgY = 0
		}
		if hoverImgX >= b.Dx() {
			hoverImgX = b.Dx() - 1
		}
		if hoverImgY >= b.Dy() {
			hoverImgY = b.Dy() - 1
		}
		hoverHasPos = true
		updateSpMag()
	}

	spHover = newSpHoverArea(
		setHoverPos, // onMouseMove
		setHoverPos, // onDrag
		func(pos fyne.Position) { // onTap
			if !showingOriginal || regionImg == nil {
				return
			}
			stride := float32(dotCellSize + 1)
			px := int(pos.X / stride)
			py := int(pos.Y / stride)
			var src image.Image = regionImg
			if binaryRegion != nil {
				src = binaryRegion
			}
			b := src.Bounds()
			if px < 0 || py < 0 || px >= b.Dx() || py >= b.Dy() {
				return
			}
			r, g, bl, _ := regionImg.At(b.Min.X+px, b.Min.Y+py).RGBA()
			spColorEntry.SetText(fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, bl>>8))
			hexColor := fmt.Sprintf("%02X%02X%02X", r>>8, g>>8, bl>>8)
			cur := fgColorEntry.Text
			if idx := strings.Index(cur, "-"); idx >= 0 {
				fgColorEntry.SetText(hexColor + cur[idx:])
			} else {
				fgColorEntry.SetText(hexColor)
			}
		},
		func(key *fyne.KeyEvent) { // onKey
			var src image.Image = regionImg
			if binaryRegion != nil {
				src = binaryRegion
			}
			if src == nil {
				return
			}
			b := src.Bounds()
			if !hoverHasPos {
				hoverImgX = b.Dx() / 2
				hoverImgY = b.Dy() / 2
				hoverHasPos = true
			}
			switch key.Name {
			case fyne.KeyUp:
				hoverImgY--
			case fyne.KeyDown:
				hoverImgY++
			case fyne.KeyLeft:
				hoverImgX--
			case fyne.KeyRight:
				hoverImgX++
			case fyne.KeyReturn:
				if hoverHasPos {
					r8, g8, b8 := getPixelColorFast(src, hoverImgX, hoverImgY)
					hexColor := fmt.Sprintf("%02X%02X%02X", r8, g8, b8)
					cur := fgColorEntry.Text
					if idx := strings.Index(cur, "-"); idx >= 0 {
						fgColorEntry.SetText(hexColor + cur[idx:])
					} else {
						fgColorEntry.SetText(hexColor)
					}
				}
				return
			case fyne.KeySpace:
				// 空格键：把当前取色点添加到主窗口
				if hoverHasPos && regionImg != nil {
					b2 := regionImg.Bounds()
					if hoverImgX >= 0 && hoverImgX < b2.Dx() && hoverImgY >= 0 && hoverImgY < b2.Dy() {
						r8, g8, b8 := getPixelColorFast(regionImg, hoverImgX, hoverImgY)
						hexColor := fmt.Sprintf("%02X%02X%02X", r8, g8, b8)
						// 计算实际坐标（加上选区偏移）
						actualX, actualY := hoverImgX, hoverImgY
						if len(imageViewer.markRects) > 0 {
							rect := imageViewer.markRects[0]
							actualX += min(rect.X1, rect.X2)
							actualY += min(rect.Y1, rect.Y2)
						}
						// 提取偏色值
						tolerance := "101010"
						cur := fgColorEntry.Text
						if idx := strings.Index(cur, "-"); idx >= 0 {
							tolerance = cur[idx+1:]
						}
						colorPoints = append(colorPoints, ColorPoint{
							ID:       len(colorPoints) + 1,
							Color:    hexColor,
							Offset:   tolerance,
							Position: fmt.Sprintf("%d, %d", actualX, actualY),
							Selected: true,
						})
						fyne.Do(func() {
							if tableContent != nil {
								tableContent.Refresh()
							}
						})
						infoLabel.SetText(fmt.Sprintf("已添加取色点: %s @ %d,%d", hexColor, actualX, actualY))
					}
				}
				return
			default:
				return
			}
			if hoverImgX < 0 {
				hoverImgX = 0
			}
			if hoverImgY < 0 {
				hoverImgY = 0
			}
			if hoverImgX >= b.Dx() {
				hoverImgX = b.Dx() - 1
			}
			if hoverImgY >= b.Dy() {
				hoverImgY = b.Dy() - 1
			}
			updateSpMag()
			ignoreMouseHover = true
			moveSystemCursor(hoverImgX, hoverImgY, w, spHover)
			time.AfterFunc(50*time.Millisecond, func() {
				ignoreMouseHover = false
			})
		},
	)

	scrollContent := container.NewStack(
		container.New(&topLeftLayout{}, previewCanvasImg),
	)
	previewScroll := container.NewScroll(scrollContent)
	previewArea := container.NewStack(gridBg, previewScroll, spHover)

	optimizeBtn := widget.NewButton("优化", func() {
		if regionImg == nil {
			dialog.ShowInformation("提示", "请先获取选区或加载图片", w)
			return
		}
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		// 根据勾选的偏移复选框确定间距值
		offset := 1
		if offset60Check.Checked {
			offset = 60
		} else if offset40Check.Checked {
			offset = 40
		} else if offset32Check.Checked {
			offset = 32
		} else if offset15Check.Checked {
			offset = 15
		}
		// 原图二值化
		binary := createBinaryPreview(regionImg, fgHex)
		// 渲染为放大点阵图（不绘制边界框）
		dotImg := renderDotMatrixOnly(binary)
		previewCanvasImg.Image = dotImg
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(dotImg.Bounds().Dx()), float32(dotImg.Bounds().Dy())))
		previewCanvasImg.Refresh()
		showingOriginal = false
		infoLabel.SetText(fmt.Sprintf("原图二值化: %d×%d px | 间距:%d", binary.Bounds().Dx(), binary.Bounds().Dy(), offset))
	})
	optimizeBtn.Importance = widget.HighImportance

	part1Btn := widget.NewButton("第一部分", func() {
		if regionImg == nil {
			dialog.ShowInformation("提示", "请先获取选区或加载图片", w)
			return
		}
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		// 二值化
		binary := createBinaryPreview(regionImg, fgHex)
		b := binary.Bounds()
		pw, ph := b.Dx(), b.Dy()

		// 获取选区偏移量（从 markRects 计算）
		offsetX, offsetY := 0, 0
		if len(imageViewer.markRects) > 0 {
			rect := imageViewer.markRects[0]
			offsetX = min(rect.X1, rect.X2)
			offsetY = min(rect.Y1, rect.Y2)
		}

		// 第一步：找最佳列（有连续>=2绿色段，且绿色点最多的列）
		bestCol := -1
		maxGreen := 0
		for x := 0; x < pw; x++ {
			hasRun := false
			curRun := 0
			totalGreen := 0
			for y := 0; y < ph; y++ {
				if binary.NRGBAAt(x, y).G > 128 {
					curRun++
					totalGreen++
					if curRun >= 2 {
						hasRun = true
					}
				} else {
					curRun = 0
				}
			}
			if hasRun && totalGreen > maxGreen {
				maxGreen = totalGreen
				bestCol = x
			}
		}

		if bestCol < 0 {
			dialog.ShowInformation("提示", "未找到竖线", w)
			return
		}

		// 第二步：从最佳列中提取所有段（连续>=2绿色点），分边界点和中间点（用图片本地坐标，不加 offset）
		type point struct{ x, y int }
		var middlePoints []point
		var boundaryPoints []point

		runStart := -1
		for y := 0; y <= ph; y++ {
			isGreen := y < ph && binary.NRGBAAt(bestCol, y).G > 128
			if isGreen && runStart < 0 {
				runStart = y
			} else if !isGreen && runStart >= 0 {
				runLen := y - runStart
				if runLen >= 2 {
					boundaryPoints = append(boundaryPoints, point{bestCol, runStart})
					if runLen > 2 {
						for yy := runStart + 1; yy < y-1; yy++ {
							middlePoints = append(middlePoints, point{bestCol, yy})
						}
					}
					boundaryPoints = append(boundaryPoints, point{bestCol, y - 1})
				}
				runStart = -1
			}
		}

		total := len(middlePoints) + len(boundaryPoints)
		if total < 2 {
			dialog.ShowInformation("提示", "绿色点不足", w)
			return
		}

		targetCount := 7
		if total < targetCount {
			targetCount = total
		}

		// 重新渲染点阵图后，在渲染图上直接把命中点画成黑色块（避免 renderDotMatrixOnly 把黑色判定成"非绿"）
		newDotImg := renderDotMatrixOnly(binary)
		var pts []image.Point
		for _, p := range boundaryPoints {
			pts = append(pts, image.Pt(p.x, p.y))
		}
		for _, p := range middlePoints {
			pts = append(pts, image.Pt(p.x, p.y))
		}
		drawBlackBlocksOnImg(newDotImg, pts)
		previewCanvasImg.Image = newDotImg
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(newDotImg.Bounds().Dx()), float32(newDotImg.Bounds().Dy())))
		previewCanvasImg.Refresh()

		var selected []point

		// 先从中间点随机取
		rand.Shuffle(len(middlePoints), func(i, j int) {
			middlePoints[i], middlePoints[j] = middlePoints[j], middlePoints[i]
		})
		need := targetCount
		if need > len(middlePoints) {
			need = len(middlePoints)
		}
		selected = append(selected, middlePoints[:need]...)

		// 不够再从边界点补
		if len(selected) < targetCount {
			rand.Shuffle(len(boundaryPoints), func(i, j int) {
				boundaryPoints[i], boundaryPoints[j] = boundaryPoints[j], boundaryPoints[i]
			})
			remain := targetCount - len(selected)
			if remain > len(boundaryPoints) {
				remain = len(boundaryPoints)
			}
			selected = append(selected, boundaryPoints[:remain]...)
		}

		// 按 Y 坐标排序
		sort.Slice(selected, func(i, j int) bool {
			return selected[i].y < selected[j].y
		})

		// 从原图取色，添加到主窗口（用绝对坐标，加回 offset）
		tolerance := "101010"
		if idx := strings.Index(fgHex, "-"); idx >= 0 {
			tolerance = fgHex[idx+1:]
		}
		for _, p := range selected {
			r, g, bl, _ := regionImg.At(p.x, p.y).RGBA()
			colorHex := fmt.Sprintf("%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(bl>>8))
			colorPoints = append(colorPoints, ColorPoint{
				ID:       len(colorPoints) + 1,
				Color:    colorHex,
				Offset:   tolerance,
				Position: fmt.Sprintf("%d, %d", p.x+offsetX, p.y+offsetY),
				Selected: true,
			})
		}

		fyne.Do(func() {
			if tableContent != nil {
				tableContent.Refresh()
			}
			spPointList.Refresh() // ★ 同步刷新右栏点表镜像
		})

		// 拼接所有符合点坐标（图片本地坐标，未加 offset）
		var coordsSb strings.Builder
		all := append(append([]point{}, boundaryPoints...), middlePoints...)
		sort.Slice(all, func(i, j int) bool {
			if all[i].y != all[j].y {
				return all[i].y < all[j].y
			}
			return all[i].x < all[j].x
		})
		for _, p := range all {
			coordsSb.WriteString(fmt.Sprintf("(%d,%d) ", p.x, p.y))
		}

		infoLabel.SetText(fmt.Sprintf("第一部分：列%d 中间%d点 边界%d点 共%d个 抽中%d",
			bestCol, len(middlePoints), len(boundaryPoints), total, len(selected)))
		coordsLabel.SetText("坐标(图内): " + coordsSb.String())
	})
	part2Btn := widget.NewButton("第二部分", func() {
		if regionImg == nil {
			dialog.ShowInformation("提示", "请先获取选区或加载图片", w)
			return
		}
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		// 提取偏色值
		tolerance := "101010"
		if idx := strings.Index(fgHex, "-"); idx >= 0 {
			tolerance = fgHex[idx+1:]
		}
		// 二值化
		binary := createBinaryPreview(regionImg, fgHex)
		b := binary.Bounds()
		pw, ph := b.Dx(), b.Dy()

		// 获取选区偏移量
		offsetX, offsetY := 0, 0
		if len(imageViewer.markRects) > 0 {
			rect := imageViewer.markRects[0]
			offsetX = min(rect.X1, rect.X2)
			offsetY = min(rect.Y1, rect.Y2)
		}

		// 第一步：找最佳行（有连续>=2绿色段，且绿色点最多的行）
		bestRow := -1
		maxGreen := 0
		for y := 0; y < ph; y++ {
			hasRun := false
			curRun := 0
			totalGreen := 0
			for x := 0; x < pw; x++ {
				if binary.NRGBAAt(x, y).G > 128 {
					curRun++
					totalGreen++
					if curRun >= 2 {
						hasRun = true
					}
				} else {
					curRun = 0
				}
			}
			if hasRun && totalGreen > maxGreen {
				maxGreen = totalGreen
				bestRow = y
			}
		}

		if bestRow < 0 {
			dialog.ShowInformation("提示", "未找到横线", w)
			return
		}

		// 第二步：从最佳行中提取所有段（连续>=2绿色点），分边界点和中间点（用图片本地坐标，不加 offset）
		type point struct{ x, y int }
		var middlePoints []point
		var boundaryPoints []point

		runStart := -1
		for x := 0; x <= pw; x++ {
			isGreen := x < pw && binary.NRGBAAt(x, bestRow).G > 128
			if isGreen && runStart < 0 {
				runStart = x
			} else if !isGreen && runStart >= 0 {
				runLen := x - runStart
				if runLen >= 2 {
					boundaryPoints = append(boundaryPoints, point{runStart, bestRow})
					if runLen > 2 {
						for xx := runStart + 1; xx < x-1; xx++ {
							middlePoints = append(middlePoints, point{xx, bestRow})
						}
					}
					boundaryPoints = append(boundaryPoints, point{x - 1, bestRow})
				}
				runStart = -1
			}
		}

		total := len(middlePoints) + len(boundaryPoints)
		if total < 2 {
			dialog.ShowInformation("提示", "绿色点不足", w)
			return
		}

		targetCount := 7
		if total < targetCount {
			targetCount = total
		}

		// 重新渲染后直接在渲染图上画黑块（避免被判定为"非绿"）
		newDotImg := renderDotMatrixOnly(binary)
		var pts2 []image.Point
		for _, p := range boundaryPoints {
			pts2 = append(pts2, image.Pt(p.x, p.y))
		}
		for _, p := range middlePoints {
			pts2 = append(pts2, image.Pt(p.x, p.y))
		}
		drawBlackBlocksOnImg(newDotImg, pts2)
		previewCanvasImg.Image = newDotImg
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(newDotImg.Bounds().Dx()), float32(newDotImg.Bounds().Dy())))
		previewCanvasImg.Refresh()

		var selected []point

		// 先从中间点随机取
		rand.Shuffle(len(middlePoints), func(i, j int) {
			middlePoints[i], middlePoints[j] = middlePoints[j], middlePoints[i]
		})
		need := targetCount
		if need > len(middlePoints) {
			need = len(middlePoints)
		}
		selected = append(selected, middlePoints[:need]...)

		// 不够再从边界点补
		if len(selected) < targetCount {
			rand.Shuffle(len(boundaryPoints), func(i, j int) {
				boundaryPoints[i], boundaryPoints[j] = boundaryPoints[j], boundaryPoints[i]
			})
			remain := targetCount - len(selected)
			if remain > len(boundaryPoints) {
				remain = len(boundaryPoints)
			}
			selected = append(selected, boundaryPoints[:remain]...)
		}

		// 按 X 坐标排序
		sort.Slice(selected, func(i, j int) bool {
			return selected[i].x < selected[j].x
		})

		// 从原图取色，添加到主窗口（坐标已经在图内，加回 offset 仅用于展示绝对坐标）
		for _, p := range selected {
			r, g, bl, _ := regionImg.At(p.x, p.y).RGBA()
			colorHex := fmt.Sprintf("%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(bl>>8))
			colorPoints = append(colorPoints, ColorPoint{
				ID:       len(colorPoints) + 1,
				Color:    colorHex,
				Offset:   tolerance,
				Position: fmt.Sprintf("%d, %d", p.x+offsetX, p.y+offsetY),
				Selected: true,
			})
		}

		fyne.Do(func() {
			if tableContent != nil {
				tableContent.Refresh()
			}
			spPointList.Refresh() // ★ 同步刷新右栏点表镜像
		})

		// 拼接所有符合点坐标（图片本地坐标）
		var coordsSb strings.Builder
		all := append(append([]point{}, boundaryPoints...), middlePoints...)
		sort.Slice(all, func(i, j int) bool {
			if all[i].x != all[j].x {
				return all[i].x < all[j].x
			}
			return all[i].y < all[j].y
		})
		for _, p := range all {
			coordsSb.WriteString(fmt.Sprintf("(%d,%d) ", p.x, p.y))
		}

		infoLabel.SetText(fmt.Sprintf("第二部分：行%d 中间%d点 边界%d点 共%d个 抽中%d",
			bestRow, len(middlePoints), len(boundaryPoints), total, len(selected)))
		coordsLabel.SetText("坐标(图内): " + coordsSb.String())
	})
	part3Btn := widget.NewButton("第三部分", func() {
		// TODO: 第三部分功能
	})

	buttonRow := container.NewGridWithColumns(4, optimizeBtn, part1Btn, part2Btn, part3Btn)
	charCardArea := newFixedHeightContainer(charCardScroll, 130)

	centerArea := container.NewBorder(
		infoLabel,
		container.NewVBox(
			widget.NewSeparator(),
			charCardArea,
			buttonRow,
		),
		nil, nil,
		previewArea,
	)

	// ===== 主界面取色点表映射（右栏，只读镜像；colorPoints 为包级变量直接读）=====
	// 行 = 5 列分栏（复用主界面表格的 weightedGridLayout），不再挤成一个字符串
	spPointList = widget.NewList(
		func() int { return len(colorPoints) },
		func() fyne.CanvasObject {
			mk := func(w float32) *canvas.Text {
				t := canvas.NewText("", getTextColor(isDarkTheme))
				t.TextSize = 10
				t.Alignment = fyne.TextAlignCenter
				t.Resize(fyne.NewSize(w, t.MinSize().Height))
				return t
			}
			idT, posT, colorT, offT, selT := mk(30), mk(60), mk(52), mk(44), mk(18)
			// ⛔⛔ 行 content 的 Objects[0..4] 必须就是 5 个列对象（更新函数按下标取）——
			//    中间再包 Padded/Stack 都会让断言 panic ⇒ 整窗秒退（00:13、00:43 两次实测）。
			//    列间距只能靠 weights/列宽，不能加包装层。
			return newClickableTableRow(color.Transparent, container.New(&weightedGridLayout{
				cols:    5,
				weights: []float32{0.7, 1.6, 1.5, 1.3, 0.5},
			}, idT, posT, colorT, offT, container.NewCenter(selT)), nil)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(colorPoints) {
				return
			}
			p := colorPoints[id]
			row := item.(*ClickableTableRow)
			cells := row.content.Objects
			idT, posT := cells[0].(*canvas.Text), cells[1].(*canvas.Text)
			colorT, offT := cells[2].(*canvas.Text), cells[3].(*canvas.Text)
			selT := cells[4].(*fyne.Container).Objects[0].(*canvas.Text)
			txtColor := getTextColor(isDarkTheme)
			idT.Text = fmt.Sprintf("%d", p.ID)
			posT.Text = p.Position
			colorT.Text = strings.TrimPrefix(p.Color, "#")
			offT.Text = p.Offset
			selT.Text = "☑"
			selT.Color = hexToColor(p.Color)
			if !p.Selected {
				selT.Text = "☐"
				selT.Color = txtColor
			}
			for _, t := range []*canvas.Text{idT, posT, colorT, offT} {
				t.Color = txtColor
				t.Refresh()
			}
			selT.Refresh()
		},
	)
	spPointHeader := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle("主界面取色点", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() { spPointList.Refresh() }),
	)

	// ★ 右栏整个给「主界面取色点」镜像（原「可选颜色组」空列表已移除——与点表无关）
	spPointArea := container.NewBorder(spPointHeader, nil, nil, nil, spPointList)

	rightContent := container.NewBorder(
		nil,
		nil, // ★ 底部「取色颜色值」输入框已去掉（取色直接填左栏文字颜色，用不着它）
		nil, nil,
		spPointArea,
	)
	rightBg := canvas.NewRectangle(color.Transparent)
	rightBg.SetMinSize(fyne.NewSize(240, 0)) // ★ 200→240：点表镜像 5 列不挤（用户反馈挤在一起）
	rightPanel := container.NewStack(rightBg, container.NewPadded(rightContent))

	mainContent := container.NewBorder(nil, nil, leftPanel, rightPanel, centerArea)
	w.SetContent(mainContent)
	w.Canvas().Focus(spHover)
	w.Show()
}

// drawBlackBlocksOnImg 在已渲染的点阵图上直接画黑块（绕过 renderDotMatrixOnly 的"非绿"判定）
func drawBlackBlocksOnImg(img *image.NRGBA, points []image.Point) {
	stride := dotCellSize + 1
	for _, p := range points {
		cx := p.X*stride + 1
		cy := p.Y*stride + 1
		for dy := 0; dy < dotCellSize; dy++ {
			idx := (cy+dy)*img.Stride + cx*4
			for dx := 0; dx < dotCellSize; dx++ {
				img.Pix[idx] = 0
				img.Pix[idx+1] = 0
				img.Pix[idx+2] = 0
				img.Pix[idx+3] = 255
				idx += 4
			}
		}
	}
}
