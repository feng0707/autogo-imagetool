package main

import (
	"os"
	"testing"
	"time"
)

// 端到端：进程内直调微信 OCR DLL 识别合成图（需本机存在引擎目录）
// baseDir 可用环境变量 WXOCR_TEST_DIR 覆盖（默认开发机引擎目录；可指到 dlst 验证分发副本）。
func TestWxOcrRun(t *testing.T) {
	baseDir := os.Getenv("WXOCR_TEST_DIR")
	if baseDir == "" {
		baseDir = `E:\myandroid\myweb4ocr`
	}
	img := baseDir + `\ocr_tmp\_test_split.png`
	lines, err := wxOcrRun(baseDir, img, 90*time.Second)
	if err != nil {
		t.Fatalf("wxOcrRun: %v", err)
	}
	if len(lines) == 0 {
		t.Fatalf("no lines")
	}
	ln := lines[0]
	if ln.Text != "雷电游戏中心" {
		t.Fatalf("text=%q", ln.Text)
	}
	if len(ln.Chars) != 6 || len(ln.CharBoxes) != 6 {
		t.Fatalf("chars=%d boxes=%d, want 6/6", len(ln.Chars), len(ln.CharBoxes))
	}
	t.Logf("text=%s", ln.Text)
	for i, ch := range ln.Chars {
		b := ln.CharBoxes[i]
		if b == nil || len(b) != 4 || b[2] <= b[0] || b[3] <= b[1] {
			t.Fatalf("char %d box invalid: %v", i, b)
		}
		t.Logf("  %s [%.1f %.1f %.1f %.1f]", ch, b[0], b[1], b[2], b[3])
	}
}
