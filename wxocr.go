//go:build windows
// +build windows

package main

// 微信 OCR 进程内直调（mmmojo_64.dll）2026-09-30 —— 对齐 myweb4ocr/wechat_ocr 的 Python 封装。
// 目录约定：<base>/weixin/{mmmojo_64.dll, Weixin.exe} + <base>/wxocr/（识别模型 .xnet）。

// ★ OCR 引擎目录。⛔ 与 8080 的 python 服务（ocr_server.py）**互斥**：
//   mmmojo 的 OCR 后端全局只能有一个环境，服务开着时直调会静默超时（python 侧实测同样如此）。
// 链路：LoadDLL → InitializeMMMojo → CreateMMMojoEnvironment → 设 8 个回调 + 启动参数/开关
//       → Start → (引擎子进程连上后 connected=true) → OcrRequest pb（图片绝对路径）→ ReadPush
//       回调收 OcrResponse pb → 按 task_id 路由 → 解析行框/逐字文本/逐字 bbox。
// ⚠️ 引擎在工具进程内跑：回调里只做解析+投递 channel，绝不做重活/阻塞。

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// ★ OCR 引擎目录解析：优先用 **exe 同目录的 dlst**（自带 weixin/ + wxocr/ ⇒ 工具可独立分发），
//   没有则兜底开发机引擎目录 E:\myandroid\myweb4ocr。
//   ⛔ 与 8080 的 python 服务（ocr_server.py）**互斥**：
//   mmmojo 的 OCR 后端全局只能有一个环境，服务开着时直调会静默超时（python 侧实测同样如此）。
func wxOcrEngineDir() string {
	if exe, err := os.Executable(); err == nil {
		d := filepath.Join(filepath.Dir(exe), "dlst")
		if st, err := os.Stat(filepath.Join(d, "weixin", "mmmojo_64.dll")); err == nil && !st.IsDir() {
			return d
		}
	}
	return `E:\myandroid\myweb4ocr`
}

// ── 对外结构 ──────────────────────────────────────────────────────────────

type wxOcrLine struct {
	Text      string
	Chars     []string
	CharBoxes [][]float64 // 每字 [l,t,r,b]（与 Chars 一一对应；可能为空）
	Left, Top, Right, Bottom float64
	Rate      float64
}

// ── 常量（对齐 mmmojo_dll.py / xplugin_manager.py）────────────────────────

const (
	wxCallbackUserData        = 0
	wxCallbackReadPush        = 1
	wxCallbackReadPull        = 2
	wxCallbackReadShared      = 3
	wxCallbackRemoteConnect   = 4
	wxCallbackRemoteDisconn   = 5
	wxCallbackProcessLaunched = 6
	wxCallbackLaunchFailed    = 7
	wxCallbackMojoError       = 8

	wxParamHostProcess   = 0
	wxParamLoopStartThread = 1
	wxParamExePath       = 2
	wxParamLogPath       = 3

	wxMethodPush = 1 // kMMPush

	// ★ 2026-09-30 实测定位：这版 Weixin.exe 只认 **wx4 协议** ——
	//   请求 request_id=10010（UtilityResampleImagePullReq），结果经 10011 ReadPush 推回。
	//   老的 OcrRequest/id=1（ocr_manager.SendOCRTask 路径）发出去**石沉大海**（连接正常、无响应），
	//   这就是此前 DLL 直调 90s 超时的根因（python 侧 ocr_server.py 用 10010 所以一直是好的）。
	wxReqIDWx4  = 10010
	wxRespPushID = 10011 // 引擎推送识别结果的 request_id
)

// ── Manager ───────────────────────────────────────────────────────────────

type wxOcrManager struct {
	baseDir string

	proc   *syscall.LazyDLL
	pInit, pShutdown                   *syscall.LazyProc
	pCreateEnv, pSetCb, pSetParam      *syscall.LazyProc
	pAppendSwitch, pStart, pStop, pRemoveEnv *syscall.LazyProc
	pCreateWrite, pGetWriteReq, pSendWrite   *syscall.LazyProc
	pGetReadReq, pRemoveReadInfo       *syscall.LazyProc

	mu        sync.Mutex
	env       uintptr
	connected bool
	startErr  string
	results   map[uint32]chan *wxOcrResp
	nextTask  uint32
}

var (
	wxOcrSingleton *wxOcrManager
	wxOcrMu        sync.Mutex
)

type wxOcrResp struct {
	TaskID uint32
	Lines  []wxOcrLine
}

// wxOcrRun 识别一张图（绝对路径），返回行列表。进程内引擎只初始化一次。
func wxOcrRun(baseDir, imgPath string, timeout time.Duration) (lines []wxOcrLine, err error) {
	defer func() { // 引擎在进程内，崩了不能带走工具
		if r := recover(); r != nil {
			err = fmt.Errorf("微信OCR引擎崩溃: %v", r)
		}
	}()
	mgr, err := wxOcrGet(baseDir)
	if err != nil {
		return nil, err
	}
	return mgr.run(imgPath, timeout)
}

func wxOcrGet(baseDir string) (*wxOcrManager, error) {
	wxOcrMu.Lock()
	defer wxOcrMu.Unlock()
	if wxOcrSingleton != nil {
		return wxOcrSingleton, nil
	}
	mgr, err := newWxOcrManager(baseDir)
	if err != nil {
		return nil, err
	}
	wxOcrSingleton = mgr
	return mgr, nil
}

func newWxOcrManager(baseDir string) (*wxOcrManager, error) {
	dllPath := filepath.Join(baseDir, "weixin", "mmmojo_64.dll")
	exePath := filepath.Join(baseDir, "weixin", "Weixin.exe")
	wxocrDir := filepath.Join(baseDir, "wxocr")
	if _, err := os.Stat(dllPath); err != nil {
		return nil, fmt.Errorf("找不到 %s（微信OCR引擎目录不对）", dllPath)
	}
	if _, err := os.Stat(exePath); err != nil {
		return nil, fmt.Errorf("找不到 %s", exePath)
	}

	m := &wxOcrManager{
		baseDir: baseDir,
		results: make(map[uint32]chan *wxOcrResp),
		nextTask: 1,
	}
	dll := syscall.NewLazyDLL(dllPath)
	m.proc = dll
	m.pInit = dll.NewProc("InitializeMMMojo")
	m.pShutdown = dll.NewProc("ShutdownMMMojo")
	m.pCreateEnv = dll.NewProc("CreateMMMojoEnvironment")
	m.pSetCb = dll.NewProc("SetMMMojoEnvironmentCallbacks")
	m.pSetParam = dll.NewProc("SetMMMojoEnvironmentInitParams")
	m.pAppendSwitch = dll.NewProc("AppendMMSubProcessSwitchNative")
	m.pStart = dll.NewProc("StartMMMojoEnvironment")
	m.pStop = dll.NewProc("StopMMMojoEnvironment")
	m.pRemoveEnv = dll.NewProc("RemoveMMMojoEnvironment")
	m.pCreateWrite = dll.NewProc("CreateMMMojoWriteInfo")
	m.pGetWriteReq = dll.NewProc("GetMMMojoWriteInfoRequest")
	m.pSendWrite = dll.NewProc("SendMMMojoWriteInfo")
	m.pGetReadReq = dll.NewProc("GetMMMojoReadInfoRequest")
	m.pRemoveReadInfo = dll.NewProc("RemoveMMMojoReadInfo")
	if err := dll.Load(); err != nil {
		return nil, fmt.Errorf("加载 %s 失败: %v", dllPath, err)
	}

	// ★ 引擎子进程继承本进程 cwd——切到引擎目录（python 服务 cwd 就是这里）
	if oldWd, err := os.Getwd(); err == nil && oldWd != baseDir {
		os.Chdir(baseDir)
		defer os.Chdir(oldWd)
	}

	// InitializeMMMojo(0, NULL)
	m.pInit.Call(0, 0)
	env, _, _ := m.pCreateEnv.Call()
	if env == 0 {
		return nil, fmt.Errorf("CreateMMMojoEnvironment 失败")
	}
	m.env = env

	// user_data：DLL 只透传，Go 回调靠闭包拿 manager，塞个非 0 占位
	m.pSetCb.Call(env, wxCallbackUserData, 1)
	// 8 个回调（签名见 default_callback.py callbacks_def）
	cbs := map[int]uintptr{
		wxCallbackReadPush:        syscall.NewCallback(m.onReadPush),
		wxCallbackReadPull:        syscall.NewCallback(m.onIgnore3),
		wxCallbackReadShared:      syscall.NewCallback(m.onIgnore3),
		wxCallbackRemoteConnect:   syscall.NewCallback(m.onConnect),
		wxCallbackRemoteDisconn:   syscall.NewCallback(m.onIgnore1),
		wxCallbackProcessLaunched: syscall.NewCallback(m.onIgnore1),
		wxCallbackLaunchFailed:    syscall.NewCallback(m.onLaunchFailed),
		wxCallbackMojoError:       syscall.NewCallback(m.onMojoError),
	}
	for t := wxCallbackReadPush; t <= wxCallbackMojoError; t++ {
		m.pSetCb.Call(env, uintptr(t), cbs[t])
	}

	// 启动参数：kMMHostProcess=1（int）、kMMExePath=Weixin.exe（wchar*）
	m.pSetParam.Call(env, wxParamHostProcess, 1)
	exe16, _ := syscall.UTF16PtrFromString(exePath)
	m.pSetParam.Call(env, wxParamExePath, uintptr(unsafe.Pointer(exe16)))

	// 子进程开关：type=wxocr / app-path=模型目录 / no-sandbox / user-lib-dir
	appendSwitch := func(k, v string) {
		k16, _ := syscall.BytePtrFromString(k)
		v16, _ := syscall.UTF16PtrFromString(v)
		m.pAppendSwitch.Call(env, uintptr(unsafe.Pointer(k16)), uintptr(unsafe.Pointer(v16)))
	}
	appendSwitch("type", "wxocr")
	appendSwitch("app-path", wxocrDir)
	appendSwitch("no-sandbox", "")
	appendSwitch("user-lib-dir", filepath.Join(baseDir, "weixin"))

	m.pStart.Call(env)

	// 等引擎子进程连上（实测 ~5s 内）
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		ok := m.connected
		m.mu.Unlock()
		if ok {
			return m, nil
		}
		if m.startErr != "" {
			return nil, fmt.Errorf("微信OCR引擎启动失败: %s", m.startErr)
		}
		time.Sleep(300 * time.Millisecond)
	}
	m.mu.Lock()
	se := m.startErr
	m.mu.Unlock()
	if se != "" {
		return nil, fmt.Errorf("微信OCR引擎启动失败: %s", se)
	}
	return nil, fmt.Errorf("微信OCR引擎连接超时（15s）")
}

// ── 回调（引擎线程调用；只投递，不阻塞）──────────────────────────────────

func (m *wxOcrManager) onConnect(isConnected, _ uintptr) uintptr {
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] Connect connected=%v\n", isConnected != 0)
	}
	m.mu.Lock()
	m.connected = isConnected != 0
	m.mu.Unlock()
	return 0
}

func (m *wxOcrManager) onLaunchFailed(errCode, _ uintptr) uintptr {
	fmt.Fprintf(os.Stderr, "[go-wxocr] LaunchFailed err=%d\n", errCode)
	m.mu.Lock()
	m.startErr = fmt.Sprintf("引擎进程启动失败 err=%d", errCode)
	m.mu.Unlock()
	return 0
}

func (m *wxOcrManager) onMojoError(errBuf, errSize, _ uintptr) uintptr {
	msg := ""
	if errBuf != 0 && errSize > 0 {
		b := unsafe.Slice((*byte)(unsafe.Pointer(errBuf)), int(errSize))
		msg = string(b)
	}
	fmt.Fprintf(os.Stderr, "[go-wxocr] MojoError: %s\n", msg)
	m.mu.Lock()
	m.startErr = "mojo 错误: " + msg
	m.mu.Unlock()
	return 0
}

func (m *wxOcrManager) onIgnore1(a uintptr) uintptr {
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] Ignore1 a=%x\n", a)
	}
	return 0
}
func (m *wxOcrManager) onIgnore3(id, info, _ uintptr) uintptr {
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] ReadPull/Shared id=%d info=%x\n", id, info)
	}
	return 0
}

// onReadPush：识别结果推送（request_id == 10011）
func (m *wxOcrManager) onReadPush(requestID, requestInfo, _ uintptr) uintptr {
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] ReadPush id=%d info=%x\n", requestID, requestInfo)
	}
	if requestID != wxRespPushID || requestInfo == 0 {
		return 0
	}
	var size uint32
	pbPtr, _, _ := m.pGetReadReq.Call(requestInfo, uintptr(unsafe.Pointer(&size)))
	if pbPtr == 0 || size <= 10 {
		return 0
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(pbPtr)), int(size))
	resp := wxParseWx4Resp(data)
	m.pRemoveReadInfo.Call(requestInfo)

	m.mu.Lock()
	ch, ok := m.results[resp.TaskID]
	m.mu.Unlock()
	if ok {
		ch <- resp // 缓冲 1，必不阻塞
	}
	return 0
}

// ── 识别请求 ──────────────────────────────────────────────────────────────

func (m *wxOcrManager) run(imgPath string, timeout time.Duration) ([]wxOcrLine, error) {
	absPath, err := filepath.Abs(imgPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("图片不存在: %s", imgPath)
	}

	// 等连接
	deadline := time.Now().Add(8 * time.Second)
	for {
		m.mu.Lock()
		ok := m.connected
		m.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("微信OCR引擎未连接")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 任务号 1..32 轮转
	m.mu.Lock()
	m.nextTask++
	if m.nextTask > 32 {
		m.nextTask = 1
	}
	taskID := m.nextTask
	ch := make(chan *wxOcrResp, 1)
	m.results[taskID] = ch
	env := m.env
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.results, taskID)
		m.mu.Unlock()
	}()

	// WeChat 4.x ParseOCRReqMessage（对齐 wechat4_ocr.py:_build_wx4_req）：
	//   msg = {1: task_id, 2: pic_path, 3: 1, 4: 1, 6: {1:1, 2:1, 3:0}}
	//   ⛔ request_id 必须用 10010（见常量注释）；结果经 10011 ReadPush 推回。
	fwd := strings.ReplaceAll(absPath, `\`, "/")
	reqType := pbAppendVarint(pbAppendVarint(pbAppendVarint(nil, 1, 1), 2, 1), 3, 0)
	pb := pbAppendVarint(nil, 1, uint64(taskID))
	pb = pbAppendBytes(pb, 2, []byte(fwd))
	pb = pbAppendVarint(pb, 3, 1)
	pb = pbAppendVarint(pb, 4, 1)
	pb = pbAppendBytes(pb, 6, reqType)
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] req task=%d pb=%x\n", taskID, pb)
	}

	wi, _, callErr := m.pCreateWrite.Call(wxMethodPush, 0, wxReqIDWx4)
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] CreateWrite wi=%x err=%v\n", wi, callErr)
	}
	if wi == 0 {
		return nil, fmt.Errorf("CreateMMMojoWriteInfo 失败")
	}
	reqBuf, _, callErr := m.pGetWriteReq.Call(wi, uintptr(len(pb)))
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] GetWriteReq buf=%x err=%v\n", reqBuf, callErr)
	}
	if reqBuf == 0 {
		m.pRemoveReadInfo.Call(wi)
		return nil, fmt.Errorf("GetMMMojoWriteInfoRequest 失败")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(reqBuf)), len(pb)), pb)
	ok, _, callErr := m.pSendWrite.Call(env, wi)
	if os.Getenv("WXOCR_GO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[go-wxocr] SendWrite ok=%d err=%v env=%x\n", ok, callErr, env)
	}
	if ok == 0 {
		return nil, fmt.Errorf("SendMMMojoWriteInfo 失败")
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("OCR 结果为空")
		}
		return resp.Lines, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("OCR 超时（%v）", timeout)
	}
}

// ── protobuf 手写编解码（对齐 wechat4_ocr.py 的 _parse_wx4_resp/_parse_line）──

type pbField struct {
	fn, wt int
	num    uint64
	data   []byte
}

func pbVarintDecode(data []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, len(data)
}

func pbParseFields(data []byte) []pbField {
	var out []pbField
	for len(data) > 0 {
		tag, n := pbVarintDecode(data)
		if n == 0 {
			break
		}
		data = data[n:]
		fn, wt := int(tag>>3), int(tag&7)
		switch wt {
		case 0:
			v, n2 := pbVarintDecode(data)
			if n2 == 0 {
				return out
			}
			data = data[n2:]
			out = append(out, pbField{fn: fn, wt: wt, num: v})
		case 2:
			l, n2 := pbVarintDecode(data)
			if n2 == 0 || int(n2)+int(l) > len(data) {
				return out
			}
			data = data[n2:]
			out = append(out, pbField{fn: fn, wt: wt, data: data[:l]})
			data = data[l:]
		case 5: // fixed32
			if len(data) < 4 {
				return out
			}
			out = append(out, pbField{fn: fn, wt: wt, num: uint64(le32(data))})
			data = data[4:]
		case 1: // fixed64
			if len(data) < 8 {
				return out
			}
			data = data[8:]
		default:
			return out
		}
	}
	return out
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// ⚠️ 线格式：tag（field<<3|wire）在前，payload 在后
func pbAppendVarint(dst []byte, field int, v uint64) []byte {
	dst = append(dst, byte(uint64(field)<<3|0))
	for v > 0x7f {
		dst = append(dst, byte(0x80|(v&0x7f)))
		v >>= 7
	}
	return append(dst, byte(v))
}

func pbAppendBytes(dst []byte, field int, b []byte) []byte {
	dst = append(dst, byte(uint64(field)<<3|2))
	l := uint64(len(b))
	for l > 0x7f {
		dst = append(dst, byte(0x80|(l&0x7f)))
		l >>= 7
	}
	dst = append(dst, byte(l))
	return append(dst, b...)
}

func pbF32(v uint64) float64 { return float64(math.Float32frombits(uint32(v))) }

// wxParseWx4Resp 解析引擎推送的 OcrResponse（对齐 _parse_wx4_resp/_parse_line）
func wxParseWx4Resp(data []byte) *wxOcrResp {
	resp := &wxOcrResp{}
	for _, f := range pbParseFields(data) {
		if f.fn == 1 && f.wt == 0 {
			resp.TaskID = uint32(f.num)
		} else if f.fn == 3 && f.wt == 2 {
			for _, rf := range pbParseFields(f.data) {
				if rf.fn == 3 && rf.wt == 2 {
					resp.Lines = append(resp.Lines, wxParseLine(rf.data))
				}
			}
		}
	}
	return resp
}

func wxParseLine(data []byte) wxOcrLine {
	ln := wxOcrLine{}
	for _, f := range pbParseFields(data) {
		switch {
		case f.fn == 2 && f.wt == 2:
			ln.Text = string(f.data)
		case f.fn == 3 && f.wt == 5:
			ln.Rate = pbF32(f.num)
		case f.fn == 4 && f.wt == 2: // CharBlock{ 1=bbox, 2=char }
			var ch string
			var box []float64
			for _, cf := range pbParseFields(f.data) {
				if cf.fn == 2 && cf.wt == 2 {
					ch = string(cf.data)
				} else if cf.fn == 1 && cf.wt == 2 {
					box = wxParseCharBox(cf.data)
				}
			}
			if ch != "" {
				ln.Chars = append(ln.Chars, ch)
				ln.CharBoxes = append(ln.CharBoxes, box)
			}
		case f.fn == 5 && f.wt == 5:
			ln.Left = pbF32(f.num)
		case f.fn == 6 && f.wt == 5:
			ln.Top = pbF32(f.num)
		case f.fn == 7 && f.wt == 5:
			ln.Right = pbF32(f.num)
		case f.fn == 8 && f.wt == 5:
			ln.Bottom = pbF32(f.num)
		}
	}
	return ln
}

// wxParseCharBox CharBlock.bbox = 4 个角点子消息（1左上 2右上 3右下 4左下），各 {1:x, 2:y}
func wxParseCharBox(data []byte) []float64 {
	var xs, ys []float64
	for _, pf := range pbParseFields(data) {
		if pf.wt != 2 {
			continue
		}
		x, y := math.NaN(), math.NaN()
		for _, p := range pbParseFields(pf.data) {
			if p.wt == 5 {
				if p.fn == 1 {
					x = pbF32(p.num)
				} else if p.fn == 2 {
					y = pbF32(p.num)
				}
			}
		}
		if !math.IsNaN(x) && !math.IsNaN(y) {
			xs, ys = append(xs, x), append(ys, y)
		}
	}
	if len(xs) < 2 {
		return nil
	}
	l, r := xs[0], xs[0]
	t, b := ys[0], ys[0]
	for _, v := range xs {
		l = math.Min(l, v)
		r = math.Max(r, v)
	}
	for _, v := range ys {
		t = math.Min(t, v)
		b = math.Max(b, v)
	}
	return []float64{math.Round(l*100) / 100, math.Round(t*100) / 100,
		math.Round(r*100) / 100, math.Round(b*100) / 100}
}
