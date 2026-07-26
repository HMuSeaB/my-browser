package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestE2EExtensionLoadsWithInjectedGeckoID 走完整链路验证扩展装载：
// 安装 → 启用 → 生成到 profile（注入 gecko id）→ 真实启动 → 浏览器中可见。
//
// 缺少 gecko id 时 Firefox 会拒绝安装，因此这条链路的关键就是注入是否生效。
func TestE2EExtensionLoadsWithInjectedGeckoID(t *testing.T) {
	requireE2E(t)

	app := &App{dataDir: t.TempDir()}

	exePath, err := app.getCamoufoxPath()
	if err != nil {
		t.Skipf("未找到 Camoufox，跳过: %v", err)
	}

	// 造一个与用户实际扩展同形状的空壳：MV3、无 background、带 popup、无 gecko id
	src := filepath.Join(t.TempDir(), "shape-ext")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("准备扩展目录失败: %v", err)
	}
	manifest := `{
  "manifest_version": 3,
  "name": "E2E Popup Extension",
  "version": "1.0.8",
  "description": "端到端验证用空壳",
  "minimum_chrome_version": "102",
  "permissions": ["activeTab", "scripting", "storage"],
  "action": { "default_title": "E2E Popup Extension", "default_popup": "popup.html" }
}`
	if err := os.WriteFile(filepath.Join(src, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatalf("写入 manifest 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "popup.html"),
		[]byte("<!doctype html><meta charset=utf-8><div>popup ok</div>"), 0644); err != nil {
		t.Fatalf("写入 popup 失败: %v", err)
	}

	ext, err := app.InstallExtensionFromPath(src)
	if err != nil {
		t.Fatalf("安装扩展失败: %v", err)
	}
	if !ext.GeckoIDInjected {
		t.Fatal("该 manifest 无 gecko id，应标记为需要注入")
	}
	if err := app.SetExtensionEnabled(ext.ID, true); err != nil {
		t.Fatalf("启用扩展失败: %v", err)
	}

	profile := BrowserProfile{
		ID:                "e2e-ext-profile",
		Name:              "扩展测试环境",
		UA:                "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0",
		Platform:          "Windows",
		Cookies:           "[]",
		EnabledExtensions: []string{ext.ID},
	}
	app.profiles = []BrowserProfile{profile}

	_, userDataDir, err := app.prepareProfileLaunch(profile)
	if err != nil {
		t.Fatalf("prepareProfileLaunch 失败: %v", err)
	}

	target := filepath.Join(userDataDir, "extensions", ext.GeckoID)
	if _, err := os.Stat(filepath.Join(target, "manifest.json")); err != nil {
		t.Fatalf("扩展未铺设到 profile: %v", err)
	}

	port, err := reserveTCPPort()
	if err != nil {
		t.Fatalf("分配端口失败: %v", err)
	}

	args := append(app.buildBrowserArgs(userDataDir, "about:blank", port), "--headless")
	cmd := exec.Command(exePath, args...)
	cmd.Env = app.buildCamoufoxEnv(profile)
	cmd.Dir = filepath.Dir(exePath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("创建 stdout 管道失败: %v", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动浏览器失败: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	connectCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if url, ok := extractBidiConnectURL(scanner.Text()); ok {
				select {
				case connectCh <- url:
				default:
				}
			}
		}
	}()

	var connectURL string
	select {
	case connectURL = <-connectCh:
	case <-time.After(60 * time.Second):
		t.Fatal("等待 BiDi 地址超时")
	}

	conn, _, err := websocket.DefaultDialer.Dial(connectURL, nil)
	if err != nil {
		t.Fatalf("连接 BiDi 失败: %v", err)
	}
	defer conn.Close()

	var cmdID int64
	cmdID++
	if _, err := sendBiDiCommand(conn, cmdID, "session.new", map[string]interface{}{
		"capabilities": map[string]interface{}{"alwaysMatch": map[string]interface{}{}},
	}, 30*time.Second); err != nil {
		t.Logf("session.new 返回: %v（若会话已存在可忽略）", err)
	}

	cmdID++
	tree, err := sendBiDiCommand(conn, cmdID, "browsingContext.getTree", nil, 20*time.Second)
	if err != nil {
		t.Fatalf("获取浏览上下文失败: %v", err)
	}
	ctxID, err := extractRootContextID(tree)
	if err != nil {
		t.Fatalf("解析上下文失败: %v", err)
	}

	// about:support 会列出所有已安装扩展及其启用状态
	cmdID++
	if _, err := sendBiDiCommand(conn, cmdID, "browsingContext.navigate", map[string]interface{}{
		"context": ctxID,
		"url":     "about:support",
		"wait":    "complete",
	}, 60*time.Second); err != nil {
		t.Fatalf("导航到 about:support 失败: %v", err)
	}
	time.Sleep(2 * time.Second)

	cmdID++
	raw, err := sendBiDiCommand(conn, cmdID, "script.evaluate", map[string]interface{}{
		"expression":   "document.body.innerText",
		"target":       map[string]string{"context": ctxID},
		"awaitPromise": true,
	}, 20*time.Second)
	if err != nil {
		t.Fatalf("读取 about:support 失败: %v", err)
	}

	var result bidiEvalResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析求值结果失败: %v", err)
	}
	var text string
	if err := json.Unmarshal(result.Result.Value, &text); err != nil {
		t.Fatalf("求值结果不是字符串: %v", err)
	}

	if !strings.Contains(text, "E2E Popup Extension") {
		t.Error("扩展未出现在浏览器的已安装列表中")
	}
	if !strings.Contains(text, ext.GeckoID) {
		t.Errorf("注入的扩展 ID 未生效，期望出现 %q", ext.GeckoID)
	}
}
