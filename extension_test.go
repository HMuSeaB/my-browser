package main

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用户提供的真实 manifest 形状：MV3、无 background、带 popup、无 gecko id
const sepaManifest = `{
  "manifest_version": 3,
  "name": "third-auth SEPA Helper",
  "version": "1.0.8",
  "description": "在中转站页面中创建表单，并以只读方式检查异步付款状态。",
  "minimum_chrome_version": "102",
  "permissions": ["activeTab", "scripting", "storage"],
  "action": {
    "default_title": "third-auth SEPA Helper",
    "default_popup": "popup.html"
  }
}`

// writeExtensionDir 造一个最小扩展目录
func writeExtensionDir(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatalf("写入 manifest 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "popup.html"), []byte("<div>popup</div>"), 0644); err != nil {
		t.Fatalf("写入 popup 失败: %v", err)
	}
}

func TestInstallExtensionFromDirectory(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "sepa-ext")
	writeExtensionDir(t, src, sepaManifest)

	ext, err := app.InstallExtensionFromPath(src)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}

	if ext.Name != "third-auth SEPA Helper" {
		t.Errorf("Name = %q", ext.Name)
	}
	if ext.Version != "1.0.8" {
		t.Errorf("Version = %q", ext.Version)
	}
	if ext.ManifestVer != 3 {
		t.Errorf("ManifestVer = %d, want 3", ext.ManifestVer)
	}
	if !ext.HasPopup {
		t.Error("应识别出 action.default_popup")
	}
	// 新装扩展默认不启用，与环境包导入的安全默认一致
	if ext.Enabled {
		t.Error("新安装的扩展不应默认启用")
	}
	// 原 manifest 无 gecko id，应自动生成并标记
	if !ext.GeckoIDInjected {
		t.Error("缺少 gecko id 时应标记为需要注入")
	}
	if !strings.Contains(ext.GeckoID, "@") {
		t.Errorf("生成的 GeckoID 格式不合法: %q", ext.GeckoID)
	}
	// 该 manifest 没有 Firefox 不兼容项
	if len(ext.Incompatible) != 0 {
		t.Errorf("不应报告不兼容项: %v", ext.Incompatible)
	}
}

// 核心约束：原始文件夹必须原样保留，注入只发生在 profile 副本上
func TestInstallExtensionLeavesSourceUntouched(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "sepa-ext")
	writeExtensionDir(t, src, sepaManifest)

	before, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatalf("读取原始 manifest 失败: %v", err)
	}

	ext, err := app.InstallExtensionFromPath(src)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatalf("读取原始 manifest 失败: %v", err)
	}
	if string(before) != string(after) {
		t.Error("原始文件夹的 manifest 被修改了，应保持只读")
	}

	// 内部副本同样保持原样（未注入）
	stored, err := os.ReadFile(filepath.Join(app.getExtensionSourceDir(ext.ID), "manifest.json"))
	if err != nil {
		t.Fatalf("读取内部副本失败: %v", err)
	}
	if strings.Contains(string(stored), "browser_specific_settings") {
		t.Error("内部副本不应被注入 gecko id，注入只应发生在 profile 副本")
	}
}

func TestSetupExtensionsInjectsGeckoIDIntoProfileCopyOnly(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "sepa-ext")
	writeExtensionDir(t, src, sepaManifest)

	ext, err := app.InstallExtensionFromPath(src)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	if err := app.SetExtensionEnabled(ext.ID, true); err != nil {
		t.Fatalf("启用失败: %v", err)
	}

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", Name: "测试", EnabledExtensions: []string{ext.ID}}
	if err := app.setupExtensions(userDataDir, profile); err != nil {
		t.Fatalf("setupExtensions 失败: %v", err)
	}

	target := filepath.Join(userDataDir, "extensions", ext.GeckoID)
	data, err := os.ReadFile(filepath.Join(target, "manifest.json"))
	if err != nil {
		t.Fatalf("profile 副本缺失: %v", err)
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("profile 副本 manifest 解析失败: %v", err)
	}
	if got := existingGeckoID(manifest); got != ext.GeckoID {
		t.Errorf("profile 副本未注入正确的 gecko id: %q", got)
	}
	// 原有字段不得因重写而丢失
	if manifest["minimum_chrome_version"] != "102" {
		t.Error("重写 manifest 时丢失了原有字段")
	}
	action, ok := manifest["action"].(map[string]interface{})
	if !ok || action["default_popup"] != "popup.html" {
		t.Fatal("重写 manifest 时丢失了 action 段")
	}
	// 有弹窗的扩展默认固定到地址栏旁，实测由 default_area 控制
	if action["default_area"] != "navbar" {
		t.Errorf("默认应固定到 navbar，实际 = %v", action["default_area"])
	}
	// 其它文件应一并铺好
	if _, err := os.Stat(filepath.Join(target, "popup.html")); err != nil {
		t.Errorf("popup.html 未铺设: %v", err)
	}
}

// 取消固定后应落回拼图面板，且该改动要能触发 profile 副本重铺
func TestSetExtensionPinnedRewritesProfileCopy(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "sepa-ext")
	writeExtensionDir(t, src, sepaManifest)

	ext, err := app.InstallExtensionFromPath(src)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	if !ext.Pinned {
		t.Fatal("有弹窗的扩展应默认固定")
	}
	if err := app.SetExtensionEnabled(ext.ID, true); err != nil {
		t.Fatalf("启用失败: %v", err)
	}

	userDataDir := t.TempDir()
	profile := BrowserProfile{ID: "p1", EnabledExtensions: []string{ext.ID}}
	if err := app.setupExtensions(userDataDir, profile); err != nil {
		t.Fatalf("首次铺设失败: %v", err)
	}

	readArea := func() interface{} {
		data, err := os.ReadFile(filepath.Join(userDataDir, "extensions", ext.GeckoID, "manifest.json"))
		if err != nil {
			t.Fatalf("读取 profile 副本失败: %v", err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		action, _ := m["action"].(map[string]interface{})
		return action["default_area"]
	}

	if got := readArea(); got != "navbar" {
		t.Errorf("固定时应为 navbar，实际 %v", got)
	}

	// 取消固定并重铺：指纹含 pinned，应触发重写而非跳过
	if err := app.SetExtensionPinned(ext.ID, false); err != nil {
		t.Fatalf("取消固定失败: %v", err)
	}
	if err := app.setupExtensions(userDataDir, profile); err != nil {
		t.Fatalf("二次铺设失败: %v", err)
	}
	if got := readArea(); got != "menupanel" {
		t.Errorf("取消固定后应为 menupanel，实际 %v", got)
	}
}

// 无弹窗的扩展没有可点的图标，不应允许固定
func TestSetExtensionPinnedRejectsPopuplessExtension(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "no-popup")
	writeExtensionDir(t, src, `{
	  "manifest_version": 3,
	  "name": "No Popup",
	  "version": "1.0",
	  "permissions": ["storage"]
	}`)

	ext, err := app.InstallExtensionFromPath(src)
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	if ext.HasPopup || ext.Pinned {
		t.Fatal("无 action 的扩展不应识别为有弹窗或被固定")
	}
	if err := app.SetExtensionPinned(ext.ID, true); err == nil {
		t.Error("无弹窗的扩展应拒绝固定")
	}
}

// 未启用扩展的环境不得留下任何扩展目录
func TestSetupExtensionsRemovesDisabledOnes(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	src := filepath.Join(t.TempDir(), "sepa-ext")
	writeExtensionDir(t, src, sepaManifest)

	ext, _ := app.InstallExtensionFromPath(src)
	app.SetExtensionEnabled(ext.ID, true)

	userDataDir := t.TempDir()
	enabled := BrowserProfile{ID: "p1", EnabledExtensions: []string{ext.ID}}
	if err := app.setupExtensions(userDataDir, enabled); err != nil {
		t.Fatalf("首次铺设失败: %v", err)
	}
	target := filepath.Join(userDataDir, "extensions", ext.GeckoID)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("首次铺设后目录应存在: %v", err)
	}

	disabled := BrowserProfile{ID: "p1", EnabledExtensions: nil}
	if err := app.setupExtensions(userDataDir, disabled); err != nil {
		t.Fatalf("二次调用失败: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("取消启用后应移除扩展目录, err = %v", err)
	}
}

// 用户脚本引擎与扩展共用 extensions 目录，清理时不得互相误删
func TestSetupExtensionsPreservesUserScriptEngine(t *testing.T) {
	app := &App{dataDir: t.TempDir()}

	userDataDir := t.TempDir()
	engineDir := filepath.Join(userDataDir, "extensions", userScriptExtID)
	if err := os.MkdirAll(engineDir, 0755); err != nil {
		t.Fatalf("准备用户脚本引擎目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, "manifest.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	profile := BrowserProfile{ID: "p1"} // 未启用任何扩展
	if err := app.setupExtensions(userDataDir, profile); err != nil {
		t.Fatalf("setupExtensions 失败: %v", err)
	}

	if _, err := os.Stat(filepath.Join(engineDir, "manifest.json")); err != nil {
		t.Errorf("用户脚本引擎目录被误删: %v", err)
	}
}

func TestDetectFirefoxIncompatibilities(t *testing.T) {
	parse := func(s string) map[string]interface{} {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		return m
	}

	// Chrome MV3 的 service worker 后台在 Firefox 下不工作
	issues := detectFirefoxIncompatibilities(parse(`{
		"manifest_version": 3,
		"background": {"service_worker": "bg.js", "type": "module"}
	}`))
	if len(issues) != 1 || !strings.Contains(issues[0], "service_worker") {
		t.Errorf("应检出 service_worker 后台: %v", issues)
	}

	// 同时提供 scripts 的则可用，不应报警
	issues = detectFirefoxIncompatibilities(parse(`{
		"background": {"service_worker": "bg.js", "scripts": ["bg.js"]}
	}`))
	if len(issues) != 0 {
		t.Errorf("同时声明 scripts 时不应报警: %v", issues)
	}

	// Chrome 专有权限
	issues = detectFirefoxIncompatibilities(parse(`{
		"permissions": ["storage", "offscreen", "sidePanel"]
	}`))
	if len(issues) != 2 {
		t.Errorf("应检出 2 个 Chrome 专有权限: %v", issues)
	}

	// Firefox 不识别的顶层键
	issues = detectFirefoxIncompatibilities(parse(`{"externally_connectable": {}}`))
	if len(issues) != 1 {
		t.Errorf("应检出 externally_connectable: %v", issues)
	}

	// 用户提供的真实 manifest 应无不兼容项
	if issues := detectFirefoxIncompatibilities(parse(sepaManifest)); len(issues) != 0 {
		t.Errorf("该 manifest 不应有不兼容项: %v", issues)
	}
}

func TestExistingGeckoIDSupportsBothKeys(t *testing.T) {
	parse := func(s string) map[string]interface{} {
		var m map[string]interface{}
		json.Unmarshal([]byte(s), &m)
		return m
	}

	if got := existingGeckoID(parse(`{"browser_specific_settings":{"gecko":{"id":"a@b.c"}}}`)); got != "a@b.c" {
		t.Errorf("新写法解析失败: %q", got)
	}
	// 旧写法 applications 同样要认
	if got := existingGeckoID(parse(`{"applications":{"gecko":{"id":"x@y.z"}}}`)); got != "x@y.z" {
		t.Errorf("旧写法解析失败: %q", got)
	}
	if got := existingGeckoID(parse(`{"name":"no id"}`)); got != "" {
		t.Errorf("无 id 时应返回空: %q", got)
	}
}

// --- 压缩包安装 ---

func buildZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建 zip 失败: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatalf("写入 zip 条目失败: %v", err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("写入 zip 内容失败: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
}

func TestInstallExtensionFromZip(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	zipPath := filepath.Join(t.TempDir(), "ext.zip")
	buildZip(t, zipPath, map[string]string{
		"manifest.json": sepaManifest,
		"popup.html":    "<div>popup</div>",
	})

	ext, err := app.InstallExtensionFromPath(zipPath)
	if err != nil {
		t.Fatalf("从 zip 安装失败: %v", err)
	}
	if ext.Name != "third-auth SEPA Helper" {
		t.Errorf("Name = %q", ext.Name)
	}
	if _, err := os.Stat(filepath.Join(app.getExtensionSourceDir(ext.ID), "popup.html")); err != nil {
		t.Errorf("zip 内文件未解出: %v", err)
	}
}

// 压缩包多套一层目录时应自动向下定位到 manifest 所在层
func TestInstallExtensionFromNestedZip(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	zipPath := filepath.Join(t.TempDir(), "nested.zip")
	buildZip(t, zipPath, map[string]string{
		"my-ext/manifest.json": sepaManifest,
		"my-ext/popup.html":    "<div>popup</div>",
	})

	ext, err := app.InstallExtensionFromPath(zipPath)
	if err != nil {
		t.Fatalf("嵌套 zip 安装失败: %v", err)
	}
	root := app.getExtensionSourceDir(ext.ID)
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err != nil {
		t.Errorf("manifest 未被提升到根层: %v", err)
	}
}

func TestCrxZipOffset(t *testing.T) {
	// 普通 zip
	if off, err := crxZipOffset([]byte("PK\x03\x04somedata....")); err != nil || off != 0 {
		t.Errorf("普通 zip 偏移应为 0，得到 %d, %v", off, err)
	}

	// CRX3：Cr24 + 版本3 + 头长度
	crx3 := make([]byte, 12+8)
	copy(crx3[0:4], "Cr24")
	binary.LittleEndian.PutUint32(crx3[4:8], 3)
	binary.LittleEndian.PutUint32(crx3[8:12], 8)
	if off, err := crxZipOffset(crx3); err != nil || off != 20 {
		t.Errorf("CRX3 偏移应为 20，得到 %d, %v", off, err)
	}

	// CRX2：Cr24 + 版本2 + 公钥长 + 签名长
	crx2 := make([]byte, 16+4+6)
	copy(crx2[0:4], "Cr24")
	binary.LittleEndian.PutUint32(crx2[4:8], 2)
	binary.LittleEndian.PutUint32(crx2[8:12], 4)
	binary.LittleEndian.PutUint32(crx2[12:16], 6)
	if off, err := crxZipOffset(crx2); err != nil || off != 26 {
		t.Errorf("CRX2 偏移应为 26，得到 %d, %v", off, err)
	}

	// 头部长度越界应报错而不是越界读取
	bad := make([]byte, 16)
	copy(bad[0:4], "Cr24")
	binary.LittleEndian.PutUint32(bad[4:8], 3)
	binary.LittleEndian.PutUint32(bad[8:12], 0xFFFFFF)
	if _, err := crxZipOffset(bad); err == nil {
		t.Error("头部长度越界应报错")
	}
}

// 压缩包内的越界路径必须被拒绝（zip slip）
func TestSafeJoinExtractPathRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	for _, evil := range []string{"../evil.js", "..\\evil.js", "a/../../evil.js", "/abs/evil.js"} {
		if _, err := safeJoinExtractPath(base, evil); err == nil {
			t.Errorf("越界路径应被拒绝: %q", evil)
		}
	}
	if got, err := safeJoinExtractPath(base, "sub/ok.js"); err != nil || got == "" {
		t.Errorf("正常路径不应被拒绝: %v", err)
	}
}

func TestDeleteExtensionCleansProfileReferences(t *testing.T) {
	app := &App{
		dataDir: t.TempDir(),
		profiles: []BrowserProfile{
			{ID: "p1", Name: "环境一", EnabledExtensions: []string{"e1", "e2"}},
			{ID: "p2", Name: "环境二", EnabledExtensions: []string{"e2"}},
		},
		extensions: []BrowserExtension{{ID: "e1"}, {ID: "e2"}},
	}

	if err := app.DeleteExtension("e1"); err != nil {
		t.Fatalf("卸载失败: %v", err)
	}
	if len(app.extensions) != 1 || app.extensions[0].ID != "e2" {
		t.Errorf("索引未正确移除: %+v", app.extensions)
	}
	for _, id := range app.profiles[0].EnabledExtensions {
		if id == "e1" {
			t.Error("环境一仍残留已卸载扩展的悬空引用")
		}
	}
	if len(app.profiles[1].EnabledExtensions) != 1 {
		t.Errorf("环境二的引用被误删: %+v", app.profiles[1].EnabledExtensions)
	}
}

func TestInstallExtensionRejectsNonExtension(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	dir := filepath.Join(t.TempDir(), "not-an-ext")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if _, err := app.InstallExtensionFromPath(dir); err == nil {
		t.Fatal("无 manifest.json 的目录应被拒绝")
	}
	// 失败后不得留下垃圾目录
	entries, _ := os.ReadDir(app.getExtensionStoreDir())
	if len(entries) != 0 {
		t.Errorf("安装失败后应清理临时目录，残留 %d 项", len(entries))
	}
}
