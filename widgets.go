package main

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// 固定高度的容器布局
type fixedHeightContainer struct {
	widget.BaseWidget
	content fyne.CanvasObject
	height  float32
}

func newFixedHeightContainer(content fyne.CanvasObject, height float32) *fixedHeightContainer {
	c := &fixedHeightContainer{content: content, height: height}
	c.ExtendBaseWidget(c)
	return c
}

func (c *fixedHeightContainer) CreateRenderer() fyne.WidgetRenderer {
	return &fixedHeightRenderer{container: c}
}

type fixedHeightRenderer struct {
	container *fixedHeightContainer
}

func (r *fixedHeightRenderer) Layout(size fyne.Size) {
	r.container.content.Resize(fyne.NewSize(size.Width, r.container.height))
	r.container.content.Move(fyne.NewPos(0, 0))
}

func (r *fixedHeightRenderer) MinSize() fyne.Size {
	return fyne.NewSize(r.container.content.MinSize().Width, r.container.height)
}

func (r *fixedHeightRenderer) Refresh() {
	r.container.content.Refresh()
}

func (r *fixedHeightRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.container.content}
}

func (r *fixedHeightRenderer) Destroy() {}

// 自定义可点击表格行
type ClickableTableRow struct {
	widget.BaseWidget
	background    *canvas.Rectangle
	content       *fyne.Container
	onTapped      func()
	id            int
	isHighlighted bool
}

func newClickableTableRow(bg color.Color, content *fyne.Container, onTapped func()) *ClickableTableRow {
	row := &ClickableTableRow{
		background: canvas.NewRectangle(bg),
		content:    content,
		onTapped:   onTapped,
	}
	row.ExtendBaseWidget(row)
	return row
}

func (r *ClickableTableRow) TappedSecondary(*fyne.PointEvent) {
	// 右键点击，可以实现其他功能
}

func (r *ClickableTableRow) CreateRenderer() fyne.WidgetRenderer {
	r.background.SetMinSize(fyne.NewSize(250, 30))
	return &clickableTableRowRenderer{
		row:     r,
		objects: []fyne.CanvasObject{r.background, r.content},
	}
}

type clickableTableRowRenderer struct {
	row     *ClickableTableRow
	objects []fyne.CanvasObject
}

func (r *clickableTableRowRenderer) Destroy() {}

func (r *clickableTableRowRenderer) Layout(size fyne.Size) {
	r.row.background.Resize(size)
	r.row.content.Resize(size)
}

func (r *clickableTableRowRenderer) MinSize() fyne.Size {
	return fyne.NewSize(250, 30)
}

func (r *clickableTableRowRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *clickableTableRowRenderer) Refresh() {
	r.Layout(r.row.Size())
}

// 解析十六进制颜色字符串为RGBA颜色
func hexToColor(hex string) color.Color {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return color.RGBA{100, 100, 255, 255} // 默认蓝色
	}

	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)

	return color.RGBA{uint8(r), uint8(g), uint8(b), 255}
}

// 带动画的截图按钮
type AnimatedScreenshotButton struct {
	widget.BaseWidget
	button        *widget.Button
	animationView *canvas.Raster
	isLoading     bool
	scale         float32
	scaleDir      float32 // 1 为放大, -1 为缩小
	animationStop chan bool
	onTapped      func()
}

func NewAnimatedScreenshotButton(text string, icon fyne.Resource, tapped func()) *AnimatedScreenshotButton {
	btn := &AnimatedScreenshotButton{
		scale:    0.5,
		scaleDir: 1.0,
		onTapped: tapped,
	}
	btn.button = widget.NewButtonWithIcon(text, icon, func() {
		if !btn.isLoading {
			tapped()
		}
	})
	btn.button.Importance = widget.MediumImportance

	// 创建动画视图
	btn.animationView = canvas.NewRaster(btn.drawAnimation)
	btn.animationView.SetMinSize(fyne.NewSize(20, 20))
	btn.animationView.Hide()

	btn.ExtendBaseWidget(btn)
	return btn
}

// 绘制动画圆圈
func (b *AnimatedScreenshotButton) drawAnimation(w, h int) image.Image {
	if !b.isLoading || w == 0 || h == 0 {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// 圆圈中心位置
	centerX := w / 2
	centerY := h / 2
	baseRadius := 6.0
	radius := baseRadius * float64(b.scale)

	// 绘制填充的圆
	fillColor := color.NRGBA{66, 150, 255, 200}
	strokeColor := color.NRGBA{66, 150, 255, 255}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x - centerX)
			dy := float64(y - centerY)
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist <= radius {
				img.Set(x, y, fillColor)
			} else if dist <= radius+1.5 {
				// 描边
				img.Set(x, y, strokeColor)
			}
		}
	}

	return img
}

func (b *AnimatedScreenshotButton) StartLoading() {
	if b.isLoading {
		return
	}
	b.isLoading = true
	b.button.Disable()
	b.scale = 0.5
	b.scaleDir = 1.0
	b.animationView.Show()
	b.animationStop = make(chan bool)

	// 启动动画
	go func() {
		ticker := time.NewTicker(16 * time.Millisecond) // 约60fps，更流畅
		defer ticker.Stop()

		for {
			select {
			case <-b.animationStop:
				return
			case <-ticker.C:
				// 更新缩放 - 从0.5到1.5之间缩放，速度更快
				b.scale += b.scaleDir * 0.15
				if b.scale >= 1.6 {
					b.scale = 1.6
					b.scaleDir = -1.0
				} else if b.scale <= 0.4 {
					b.scale = 0.4
					b.scaleDir = 1.0
				}

				// 使用fyne.Do确保在主线程中刷新UI
				fyne.Do(func() {
					b.animationView.Refresh()
				})
			}
		}
	}()
}

func (b *AnimatedScreenshotButton) StopLoading() {
	if !b.isLoading {
		return
	}
	b.isLoading = false

	if b.animationStop != nil {
		close(b.animationStop)
		b.animationStop = nil
	}

	// 使用fyne.Do确保在主线程中更新UI
	fyne.Do(func() {
		b.button.Enable()
		b.scale = 0.5
		b.scaleDir = 1.0
		b.animationView.Hide()
		b.Refresh()
	})
}

func (b *AnimatedScreenshotButton) IsLoading() bool {
	return b.isLoading
}

func (b *AnimatedScreenshotButton) CreateRenderer() fyne.WidgetRenderer {
	return &animatedScreenshotButtonRenderer{
		button:        b,
		buttonWidget:  b.button,
		animationView: b.animationView,
		objects:       []fyne.CanvasObject{b.button, b.animationView},
	}
}

type animatedScreenshotButtonRenderer struct {
	button        *AnimatedScreenshotButton
	buttonWidget  *widget.Button
	animationView *canvas.Raster
	objects       []fyne.CanvasObject
}

func (r *animatedScreenshotButtonRenderer) Layout(size fyne.Size) {
	r.buttonWidget.Resize(size)

	// 动画圆圈位置：在按钮右侧
	animSize := float32(20)
	animX := size.Width - animSize - 8
	animY := (size.Height - animSize) / 2

	r.animationView.Move(fyne.NewPos(animX, animY))
	r.animationView.Resize(fyne.NewSize(animSize, animSize))
}

func (r *animatedScreenshotButtonRenderer) MinSize() fyne.Size {
	return r.buttonWidget.MinSize()
}

func (r *animatedScreenshotButtonRenderer) Refresh() {
	r.buttonWidget.Refresh()
	r.animationView.Refresh()
	r.Layout(r.button.Size())
}

func (r *animatedScreenshotButtonRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *animatedScreenshotButtonRenderer) Destroy() {
	if r.button.animationStop != nil {
		close(r.button.animationStop)
	}
}

// 完全自定义的颜色复选框
type ColorCheck struct {
	widget.BaseWidget
	Checked   bool
	OnChanged func(bool)
	Color     color.Color
}

// 创建新的彩色复选框
func NewColorCheck(checked bool, fillColor color.Color, changed func(bool)) *ColorCheck {
	check := &ColorCheck{
		Checked:   checked,
		OnChanged: changed,
		Color:     fillColor,
	}
	check.ExtendBaseWidget(check)
	return check
}

// 处理点击事件
func (c *ColorCheck) Tapped(*fyne.PointEvent) {
	c.Checked = !c.Checked
	if c.OnChanged != nil {
		c.OnChanged(c.Checked)
	}
	c.Refresh()
}

// 鼠标进入时显示指针
func (c *ColorCheck) MouseIn(*desktop.MouseEvent) {
	c.Refresh()
}

// 鼠标离开时恢复
func (c *ColorCheck) MouseOut() {
	c.Refresh()
}

// 实现桌面鼠标悬停接口
func (c *ColorCheck) MouseMoved(*desktop.MouseEvent) {}
func (c *ColorCheck) CursorType() desktop.Cursor {
	return desktop.PointerCursor
}

// 创建渲染器
func (c *ColorCheck) CreateRenderer() fyne.WidgetRenderer {
	// 创建绘制组件
	box := canvas.NewRectangle(color.NRGBA{0, 0, 0, 0})
	box.StrokeWidth = 1                               // 边框保持1像素
	box.StrokeColor = color.NRGBA{160, 160, 160, 200} // 使用灰色边框，不那么刺眼

	// 创建选中标记 - 使用两条线组成勾号
	// 勾的颜色根据背景色而定，初始化时可以先用白色，Refresh时会更新
	checkLine1 := canvas.NewLine(color.White)
	checkLine1.StrokeWidth = 2 // 回到标准线宽

	checkLine2 := canvas.NewLine(color.White)
	checkLine2.StrokeWidth = 2 // 回到标准线宽

	renderer := &colorCheckRenderer{
		check:      c,
		box:        box,
		checkLine1: checkLine1,
		checkLine2: checkLine2,
		objects:    []fyne.CanvasObject{box, checkLine1, checkLine2},
	}

	// 如果已经选中，立即设置正确的对比色
	if c.Checked {
		contrastColor := getContrastColor(c.Color)
		checkLine1.StrokeColor = contrastColor
		checkLine2.StrokeColor = contrastColor
	}

	return renderer
}

// 自定义渲染器
type colorCheckRenderer struct {
	check      *ColorCheck
	box        *canvas.Rectangle
	checkLine1 *canvas.Line
	checkLine2 *canvas.Line
	objects    []fyne.CanvasObject
}

func (r *colorCheckRenderer) MinSize() fyne.Size {
	return fyne.NewSize(16, 16) // 从18x18减小到16x16
}

func (r *colorCheckRenderer) Layout(size fyne.Size) {
	// 计算复选框的尺寸和位置
	boxSize := fyne.Min(size.Width, size.Height)
	r.box.Resize(fyne.NewSize(boxSize, boxSize))
	r.box.Move(fyne.NewPos(0, (size.Height-boxSize)/2))

	// 调整勾选图标位置，使其在复选框中更加居中
	// 整体向右下方移动一些

	// 第一条线 - 从左往右稍微向下倾斜
	r.checkLine1.Position1 = fyne.NewPos(boxSize*0.25, boxSize*0.5)  // 右移并下移起点
	r.checkLine1.Position2 = fyne.NewPos(boxSize*0.45, boxSize*0.65) // 右移并下移终点

	// 第二条线 - 从中间向右上方延伸
	r.checkLine2.Position1 = fyne.NewPos(boxSize*0.45, boxSize*0.65) // 与第一条线终点一致
	r.checkLine2.Position2 = fyne.NewPos(boxSize*0.8, boxSize*0.35)  // 右移并下移终点
}

func (r *colorCheckRenderer) Refresh() {
	if r.check.Checked {
		// 如果选中，使用指定的颜色填充，边框设为0使其不可见
		r.box.FillColor = r.check.Color
		r.box.StrokeWidth = 0 // 选中时不显示边框
		r.checkLine1.Hidden = false
		r.checkLine2.Hidden = false

		// 根据背景色亮度决定勾的颜色
		contrastColor := getContrastColor(r.check.Color)
		r.checkLine1.StrokeColor = contrastColor
		r.checkLine2.StrokeColor = contrastColor
	} else {
		// 如果未选中，使用透明填充，恢复边框
		r.box.FillColor = color.NRGBA{0, 0, 0, 0}
		r.box.StrokeWidth = 1                               // 未选中时显示边框
		r.box.StrokeColor = color.NRGBA{160, 160, 160, 200} // 确保未选中时边框是灰色
		r.checkLine1.Hidden = true
		r.checkLine2.Hidden = true
	}

	r.box.Refresh()
	r.checkLine1.Refresh()
	r.checkLine2.Refresh()
}

func (r *colorCheckRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *colorCheckRenderer) Destroy() {}

// 自定义勾选框（深色背景下高亮可见，无状态残留）
type customCheck struct {
	widget.BaseWidget
	label      string
	checked    bool
	labelColor color.Color
	box        *canvas.Rectangle
	labelTxt   *canvas.Text
	onChanged  func(bool)
}

func newCustomCheck(label string, labelColor color.Color, onChanged func(bool)) *customCheck {
	c := &customCheck{label: label, labelColor: labelColor, onChanged: onChanged}
	c.ExtendBaseWidget(c)
	c.box = canvas.NewRectangle(color.NRGBA{80, 80, 90, 255})
	c.box.CornerRadius = 3
	c.box.SetMinSize(fyne.NewSize(18, 18))
	c.labelTxt = canvas.NewText(label, labelColor)
	c.labelTxt.TextSize = 14
	return c
}

func (c *customCheck) CreateRenderer() fyne.WidgetRenderer {
	content := container.NewHBox(c.box, c.labelTxt)
	return &customCheckRenderer{c: c, content: content}
}

func (c *customCheck) Tapped(_ *fyne.PointEvent) {
	c.checked = !c.checked
	c.applyColors()
	c.Refresh()
	if c.onChanged != nil {
		c.onChanged(c.checked)
	}
}

func (c *customCheck) SetChecked(v bool) {
	c.checked = v
	c.applyColors()
	c.Refresh()
}

func (c *customCheck) applyColors() {
	if c.checked {
		c.box.FillColor = color.NRGBA{100, 180, 255, 255}
		c.labelTxt.Color = color.NRGBA{255, 255, 255, 255}
	} else {
		c.box.FillColor = color.NRGBA{80, 80, 90, 255}
		c.labelTxt.Color = c.labelColor
	}
}

type customCheckRenderer struct {
	c       *customCheck
	content *fyne.Container
}

func (r *customCheckRenderer) Layout(size fyne.Size) { r.content.Resize(size) }
func (r *customCheckRenderer) MinSize() fyne.Size    { return r.content.MinSize() }
func (r *customCheckRenderer) Refresh()              { r.content.Refresh() }
func (r *customCheckRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.content}
}
func (r *customCheckRenderer) Destroy() {}
