package main

// 三个工作路径的持久化（与 screenshot_urls.json 同模式：exe 同目录 json，启动加载、变更即存）
//   - screenshot_dir  主界面截图保存目录
//   - paint_img_dir   涂抹窗口图片目录（导入/保存 PNG 都用它）
//   - font_lib_dir    字库制作最近导入/导出的字库目录
// 保存用 read-modify-write + 互斥：各窗口只改自己的字段，互不覆盖。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type toolPaths struct {
	ScreenshotDir string `json:"screenshot_dir"`
	PaintImgDir   string `json:"paint_img_dir"`
	FontLibDir    string `json:"font_lib_dir"`
}

var toolPathsMu sync.Mutex

func toolPathsFile() string {
	exePath, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exePath), "tool_paths.json")
	}
	return "tool_paths.json"
}

func loadToolPaths() toolPaths {
	var p toolPaths
	if data, err := os.ReadFile(toolPathsFile()); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	return p
}

// saveToolPaths 取最新配置 → 改一个字段 → 写回（并发安全）
func saveToolPaths(mutate func(*toolPaths)) {
	toolPathsMu.Lock()
	defer toolPathsMu.Unlock()
	p := loadToolPaths()
	mutate(&p)
	data, _ := json.MarshalIndent(p, "", "  ")
	_ = os.WriteFile(toolPathsFile(), data, 0644)
}
