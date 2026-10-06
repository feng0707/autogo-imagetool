package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// 颜色点信息结构
type ColorPoint struct {
	ID       int
	Position string
	Color    string
	Offset   string // 添加偏色字段
	Selected bool
}

// 标签页数据结构 - 保存每个标签页的独立数据
type TabData struct {
	colorPoints        []ColorPoint // 该标签页的颜色点列表
	markRects          []MarkRect   // 该标签页的矩形标记
	manualRectSelected bool         // 是否手动框选了区域
	imageViewer        *ImageViewer // 该标签页的图像查看器
	generatedCode      string       // 该标签页生成的代码
}

// 全局变量定义
var (
	headerBgColor      = color.NRGBA{30, 30, 30, 255}    // 表头深色背景
	lightHeaderBgColor = color.NRGBA{220, 220, 220, 255} // 表头浅色背景
	transparent        = color.NRGBA{0, 0, 0, 0}         // 透明色

	// 主题状态变量 - 初始值会在程序启动时根据系统主题设置
	isDarkTheme = false

	// 截图地址下拉框（myweb3 服务器截图，地址列表持久化保存）
	screenshotURLSelect *widget.Select

	// 区域坐标显示
	rectCoordEntry *widget.Entry

	// 偏色值输入
	colorOffsetEntry *widget.Entry

	// 找色模式选择
	colorModeRadio *widget.RadioGroup

	// 图像查看器（当前活动的）
	imageViewer *ImageViewer

	// 代码显示框
	codeDisplayEntry *widget.Entry

	// 点阵模式状态
	gridModeEnabled   = false // 默认不启用点阵模式
	gridColsValue     = 4
	gridRowsValue     = 4
	gridSpacingValue  = 7
	screenshotSaveDir = ""

	//放大因子
	wholeSicale float32 = 1.0
)
var updateTableHeader func()

// 创建表格选中更新函数 - 前向声明
var updateTableSelection func()
var tableContent *widget.List
var tableHeader *fyne.Container
var headerBg *canvas.Rectangle
var idHeader, posHeader, colorHeader, offsetHeader, statusHeader *canvas.Text

type MarkPoint struct {
	X, Y  int
	Color color.Color
}

type MarkText struct {
	X, Y  int
	Text  string
	Color color.Color
}

// 定义矩形标记结构体
type MarkRect struct {
	X1, Y1 int // 起点
	X2, Y2 int // 终点
	Color  color.Color
}

var colorPoints []ColorPoint

// 全局标签页数据映射：tabItem -> TabData
var tabDataMap = make(map[*container.TabItem]*TabData)

// 当前活动的标签页
var currentTab *container.TabItem

// 全局刷新表格函数，会在main中设置
var refreshColorList func()

// 全局触发生成代码函数，会在main中设置
var triggerGenerateCode func()
var triggerTestAction func()
var triggerClearAction func()
