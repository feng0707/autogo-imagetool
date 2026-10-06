package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type ImageViewer struct {
	widget.BaseWidget
	image              image.Image // 当前显示的图像
	originalImage      image.Image // 保存的原始图像
	rotationDegrees    int         // 当前旋转角度 (0, 90, 180, 270)
	displayImage       *canvas.Image
	markPoints         []MarkPoint // 存储点标记
	testPoints         []MarkPoint
	testTexts          []MarkText
	markRects          []MarkRect // 存储矩形标记
	mouseDownX         int        // 鼠标按下时的X坐标
	mouseDownY         int        // 鼠标按下时的Y坐标
	isDragging         bool       // 是否正在拖动
	tempRect           *MarkRect  // 临时矩形，用于拖动过程的显示
	lastMouseX         int        // 上次鼠标X坐标（用于检测真实移动）
	lastMouseY         int        // 上次鼠标Y坐标（用于检测真实移动）
	onMouseMove        func(x, y int)
	onMouseDown        func(x, y int)
	onMouseUp          func(x, y int)
	onRightClick       func(x, y int)
	getGridParams      func() (cols, rows, spacing int, hasParams bool) // 获取点阵参数的回调
	scrollContainer    *container.Scroll                                // 滚动容器引用
	magnifier          *MagnifierWidget                                 // 放大镜引用
	manualRectSelected bool                                             // 是否手动框选了区域
	mouseInWidget      bool                                             // 鼠标是否在图片框上
	window             fyne.Window                                      // 窗口引用，用于获取窗口位置
	isBinarized        bool                                             // 是否处于二值化状态
	pixelScale         float32                                          // 像素缩放比例，默认1.0
	// 方向键移动光标后的抑制窗口：期间丢弃与期望位置不符的异步 MouseMoved（过期/被系统合并的事件），
	// 匹配到期望位置或超时后自动恢复正常处理。全部在 fyne 主事件循环内访问，无需加锁。
	kbSuppressActive         bool      // 抑制窗口是否有效
	kbSuppressDeadline       time.Time // 抑制截止时间
	kbSuppressX, kbSuppressY int       // 抑制期间期望的图像坐标（方向键移动后的目标位置）

	// ★ 画笔涂抹（异形图）2026-09-30：对齐大漠涂抹异形图用法——把不要的部分涂成纯色
	//   背景，找图算法（四角同色≥50% 判背景）就只比前景。见 SetPaintMode/paintCircle。
	paintMode      bool             // 是否处于画笔涂抹模式
	eyedropperMode bool             // ★ 吸管模式：点击图像取色为画笔色（一次性）
	onEyedrop      func(color.RGBA) // 吸管取到颜色后的回调
	brushSize  int          // 笔刷半径（图像像素）
	paintColor color.RGBA   // 涂抹色（默认 = 四角最常见色）
	paintImg   *image.NRGBA // 可写图像（进入涂抹模式时从 image 转换）
	lastPaintX int          // 上一次涂抹点（防快速拖动断线）
	lastPaintY int
	undoStack  []*image.NRGBA // 撤销快照（深度 5）
}

// 验证偏色字符串是否有效（6位十六进制）
func validateOffset(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "#")
	if len(text) == 6 {
		for _, c := range text {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return ""
			}
		}
		return strings.ToUpper(text)
	}
	return ""
}

// 获取找色模式，返回"多点找色"或"多点比色"
func getColorMode() string {
	if colorModeRadio == nil {
		return "比色" // 默认模式
	}

	selected := colorModeRadio.Selected
	if selected == "" {
		return "比色" // 未选择时返回默认模式
	}

	return selected
}

type multiColorPoint struct {
	X, Y int
	Want color.RGBA
	Tol  color.RGBA
}

func parseHexRGB(text string) (color.RGBA, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "#")
	if len(text) != 6 {
		return color.RGBA{}, fmt.Errorf("颜色格式错误: %q", text)
	}
	val, err := strconv.ParseUint(text, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("颜色解析失败: %q", text)
	}
	return color.RGBA{R: uint8(val >> 16), G: uint8(val >> 8), B: uint8(val), A: 255}, nil
}

func parseTolRGB(text string) (color.RGBA, error) {
	tol, err := parseHexRGB(text)
	if err != nil {
		return color.RGBA{}, err
	}
	tol.A = 255
	return tol, nil
}

func parseMultiPointSpec(spec string, defaultTol color.RGBA) (multiColorPoint, error) {
	parts := strings.Split(spec, "|")
	if len(parts) != 3 {
		return multiColorPoint{}, fmt.Errorf("点位格式错误: %q", spec)
	}
	x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return multiColorPoint{}, fmt.Errorf("X 坐标解析失败: %q", parts[0])
	}
	y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return multiColorPoint{}, fmt.Errorf("Y 坐标解析失败: %q", parts[1])
	}

	colorPart := strings.TrimSpace(parts[2])
	tol := defaultTol
	if strings.Contains(colorPart, "-") {
		segs := strings.SplitN(colorPart, "-", 2)
		colorPart = strings.TrimSpace(segs[0])
		tolPart := strings.TrimSpace(segs[1])
		parsedTol, err := parseTolRGB(tolPart)
		if err != nil {
			return multiColorPoint{}, err
		}
		tol = parsedTol
	}
	want, err := parseHexRGB(colorPart)
	if err != nil {
		return multiColorPoint{}, err
	}
	return multiColorPoint{X: x, Y: y, Want: want, Tol: tol}, nil
}

func parseMultiPointString(pointsStr string, defaultTol color.RGBA) ([]multiColorPoint, error) {
	raw := strings.TrimSpace(pointsStr)
	if raw == "" {
		return nil, fmt.Errorf("多点字符串为空")
	}
	items := strings.Split(raw, ",")
	points := make([]multiColorPoint, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		p, err := parseMultiPointSpec(item, defaultTol)
		if err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("未解析到有效点位")
	}
	return points, nil
}

var generatedPatternRe = regexp.MustCompile(`\["[^"]*"\]\s*=\s*\{\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*"([0-9a-fA-F]{6}(?:-[0-9a-fA-F]{6})?)"\s*,\s*"([^"]+)"`)

func extractGeneratedPattern(code string) (x1, y1, x2, y2 int, firstColorSpec string, pointsStr string, err error) {
	m := generatedPatternRe.FindStringSubmatch(code)
	if m == nil {
		return 0, 0, 0, 0, "", "", fmt.Errorf("未找到多点段（期望形如 [\"名字\"]={x1,y1,x2,y2, \"ffffff-101010\", \"x|y|color-偏色,...\", ...};）")
	}
	px1, err := strconv.Atoi(strings.TrimSpace(m[1]))
	if err != nil {
		return 0, 0, 0, 0, "", "", fmt.Errorf("x1 解析失败")
	}
	py1, err := strconv.Atoi(strings.TrimSpace(m[2]))
	if err != nil {
		return 0, 0, 0, 0, "", "", fmt.Errorf("y1 解析失败")
	}
	px2, err := strconv.Atoi(strings.TrimSpace(m[3]))
	if err != nil {
		return 0, 0, 0, 0, "", "", fmt.Errorf("x2 解析失败")
	}
	py2, err := strconv.Atoi(strings.TrimSpace(m[4]))
	if err != nil {
		return 0, 0, 0, 0, "", "", fmt.Errorf("y2 解析失败")
	}
	return px1, py1, px2, py2, strings.ToLower(strings.TrimSpace(m[5])), strings.TrimSpace(m[6]), nil
}

func extractGeneratedMultiPoint(code string) (firstColorSpec string, pointsStr string, err error) {
	_, _, _, _, firstColorSpec, pointsStr, err = extractGeneratedPattern(code)
	return firstColorSpec, pointsStr, err
}

// withinTolerance 检查实际颜色是否在期望颜色的偏色范围内
// 对每个颜色通道(R,G,B)分别计算：|实际值 - 期望值| <= 偏色值
func withinTolerance(actual color.Color, want color.RGBA, tol color.RGBA) bool {
	r, g, b, _ := actual.RGBA()
	ar, ag, ab := int(uint8(r>>8)), int(uint8(g>>8)), int(uint8(b>>8))
	if abs(ar-int(want.R)) > int(tol.R) {
		return false
	}
	if abs(ag-int(want.G)) > int(tol.G) {
		return false
	}
	if abs(ab-int(want.B)) > int(tol.B) {
		return false
	}
	return true
}

func colorRGB8(c color.Color) (r, g, b int) {
	cr, cg, cb, _ := c.RGBA()
	return int(uint8(cr >> 8)), int(uint8(cg >> 8)), int(uint8(cb >> 8))
}

func withinDeltaTolerance(base color.Color, actual color.Color, wantBase color.RGBA, want color.RGBA, tol color.RGBA) bool {
	br, bg, bb := colorRGB8(base)
	ar, ag, ab := colorRGB8(actual)

	expectedDr := int(want.R) - int(wantBase.R)
	expectedDg := int(want.G) - int(wantBase.G)
	expectedDb := int(want.B) - int(wantBase.B)

	actualDr := ar - br
	actualDg := ag - bg
	actualDb := ab - bb

	if abs(actualDr-expectedDr) > int(tol.R) {
		return false
	}
	if abs(actualDg-expectedDg) > int(tol.G) {
		return false
	}
	if abs(actualDb-expectedDb) > int(tol.B) {
		return false
	}
	return true
}

// showFindTemplateResultDialog 找色测试结果弹窗：上方为 Lua 模板框，下方为找到的坐标框，各自带复制按钮
func showFindTemplateResultDialog(w fyne.Window, template, result string) {
	makeSection := func(title, text string, minHeight float32) fyne.CanvasObject {
		entry := widget.NewMultiLineEntry()
		entry.SetText(text)
		entry.Wrapping = fyne.TextWrapOff
		entry.TextStyle = fyne.TextStyle{Monospace: true}

		scroll := container.NewScroll(entry)
		scroll.SetMinSize(fyne.NewSize(440, minHeight))

		copyBtn := widget.NewButtonWithIcon("复制", theme.ContentCopyIcon(), func() {
			w.Clipboard().SetContent(text)
		})
		copyBtn.Importance = widget.LowImportance

		titleLabel := canvas.NewText(title, theme.ForegroundColor())
		titleLabel.TextStyle = fyne.TextStyle{Bold: true}

		return container.NewVBox(
			container.NewHBox(titleLabel, copyBtn),
			scroll,
		)
	}

	content := container.NewVBox(
		makeSection("找色模板", template, 130),
		container.NewPadded(widget.NewSeparator()),
		makeSection("测试结果", result, 130),
	)

	d := dialog.NewCustom("FindMulColor", "关闭", container.NewPadded(content), w)
	d.Resize(fyne.NewSize(500, 520))
	d.Show()
}

func showFindMultiResultDialog(w fyne.Window, text string) {
	entry := widget.NewMultiLineEntry()
	entry.SetText(text)
	entry.Wrapping = fyne.TextWrapOff
	entry.TextStyle = fyne.TextStyle{Monospace: true}

	scroll := container.NewScroll(entry)
	scroll.SetMinSize(fyne.NewSize(420, 300))

	d := dialog.NewCustom("FindMulColor", "关闭", container.NewPadded(scroll), w)
	d.Resize(fyne.NewSize(460, 400))
	d.Show()
}

func showFindMultiResultDialogHighlightFirst(w fyne.Window, line string, highlight bool) {
	titleLine := canvas.NewText(line, theme.ForegroundColor())
	titleLine.TextStyle = fyne.TextStyle{Monospace: true, Bold: highlight}
	titleLine.TextSize = 14
	if highlight {
		titleLine.Color = color.NRGBA{255, 60, 60, 255}
	}

	entry := widget.NewMultiLineEntry()
	entry.SetText(line)
	entry.Wrapping = fyne.TextWrapOff
	entry.TextStyle = fyne.TextStyle{Monospace: true}

	entryScroll := container.NewScroll(entry)
	entryScroll.SetMinSize(fyne.NewSize(240, 220))

	content := container.NewVBox(
		container.NewPadded(titleLine),
		container.NewPadded(entryScroll),
	)

	d := dialog.NewCustom("FindMulColor", "关闭", content, w)
	d.Resize(fyne.NewSize(280, 320))
	d.Show()
}

// 保存当前标签页的数据
func saveCurrentTabData() {
	if currentTab == nil || imageViewer == nil {
		return
	}

	tabData, exists := tabDataMap[currentTab]
	if !exists {
		tabData = &TabData{}
		tabDataMap[currentTab] = tabData
	}

	// 保存矩形标记数据（深拷贝）
	tabData.markRects = make([]MarkRect, len(imageViewer.markRects))
	copy(tabData.markRects, imageViewer.markRects)
	tabData.manualRectSelected = imageViewer.manualRectSelected

	// 保存生成的代码
	if codeDisplayEntry != nil {
		tabData.generatedCode = codeDisplayEntry.Text
	}

	// 保存图像查看器引用
	tabData.imageViewer = imageViewer
}

// 恢复标签页数据
func restoreTabData(tab *container.TabItem) {
	tabData, exists := tabDataMap[tab]
	if !exists || tabData.imageViewer == nil {
		// 如果是欢迎页或没有数据，清空图像相关（颜色点全局保留）
		imageViewer = nil
		if rectCoordEntry != nil {
			rectCoordEntry.SetText("")
		}
		if codeDisplayEntry != nil {
			codeDisplayEntry.SetText("")
		}
		return
	}

	// 恢复图像查看器引用
	imageViewer = tabData.imageViewer

	// 恢复矩形坐标输入框
	if rectCoordEntry != nil {
		rectCoordEntry.SetText("")
	}

	// 恢复生成的代码
	if codeDisplayEntry != nil {
		codeDisplayEntry.SetText("")
	}

	// 恢复颜色点列表
	if refreshColorList != nil {
		refreshColorList()
	}
}

// 根据颜色点自动计算矩形范围
func calculateAutoRect() string {
	if len(colorPoints) == 0 {
		return ""
	}

	// 找到所有点的最小和最大坐标
	minX, minY := int(^uint(0)>>1), int(^uint(0)>>1) // int 最大值
	maxX, maxY := 0, 0

	for _, point := range colorPoints {
		// 解析坐标 "x, y" 格式
		coords := strings.Split(point.Position, ", ")
		if len(coords) != 2 {
			continue
		}
		x, err1 := strconv.Atoi(strings.TrimSpace(coords[0]))
		y, err2 := strconv.Atoi(strings.TrimSpace(coords[1]))
		if err1 != nil || err2 != nil {
			continue
		}

		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}

	// 如果只有一个点，创建一个小的矩形范围
	if minX == maxX && minY == maxY {
		// 以该点为中心，创建一个10x10的矩形
		minX -= 5
		minY -= 5
		maxX += 5
		maxY += 5
	}

	// 添加一些边距（10像素）
	minX -= 10
	minY -= 10
	maxX += 10
	maxY += 10

	// 确保坐标不为负数
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}

	// 如果有图像，确保不超出图像范围
	if imageViewer != nil && imageViewer.image != nil {
		bounds := imageViewer.image.Bounds()
		if maxX > bounds.Max.X {
			maxX = bounds.Max.X
		}
		if maxY > bounds.Max.Y {
			maxY = bounds.Max.Y
		}
	}

	return fmt.Sprintf("%d,%d,%d,%d", minX, minY, maxX, maxY)
}

// 获取偏色值，返回字符串（十六进制格式），如果输入无效则返回默认值"101010"
func getColorOffset() string {
	if colorOffsetEntry == nil {
		return "101010" // 默认值
	}

	text := strings.TrimSpace(colorOffsetEntry.Text)
	if text == "" {
		return "101010" // 空值时返回默认值
	}

	// 移除可能的#前缀
	text = strings.TrimPrefix(text, "#")

	// 验证是否为有效的十六进制字符串（6位）
	if len(text) == 6 {
		// 检查是否全是十六进制字符
		for _, c := range text {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return "101010" // 包含非十六进制字符，返回默认值
			}
		}
		return strings.ToUpper(text) // 返回大写格式
	}

	return "101010" // 长度不对，返回默认值
}

// 颜色匹配辅助函数
// 生成找色代码
func generateColorCode() string {
	// 检查是否有颜色点
	if len(colorPoints) == 0 {
		return ""
	}

	// 过滤出勾选的颜色点
	var selectedPoints []ColorPoint
	for _, point := range colorPoints {
		if point.Selected {
			selectedPoints = append(selectedPoints, point)
		}
	}

	// 检查是否有勾选的点
	if len(selectedPoints) == 0 {
		return ""
	}

	// 检查用户是否输入了偏色值
	hasOffset := false
	offset := ""
	if colorOffsetEntry != nil {
		offsetText := strings.TrimSpace(colorOffsetEntry.Text)
		if offsetText != "" {
			hasOffset = true
			offset = getColorOffset() // 获取验证后的偏色值
		}
	}

	// 获取找色模式：比色=固定（绝对）坐标；找色=第一个点绝对坐标，其余点相对第一个点的偏移
	mode := getColorMode()
	isFindColor := mode == "找色"

	// 辅助函数：为点获取最终偏色（优先使用点独立偏色，否则使用全局偏色）
	getFinalOffset := func(p ColorPoint) string {
		pOffset := validateOffset(p.Offset)
		if pOffset != "" {
			return pOffset
		}
		if hasOffset {
			return offset
		}
		return ""
	}

	// 获取区域坐标
	rectText := ""
	if rectCoordEntry != nil {
		rectText = strings.TrimSpace(rectCoordEntry.Text)
	}

	// 解析区域坐标 "x1,y1,x2,y2"
	x1, y1, x2, y2 := "0", "0", "0", "0"
	if rectText != "" {
		coords := strings.Split(rectText, ",")
		if len(coords) == 4 {
			x1 = strings.TrimSpace(coords[0])
			y1 = strings.TrimSpace(coords[1])
			x2 = strings.TrimSpace(coords[2])
			y2 = strings.TrimSpace(coords[3])
		}
	}

	// 解析点坐标文本 "x, y" 为整数
	parsePointPos := func(point ColorPoint) (int, int, bool) {
		coords := strings.Split(point.Position, ", ")
		if len(coords) != 2 {
			return 0, 0, false
		}
		px, err1 := strconv.Atoi(strings.TrimSpace(coords[0]))
		py, err2 := strconv.Atoi(strings.TrimSpace(coords[1]))
		return px, py, err1 == nil && err2 == nil
	}

	// 单点格式化: x|y|color 或 x|y|color-offset
	formatPoint := func(point ColorPoint, xStr, yStr string) string {
		color := strings.TrimPrefix(point.Color, "#")
		color = strings.ToLower(color) // 转为小写
		pFinalOffset := getFinalOffset(point)
		if pFinalOffset != "" {
			return fmt.Sprintf("%s|%s|%s-%s", xStr, yStr, color, pFinalOffset)
		}
		return fmt.Sprintf("%s|%s|%s", xStr, yStr, color)
	}

	firstPoint := selectedPoints[0]
	firstColor := strings.TrimPrefix(firstPoint.Color, "#")
	firstColor = strings.ToLower(firstColor)
	firstFinalOffset := getFinalOffset(firstPoint)
	if firstFinalOffset != "" {
		firstColor = fmt.Sprintf("\"%s-%s\"", firstColor, firstFinalOffset)
	} else {
		firstColor = fmt.Sprintf("\"%s\"", firstColor)
	}

	var parts []string

	if isFindColor {
		// 多点找色：全部输出相对第一个点的偏移坐标，第一个点即 0|0（基准点）
		baseX, baseY := 0, 0
		if bx, by, ok := parsePointPos(selectedPoints[0]); ok {
			baseX, baseY = bx, by
		}
		for i, point := range selectedPoints {
			px, py, ok := parsePointPos(point)
			if !ok {
				continue
			}
			if i == 0 {
				parts = append(parts, formatPoint(point, "0", "0"))
			} else {
				parts = append(parts, formatPoint(point, strconv.Itoa(px-baseX), strconv.Itoa(py-baseY)))
			}
		}
	} else {
		// 多点比色：全部输出固定（绝对）坐标
		for _, point := range selectedPoints {
			px, py, ok := parsePointPos(point)
			if !ok {
				continue
			}
			parts = append(parts, formatPoint(point, strconv.Itoa(px), strconv.Itoa(py)))
		}
	}

	colorStr := strings.Join(parts, ",")
	return fmt.Sprintf("[\"\"]={%s, %s, %s, %s, %s, \"%s\", 100,0,0,0};", x1, y1, x2, y2, firstColor, colorStr)
}

// 更新指定序号的颜色点（序号从1开始）
func updateColorPointAtIndex(index, x, y int, colorHex string) {
	// 确保索引有效（从1开始）
	if index < 1 || index > len(colorPoints) {
		return
	}

	// 获取当前全局偏色作为默认值
	defaultOffset := "101010"
	if colorOffsetEntry != nil && strings.TrimSpace(colorOffsetEntry.Text) != "" {
		defaultOffset = getColorOffset()
	}

	// 更新指定位置的颜色点
	colorPoints[index-1].Position = fmt.Sprintf("%d, %d", x, y)
	colorPoints[index-1].Color = colorHex
	colorPoints[index-1].Offset = defaultOffset
	colorPoints[index-1].Selected = true

	// 刷新表格显示
	if refreshColorList != nil {
		refreshColorList()
	}

	// 如果当前没有手动框选矩形，自动更新矩形范围
	if imageViewer != nil && !imageViewer.manualRectSelected && rectCoordEntry != nil {
		autoRect := calculateAutoRect()
		if autoRect != "" {
			rectCoordEntry.SetText(autoRect)
		}
	}
}

// 添加点到颜色列表
func addColorPointToList(x, y int, colorHex string, selected bool) {
	// 获取当前全局偏色作为默认值
	defaultOffset := "101010"
	if colorOffsetEntry != nil && strings.TrimSpace(colorOffsetEntry.Text) != "" {
		defaultOffset = getColorOffset()
	}

	// 创建新的颜色点
	newPoint := ColorPoint{
		ID:       len(colorPoints) + 1,
		Position: fmt.Sprintf("%d, %d", x, y),
		Color:    colorHex,
		Offset:   defaultOffset,
		Selected: selected,
	}

	// 添加到列表
	colorPoints = append(colorPoints, newPoint)

	// 刷新表格显示
	if refreshColorList != nil {
		refreshColorList()
	}

	// 如果当前没有手动框选矩形，自动更新矩形范围
	if imageViewer != nil && !imageViewer.manualRectSelected && rectCoordEntry != nil {
		autoRect := calculateAutoRect()
		if autoRect != "" {
			rectCoordEntry.SetText(autoRect)
		}
	}
}

// 添加矩形标记 - 确保只有一个矩形
func (v *ImageViewer) AddRect(x1, y1, x2, y2 int, c color.Color) {
	// 清空现有的所有矩形
	v.markRects = v.markRects[:0]

	// 添加新矩形
	v.markRects = append(v.markRects, MarkRect{X1: x1, Y1: y1, X2: x2, Y2: y2, Color: c})

	// 标记为手动框选
	v.manualRectSelected = true

	// 更新坐标显示框
	if rectCoordEntry != nil {
		// 确保坐标是左上角到右下角的顺序
		minX := min(x1, x2)
		minY := min(y1, y2)
		maxX := max(x1, x2)
		maxY := max(y1, y2)

		// 更新编辑框文本，仅显示四个坐标
		rectCoordEntry.SetText(fmt.Sprintf("%d,%d,%d,%d", minX, minY, maxX, maxY))
	}

	v.Refresh() // 刷新视图以显示新矩形
}

// 更新区域坐标为所有点的外包围盒
func (v *ImageViewer) updateBoundingBox() {
	// 如果是手动框选的区域，不自动更新
	if v.manualRectSelected {
		return
	}

	// 如果没有点，不更新
	if len(v.markPoints) == 0 {
		return
	}

	// 找出所有点的最小和最大坐标
	minX := v.markPoints[0].X
	minY := v.markPoints[0].Y
	maxX := v.markPoints[0].X
	maxY := v.markPoints[0].Y

	for _, point := range v.markPoints[1:] {
		if point.X < minX {
			minX = point.X
		}
		if point.X > maxX {
			maxX = point.X
		}
		if point.Y < minY {
			minY = point.Y
		}
		if point.Y > maxY {
			maxY = point.Y
		}
	}

	// 向外扩展10像素
	minX -= 10
	minY -= 10
	maxX += 10
	maxY += 10

	// 确保不超出图像边界
	if v.image != nil {
		bounds := v.image.Bounds()
		if minX < bounds.Min.X {
			minX = bounds.Min.X
		}
		if minY < bounds.Min.Y {
			minY = bounds.Min.Y
		}
		if maxX > bounds.Max.X {
			maxX = bounds.Max.X
		}
		if maxY > bounds.Max.Y {
			maxY = bounds.Max.Y
		}
	}

	// 更新坐标显示框
	if rectCoordEntry != nil {
		rectCoordEntry.SetText(fmt.Sprintf("%d,%d,%d,%d", minX, minY, maxX, maxY))
	}
}

// 清除所有标记
func (v *ImageViewer) ClearMarks() {
	v.markPoints = v.markPoints[:0]
	v.testPoints = v.testPoints[:0]
	v.testTexts = v.testTexts[:0]
	v.markRects = v.markRects[:0]

	// 重置手动框选标志
	v.manualRectSelected = false

	// 如果处于二值化状态，恢复原始图像
	if v.isBinarized && v.originalImage != nil {
		v.image = v.originalImage
		v.displayImage.Image = v.originalImage
		v.isBinarized = false
	}

	// 清空颜色点列表
	colorPoints = colorPoints[:0]

	// 使用fyne.Do确保在主线程中更新UI
	fyne.Do(func() {
		// 清空坐标显示框
		if rectCoordEntry != nil {
			rectCoordEntry.SetText("")
		}

		// 刷新表格显示
		if refreshColorList != nil {
			refreshColorList()
		}

		v.Refresh()
	})
}

func (v *ImageViewer) ClearTestOverlay() {
	v.testPoints = v.testPoints[:0]
	v.testTexts = v.testTexts[:0]
	v.Refresh()
}

func (v *ImageViewer) SetTestOverlay(points []MarkPoint, texts []MarkText) {
	v.testPoints = points
	v.testTexts = texts
	v.Refresh()
}

// 设置左键按下回调函数
func (v *ImageViewer) SetOnMouseDown(callback func(x, y int)) {
	v.onMouseDown = callback
}

// 设置左键弹起回调函数
func (v *ImageViewer) SetOnMouseUp(callback func(x, y int)) {
	v.onMouseUp = callback
}

// 设置右键点击回调函数
func (v *ImageViewer) SetOnRightClick(callback func(x, y int)) {
	v.onRightClick = callback
}

// 实现MouseDown方法，处理左键按下
func (v *ImageViewer) MouseDown(e *desktop.MouseEvent) {
	if v.image == nil || e.Button != desktop.MouseButtonPrimary {
		return
	}
	if v.window != nil {
		v.window.Canvas().Focus(v)
	}

	// ★ 吸管模式：点一下取该像素颜色为画笔色（一次性，取完自动退出吸管）
	if v.eyedropperMode {
		mx := int(float32(e.Position.X) / v.pixelScale)
		my := int(float32(e.Position.Y) / v.pixelScale)
		b := v.image.Bounds()
		if mx >= b.Min.X && mx < b.Max.X && my >= b.Min.Y && my < b.Max.Y {
			r8, g8, b8 := colorRGB8(v.image.At(mx, my))
			v.paintColor = color.RGBA{uint8(r8), uint8(g8), uint8(b8), 255}
			if v.onEyedrop != nil {
				v.onEyedrop(v.paintColor)
			}
		}
		v.eyedropperMode = false
		return
	}

	var mouseX, mouseY int

	// 仅在方向键移动后的抑制窗口内优先使用 lastMouseX/Y（此时事件坐标可能尚未同步），
	// 其余情况一律用鼠标事件坐标换算，避免漂移污染取色/框选位置
	if v.lastMouseX >= 0 && v.lastMouseY >= 0 && v.kbSuppressActive {
		mouseX = v.lastMouseX
		mouseY = v.lastMouseY
	} else {
		// 使用鼠标事件的坐标，将视觉坐标转换为原始图像坐标
		mouseX = int(float32(e.Position.X) / v.pixelScale)
		mouseY = int(float32(e.Position.Y) / v.pixelScale)
	}

	// ★ 画笔涂抹模式：按下即落笔（快照 → 圆形涂抹），不走框选逻辑
	if v.paintMode {
		v.paintSnapshot()
		v.paintCircle(mouseX, mouseY, v.brushSize)
		v.lastPaintX, v.lastPaintY = mouseX, mouseY
		v.isDragging = true
		v.tempRect = nil
		v.Refresh()
		return
	}

	// 记录按下的坐标（不限制在图像范围内，允许框选超出图像边界的区域）
	v.mouseDownX = mouseX
	v.mouseDownY = mouseY
	v.isDragging = true

	// 清除任何现有的临时矩形
	v.tempRect = nil

	// 鼠标按下时隐藏放大镜
	if v.magnifier != nil {
		v.magnifier.Hide()
	}

	// 调用回调函数
	if v.onMouseDown != nil {
		v.onMouseDown(mouseX, mouseY)
	}
}

// 实现MouseUp方法，处理左键弹起
func (v *ImageViewer) MouseUp(e *desktop.MouseEvent) {
	if v.image == nil || e.Button != desktop.MouseButtonPrimary || !v.isDragging {
		return
	}
	// ★ 画笔涂抹模式：抬笔即结束，不走框选逻辑
	if v.paintMode {
		v.isDragging = false
		return
	}

	var mouseX, mouseY int

	// 仅在方向键移动后的抑制窗口内优先使用 lastMouseX/Y，其余情况用事件坐标（防止漂移固化到取色点）
	if v.lastMouseX >= 0 && v.lastMouseY >= 0 && v.kbSuppressActive {
		mouseX = v.lastMouseX
		mouseY = v.lastMouseY
	} else {
		// 使用鼠标事件的坐标，将视觉坐标转换为原始图像坐标
		mouseX = int(float32(e.Position.X) / v.pixelScale)
		mouseY = int(float32(e.Position.Y) / v.pixelScale)
	}

	// 计算按下和弹起位置的距离
	dist := distance(v.mouseDownX, v.mouseDownY, mouseX, mouseY)

	// 如果距离大于4像素，保留矩形；否则画点
	if dist > 4 {
		// 如果有临时矩形，将其添加到矩形列表中（会先清空现有矩形）
		if v.tempRect != nil {
			// 使用AddRect方法而不是直接操作数组，这样可以同时更新坐标显示
			v.AddRect(v.tempRect.X1, v.tempRect.Y1, v.tempRect.X2, v.tempRect.Y2, v.tempRect.Color)
			v.window.Clipboard().SetContent(fmt.Sprintf("%d, %d, %d, %d", v.tempRect.X1, v.tempRect.Y1, v.tempRect.X2, v.tempRect.Y2))
		}
	}

	// 清除临时矩形
	v.tempRect = nil

	// 重置拖动状态
	v.isDragging = false

	// 鼠标弹起时重新显示放大镜
	if v.magnifier != nil {
		v.magnifier.Show()
	}

	// 刷新视图
	v.Refresh()

	// 调用回调函数
	if v.onMouseUp != nil {
		v.onMouseUp(mouseX, mouseY)
	}
}

// 实现TappedSecondary接口方法，处理右键点击
func (v *ImageViewer) TappedSecondary(e *fyne.PointEvent) {
	if v.image == nil {
		return
	}
	if v.window != nil {
		v.window.Canvas().Focus(v)
	}

	mouseX := int(e.Position.X)
	mouseY := int(e.Position.Y)

	// 将视觉坐标转换为原始图像坐标
	if v.pixelScale > 0 && v.pixelScale != 1.0 {
		mouseX = int(float32(mouseX) / v.pixelScale)
		mouseY = int(float32(mouseY) / v.pixelScale)
	}

	if triggerTestAction != nil {
		triggerTestAction()
	}

	// 调用回调函数
	if v.onRightClick != nil {
		v.onRightClick(mouseX, mouseY)
	}
}

// 设置图像
func (v *ImageViewer) SetImage(img image.Image) {
	// 保存当前颜色点列表（深拷贝）
	if v.originalImage != nil && len(colorPoints) > 0 {
		// 创建新的颜色点列表并复制现有数据
		newColorPoints := make([]ColorPoint, len(colorPoints))
		copy(newColorPoints, colorPoints)
		// 保存到当前标签页的数据中
		if currentTab != nil {
			tabData, exists := tabDataMap[currentTab]
			if !exists {
				tabData = &TabData{}
				tabDataMap[currentTab] = tabData
			}
			tabData.colorPoints = newColorPoints
		}
	}

	v.image = img
	v.originalImage = img // 保存原始图像
	v.rotationDegrees = 0 // 重置旋转角度
	v.displayImage.Image = img
	v.Refresh()
}

// 旋转图像到指定角度
func (v *ImageViewer) RotateImage(degrees int) {
	if v.originalImage == nil {
		return
	}

	// 确保角度在0-359范围内
	degrees = degrees % 360

	// 如果角度是0，直接使用原图
	if degrees == 0 {
		v.image = v.originalImage
		v.displayImage.Image = v.image
		v.rotationDegrees = degrees
		v.Refresh()
		return
	}

	// 获取原始图像的尺寸
	bounds := v.originalImage.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	var rotated *image.NRGBA // 使用NRGBA而不是RGBA

	// 根据角度执行不同的旋转
	switch degrees {
	case 90:
		// 创建新的画布，宽高互换
		rotated = image.NewNRGBA(image.Rect(0, 0, height, width))

		// 执行90度旋转 (x,y) -> (height-y-1, x)
		for x := 0; x < width; x++ {
			for y := 0; y < height; y++ {
				rotated.Set(height-y-1, x, v.originalImage.At(x, y))
			}
		}

	case 180:
		// 创建新的画布，宽高保持不变
		rotated = image.NewNRGBA(image.Rect(0, 0, width, height))

		// 执行180度旋转 (x,y) -> (width-x-1, height-y-1)
		for x := 0; x < width; x++ {
			for y := 0; y < height; y++ {
				rotated.Set(width-x-1, height-y-1, v.originalImage.At(x, y))
			}
		}

	case 270:
		// 创建新的画布，宽高互换
		rotated = image.NewNRGBA(image.Rect(0, 0, height, width))

		// 执行270度旋转 (x,y) -> (y, width-x-1)
		for x := 0; x < width; x++ {
			for y := 0; y < height; y++ {
				rotated.Set(y, width-x-1, v.originalImage.At(x, y))
			}
		}
	}

	v.image = rotated
	v.displayImage.Image = rotated
	v.rotationDegrees = degrees

	// 清除所有绘制的标记
	v.ClearMarks()

	v.Refresh()
}

// 实现CreateRenderer方法
func (v *ImageViewer) CreateRenderer() fyne.WidgetRenderer {
	// 创建一个透明的临时矩形对象
	tempRectObj := canvas.NewRectangle(color.RGBA{255, 0, 0, 255})
	tempRectObj.StrokeWidth = 1
	tempRectObj.StrokeColor = color.RGBA{255, 0, 0, 255}
	tempRectObj.FillColor = color.RGBA{0, 0, 0, 0} // 透明填充
	tempRectObj.Hide()                             // 初始时隐藏

	return &imageViewerRenderer{
		viewer:   v,
		objects:  []fyne.CanvasObject{v.displayImage, tempRectObj},
		points:   []fyne.CanvasObject{},
		texts:    []fyne.CanvasObject{},
		rects:    []fyne.CanvasObject{},
		tempRect: tempRectObj,
	}
}

// 处理鼠标移动事件
func (v *ImageViewer) MouseMoved(e *desktop.MouseEvent) {
	if v.image == nil {
		return
	}

	// 方向键移动光标后的抑制窗口：丢弃与期望位置不符的过期/合并鼠标事件，
	// 匹配到期望位置或超时后恢复正常处理（替代旧的 50ms 定时恢复，大图重绘慢时不再漏网）
	if v.kbSuppressActive {
		if time.Now().After(v.kbSuppressDeadline) {
			v.kbSuppressActive = false
		} else {
			sx := int(float32(e.Position.X) / v.pixelScale)
			sy := int(float32(e.Position.Y) / v.pixelScale)
			if sx == v.kbSuppressX && sy == v.kbSuppressY {
				// 事件位置与键盘移动后的期望位置一致，恢复正常处理
				v.kbSuppressActive = false
			} else {
				// 过期事件（光标程序移动期间产生），丢弃
				return
			}
		}
	}

	mouseX := int(e.Position.X)
	mouseY := int(e.Position.Y)

	// 将视觉坐标转换为原始图像坐标（因为 ImageViewer 尺寸是缩放后的）
	origMouseX := int(float32(mouseX) / v.pixelScale)
	origMouseY := int(float32(mouseY) / v.pixelScale)

	// 检测鼠标是否真的移动了（避免滚动导致的坐标变化）
	// 使用原始坐标进行比较
	mouseMoved := (origMouseX != v.lastMouseX || origMouseY != v.lastMouseY)

	// 更新上次鼠标位置
	v.lastMouseX = origMouseX
	v.lastMouseY = origMouseY

	// ★ 画笔涂抹模式：拖动沿轨迹补涂（插值防断线），不走框选/放大镜逻辑
	if v.paintMode && v.isDragging {
		if mouseMoved {
			v.paintStroke(v.lastPaintX, v.lastPaintY, origMouseX, origMouseY)
			v.lastPaintX, v.lastPaintY = origMouseX, origMouseY
			v.Refresh()
		}
		return
	}

	// 如果正在拖动，更新临时矩形
	if v.isDragging {
		// 不限制鼠标坐标在图像范围内，允许框选超出图像边界的区域
		clampedX := origMouseX
		clampedY := origMouseY

		// 更新或创建临时矩形
		if v.tempRect == nil {
			v.tempRect = &MarkRect{
				X1:    v.mouseDownX,
				Y1:    v.mouseDownY,
				X2:    clampedX,
				Y2:    clampedY,
				Color: color.RGBA{255, 0, 0, 255}, // 红色
			}
		} else {
			// 更新临时矩形的终点
			v.tempRect.X2 = clampedX
			v.tempRect.Y2 = clampedY
		}

		// 刷新视图以更新临时矩形的显示
		v.Refresh()
	}

	// 更新放大镜（不在拖动状态时）
	if !v.isDragging && v.magnifier != nil && v.scrollContainer != nil {
		// origMouseX, origMouseY 是原始图像坐标
		// 需要转换为相对于可见窗口的坐标（需要乘以 pixelScale）
		scrollOffset := v.scrollContainer.Offset
		viewX := float32(origMouseX)*v.pixelScale - scrollOffset.X
		viewY := float32(origMouseY)*v.pixelScale - scrollOffset.Y

		// 检查图像坐标是否在范围内
		imgBounds := v.image.Bounds()
		if origMouseX >= imgBounds.Min.X && origMouseX < imgBounds.Max.X &&
			origMouseY >= imgBounds.Min.Y && origMouseY < imgBounds.Max.Y {
			// 更新放大镜，传递图像坐标和可见窗口坐标
			v.magnifier.Update(v.image, origMouseX, origMouseY, viewX, viewY)
		} else {
			v.magnifier.Hide()
		}
	}

	// 调用回调函数（如果有）
	if mouseMoved && v.onMouseMove != nil {
		v.onMouseMove(origMouseX, origMouseY)
	}
}

// 实现桌面光标接口的其他方法
func (v *ImageViewer) MouseIn(e *desktop.MouseEvent) {
	v.mouseInWidget = true
	if v.window != nil {
		v.window.Canvas().Focus(v)
	}

	// 鼠标移入时更新放大镜位置
	if v.magnifier != nil && v.image != nil {
		// 获取鼠标位置并转换为原始图像坐标
		mouseX := int(e.Position.X)
		mouseY := int(e.Position.Y)

		// 将视觉坐标转换为原始图像坐标
		origMouseX := mouseX
		origMouseY := mouseY
		if v.pixelScale > 0 && v.pixelScale != 1.0 {
			origMouseX = int(float32(mouseX) / v.pixelScale)
			origMouseY = int(float32(mouseY) / v.pixelScale)
		}

		// 更新鼠标位置
		v.lastMouseX = origMouseX
		v.lastMouseY = origMouseY

		// 检查图像坐标是否在范围内
		imgBounds := v.image.Bounds()
		if origMouseX >= imgBounds.Min.X && origMouseX < imgBounds.Max.X &&
			origMouseY >= imgBounds.Min.Y && origMouseY < imgBounds.Max.Y {
			// 计算相对于可见窗口的坐标
			if v.scrollContainer != nil {
				scrollOffset := v.scrollContainer.Offset
				viewX := float32(origMouseX)*v.pixelScale - scrollOffset.X
				viewY := float32(origMouseY)*v.pixelScale - scrollOffset.Y
				v.magnifier.Update(v.image, origMouseX, origMouseY, viewX, viewY)
			} else {
				v.magnifier.Update(v.image, origMouseX, origMouseY, float32(mouseX), float32(mouseY))
			}
		} else {
			v.magnifier.Hide()
		}
	}
}

func (v *ImageViewer) MouseOut() {
	v.mouseInWidget = false
	if v.magnifier != nil && !v.isDragging {
		v.magnifier.Hide()
	}
}

// 实现 Focusable 接口
func (v *ImageViewer) FocusGained()     {}
func (v *ImageViewer) FocusLost()       {}
func (v *ImageViewer) TypedRune(r rune) {}

// func (v *ImageViewer) TypedShortcut(shortcut fyne.Shortcut) {
// 	cs, ok := shortcut.(*desktop.CustomShortcut)
// 	if !ok {
// 		return
// 	}
// 	if (cs.Modifier&fyne.KeyModifierAlt) != 0 && (cs.KeyName == fyne.KeyReturn || cs.KeyName == fyne.KeyEnter) {
// 		if triggerClearAction != nil {
// 			triggerClearAction()
// 		}
// 	}
// }

func (v *ImageViewer) TypedShortcut(shortcut fyne.Shortcut) {
	cs, ok := shortcut.(*desktop.CustomShortcut)
	if !ok {
		return
	}
	if (cs.Modifier&fyne.KeyModifierAlt) != 0 && (cs.KeyName == fyne.KeyReturn || cs.KeyName == fyne.KeyEnter) {
		if triggerClearAction != nil {
			triggerClearAction()
		}
		return
	}

	// ★ Ctrl+Z：撤销画笔涂抹（进入涂抹模式后生效）
	if (cs.Modifier&fyne.KeyModifierControl) != 0 && cs.KeyName == fyne.KeyZ {
		v.UndoPaint()
		return
	}

	// 处理 Alt+1 到 Alt+10 快捷键：更新对应序号 11-20 的颜色点
	if (cs.Modifier&fyne.KeyModifierAlt) != 0 &&
		(cs.KeyName == fyne.Key1 || cs.KeyName == fyne.Key2 || cs.KeyName == fyne.Key3 ||
			cs.KeyName == fyne.Key4 || cs.KeyName == fyne.Key5 || cs.KeyName == fyne.Key6 ||
			cs.KeyName == fyne.Key7 || cs.KeyName == fyne.Key8 || cs.KeyName == fyne.Key9 ||
			cs.KeyName == fyne.Key0) {

		// 确定对应的序号（11-20）
		var pointIndex int
		switch cs.KeyName {
		case fyne.Key1:
			pointIndex = 11
		case fyne.Key2:
			pointIndex = 12
		case fyne.Key3:
			pointIndex = 13
		case fyne.Key4:
			pointIndex = 14
		case fyne.Key5:
			pointIndex = 15
		case fyne.Key6:
			pointIndex = 16
		case fyne.Key7:
			pointIndex = 17
		case fyne.Key8:
			pointIndex = 18
		case fyne.Key9:
			pointIndex = 19
		case fyne.Key0:
			pointIndex = 20
		}

		if v.lastMouseX >= 0 && v.lastMouseY >= 0 {
			bounds := v.image.Bounds()
			mouseX := v.lastMouseX
			mouseY := v.lastMouseY
			if mouseX < bounds.Min.X {
				mouseX = bounds.Min.X
			}
			if mouseX >= bounds.Max.X {
				mouseX = bounds.Max.X - 1
			}
			if mouseY < bounds.Min.Y {
				mouseY = bounds.Min.Y
			}
			if mouseY >= bounds.Max.Y {
				mouseY = bounds.Max.Y - 1
			}

			// 如果颜色点列表长度不足，先扩展到足够长度
			for len(colorPoints) < pointIndex {
				// 添加占位点（位置为0,0，颜色为黑色）
				addColorPointToList(0, 0, "#000000", false)
			}

			// 获取当前位置的颜色
			if v.image != nil {
				bounds := v.image.Bounds()
				if mouseX >= bounds.Min.X && mouseX < bounds.Max.X && mouseY >= bounds.Min.Y && mouseY < bounds.Max.Y {
					pixelColor := v.image.At(mouseX, mouseY)
					r, g, b, _ := pixelColor.RGBA()
					r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
					hexColor := fmt.Sprintf("#%02X%02X%02X", r8, g8, b8)

					// 更新对应序号的颜色点
					updateColorPointAtIndex(pointIndex, mouseX, mouseY, hexColor)
				}
			}
		} else if v.window != nil {
			showFindMultiResultDialog(v.window, "请先把鼠标移到图片上，再按 Alt+数字键取色")
		}
		return
	}
}

// 处理键盘按键事件
func (v *ImageViewer) TypedKey(key *fyne.KeyEvent) {
	if v.image == nil || v.window == nil {
		return
	}

	if key.Name == fyne.KeyReturn || key.Name == fyne.KeyEnter {
		if triggerGenerateCode != nil {
			triggerGenerateCode()
		}
		return
	}

	if key.Name == fyne.KeySpace {
		if v.lastMouseX >= 0 && v.lastMouseY >= 0 {
			bounds := v.image.Bounds()
			mouseX := v.lastMouseX
			mouseY := v.lastMouseY
			if mouseX < bounds.Min.X {
				mouseX = bounds.Min.X
			}
			if mouseX >= bounds.Max.X {
				mouseX = bounds.Max.X - 1
			}
			if mouseY < bounds.Min.Y {
				mouseY = bounds.Min.Y
			}
			if mouseY >= bounds.Max.Y {
				mouseY = bounds.Max.Y - 1
			}
			v.AddPoint(mouseX, mouseY, nil)
		} else if v.window != nil {
			showFindMultiResultDialog(v.window, "请先把鼠标移到图片上，再按空格取色")
		}
		return
	}

	// 处理数字键 1-10：更新对应序号的颜色点（而不是添加新点）
	if key.Name == fyne.Key1 || key.Name == fyne.Key2 || key.Name == fyne.Key3 ||
		key.Name == fyne.Key4 || key.Name == fyne.Key5 || key.Name == fyne.Key6 ||
		key.Name == fyne.Key7 || key.Name == fyne.Key8 || key.Name == fyne.Key9 ||
		key.Name == fyne.Key0 {

		// 确定对应的序号（1-10）
		var pointIndex int
		switch key.Name {
		case fyne.Key1:
			pointIndex = 1
		case fyne.Key2:
			pointIndex = 2
		case fyne.Key3:
			pointIndex = 3
		case fyne.Key4:
			pointIndex = 4
		case fyne.Key5:
			pointIndex = 5
		case fyne.Key6:
			pointIndex = 6
		case fyne.Key7:
			pointIndex = 7
		case fyne.Key8:
			pointIndex = 8
		case fyne.Key9:
			pointIndex = 9
		case fyne.Key0:
			pointIndex = 10
		}

		if v.lastMouseX >= 0 && v.lastMouseY >= 0 {
			bounds := v.image.Bounds()
			mouseX := v.lastMouseX
			mouseY := v.lastMouseY
			if mouseX < bounds.Min.X {
				mouseX = bounds.Min.X
			}
			if mouseX >= bounds.Max.X {
				mouseX = bounds.Max.X - 1
			}
			if mouseY < bounds.Min.Y {
				mouseY = bounds.Min.Y
			}
			if mouseY >= bounds.Max.Y {
				mouseY = bounds.Max.Y - 1
			}

			// 如果颜色点列表长度不足，先扩展到足够长度
			for len(colorPoints) < pointIndex {
				// 添加占位点（位置为0,0，颜色为黑色）
				addColorPointToList(0, 0, "#000000", false)
			}

			// 获取当前位置的颜色
			if v.image != nil {
				bounds := v.image.Bounds()
				if mouseX >= bounds.Min.X && mouseX < bounds.Max.X && mouseY >= bounds.Min.Y && mouseY < bounds.Max.Y {
					pixelColor := v.image.At(mouseX, mouseY)
					r, g, b, _ := pixelColor.RGBA()
					r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
					hexColor := fmt.Sprintf("#%02X%02X%02X", r8, g8, b8)

					// 更新对应序号的颜色点
					updateColorPointAtIndex(pointIndex, mouseX, mouseY, hexColor)
				}
			}
		} else if v.window != nil {
			showFindMultiResultDialog(v.window, "请先把鼠标移到图片上，再按数字键取色")
		}
		return
	}

	if !v.mouseInWidget {
		return
	}

	// 如果 lastMouseX 和 lastMouseY 还没有初始化，使用图像中心作为起始位置
	if v.lastMouseX < 0 || v.lastMouseY < 0 {
		bounds := v.image.Bounds()
		v.lastMouseX = (bounds.Min.X + bounds.Max.X) / 2
		v.lastMouseY = (bounds.Min.Y + bounds.Max.Y) / 2
	}

	// 定义移动步长（像素）
	step := 1

	// 根据按键方向计算新的图像坐标
	newX := v.lastMouseX
	newY := v.lastMouseY

	switch key.Name {
	case fyne.KeyUp:
		newY -= step
	case fyne.KeyDown:
		newY += step
	case fyne.KeyLeft:
		newX -= step
	case fyne.KeyRight:
		newX += step
	default:
		return // 不是方向键、空格键或回车键，直接返回
	}

	// 限制坐标在图像范围内
	bounds := v.image.Bounds()
	if newX < bounds.Min.X {
		newX = bounds.Min.X
	}
	if newX >= bounds.Max.X {
		newX = bounds.Max.X - 1
	}
	if newY < bounds.Min.Y {
		newY = bounds.Min.Y
	}
	if newY >= bounds.Max.Y {
		newY = bounds.Max.Y - 1
	}

	// 更新内部状态
	v.lastMouseX = newX
	v.lastMouseY = newY

	// 开启抑制窗口（匹配到期望位置或超时后自动恢复，不再用 sleep goroutine）
	v.kbSuppressActive = true
	v.kbSuppressDeadline = time.Now().Add(250 * time.Millisecond)
	v.kbSuppressX = newX
	v.kbSuppressY = newY

	// 先滚动容器到新位置，确保键盘移动的坐标在可见区域内
	if v.scrollContainer != nil {
		// 获取当前滚动偏移（基于原始图像坐标）
		scrollOffset := v.scrollContainer.Offset
		viewWidth := v.scrollContainer.Size().Width / v.pixelScale
		viewHeight := v.scrollContainer.Size().Height / v.pixelScale

		// 计算需要的滚动偏移，使新位置在视图中心（或边缘）
		targetX := float32(newX) - viewWidth/2
		targetY := float32(newY) - viewHeight/2

		// 限制滚动范围
		bounds := v.image.Bounds()
		contentWidth := float32(bounds.Max.X - bounds.Min.X)
		contentHeight := float32(bounds.Max.Y - bounds.Min.Y)

		if targetX < 0 {
			targetX = 0
		} else if targetX > contentWidth-viewWidth {
			targetX = contentWidth - viewWidth
		}
		if targetY < 0 {
			targetY = 0
		} else if targetY > contentHeight-viewHeight {
			targetY = contentHeight - viewHeight
		}

		// 只有当需要滚动时才更新
		if targetX != scrollOffset.X || targetY != scrollOffset.Y {
			v.scrollContainer.ScrollToOffset(fyne.NewPos(targetX, targetY))
		}
	}

	// 绝对定位系统光标到目标像素中心（内部处理窗口客户区原点和 DPI 缩放，
	// 相比旧的相对移动无累积误差、无 DPI 系数偏差）
	// 必须在滚动之后调用，保证 widget 的绝对位置已是滚动后的最终值
	moveSystemCursorStride(newX, newY, v.pixelScale, v.window, v)

	// 主动更新放大镜（因为移动鼠标可能不会触发MouseMoved事件）
	if v.magnifier != nil && v.scrollContainer != nil {
		// 计算相对于可见窗口的坐标
		// 注意：需要乘以 pixelScale，因为可见区域显示的是缩放后的图像
		scrollOffset := v.scrollContainer.Offset
		viewX := float32(newX)*v.pixelScale - scrollOffset.X
		viewY := float32(newY)*v.pixelScale - scrollOffset.Y

		// 更新放大镜
		v.magnifier.Update(v.image, newX, newY, viewX, viewY)
	}
}

func (v *ImageViewer) CursorType() desktop.Cursor {
	return desktop.CrosshairCursor
}

// 获取图像尺寸
func (v *ImageViewer) ImageSize() (int, int) {
	if v.image == nil {
		return 0, 0
	}
	bounds := v.image.Bounds()
	return bounds.Max.X - bounds.Min.X, bounds.Max.Y - bounds.Min.Y
}

// applyPixelScale 更新缩放比例，并按比例重定位滚动偏移，
// 使视口中心对应的图像坐标在缩放前后保持不变（避免缩放后坐标换算错位一次）
func (v *ImageViewer) applyPixelScale(scale float32) {
	old := v.pixelScale
	if old <= 0 {
		old = 1
	}
	if v.scrollContainer != nil {
		off := v.scrollContainer.Offset
		v.pixelScale = scale
		v.scrollContainer.ScrollToOffset(fyne.NewPos(off.X*scale/old, off.Y*scale/old))
	} else {
		v.pixelScale = scale
	}
	v.Refresh()
}

// 图像查看器渲染器
type imageViewerRenderer struct {
	viewer   *ImageViewer
	objects  []fyne.CanvasObject
	points   []fyne.CanvasObject // 用于绘制点的对象
	texts    []fyne.CanvasObject
	rects    []fyne.CanvasObject // 用于绘制矩形的对象
	tempRect fyne.CanvasObject   // 用于绘制临时矩形的对象
}

func (r *imageViewerRenderer) MinSize() fyne.Size {
	if r.viewer.image == nil {
		return fyne.NewSize(0, 0)
	}
	bounds := r.viewer.image.Bounds()
	// 返回缩放后的尺寸，使 ImageViewer 的事件接收区域与放大后的图像一致
	scale := r.viewer.pixelScale
	return fyne.NewSize(
		float32(bounds.Max.X-bounds.Min.X)*scale,
		float32(bounds.Max.Y-bounds.Min.Y)*scale,
	)
}

func (r *imageViewerRenderer) Layout(size fyne.Size) {
	// size 已经是缩放后的尺寸（MinSize 返回 原始*pixelScale）
	// displayImage 直接使用即可，不需要再乘以 scale
	r.viewer.displayImage.Resize(size)

	// 调整点标记的位置（标记在缩放后的图像上，所以坐标需要乘以 scale）
	r.updatePointsLayout()
	r.updateTextsLayout()

	// 调整矩形标记的位置
	r.updateRectsLayout()
}

func (r *imageViewerRenderer) Refresh() {
	r.viewer.displayImage.Refresh()

	// 更新点标记和矩形标记
	r.updatePoints()
	r.updateTexts()
	r.updateRects()
	r.updateTempRect() // 更新临时矩形

	// 确保我们有所有的对象
	allObjects := []fyne.CanvasObject{r.viewer.displayImage, r.tempRect}
	allObjects = append(allObjects, r.points...)
	allObjects = append(allObjects, r.texts...)
	allObjects = append(allObjects, r.rects...)
	r.objects = allObjects

	// 强制刷新所有点和矩形的大小和位置
	r.updatePointsLayout()
	r.updateTextsLayout()
	r.updateRectsLayout()

	// 刷新所有点和矩形
	for _, p := range r.points {
		p.Refresh()
	}
	for _, t := range r.texts {
		t.Refresh()
	}
	for _, rect := range r.rects {
		rect.Refresh()
	}
	r.tempRect.Refresh()
}

// 更新点标记的位置
func (r *imageViewerRenderer) updatePointsLayout() {
	// 如果没有图像，不做任何事
	if r.viewer.image == nil {
		return
	}

	scale := r.viewer.pixelScale
	// 遍历所有点对象并调整它们的位置和大小
	total := len(r.viewer.markPoints) + len(r.viewer.testPoints)
	for i, p := range r.points {
		if i >= total {
			break
		}
		var point MarkPoint
		if i < len(r.viewer.markPoints) {
			point = r.viewer.markPoints[i]
		} else {
			point = r.viewer.testPoints[i-len(r.viewer.markPoints)]
		}
		// 标记点大小和位置随缩放因子调整
		p.Resize(fyne.NewSize(2*scale, 2*scale))
		p.Move(fyne.NewPos(float32(point.X)*scale, float32(point.Y)*scale))
	}
}

func (r *imageViewerRenderer) updateTextsLayout() {
	if r.viewer.image == nil {
		return
	}
	scale := r.viewer.pixelScale
	for i, obj := range r.texts {
		if i >= len(r.viewer.testTexts) {
			break
		}
		t := r.viewer.testTexts[i]
		obj.Move(fyne.NewPos(float32(t.X)*scale, float32(t.Y)*scale))
	}
}

// 更新矩形标记的位置
func (r *imageViewerRenderer) updateRectsLayout() {
	// 如果没有图像，不做任何事
	if r.viewer.image == nil {
		return
	}

	// 遍历所有矩形对象并调整它们的位置和大小
	scale := r.viewer.pixelScale
	for i, rect := range r.rects {
		if i < len(r.viewer.markRects) {
			markRect := r.viewer.markRects[i]

			// 计算矩形的位置和尺寸，并应用缩放
			x := float32(min(markRect.X1, markRect.X2)) * scale
			y := float32(min(markRect.Y1, markRect.Y2)) * scale
			width := float32(abs(markRect.X2-markRect.X1)) * scale
			height := float32(abs(markRect.Y2-markRect.Y1)) * scale

			// 确保宽高至少为1
			if width < 1 {
				width = 1
			}
			if height < 1 {
				height = 1
			}

			// 移动和调整矩形大小
			rect.Move(fyne.NewPos(x, y))
			rect.Resize(fyne.NewSize(width, height))
		}
	}
}

// 更新点标记（添加新点，移除旧点）
func (r *imageViewerRenderer) updatePoints() {
	// 清除现有点
	r.points = make([]fyne.CanvasObject, 0, len(r.viewer.markPoints)+len(r.viewer.testPoints))

	scale := r.viewer.pixelScale
	// 为每个标记点创建一个方块（大小随缩放因子调整）
	for _, point := range r.viewer.markPoints {
		rect := canvas.NewRectangle(point.Color)
		rect.SetMinSize(fyne.NewSize(2*scale, 2*scale))
		rect.Resize(fyne.NewSize(2*scale, 2*scale))
		rect.Move(fyne.NewPos(float32(point.X)*scale, float32(point.Y)*scale))
		r.points = append(r.points, rect)
	}
	for _, point := range r.viewer.testPoints {
		rect := canvas.NewRectangle(point.Color)
		rect.SetMinSize(fyne.NewSize(2*scale, 2*scale))
		rect.Resize(fyne.NewSize(2*scale, 2*scale))
		rect.Move(fyne.NewPos(float32(point.X)*scale, float32(point.Y)*scale))
		r.points = append(r.points, rect)
	}
}

func (r *imageViewerRenderer) updateTexts() {
	r.texts = make([]fyne.CanvasObject, 0, len(r.viewer.testTexts))
	scale := r.viewer.pixelScale
	for _, t := range r.viewer.testTexts {
		txt := canvas.NewText(t.Text, t.Color)
		txt.TextSize = 12 * scale
		txt.TextStyle = fyne.TextStyle{Monospace: true}
		txt.Move(fyne.NewPos(float32(t.X)*scale, float32(t.Y)*scale))
		r.texts = append(r.texts, txt)
	}
}

// 更新矩形标记（添加新矩形，移除旧矩形）
func (r *imageViewerRenderer) updateRects() {
	// 清除现有矩形
	r.rects = make([]fyne.CanvasObject, 0, len(r.viewer.markRects))

	// 为每个标记矩形创建一个矩形对象
	scale := r.viewer.pixelScale
	for _, markRect := range r.viewer.markRects {
		rect := canvas.NewRectangle(markRect.Color)
		rect.StrokeWidth = 1
		rect.StrokeColor = markRect.Color
		rect.FillColor = color.RGBA{0, 0, 0, 0} // 透明填充

		// 计算矩形的位置和尺寸，并应用缩放
		x := float32(min(markRect.X1, markRect.X2)) * scale
		y := float32(min(markRect.Y1, markRect.Y2)) * scale
		width := float32(abs(markRect.X2-markRect.X1)) * scale
		height := float32(abs(markRect.Y2-markRect.Y1)) * scale

		// 确保宽高至少为1
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}

		// 设置矩形的位置和尺寸
		rect.Move(fyne.NewPos(x, y))
		rect.Resize(fyne.NewSize(width, height))

		r.rects = append(r.rects, rect)
	}
}

// 更新临时矩形
func (r *imageViewerRenderer) updateTempRect() {
	// 如果没有临时矩形数据，则隐藏临时矩形对象
	if r.viewer.tempRect == nil {
		r.tempRect.Hide()
		return
	}

	// 显示临时矩形
	r.tempRect.Show()

	// 获取临时矩形数据
	tempRect := r.viewer.tempRect

	// 计算矩形的位置和尺寸，并应用缩放
	scale := r.viewer.pixelScale
	x := float32(min(tempRect.X1, tempRect.X2)) * scale
	y := float32(min(tempRect.Y1, tempRect.Y2)) * scale
	width := float32(abs(tempRect.X2-tempRect.X1)) * scale
	height := float32(abs(tempRect.Y2-tempRect.Y1)) * scale

	// 确保宽高至少为1
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	// 移动和调整矩形大小
	rect := r.tempRect.(*canvas.Rectangle)
	rect.Move(fyne.NewPos(x, y))
	rect.Resize(fyne.NewSize(width, height))
}

// 辅助函数：计算两点之间的欧几里得距离
func distance(x1, y1, x2, y2 int) float64 {
	dx := float64(x2 - x1)
	dy := float64(y2 - y1)
	return math.Sqrt(dx*dx + dy*dy)
}

// 辅助函数：取两个数中的最小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 辅助函数：取绝对值
func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func (r *imageViewerRenderer) Objects() []fyne.CanvasObject {
	// 确保所有点对象和矩形对象都包含在返回的切片中
	return r.objects
}

func (r *imageViewerRenderer) Destroy() {}

// 计算颜色的反色（使用增强的反色算法）
func getInverseColor(c color.Color) color.Color {
	// 转换为RGBA
	r, g, b, a := c.RGBA()

	// 转换到0-255范围
	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)
	a8 := uint8(a >> 8)

	// 使用增强的反色算法
	adjust := func(value uint8) uint8 {
		normalized := float64(value) / 255.0
		if normalized < 0.5 {
			normalized = normalized * normalized // 增强低值区域
		} else {
			normalized = 1 - (1-normalized)*(1-normalized) // 增强高值区域
		}
		return uint8((1 - normalized) * 255) // 取反并返回
	}

	invR := adjust(r8)
	invG := adjust(g8)
	invB := adjust(b8)

	return color.RGBA{invR, invG, invB, a8}
}

// 检查颜色是否接近中间灰色
func isNearMidGray(r, g, b uint8) bool {
	// 计算与中间灰色(127,127,127)的差距
	const midGray = 127
	const threshold = 30 // 阈值，可以调整

	rDiff := abs(int(r) - midGray)
	gDiff := abs(int(g) - midGray)
	bDiff := abs(int(b) - midGray)

	// 如果RGB三个通道都接近中间值，且彼此接近，则认为是接近中间灰色
	avgDiff := (rDiff + gDiff + bDiff) / 3
	maxDiff := max(max(rDiff, gDiff), bDiff)

	return avgDiff < threshold && maxDiff < threshold*1.5
}

// 计算颜色亮度
func getBrightness(r, g, b uint8) uint8 {
	// 使用感知亮度公式: 0.299*R + 0.587*G + 0.114*B
	return uint8(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b))
}

// 取两个数中的较大值
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// 创建新的图像查看器
func NewImageViewer() *ImageViewer {
	viewer := &ImageViewer{
		displayImage:    canvas.NewImageFromImage(nil),
		markPoints:      make([]MarkPoint, 0),
		testPoints:      make([]MarkPoint, 0),
		testTexts:       make([]MarkText, 0),
		markRects:       make([]MarkRect, 0),
		tempRect:        nil, // 初始化为nil，表示没有临时矩形
		rotationDegrees: 0,   // 初始化旋转角度为0
		lastMouseX:      -1,  // 初始化为-1，确保第一次移动会被检测到
		lastMouseY:      -1,
		pixelScale:      float32(wholeSicale), // 默认像素缩放为2倍放大
		brushSize:       3,                    // 画笔默认直径（像素）
		onMouseMove: func(x, y int) {
			//fmt.Printf("鼠标移动: X=%d, Y=%d\n", x, y)
		},
		onMouseDown: func(x, y int) {
			//fmt.Printf("左键按下: X=%d, Y=%d\n", x, y)
		},
		onMouseUp: func(x, y int) {
			//fmt.Printf("左键弹起: X=%d, Y=%d\n", x, y)
		},
		onRightClick: func(x, y int) {
			//fmt.Printf("右键点击: X=%d, Y=%d\n", x, y)
		},
	}
	viewer.displayImage.FillMode = canvas.ImageFillStretch
	viewer.ExtendBaseWidget(viewer)
	return viewer
}

// 添加点标记
func (v *ImageViewer) AddPoint(x, y int, c color.Color) {
	// 如果提供了颜色，使用提供的颜色
	// 如果没有提供颜色（nil）且有图像，则根据背景亮度选择高对比度颜色
	markColor := c
	var originalColor color.Color // 存储原始颜色

	if c == nil && v.image != nil {
		// 确保坐标在图像范围内
		bounds := v.image.Bounds()
		if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
			// 获取图像中该点的颜色
			pixelColor := v.image.At(x, y)
			originalColor = pixelColor // 保存原始颜色

			// 使用反色作为标记颜色
			markColor = getInverseColor(pixelColor)
		} else {
			// 如果坐标超出范围，使用默认黑色的反色（白色）
			originalColor = color.RGBA{0, 0, 0, 255} // 默认黑色
			markColor = getInverseColor(originalColor)
		}
	} else {
		originalColor = color.RGBA{0, 0, 0, 255} // 默认黑色
	}

	v.markPoints = append(v.markPoints, MarkPoint{X: x, Y: y, Color: markColor})

	// 获取原始颜色的十六进制表示
	r, g, b, _ := originalColor.RGBA()
	r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
	hexColor := fmt.Sprintf("#%02X%02X%02X", r8, g8, b8)

	// 将点的信息添加到颜色点列表中，使用原始颜色
	addColorPointToList(x, y, hexColor, true)

	// 更新区域包围盒
	v.updateBoundingBox()

	v.Refresh() // 刷新视图以显示新点
}

// 批量添加NxN点阵的颜色点
func (v *ImageViewer) AddGridPoints(startX, startY, cols, rows, spacing int) {
	if v.image == nil {
		return
	}

	bounds := v.image.Bounds()

	// 双层循环遍历点阵
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			// 计算当前点的坐标
			x := startX + col*spacing
			y := startY + row*spacing

			// 检查坐标是否在图像范围内
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue // 跳过超出范围的点
			}

			// 获取该点的颜色
			pixelColor := v.image.At(x, y)
			r, g, b, _ := pixelColor.RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)

			// 使用反色作为标记颜色
			markColor := getInverseColor(pixelColor)

			// 添加标记点
			v.markPoints = append(v.markPoints, MarkPoint{X: x, Y: y, Color: markColor})

			// 添加到颜色点列表
			hexColor := fmt.Sprintf("#%02X%02X%02X", r8, g8, b8)
			addColorPointToList(x, y, hexColor, true)
		}
	}

	// 更新区域包围盒
	v.updateBoundingBox()

	// 批量添加完成后刷新一次
	v.Refresh()
}

// ── ★ 画笔涂抹（异形图）2026-09-30 ─────────────────────────────────
// 规则对齐大漠/OP 找图：模板【四角同色且该色占比≥50%】⇒ 该色为背景，匹配时只比
// 前景像素（带每通道偏色）。因此进入画笔模式时自动取【四角最常见色】为涂抹色，
// 并自动把四角补涂成该色（异形贴到角落时四角检测才不会失效）。PNG 真 alpha
// 透明也兼容（本工具涂抹用纯色，保存后大漠/引擎通吃）。

// ensurePaintNRGBA 把当前图像转成可写的 *image.NRGBA（originalImage 保持不动）
func (v *ImageViewer) ensurePaintNRGBA() {
	if n, ok := v.image.(*image.NRGBA); ok {
		v.paintImg = n
		return
	}
	v.paintImg = cloneToNRGBA(v.image)
	v.image = v.paintImg
}

// cornerBgColor 取四角像素中出现最多的颜色（作为默认涂抹背景色）
func (v *ImageViewer) cornerBgColor() color.RGBA {
	if v.paintImg == nil {
		return color.RGBA{255, 0, 255, 255}
	}
	b := v.paintImg.Bounds()
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
			px := v.paintImg.NRGBAAt(c[0], c[1])
			best = color.RGBA{px.R, px.G, px.B, 255}
		}
	}
	return best
}

// paintSnapshot 保存涂抹前快照供 Ctrl+Z 撤销（深度 5）
func (v *ImageViewer) paintSnapshot() {
	if v.paintImg == nil {
		return
	}
	cp := *v.paintImg
	cp.Pix = append([]uint8(nil), v.paintImg.Pix...)
	v.undoStack = append(v.undoStack, &cp)
	if len(v.undoStack) > 5 {
		v.undoStack = v.undoStack[1:]
	}
}

// UndoPaint 撤销上一次涂抹
func (v *ImageViewer) UndoPaint() {
	if len(v.undoStack) == 0 {
		return
	}
	v.paintImg = v.undoStack[len(v.undoStack)-1]
	v.undoStack = v.undoStack[:len(v.undoStack)-1]
	v.image = v.paintImg
	v.Refresh()
}

// paintCircle 以 (cx,cy) 为中心涂【直径 d】的实心圆（图像像素坐标，自动裁边）。
// ★ d=1 恰好涂 1 个像素（2026-09-30 改：原半径语义 d=1 会涂 3×3，用户反馈对不上）
func (v *ImageViewer) paintCircle(cx, cy, d int) {
	if v.paintImg == nil {
		return
	}
	if d < 1 {
		d = 1
	}
	if d > 64 {
		d = 64
	}
	half := d / 2
	cxc := float32((d - 1) % 2) * 0.5 // 偶数直径时中心偏移半像素
	cyc := cxc
	r2 := float32(d) * float32(d) / 4.0
	b := v.paintImg.Bounds()
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
				off := v.paintImg.PixOffset(x, y)
				v.paintImg.Pix[off+0] = v.paintColor.R
				v.paintImg.Pix[off+1] = v.paintColor.G
				v.paintImg.Pix[off+2] = v.paintColor.B
				v.paintImg.Pix[off+3] = 255
			}
		}
	}
}

// paintStroke 两点间连线涂抹（按步长插值，快速拖动不断线）
func (v *ImageViewer) paintStroke(x1, y1, x2, y2 int) {
	dx, dy := x2-x1, y2-y1
	dist := math.Sqrt(float64(dx*dx + dy*dy))
	step := v.brushSize / 2
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
		v.paintCircle(x1+int(float64(dx)*t), y1+int(float64(dy)*t), v.brushSize)
	}
}

// SetPaintMode 进入/退出画笔涂抹模式。
// 进入时：图像转 NRGBA 可写、涂抹色=四角最常见色、自动补涂四角（快照可撤销）。
func (v *ImageViewer) SetPaintMode(on bool) {
	if on {
		if v.image == nil {
			return
		}
		if v.brushSize <= 0 {
			v.brushSize = 6
		}
		v.ensurePaintNRGBA()
		v.paintColor = v.cornerBgColor()
		v.paintSnapshot()
		b := v.paintImg.Bounds()
		v.paintCircle(b.Min.X, b.Min.Y, v.brushSize)
		v.paintCircle(b.Max.X-1, b.Min.Y, v.brushSize)
		v.paintCircle(b.Min.X, b.Max.Y-1, v.brushSize)
		v.paintCircle(b.Max.X-1, b.Max.Y-1, v.brushSize)
		v.paintMode = true
		v.Refresh()
	} else {
		v.paintMode = false
		v.isDragging = false
		v.undoStack = nil
	}
}

// 裁剪图像的辅助函数
func cropImage(img image.Image, rect image.Rectangle) image.Image {
	bounds := img.Bounds()
	croppedRect := image.Rect(0, 0, rect.Dx(), rect.Dy())
	croppedImg := image.NewNRGBA(croppedRect) // 使用NRGBA而不是RGBA

	// 复制像素
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
				croppedImg.Set(x-rect.Min.X, y-rect.Min.Y, img.At(x, y))
			}
		}
	}

	return croppedImg
}

// 将任意图像转换为NRGBA格式
func convertToNRGBA(src image.Image) *image.NRGBA {
	// 如果已经是NRGBA，直接返回
	if nrgba, ok := src.(*image.NRGBA); ok {
		return nrgba
	}

	// 创建新的NRGBA图像
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)

	// 复制像素
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}

	return dst
}

func cloneToNRGBA(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
	return dst
}

func saveScreenshotPNG(img image.Image, dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	now := time.Now()
	filename := fmt.Sprintf("screenshot_%s_%03d.png", now.Format("20060102_150405"), now.Nanosecond()/1e6)
	outPath := filepath.Join(dir, filename)
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return "", err
	}
	return outPath, nil
}
