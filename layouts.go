package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// 带权重的网格布局，让不同列有不同的宽度
type weightedGridLayout struct {
	cols    int
	weights []float32 // 每列的权重
}

func (g *weightedGridLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	minW, minH := float32(0), float32(0)
	for i, obj := range objects {
		if i >= g.cols {
			break
		}
		childSize := obj.MinSize()
		minW += childSize.Width
		minH = fyne.Max(minH, childSize.Height)
	}
	return fyne.NewSize(minW, minH)
}

func (g *weightedGridLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	// 计算总权重
	totalWeight := float32(0)
	for _, weight := range g.weights {
		totalWeight += weight
	}

	// 计算单位宽度
	unitWidth := containerSize.Width / totalWeight

	// 布局对象
	x := float32(0)
	for i, obj := range objects {
		if i >= g.cols {
			break
		}

		// 计算此列宽度
		colWidth := unitWidth * g.weights[i]

		// 设置对象大小和位置
		obj.Move(fyne.NewPos(x, 0))
		obj.Resize(fyne.NewSize(colWidth, containerSize.Height))

		// 更新x坐标
		x += colWidth
	}
}

// 自定义布局，确保内容始终在左上角
type topLeftLayout struct{}

func (t *topLeftLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	minSize := fyne.NewSize(0, 0)
	for _, obj := range objects {
		objSize := obj.MinSize()
		minSize.Width = fyne.Max(minSize.Width, objSize.Width)
		minSize.Height = fyne.Max(minSize.Height, objSize.Height)
	}
	return minSize
}

func (t *topLeftLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	pos := fyne.NewPos(0, 0) // 始终从左上角(0,0)开始放置
	for _, obj := range objects {
		size := obj.MinSize()
		obj.Resize(size)
		obj.Move(pos)
	}
}

type imageAreaBackground struct {
	widget.BaseWidget
	viewer *ImageViewer
}

func newImageAreaBackground(viewer *ImageViewer) *imageAreaBackground {
	bg := &imageAreaBackground{viewer: viewer}
	bg.ExtendBaseWidget(bg)
	return bg
}

func (b *imageAreaBackground) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.NRGBA{0, 0, 0, 0})
	return widget.NewSimpleRenderer(rect)
}

func (b *imageAreaBackground) MinSize() fyne.Size {
	// 返回缩放后的尺寸，与 ImageViewer 的 MinSize 一致
	if b.viewer == nil || b.viewer.image == nil {
		return fyne.NewSize(0, 0)
	}
	bounds := b.viewer.image.Bounds()
	scale := b.viewer.pixelScale
	return fyne.NewSize(
		float32(bounds.Max.X-bounds.Min.X)*scale,
		float32(bounds.Max.Y-bounds.Min.Y)*scale,
	)
}

func (b *imageAreaBackground) Tapped(*fyne.PointEvent) {
	if b.viewer != nil && b.viewer.window != nil {
		b.viewer.window.Canvas().Focus(b.viewer)
	}
}

func (b *imageAreaBackground) TappedSecondary(e *fyne.PointEvent) {
	if b.viewer == nil || b.viewer.image == nil {
		return
	}
	if b.viewer.window != nil {
		b.viewer.window.Canvas().Focus(b.viewer)
	}
	b.viewer.mouseInWidget = true

	mouseX := int(e.Position.X)
	mouseY := int(e.Position.Y)

	bounds := b.viewer.image.Bounds()
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

	if triggerTestAction != nil {
		triggerTestAction()
	}
	if b.viewer.onRightClick != nil {
		b.viewer.onRightClick(mouseX, mouseY)
	}
}

func (b *imageAreaBackground) MouseIn(*desktop.MouseEvent) {
	if b.viewer != nil {
		b.viewer.mouseInWidget = true
		if b.viewer.window != nil {
			b.viewer.window.Canvas().Focus(b.viewer)
		}
	}
}

func (b *imageAreaBackground) MouseMoved(e *desktop.MouseEvent) {
	if b.viewer == nil || b.viewer.image == nil {
		return
	}
	// 将鼠标事件转发到 viewer
	b.viewer.MouseMoved(e)
}

func (b *imageAreaBackground) MouseOut() {
	if b.viewer != nil {
		b.viewer.mouseInWidget = false
	}
}

type imageAreaLayout struct{}

func (t *imageAreaLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	minSize := fyne.NewSize(0, 0)
	if len(objects) <= 1 {
		for _, obj := range objects {
			objSize := obj.MinSize()
			minSize.Width = fyne.Max(minSize.Width, objSize.Width)
			minSize.Height = fyne.Max(minSize.Height, objSize.Height)
		}
		return minSize
	}
	for _, obj := range objects[1:] {
		objSize := obj.MinSize()
		minSize.Width = fyne.Max(minSize.Width, objSize.Width)
		minSize.Height = fyne.Max(minSize.Height, objSize.Height)
	}
	return minSize
}

func (t *imageAreaLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	if len(objects) == 0 {
		return
	}
	// 背景占满容器
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(containerSize)

	// ImageViewer 使用自己的 MinSize（原始图像尺寸）
	for _, obj := range objects[1:] {
		size := obj.MinSize()
		obj.Resize(size)
		obj.Move(fyne.NewPos(0, 0))
	}
}

// 前缀弹性横向布局：前面子控件按 MinSize 排，最后一个填满剩余宽度，间距由 padding 指定
type prefixStretchHBoxLayout struct {
	padding float32 // 子控件之间的间距
}

func (p *prefixStretchHBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	h := float32(0)
	for _, o := range objects {
		s := o.MinSize()
		w += s.Width
		if s.Height > h {
			h = s.Height
		}
	}
	if len(objects) > 1 {
		w += p.padding * float32(len(objects)-1)
	}
	return fyne.NewSize(w, h)
}

func (p *prefixStretchHBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	x := float32(0)
	// 前 N-1 个控件按 MinSize 排列
	for i := 0; i < len(objects)-1; i++ {
		o := objects[i]
		s := o.MinSize()
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSize(s.Width, size.Height))
		x += s.Width + p.padding
	}
	// 最后一个控件撑满剩余宽度
	last := objects[len(objects)-1]
	last.Move(fyne.NewPos(x, 0))
	last.Resize(fyne.NewSize(size.Width-x, size.Height))
}

// 固定宽度布局
type fixedWidthLayout struct {
	width           float32
	padding         float32 // 水平内边距
	verticalSpacing float32 // 垂直间距
}

func (f *fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for i, o := range objects {
		childSize := o.MinSize()
		height += childSize.Height

		// 除了最后一个元素，其他元素后面都加上间距
		if i < len(objects)-1 {
			height += f.verticalSpacing
		}
	}
	return fyne.NewSize(f.width, height)
}

func (f *fixedWidthLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	pos := fyne.NewPos(0, 0)
	for i, o := range objects {
		var size fyne.Size
		minSize := o.MinSize()

		if _, isBorder := o.(*fyne.Container); isBorder && len(objects) == 1 {
			// 如果是BorderLayout容器且是唯一子元素，让它占据整个高度
			size = fyne.NewSize(f.width, containerSize.Height)
		} else {
			// 所有控件宽度相同，但会应用内边距
			size = fyne.NewSize(f.width-(f.padding*2), minSize.Height)
		}

		// 对大多数控件应用水平内边距
		objPos := fyne.NewPos(pos.X+f.padding, pos.Y)

		// 布局和尺寸调整
		o.Move(objPos)
		o.Resize(size)

		// 更新下一个元素的位置，添加垂直间距
		pos = pos.Add(fyne.NewPos(0, minSize.Height))
		if i < len(objects)-1 {
			pos = pos.Add(fyne.NewPos(0, f.verticalSpacing))
		}
	}
}
