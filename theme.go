package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// 获取当前主题下适合的文字颜色
func getTextColor(isDark bool) color.Color {
	if isDark {
		return color.White
	} else {
		return color.Black
	}
}

// 获取当前主题下适合的表头背景色
func getHeaderBgColor(isDark bool) color.Color {
	if isDark {
		return headerBgColor
	} else {
		return lightHeaderBgColor
	}
}

// 判断颜色亮度，决定是使用白色还是黑色文字
func getContrastColor(bgColor color.Color) color.Color {
	r, g, b, _ := bgColor.RGBA()
	// 转换为0-255范围
	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)

	// 计算亮度 (亮度公式: 0.299*R + 0.587*G + 0.114*B)
	brightness := 0.299*float64(r8) + 0.587*float64(g8) + 0.114*float64(b8)

	// 如果亮度大于阈值，使用黑色，否则使用白色
	if brightness > 128 {
		return color.Black
	}
	return color.White
}

// 自定义主题，使用微软雅黑字体的深色主题
type myTheme struct {
	fyne.Theme
}

// 设置深色主题
func (m myTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	// 如果是深色主题，使用深色变体
	if variant == theme.VariantDark || isDarkTheme {
		// 为下拉框背景设置更明显的颜色
		if name == theme.ColorNameInputBackground {
			return color.NRGBA{60, 60, 60, 255} // 深色主题下使用深灰色作为输入框背景
		} else if name == theme.ColorNameFocus {
			// 消除焦点背景，使其与输入框背景相同
			return color.NRGBA{60, 60, 60, 255}
		}
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}

	// 如果是浅色主题，对特定元素进行自定义
	if name == theme.ColorNameBackground {
		return color.NRGBA{240, 240, 240, 255} // 使用浅灰色背景而非纯白色
	} else if name == theme.ColorNameButton {
		// 浅色主题下按钮背景使用浅灰色，以便与背景区分
		return color.NRGBA{220, 220, 220, 255}
	} else if name == theme.ColorNameInputBackground {
		// 浅色主题下为下拉框设置背景色
		return color.NRGBA{220, 220, 220, 255}
	} else if name == theme.ColorNameFocus {
		// 消除焦点背景，使其与输入框背景相同
		return color.NRGBA{220, 220, 220, 255}
	} else if name == theme.ColorNameShadow {
		// 增强浅色主题下的阴影可见度
		return color.NRGBA{0, 0, 0, 40}
	}

	// 其他颜色使用默认主题的浅色变体
	return theme.DefaultTheme().Color(name, theme.VariantLight)
}

// 设置尺寸
func (m myTheme) Size(name fyne.ThemeSizeName) float32 {
	// 设置滚动条始终为粗的样式
	if name == theme.SizeNameScrollBar {
		return 10
	}
	if name == theme.SizeNameScrollBarSmall {
		return 10
	}

	// 其他尺寸使用默认值
	return theme.DefaultTheme().Size(name)
}

// 自定义主题，只修改需要的主题属性
func newMyTheme() fyne.Theme {
	return &myTheme{Theme: theme.DarkTheme()}
}
