//go:build !windows
// +build !windows

package main

// PaddleOCR-json 的非 Windows 桩（2026-10-06 mac 版）：引擎是 Windows exe，
// mac 上没有对应程序 —— 功能禁用，调用处会收到明确的「不支持」错误。
// 接口签名与 paddle.go（windows 版）逐一对齐，fontlib.go 无需任何改动。

import (
	"errors"
	"time"
)

const paddleKoConfig = "models/config_korean.txt"

type paddleLine struct {
	Text  string
	Box   [][2]uint32
	Score float64
}

// Left/Top/Right/Bottom：四角点取外接矩形（与 windows 版 paddle.go 逻辑一致）
func (l paddleLine) Rect() (left, top, right, bottom float64) {
	if len(l.Box) == 0 {
		return 0, 0, 0, 0
	}
	left, top = float64(l.Box[0][0]), float64(l.Box[0][1])
	right, bottom = left, top
	for _, pt := range l.Box[1:] {
		x, y := float64(pt[0]), float64(pt[1])
		if x < left {
			left = x
		}
		if x > right {
			right = x
		}
		if y < top {
			top = y
		}
		if y > bottom {
			bottom = y
		}
	}
	return left, top, right, bottom
}

func paddleEngineDir() string { return "" }

func paddleOCRRun(engineDir, configRel, imgPath string, timeout time.Duration) ([]paddleLine, error) {
	return nil, errors.New("PaddleOCR(韩文)仅支持 Windows，mac 版未启用")
}
