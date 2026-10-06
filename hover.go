package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// spHoverArea: 智能取色窗口专用透明覆盖层，支持鼠标跟踪+方向键+放大镜
type spHoverArea struct {
	widget.BaseWidget
	onMouseMove func(pos fyne.Position)
	onDrag      func(pos fyne.Position)
	onTap       func(pos fyne.Position)
	onKey       func(key *fyne.KeyEvent)
}

func newSpHoverArea(onMove func(fyne.Position), onDrag func(fyne.Position), onTap func(fyne.Position), onKey func(*fyne.KeyEvent)) *spHoverArea {
	h := &spHoverArea{onMouseMove: onMove, onDrag: onDrag, onTap: onTap, onKey: onKey}
	h.ExtendBaseWidget(h)
	return h
}

func (h *spHoverArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
func (h *spHoverArea) MouseIn(*desktop.MouseEvent) {}
func (h *spHoverArea) MouseMoved(e *desktop.MouseEvent) {
	if h.onMouseMove != nil {
		h.onMouseMove(e.Position)
	}
}
func (h *spHoverArea) MouseOut() {}
func (h *spHoverArea) Dragged(e *fyne.DragEvent) {
	if h.onDrag != nil {
		h.onDrag(e.Position)
	}
}
func (h *spHoverArea) DragEnd() {}
func (h *spHoverArea) Tapped(e *fyne.PointEvent) {
	if h.onTap != nil {
		h.onTap(e.Position)
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(h); c != nil {
		c.Focus(h)
	}
}
func (h *spHoverArea) FocusGained()     {}
func (h *spHoverArea) FocusLost()       {}
func (h *spHoverArea) TypedRune(r rune) {}
func (h *spHoverArea) TypedKey(key *fyne.KeyEvent) {
	if h.onKey != nil {
		h.onKey(key)
	}
}
