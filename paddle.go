//go:build windows
// +build windows

package main

// PaddleOCR-json 子进程封装（韩文等多语言 OCR，离线推理）2026-09-30。
//
// 协议（对齐官方 api/python/PPOCR_api.py 管道模式）：
//   1) 启动 PaddleOCR-json.exe（cwd=exe 目录，可带 --config_path=models/config_ko.txt 切语言）
//   2) 读 stdout 行，直到出现 "OCR init completed."（超时/进程退出 = 初始化失败）
//   3) 每次请求：向 stdin 写一行 JSON（{"image_path":"绝对路径"}）→ 从 stdout 读一行回包：
//      {"code":100, "data":[{"text":..,"box":[[x,y]×4],"score":..}], "time":..}
//      code=100 成功；其他为错误（data 是字符串）
//
// ⛔ 与微信 OCR（wxocr.go）互为独立引擎，可并存；但 PaddleOCR-json 子进程本身全局一个实例。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ★ PaddleOCR-json 引擎目录（exe + models/）。解析顺序：
//   1) exe 同目录的 dlst\paddle（正式分发形态）
//   2) 开发机源码树 dlst\paddle（go test / 开发态兜底）
func paddleEngineDir() string {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "dlst", "paddle"))
	}
	cands = append(cands, `E:\myandroid\AutoGo图色助手源码\AutoGo图色助手\dlst\paddle`)
	for _, d := range cands {
		if st, err := os.Stat(filepath.Join(d, paddleExeName)); err == nil && !st.IsDir() {
			return d
		}
	}
	return cands[0]
}

// 韩文语言配置（v1.4.1 主包 models/ 自带 简中/繁中/英/日/韩 五库，韩文= config_korean.txt）
const paddleKoConfig = "models/config_korean.txt"

const paddleExeName = "PaddleOCR-json.exe"

// ── 结构 ──────────────────────────────────────────────────────────────────

type paddleItem struct {
	Text  string      `json:"text"`
	Box   [][2]uint32 `json:"box"` // 四角点（顺序：左上/右上/右下/左下）
	Score float64     `json:"score"`
}

type paddleLine struct {
	Text  string
	Box   [][2]uint32
	Score float64
}

// Left/Top/Right/Bottom：四角点取外接矩形（供智能分割的行框用）
func (l paddleLine) Rect() (left, top, right, bottom float64) {
	for i, p := range l.Box {
		if i == 0 || float64(p[0]) < left {
			left = float64(p[0])
		}
		if i == 0 || float64(p[1]) < top {
			top = float64(p[1])
		}
		if float64(p[0]) > right {
			right = float64(p[0])
		}
		if float64(p[1]) > bottom {
			bottom = float64(p[1])
		}
	}
	return
}

type paddleProc struct {
	cmd    *exec.Cmd
	stdin  ioWriteCloser
	reader *bufio.Reader
	mu     sync.Mutex // 序列化每次请求（管道是单通道）
}

type ioWriteCloser interface {
	Write(p []byte) (n int, err error)
	Close() error
}

var (
	paddleMu     sync.Mutex
	paddleSingle *paddleProc
)

// ── 启动 / 单例 ───────────────────────────────────────────────────────────

// paddleEnsure 拉起（或复用）PaddleOCR-json 子进程。configRel 相对引擎目录（如 models/config_ko.txt）。
func paddleEnsure(engineDir, configRel string) (*paddleProc, error) {
	paddleMu.Lock()
	defer paddleMu.Unlock()
	if paddleSingle != nil {
		if p := paddleSingle; p.alive() {
			return p, nil
		}
		paddleSingle = nil // 挂了就重拉
	}
	exePath := filepath.Join(engineDir, paddleExeName)
	if _, err := os.Stat(exePath); err != nil {
		return nil, fmt.Errorf("找不到 %s", exePath)
	}
	cfgPath := filepath.Join(engineDir, configRel)
	if _, err := os.Stat(cfgPath); err != nil {
		return nil, fmt.Errorf("找不到语言配置 %s", cfgPath)
	}

	cmd := exec.Command(exePath, "--config_path", configRel)
	cmd.Dir = engineDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil // 丢弃 stderr（对齐官方 api）
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 %s 失败: %v", exePath, err)
	}

	p := &paddleProc{cmd: cmd, stdin: stdin, reader: bufio.NewReaderSize(stdout, 1<<20)}
	// 等初始化完成（首次要加载模型，给足时间）
	deadline := time.Now().Add(60 * time.Second)
	for {
		if time.Now().After(deadline) {
			p.kill()
			return nil, fmt.Errorf("PaddleOCR-json 初始化超时（60s）")
		}
		line, err := p.reader.ReadString('\n')
		if err != nil {
			p.kill()
			return nil, fmt.Errorf("PaddleOCR-json 初始化失败（进程退出）")
		}
		if containsASCII(line, "OCR init completed.") {
			break
		}
		// 其余启动期输出忽略（可能有加载进度等）
	}
	paddleSingle = p
	return p, nil
}

func (p *paddleProc) alive() bool {
	return p != nil && p.cmd != nil && p.cmd.Process != nil &&
		p.cmd.ProcessState == nil
}

func (p *paddleProc) kill() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	_, _ = p.cmd.Process.Wait()
}

func containsASCII(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ── 请求 ──────────────────────────────────────────────────────────────────

type paddleResp struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
	Time float64         `json:"time"`
}

// paddleOCRRun 识别一张本机图片（绝对路径），返回行列表。
// configRel 例如 "models/config_ko.txt"（韩文）。
func paddleOCRRun(engineDir, configRel, imgPath string, timeout time.Duration) ([]paddleLine, error) {
	absPath, err := filepath.Abs(imgPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("图片不存在: %s", imgPath)
	}
	p, err := paddleEnsure(engineDir, configRel)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	req := map[string]string{"image_path": absPath}
	jb, _ := json.Marshal(req)
	if _, err := p.stdin.Write(append(jb, '\n')); err != nil {
		return nil, fmt.Errorf("写入 OCR 请求失败: %v", err)
	}

	type once struct {
		line string
		err  error
	}
	ch := make(chan once, 1)
	go func() {
		line, err := p.reader.ReadString('\n')
		ch <- once{line, err}
	}()
	var line string
	select {
	case o := <-ch:
		if o.err != nil {
			paddleMu.Lock()
			paddleSingle = nil // 管道坏了，下次重拉
			paddleMu.Unlock()
			return nil, fmt.Errorf("读取 OCR 结果失败: %v", o.err)
		}
		line = o.line
	case <-time.After(timeout):
		return nil, fmt.Errorf("PaddleOCR 识别超时（%v）", timeout)
	}

	var resp paddleResp
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, fmt.Errorf("OCR 回包解析失败: %v（原文 %.120s）", err, line)
	}
	if resp.Code != 100 {
		msg := resp.Msg
		if msg == "" {
			msg = string(resp.Data)
		}
		return nil, fmt.Errorf("OCR 引擎错误 code=%d: %s", resp.Code, msg)
	}
	var items []paddleItem
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return nil, fmt.Errorf("OCR 结果解析失败: %v", err)
	}
	out := make([]paddleLine, 0, len(items))
	for _, it := range items {
		out = append(out, paddleLine{Text: it.Text, Box: it.Box, Score: it.Score})
	}
	return out, nil
}
