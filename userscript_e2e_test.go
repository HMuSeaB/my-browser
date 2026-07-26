package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 端到端验证需要真实拉起 Camoufox（约 15 秒）并访问外网，
// 因此默认跳过，通过环境变量显式开启：
//
//	$env:MYBROWSER_E2E=1; go test -run TestE2EUserScript -v .
func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("MYBROWSER_E2E") == "" {
		t.Skip("跳过端到端测试；设置 MYBROWSER_E2E=1 后运行")
	}
}

// bidiEvalResult 对应 script.evaluate 的返回结构
type bidiEvalResult struct {
	Type   string `json:"type"`
	Result struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	} `json:"result"`
}

// evalJS 在页面主世界求值并返回字符串结果
func evalJS(t *testing.T, conn *websocket.Conn, cmdID *int64, ctxID, expr string) string {
	t.Helper()

	*cmdID++
	raw, err := sendBiDiCommand(conn, *cmdID, "script.evaluate", map[string]interface{}{
		"expression":   expr,
		"target":       map[string]string{"context": ctxID},
		"awaitPromise": true,
	}, 20*time.Second)
	if err != nil {
		t.Fatalf("script.evaluate(%s) 失败: %v", expr, err)
	}

	var result bidiEvalResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("解析求值结果失败: %v", err)
	}

	var value string
	if err := json.Unmarshal(result.Result.Value, &value); err != nil {
		return string(result.Result.Value)
	}
	return value
}

// TestE2EUserScriptInjectionAndIsolation 验证两个核心属性：
//  1. isolated 模式的脚本确实被执行（能改动 DOM）
//  2. 脚本在沙箱内的变量对页面主世界不可见（这正是不可检测性的来源）
func TestE2EUserScriptInjectionAndIsolation(t *testing.T) {
	requireE2E(t)

	app := &App{dataDir: t.TempDir()}

	exePath, err := app.getCamoufoxPath()
	if err != nil {
		t.Skipf("未找到 Camoufox，跳过: %v", err)
	}

	// 走真实的对外接口保存脚本
	script, err := app.SaveUserScript("", `// ==UserScript==
// @name        E2E 注入验证
// @match       https://example.com/*
// @run-at      document_end
// ==/UserScript==
window.__sandboxOnly = 'leaked';
document.title = 'INJECTED_BY_ENGINE';`)
	if err != nil {
		t.Fatalf("保存脚本失败: %v", err)
	}
	if err := app.SetUserScriptEnabled(script.ID, true); err != nil {
		t.Fatalf("启用脚本失败: %v", err)
	}

	profile := BrowserProfile{
		ID:             "e2e-profile",
		Name:           "E2E 测试环境",
		UA:             "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0",
		Platform:       "Windows",
		Cookies:        "[]",
		EnabledScripts: []string{script.ID},
	}
	app.profiles = []BrowserProfile{profile}

	// 走真实启动准备链路（prefs + cookies + 脚本扩展）
	_, userDataDir, err := app.prepareProfileLaunch(profile)
	if err != nil {
		t.Fatalf("prepareProfileLaunch 失败: %v", err)
	}

	extDir := filepath.Join(userDataDir, "extensions", userScriptExtID)
	if _, err := os.Stat(filepath.Join(extDir, "manifest.json")); err != nil {
		t.Fatalf("脚本扩展未生成: %v", err)
	}

	port, err := reserveTCPPort()
	if err != nil {
		t.Fatalf("分配端口失败: %v", err)
	}

	args := append(app.buildBrowserArgs(userDataDir, "https://example.com", port), "--headless")
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

	// 从 stdout 捕获 BiDi 地址
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

	t.Logf("BiDi 连接地址: %s", connectURL)

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

	cmdID++
	if _, err := sendBiDiCommand(conn, cmdID, "browsingContext.navigate", map[string]interface{}{
		"context": ctxID,
		"url":     "https://example.com",
		"wait":    "complete",
	}, 60*time.Second); err != nil {
		t.Fatalf("导航失败: %v", err)
	}

	time.Sleep(2 * time.Second) // 等 document_end 脚本执行完毕

	// 属性一：脚本确实执行了
	if title := evalJS(t, conn, &cmdID, ctxID, "document.title"); title != "INJECTED_BY_ENGINE" {
		t.Errorf("脚本未生效，document.title = %q, want INJECTED_BY_ENGINE", title)
	}

	// 属性二：沙箱变量对页面不可见——这正是不可检测性的来源
	if got := evalJS(t, conn, &cmdID, ctxID, "typeof window.__sandboxOnly"); got != "undefined" {
		t.Errorf("沙箱隔离失效：页面可见 __sandboxOnly (typeof = %q)", got)
	}

	// 属性三：无法按扩展 ID 探测到引擎的存在
	probe := evalJS(t, conn, &cmdID, ctxID, `
		(async () => {
		  try {
		    await fetch('moz-extension://`+userScriptExtID+`/manifest.json');
		    return 'REACHABLE';
		  } catch (e) { return 'BLOCKED'; }
		})()`)
	if probe != "BLOCKED" {
		t.Errorf("扩展可被按 ID 探测到: %q", probe)
	}
}
