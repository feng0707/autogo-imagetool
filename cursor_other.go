//go:build !windows
// +build !windows

package main

import "fyne.io/fyne/v2"

// moveSystemCursorStride: 非 Windows 平台暂不支持系统光标绝对定位（空实现）
func moveSystemCursorStride(imgX, imgY int, stride float32, win fyne.Window, widget fyne.CanvasObject) {
}

// moveSystemCursor: 非 Windows 平台暂不支持（空实现）
func moveSystemCursor(imgX, imgY int, win fyne.Window, widget fyne.CanvasObject) {
}
