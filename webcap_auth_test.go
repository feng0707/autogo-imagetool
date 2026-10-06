package main

// ★ 2026-10-06 云控授权码 E2E 测试：DEBUG_CODE 环境变量传真实授权码（不落盘、不打印）
// 跑法：DEBUG_CODE=xxx go test -run TestCloudAuthFlow -v
import (
	"os"
	"testing"
)

func TestCloudAuthFlow(t *testing.T) {
	code := os.Getenv("DEBUG_CODE")
	if code == "" {
		t.Skip("未设置 DEBUG_CODE，跳过")
	}
	// 用在线设备 cs1：完整走 parseCloudAddr → debuglogin → time 基线 → command → 轮询 → 下载
	addr := "http://101.43.86.185:10087#com.luatouch.app-1-test-cs1?k=" + code
	img, err := captureScreenViaWeb(addr)
	if err != nil {
		t.Fatalf("截图流程失败: %v", err)
	}
	b := img.Bounds()
	t.Logf("截图成功: %dx%d", b.Dx(), b.Dy())

	// 第二次截图：复用内存 token（不再登录）
	img2, err := captureScreenViaWeb("http://101.43.86.185:10087#com.luatouch.app-1-test-cs1")
	if err != nil {
		t.Fatalf("复用 token 二次截图失败: %v", err)
	}
	b2 := img2.Bounds()
	t.Logf("二次截图成功: %dx%d", b2.Dx(), b2.Dy())
}

func TestCloudAuthBadCode(t *testing.T) {
	// 清空内存 token/code：错码必须真的被拦（不能沾上一个测试登录成功的 token）
	cloudAuth.Lock()
	cloudAuth.tokens = map[string]string{}
	cloudAuth.codes = map[string]string{}
	cloudAuth.Unlock()
	// 错误授权码：应得到明确错误而不是静默失败
	addr := "http://101.43.86.185:10087#com.luatouch.app-1-test-cs1?k=00000000000000000000000000000000"
	_, err := captureScreenViaWeb(addr)
	if err == nil {
		t.Fatal("错误授权码竟然截图成功了？")
	}
	t.Logf("错码按预期报错: %v", err)
}
