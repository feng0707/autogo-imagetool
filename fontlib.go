package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	nativedialog "github.com/sqweek/dialog"
)

const dotCellSize = 8

type FontChar struct {
	Char        string
	Color       string // ★ 提取该字用的文字颜色（如 ffffff-000000），导出跟在名字后
	Width       int
	Height      int
	HexData     string
	WhitePixels int
	Bitmap      [][]bool
}

type CharCell struct {
	BBox   image.Rectangle
	Bitmap [][]bool
	Char   string
}

// ==================== 二值化 ====================

func parseColorWithOffset(colorStr string) (color.NRGBA, color.NRGBA) {
	arr := strings.Split(colorStr, "-")
	s := strings.TrimPrefix(arr[0], "#")
	if len(s) < 6 {
		s = "FFFFFF"
	}
	r, _ := strconv.ParseUint(s[0:2], 16, 8)
	g, _ := strconv.ParseUint(s[2:4], 16, 8)
	b, _ := strconv.ParseUint(s[4:6], 16, 8)
	baseColor := color.NRGBA{uint8(r), uint8(g), uint8(b), 255}
	var offsetColor color.NRGBA
	if len(arr) > 1 {
		s2 := strings.TrimPrefix(arr[1], "#")
		if len(s2) >= 6 {
			or, _ := strconv.ParseUint(s2[0:2], 16, 8)
			og, _ := strconv.ParseUint(s2[2:4], 16, 8)
			ob, _ := strconv.ParseUint(s2[4:6], 16, 8)
			offsetColor = color.NRGBA{uint8(or), uint8(og), uint8(ob), 255}
		}
	}
	return baseColor, offsetColor
}

func colorAbsDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func isColorMatch(c1, base, offset color.NRGBA) bool {
	return colorAbsDiff(c1.R, base.R) <= offset.R &&
		colorAbsDiff(c1.G, base.G) <= offset.G &&
		colorAbsDiff(c1.B, base.B) <= offset.B
}

func createBinaryPreview(src image.Image, colorStr string) *image.NRGBA {
	bounds := src.Bounds()
	binary := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	base, offset := parseColorWithOffset(colorStr)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := src.At(x, y).RGBA()
			pixel := color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
			if isColorMatch(pixel, base, offset) {
				binary.SetNRGBA(x-bounds.Min.X, y-bounds.Min.Y, color.NRGBA{0, 230, 0, 255})
			} else {
				binary.SetNRGBA(x-bounds.Min.X, y-bounds.Min.Y, color.NRGBA{255, 50, 0, 255})
			}
		}
	}
	return binary
}

func extractBitmapFromRect(binaryImg *image.NRGBA, rect image.Rectangle) [][]bool {
	w := rect.Dx()
	h := rect.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	bitmap := make([][]bool, h)
	for y := 0; y < h; y++ {
		bitmap[y] = make([]bool, w)
		for x := 0; x < w; x++ {
			bitmap[y][x] = binaryImg.NRGBAAt(rect.Min.X+x, rect.Min.Y+y).G > 128
		}
	}
	return bitmap
}

// ==================== 投影分割 ====================

func findCharacterBBoxes(binaryImg *image.NRGBA, colGap, rowGap int) []image.Rectangle {
	w := binaryImg.Bounds().Dx()
	h := binaryImg.Bounds().Dy()

	// Step 1: flood fill (4-connected) to find basic connected components
	visited := make([]bool, w*h)
	var compBB []image.Rectangle

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if visited[idx] || binaryImg.NRGBAAt(x, y).G <= 128 {
				continue
			}
			mnX, mnY, mxX, mxY := x, y, x, y
			stack := [][2]int{{x, y}}
			visited[idx] = true
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				px, py := p[0], p[1]
				if px < mnX {
					mnX = px
				}
				if px > mxX {
					mxX = px
				}
				if py < mnY {
					mnY = py
				}
				if py > mxY {
					mxY = py
				}
				for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
					nx, ny := px+d[0], py+d[1]
					if nx < 0 || ny < 0 || nx >= w || ny >= h {
						continue
					}
					ni := ny*w + nx
					if visited[ni] || binaryImg.NRGBAAt(nx, ny).G <= 128 {
						continue
					}
					visited[ni] = true
					stack = append(stack, [2]int{nx, ny})
				}
			}
			compBB = append(compBB, image.Rect(mnX, mnY, mxX+1, mxY+1))
		}
	}

	if len(compBB) == 0 {
		return nil
	}

	// Step 2: union-find + iterative proximity merge
	parent := make([]int, len(compBB))
	bbox := make([]image.Rectangle, len(compBB))
	for i := range compBB {
		parent[i] = i
		bbox[i] = compBB[i]
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}

	for changed := true; changed; {
		changed = false
		for i := 0; i < len(compBB); i++ {
			for j := i + 1; j < len(compBB); j++ {
				ri, rj := find(i), find(j)
				if ri == rj {
					continue
				}
				bi, bj := bbox[ri], bbox[rj]
				hGap := max(0, max(bi.Min.X, bj.Min.X)-min(bi.Max.X, bj.Max.X))
				vGap := max(0, max(bi.Min.Y, bj.Min.Y)-min(bi.Max.Y, bj.Max.Y))
				if hGap <= colGap && vGap <= rowGap {
					parent[ri] = rj
					bbox[rj] = bi.Union(bj)
					changed = true
				}
			}
		}
	}

	// Step 3: collect and sort
	seen := make(map[int]bool)
	var result []image.Rectangle
	for i := range compBB {
		root := find(i)
		if !seen[root] {
			seen[root] = true
			result = append(result, bbox[root])
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Min.X != result[j].Min.X {
			return result[i].Min.X < result[j].Min.X
		}
		return result[i].Min.Y < result[j].Min.Y
	})
	return result
}

// ==================== 点阵编码/解码 ====================

func encodeBitmapHex(bitmap [][]bool) (string, int) {
	height := len(bitmap)
	if height == 0 {
		return "", 0
	}
	width := len(bitmap[0])
	total := width * height
	numBytes := (total + 7) / 8
	byteArr := make([]byte, numBytes)
	whiteCount := 0
	idx := 0
	for _, row := range bitmap {
		for _, px := range row {
			if px {
				byteArr[idx/8] |= 1 << (idx % 8)
				whiteCount++
			}
			idx++
		}
	}
	return fmt.Sprintf("%x", byteArr), whiteCount
}

func decodeBitmapHex(hexData string, width, height int) [][]bool {
	byteArr := make([]byte, len(hexData)/2)
	for i := 0; i < len(hexData)-1; i += 2 {
		b, _ := strconv.ParseUint(hexData[i:i+2], 16, 8)
		byteArr[i/2] = byte(b)
	}
	bitmap := make([][]bool, height)
	idx := 0
	for y := 0; y < height; y++ {
		bitmap[y] = make([]bool, width)
		for x := 0; x < width; x++ {
			if idx/8 < len(byteArr) {
				bitmap[y][x] = (byteArr[idx/8]>>(idx%8))&1 == 1
			}
			idx++
		}
	}
	return bitmap
}

// ==================== 字库文件读写 ====================

func parseFontLib(content string) []FontChar {
	var chars []FontChar
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "$")
		// ★ OP 文本字库条目：3 段（名字$h,w,bit$HEX）或 4 段（名字$颜色$h,w,bit$HEX，颜色跳过）
		//   HEX 位序=列优先（bit 序号 = x*高 + y，字节内低位在前）；w/h ≤ 255
		if len(parts) == 3 || len(parts) == 4 {
			head := parts[1]
			color := ""
			if len(parts) == 4 {
				color = parts[1] // 颜色字段（人读用，点阵无关）
				head = parts[2]
			}
			var hh, ww, bb int
			if _, err := fmt.Sscanf(head, "%d,%d,%d", &hh, &ww, &bb); err != nil || hh <= 0 || ww <= 0 {
				continue
			}
			bm, bc := opHexToBitmapWH(parts[len(parts)-1], ww, hh)
			if bm == nil || parts[0] == "" {
				continue
			}
			chars = append(chars, FontChar{
				Char: parts[0], Color: color, Width: ww, Height: hh,
				HexData: parts[len(parts)-1], WhitePixels: bc, Bitmap: bm,
			})
			continue
		}
		if len(parts) < 5 {
			continue
		}
		charName := parts[0]
		hexData := parts[1]
		infoStr := parts[3]
		widthStr := parts[4]
		w, e1 := strconv.Atoi(widthStr)
		if e1 != nil || w <= 0 {
			continue
		}
		totalPixels := len(hexData) / 2 * 8
		h := totalPixels / w
		if h <= 0 {
			continue
		}
		wp := 0
		if dotIdx := strings.LastIndex(infoStr, "."); dotIdx >= 0 {
			wp, _ = strconv.Atoi(infoStr[dotIdx+1:])
		}
		bm := decodeBitmapHex(hexData, w, h)
		chars = append(chars, FontChar{
			Char: charName, Width: w, Height: h,
			HexData: hexData, WhitePixels: wp, Bitmap: bm,
		})
	}
	return chars
}

func exportFontLib(chars []FontChar, fgColor string) string {
	var sb strings.Builder
	sb.WriteString("# AutoGo FontLib v1.0\n")
	sb.WriteString(fmt.Sprintf("# Color:%s\n#\n", fgColor))
	for _, ch := range chars {
		sb.WriteString(fmt.Sprintf("%s$%s$1$0.0.%d$%d\n", ch.Char, ch.HexData, ch.WhitePixels, ch.Width))
	}
	return sb.String()
}

// ==================== OP 字库格式（op txt / .dict 同源）2026-09-30 ====================
// 文本条目（3 段）：名字$h,w,bit数$HEX —— 对齐 OP Dictionary.h word1_t::from_string/to_string。
// HEX 位序：列优先，bit 序号 = x*h + y（x 外层、y 内层），字节内低位在前；w/h ≤ 255、名 ≤7 字。
// 与引擎 damocr / OP 插件互通。文件编码 GBK（OP 在 Windows 用 ANSI 读）；导入自动识别 UTF-8/GBK。

// bitmapToOpHex 行优先位图 → OP 文本条目 HEX（列优先）+ 位数
func bitmapToOpHex(bitmap [][]bool) (string, int) {
	h := len(bitmap)
	if h == 0 {
		return "", 0
	}
	w := len(bitmap[0])
	total := w * h
	const hexd = "0123456789ABCDEF"
	byteArr := make([]byte, (total+7)/8)
	idx := 0
	bc := 0
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			if bitmap[y][x] {
				byteArr[idx/8] |= 1 << (idx % 8)
				bc++
			}
			idx++
		}
	}
	var sb strings.Builder
	for _, b := range byteArr {
		sb.WriteByte(hexd[b>>4])
		sb.WriteByte(hexd[b&0x0F])
	}
	return sb.String(), bc
}

// opHexToBitmapWH 按 w/h 解码 OP 条目 HEX → 行优先位图（+实际位数）
func opHexToBitmapWH(hexData string, w, h int) ([][]bool, int) {
	if w <= 0 || h <= 0 || w > 255 || h > 255 {
		return nil, 0
	}
	byteArr := make([]byte, len(hexData)/2)
	for i := 0; i+1 < len(hexData); i += 2 {
		b, err := strconv.ParseUint(hexData[i:i+2], 16, 8)
		if err != nil {
			return nil, 0
		}
		byteArr[i/2] = byte(b)
	}
	total := w * h
	if len(byteArr) < (total+7)/8 {
		return nil, 0
	}
	bm := make([][]bool, h)
	for y := range bm {
		bm[y] = make([]bool, w)
	}
	idx := 0
	bc := 0
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			if idx/8 < len(byteArr) && (byteArr[idx/8]>>(idx%8))&1 == 1 {
				bm[y][x] = true
				bc++
			}
			idx++
		}
	}
	return bm, bc
}

// exportOpFontLib 导出 OP 文本字库；w/h 超 255 或未标注则跳过。
// ★ 行格式（2026-09-30 扩展）：名字$提取颜色$h,w,bit数$HEX —— 颜色跟在名字后
//
//	（不同字可用不同颜色提取，字多了靠它区分）；引擎 damocr 解析时跳过该字段。
//
// 文件头一行注释标记格式（解析端按 # 跳过）。返回 (内容, 成功数, 跳过数)
func exportOpFontLib(chars []FontChar, fgColor string) (string, int, int) {
	var sb strings.Builder
	sb.WriteString("# OP FontLib v1.0\n")
	ok, skip := 0, 0
	for _, ch := range chars {
		if ch.Char == "" {
			skip++
			continue
		}
		color := ch.Color
		if color == "" {
			color = fgColor
		}
		w, h := ch.Width, ch.Height
		if w <= 0 || h <= 0 || w > 255 || h > 255 {
			skip++
			continue
		}
		bm := ch.Bitmap
		if bm == nil {
			bm = decodeBitmapHex(ch.HexData, w, h)
		}
		if len(bm) == 0 || len(bm[0]) == 0 {
			skip++
			continue
		}
		hexData, bits := bitmapToOpHex(bm)
		if hexData == "" || bits == 0 {
			skip++
			continue
		}
		name := []rune(ch.Char)
		if len(name) > 7 {
			name = name[:7] // OP word1_info.name[8] 含结尾 NUL，最多 7 字
		}
		// 扩展顺序：名字$颜色$h,w,bit$HEX（h 在前；颜色供人读/引擎跳过）
		sb.WriteString(string(name))
		sb.WriteString(fmt.Sprintf("$%s$%d,%d,%d$%s\n", color, h, w, bits, hexData))
		ok++
	}
	return sb.String(), ok, skip
}

// encodeOpFileBytes 字库文本 → GBK 字节（OP Windows ANSI 兼容）；含 GBK 外字符回落 UTF-8
func encodeOpFileBytes(content string) ([]byte, string) {
	enc := simplifiedchinese.GBK.NewEncoder()
	out, err := enc.Bytes([]byte(content))
	if err == nil {
		return out, "GBK"
	}
	return []byte(content), "UTF-8"
}

// decodeFontFileBytes 字库文件字节 → 文本：合法 UTF-8 原样，否则按 GBK 解码
func decodeFontFileBytes(data []byte) (string, string) {
	if utf8.Valid(data) {
		return string(data), "UTF-8"
	}
	dec := simplifiedchinese.GBK.NewDecoder()
	out, err := dec.Bytes(data)
	if err != nil {
		return string(data), "GBK(解码失败原样)"
	}
	return string(out), "GBK"
}

// drawDotRect 在点阵渲染图上描矩形框（2px 粗，坐标=像素级，自动裁到图内）
func drawDotRect(img *image.NRGBA, x1, y1, x2, y2 int, c color.NRGBA) {
	b := img.Bounds()
	clampX := func(x int) int { return max(b.Min.X, min(x, b.Max.X-1)) }
	clampY := func(y int) int { return max(b.Min.Y, min(y, b.Max.Y-1)) }
	x1, x2 = clampX(x1), clampX(x2)
	y1, y2 = clampY(y1), clampY(y2)
	for t := 0; t < 2; t++ {
		for x := x1; x <= x2; x++ {
			img.SetNRGBA(x, clampY(y1+t), c)
			img.SetNRGBA(x, clampY(y2-t), c)
		}
		for y := y1; y <= y2; y++ {
			img.SetNRGBA(clampX(x1+t), y, c)
			img.SetNRGBA(clampX(x2-t), y, c)
		}
	}
}

// ==================== 渲染 ====================

func fillGridPattern(img *image.NRGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	stride := dotCellSize + 1
	gridColor := color.NRGBA{40, 40, 40, 255}
	cellColor := color.NRGBA{15, 15, 15, 255}
	for y := 0; y < h; y++ {
		idx := y * img.Stride
		for x := 0; x < w; x++ {
			var c color.NRGBA
			if x%stride == 0 || y%stride == 0 {
				c = gridColor
			} else {
				c = cellColor
			}
			img.Pix[idx] = c.R
			img.Pix[idx+1] = c.G
			img.Pix[idx+2] = c.B
			img.Pix[idx+3] = c.A
			idx += 4
		}
	}
}

func renderOriginalDotMatrix(src image.Image) *image.NRGBA {
	b := src.Bounds()
	pw, ph := b.Dx(), b.Dy()
	stride := dotCellSize + 1
	dispW := pw*stride + 1
	dispH := ph*stride + 1
	display := image.NewNRGBA(image.Rect(0, 0, dispW, dispH))
	fillGridPattern(display)
	for py := 0; py < ph; py++ {
		for px := 0; px < pw; px++ {
			r, g, bl, a := src.At(b.Min.X+px, b.Min.Y+py).RGBA()
			c := color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
			cx := px*stride + 1
			cy := py*stride + 1
			for dy := 0; dy < dotCellSize; dy++ {
				idx := (cy+dy)*display.Stride + cx*4
				for dx := 0; dx < dotCellSize; dx++ {
					display.Pix[idx] = c.R
					display.Pix[idx+1] = c.G
					display.Pix[idx+2] = c.B
					display.Pix[idx+3] = c.A
					idx += 4
				}
			}
		}
	}
	return display
}

func renderDotMatrix(binaryImg *image.NRGBA, colGap, rowGap int) (*image.NRGBA, []CharCell) {
	b := binaryImg.Bounds()
	pw, ph := b.Dx(), b.Dy()
	stride := dotCellSize + 1
	dispW := pw*stride + 1
	dispH := ph*stride + 1

	display := image.NewNRGBA(image.Rect(0, 0, dispW, dispH))

	fgColor := color.NRGBA{0, 230, 0, 255}
	for py := 0; py < ph; py++ {
		for px := 0; px < pw; px++ {
			if binaryImg.NRGBAAt(px, py).G > 128 {
				cx := px*stride + 1
				cy := py*stride + 1
				for dy := 0; dy < dotCellSize; dy++ {
					idx := (cy+dy)*display.Stride + cx*4
					for dx := 0; dx < dotCellSize; dx++ {
						display.Pix[idx] = fgColor.R
						display.Pix[idx+1] = fgColor.G
						display.Pix[idx+2] = fgColor.B
						display.Pix[idx+3] = fgColor.A
						idx += 4
					}
				}
			}
		}
	}

	bboxes := findCharacterBBoxes(binaryImg, colGap, rowGap)
	rectColor := color.NRGBA{255, 50, 0, 255}
	var cells []CharCell
	for _, bbox := range bboxes {
		dx1 := bbox.Min.X * stride
		dy1 := bbox.Min.Y * stride
		dx2 := bbox.Max.X * stride
		dy2 := bbox.Max.Y * stride
		drawRectOutlineOnImg(display, dx1, dy1, dx2, dy2, rectColor, 2)
		cells = append(cells, CharCell{BBox: bbox, Bitmap: extractBitmapFromRect(binaryImg, bbox)})
	}
	return display, cells
}

// renderDotMatrixOnly renders the dot matrix without drawing bounding boxes
func renderDotMatrixOnly(binaryImg *image.NRGBA) *image.NRGBA {
	b := binaryImg.Bounds()
	pw, ph := b.Dx(), b.Dy()
	stride := dotCellSize + 1
	dispW := pw*stride + 1
	dispH := ph*stride + 1

	display := image.NewNRGBA(image.Rect(0, 0, dispW, dispH))

	fgColor := color.NRGBA{0, 230, 0, 255}
	bgColor := color.NRGBA{255, 50, 0, 255}
	for py := 0; py < ph; py++ {
		for px := 0; px < pw; px++ {
			cx := px*stride + 1
			cy := py*stride + 1
			var c color.NRGBA
			if binaryImg.NRGBAAt(px, py).G > 128 {
				c = fgColor
			} else {
				c = bgColor
			}
			for dy := 0; dy < dotCellSize; dy++ {
				idx := (cy+dy)*display.Stride + cx*4
				for dx := 0; dx < dotCellSize; dx++ {
					display.Pix[idx] = c.R
					display.Pix[idx+1] = c.G
					display.Pix[idx+2] = c.B
					display.Pix[idx+3] = c.A
					idx += 4
				}
			}
		}
	}
	return display
}

func drawRectOutlineOnImg(img *image.NRGBA, x1, y1, x2, y2 int, c color.NRGBA, thickness int) {
	bx, by := img.Bounds().Dx(), img.Bounds().Dy()
	for t := 0; t < thickness; t++ {
		for x := x1; x <= x2; x++ {
			if x >= 0 && x < bx {
				if y1+t >= 0 && y1+t < by {
					img.SetNRGBA(x, y1+t, c)
				}
				if y2-t >= 0 && y2-t < by {
					img.SetNRGBA(x, y2-t, c)
				}
			}
		}
		for y := y1; y <= y2; y++ {
			if y >= 0 && y < by {
				if x1+t >= 0 && x1+t < bx {
					img.SetNRGBA(x1+t, y, c)
				}
				if x2-t >= 0 && x2-t < bx {
					img.SetNRGBA(x2-t, y, c)
				}
			}
		}
	}
}

func createCharPreview(bitmap [][]bool, cellSize int) image.Image {
	if len(bitmap) == 0 || len(bitmap[0]) == 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	h, w := len(bitmap), len(bitmap[0])
	img := image.NewNRGBA(image.Rect(0, 0, w*cellSize, h*cellSize))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.NRGBA
			if bitmap[y][x] {
				c = color.NRGBA{0, 230, 0, 255}
			} else {
				c = color.NRGBA{25, 25, 25, 255}
			}
			for dy := 0; dy < cellSize; dy++ {
				for dx := 0; dx < cellSize; dx++ {
					img.SetNRGBA(x*cellSize+dx, y*cellSize+dy, c)
				}
			}
		}
	}
	return img
}

// ==================== 悬停容器 ====================

func createGridBackground(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	gridColor := color.NRGBA{40, 40, 40, 255}
	cellColor := color.NRGBA{15, 15, 15, 255}
	stride := dotCellSize + 1
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x%stride == 0 || y%stride == 0 {
				img.SetNRGBA(x, y, gridColor)
			} else {
				img.SetNRGBA(x, y, cellColor)
			}
		}
	}
	return img
}

// ==================== 动态网格背景 ====================

type gridBgWidget struct {
	widget.BaseWidget
}

func newGridBgWidget() *gridBgWidget {
	g := &gridBgWidget{}
	g.ExtendBaseWidget(g)
	return g
}

func (g *gridBgWidget) CreateRenderer() fyne.WidgetRenderer {
	img := canvas.NewImageFromImage(nil)
	img.ScaleMode = canvas.ImageScalePixels
	img.FillMode = canvas.ImageFillOriginal
	return &gridBgRenderer{img: img}
}

type gridBgRenderer struct {
	img          *canvas.Image
	lastW, lastH int
}

func (r *gridBgRenderer) Layout(size fyne.Size) {
	w := int(size.Width)
	h := int(size.Height)
	if w > 0 && h > 0 && (w != r.lastW || h != r.lastH) {
		r.lastW = w
		r.lastH = h
		gridImg := image.NewNRGBA(image.Rect(0, 0, w, h))
		fillGridPattern(gridImg)
		r.img.Image = gridImg
		r.img.Refresh()
	}
	r.img.Resize(size)
	r.img.Move(fyne.NewPos(0, 0))
}

func (r *gridBgRenderer) MinSize() fyne.Size           { return fyne.NewSize(1, 1) }
func (r *gridBgRenderer) Refresh()                     {}
func (r *gridBgRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.img} }
func (r *gridBgRenderer) Destroy()                     {}

type tappableArea struct {
	widget.BaseWidget
	onTap func(fyne.Position)
}

func newTappableArea(onTap func(fyne.Position)) *tappableArea {
	t := &tappableArea{onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableArea) CreateRenderer() fyne.WidgetRenderer {
	r := canvas.NewRectangle(color.Transparent)
	return widget.NewSimpleRenderer(r)
}

func (t *tappableArea) Tapped(e *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap(e.Position)
	}
}

type hoverContainer struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onEnter func()
	onLeave func()
}

func newHoverContainer(content fyne.CanvasObject, onEnter, onLeave func()) *hoverContainer {
	h := &hoverContainer{content: content, onEnter: onEnter, onLeave: onLeave}
	h.ExtendBaseWidget(h)
	return h
}

func (h *hoverContainer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(h.content)
}

func (h *hoverContainer) MouseIn(*desktop.MouseEvent) {
	if h.onEnter != nil {
		h.onEnter()
	}
}

func (h *hoverContainer) MouseMoved(*desktop.MouseEvent) {}

func (h *hoverContainer) MouseOut() {
	if h.onLeave != nil {
		h.onLeave()
	}
}

// ==================== 字库制作窗口 ====================

// ★ 单实例：重复点按钮只聚焦已开的窗口（用户要求保持一个）
var fontLibWin fyne.Window

func openFontLibWindow(parentWindow fyne.Window) {
	if fontLibWin != nil {
		fontLibWin.RequestFocus()
		return
	}
	a := fyne.CurrentApp()
	w := a.NewWindow("AutoGo 字库制作")
	fontLibWin = w
	w.SetOnClosed(func() { fontLibWin = nil })
	w.Resize(fyne.NewSize(1200, 700))
	w.CenterOnScreen()

	var charCells []CharCell
	var charNameEntries []*widget.Entry
	var charHexCache []string
	var charWpCache []int
	var charMatchedLib []bool
	var binaryRegion *image.NRGBA
	var regionImg image.Image
	var fontLibChars []FontChar
	showingOriginal := false
	splitDone := false // ★ 分割（手动/智能/韩文）后置 true ⇒ 预览点击取色失效（用户要求：分割后取色乱）

	fgColorEntry := widget.NewEntry()
	fgColorEntry.SetText("000000-101010")
	fgColorEntry.SetPlaceHolder("如: 000000-101010")
	// ★ 2026-10-01 偏色快捷勾选框（照搬智能取色窗口的偏移15/32/40/60）：
	//   勾上任意一个，直接把「文字颜色」的偏色段（- 后段）改成对应容差。
	var offset15Check, offset32Check, offset40Check, offset60Check *widget.Check
	offset15Check = widget.NewCheck("偏移15", nil)
	offset32Check = widget.NewCheck("偏移32", nil)
	offset40Check = widget.NewCheck("偏移40", nil)
	offset60Check = widget.NewCheck("偏移60", nil)

	// 偏移值对应的容差
	offsetTolerance := map[*widget.Check]string{
		offset15Check: "151515",
		offset32Check: "323232",
		offset40Check: "404040",
		offset60Check: "606060",
	}
	updateOffsetTolerance := func(check *widget.Check) {
		fyne.Do(func() {
			check.Refresh()
			tol, ok := offsetTolerance[check]
			if !ok || !check.Checked {
				return
			}
			// 更新fgColorEntry的容差部分
			cur := fgColorEntry.Text
			if idx := strings.Index(cur, "-"); idx >= 0 {
				fgColorEntry.SetText(cur[:idx+1] + tol)
			} else {
				fgColorEntry.SetText(cur + "-" + tol)
			}
		})
	}
	offset15Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset15Check) }
	offset32Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset32Check) }
	offset40Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset40Check) }
	offset60Check.OnChanged = func(checked bool) { updateOffsetTolerance(offset60Check) }
	colGapEntry := widget.NewEntry()
	colGapEntry.SetText("1")
	rowGapEntry := widget.NewEntry()
	rowGapEntry.SetText("1")

	previewCanvasImg := canvas.NewImageFromImage(nil)
	previewCanvasImg.ScaleMode = canvas.ImageScalePixels
	previewCanvasImg.FillMode = canvas.ImageFillOriginal

	infoLabel := widget.NewLabel("请先获取选区或加载图片")
	infoLabel.Wrapping = fyne.TextWrapWord

	// ★ 紧凑正方形放大镜：左栏「获取选区」上面，鼠标悬停预览区时显示放大格
	//   （原字库制作没有放大镜；取色/涂抹窗口的大面板版已统一移除）
	magPanel := NewPickMagnifierPanelCompact(15, 9)
	updateMagFromPos := func(pos fyne.Position) {
		var src image.Image
		if showingOriginal || binaryRegion == nil {
			src = regionImg
		} else {
			src = binaryRegion
		}
		if src == nil {
			return
		}
		stride := float32(dotCellSize + 1)
		ix, iy := int(pos.X/stride), int(pos.Y/stride)
		b := src.Bounds()
		if ix < 0 {
			ix = 0
		}
		if iy < 0 {
			iy = 0
		}
		if ix >= b.Dx() {
			ix = b.Dx() - 1
		}
		if iy >= b.Dy() {
			iy = b.Dy() - 1
		}
		magPanel.Update(src, ix, iy)
	}
	updateMagCenter := func() {
		var src image.Image
		if showingOriginal || binaryRegion == nil {
			src = regionImg
		} else {
			src = binaryRegion
		}
		if src == nil {
			return
		}
		magPanel.Update(src, src.Bounds().Dx()/2, src.Bounds().Dy()/2)
	}

	charCardHolder := container.NewHBox()
	scrollBarPad := canvas.NewRectangle(color.Transparent)
	scrollBarPad.SetMinSize(fyne.NewSize(1, 12))
	charCardInner := container.NewVBox(charCardHolder, scrollBarPad)
	charCardScroll := container.NewHScroll(charCardInner)

	quickFillEntry := widget.NewEntry()
	quickFillEntry.SetPlaceHolder("快速填入: 主题壁纸")
	quickFillBtn := widget.NewButtonWithIcon("快速填入", theme.ConfirmIcon(), func() {
		chars := []rune(strings.TrimSpace(quickFillEntry.Text))
		for i := 0; i < len(charNameEntries) && i < len(chars); i++ {
			charNameEntries[i].SetText(string(chars[i]))
		}
	})
	quickFillBtn.Importance = widget.HighImportance

	readInt := func(e *widget.Entry, def int) int {
		v, err := strconv.Atoi(strings.TrimSpace(e.Text))
		if err != nil || v < 0 {
			return def
		}
		return v
	}

	// ===== 右侧字库列表 =====
	libListBox := container.NewVBox()
	libListScroll := container.NewVScroll(libListBox)
	libHeaderLabel := widget.NewLabel("字库内容 (0)")
	libHeaderLabel.TextStyle = fyne.TextStyle{Bold: true}
	// ★ 最近导入的字库文件路径（小字灰显，导入选中后更新）
	libPathLabel := canvas.NewText("", color.NRGBA{130, 130, 140, 255})
	libPathLabel.TextSize = 9
	libPathLabel.TextStyle = fyne.TextStyle{Monospace: true}

	var rebuildLibList func()
	rebuildLibList = func() {
		libListBox.RemoveAll()
		for i, ch := range fontLibChars {
			idx := i
			previewImg := canvas.NewImageFromImage(createCharPreview(ch.Bitmap, 3))
			previewImg.ScaleMode = canvas.ImageScalePixels
			previewImg.FillMode = canvas.ImageFillContain
			previewImg.SetMinSize(fyne.NewSize(40, 40))

			nameText := canvas.NewText(ch.Char, getTextColor(isDarkTheme))
			nameText.TextSize = 15
			nameText.TextStyle = fyne.TextStyle{Bold: true}

			sizeStr := fmt.Sprintf("%d x %d", ch.Width, ch.Height)
			if ch.Color != "" {
				sizeStr += "  " + ch.Color
			}
			sizeText := canvas.NewText(sizeStr, color.NRGBA{150, 150, 150, 255})
			sizeText.TextSize = 10

			delBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				fontLibChars = append(fontLibChars[:idx], fontLibChars[idx+1:]...)
				rebuildLibList()
			})
			delBtn.Importance = widget.LowImportance

			// ★ 字名/尺寸两行排布 + 预览放大（显示更宽松）
			infoCol := container.NewVBox(container.NewCenter(nameText), container.NewCenter(sizeText))
			leftInfo := container.NewHBox(container.NewCenter(previewImg), container.NewPadded(infoCol))
			scrollPad := canvas.NewRectangle(color.Transparent)
			scrollPad.SetMinSize(fyne.NewSize(12, 1))
			row := container.NewBorder(nil, nil, leftInfo, container.NewHBox(delBtn, scrollPad))
			libListBox.Add(row)
		}
		libListBox.Refresh()
		libHeaderLabel.SetText(fmt.Sprintf("字库内容 (%d)", len(fontLibChars)))
	}

	// 复用：把分割结果（点阵预览 + 字符格）套到预览画布/卡片/缓存上（doSlice 与智能分割共用）
	// ★ 卡片/预览统一入口。dotImg 参数已废弃（传 nil 即可）：预览图**内部**按 binaryRegion
	//   重渲染 + 画当前 cells 的蓝框 —— 叉号删除卡片后重调本函数，框自动跟着少一张。
	lastSplitTag := ""
	var applyCells func(cells []CharCell, dotImg *image.NRGBA, cg, rg int) // 先声明：叉号处理器要递归调用
	applyCells = func(cells []CharCell, dotImg *image.NRGBA, cg, rg int) {
		charCells = cells

		base := renderDotMatrixOnly(binaryRegion)
		stride := dotCellSize + 1
		blue := color.NRGBA{0, 130, 255, 255}
		for _, c := range cells {
			x1 := c.BBox.Min.X*stride + 1
			y1 := c.BBox.Min.Y*stride + 1
			x2 := (c.BBox.Max.X-1)*stride + 1 + dotCellSize
			y2 := (c.BBox.Max.Y-1)*stride + 1 + dotCellSize
			drawDotRect(base, x1, y1, x2, y2, blue)
		}
		previewCanvasImg.Image = base
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(base.Bounds().Dx()), float32(base.Bounds().Dy())))
		previewCanvasImg.Refresh()

		bw := binaryRegion.Bounds().Dx()
		bh := binaryRegion.Bounds().Dy()
		info := fmt.Sprintf("选区: %d×%d px | 检测到 %d 个字符 | 列间距:%d 行间距:%d",
			bw, bh, len(charCells), cg, rg)
		if lastSplitTag != "" {
			info += " | " + lastSplitTag
		}
		infoLabel.SetText(info)

		charCardHolder.RemoveAll()
		charNameEntries = make([]*widget.Entry, len(charCells))
		charHexCache = make([]string, len(charCells))
		charWpCache = make([]int, len(charCells))
		charMatchedLib = make([]bool, len(charCells))

		var cards []fyne.CanvasObject
		for i, cell := range charCells {
			hexData, wp := encodeBitmapHex(cell.Bitmap)
			charHexCache[i] = hexData
			charWpCache[i] = wp
			matchedName := ""
			for _, lc := range fontLibChars {
				if lc.HexData == hexData {
					matchedName = lc.Char
					charMatchedLib[i] = true
					break
				}
			}

			previewImg := canvas.NewImageFromImage(createCharPreview(cell.Bitmap, 2))
			previewImg.ScaleMode = canvas.ImageScalePixels
			previewImg.FillMode = canvas.ImageFillContain
			pw := float32(cell.BBox.Dx() * 2)
			ph := float32(cell.BBox.Dy() * 2)
			if pw < 20 {
				pw = 20
			}
			if pw > 70 {
				pw = 70
			}
			if ph < 20 {
				ph = 20
			}
			if ph > 50 {
				ph = 50
			}
			previewImg.SetMinSize(fyne.NewSize(pw, ph))

			idText := canvas.NewText(fmt.Sprintf("#%d  %dx%d", i, cell.BBox.Dx(), cell.BBox.Dy()), color.NRGBA{130, 170, 230, 255})
			idText.TextSize = 10

			nameEntry := widget.NewEntry()
			nameEntry.SetPlaceHolder("字符")
			if matchedName != "" {
				nameEntry.SetText(matchedName)
			} else if cell.Char != "" {
				nameEntry.SetText(cell.Char) // 智能分割的 OCR 自动命名
			}
			charNameEntries[i] = nameEntry

			// ★ 叉号：手动删除识别错的卡片（重调 applyCells → 框与卡片同步减少）
			delBtn := widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() {
				if i >= len(charCells) {
					return
				}
				nc := make([]CharCell, 0, len(charCells)-1)
				nc = append(nc, charCells[:i]...)
				nc = append(nc, charCells[i+1:]...)
				applyCells(nc, nil, cg, rg)
			})

			cardContent := container.NewBorder(
				container.NewHBox(idText, layout.NewSpacer(), delBtn), nameEntry, nil, nil,
				container.NewCenter(previewImg),
			)

			cardBg := canvas.NewRectangle(color.NRGBA{30, 30, 38, 255})
			if !isDarkTheme {
				cardBg = canvas.NewRectangle(color.NRGBA{235, 237, 242, 255})
			}
			cardMinW := canvas.NewRectangle(color.Transparent)
			cardMinW.SetMinSize(fyne.NewSize(100, 0))
			card := container.NewStack(cardMinW, cardBg, container.NewPadded(cardContent))
			cards = append(cards, card)
		}

		if len(cards) > 0 {
			row := container.NewHBox(cards...)
			charCardHolder.Add(row)
		}
		charCardHolder.Refresh()
		updateMagCenter() // ★ 分割后放大镜切到二值图中心
	}

	// ===== 智能分割（微信 OCR：行框定位 + 行内分字 + 识别文本自动命名）=====
	// ★ 2026-09-30 改为**进程内直调** mmmojo_64.dll（wx4 协议：请求 10010 → 结果 10011 推回），
	//   不再依赖 8080 的 python 服务。⛔ OCR 后端全局只能有一个环境 —— 8080 服务开着时
	//   直调会静默超时（python 进程内直调同样如此，实测）。引擎目录见 wxOcrEngineDir。
	// ★ 流程（用户拍板）：**原图**直接喂 OCR → 拿识别框（坐标=原图 1:1）→ 再按「文字颜色」
	//   二值化 → 用框在二值图上裁剪/分字。⛔ 别把二值图/放大图喂 OCR：放大后框坐标变 2 倍
	//   （下游按 1 倍裁就是错位），且网格噪声反而把识别带偏（实测更差）。
	saveRegionTmp := func(name string) (string, error) {
		var pbuf bytes.Buffer
		if err := png.Encode(&pbuf, regionImg); err != nil {
			return "", err
		}
		p := filepath.Join(os.TempDir(), name)
		if err := os.WriteFile(p, pbuf.Bytes(), 0644); err != nil {
			return "", err
		}
		return p, nil
	}
	// 共用下游：OCR 行结果 → 行框裁剪/分字/自动命名 → 卡片与预览（微信与 Paddle 共用）
	// ★ vertical=true 竖排（2026-10-05）：单元分界/落单元/兜底合并/阅读顺序全部改按 y 轴
	smartSplitRun := func(binImg *image.NRGBA, lines []wxOcrLine, srcTag string, vertical bool) {
		splitDone = true // ★ OCR 分割完成 ⇒ 预览点击取色失效
		cg := readInt(colGapEntry, 1)
		rg := readInt(rowGapEntry, 1)
		bw, bh := binImg.Bounds().Dx(), binImg.Bounds().Dy()

		type ocrLine struct {
			text  string
			chars []string
			boxes [][]float64
			rect  image.Rectangle
		}
		var lns []ocrLine
		for _, l := range lines {
			if strings.TrimSpace(l.Text) == "" {
				continue
			}
			x1, y1 := int(l.Left), int(l.Top)
			x2, y2 := int(l.Right+0.5), int(l.Bottom+0.5)
			r := image.Rect(x1, y1, x2, y2).Intersect(image.Rect(0, 0, bw, bh))
			if r.Empty() {
				continue
			}
			lns = append(lns, ocrLine{text: l.Text, chars: l.Chars, boxes: l.CharBoxes, rect: r})
		}
		sort.Slice(lns, func(i, j int) bool {
			if lns[i].rect.Min.Y != lns[j].rect.Min.Y {
				return lns[i].rect.Min.Y < lns[j].rect.Min.Y
			}
			return lns[i].rect.Min.X < lns[j].rect.Min.X
		})

		var cells []CharCell
		autoNamed, lineCt := 0, 0
		for _, ln := range lns {
			// ★ 首选：微信 OCR 的字符级坐标（CharBlock bbox）。
			//   ⛔ 实测（合成图对照）：该框是「检测单元」**不是笔画紧框** —— 窄字偏窄
			//   （7 真宽31给17.7）、'.' 给全高框、整体略偏 ⇒ 直接裁必切坏（0 → C）。
			//   正确用法：框只当**分界引导** —— 行内墨迹在二值图上取紧框（像素级精确），
			//   每个紧框按中心 x 落进哪个单元就命名成哪个字；同单元多框=断笔 → 合并。
			if len(ln.boxes) > 0 && len(ln.boxes) == len(ln.chars) {
				lineCt++
				// 单元分界：相邻单元框的中线（全局坐标）。
				//   横排按 x 分界；竖排按 y 分界（竖排字符 x 范围重叠，按 x 算出的分界互相穿插必错）。
				//   ⛔ 首尾**不许**放开到行框两端：微信行框会把选区边缘的残字也罩进去，
				//   放开 = 最后一个字把残字吞进同一框（实测 方+卟 合体）。±2px 容差。
				nBox := len(ln.boxes)
				mids := make([]float64, nBox+1)
				if vertical {
					mids[0] = float64(ln.boxes[0][1]) - 2
					for j := 1; j < nBox; j++ {
						mids[j] = (ln.boxes[j-1][3] + ln.boxes[j][1]) / 2
					}
					mids[nBox] = float64(ln.boxes[nBox-1][3]) + 2
				} else {
					mids[0] = float64(ln.boxes[0][0]) - 2
					for j := 1; j < nBox; j++ {
						mids[j] = (ln.boxes[j-1][2] + ln.boxes[j][0]) / 2
					}
					mids[nBox] = float64(ln.boxes[nBox-1][2]) + 2
				}
				cellOf := func(cv float64) int {
					for j := 0; j < len(mids)-1; j++ {
						if cv >= mids[j] && cv < mids[j+1] {
							return j
						}
					}
					return -1
				}
				// 行内墨迹分字（二值图，像素级）
				lineImg := image.NewNRGBA(image.Rect(0, 0, ln.rect.Dx(), ln.rect.Dy()))
				for y := 0; y < ln.rect.Dy(); y++ {
					for x := 0; x < ln.rect.Dx(); x++ {
						lineImg.SetNRGBA(x, y, binImg.NRGBAAt(ln.rect.Min.X+x, ln.rect.Min.Y+y))
					}
				}
				bboxes := findCharacterBBoxes(lineImg, cg, rg)
				// 墨迹紧框排序：横排按 x，竖排按 y（竖排字符自上而下，x 几乎相同）
				if vertical {
					sort.Slice(bboxes, func(a, b int) bool { return bboxes[a].Min.Y < bboxes[b].Min.Y })
				} else {
					sort.Slice(bboxes, func(a, b int) bool { return bboxes[a].Min.X < bboxes[b].Min.X })
				}
				if len(bboxes) == 0 {
					// 二值图没分出墨迹（文字颜色没配对等）→ 退回直接按单元框裁剪（旧行为兜底）
					for j, bx := range ln.boxes {
						if len(bx) < 4 {
							continue
						}
						var r image.Rectangle
						if vertical {
							tp := math.Max(bx[1]-2, mids[j])
							bt := math.Min(bx[3]+2.5, mids[j+1])
							r = image.Rect(int(bx[0])-2, int(tp), int(bx[2]+2.5), int(bt))
						} else {
							lf := math.Max(bx[0]-2, mids[j])
							rt := math.Min(bx[2]+2, mids[j+1])
							r = image.Rect(int(lf), int(bx[1])-2, int(rt+0.5), int(bx[3]+2.5))
						}
						r = r.Intersect(image.Rect(0, 0, bw, bh))
						if r.Empty() {
							continue
						}
						cbm := extractBitmapFromRect(binImg, r)
						if cbm == nil {
							continue
						}
						cells = append(cells, CharCell{BBox: r, Bitmap: cbm, Char: ln.chars[j]})
						autoNamed++
					}
					continue
				}
				// 墨迹紧框 → 按中心坐标落单元（横排看 x、竖排看 y）；同单元连续框合并（断笔）
				type inkGrp struct {
					rect image.Rectangle
					cell int
				}
				var grps []*inkGrp
				for _, bb := range bboxes {
					g := bb.Add(ln.rect.Min)
					var c int
					if vertical {
						c = cellOf((float64(g.Min.Y) + float64(g.Max.Y)) / 2)
					} else {
						c = cellOf((float64(g.Min.X) + float64(g.Max.X)) / 2)
					}
					if c >= 0 && len(grps) > 0 && grps[len(grps)-1].cell == c {
						grps[len(grps)-1].rect = grps[len(grps)-1].rect.Union(g)
						continue
					}
					grps = append(grps, &inkGrp{rect: g, cell: c})
				}
				for _, gp := range grps {
					r := gp.rect.Intersect(image.Rect(0, 0, bw, bh))
					if r.Empty() {
						continue
					}
					cbm := extractBitmapFromRect(binImg, r)
					if cbm == nil {
						continue
					}
					name := ""
					if gp.cell >= 0 {
						name = ln.chars[gp.cell]
						autoNamed++
					}
					cells = append(cells, CharCell{BBox: r, Bitmap: cbm, Char: name})
				}
				continue
			}
			// 回退：服务无字符坐标 → 行内二值分字 + 字数指导合并
			lineImg := image.NewNRGBA(image.Rect(0, 0, ln.rect.Dx(), ln.rect.Dy()))
			for y := 0; y < ln.rect.Dy(); y++ {
				for x := 0; x < ln.rect.Dx(); x++ {
					lineImg.SetNRGBA(x, y, binImg.NRGBAAt(ln.rect.Min.X+x, ln.rect.Min.Y+y))
				}
			}
			bboxes := findCharacterBBoxes(lineImg, cg, rg)
			if len(bboxes) == 0 {
				continue
			}
			lineCt++
			if vertical {
				sort.Slice(bboxes, func(a, b int) bool { return bboxes[a].Min.Y < bboxes[b].Min.Y })
			} else {
				sort.Slice(bboxes, func(a, b int) bool { return bboxes[a].Min.X < bboxes[b].Min.X })
			}
			// ★ OCR 字数指导合并：分字数 > 识别字数（某字断成两笔/杂点）时，
			//   反复合并间隙最小的相邻框（横排按水平间隙、竖排按垂直间隙），
			//   直到数量与识别文本一致（才能逐字自动命名）
			for len(ln.chars) > 0 && len(bboxes) > len(ln.chars) && len(bboxes) > 1 {
				best, bestGap := 1, 1<<30
				for j := 1; j < len(bboxes); j++ {
					var gap int
					if vertical {
						gap = bboxes[j].Min.Y - bboxes[j-1].Max.Y
					} else {
						gap = bboxes[j].Min.X - bboxes[j-1].Max.X
					}
					if gap < bestGap {
						bestGap = gap
						best = j
					}
				}
				bboxes[best-1] = bboxes[best-1].Union(bboxes[best])
				bboxes = append(bboxes[:best], bboxes[best+1:]...)
			}
			for j, bb := range bboxes {
				g := bb.Add(ln.rect.Min)
				cbm := extractBitmapFromRect(binImg, g)
				if cbm == nil {
					continue
				}
				name := ""
				if len(ln.chars) > 0 && len(ln.chars) == len(bboxes) && j < len(ln.chars) {
					name = ln.chars[j] // 数量一致 → 逐字自动命名
					autoNamed++
				}
				cells = append(cells, CharCell{BBox: g, Bitmap: cbm, Char: name})
			}
		}
		modeTag := "横排"
		if vertical {
			modeTag = "竖排"
		}
		if len(cells) == 0 {
			infoLabel.SetText("智能分割(" + modeTag + "): OCR 未检出有效字符（检查文字颜色/间距后重试）")
			return
		}
		// ★ 排序 = 阅读顺序：横排「y 区间有重叠 = 同一行」行间按 Min.Y、行内按 Min.X
		//   （旧逻辑按顶部 y 差 <5px 分行 —— '.' 这种贴行底的小字符顶部低 30px，
		//   被错判成单独一行排到最后）。
		//   竖排反过来：「x 区间有重叠 = 同一列」列间按 Min.X、列内按 Min.Y（自上而下）。
		if !vertical {
			sort.Slice(cells, func(i, j int) bool { return cells[i].BBox.Min.Y < cells[j].BBox.Min.Y })
			type cellLine struct {
				yMin, yMax int
				idx        []int
			}
			var clines []cellLine
			for i, c := range cells {
				if n := len(clines); n > 0 && c.BBox.Min.Y < clines[n-1].yMax && c.BBox.Max.Y > clines[n-1].yMin {
					li := &clines[n-1]
					if c.BBox.Min.Y < li.yMin {
						li.yMin = c.BBox.Min.Y
					}
					if c.BBox.Max.Y > li.yMax {
						li.yMax = c.BBox.Max.Y
					}
					li.idx = append(li.idx, i)
				} else {
					clines = append(clines, cellLine{c.BBox.Min.Y, c.BBox.Max.Y, []int{i}})
				}
			}
			{
				ordered := make([]CharCell, 0, len(cells))
				for _, ln := range clines {
					idxs := append([]int(nil), ln.idx...)
					sort.Slice(idxs, func(a, b int) bool { return cells[idxs[a]].BBox.Min.X < cells[idxs[b]].BBox.Min.X })
					for _, ix := range idxs {
						ordered = append(ordered, cells[ix])
					}
				}
				cells = ordered
			}
		} else {
			sort.Slice(cells, func(i, j int) bool { return cells[i].BBox.Min.X < cells[j].BBox.Min.X })
			type cellCol struct {
				xMin, xMax int
				idx        []int
			}
			var ccols []cellCol
			for i, c := range cells {
				if n := len(ccols); n > 0 && c.BBox.Min.X < ccols[n-1].xMax && c.BBox.Max.X > ccols[n-1].xMin {
					co := &ccols[n-1]
					if c.BBox.Min.X < co.xMin {
						co.xMin = c.BBox.Min.X
					}
					if c.BBox.Max.X > co.xMax {
						co.xMax = c.BBox.Max.X
					}
					co.idx = append(co.idx, i)
				} else {
					ccols = append(ccols, cellCol{c.BBox.Min.X, c.BBox.Max.X, []int{i}})
				}
			}
			{
				ordered := make([]CharCell, 0, len(cells))
				for _, co := range ccols {
					idxs := append([]int(nil), co.idx...)
					sort.Slice(idxs, func(a, b int) bool { return cells[idxs[a]].BBox.Min.Y < cells[idxs[b]].BBox.Min.Y })
					for _, ix := range idxs {
						ordered = append(ordered, cells[ix])
					}
				}
				cells = ordered
			}
		}

		// ★ 预览裁剪到内容区（不留大片空底），字符格坐标同步平移
		u := cells[0].BBox
		for _, c := range cells[1:] {
			u = u.Union(c.BBox)
		}
		pad := 6
		cx0 := max(0, u.Min.X-pad)
		cy0 := max(0, u.Min.Y-pad)
		cx1 := min(bw, u.Max.X+pad)
		cy1 := min(bh, u.Max.Y+pad)
		cropRect := image.Rect(cx0, cy0, cx1, cy1)
		cropped := image.NewNRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
		for y := 0; y < cropRect.Dy(); y++ {
			for x := 0; x < cropRect.Dx(); x++ {
				cropped.SetNRGBA(x, y, binImg.NRGBAAt(cropRect.Min.X+x, cropRect.Min.Y+y))
			}
		}
		for i := range cells {
			cells[i].BBox = cells[i].BBox.Sub(cropRect.Min)
		}

		binaryRegion = cropped
		lastSplitTag = srcTag
		applyCells(cells, nil, cg, rg)
		infoLabel.SetText(fmt.Sprintf("智能分割(%s): OCR %d 行 → %d 字（自动命名 %d，其余请手填）| %s",
			modeTag, lineCt, len(cells), autoNamed, srcTag))
	}

	// 微信入口（进程内直调 mmmojo_64.dll，wx4 协议）★ vertical=竖排模式
	smartSplit := func(vertical bool) {
		if regionImg == nil {
			msg := "请先获取选区，再点智能分割(横排)"
			if vertical {
				msg = "请先获取选区，再点智能分割(竖排)"
			}
			dialog.ShowInformation("提示", msg, w)
			return
		}
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		p, err := saveRegionTmp("autosmart_ocr.png")
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		lines, ocrErr := wxOcrRun(wxOcrEngineDir(), p, 60*time.Second)
		if ocrErr != nil {
			dialog.ShowError(fmt.Errorf("微信OCR: %v", ocrErr), w)
			return
		}
		binImg := createBinaryPreview(regionImg, fgHex)
		smartSplitRun(binImg, lines, "微信OCR(内置引擎)", vertical)
	}

	// ★ 韩文入口：PaddleOCR-json（korean 语言库），下游流程与智能分割一致。
	//   ⛔ 与微信 OCR 是两套独立引擎，可并存；PaddleOCR-json 子进程全局一个实例。
	//   ★ vertical=竖排模式（2026-10-05）：与微信入口同样按 y 轴分字/排序
	paddleSplit := func(vertical bool) {
		if regionImg == nil {
			msg := "请先获取选区，再点智能韩文分割(横排)"
			if vertical {
				msg = "请先获取选区，再点智能韩文分割(竖排)"
			}
			dialog.ShowInformation("提示", msg, w)
			return
		}
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		p, err := saveRegionTmp("autosmart_ocr_ko.png")
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		items, ocrErr := paddleOCRRun(paddleEngineDir(), paddleKoConfig, p, 120*time.Second)
		if ocrErr != nil {
			dialog.ShowError(fmt.Errorf("PaddleOCR: %v", ocrErr), w)
			return
		}
		var lines []wxOcrLine
		for _, it := range items {
			l, t, r, b := it.Rect()
			// ★ 把 Paddle 识别文本逐字填进 Chars（跳过空格）⇒ 下游回退路径启用
			//   「识别字数指导合并」：行内墨迹碎片按最小间隙合并到 = 字数，再逐字自动命名。
			//   （PaddleOCR-json 只给行框+文本，没有微信那种字符级框 —— 叠写韩文若不填
			//    Chars，碎片原样保留且全无名，实测被切成 7 块零碎。）
			var chars []string
			for _, ru := range it.Text {
				if ru == ' ' || ru == '　' {
					continue
				}
				chars = append(chars, string(ru))
			}
			lines = append(lines, wxOcrLine{Text: it.Text, Chars: chars, Left: l, Top: t, Right: r, Bottom: b})
		}
		binImg := createBinaryPreview(regionImg, fgHex)
		smartSplitRun(binImg, lines, "PaddleOCR(韩文)", vertical)
	}

	// ===== 核心：开始切割 =====
	doSlice := func() {
		if regionImg == nil {
			return
		}
		showingOriginal = false
		splitDone = true // ★ 手动分割完成 ⇒ 预览点击取色失效
		fgHex := strings.TrimSpace(fgColorEntry.Text)
		if fgHex == "" {
			fgHex = "000000-101010"
		}
		cg := readInt(colGapEntry, 1)
		rg := readInt(rowGapEntry, 1)

		fullBinary := createBinaryPreview(regionImg, fgHex)
		bboxes := findCharacterBBoxes(fullBinary, cg, rg)
		if len(bboxes) > 0 {
			unionRect := bboxes[0]
			for _, bb := range bboxes[1:] {
				if bb.Min.X < unionRect.Min.X {
					unionRect.Min.X = bb.Min.X
				}
				if bb.Min.Y < unionRect.Min.Y {
					unionRect.Min.Y = bb.Min.Y
				}
				if bb.Max.X > unionRect.Max.X {
					unionRect.Max.X = bb.Max.X
				}
				if bb.Max.Y > unionRect.Max.Y {
					unionRect.Max.Y = bb.Max.Y
				}
			}
			pad := 2
			imgW, imgH := fullBinary.Bounds().Dx(), fullBinary.Bounds().Dy()
			x0 := max(0, unionRect.Min.X-pad)
			y0 := max(0, unionRect.Min.Y-pad)
			x1 := min(imgW, unionRect.Max.X+pad)
			y1 := min(imgH, unionRect.Max.Y+pad)
			expanded := image.Rect(x0, y0, x1, y1)
			cropped := image.NewNRGBA(image.Rect(0, 0, expanded.Dx(), expanded.Dy()))
			for y := 0; y < expanded.Dy(); y++ {
				for x := 0; x < expanded.Dx(); x++ {
					cropped.SetNRGBA(x, y, fullBinary.NRGBAAt(expanded.Min.X+x, expanded.Min.Y+y))
				}
			}
			binaryRegion = cropped
		} else {
			binaryRegion = fullBinary
		}

		dotImg, cells := renderDotMatrix(binaryRegion, cg, rg)
		lastSplitTag = ""
		applyCells(cells, dotImg, cg, rg)
	}

	// ===== 添加到字库 =====
	addToLibBtn := widget.NewButtonWithIcon("添加到字库", theme.ContentAddIcon(), func() {
		added := 0
		for i, cell := range charCells {
			if i >= len(charNameEntries) || i >= len(charMatchedLib) {
				break
			}
			if charMatchedLib[i] {
				continue
			}
			name := strings.TrimSpace(charNameEntries[i].Text)
			if name == "" {
				continue
			}
			bm := cell.Bitmap
			if len(bm) == 0 || len(bm[0]) == 0 {
				continue
			}
			hexData := charHexCache[i]
			wp := charWpCache[i]
			fgHex := strings.TrimSpace(fgColorEntry.Text)
			if fgHex == "" {
				fgHex = "000000-101010"
			}
			newChar := FontChar{
				Char: name, Color: fgHex, Width: len(bm[0]), Height: len(bm),
				HexData: hexData, WhitePixels: wp, Bitmap: bm,
			}
			fontLibChars = append(fontLibChars, newChar)
			added++
		}
		if added > 0 {
			rebuildLibList()
		}
	})
	addToLibBtn.Importance = widget.HighImportance

	// ===== 按钮 =====
	getSelBtn := widget.NewButtonWithIcon("获取选区", theme.VisibilityIcon(), func() {
		if imageViewer == nil || imageViewer.image == nil {
			dialog.ShowInformation("提示", "主窗口没有图片，请先截图或载入", w)
			return
		}
		if len(imageViewer.markRects) == 0 {
			dialog.ShowInformation("提示", "请先在主窗口图像上拖拽框选文字区域，然后再点此按钮", w)
			return
		}
		rect := imageViewer.markRects[0]
		selRect := image.Rect(
			min(rect.X1, rect.X2), min(rect.Y1, rect.Y2),
			max(rect.X1, rect.X2), max(rect.Y1, rect.Y2),
		)
		regionImg = cropImage(imageViewer.image, selRect)
		showingOriginal = true
		splitDone = false // ★ 新选区 ⇒ 恢复预览点击取色
		dotPreview := renderOriginalDotMatrix(regionImg)
		previewCanvasImg.Image = dotPreview
		previewCanvasImg.SetMinSize(fyne.NewSize(float32(dotPreview.Bounds().Dx()), float32(dotPreview.Bounds().Dy())))
		previewCanvasImg.Refresh()
		updateMagCenter() // ★ 放大镜对准选区中心
		infoLabel.SetText(fmt.Sprintf("选区: %d×%d px | 请点击「开始切割」进行二值化分割",
			regionImg.Bounds().Dx(), regionImg.Bounds().Dy()))
	})
	getSelBtn.Importance = widget.HighImportance

	sliceBtn := widget.NewButtonWithIcon("开始切割", theme.MediaPlayIcon(), func() {
		if regionImg == nil {
			dialog.ShowInformation("提示", "请先获取选区或加载图片", w)
			return
		}
		doSlice()
	})
	sliceBtn.Importance = widget.HighImportance

	// ★ 横排/竖排各一个入口（2026-10-05 拆分）：竖排按 y 轴分字/命名/排序
	smartBtnH := widget.NewButtonWithIcon("智能分割(横排)", theme.SearchIcon(), func() {
		smartSplit(false)
	})
	smartBtnH.Importance = widget.HighImportance

	smartBtnV := widget.NewButtonWithIcon("智能分割(竖排)", theme.SearchIcon(), func() {
		smartSplit(true)
	})
	smartBtnV.Importance = widget.HighImportance

	paddleBtnH := widget.NewButtonWithIcon("智能韩文分割(横排)", theme.SearchIcon(), func() {
		paddleSplit(false)
	})
	paddleBtnH.Importance = widget.WarningImportance

	paddleBtnV := widget.NewButtonWithIcon("智能韩文分割(竖排)", theme.SearchIcon(), func() {
		paddleSplit(true)
	})
	paddleBtnV.Importance = widget.WarningImportance

	// ===== 字库操作按钮（右侧面板底部）=====
	// ★ 字库路径持久化（tool_paths.json）：导入/导出记住目录，导出从上次目录起步
	fontLibDir := loadToolPaths().FontLibDir
	// ★ 导出字库（OP 文本格式，GBK 编码，与 OP 插件/引擎 damocr 互通；头部第 2 行记录提取颜色）
	opExportBtn := widget.NewButtonWithIcon("导出字库", theme.DocumentSaveIcon(), func() {
		if len(fontLibChars) == 0 {
			dialog.ShowInformation("提示", "字库为空", w)
			return
		}
		go func() {
			startFile := "opdict.txt"
			if fontLibDir != "" {
				startFile = filepath.Join(fontLibDir, "opdict.txt")
			}
			fp, err := nativedialog.File().Filter("字库文件", "txt").
				Title("导出字库").SetStartFile(startFile).Save()
			if err != nil {
				return
			}
			fontLibDir = filepath.Dir(fp)
			saveToolPaths(func(p *toolPaths) { p.FontLibDir = fontLibDir }) // ★ 持久化
			fgHex := strings.TrimSpace(fgColorEntry.Text)
			if fgHex == "" {
				fgHex = "000000-101010"
			}
			content, okN, skipN := exportOpFontLib(fontLibChars, fgHex)
			if okN == 0 {
				fyne.Do(func() { dialog.ShowInformation("提示", "没有可导出的字符（未标注或尺寸超限）", w) })
				return
			}
			bytes_, enc := encodeOpFileBytes(content)
			if err := os.WriteFile(fp, bytes_, 0644); err != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("保存失败: %v", err), w) })
				return
			}
			fyne.Do(func() {
				msg := fmt.Sprintf("已导出 %d 个字符（%s 编码）", okN, enc)
				if skipN > 0 {
					msg += fmt.Sprintf("，跳过 %d 个（未标注/尺寸超255）", skipN)
				}
				dialog.ShowInformation("成功", msg, w)
			})
		}()
	})
	opExportBtn.Importance = widget.MediumImportance

	copyBtn := widget.NewButtonWithIcon("复制", theme.ContentCopyIcon(), func() {
		if len(fontLibChars) == 0 {
			dialog.ShowInformation("提示", "字库为空", w)
			return
		}
		var sb strings.Builder
		for _, ch := range fontLibChars {
			sb.WriteString(fmt.Sprintf("%s$%s$1$0.0.%d$%d\n", ch.Char, ch.HexData, ch.WhitePixels, ch.Width))
		}
		w.Clipboard().SetContent(sb.String())
		dialog.ShowInformation("成功", fmt.Sprintf("已复制 %d 个字符到剪贴板", len(fontLibChars)), w)
	})
	copyBtn.Importance = widget.MediumImportance

	importBtn := widget.NewButtonWithIcon("导入", theme.FolderOpenIcon(), func() {
		go func() {
			fp, err := nativedialog.File().Filter("字库文件", "txt", "lib").
				Title("导入字库").Load()
			if err != nil {
				return
			}
			data, err := os.ReadFile(fp)
			if err != nil {
				fyne.Do(func() { dialog.ShowError(fmt.Errorf("读取失败: %v", err), w) })
				return
			}
			content, enc := decodeFontFileBytes(data)
			imported := parseFontLib(content)
			if len(imported) == 0 {
				fyne.Do(func() {
					dialog.ShowInformation("提示", "未找到有效字库数据（已尝试 "+enc+" 解码）", w)
				})
				return
			}
			fyne.Do(func() {
				fontLibChars = append(fontLibChars, imported...)
				rebuildLibList()
				// ★ 小字显示字库路径（超长取尾部，避免撑爆右栏）+ 路径持久化
				fontLibDir = filepath.Dir(fp)
				saveToolPaths(func(p *toolPaths) { p.FontLibDir = fontLibDir })
				p := fp
				if len(p) > 46 {
					p = "…" + p[len(p)-45:]
				}
				libPathLabel.Text = p
				libPathLabel.Refresh()
				dialog.ShowInformation("成功", fmt.Sprintf("已导入 %d 个字符", len(imported)), w)
			})
		}()
	})
	importBtn.Importance = widget.MediumImportance

	clearLibBtn := widget.NewButtonWithIcon("清空", theme.DeleteIcon(), func() {
		if len(fontLibChars) == 0 {
			return
		}
		dialog.ShowConfirm("确认", fmt.Sprintf("确定清空字库中的 %d 个字符？", len(fontLibChars)), func(ok bool) {
			if ok {
				fontLibChars = nil
				rebuildLibList()
				libPathLabel.Text = "" // ★ 路径小字一并清掉
				libPathLabel.Refresh()
			}
		}, w)
	})
	clearLibBtn.Importance = widget.MediumImportance

	// ===== 布局 =====
	leftPanel := container.New(&fixedWidthLayout{width: 155, padding: 10, verticalSpacing: 4},
		layout.NewSpacer(),
		magPanel,
		getSelBtn,
		widget.NewSeparator(),
		widget.NewLabel("文字颜色:"),
		fgColorEntry,
		offset15Check,
		offset32Check,
		offset40Check,
		offset60Check,
		widget.NewSeparator(),
		widget.NewLabel("列间距(像素):"),
		colGapEntry,
		widget.NewLabel("行间距(像素):"),
		rowGapEntry,
		widget.NewSeparator(),
		sliceBtn,
		widget.NewSeparator(),
		smartBtnH,
		smartBtnV,
		paddleBtnH,
		paddleBtnV,
		layout.NewSpacer(),
	)

	gridBg := newGridBgWidget()
	// ★ 覆盖层升级：带鼠标跟踪（悬停更新放大镜）+ 点击取色（原 newTappableArea 只有点击）
	previewHover := newSpHoverArea(
		func(pos fyne.Position) { // onMouseMove：更新放大镜
			updateMagFromPos(pos)
		},
		nil, // onDrag：无
		func(pos fyne.Position) { // onTap：点击取色（原逻辑原样保留；分割后失效）
			if splitDone || !showingOriginal || regionImg == nil {
				return
			}
			stride := float32(dotCellSize + 1)
			px := int(pos.X / stride)
			py := int(pos.Y / stride)
			b := regionImg.Bounds()
			if px < 0 || py < 0 || px >= b.Dx() || py >= b.Dy() {
				return
			}
			r, g, bl, _ := regionImg.At(b.Min.X+px, b.Min.Y+py).RGBA()
			hexColor := fmt.Sprintf("%02X%02X%02X", r>>8, g>>8, bl>>8)
			cur := fgColorEntry.Text
			if idx := strings.Index(cur, "-"); idx >= 0 {
				fgColorEntry.SetText(hexColor + cur[idx:])
			} else {
				fgColorEntry.SetText(hexColor)
			}
		},
		nil, // onKey：无
	)
	previewScroll := container.NewScroll(container.NewStack(
		container.New(&topLeftLayout{}, previewCanvasImg),
		previewHover,
	))
	previewArea := container.NewStack(gridBg, previewScroll)

	quickFillRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(quickFillBtn, addToLibBtn),
		quickFillEntry,
	)
	charCardArea := newFixedHeightContainer(charCardScroll, 130)

	centerArea := container.NewBorder(
		infoLabel,
		container.NewVBox(
			widget.NewSeparator(),
			charCardArea,
			quickFillRow,
		),
		nil, nil,
		previewArea,
	)

	libBtnRow1 := container.NewGridWithColumns(2, opExportBtn, copyBtn)
	libBtnRow2 := container.NewGridWithColumns(2, importBtn, clearLibBtn)

	rightContent := container.NewBorder(
		container.NewVBox(libHeaderLabel, libPathLabel, widget.NewSeparator()),
		container.NewVBox(widget.NewSeparator(), libBtnRow1, libBtnRow2),
		nil, nil,
		libListScroll,
	)
	rightBg := canvas.NewRectangle(color.Transparent)
	rightBg.SetMinSize(fyne.NewSize(250, 0))
	rightPanel := container.NewStack(rightBg, container.NewPadded(rightContent))

	mainContent := container.NewBorder(nil, nil, leftPanel, rightPanel, centerArea)
	w.SetContent(mainContent)
	w.Show()
}
