package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	nativedialog "github.com/sqweek/dialog"
	"golang.org/x/image/bmp"
)

// ★ 版本号（窗口标题展示；与 FyneApp.toml / fyne_metadata_init.go 保持同步）
const appVersion = "v1.0.5"

func main() {
	// 创建应用
	a := app.New()

	// 默认使用深色主题
	isDarkTheme = true

	// 设置自定义主题
	a.Settings().SetTheme(newMyTheme())

	// 创建窗口
	w := a.NewWindow("AutoGo图色助手 " + appVersion)
	w.Resize(fyne.NewSize(1540, 850))
	w.CenterOnScreen()

	// 截图地址下拉框（地址列表持久化到 exe 同目录 screenshot_urls.json）
	// 选中地址后把地址显示在窗口标题上
	screenshotSaveDir = loadToolPaths().ScreenshotDir // ★ 截图保存目录持久化（tool_paths.json）
	screenshotURLSelect = widget.NewSelect(loadScreenshotURLs(), func(value string) {
		if value != "" {
			w.SetTitle("AutoGo图色助手 " + appVersion + "(" + value + ")")
		}
	})
	screenshotURLSelect.PlaceHolder = "选择截图地址"

	// + 号：弹窗输入新地址，添加后记录并选中
	addURLBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		entry := widget.NewEntry()
		entry.SetPlaceHolder("http://中控地址#设备ID?k=授权码 或 IP:10010")
		d := dialog.NewForm("添加截图地址", "添加", "取消",
			[]*widget.FormItem{{Text: "地址", Widget: entry}},
			func(confirm bool) {
				if !confirm {
					return
				}
				u := strings.TrimSpace(entry.Text)
				if u == "" {
					return
				}
				// 已存在则直接选中，不重复添加
				for _, existing := range screenshotURLSelect.Options {
					if existing == u {
						screenshotURLSelect.SetSelected(u)
						return
					}
				}
				opts := append(screenshotURLSelect.Options, u)
				screenshotURLSelect.Options = opts
				screenshotURLSelect.SetSelected(u)
				screenshotURLSelect.Refresh()
				saveScreenshotURLs(opts)
			}, w)
		d.Resize(fyne.NewSize(460, 170))
		d.Show()
	})
	addURLBtn.Importance = widget.LowImportance

	// - 号：移除当前选中的地址
	removeURLBtn := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
		u := screenshotURLSelect.Selected
		if u == "" {
			return
		}
		var opts []string
		for _, existing := range screenshotURLSelect.Options {
			if existing != u {
				opts = append(opts, existing)
			}
		}
		screenshotURLSelect.Options = opts
		screenshotURLSelect.Selected = ""
		screenshotURLSelect.Refresh()
		saveScreenshotURLs(opts)
		// 标题恢复默认
		w.SetTitle("AutoGo图色助手 " + appVersion)
	})
	removeURLBtn.Importance = widget.LowImportance

	// 下拉框撑满剩余宽度，+/- 固定在右侧，固定40高度
	customOptionRow := newFixedHeightContainer(
		container.NewBorder(nil, nil, nil, container.NewHBox(addURLBtn, removeURLBtn), screenshotURLSelect),
		40,
	)

	// 创建标签页容器（使用修改后的DocTabs，无滚动条但支持关闭功能）
	tabs := container.NewDocTabs()
	tabs.SetTabLocation(container.TabLocationTop)

	// 设置标签页切换监听器
	tabs.OnSelected = func(tab *container.TabItem) {
		// 保存之前标签页的数据
		saveCurrentTabData()

		// 更新当前标签页
		currentTab = tab

		// 恢复新标签页的数据
		restoreTabData(tab)
	}

	// 设置标签页关闭监听器
	tabs.OnClosed = func(tab *container.TabItem) {
		// 清理关闭标签页的数据
		delete(tabDataMap, tab)

		// 如果关闭的是当前标签页，清空当前引用（颜色点全局保留）
		if currentTab == tab {
			currentTab = nil
			imageViewer = nil
			if rectCoordEntry != nil {
				rectCoordEntry.SetText("")
			}
			if codeDisplayEntry != nil {
				codeDisplayEntry.SetText("")
			}
		}
	}

	// 标签页计数器
	tabCounter := 0

	// 创建第一个标签页（欢迎页）- 添加详细的使用说明
	welcomeTitle := widget.NewLabelWithStyle("欢迎使用 魔改版 图色助手", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	instructionsText := `
📱 开始使用
  • 截图前先在左上角下拉框选择截图地址
    （没有就点 + 号添加，会自动保存，- 号移除）
  • 截图地址支持两种格式（程序自动识别）：
    ① 中控接口（与 VSCode 插件同款格式）：
      http://中控地址#设备ID?k=远程调试授权码
      例如 http://192.168.31.208:10087#com.luatouch.app-2-test-cs2?k=xxxx
      （云控页「VSCode格式」按钮生成的串，两个工具通用；★ 2026-10-06 起中控接口
       要鉴权，没带 ?k=授权码 会提示需要授权码——授权码在云控后台「⚙功能→远程调试」生成）
      流程：取 /api/screenshot/time 基线 → POST /api/command 触发截图
           → 轮询时间戳确认新图回传（最多8秒）→ 下载 /api/screenshot
    ② LuaTouch 引擎：
      IP:端口（如 192.168.31.169:10010）
      直接 GET http://IP:端口/png 返回截图
  • 点击「截图」按钮从设备截取屏幕
  • 点击「载入」按钮导入本地图片，也支持直接拖入图片
  • 每次截图/载入会在新标签页打开
  • 当前使用的截图地址会显示在窗口标题上

⌨️ 快捷键（鼠标在图片上时生效）
  • 左键点击：在图像上标记取色点
  • 左键拖动：框选矩形区域
  • 右键点击：测试
  • ↑ ↓ ← →  移动光标位置（每次1像素）
  • Space     在当前光标位置新增取色点
  • 1~9 / 0   将第 1~10 个取色点更新为光标位置取色
  • Alt+1~0   将第 11~20 个取色点更新为光标位置取色
  • Alt+Enter 清除所有标记点和区域（等同「清除」按钮）
  • Enter     生成代码（等同「生成」按钮）

🎨 功能说明
  • 找色模式：多点找色（颜色匹配）
    代码首点固定 0|0，其余点为相对首点的偏移坐标
  • 比色模式：多点比色（精确比对），全部为绝对坐标
  • 点阵模式：快速生成网格取色点
  • 偏色设置：容差值（如：101010）
  • 裁剪功能：框选后点击「裁剪」

🧪 测试
  • 找色测试：弹窗上方为 findMultiColors Lua 调用模板
    （参数引用「测试」表），下方为命中坐标列表，均可一键复制
  • 比色测试：弹窗显示 detectsMultiColors Lua 校验模板

💡 提示
  • 生成代码会自动复制到剪切板
  • 没有主动框选区域的时候会根据标记点自动生成区域
  • 放大镜会实时显示鼠标位置的像素放大图
  • 右侧表格显示所有取色点信息
  • 点击「生成」按钮获取代码
  • 标签页右侧有关闭按钮（❌）
`

	// 旧：instructionsLabel := widget.NewLabel(instructionsText)（Label 无法选中复制）
	// 新：用多行 Entry 实现可选中复制，被改动时立即还原为原文（等效只读）
	instructionsEntry := widget.NewMultiLineEntry()
	instructionsEntry.TextStyle = fyne.TextStyle{Monospace: true}
	instructionsEntry.Wrapping = fyne.TextWrapWord
	instructionsEntry.OnChanged = func(s string) {
		if s != instructionsText {
			instructionsEntry.SetText(instructionsText)
		}
	}
	instructionsEntry.SetText(instructionsText)

	// 旧：VBox+固定高度容器（Entry 高度是猜的，窗口大就显得小）
	// 新：Border 布局——标题固定在顶部，说明 Entry 自动占满标签页剩余全部高度
	welcomeContent := container.NewBorder(
		container.NewVBox(welcomeTitle, widget.NewSeparator()),
		nil, nil, nil,
		instructionsEntry,
	)

	firstTab := container.NewTabItem("🏠", welcomeContent)
	tabs.Append(firstTab)

	// 初始化当前标签页为欢迎页
	currentTab = firstTab

	// 初始化 imageViewer 为 nil，将在第一次截图或载入时创建
	imageViewer = nil

	// 初始化空的颜色点列表，不再使用generateSampleData
	colorPoints = make([]ColorPoint, 0)

	// 设置刷新表格的函数
	refreshColorList = func() {
		if tableContent != nil {
			// 使用fyne.Do确保在主线程中执行UI更新
			fyne.Do(func() {
				tableContent.Refresh()
			})
		}
	}

	// 主题切换功能
	toggleTheme := func() {
		if isDarkTheme {
			// 切换到亮色主题
			isDarkTheme = false
			a.Settings().SetTheme(newMyTheme())
		} else {
			// 切换到深色主题
			isDarkTheme = true
			a.Settings().SetTheme(newMyTheme())
		}

		// 切换主题后更新表头和列表
		updateTableHeader()
		updateTableSelection()
	}

	// 创建区域坐标显示控件
	rectCoordEntry = widget.NewEntry()
	rectCoordEntry.SetPlaceHolder("区域: 0,0,0,0")
	rectCoordEntry.MultiLine = false
	rectCoordEntry.Wrapping = fyne.TextWrapOff
	rectCoordEntry.Scroll = fyne.ScrollNone
	rectCoordRow := newFixedHeightContainer(rectCoordEntry, 40)

	// 创建偏色值输入控件
	colorOffsetEntry = widget.NewEntry()
	colorOffsetEntry.SetPlaceHolder("偏色: 101010") // 默认占位符为101010（十六进制格式）
	colorOffsetEntry.MultiLine = false            // 单行显示
	colorOffsetEntry.Wrapping = fyne.TextWrapOff
	colorOffsetEntry.Scroll = fyne.ScrollNone
	colorOffsetRow := newFixedHeightContainer(colorOffsetEntry, 40)

	// 创建找色模式选择
	colorModeRadio = widget.NewRadioGroup([]string{"找色", "比色"}, nil)
	colorModeRadio.SetSelected("比色") // 默认选择多点比色
	colorModeRadio.Horizontal = true // 水平排列
	colorModeRow := newFixedHeightContainer(colorModeRadio, 40)

	// 点阵参数获取回调函数（每次创建新的imageViewer时都需要设置）
	getGridParamsFunc := func() (cols, rows, spacing int, hasParams bool) {
		if gridModeEnabled {
			return gridColsValue, gridRowsValue, gridSpacingValue, true
		}
		return 0, 0, 0, false
	}

	// 截图成功后的通用处理：转换格式、保存、创建新标签页
	handleScreenshotResult := func(capturedImg image.Image) {
		capturedImg = convertToNRGBA(capturedImg)
		savedPath, saveErr := saveScreenshotPNG(capturedImg, screenshotSaveDir)

		if saveErr != nil {
			dialog.ShowError(fmt.Errorf("保存截图失败: %v", saveErr), w)
		} else if savedPath != "" {
			log.Printf("截图已保存: %s", savedPath)
		}

		saveCurrentTabData()
		newImageViewer := NewImageViewer()
		newMagnifier := NewMagnifierWidget()
		newImgContainer := container.New(&imageAreaLayout{}, newImageAreaBackground(newImageViewer), newImageViewer)
		newScrollContainer := container.NewScroll(newImgContainer)

		newImageViewer.scrollContainer = newScrollContainer
		newImageViewer.magnifier = newMagnifier
		newImageViewer.window = w
		newImageViewer.getGridParams = getGridParamsFunc
		newImageViewer.SetImage(capturedImg)
		w.Canvas().Focus(newImageViewer)

		newScrollWithMagnifier := container.NewStack(newScrollContainer, newMagnifier)
		tabCounter++
		tabName := time.Now().Format("15:04:05")
		newTab := container.NewTabItem(tabName, newScrollWithMagnifier)

		tabDataMap[newTab] = &TabData{
			colorPoints:        make([]ColorPoint, 0),
			markRects:          make([]MarkRect, 0),
			manualRectSelected: false,
			imageViewer:        newImageViewer,
			generatedCode:      "",
		}

		tabs.Append(newTab)
		tabs.Select(newTab)
		currentTab = newTab
		imageViewer = newImageViewer

		if rectCoordEntry != nil {
			rectCoordEntry.SetText("")
		}
		if codeDisplayEntry != nil {
			codeDisplayEntry.SetText("")
		}
		if refreshColorList != nil {
			refreshColorList()
		}
	}

	// 创建左侧工具栏按钮 - 使用带动画的截图按钮
	var screenshotBtn *AnimatedScreenshotButton
	screenshotBtn = NewAnimatedScreenshotButton("截图", theme.ContentCopyIcon(), func() {
		// 按地址格式自动选择截图方式：
		// 含 # 或 /api/screenshot → 中控接口（VSCode 同款「http://中控#设备ID」或旧完整地址）；
		// 否则视为 LuaTouch 引擎（GET /png 直接取图）
		rawURL := strings.TrimSpace(screenshotURLSelect.Selected)
		if rawURL == "" {
			dialog.ShowError(fmt.Errorf("请先选择截图地址（可通过 + 号添加）"), w)
			return
		}
		screenshotBtn.StartLoading()
		go func() {
			var capturedImg image.Image
			var err error
			if strings.Contains(rawURL, "#") || strings.Contains(rawURL, "/api/screenshot") {
				capturedImg, err = captureScreenViaWeb(rawURL)
			} else {
				capturedImg, err = captureScreenViaLuaTouch(rawURL)
			}
			fyne.Do(func() {
				screenshotBtn.StopLoading()
				if err != nil {
					dialog.ShowError(fmt.Errorf("截图失败: %v", err), w)
					return
				}
				handleScreenshotResult(capturedImg)
			})
		}()
	})

	screenshotSettingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		dialog.ShowFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if lu == nil {
				return
			}
			screenshotSaveDir = lu.Path()
			saveToolPaths(func(p *toolPaths) { p.ScreenshotDir = screenshotSaveDir }) // ★ 持久化
			dialog.ShowInformation("提示", "截图保存目录已设置：\n"+screenshotSaveDir, w)
		}, w)
	})

	screenshotRowBorder := container.NewBorder(nil, nil, nil, screenshotSettingsBtn, screenshotBtn)
	screenshotRow := newFixedHeightContainer(screenshotRowBorder, 40)

	// 放大缩小按钮
	zoomInBtn := widget.NewButtonWithIcon("", theme.ZoomInIcon(), func() {
		newScale := wholeSicale + 0.5
		if newScale > 5.0 {
			newScale = 5.0
		}
		if newScale != wholeSicale {
			wholeSicale = newScale
			if imageViewer != nil && imageViewer.image != nil {
				// 同步滚动偏移后再刷新，避免缩放后坐标换算错位
				imageViewer.applyPixelScale(newScale)
			}
		}
	})
	zoomInBtn.Importance = widget.MediumImportance

	zoomOutBtn := widget.NewButtonWithIcon("", theme.ZoomOutIcon(), func() {
		newScale := wholeSicale - 0.5
		if newScale < 0.5 {
			newScale = 0.5
		}
		if newScale != wholeSicale {
			wholeSicale = newScale
			if imageViewer != nil && imageViewer.image != nil {
				// 同步滚动偏移后再刷新，避免缩放后坐标换算错位
				imageViewer.applyPixelScale(newScale)
			}
		}
	})
	zoomOutBtn.Importance = widget.MediumImportance

	zoomRowContent := container.NewGridWithColumns(2, zoomOutBtn, zoomInBtn)
	zoomRow := newFixedHeightContainer(zoomRowContent, 40)

	importBtn := widget.NewButtonWithIcon("载入", theme.FolderOpenIcon(), func() {
		// 使用系统原生文件打开对话框
		go func() {
			filePath, err := nativedialog.File().
				Filter("图片文件", "png", "jpg", "jpeg", "bmp").
				Title("选择图片文件").
				Load()

			if err != nil {
				// 用户取消或发生错误
				return
			}

			// 读取文件内容
			data, err := ioutil.ReadFile(filePath)
			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("读取文件失败: %v", err), w)
				})
				return
			}

			// 根据文件扩展名解码图像
			var img image.Image
			ext := strings.ToLower(filepath.Ext(filePath))

			switch ext {
			case ".png":
				img, err = png.Decode(bytes.NewReader(data))
			case ".jpg", ".jpeg":
				img, err = jpeg.Decode(bytes.NewReader(data))
			case ".bmp":
				img, err = bmp.Decode(bytes.NewReader(data))
			default:
				// 尝试自动检测格式
				img, _, err = image.Decode(bytes.NewReader(data))
			}

			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("解码图像失败: %v", err), w)
				})
				return
			}

			// 转换为NRGBA格式
			img = convertToNRGBA(img)

			// 在主线程中更新UI
			fyne.Do(func() {
				// 保存当前标签页的数据
				saveCurrentTabData()

				// 创建新的图像查看器和标签页
				newImageViewer := NewImageViewer()
				newMagnifier := NewMagnifierWidget()

				newImgContainer := container.New(&imageAreaLayout{}, newImageAreaBackground(newImageViewer), newImageViewer)
				newScrollContainer := container.NewScroll(newImgContainer)

				newImageViewer.scrollContainer = newScrollContainer
				newImageViewer.magnifier = newMagnifier
				newImageViewer.window = w
				newImageViewer.getGridParams = getGridParamsFunc // 设置点阵参数回调
				newImageViewer.SetImage(img)

				// 设置焦点到 ImageViewer，使键盘快捷键生效
				w.Canvas().Focus(newImageViewer)

				// 创建新标签页
				newScrollWithMagnifier := container.NewStack(newScrollContainer, newMagnifier)
				tabCounter++

				// 使用文件名作为标签名称
				tabName := filepath.Base(filePath)
				newTab := container.NewTabItem(tabName, newScrollWithMagnifier)

				// 初始化新标签页的数据
				tabDataMap[newTab] = &TabData{
					colorPoints:        make([]ColorPoint, 0),
					markRects:          make([]MarkRect, 0),
					manualRectSelected: false,
					imageViewer:        newImageViewer,
					generatedCode:      "",
				}

				tabs.Append(newTab)
				tabs.Select(newTab)

				// 更新当前标签页引用
				currentTab = newTab

				// 更新当前imageViewer引用
				imageViewer = newImageViewer

				// 清空矩形区域（颜色点全局保留）
				if rectCoordEntry != nil {
					rectCoordEntry.SetText("")
				}
				if codeDisplayEntry != nil {
					codeDisplayEntry.SetText("")
				}

				// 刷新表格
				if refreshColorList != nil {
					refreshColorList()
				}
			})
		}()
	})
	importBtn.Importance = widget.MediumImportance

	saveBtn := widget.NewButtonWithIcon("保存", theme.DocumentSaveIcon(), func() {
		if imageViewer == nil || imageViewer.image == nil {
			// 没有图像可保存
			dialog.ShowInformation("提示", "当前没有可保存的图像", w)
			return
		}

		// 保存当前图像的引用，避免在 goroutine 中被修改
		imgToSave := imageViewer.image

		// 使用系统原生文件保存对话框
		go func() {
			filePath, err := nativedialog.File().
				Filter("PNG 图片", "png").
				Filter("JPEG 图片", "jpg", "jpeg").
				Title("保存图片").
				SetStartFile("screenshot.png").
				Save()

			if err != nil {
				// 用户取消或发生错误
				return
			}

			// 获取文件扩展名
			ext := strings.ToLower(filepath.Ext(filePath))

			// 如果没有扩展名，默认添加.png
			if ext == "" {
				filePath = filePath + ".png"
				ext = ".png"
			}

			// 创建文件
			file, err := os.Create(filePath)
			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("创建文件失败: %v", err), w)
				})
				return
			}
			defer file.Close()

			// 根据扩展名编码图像
			if ext == ".jpg" || ext == ".jpeg" {
				err = jpeg.Encode(file, imgToSave, &jpeg.Options{Quality: 100})
			} else {
				err = png.Encode(file, imgToSave)
			}

			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("保存图像失败: %v", err), w)
				})
				return
			}
		}()
	})
	saveBtn.Importance = widget.MediumImportance

	rotateBtn := widget.NewButtonWithIcon("旋转", theme.MediaReplayIcon(), func() {
		if imageViewer == nil || imageViewer.originalImage == nil {
			return // 如果没有图像，不执行任何操作
		}

		// 计算新的旋转角度 (每次点击增加90度)
		newDegrees := (imageViewer.rotationDegrees + 90) % 360

		// 执行旋转
		imageViewer.RotateImage(newDegrees)
	})
	rotateBtn.Importance = widget.MediumImportance

	// 底部额外按钮


	// 字库制作按钮
	fontLibBtn := widget.NewButtonWithIcon("字库制作", theme.GridIcon(), func() {
		openFontLibWindow(w)
	})
	fontLibBtn.Importance = widget.MediumImportance

	// ★ 画笔涂抹（异形图）2026-09-30：独立窗口（对齐字库制作/智能取色）——
	//   从主窗口拉框选图像，画笔涂抹掉不要的部分（对齐大漠涂抹异形图用法），
	//   涂抹色自动取四角最常见色并自动补涂四角；Ctrl+Z/撤销按钮可回退。
	paintBtn := widget.NewButtonWithIcon("裁剪画笔涂抹", theme.DocumentCreateIcon(), func() {
		if imageViewer == nil || imageViewer.image == nil {
			dialog.ShowInformation("提示", "当前没有图像，请先截图或载入", w)
			return
		}
		openPaintWindow(w)
	})
	paintBtn.Importance = widget.MediumImportance

	// 主题切换按钮使用高重要性，使其更加突出
	themeBtn := widget.NewButtonWithIcon("切换主题", theme.ColorPaletteIcon(), toggleTheme)
	themeBtn.Importance = widget.MediumImportance

	// 创建代码显示框（多行只读文本框）
	codeDisplayEntry = widget.NewMultiLineEntry()
	codeDisplayEntry.SetPlaceHolder("生成的代码将显示在这里...")
	codeDisplayEntry.Wrapping = fyne.TextWrapWord
	codeDisplayEntry.TextStyle = fyne.TextStyle{Monospace: true}

	// 创建生成代码的函数
	generateCodeFunc := func() {
		// 生成代码并复制到剪贴板
		code := generateColorCode()
		if code != "" {
			// 复制到剪贴板
			w.Clipboard().SetContent(code + "\n")

			// 显示在编辑框中
			codeDisplayEntry.SetText(code)
		} else {
			codeDisplayEntry.SetText("")
			dialog.ShowError(fmt.Errorf("生成失败"), w)
		}
	}

	genBtn := widget.NewButtonWithIcon("生成", theme.DocumentCreateIcon(), generateCodeFunc)
	genBtn.Importance = widget.HighImportance

	testFunc := func() {
		if imageViewer == nil || imageViewer.image == nil {
			showFindMultiResultDialog(w, "请先加载/截图图片后再测试")
			return
		}

		mode := getColorMode()

		code := ""
		if codeDisplayEntry != nil {
			code = strings.TrimSpace(codeDisplayEntry.Text)
		}
		if code == "" {
			code = generateColorCode()
			if codeDisplayEntry != nil {
				codeDisplayEntry.SetText(code)
			}
		}

		x1, y1, x2, y2, firstColorSpec, pointsStr, err := extractGeneratedPattern(code)
		if err != nil {
			imageViewer.ClearTestOverlay()
			showFindMultiResultDialog(w, err.Error())
			return
		}

		defaultTol := color.RGBA{R: 0, G: 0, B: 0, A: 255}
		if strings.Contains(firstColorSpec, "-") {
			segs := strings.SplitN(firstColorSpec, "-", 2)
			if len(segs) == 2 {
				parsedTol, err := parseTolRGB(segs[1])
				if err != nil {
					imageViewer.ClearTestOverlay()
					showFindMultiResultDialog(w, err.Error())
					return
				}
				defaultTol = parsedTol
			}
		}

		points, err := parseMultiPointString(pointsStr, defaultTol)
		if err != nil {
			imageViewer.ClearTestOverlay()
			showFindMultiResultDialog(w, err.Error())
			return
		}

		// 找色代码中第 2 个及之后的点是相对第一个点的偏移坐标，先还原为绝对坐标再比对
		// （还原后下面的 dx=p.X-baseX 计算正好得到原始偏移量）
		if mode == "找色" && len(points) > 1 {
			for i := 1; i < len(points); i++ {
				points[i].X += points[0].X
				points[i].Y += points[0].Y
			}
		}

		img := imageViewer.image
		bounds := img.Bounds()
		if mode == "比色" {
			for _, p := range points {
				if p.X < bounds.Min.X || p.X >= bounds.Max.X || p.Y < bounds.Min.Y || p.Y >= bounds.Max.Y {
					imageViewer.ClearTestOverlay()
					showFindMultiResultDialog(w, fmt.Sprintf("比色失败：坐标超出图片范围 (%d,%d)", p.X, p.Y))
					return
				}
				if !withinTolerance(img.At(p.X, p.Y), p.Want, p.Tol) {
					imageViewer.ClearTestOverlay()
					showFindMultiResultDialog(w, fmt.Sprintf("比色失败：(%d,%d) 期望 #%02X%02X%02X 偏差 #%02X%02X%02X", p.X, p.Y, p.Want.R, p.Want.G, p.Want.B, p.Tol.R, p.Tol.G, p.Tol.B))
					return
				}
			}
			imageViewer.ClearTestOverlay()

			// 比色模式：显示 detectsMultiColors 模板（点串为绝对坐标，直接引用「测试」表第 6 字段）
			detectTemplate := fmt.Sprintf(
				"local 测试={%d, %d, %d, %d, \"%s\", \"%s\", 100,0,0,0};\n"+
					"local ok = detectsMultiColors(测试[6])\n"+
					"if ok == 1 then\n"+
					"print(\"all colors match\")\n"+
					"end",
				x1, y1, x2, y2, firstColorSpec, pointsStr,
			)
			showFindTemplateResultDialog(w, detectTemplate, "-- 全部匹配 (ok == 1)")
			return
		}

		base := points[0]
		baseX, baseY := base.X, base.Y

		minX := min(x1, x2)
		minY := min(y1, y2)
		maxX := max(x1, x2)
		maxY := max(y1, y2)

		if minX < bounds.Min.X {
			minX = bounds.Min.X
		}
		if minY < bounds.Min.Y {
			minY = bounds.Min.Y
		}
		if maxX >= bounds.Max.X {
			maxX = bounds.Max.X - 1
		}
		if maxY >= bounds.Max.Y {
			maxY = bounds.Max.Y - 1
		}

		results := make([]string, 0, 16)
		for y := minY; y <= maxY; y++ {
			for x := minX; x <= maxX; x++ {
				basePixel := img.At(x, y)
				if !withinTolerance(basePixel, base.Want, base.Tol) {
					continue
				}
				ok := true
				for i := 1; i < len(points); i++ {
					p := points[i]
					dx := p.X - baseX
					dy := p.Y - baseY
					tx := x + dx
					ty := y + dy
					if tx < bounds.Min.X || tx >= bounds.Max.X || ty < bounds.Min.Y || ty >= bounds.Max.Y {
						ok = false
						break
					}
					if !withinDeltaTolerance(basePixel, img.At(tx, ty), base.Want, p.Want, p.Tol) {
						ok = false
						break
					}
				}
				if ok {
					results = append(results, fmt.Sprintf("%d,%d", x, y))
					if len(results) >= 200 {
						break
					}
				}
			}
			if len(results) >= 200 {
				break
			}
		}

		imageViewer.ClearTestOverlay()

		// 找色模式：测试结果显示 Lua 找色调用模板，方便直接复制使用
		// findMultiColors 的参数直接引用「测试」表字段：
		// [1..4]=区域 [5]=首色 [6]=点串 [8..10]=d1,d2,d3 扫描方向（[7] 为相似度 100，findMultiColors 不用）
		// 调用语句不换行，整行输出，末尾附 print(x, y)
		findTemplate := fmt.Sprintf(
			"local 测试={%d, %d, %d, %d, \"%s\", \"%s\", 100,0,0,0};\n"+
				"local x, y = findMultiColors(测试[1], 测试[2], 测试[3], 测试[4], 测试[5], 测试[6], 测试[8], 测试[9], 测试[10])\n"+
				"print(x, y)",
			x1, y1, x2, y2, firstColorSpec, pointsStr,
		)

		if len(results) == 0 {
			showFindTemplateResultDialog(w, findTemplate, "-- 未找到")
			return
		}
		showFindTemplateResultDialog(w, findTemplate, "-- 找到 "+strconv.Itoa(len(results))+" 个\n"+strings.Join(results, "\n"))
	}

	testBtn := widget.NewButtonWithIcon("测试", theme.SearchIcon(), testFunc)
	testBtn.Importance = widget.HighImportance

	binaryFunc := func() {
		if imageViewer == nil || imageViewer.image == nil {
			showFindMultiResultDialog(w, "请先加载/截图图片后再二值化")
			return
		}

		code := ""
		if codeDisplayEntry != nil {
			code = strings.TrimSpace(codeDisplayEntry.Text)
		}
		if code == "" {
			code = generateColorCode()
			if codeDisplayEntry != nil {
				codeDisplayEntry.SetText(code)
			}
		}

		x1, y1, x2, y2, firstColorSpec, pointsStr, err := extractGeneratedPattern(code)
		if err != nil {
			showFindMultiResultDialog(w, err.Error())
			return
		}

		// 检查是否有手动选择的矩形范围，如果有则优先使用
		if imageViewer.manualRectSelected && len(imageViewer.markRects) > 0 {
			rect := imageViewer.markRects[0]
			x1, y1, x2, y2 = rect.X1, rect.Y1, rect.X2, rect.Y2
		}

		firstHex := strings.TrimSpace(firstColorSpec)
		tolHex := "101010" // 默认偏色值

		// 首先尝试从firstColorSpec中解析偏色值
		if strings.Contains(firstColorSpec, "-") {
			segs := strings.SplitN(firstColorSpec, "-", 2)
			if len(segs) == 2 {
				firstHex = segs[0]
				tolHex = segs[1]
			}
		} else {
			// 如果firstColorSpec中没有偏色值，尝试从颜色点列表中获取第一个点的偏色值
			points, err := parseMultiPointString(pointsStr, color.RGBA{R: 0x10, G: 0x10, B: 0x10, A: 255})
			if err == nil && len(points) > 0 {
				// 使用第一个点的偏色值
				tolHex = fmt.Sprintf("%02x%02x%02x", points[0].Tol.R, points[0].Tol.G, points[0].Tol.B)
			}
		}

		firstHex = strings.TrimPrefix(firstHex, "#")
		tolHex = strings.TrimPrefix(tolHex, "#")

		if firstHex == "" || len(firstHex) != 6 {
			points, err := parseMultiPointString(pointsStr, color.RGBA{R: 0, G: 0, B: 0, A: 255})
			if err != nil || len(points) == 0 {
				showFindMultiResultDialog(w, "二值化失败：第一个颜色值解析失败")
				return
			}
			firstHex = fmt.Sprintf("%02x%02x%02x", points[0].Want.R, points[0].Want.G, points[0].Want.B)
			tolHex = fmt.Sprintf("%02x%02x%02x", points[0].Tol.R, points[0].Tol.G, points[0].Tol.B)
		}

		want, err := parseHexRGB(firstHex)
		if err != nil {
			showFindMultiResultDialog(w, err.Error())
			return
		}
		tol, err := parseTolRGB(tolHex)
		if err != nil {
			showFindMultiResultDialog(w, err.Error())
			return
		}

		src := imageViewer.image
		dst := image.NewNRGBA(src.Bounds())

		bounds := dst.Bounds()
		minX := min(x1, x2)
		minY := min(y1, y2)
		maxX := max(x1, x2)
		maxY := max(y1, y2)

		if minX < bounds.Min.X {
			minX = bounds.Min.X
		}
		if minY < bounds.Min.Y {
			minY = bounds.Min.Y
		}
		if maxX > bounds.Max.X {
			maxX = bounds.Max.X
		}
		if maxY > bounds.Max.Y {
			maxY = bounds.Max.Y
		}
		if minX >= maxX || minY >= maxY {
			showFindMultiResultDialog(w, "二值化范围无效")
			return
		}

		white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		black := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
		transparent := color.NRGBA{R: 0, G: 0, B: 0, A: 0}

		// 将整个图像初始化为透明
		for i := 0; i < len(dst.Pix); i += 4 {
			dst.Pix[i+0] = transparent.R
			dst.Pix[i+1] = transparent.G
			dst.Pix[i+2] = transparent.B
			dst.Pix[i+3] = transparent.A
		}

		// 只在指定范围内进行二值化处理
		for yy := minY; yy < maxY; yy++ {
			for xx := minX; xx < maxX; xx++ {
				if withinTolerance(src.At(xx, yy), want, tol) {
					dst.SetNRGBA(xx, yy, white)
				} else {
					dst.SetNRGBA(xx, yy, black)
				}
			}
		}

		// 在二值化结果的范围内添加红色边框（1像素宽）
		red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}

		// 绘制上边框
		for x := minX; x < maxX && x < bounds.Max.X; x++ {
			if minY >= bounds.Min.Y && minY < bounds.Max.Y {
				dst.SetNRGBA(x, minY, red)
			}
		}
		// 绘制下边框
		for x := minX; x < maxX && x < bounds.Max.X; x++ {
			if maxY-1 >= bounds.Min.Y && maxY-1 < bounds.Max.Y {
				dst.SetNRGBA(x, maxY-1, red)
			}
		}
		// 绘制左边框
		for y := minY; y < maxY && y < bounds.Max.Y; y++ {
			if minX >= bounds.Min.X && minX < bounds.Max.X {
				dst.SetNRGBA(minX, y, red)
			}
		}
		// 绘制右边框
		for y := minY; y < maxY && y < bounds.Max.Y; y++ {
			if maxX-1 >= bounds.Min.X && maxX-1 < bounds.Max.X {
				dst.SetNRGBA(maxX-1, y, red)
			}
		}

		// 保存当前标签页的数据（包含颜色点信息）
		saveCurrentTabData()

		// 直接在当前图像查看器上应用二值化结果，而不是创建新标签页
		imageViewer.image = dst
		imageViewer.displayImage.Image = dst
		imageViewer.isBinarized = true
		imageViewer.Refresh()

		// 不再清空颜色点列表和生成的代码，保持原有数据
		// colorPoints = make([]ColorPoint, 0)
		// if codeDisplayEntry != nil {
		// 	codeDisplayEntry.SetText("")
		// }
		// if refreshColorList != nil {
		// 	refreshColorList()
		// }
	}

	genRow := container.NewGridWithColumns(2, genBtn, testBtn)

	// 设置全局触发生成代码函数，供键盘快捷键使用
	triggerGenerateCode = generateCodeFunc
	triggerTestAction = testFunc

	// 智能取色按钮
	smartPickBtn := widget.NewButtonWithIcon("智能取色", theme.ColorChromaticIcon(), func() {
		openSmartPickWindow(w)
	})
	smartPickBtn.Importance = widget.MediumImportance

	// 使用固定高度容器包装（高度设为35）
	gridRow := newFixedHeightContainer(smartPickBtn, 35)

	// 如果imageViewer已经存在（不为nil），设置回调
	if imageViewer != nil {
		imageViewer.getGridParams = getGridParamsFunc
	}

	// 左侧工具栏布局 - 使用Border布局让代码显示框占据更多空间
	// 上部分：所有按钮和输入框（不包括生成按钮）
	topControls := container.New(&fixedWidthLayout{
		width:           200,
		padding:         2,
		verticalSpacing: 8, // 添加垂直间距
	},
		layout.NewSpacer(),
		customOptionRow, // 自定义选项（勾选框 + 输入框）
		screenshotRow,
		zoomRow, // 放大缩小按钮行
		importBtn,
		saveBtn,
		rotateBtn,
		paintBtn, // ★ 裁剪画笔涂抹（异形图）独立窗口
		fontLibBtn, // 字库制作按钮
		gridRow,    // 点阵按钮行（固定高度）
		themeBtn,
		rectCoordRow,   // 添加坐标显示框
		colorOffsetRow, // 偏色值输入框
		colorModeRow,   // 找色模式选择
		layout.NewSpacer(),
	)

	// 设置代码显示框的最小高度
	codeDisplayEntry.SetMinRowsVisible(8) // 至少显示8行

	// 中间部分：代码显示框和生成按钮
	// 使用Border布局：代码显示框在中心（自动扩展），生成按钮在底部
	codeAndGenContainer := container.NewBorder(nil, genRow, nil, nil,
		container.NewPadded(codeDisplayEntry))

	// 整体布局：topControls 在顶部，codeAndGenContainer 在中心（自动扩展）
	leftPanel := container.NewBorder(topControls, nil, nil, nil, codeAndGenContainer)

	// 创建表格标题
	headerBg = canvas.NewRectangle(getHeaderBgColor(isDarkTheme)) // 使用更深的背景色
	headerBg.SetMinSize(fyne.NewSize(300, 40))

	// 表格标题 - 白色文字
	idHeader = canvas.NewText("序号", getTextColor(isDarkTheme))
	idHeader.Alignment = fyne.TextAlignCenter
	idHeader.TextSize = 14
	idHeader.TextStyle = fyne.TextStyle{Bold: true} // 使文字加粗

	posHeader = canvas.NewText("坐标", getTextColor(isDarkTheme))
	posHeader.Alignment = fyne.TextAlignCenter
	posHeader.TextSize = 14
	posHeader.TextStyle = fyne.TextStyle{Bold: true} // 使文字加粗

	colorHeader = canvas.NewText("颜色", getTextColor(isDarkTheme))
	colorHeader.Alignment = fyne.TextAlignCenter
	colorHeader.TextSize = 14
	colorHeader.TextStyle = fyne.TextStyle{Bold: true} // 使文字加粗

	offsetHeader = canvas.NewText("偏色", getTextColor(isDarkTheme))
	offsetHeader.Alignment = fyne.TextAlignCenter
	offsetHeader.TextSize = 14
	offsetHeader.TextStyle = fyne.TextStyle{Bold: true} // 使文字加粗

	statusHeader = canvas.NewText("状态", getTextColor(isDarkTheme))
	statusHeader.Alignment = fyne.TextAlignCenter
	statusHeader.TextSize = 14
	statusHeader.TextStyle = fyne.TextStyle{Bold: true} // 使文字加粗

	// 使用容器将标题文字排列 - 为颜色列分配更多空间
	// 使用一个具有权重的布局，让不同列有不同的宽度
	headerTextContainer := container.New(&weightedGridLayout{
		cols:    5,
		weights: []float32{1, 1.5, 2, 1.5, 1}, // 颜色和偏色列的权重更大
	},
		idHeader,
		posHeader,
		colorHeader,
		offsetHeader,
		statusHeader,
	)

	// 将背景和文字叠加
	tableHeader = container.NewStack(headerBg, headerTextContainer)

	// 定义更新表头的函数
	updateTableHeader = func() {
		// 更新表头背景颜色
		headerBg.FillColor = getHeaderBgColor(isDarkTheme)
		headerBg.Refresh()

		// 更新表头文字颜色
		textColor := getTextColor(isDarkTheme)
		idHeader.Color = textColor
		posHeader.Color = textColor
		colorHeader.Color = textColor
		offsetHeader.Color = textColor
		statusHeader.Color = textColor

		idHeader.Refresh()
		posHeader.Refresh()
		colorHeader.Refresh()
		offsetHeader.Refresh()
		statusHeader.Refresh()
	}

	// 创建表格更新函数
	updateTableSelection = func() {
		if tableContent != nil {
			// 使用fyne.Do确保在主线程中执行UI更新
			fyne.Do(func() {
				tableContent.Refresh()
			})
		}
	}

	// 创建表格内容 - 使用透明背景
	tableContent = widget.NewList(
		// 列表长度
		func() int {
			return len(colorPoints)
		},
		// 创建单元格
		func() fyne.CanvasObject {
			// 创建单元格文字
			idText := canvas.NewText("", getTextColor(isDarkTheme))
			idText.Alignment = fyne.TextAlignCenter

			posText := canvas.NewText("", getTextColor(isDarkTheme))
			posText.Alignment = fyne.TextAlignCenter

			colorEntry := widget.NewEntry()
			colorEntry.SetPlaceHolder("FFFFFF")
			colorEntry.MultiLine = false
			colorEntry.Wrapping = fyne.TextWrapOff
			colorEntry.Scroll = fyne.ScrollNone

			// 创建偏色输入框
			offsetEntry := widget.NewEntry()
			offsetEntry.SetPlaceHolder("101010")
			offsetEntry.MultiLine = false // 单行显示
			offsetEntry.Wrapping = fyne.TextWrapOff
			offsetEntry.Scroll = fyne.ScrollNone

			// 创建自定义颜色的复选框
			statusCheck := NewColorCheck(false, color.RGBA{100, 100, 255, 255}, func(bool) {})

			// 使用网格布局排列单元格 - 为颜色和偏色列分配更多空间
			cellContainer := container.New(&weightedGridLayout{
				cols:    5,
				weights: []float32{1, 1.5, 2, 1.5, 1},
			},
				idText,
				posText,
				colorEntry,
				offsetEntry,
				container.NewCenter(statusCheck),
			)

			// 创建可点击的行，使用透明背景
			row := newClickableTableRow(transparent, cellContainer, nil)

			return row
		},
		// 更新单元格
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id < len(colorPoints) {
				point := colorPoints[id]
				row := item.(*ClickableTableRow)
				cellContainer := row.content

				// 获取单元格内容
				idText := cellContainer.Objects[0].(*canvas.Text)
				posText := cellContainer.Objects[1].(*canvas.Text)
				colorEntry := cellContainer.Objects[2].(*widget.Entry)
				offsetEntry := cellContainer.Objects[3].(*widget.Entry)
				statusContainer := cellContainer.Objects[4].(*fyne.Container)

				lightRow := point.ID >= 8 && point.ID <= 14
				if lightRow {
					row.background.FillColor = color.White
					idText.Color = color.Black
					posText.Color = color.Black
				} else {
					row.background.FillColor = transparent
					textColor := getTextColor(isDarkTheme)
					idText.Color = textColor
					posText.Color = textColor
				}
				row.background.Refresh()

				// 获取并更新自定义复选框
				colorCheck := statusContainer.Objects[0].(*ColorCheck)

				// 设置复选框颜色
				colorCheck.Color = hexToColor(point.Color)

				// 更新文本内容
				idText.Text = strconv.Itoa(point.ID)
				posText.Text = point.Position
				colorEntry.OnChanged = nil
				colorEntry.SetText(strings.TrimPrefix(point.Color, "#"))
				colorEntry.OnChanged = func(text string) {
					raw := strings.TrimSpace(text)
					if raw == "" {
						colorPoints[id].Color = ""
						colorCheck.Color = hexToColor("")
						colorCheck.Refresh()
						return
					}

					t := validateOffset(raw)
					if t == "" {
						return
					}

					normalized := "#" + strings.ToLower(t)
					colorPoints[id].Color = normalized
					colorCheck.Color = hexToColor(normalized)
					colorCheck.Refresh()
				}

				// 设置偏色输入框的数据
				// 重要：先解除回调，防止 SetText 触发旧的 id 回调导致数据错乱
				offsetEntry.OnChanged = nil
				offsetEntry.SetText(point.Offset)
				offsetEntry.OnChanged = func(text string) {
					// 仅保存，生成代码时会验证
					colorPoints[id].Offset = strings.TrimSpace(text)
				}

				// 更新选中状态
				colorCheck.Checked = point.Selected
				colorCheck.OnChanged = func(checked bool) {
					colorPoints[id].Selected = checked
					updateTableSelection() // 使用前面声明的函数
				}

				// 刷新文字和复选框
				idText.Refresh()
				posText.Refresh()
				colorEntry.Refresh()
				offsetEntry.Refresh() // 刷新偏色输入框
				colorCheck.Refresh()
			}
		},
	)

	// 将表格包装在一个滚动容器中
	tableScroll := container.NewVScroll(tableContent)

	// 创建右侧面板：表头 + 滚动表格内容
	binarizeBtn := widget.NewButtonWithIcon("二值化", theme.ColorAchromaticIcon(), binaryFunc)
	binarizeBtn.Importance = widget.HighImportance

	clearFunc := func() {
		if imageViewer != nil {
			imageViewer.ClearMarks()
		} else {
			colorPoints = colorPoints[:0]
			if rectCoordEntry != nil {
				rectCoordEntry.SetText("")
			}
			if codeDisplayEntry != nil {
				codeDisplayEntry.SetText("")
			}
			if refreshColorList != nil {
				refreshColorList()
			}
		}

		if currentTab != nil {
			if tabData, ok := tabDataMap[currentTab]; ok && tabData != nil {
				tabData.colorPoints = make([]ColorPoint, 0)
				tabData.markRects = make([]MarkRect, 0)
				tabData.manualRectSelected = false
				tabData.generatedCode = ""
			}
		}
	}
	clearBtn := widget.NewButtonWithIcon("清除", theme.ContentClearIcon(), clearFunc)
	clearBtn.Importance = widget.HighImportance
	triggerClearAction = clearFunc

	bottomRow := container.NewPadded(container.NewGridWithColumns(2, binarizeBtn, clearBtn))
	rightContent := container.NewBorder(tableHeader, bottomRow, nil, nil, tableScroll)
	rightPanel := container.New(&fixedWidthLayout{width: 320}, rightContent)

	// 创建一个外层容器，包含三个区域，中间是标签页容器
	mainContent := container.NewBorder(
		nil,        // 顶部
		nil,        // 底部
		leftPanel,  // 左侧
		rightPanel, // 右侧
		tabs,       // 中间区域使用标签页容器
	)

	// 设置窗口内容并显示
	w.SetContent(mainContent)

	// 设置拖放图片功能
	w.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		// 处理拖放的文件
		for _, uri := range uris {
			filePath := uri.Path()
			ext := strings.ToLower(filepath.Ext(filePath))

			// 检查是否为支持的图片格式
			if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".bmp" {
				continue
			}

			// 在 goroutine 中加载图片
			go func(path string, extension string) {
				// 读取文件内容
				data, err := ioutil.ReadFile(path)
				if err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("读取文件失败: %v", err), w)
					})
					return
				}

				// 根据文件扩展名解码图像
				var img image.Image

				switch extension {
				case ".png":
					img, err = png.Decode(bytes.NewReader(data))
				case ".jpg", ".jpeg":
					img, err = jpeg.Decode(bytes.NewReader(data))
				case ".bmp":
					img, err = bmp.Decode(bytes.NewReader(data))
				default:
					// 尝试自动检测格式
					img, _, err = image.Decode(bytes.NewReader(data))
				}

				if err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("解码图像失败: %v", err), w)
					})
					return
				}

				// 转换为NRGBA格式
				img = convertToNRGBA(img)

				// 在主线程中更新UI
				fyne.Do(func() {
					// 保存当前标签页的数据
					saveCurrentTabData()

					// 创建新的图像查看器和标签页
					newImageViewer := NewImageViewer()
					newMagnifier := NewMagnifierWidget()

					newImgContainer := container.New(&imageAreaLayout{}, newImageAreaBackground(newImageViewer), newImageViewer)
					newScrollContainer := container.NewScroll(newImgContainer)

					newImageViewer.scrollContainer = newScrollContainer
					newImageViewer.magnifier = newMagnifier
					newImageViewer.window = w
					newImageViewer.getGridParams = getGridParamsFunc // 设置点阵参数回调
					newImageViewer.SetImage(img)

					// 设置焦点到 ImageViewer，使键盘快捷键生效
					w.Canvas().Focus(newImageViewer)

					// 创建新标签页
					newScrollWithMagnifier := container.NewStack(newScrollContainer, newMagnifier)
					tabCounter++

					// 使用文件名作为标签名称
					tabName := filepath.Base(path)
					newTab := container.NewTabItem(tabName, newScrollWithMagnifier)

					// 初始化新标签页的数据
					tabDataMap[newTab] = &TabData{
						colorPoints:        make([]ColorPoint, 0),
						markRects:          make([]MarkRect, 0),
						manualRectSelected: false,
						imageViewer:        newImageViewer,
						generatedCode:      "",
					}

					tabs.Append(newTab)
					tabs.Select(newTab)

					// 更新当前标签页引用
					currentTab = newTab

					// 更新当前imageViewer引用
					imageViewer = newImageViewer

					// 清空矩形区域（颜色点全局保留）
					if rectCoordEntry != nil {
						rectCoordEntry.SetText("")
					}
					if codeDisplayEntry != nil {
						codeDisplayEntry.SetText("")
					}

					// 刷新表格
					if refreshColorList != nil {
						refreshColorList()
					}
				})
			}(filePath, ext)

			// 只处理第一个有效的图片文件
			break
		}
	})

	// 确保初始显示与当前系统主题匹配
	updateTableHeader()
	updateTableSelection()

	w.ShowAndRun()
}
