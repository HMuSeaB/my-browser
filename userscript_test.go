package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUserScriptMeta(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantName    string
		wantMatches []string
		wantRunAt   string
		wantWorld   string
		wantGrants  []string
	}{
		{
			name: "完整元数据",
			source: `// ==UserScript==
// @name        测试脚本
// @description 演示用
// @match       https://example.com/*
// @match       https://*.github.com/*
// @run-at      document-start
// @grant       GM_setValue
// ==/UserScript==
console.log('hi');`,
			wantName:    "测试脚本",
			wantMatches: []string{"https://example.com/*", "https://*.github.com/*"},
			wantRunAt:   "document_start",
			wantWorld:   "isolated",
			wantGrants:  []string{"GM_setValue"},
		},
		{
			name:        "无元数据块时使用安全默认值",
			source:      `console.log('no meta');`,
			wantName:    "",
			wantMatches: nil,
			wantRunAt:   "document_end",
			wantWorld:   "isolated",
		},
		{
			name: "grant none 不应切换到 page 模式",
			source: `// ==UserScript==
// @name  G
// @match https://a.com/*
// @grant none
// ==/UserScript==`,
			wantName:    "G",
			wantMatches: []string{"https://a.com/*"},
			wantRunAt:   "document_end",
			wantWorld:   "isolated",
			wantGrants:  nil,
		},
		{
			name: "非法的 include 规则被丢弃",
			source: `// ==UserScript==
// @name  X
// @match https://ok.com/*
// @include *
// @include /^https?://regex\.com/
// ==/UserScript==`,
			wantName:    "X",
			wantMatches: []string{"https://ok.com/*"},
			wantRunAt:   "document_end",
			wantWorld:   "isolated",
		},
		{
			name: "非法的 run-at 回退到默认值",
			source: `// ==UserScript==
// @name  R
// @match https://a.com/*
// @run-at bogus-timing
// ==/UserScript==`,
			wantName:    "R",
			wantMatches: []string{"https://a.com/*"},
			wantRunAt:   "document_end",
			wantWorld:   "isolated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUserScriptMeta(tt.source)

			if got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.RunAt != tt.wantRunAt {
				t.Errorf("RunAt = %q, want %q", got.RunAt, tt.wantRunAt)
			}
			if got.World != tt.wantWorld {
				t.Errorf("World = %q, want %q", got.World, tt.wantWorld)
			}
			if len(got.Matches) != len(tt.wantMatches) {
				t.Fatalf("Matches = %v, want %v", got.Matches, tt.wantMatches)
			}
			for i := range tt.wantMatches {
				if got.Matches[i] != tt.wantMatches[i] {
					t.Errorf("Matches[%d] = %q, want %q", i, got.Matches[i], tt.wantMatches[i])
				}
			}
			if len(got.Grants) != len(tt.wantGrants) {
				t.Errorf("Grants = %v, want %v", got.Grants, tt.wantGrants)
			}
		})
	}
}

func TestIsValidMatchPattern(t *testing.T) {
	valid := []string{
		"https://example.com/*",
		"http://example.com/path",
		"*://*.example.com/*",
		"https://*.sub.example.com/*",
		"<all_urls>",
		"file:///C:/tmp/x.html",
		"wss://ws.example.com/*",
	}
	for _, p := range valid {
		if !isValidMatchPattern(p) {
			t.Errorf("isValidMatchPattern(%q) = false, want true", p)
		}
	}

	// 这些若混入 manifest 会导致 Gecko 拒绝加载整个扩展
	invalid := []string{
		"",
		"*",
		"example.com",
		"https://example.com",   // 缺少路径段
		`/^https?://regex\.com/`, // 正则写法
		"javascript:alert(1)",
	}
	for _, p := range invalid {
		if isValidMatchPattern(p) {
			t.Errorf("isValidMatchPattern(%q) = true, want false", p)
		}
	}
}

// newScriptTestApp 构造一个带脚本源文件的测试实例
func newScriptTestApp(t *testing.T, scripts []UserScript, sources map[string]string) *App {
	t.Helper()

	app := &App{
		dataDir:     t.TempDir(),
		userScripts: scripts,
	}

	if len(sources) > 0 {
		if err := os.MkdirAll(app.getUserScriptSourceDir(), 0755); err != nil {
			t.Fatalf("创建脚本目录失败: %v", err)
		}
		for id, src := range sources {
			if err := os.WriteFile(app.getUserScriptSourcePath(id), []byte(src), 0644); err != nil {
				t.Fatalf("写入脚本源文件失败: %v", err)
			}
		}
	}
	return app
}

func TestResolveEnabledScripts(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
		userScripts: []UserScript{
			{ID: "a", Name: "全局启用且环境启用", Enabled: true, Matches: []string{"https://a.com/*"}},
			{ID: "b", Name: "全局禁用", Enabled: false, Matches: []string{"https://b.com/*"}},
			{ID: "c", Name: "未被环境选中", Enabled: true, Matches: []string{"https://c.com/*"}},
			{ID: "d", Name: "无匹配规则", Enabled: true, Matches: nil},
		},
	}

	profile := BrowserProfile{ID: "p1", EnabledScripts: []string{"a", "b", "d", "不存在的ID"}}
	got := app.resolveEnabledScripts(profile)

	if len(got) != 1 {
		t.Fatalf("生效脚本数 = %d, want 1 (实际: %+v)", len(got), got)
	}
	if got[0].ID != "a" {
		t.Errorf("生效脚本 = %q, want \"a\"", got[0].ID)
	}
}

// 最关键的安全属性：未启用脚本的环境不得产生任何文件，
// 使其暴露面与未引入本功能时完全一致。
func TestSetupUserScriptsZeroExposureWhenNoScripts(t *testing.T) {
	app := newScriptTestApp(t, []UserScript{
		{ID: "a", Enabled: true, Matches: []string{"https://a.com/*"}},
	}, nil)

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", Name: "干净环境"} // EnabledScripts 为 nil

	if err := app.setupUserScripts(userDataDir, profile); err != nil {
		t.Fatalf("setupUserScripts 返回错误: %v", err)
	}

	if _, err := os.Stat(filepath.Join(userDataDir, "extensions")); !os.IsNotExist(err) {
		t.Fatalf("未启用脚本时不应生成 extensions 目录，err = %v", err)
	}
}

func TestSetupUserScriptsGeneratesValidManifest(t *testing.T) {
	app := newScriptTestApp(t,
		[]UserScript{
			{
				ID: "s1", Name: "脚本一", Enabled: true, World: "isolated",
				RunAt: "document_start", Matches: []string{"https://example.com/*"},
			},
		},
		map[string]string{"s1": "document.title = 'INJECTED';"},
	)

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", Name: "测试环境", EnabledScripts: []string{"s1"}}

	if err := app.setupUserScripts(userDataDir, profile); err != nil {
		t.Fatalf("setupUserScripts 返回错误: %v", err)
	}

	extDir := filepath.Join(userDataDir, "extensions", userScriptExtID)
	data, err := os.ReadFile(filepath.Join(extDir, "manifest.json"))
	if err != nil {
		t.Fatalf("读取 manifest 失败: %v", err)
	}

	var manifest struct {
		ManifestVersion int `json:"manifest_version"`
		ContentScripts  []struct {
			Matches []string `json:"matches"`
			JS      []string `json:"js"`
			RunAt   string   `json:"run_at"`
		} `json:"content_scripts"`
		WebAccessibleResources []string `json:"web_accessible_resources"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest 不是合法 JSON: %v", err)
	}

	if manifest.ManifestVersion != 2 {
		t.Errorf("manifest_version = %d, want 2", manifest.ManifestVersion)
	}
	if len(manifest.ContentScripts) != 1 {
		t.Fatalf("content_scripts 数量 = %d, want 1", len(manifest.ContentScripts))
	}
	cs := manifest.ContentScripts[0]
	if cs.RunAt != "document_start" {
		t.Errorf("run_at = %q, want document_start", cs.RunAt)
	}
	if len(cs.Matches) != 1 || cs.Matches[0] != "https://example.com/*" {
		t.Errorf("matches = %v", cs.Matches)
	}

	// 不得声明 web_accessible_resources：该字段是扩展被页面枚举的主要途径
	if len(manifest.WebAccessibleResources) != 0 {
		t.Errorf("不应声明 web_accessible_resources, 实际 = %v", manifest.WebAccessibleResources)
	}

	// isolated 模式应原样写出，不做主世界包装
	body, err := os.ReadFile(filepath.Join(extDir, cs.JS[0]))
	if err != nil {
		t.Fatalf("读取脚本文件失败: %v", err)
	}
	if string(body) != "document.title = 'INJECTED';" {
		t.Errorf("isolated 模式脚本内容被改写: %q", string(body))
	}
}

func TestSetupUserScriptsPageWorldIsWrapped(t *testing.T) {
	app := newScriptTestApp(t,
		[]UserScript{
			{
				ID: "s1", Name: "主世界脚本", Enabled: true, World: "page",
				RunAt: "document_end", Matches: []string{"https://example.com/*"},
			},
		},
		map[string]string{"s1": "window.foo = 'bar';"},
	)

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", EnabledScripts: []string{"s1"}}

	if err := app.setupUserScripts(userDataDir, profile); err != nil {
		t.Fatalf("setupUserScripts 返回错误: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(userDataDir, "extensions", userScriptExtID, "us_s1.js"))
	if err != nil {
		t.Fatalf("读取脚本文件失败: %v", err)
	}
	content := string(body)
	if !strings.Contains(content, "createElement('script')") {
		t.Errorf("page 模式脚本未被包装: %q", content)
	}
	if !strings.Contains(content, "s.remove()") {
		t.Error("page 模式包装缺少 script 标签清理，会在 DOM 中留下痕迹")
	}
}

// 禁用全部脚本后必须彻底移除扩展目录，不得留下残迹
func TestSetupUserScriptsRemovesStaleExtension(t *testing.T) {
	app := newScriptTestApp(t,
		[]UserScript{
			{ID: "s1", Enabled: true, World: "isolated", RunAt: "document_end",
				Matches: []string{"https://example.com/*"}},
		},
		map[string]string{"s1": "void 0;"},
	)

	userDataDir := t.TempDir()
	enabled := BrowserProfile{ID: "p1", EnabledScripts: []string{"s1"}}

	if err := app.setupUserScripts(userDataDir, enabled); err != nil {
		t.Fatalf("首次生成失败: %v", err)
	}
	extDir := filepath.Join(userDataDir, "extensions", userScriptExtID)
	if _, err := os.Stat(extDir); err != nil {
		t.Fatalf("首次生成后扩展目录应存在: %v", err)
	}

	// 取消启用后重新准备启动
	disabled := BrowserProfile{ID: "p1", EnabledScripts: nil}
	if err := app.setupUserScripts(userDataDir, disabled); err != nil {
		t.Fatalf("二次调用失败: %v", err)
	}
	if _, err := os.Stat(extDir); !os.IsNotExist(err) {
		t.Errorf("禁用脚本后扩展目录应被移除, err = %v", err)
	}
}

// 索引可能被手工编辑过，非法匹配规则必须在生成阶段被拦截，
// 否则整个扩展会被 Gecko 拒绝加载。
func TestSetupUserScriptsDropsInvalidMatches(t *testing.T) {
	app := newScriptTestApp(t,
		[]UserScript{
			{ID: "s1", Name: "脏数据脚本", Enabled: true, World: "isolated", RunAt: "document_end",
				Matches: []string{"https://good.com/*", "!!!非法规则!!!"}},
		},
		map[string]string{"s1": "void 0;"},
	)

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", EnabledScripts: []string{"s1"}}

	if err := app.setupUserScripts(userDataDir, profile); err != nil {
		t.Fatalf("setupUserScripts 返回错误: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(userDataDir, "extensions", userScriptExtID, "manifest.json"))
	if err != nil {
		t.Fatalf("读取 manifest 失败: %v", err)
	}
	if strings.Contains(string(data), "非法规则") {
		t.Error("非法匹配规则被写入 manifest，会导致整个扩展加载失败")
	}
}

// 脚本全部不可用时应回退到零暴露面，而不是留下空壳扩展
func TestSetupUserScriptsNoOrphanExtensionWhenSourceMissing(t *testing.T) {
	app := newScriptTestApp(t,
		[]UserScript{
			{ID: "missing", Name: "源文件缺失", Enabled: true, World: "isolated",
				RunAt: "document_end", Matches: []string{"https://a.com/*"}},
		},
		nil, // 刻意不写源文件
	)

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", EnabledScripts: []string{"missing"}}

	if err := app.setupUserScripts(userDataDir, profile); err != nil {
		t.Fatalf("setupUserScripts 返回错误: %v", err)
	}

	extDir := filepath.Join(userDataDir, "extensions", userScriptExtID)
	if _, err := os.Stat(extDir); !os.IsNotExist(err) {
		t.Errorf("源文件缺失时不应留下空壳扩展, err = %v", err)
	}
}

func TestWrapForPageWorldEscapesSource(t *testing.T) {
	// 含引号、换行与反斜杠的源码必须被安全转义，否则包装层语法会被破坏
	wrapped := wrapForPageWorld("var s = \"it's\\n\";\nconsole.log(s);")

	if !strings.Contains(wrapped, "createElement('script')") {
		t.Fatalf("包装结果异常: %q", wrapped)
	}
	// 原始换行不得直接出现在 JS 字符串字面量内部
	payloadStart := strings.Index(wrapped, "s.textContent=")
	payloadEnd := strings.Index(wrapped, "\n(document.head")
	if payloadStart < 0 || payloadEnd < 0 {
		t.Fatalf("包装结构不符合预期: %q", wrapped)
	}
	literal := wrapped[payloadStart:payloadEnd]
	if strings.Count(literal, "\n") != 0 {
		t.Errorf("字符串字面量内出现未转义换行: %q", literal)
	}
}

func TestSaveUserScriptDefaultsToDisabled(t *testing.T) {
	app := &App{dataDir: t.TempDir()}

	script, err := app.SaveUserScript("", `// ==UserScript==
// @name  新脚本
// @match https://example.com/*
// ==/UserScript==
void 0;`)
	if err != nil {
		t.Fatalf("SaveUserScript 返回错误: %v", err)
	}

	// 新建脚本默认不启用，避免一保存就在所有环境生效
	if script.Enabled {
		t.Error("新建脚本不应默认启用")
	}
	if script.Name != "新脚本" {
		t.Errorf("Name = %q, want 新脚本", script.Name)
	}
	if script.World != "isolated" {
		t.Errorf("World = %q, want isolated（默认应为不可检测模式）", script.World)
	}
	if _, err := os.Stat(app.getUserScriptSourcePath(script.ID)); err != nil {
		t.Errorf("脚本源文件未落盘: %v", err)
	}
}

func TestSaveUserScriptPreservesWorldChoice(t *testing.T) {
	app := &App{dataDir: t.TempDir()}

	script, err := app.SaveUserScript("", "// ==UserScript==\n// @name A\n// @match https://a.com/*\n// ==/UserScript==\nvoid 0;")
	if err != nil {
		t.Fatalf("SaveUserScript 返回错误: %v", err)
	}
	if err := app.SetUserScriptWorld(script.ID, "page"); err != nil {
		t.Fatalf("SetUserScriptWorld 返回错误: %v", err)
	}

	// 重新保存正文不应把用户选择的运行模式重置回默认值
	updated, err := app.SaveUserScript(script.ID, "// ==UserScript==\n// @name A2\n// @match https://a.com/*\n// ==/UserScript==\nvoid 1;")
	if err != nil {
		t.Fatalf("二次保存失败: %v", err)
	}
	if updated.World != "page" {
		t.Errorf("World = %q, want page（用户选择被覆盖）", updated.World)
	}
	if updated.Name != "A2" {
		t.Errorf("Name = %q, want A2", updated.Name)
	}
}

func TestDeleteUserScriptCleansProfileReferences(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
		profiles: []BrowserProfile{
			{ID: "p1", Name: "环境一", EnabledScripts: []string{"s1", "s2"}},
			{ID: "p2", Name: "环境二", EnabledScripts: []string{"s2"}},
		},
		userScripts: []UserScript{{ID: "s1"}, {ID: "s2"}},
	}

	if err := app.DeleteUserScript("s1"); err != nil {
		t.Fatalf("DeleteUserScript 返回错误: %v", err)
	}

	if len(app.userScripts) != 1 || app.userScripts[0].ID != "s2" {
		t.Errorf("脚本索引未正确移除: %+v", app.userScripts)
	}
	for _, id := range app.profiles[0].EnabledScripts {
		if id == "s1" {
			t.Error("环境一仍残留已删除脚本的悬空引用")
		}
	}
	if len(app.profiles[1].EnabledScripts) != 1 {
		t.Errorf("环境二的引用被误删: %+v", app.profiles[1].EnabledScripts)
	}
}

// --- 环境包导入导出 ---

// exportFixture 构造一个带脚本、且 profile 目录内存在自动生成 extensions/ 的导出源
func exportFixture(t *testing.T) (*App, BrowserProfile, string) {
	t.Helper()

	app := &App{dataDir: t.TempDir()}

	script, err := app.SaveUserScript("", `// ==UserScript==
// @name  随包脚本
// @match https://example.com/*
// ==/UserScript==
document.title = 'X';`)
	if err != nil {
		t.Fatalf("保存脚本失败: %v", err)
	}
	if err := app.SetUserScriptEnabled(script.ID, true); err != nil {
		t.Fatalf("启用脚本失败: %v", err)
	}

	profile := BrowserProfile{
		ID: "profile-export", Name: "导出源", Cookies: "[]", Platform: "Windows",
		EnabledScripts: []string{script.ID},
	}
	app.profiles = []BrowserProfile{profile}

	userDataDir := filepath.Join(app.dataDir, "profiles", profile.ID)
	if err := os.MkdirAll(filepath.Join(userDataDir, "extensions", userScriptExtID), 0755); err != nil {
		t.Fatalf("创建 profile 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "prefs.js"), []byte("user_pref();"), 0644); err != nil {
		t.Fatalf("写入 prefs.js 失败: %v", err)
	}
	// 模拟上次启动自动生成的扩展文件
	if err := os.WriteFile(
		filepath.Join(userDataDir, "extensions", userScriptExtID, "manifest.json"),
		[]byte(`{"stale":true}`), 0644); err != nil {
		t.Fatalf("写入陈旧扩展失败: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), "profile.mbp")
	if err := app.exportProfileBundle(profile, bundlePath); err != nil {
		t.Fatalf("exportProfileBundle 返回错误: %v", err)
	}
	return app, profile, bundlePath
}

func bundleEntryNames(t *testing.T, bundlePath string) []string {
	t.Helper()

	reader, err := zip.OpenReader(bundlePath)
	if err != nil {
		t.Fatalf("打开环境包失败: %v", err)
	}
	defer reader.Close()

	names := make([]string, 0, len(reader.File))
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	return names
}

func TestExportProfileBundleIncludesUserScripts(t *testing.T) {
	app, profile, bundlePath := exportFixture(t)
	names := bundleEntryNames(t, bundlePath)

	if !containsString(names, "scripts/index.json") {
		t.Fatalf("环境包缺少脚本索引: %v", names)
	}
	scriptID := profile.EnabledScripts[0]
	if !containsString(names, "scripts/"+scriptID+".user.js") {
		t.Fatalf("环境包缺少脚本正文: %v", names)
	}
	_ = app
}

// extensions/ 每次启动都会重建，随包导出只会带来体积膨胀与陈旧内容
func TestExportProfileBundleSkipsExtensionsDirectory(t *testing.T) {
	_, _, bundlePath := exportFixture(t)

	for _, name := range bundleEntryNames(t, bundlePath) {
		if strings.Contains(name, "extensions/") {
			t.Errorf("自动生成的扩展目录不应随包导出: %s", name)
		}
	}
}

func TestImportProfileBundleRestoresScriptsAndRemapsIDs(t *testing.T) {
	_, srcProfile, bundlePath := exportFixture(t)

	// 另一台机器：全新的数据目录
	target := &App{dataDir: t.TempDir()}
	imported, err := target.importProfileBundle(bundlePath)
	if err != nil {
		t.Fatalf("importProfileBundle 返回错误: %v", err)
	}

	if len(target.userScripts) != 1 {
		t.Fatalf("应还原 1 个脚本，实际 %d", len(target.userScripts))
	}
	restored := target.userScripts[0]

	if restored.Name != "随包脚本" {
		t.Errorf("脚本名 = %q, want 随包脚本", restored.Name)
	}
	// 安全默认：导入的脚本需用户确认后再启用
	if restored.Enabled {
		t.Error("导入的脚本不应自动启用")
	}
	if restored.ID == srcProfile.EnabledScripts[0] {
		t.Error("脚本 ID 应重新分配，避免与源机器冲突")
	}

	if len(imported.EnabledScripts) != 1 || imported.EnabledScripts[0] != restored.ID {
		t.Fatalf("启用清单未正确重映射: %v (期望 [%s])", imported.EnabledScripts, restored.ID)
	}

	source, err := os.ReadFile(target.getUserScriptSourcePath(restored.ID))
	if err != nil {
		t.Fatalf("脚本正文未落盘: %v", err)
	}
	if !strings.Contains(string(source), "document.title = 'X'") {
		t.Errorf("脚本正文内容不符: %q", string(source))
	}
}

func TestImportProfileBundleDeduplicatesIdenticalScripts(t *testing.T) {
	_, _, bundlePath := exportFixture(t)

	target := &App{dataDir: t.TempDir()}
	first, err := target.importProfileBundle(bundlePath)
	if err != nil {
		t.Fatalf("首次导入失败: %v", err)
	}
	second, err := target.importProfileBundle(bundlePath)
	if err != nil {
		t.Fatalf("二次导入失败: %v", err)
	}

	if len(target.userScripts) != 1 {
		t.Fatalf("正文相同的脚本不应重复导入，实际 %d 个", len(target.userScripts))
	}
	// 两个环境应共同指向同一个脚本
	if len(second.EnabledScripts) != 1 || second.EnabledScripts[0] != first.EnabledScripts[0] {
		t.Errorf("去重后启用清单未指向同一脚本: %v vs %v",
			first.EnabledScripts, second.EnabledScripts)
	}
}

// 旧版环境包没有 scripts/ 段，导入不得报错
func TestImportProfileBundleWithoutScriptsSection(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "legacy.mbp")
	file, err := os.Create(bundlePath)
	if err != nil {
		t.Fatalf("创建环境包失败: %v", err)
	}
	writer := zip.NewWriter(file)
	meta, _ := json.Marshal(BrowserProfile{
		ID: "legacy", Name: "旧版环境", Cookies: "[]", Platform: "Windows",
	})
	entry, err := writer.Create("metadata.json")
	if err != nil {
		t.Fatalf("写入元数据失败: %v", err)
	}
	if _, err := entry.Write(meta); err != nil {
		t.Fatalf("写入元数据失败: %v", err)
	}
	writer.Close()
	file.Close()

	target := &App{dataDir: t.TempDir()}
	imported, err := target.importProfileBundle(bundlePath)
	if err != nil {
		t.Fatalf("旧版环境包导入失败: %v", err)
	}
	if len(imported.EnabledScripts) != 0 {
		t.Errorf("旧版环境包不应带出启用清单: %v", imported.EnabledScripts)
	}
	if len(target.userScripts) != 0 {
		t.Errorf("旧版环境包不应产生脚本: %d", len(target.userScripts))
	}
}

func TestSetProfileScriptsRejectsUnknownIDs(t *testing.T) {
	app := &App{
		dataDir:     t.TempDir(),
		profiles:    []BrowserProfile{{ID: "p1", Name: "环境一"}},
		userScripts: []UserScript{{ID: "s1"}},
	}

	if err := app.SetProfileScripts("p1", []string{"s1", "不存在"}); err != nil {
		t.Fatalf("SetProfileScripts 返回错误: %v", err)
	}

	got := app.profiles[0].EnabledScripts
	if len(got) != 1 || got[0] != "s1" {
		t.Errorf("EnabledScripts = %v, want [s1]", got)
	}
}
