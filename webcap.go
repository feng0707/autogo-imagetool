package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// errCloudAuth 鉴权类错误（cloudDo 里 401 且重登失败）：调用方用 errors.Is 识别后应直接中止，
// 不能当「老服务器无此接口」降级继续跑
var errCloudAuth = errors.New("cloud auth")

// captureScreenViaLuaTouch 通过 LuaTouch 引擎 HTTP 服务直接截图
// addr 格式: IP:端口 或 http://IP:端口（如 192.168.31.169:10010）
// 只需 GET http://IP:端口/png 即可直接返回 PNG 图片
func captureScreenViaLuaTouch(addr string) (image.Image, error) {
	u := strings.TrimSpace(addr)
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	u = strings.TrimRight(u, "/")

	resp, err := http.Get(u + "/png")
	if err != nil {
		return nil, fmt.Errorf("连接引擎失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("截图失败: HTTP %d（引擎侧截屏不可用？）", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取截图数据失败: %v", err)
	}
	if len(data) < 100 {
		return nil, fmt.Errorf("截图失败（引擎侧截屏不可用？）")
	}
	return png.Decode(bytes.NewReader(data))
}

// screenshotURLsFile 返回截图地址列表的持久化文件路径（与 exe 同目录）
func screenshotURLsFile() string {
	exePath, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exePath), "screenshot_urls.json")
	}
	return "screenshot_urls.json"
}

// loadScreenshotURLs 读取已保存的截图地址列表
func loadScreenshotURLs() []string {
	data, err := os.ReadFile(screenshotURLsFile())
	if err != nil {
		return nil
	}
	var urls []string
	if json.Unmarshal(data, &urls) == nil {
		return urls
	}
	return nil
}

// saveScreenshotURLs 保存截图地址列表
func saveScreenshotURLs(urls []string) {
	data, _ := json.MarshalIndent(urls, "", "  ")
	_ = os.WriteFile(screenshotURLsFile(), data, 0644)
}

// ════════ ★ 2026-10-06 云控鉴权（与 VSCode 插件 1.4.50 同一套） ════════
// 云控调试接口已上锁：地址串带 ?k=远程调试授权码 → POST /api/debuglogin 换 token(24h) →
// 所有 /api/* 请求带 Bearer，401 自动重登一次再重试。token/code 按中控地址分开存。
var cloudAuth = struct {
	sync.Mutex
	tokens map[string]string // baseURL -> token
	codes  map[string]string // baseURL -> 授权码
}{tokens: map[string]string{}, codes: map[string]string{}}

// cloudLogin 用授权码换 token；成功返回 true
func cloudLogin(baseURL string) bool {
	cloudAuth.Lock()
	code := cloudAuth.codes[baseURL]
	cloudAuth.Unlock()
	if code == "" {
		return false
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(baseURL+"/api/debuglogin", "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var jr struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(resp.Body).Decode(&jr) != nil || jr.Token == "" {
		return false
	}
	cloudAuth.Lock()
	cloudAuth.tokens[baseURL] = jr.Token
	cloudAuth.Unlock()
	return true
}

// cloudDo 带鉴权的中控请求：带 Bearer，401 → 重登一次再试一次
// 仍 401（授权码无效/已重新生成）返回明确错误，调用方直接透传给用户
func cloudDo(baseURL, method, path, contentType string, body []byte) (*http.Response, error) {
	attempt := func() (*http.Response, error) {
		req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		cloudAuth.Lock()
		tok := cloudAuth.tokens[baseURL]
		cloudAuth.Unlock()
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		return http.DefaultClient.Do(req)
	}
	resp, err := attempt()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if cloudLogin(baseURL) {
			resp, err = attempt()
		} else {
			return nil, fmt.Errorf("%w: 中控需要远程调试授权码：地址后缀加 ?k=授权码（后台「⚙功能→远程调试」生成，泄露重新生成后旧串作废）", errCloudAuth)
		}
	}
	return resp, err
}

// parseCloudAddr 拆地址串：http://中控#设备ID?k=授权码（k 可省略则用上次存下的）
// 兼容旧完整地址 http://host/api/screenshot?device_id=xxx&k=码
func parseCloudAddr(rawURL string) (baseURL, deviceID, code string, err error) {
	if idx := strings.Index(rawURL, "#"); idx >= 0 {
		baseURL = strings.TrimRight(rawURL[:idx], "/")
		rest := strings.TrimSpace(rawURL[idx+1:])
		if qi := strings.Index(rest, "?"); qi >= 0 {
			deviceID = strings.TrimSpace(rest[:qi])
			q, perr := url.ParseQuery(rest[qi+1:])
			if perr == nil {
				code = q.Get("k")
				if code == "" {
					code = q.Get("code")
				}
				if code == "" {
					code = q.Get("key")
				}
			}
		} else {
			deviceID = rest
		}
	} else {
		u, perr := url.Parse(rawURL)
		if perr != nil {
			return "", "", "", fmt.Errorf("解析地址失败: %v", perr)
		}
		deviceID = u.Query().Get("device_id")
		code = u.Query().Get("k")
		if code == "" {
			code = u.Query().Get("code")
		}
		baseURL = u.Scheme + "://" + u.Host
	}
	if deviceID == "" {
		return "", "", "", fmt.Errorf("地址中缺少设备ID（格式：http://中控地址#设备ID?k=授权码）")
	}
	return baseURL, deviceID, strings.TrimSpace(code), nil
}

// captureScreenViaWeb 通过 myweb3 服务器截图
// rawURL 支持两种格式（与 VSCode 插件统一）：
//   - 一次填完: http://中控地址#设备ID?k=远程调试授权码   （云控页「VSCode格式」按钮生成，★ 2026-10-06 起接口要鉴权）
//   - 兼容旧版: http://host/api/screenshot?device_id=xxx&k=码
//
// ★ v2 改法：先取 /api/screenshot/time 作基线（服务器时钟）→ 发命令 →
//
//	300ms 轮询时间戳直到 ts > 基线（新图已到中控）→ 才下载。
//	旧逻辑「只要返回的不是 JSON 就收」必拿上一张：/api/screenshot
//	在设备截过图之后永远 200+旧图，第一次轮询(1s)必然跑输新图回传(1~2.5s)。
func captureScreenViaWeb(rawURL string) (image.Image, error) {
	baseURL, deviceID, code, err := parseCloudAddr(rawURL)
	if err != nil {
		return nil, err
	}
	// 授权码：串里有就更新（重新生成后粘新串即可换码）；没有就沿用上次存下的
	if code != "" {
		cloudAuth.Lock()
		cloudAuth.codes[baseURL] = code
		cloudAuth.Unlock()
	}
	encoded := url.QueryEscape(deviceID)

	// 1. 取当前截图时间戳作基线（401=授权问题直接报错；其他非 200=老服务器，走旧兜底）
	hasTimeAPI := true
	var baseTs int64
	if tr, terr := cloudDo(baseURL, "GET", "/api/screenshot/time?device_id="+encoded, "", nil); terr == nil {
		if tr.StatusCode == http.StatusUnauthorized {
			tr.Body.Close()
			return nil, fmt.Errorf("授权码无效（可能已重新生成，请更新地址串里的 ?k=授权码）")
		}
		if tr.StatusCode == http.StatusOK {
			var jr struct {
				Ts int64 `json:"ts"`
			}
			if json.NewDecoder(tr.Body).Decode(&jr) == nil {
				baseTs = jr.Ts
			}
		} else {
			hasTimeAPI = false
		}
		tr.Body.Close()
	} else {
		// ★ 鉴权失败必须中止（错码不该静默降级到旧轮询跑 8 秒然后才报错）
		if errors.Is(terr, errCloudAuth) {
			return nil, terr
		}
		hasTimeAPI = false
	}

	// 2. POST 触发截图
	type cmdReq struct {
		DeviceID string `json:"device_id"`
		Command  string `json:"command"`
	}
	body, _ := json.Marshal(cmdReq{DeviceID: deviceID, Command: "screenshot"})
	resp, err := cloudDo(baseURL, "POST", "/api/command", "application/json", body)
	if err != nil {
		return nil, fmt.Errorf("发送截图命令失败: %v", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, fmt.Errorf("授权码无效（可能已重新生成，请更新地址串里的 ?k=授权码）")
	}
	resp.Body.Close()

	// 3. 等新图到达（最多 8 秒）
	if hasTimeAPI {
		// ★ 轮询时间戳：ts > 基线 = 新图已到 → 立即下载，永不差拍
		deadline := time.Now().Add(8 * time.Second)
		for {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("等待设备回传超时，截图未生成")
			}
			time.Sleep(300 * time.Millisecond)
			tr, terr := cloudDo(baseURL, "GET", "/api/screenshot/time?device_id="+encoded, "", nil)
			if terr != nil {
				continue
			}
			if tr.StatusCode != http.StatusOK {
				tr.Body.Close()
				continue
			}
			var jr struct {
				Ts int64 `json:"ts"`
			}
			derr := json.NewDecoder(tr.Body).Decode(&jr)
			tr.Body.Close()
			if derr != nil {
				continue
			}
			if jr.Ts > baseTs {
				break
			}
		}
	} else {
		// 老服务器无时间戳接口：退回旧轮询（尽力而为）
		arrived := false
		for i := 0; i < 8; i++ {
			time.Sleep(time.Second)
			resp, gerr := cloudDo(baseURL, "GET", "/api/screenshot?device_id="+encoded, "", nil)
			if gerr != nil {
				continue
			}
			data, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				continue
			}
			if bytes.HasPrefix(data, []byte("{")) {
				continue
			}
			arrived = true
			break
		}
		if !arrived {
			return nil, fmt.Errorf("等待设备回传超时，截图未生成")
		}
	}

	// 4. 下载并解码（加 _t 参数防缓存）
	resp, err = cloudDo(baseURL, "GET", "/api/screenshot?device_id="+encoded+"&_t="+fmt.Sprint(time.Now().UnixMilli()), "", nil)
	if err != nil {
		return nil, fmt.Errorf("下载截图失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("授权码无效（可能已重新生成，请更新地址串里的 ?k=授权码）")
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取截图数据失败: %v", err)
	}
	if bytes.HasPrefix(data, []byte("{")) {
		return nil, fmt.Errorf("截图未生成")
	}
	return png.Decode(bytes.NewReader(data))
}
