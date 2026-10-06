package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
)

// optimizeWithOCR 调用Python文字提取服务
func optimizeWithOCR(src image.Image, detectIcon, detectTransparent, detectSolid bool) *image.NRGBA {
	b := src.Bounds()

	// 编码为PNG base64
	var buf bytes.Buffer
	png.Encode(&buf, src)
	imgB64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	// 调用Python服务
	resp, err := http.Post("http://127.0.0.1:18920/", "application/json",
		bytes.NewReader([]byte(`{"image":"`+imgB64+`"}`)))
	if err != nil {
		// 服务未启动，回退本地Otsu
		return localOtsuExtract(src)
	}
	defer resp.Body.Close()

	var result struct {
		Image string `json:"image"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Image == "" {
		return localOtsuExtract(src)
	}

	// 解码返回的PNG
	imgData, err := base64.StdEncoding.DecodeString(result.Image)
	if err != nil {
		return localOtsuExtract(src)
	}
	decoded, err := png.Decode(bytes.NewReader(imgData))
	if err != nil {
		return localOtsuExtract(src)
	}

	// 转为NRGBA
	nrgba := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			nrgba.Set(x, y, decoded.At(x-b.Min.X, y-b.Min.Y))
		}
	}
	return nrgba
}

// localOtsuExtract 本地Otsu回退方案
func localOtsuExtract(src image.Image) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	brightness := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			brightness[y*w+x] = float64(int(r>>8)+int(g>>8)+int(bl>>8)) / 3.0
		}
	}
	threshold := otsuThreshold(brightness)
	var belowCount, aboveCount int
	for _, v := range brightness {
		if v < threshold {
			belowCount++
		} else {
			aboveCount++
		}
	}
	textIsDark := belowCount < aboveCount
	nrgba := image.NewNRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := brightness[y*w+x]
			keep := false
			if textIsDark && v < threshold {
				keep = true
			} else if !textIsDark && v >= threshold {
				keep = true
			}
			if keep {
				nrgba.Set(b.Min.X+x, b.Min.Y+y, src.At(b.Min.X+x, b.Min.Y+y))
			} else {
				nrgba.Set(b.Min.X+x, b.Min.Y+y, color.NRGBA{0, 0, 0, 255})
			}
		}
	}
	return nrgba
}

// optimizeImage 智能分割：根据勾选类型，非匹配区域涂黑
func optimizeImage(src image.Image, detectText, detectIcon, detectTransparent, detectSolid bool) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return image.NewNRGBA(b)
	}

	nrgba := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			nrgba.Set(x, y, src.At(x, y))
		}
	}

	if !detectText && !detectIcon && !detectTransparent && !detectSolid {
		return nrgba
	}

	// 背景色（四角平均）
	corners := []color.Color{
		nrgba.At(b.Min.X, b.Min.Y),
		nrgba.At(b.Max.X-1, b.Min.Y),
		nrgba.At(b.Min.X, b.Max.Y-1),
		nrgba.At(b.Max.X-1, b.Max.Y-1),
	}
	bgR, bgG, bgB := avgCornerRGB(corners)

	// 文字检测：投影分析法 + 连通域几何特征
	var textMask [][]bool
	if detectText {
		textMask = detectTextRegions(nrgba, b)
	}

	// 连通域分析（图标检测）
	var compMap [][]int
	var compInfo []struct {
		size                   int
		colors                 map[string]bool
		minX, maxX, minY, maxY int
	}
	if detectIcon {
		compMap, compInfo = analyzeComponents(nrgba, b)
	}

	// 预计算方差（纯色检测）
	var varMap [][]float64
	var allVars []float64
	if detectSolid {
		varMap = make([][]float64, h)
		allVars = make([]float64, 0, w*h)
		for y := 0; y < h; y++ {
			varMap[y] = make([]float64, w)
			for x := 0; x < w; x++ {
				v := calcLocalVariance(nrgba, b.Min.X+x, b.Min.Y+y, b, 5)
				varMap[y][x] = v
				allVars = append(allVars, v)
			}
		}
	}
	solidThreshold := 0.0
	if detectSolid {
		solidThreshold = percentile(allVars, 0.25)
	}

	// 逐像素分类
	result := image.NewNRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px, py := b.Min.X+x, b.Min.Y+y
			r, g, bl, a := nrgba.At(px, py).RGBA()
			matched := false

			// 文字：投影分析 + 几何特征
			if detectText && !matched && textMask != nil {
				if textMask[y][x] {
					matched = true
				}
			}

			// 图标：中等大小多色连通域
			if detectIcon && !matched && compMap != nil {
				cid := compMap[y][x]
				if cid >= 0 && cid < len(compInfo) {
					info := compInfo[cid]
					if info.size >= 8 && info.size <= w*h/4 && len(info.colors) >= 2 {
						matched = true
					}
				}
			}

			// 透明：与背景色接近
			if detectTransparent && !matched {
				pr, pg, pb := rgbFromRGBA(r, g, bl)
				if colorDist(pr, pg, pb, bgR, bgG, bgB) < 25 && a < 0xFFFF {
					matched = true
				}
			}

			// 纯色：方差低于自适应阈值
			if detectSolid && !matched && varMap != nil {
				if varMap[y][x] < solidThreshold {
					matched = true
				}
			}

			if matched {
				result.Set(px, py, nrgba.At(px, py))
			} else {
				result.Set(px, py, color.NRGBA{0, 0, 0, 255})
			}
		}
	}
	return result
}

// detectTextRegions 文字区域检测：检测颜色突变边界
func detectTextRegions(img *image.NRGBA, bounds image.Rectangle) [][]bool {
	w, h := bounds.Dx(), bounds.Dy()

	// 1. 检测每个像素是否是"颜色突变"像素（任一4-邻居色差>40）
	edgePixels := make([][]bool, h)
	for y := 0; y < h; y++ {
		edgePixels[y] = make([]bool, w)
		for x := 0; x < w; x++ {
			px, py := bounds.Min.X+x, bounds.Min.Y+y
			r, g, b, _ := img.At(px, py).RGBA()
			cr, cg, cb := uint8(r>>8), uint8(g>>8), uint8(b>>8)

			isEdge := false
			for _, dir := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := px+dir[0], py+dir[1]
				if nx < bounds.Min.X || nx >= bounds.Max.X || ny < bounds.Min.Y || ny >= bounds.Max.Y {
					continue
				}
				nr, ng, nb, _ := img.At(nx, ny).RGBA()
				dr := abs(int(cr) - int(uint8(nr>>8)))
				dg := abs(int(cg) - int(uint8(ng>>8)))
				db := abs(int(cb) - int(uint8(nb>>8)))
				if dr+dg+db > 60 {
					isEdge = true
					break
				}
			}
			edgePixels[y][x] = isEdge
		}
	}

	// 2. 膨胀连接笔画（半径2）
	dilated := morphDilate(edgePixels, w, h, 2)

	// 3. 连通域分析，过滤掉极小噪点（<3像素）和极大背景（>画面50%）
	textMask := make([][]bool, h)
	for y := range textMask {
		textMask[y] = make([]bool, w)
	}

	visited := make([][]bool, h)
	for i := range visited {
		visited[i] = make([]bool, w)
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if visited[y][x] || !dilated[y][x] {
				continue
			}
			var pixels [][2]int
			var queue [][2]int
			queue = append(queue, [2]int{x, y})
			visited[y][x] = true
			minX, maxX, minY, maxY := x, x, y, y

			for len(queue) > 0 {
				cx, cy := queue[0][0], queue[0][1]
				queue = queue[1:]
				pixels = append(pixels, [2]int{cx, cy})
				if cx < minX {
					minX = cx
				}
				if cx > maxX {
					maxX = cx
				}
				if cy < minY {
					minY = cy
				}
				if cy > maxY {
					maxY = cy
				}

				for _, dir := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := cx+dir[0], cy+dir[1]
					if nx >= 0 && nx < w && ny >= 0 && ny < h && !visited[ny][nx] && dilated[ny][nx] {
						visited[ny][nx] = true
						queue = append(queue, [2]int{nx, ny})
					}
				}
			}

			size := len(pixels)
			bboxArea := (maxX - minX + 1) * (maxY - minY + 1)

			// 只过滤极小噪点和整个画面
			if size >= 3 && bboxArea < w*h*50/100 {
				for _, p := range pixels {
					if edgePixels[p[1]][p[0]] {
						textMask[p[1]][p[0]] = true
					}
				}
			}
		}
	}

	// 4. 轻微膨胀让笔画连续
	textMask = morphDilate(textMask, w, h, 1)
	return textMask
}

// morphDilate 形态学膨胀
func morphDilate(src [][]bool, w, h, radius int) [][]bool {
	result := make([][]bool, h)
	for y := range result {
		result[y] = make([]bool, w)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if src[y][x] {
				for dy := -radius; dy <= radius; dy++ {
					for dx := -radius; dx <= radius; dx++ {
						ny, nx := y+dy, x+dx
						if ny >= 0 && ny < h && nx >= 0 && nx < w {
							result[ny][nx] = true
						}
					}
				}
			}
		}
	}
	return result
}

// otsuThreshold Otsu自动阈值法：分析直方图，找到使类间方差最大的分割点
func otsuThreshold(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// 找最大最小值
	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV-minV < 0.01 {
		return (minV + maxV) / 2
	}

	// 构建256级直方图
	const levels = 256
	histogram := make([]int, levels)
	scale := float64(levels-1) / (maxV - minV)
	for _, v := range values {
		level := int((v - minV) * scale)
		if level >= levels {
			level = levels - 1
		}
		histogram[level]++
	}

	total := len(values)
	sum := 0.0
	for i, count := range histogram {
		sum += float64(i) * float64(count)
	}

	var bestThreshold float64
	maxBetweenVar := 0.0
	sumB := 0.0
	wB := 0.0

	for i, count := range histogram {
		wB += float64(count)
		if wB == 0 {
			continue
		}
		wF := float64(total) - wB
		if wF == 0 {
			break
		}

		sumB += float64(i) * float64(count)
		meanB := sumB / wB
		meanF := (sum - sumB) / wF

		betweenVar := wB * wF * (meanB - meanF) * (meanB - meanF)
		if betweenVar > maxBetweenVar {
			maxBetweenVar = betweenVar
			// 还原到原始值域
			bestThreshold = float64(i)/scale + minV
		}
	}
	return bestThreshold
}

// percentile 计算排序后第p百分位的值（p=0~1）
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	// 简单排序
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}
	idx := int(p * float64(len(sorted)-1))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// calcLocalContrast 计算窗口内最大最小亮度差（对比度）
func calcLocalContrast(img *image.NRGBA, cx, cy int, bounds image.Rectangle, win int) float64 {
	half := win / 2
	var minLum, maxLum float64 = 255, 0
	first := true
	for dy := -half; dy <= half; dy++ {
		for dx := -half; dx <= half; dx++ {
			x, y := cx+dx, cy+dy
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			// 亮度 = 0.299R + 0.587G + 0.114B
			lum := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
			if first {
				minLum, maxLum = lum, lum
				first = false
			} else {
				if lum < minLum {
					minLum = lum
				}
				if lum > maxLum {
					maxLum = lum
				}
			}
		}
	}
	return maxLum - minLum
}

func avgCornerRGB(corners []color.Color) (uint8, uint8, uint8) {
	var sr, sg, sb int
	for _, c := range corners {
		r, g, b, _ := c.RGBA()
		sr += int(r >> 8)
		sg += int(g >> 8)
		sb += int(b >> 8)
	}
	return uint8(sr / 4), uint8(sg / 4), uint8(sb / 4)
}

func rgbFromRGBA(r, g, b uint32) (uint8, uint8, uint8) {
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

func colorDist(r1, g1, b1, r2, g2, b2 uint8) float64 {
	dr := float64(int(r1) - int(r2))
	dg := float64(int(g1) - int(g2))
	db := float64(int(b1) - int(b2))
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// calcEdgeDensity 计算窗口内边缘密度（简化Sobel）
func calcEdgeDensity(img *image.NRGBA, cx, cy int, bounds image.Rectangle, win int) float64 {
	half := win / 2
	sum := 0.0
	count := 0
	for dy := -half; dy <= half; dy++ {
		for dx := -half; dx <= half; dx++ {
			x, y := cx+dx, cy+dy
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}
			// 右和下邻居的梯度
			r, g, b, _ := img.At(x, y).RGBA()
			cr, cg, cb := uint8(r>>8), uint8(g>>8), uint8(b>>8)

			nx, ny := x+1, y
			if nx < bounds.Max.X {
				nr, ng, nb, _ := img.At(nx, ny).RGBA()
				gradX := math.Abs(float64(cr)-float64(uint8(nr>>8))) +
					math.Abs(float64(cg)-float64(uint8(ng>>8))) +
					math.Abs(float64(cb)-float64(uint8(nb>>8)))
				sum += gradX
			}

			nx2, ny2 := x, y+1
			if ny2 < bounds.Max.Y {
				nr2, ng2, nb2, _ := img.At(nx2, ny2).RGBA()
				gradY := math.Abs(float64(cr)-float64(uint8(nr2>>8))) +
					math.Abs(float64(cg)-float64(uint8(ng2>>8))) +
					math.Abs(float64(cb)-float64(uint8(nb2>>8)))
				sum += gradY
			}
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// calcLocalVariance 计算窗口内颜色方差
func calcLocalVariance(img *image.NRGBA, cx, cy int, bounds image.Rectangle, win int) float64 {
	half := win / 2
	var sumR, sumG, sumB float64
	var sumR2, sumG2, sumB2 float64
	count := 0
	for dy := -half; dy <= half; dy++ {
		for dx := -half; dx <= half; dx++ {
			x, y := cx+dx, cy+dy
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			fr, fg, fb := float64(r>>8), float64(g>>8), float64(b>>8)
			sumR += fr
			sumG += fg
			sumB += fb
			sumR2 += fr * fr
			sumG2 += fg * fg
			sumB2 += fb * fb
			count++
		}
	}
	if count == 0 {
		return 0
	}
	n := float64(count)
	varR := sumR2/n - (sumR/n)*(sumR/n)
	varG := sumG2/n - (sumG/n)*(sumG/n)
	varB := sumB2/n - (sumB/n)*(sumB/n)
	return (varR + varG + varB) / 3
}

// analyzeComponents 连通域分析，返回组件映射和组件信息
func analyzeComponents(img *image.NRGBA, bounds image.Rectangle) ([][]int, []struct {
	size                   int
	colors                 map[string]bool
	minX, maxX, minY, maxY int
}) {
	w, h := bounds.Dx(), bounds.Dy()
	compMap := make([][]int, h)
	for i := range compMap {
		compMap[i] = make([]int, w)
		for j := range compMap[i] {
			compMap[i][j] = -1
		}
	}

	var components []struct {
		size                   int
		colors                 map[string]bool
		minX, maxX, minY, maxY int
	}

	visited := make([][]bool, h)
	for i := range visited {
		visited[i] = make([]bool, w)
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if visited[y][x] {
				continue
			}
			// BFS 找连通域（4-邻域，颜色容差15）
			baseR, baseG, baseB, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			br, bg, bb := uint8(baseR>>8), uint8(baseG>>8), uint8(baseB>>8)

			var queue [][2]int
			queue = append(queue, [2]int{x, y})
			visited[y][x] = true
			cid := len(components)
			components = append(components, struct {
				size                   int
				colors                 map[string]bool
				minX, maxX, minY, maxY int
			}{colors: make(map[string]bool), minX: x, maxX: x, minY: y, maxY: y})

			for len(queue) > 0 {
				cx, cy := queue[0][0], queue[0][1]
				queue = queue[1:]

				compMap[cy][cx] = cid
				components[cid].size++

				px, py := bounds.Min.X+cx, bounds.Min.Y+cy
				r, g, b, _ := img.At(px, py).RGBA()
				key := fmt.Sprintf("%d,%d,%d", r>>8, g>>8, b>>8)
				components[cid].colors[key] = true

				if cx < components[cid].minX {
					components[cid].minX = cx
				}
				if cx > components[cid].maxX {
					components[cid].maxX = cx
				}
				if cy < components[cid].minY {
					components[cid].minY = cy
				}
				if cy > components[cid].maxY {
					components[cid].maxY = cy
				}

				for _, dir := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := cx+dir[0], cy+dir[1]
					if nx < 0 || nx >= w || ny < 0 || ny >= h || visited[ny][nx] {
						continue
					}
					nr, ng, nb, _ := img.At(bounds.Min.X+nx, bounds.Min.Y+ny).RGBA()
					nr8, ng8, nb8 := uint8(nr>>8), uint8(ng>>8), uint8(nb>>8)
					if colorDist(br, bg, bb, nr8, ng8, nb8) < 15 {
						visited[ny][nx] = true
						queue = append(queue, [2]int{nx, ny})
					}
				}
			}
		}
	}
	return compMap, components
}

// 把选区图像渲染成点阵（每个像素=一个方格+网格线），类似字库预览
// zoom: 每个原始像素放大的倍数（如5表示5倍放大）
// 返回渲染后的图像和 stride（每个原始像素占用的显示像素数，用于坐标换算）
func renderPickDotMatrix(src image.Image, zoom int) (*image.NRGBA, int) {
	b := src.Bounds()
	pw, ph := b.Dx(), b.Dy()

	cell := zoom
	if cell < 1 {
		cell = 1
	}
	stride := cell + 1 // 格子边长 + 1px 网格线

	dispW := pw*stride + 1
	dispH := ph*stride + 1
	display := image.NewNRGBA(image.Rect(0, 0, dispW, dispH))

	// 先铺满网格线颜色
	gridColor := color.NRGBA{60, 60, 60, 255}
	for i := 0; i < len(display.Pix); i += 4 {
		display.Pix[i] = gridColor.R
		display.Pix[i+1] = gridColor.G
		display.Pix[i+2] = gridColor.B
		display.Pix[i+3] = 255
	}

	// 逐像素填充格子（留出网格线）
	for py := 0; py < ph; py++ {
		for px := 0; px < pw; px++ {
			r, g, bl, a := src.At(b.Min.X+px, b.Min.Y+py).RGBA()
			cr, cg, cb, ca := uint8(r>>8), uint8(g>>8), uint8(bl>>8), uint8(a>>8)
			cx := px*stride + 1
			cy := py*stride + 1
			for dy := 0; dy < cell; dy++ {
				idx := (cy+dy)*display.Stride + cx*4
				for dx := 0; dx < cell; dx++ {
					display.Pix[idx] = cr
					display.Pix[idx+1] = cg
					display.Pix[idx+2] = cb
					display.Pix[idx+3] = ca
					idx += 4
				}
			}
		}
	}
	return display, stride
}
