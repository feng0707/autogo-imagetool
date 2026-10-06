package main

/*
#cgo LDFLAGS: -luser32
#include <windows.h>
*/
import "C"
import "fyne.io/fyne/v2"

// moveSystemCursorStride: 把系统光标绝对定位到 widget 内"图像坐标 imgX/imgY、每像素 stride 个
// 逻辑单位"对应的屏幕物理位置。自动处理窗口客户区原点、系统 DPI 缩放和半像素居中。
// 绝对定位（而非相对移动）不产生累积误差，方向键逐像素移动时坐标不会漂移。
func moveSystemCursorStride(imgX, imgY int, stride float32, win fyne.Window, widget fyne.CanvasObject) {
	if win == nil || widget == nil || stride <= 0 {
		return
	}

	// 获取前台窗口客户区屏幕原点
	hwnd := C.GetForegroundWindow()
	if hwnd == nil {
		return
	}
	var pt C.POINT
	C.ClientToScreen(hwnd, &pt)

	scale := win.Canvas().Scale()
	// AbsolutePositionForObject 返回 widget 在 canvas 中的逻辑位置
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(widget)

	// 屏幕物理坐标 = 客户区屏幕原点 + (widget画布逻辑位置 + 图像像素偏移×stride + 半像素居中) × scale
	physicalX := int(float32(pt.x) + (pos.X+float32(imgX)*stride+stride*0.5)*scale)
	physicalY := int(float32(pt.y) + (pos.Y+float32(imgY)*stride+stride*0.5)*scale)
	C.SetCursorPos(C.int(physicalX), C.int(physicalY))
}

// moveSystemCursor: 字库制作/智能取色窗口用（点阵格 stride = dotCellSize+1）
func moveSystemCursor(imgX, imgY int, win fyne.Window, widget fyne.CanvasObject) {
	moveSystemCursorStride(imgX, imgY, float32(dotCellSize+1), win, widget)
}
