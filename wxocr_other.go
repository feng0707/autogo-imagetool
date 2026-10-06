//go:build !windows
// +build !windows

package main

// 微信 OCR 的非 Windows 桩（2026-10-06 mac 版）：mmmojo_64.dll 是 Windows 专属引擎，
// mac 上编译不过也跑不了 —— 功能禁用，调用处会收到明确的「不支持」错误。
// 接口签名与 wxocr.go（windows 版）逐一对齐，fontlib.go 无需任何改动。

import (
	"errors"
	"time"
)

type wxOcrLine struct {
	Text      string
	Chars     []string
	CharBoxes [][]float64 // 每字 [l,t,r,b]（与 Chars 一一对应；可能为空）
	Left, Top, Right, Bottom float64
	Rate      float64
}

func wxOcrEngineDir() string { return "" }

func wxOcrRun(baseDir, imgPath string, timeout time.Duration) ([]wxOcrLine, error) {
	return nil, errors.New("微信OCR(内置引擎)仅支持 Windows，mac 版未启用")
}
