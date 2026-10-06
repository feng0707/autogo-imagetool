package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 位序黄金值：2x2 位图 [[1,0],[0,1]]（[y][x]）→ OP 列优先流 1,0,0,1 → byte 0b1001=0x09
func TestOpHexGolden(t *testing.T) {
	bm := [][]bool{{true, false}, {false, true}}
	hex, bits := bitmapToOpHex(bm)
	if hex != "09" || bits != 2 {
		t.Fatalf("golden: got %q bits=%d, want 09/2", hex, bits)
	}
	bm2, bc := opHexToBitmapWH("09", 2, 2)
	if bm2 == nil || bc != 2 || bm2[0][0] != true || bm2[0][1] != false || bm2[1][0] != false || bm2[1][1] != true {
		t.Fatalf("decode mismatch: %v bc=%d", bm2, bc)
	}
}

// 导出 → 导入 round trip：位图逐位一致；颜色头两行；名超7字截断
func TestOpExportImportRoundTrip(t *testing.T) {
	bm := make([][]bool, 9)
	for y := range bm {
		bm[y] = make([]bool, 11)
		for x := range bm[y] {
			bm[y][x] = (x+y)%3 == 0
		}
	}
	chars := []FontChar{
		{Char: "测", Width: 11, Height: 9, Bitmap: bm},
		{Char: "", Width: 11, Height: 9, Bitmap: bm},
		{Char: "超长名字符截断测试", Width: 11, Height: 9, Bitmap: bm},
	}
	content, okN, skipN := exportOpFontLib(chars, "ffffff-000000")
	if okN != 2 || skipN != 1 {
		t.Fatalf("okN=%d skipN=%d, want 2/1", okN, skipN)
	}
	if !strings.HasPrefix(content, "# OP FontLib v1.0\n") {
		t.Fatalf("missing format header line 1")
	}
	if strings.Contains(content, "# Color:") {
		t.Fatalf("per-char color now, header # Color should be gone://n%.160s", content)
	}
	if !strings.Contains(content, "测$ffffff-000000$9,11,") {
		t.Fatalf("per-char color field wrong://n%s", content)
	}
	imported := parseFontLib(content)
	if len(imported) != 2 {
		t.Fatalf("imported=%d, want 2", len(imported))
	}
	if imported[0].Char != "测" {
		t.Fatalf("char lost: %q", imported[0].Char)
	}
	if imported[1].Char != "超长名字符截断" {
		t.Fatalf("7-char truncation wrong: %q", imported[1].Char)
	}
	if imported[0].Color != "ffffff-000000" {
		t.Fatalf("color lost on import: %q", imported[0].Color)
	}
	for i, ch := range imported {
		src := chars[i].Bitmap
		if len(ch.Bitmap) != len(src) || len(ch.Bitmap[0]) != len(src[0]) {
			t.Fatalf("dim mismatch")
		}
		for y := range src {
			for x := range src[y] {
				if ch.Bitmap[y][x] != src[y][x] {
					t.Fatalf("bit mismatch @%d (%d,%d)", i, x, y)
				}
			}
		}
	}
}

// GBK 编解码 round trip（中文名）
func TestGbkRoundTrip(t *testing.T) {
	content := "测$9,11,33$ABCD\n"
	b, enc := encodeOpFileBytes(content)
	if enc != "GBK" {
		t.Fatalf("enc=%s", enc)
	}
	back, dec := decodeFontFileBytes(b)
	if dec != "GBK" || back != content {
		t.Fatalf("roundtrip: dec=%s content=%q", dec, back)
	}
	if utf8.Valid(b) {
		t.Fatalf("GBK bytes should not be valid utf8 for chinese")
	}
}
