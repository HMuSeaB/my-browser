package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	mrand "math/rand"



	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	_ "modernc.org/sqlite"
)

// BrowserProfile 代表一个指纹环境
type BrowserProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Proxy    string `json:"proxy"`     // 格式: type://user:pass@host:port
	StartURL string `json:"start_url"` // 默认启动页
	UA       string `json:"ua"`        // User-Agent
	Platform string `json:"platform"`  // Windows/macOS/Linux
	Cookies  string `json:"cookies"`   // JSON 格式的 Cookie 字符串
	CreateAt int64  `json:"create_at"`
	// EnabledScripts 为本环境启用的用户脚本 ID 列表。
	// nil 表示未启用任何脚本，旧版 profiles.json 反序列化后即为该值，天然向后兼容。
	EnabledScripts []string `json:"enabled_scripts"`
}

// UserScript 代表一个用户脚本（油猴脚本）
type UserScript struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Matches     []string `json:"matches"` // 解析自 @match / @include
	RunAt       string   `json:"run_at"`  // document_start | document_end | document_idle
	World       string   `json:"world"`   // isolated（默认，页面不可检测）| page（会留下可检测痕迹）
	Grants      []string `json:"grants"`  // 解析自 @grant
	// Requires / Resources 为脚本声明的外部依赖原文
	Requires  []string `json:"requires"`  // 解析自 @require
	Resources []string `json:"resources"` // 解析自 @resource，格式为 "名称 URL"
	// RequireAssets / ResourceAssets 为已成功下载到本地的依赖。
	// 与上面的声明数量不一致即表示有依赖下载失败，脚本无法正常工作。
	RequireAssets  []ScriptAsset `json:"require_assets"`
	ResourceAssets []ScriptAsset `json:"resource_assets"`
	Enabled        bool          `json:"enabled"` // 全局开关
	UpdatedAt      int64         `json:"updated_at"`
}

// ScriptAsset 表示一个已下载到本地的脚本依赖
type ScriptAsset struct {
	Name string `json:"name"` // @resource 的资源名；@require 留空
	URL  string `json:"url"`
	File string `json:"file"` // 相对该脚本 deps 目录的文件名
}

// ProxyEntry 代表代理池中的一个条目
type ProxyEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Proxy     string `json:"proxy"`
	Status    string `json:"status"` // "online", "offline", "unknown"
	Latency   string `json:"latency"`
	UpdatedAt int64  `json:"updated_at"`
}

type AutomationConfig struct {
	Enabled       bool   `json:"enabled"`
	APIListenAddr string `json:"api_listen_addr"`
	APIToken      string `json:"api_token"`
}

type AutomationInfo struct {
	Enabled         bool   `json:"enabled"`
	ListenAddr      string `json:"listen_addr"`
	BaseURL         string `json:"base_url"`
	AuthScheme      string `json:"auth_scheme"`
	Protocol        string `json:"protocol"`
	SessionCount    int    `json:"session_count"`
	TokenConfigured bool   `json:"token_configured"`
}

type AutomationSession struct {
	SessionID   string `json:"session_id"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	PID         int    `json:"pid"`
	StartedAt   int64  `json:"started_at"`
	Status      string `json:"status"`
	DebugPort   int    `json:"debug_port"`
	ConnectURL  string `json:"connect_url"`
	Protocol    string `json:"protocol"`
	StartURL    string `json:"start_url"`
	LastError   string `json:"last_error"`
}

type AutomationProfileSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Proxy    string `json:"proxy"`
	StartURL string `json:"start_url"`
	Platform string `json:"platform"`
	CreateAt int64  `json:"create_at"`
}

type automationSessionRuntime struct {
	cmd     *exec.Cmd
	profile BrowserProfile
}

// App struct
type App struct {
	ctx         context.Context
	profiles    []BrowserProfile
	proxies     []ProxyEntry
	userScripts []UserScript
	// assetFetcher 用于下载脚本的外部依赖。留空时走 fetchScriptAsset，
	// 测试可替换它以避免真实网络请求。
	assetFetcher         func(rawURL string) ([]byte, error)
	StartupURL           string // 用于从命令行拉起的 URL
	listener             net.Listener
	dataDir              string
	legacyDataDir        string
	legacyDataDirs       []string
	portableBaseDirs     []string
	storageMigrationNote string
	automationConfig     AutomationConfig
	automationListenAddr string
	automationServer     *http.Server
	automationSessions   map[string]*AutomationSession
	automationRuntimes   map[string]*automationSessionRuntime
	automationMu         sync.RWMutex
}

type automationCreateRequest struct {
	ProfileID string `json:"profile_id"`
	StartURL  string `json:"start_url"`
}

type automationErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type automationResponse struct {
	Success   bool                    `json:"success"`
	Data      interface{}             `json:"data,omitempty"`
	Error     *automationErrorPayload `json:"error,omitempty"`
	RequestID string                  `json:"request_id"`
}

type bidiCommandRequest struct {
	ID     int64       `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type bidiCommandResponse struct {
	ID     int64           `json:"id,omitempty"`
	Type   string          `json:"type,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	// WebDriver BiDi 的错误响应是扁平结构，error 为错误码字符串而非对象：
	// {"type":"error","id":1,"error":"invalid session id","message":"...","stacktrace":"..."}
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type bidiGetTreeResult struct {
	Contexts []struct {
		Context string `json:"context"`
	} `json:"contexts"`
}

var bidiEndpointPattern = regexp.MustCompile(`ws://(?:127\.0\.0\.1|localhost):\d+(?:/[^\s"]*)?`)

var (
	getExecutablePath        = os.Executable
	getUserHomeDir           = os.UserHomeDir
	runHiddenCombinedCommand = func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.CombinedOutput()
	}
	startHiddenCommand = func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Start()
	}
)

// NewApp creates a new App application struct
func NewApp() *App {
	a := &App{
		profiles:           []BrowserProfile{},
		proxies:            []ProxyEntry{},
		automationSessions: map[string]*AutomationSession{},
		automationRuntimes: map[string]*automationSessionRuntime{},
	}
	if err := a.initializeStorage(); err != nil {
		fmt.Printf("初始化存储目录失败: %v\n", err)
	}
	if err := a.loadAutomationConfig(); err != nil {
		fmt.Printf("初始化自动化配置失败: %v\n", err)
	}
	a.loadProfiles()
	a.loadProxies()
	a.loadUserScripts()
	return a
}

// initializeStorage 解析新的存储目录，并在需要时迁移旧版 data 目录。
func (a *App) initializeStorage() error {
	targetDir, err := a.resolveDataDir()
	if err != nil {
		return err
	}

	legacyDirs, err := a.getLegacyDataDirs()
	if err == nil {
		for _, legacyDir := range legacyDirs {
			migrated, migrateErr := migrateLegacyStorage(legacyDir, targetDir)
			if migrateErr != nil {
				return migrateErr
			}
			if migrated {
				a.storageMigrationNote = fmt.Sprintf("检测到旧版数据目录，已迁移到 %s。旧目录仍保留在 %s，可确认无误后手动清理。", targetDir, legacyDir)
				fmt.Println(a.storageMigrationNote)
				break
			}
		}
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("创建存储目录失败: %w", err)
	}

	a.dataDir = targetDir
	return nil
}

func (a *App) resolveDataDir() (string, error) {
	if a.dataDir != "" {
		return a.dataDir, nil
	}

	if portableDir, ok, err := a.resolvePortableDataDir(); err != nil {
		return "", err
	} else if ok {
		return portableDir, nil
	}

	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "MyBrowser"), nil
	}

	configDir, err := os.UserConfigDir()
	if err == nil && configDir != "" {
		lowerConfigDir := strings.ToLower(configDir)
		if strings.Contains(lowerConfigDir, "appdata\\roaming") {
			return filepath.Join(filepath.Dir(configDir), "Local", "MyBrowser"), nil
		}
		return filepath.Join(configDir, "MyBrowser"), nil
	}

	homeDir, homeErr := os.UserHomeDir()
	if homeErr != nil {
		if err != nil {
			return "", fmt.Errorf("无法解析 LOCALAPPDATA 且无法获取用户配置目录: %w", err)
		}
		return "", fmt.Errorf("无法解析存储目录: %w", homeErr)
	}

	return filepath.Join(homeDir, "AppData", "Local", "MyBrowser"), nil
}

func (a *App) resolvePortableDataDir() (string, bool, error) {
	baseDirs, err := a.getPortableBaseDirs()
	if err != nil {
		return "", false, err
	}

	for _, baseDir := range baseDirs {
		flagPath := filepath.Join(baseDir, "portable.flag")
		if _, statErr := os.Stat(flagPath); statErr == nil {
			return filepath.Join(baseDir, "MyBrowserData"), true, nil
		} else if !os.IsNotExist(statErr) {
			return "", false, fmt.Errorf("检查便携模式标记失败 [%s]: %w", flagPath, statErr)
		}
	}

	return "", false, nil
}

func (a *App) getPortableBaseDirs() ([]string, error) {
	if len(a.portableBaseDirs) > 0 {
		return uniquePaths(a.portableBaseDirs), nil
	}

	var candidates []string

	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("获取程序路径失败: %w", err)
	}
	candidates = append(candidates, filepath.Dir(exePath))

	workingDir, err := os.Getwd()
	if err == nil && workingDir != "" {
		candidates = append(candidates, workingDir)
	}

	return uniquePaths(candidates), nil
}

func (a *App) getLegacyDataDirs() ([]string, error) {
	if len(a.legacyDataDirs) > 0 {
		return uniquePaths(a.legacyDataDirs), nil
	}
	if a.legacyDataDir != "" {
		return []string{a.legacyDataDir}, nil
	}

	var candidates []string

	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("获取程序路径失败: %w", err)
	}
	candidates = append(candidates, filepath.Join(filepath.Dir(exePath), "data"))

	workingDir, err := os.Getwd()
	if err == nil && workingDir != "" {
		candidates = append(candidates, filepath.Join(workingDir, "data"))
	}

	return uniquePaths(candidates), nil
}

func (a *App) getStorageModeLabel() string {
	dataDir := filepath.Clean(a.getDataDir())
	for _, baseDir := range uniquePaths(a.portableBaseDirs) {
		if filepath.Clean(filepath.Join(baseDir, "MyBrowserData")) == dataDir {
			return "portable"
		}
	}

	baseDirs, err := a.getPortableBaseDirs()
	if err == nil {
		for _, baseDir := range baseDirs {
			if filepath.Clean(filepath.Join(baseDir, "MyBrowserData")) == dataDir {
				return "portable"
			}
		}
	}

	return "localappdata"
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		normalized := filepath.Clean(path)
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func migrateLegacyStorage(legacyDir, targetDir string) (bool, error) {
	legacyHasData, err := dirHasEntries(legacyDir)
	if err != nil {
		return false, err
	}
	if !legacyHasData {
		return false, nil
	}

	targetHasData, err := dirHasEntries(targetDir)
	if err != nil {
		return false, err
	}
	if targetHasData {
		return false, nil
	}

	if err := copyDir(legacyDir, targetDir); err != nil {
		return false, fmt.Errorf("迁移旧版数据目录失败: %w", err)
	}

	return true, nil
}

func dirHasEntries(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("读取目录失败 [%s]: %w", dir, err)
	}

	return len(entries) > 0, nil
}

func copyDir(srcDir, dstDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		targetPath := dstDir
		if relPath != "." {
			targetPath = filepath.Join(dstDir, relPath)
		}

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		return copyFile(path, targetPath, info.Mode())
	})
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// getDataDir 获取应用数据目录，统一存放在 LOCALAPPDATA\MyBrowser
func (a *App) getDataDir() string {
	if a.dataDir == "" {
		if err := a.initializeStorage(); err != nil {
			fmt.Printf("初始化存储目录失败: %v\n", err)
			return ""
		}
	}
	return a.dataDir
}

// getStoragePath 获取配置文件存储路径
func (a *App) getStoragePath() string {
	dir := a.getDataDir()
	return filepath.Join(dir, "profiles.json")
}

// loadProfiles 从文件加载配置
func (a *App) loadProfiles() {
	path := a.getStoragePath()
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("未找到配置文件，初始化默认数据: %v\n", err)
		// 初始化一个默认环境
		a.profiles = []BrowserProfile{
			{
				ID:       "default",
				Name:     "默认环境 (Firefox)",
				UA:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0",
				Platform: "Windows",
				Cookies:  "[]",
				CreateAt: time.Now().Unix(),
			},
		}
		a.saveProfiles()
		return
	}
	json.Unmarshal(data, &a.profiles)
}

// saveProfiles 保存配置到文件
func (a *App) saveProfiles() error {
	path := a.getStoragePath()
	data, _ := json.MarshalIndent(a.profiles, "", "  ")
	return os.WriteFile(path, data, 0644)
}

// --- 代理池管理 ---

func (a *App) getProxyStoragePath() string {
	dir := a.getDataDir()
	return filepath.Join(dir, "proxies.json")
}

func (a *App) loadProxies() {
	path := a.getProxyStoragePath()
	data, err := os.ReadFile(path)
	if err != nil {
		a.proxies = []ProxyEntry{}
		return
	}
	json.Unmarshal(data, &a.proxies)
}

func (a *App) saveProxies() error {
	path := a.getProxyStoragePath()
	data, _ := json.MarshalIndent(a.proxies, "", "  ")
	return os.WriteFile(path, data, 0644)
}

// --- 用户脚本（油猴脚本）管理 ---

var (
	metaBlockPattern = regexp.MustCompile(`(?s)//\s*==UserScript==\s*\r?\n(.*?)//\s*==/UserScript==`)
	metaLinePattern  = regexp.MustCompile(`^//\s*@(\S+)\s+(.*)$`)
	// matchPatternRe 校验 WebExtension 匹配模式。
	// 必须严格校验：manifest 中只要有一条非法模式，Gecko 会拒绝加载整个扩展。
	matchPatternRe = regexp.MustCompile(`^(\*|https?|wss?|ftp)://(\*|\*\.[^/*]+|[^/*]+)/`)
)

func (a *App) getUserScriptStoragePath() string {
	return filepath.Join(a.getDataDir(), "userscripts.json")
}

func (a *App) getUserScriptSourceDir() string {
	return filepath.Join(a.getDataDir(), "userscripts")
}

func (a *App) getUserScriptSourcePath(id string) string {
	return filepath.Join(a.getUserScriptSourceDir(), id+".user.js")
}

// getUserScriptDepsDir 返回某个脚本的依赖缓存目录。
// 按脚本隔离而非全局共享，换来的是删除脚本时可直接整目录清理，不必做引用计数。
func (a *App) getUserScriptDepsDir(id string) string {
	return filepath.Join(a.getUserScriptSourceDir(), id+".deps")
}

const (
	// maxScriptAssetSize 限制单个依赖体积。jQuery 约 90KB，此处留足余量。
	maxScriptAssetSize = 8 << 20 // 8 MiB
	scriptAssetTimeout = 30 * time.Second
)

// parseResourceDeclaration 拆解 "@resource 名称 URL" 的声明
func parseResourceDeclaration(val string) (name, rawURL string, ok bool) {
	fields := strings.Fields(val)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// scriptAssetFileName 用 URL 的哈希作为文件名。
// 不使用 URL 路径中的文件名，以避免路径穿越与同名覆盖。
func scriptAssetFileName(index int, rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	ext := ".bin"
	if parsed, err := url.Parse(rawURL); err == nil {
		if e := strings.ToLower(filepath.Ext(parsed.Path)); e != "" && len(e) <= 6 && isSafeAssetExt(e) {
			ext = e
		}
	}
	return fmt.Sprintf("%02d_%x%s", index, sum[:8], ext)
}

func isSafeAssetExt(ext string) bool {
	for _, r := range ext[1:] {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return len(ext) > 1
}

// fetchScriptAsset 下载单个依赖。仅允许 HTTPS，避免明文链路被篡改后注入代码。
func fetchScriptAsset(rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("地址非法: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("仅支持 https 依赖，实际为 %q", parsed.Scheme)
	}

	client := &http.Client{Timeout: scriptAssetTimeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败，状态码 %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxScriptAssetSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if len(data) > maxScriptAssetSize {
		return nil, fmt.Errorf("依赖过大，超过 %d MiB", maxScriptAssetSize>>20)
	}
	return data, nil
}

func (a *App) fetchAsset(rawURL string) ([]byte, error) {
	if a.assetFetcher != nil {
		return a.assetFetcher(rawURL)
	}
	return fetchScriptAsset(rawURL)
}

// downloadScriptAssets 下载脚本声明的 @require / @resource 到本地缓存。
//
// 单个依赖失败不阻断脚本保存：脚本仍会落盘，但下载数量与声明数量不符，
// unsupportedScriptFeatures 会据此报告脚本不可用，用户可稍后重试。
// 已存在且 URL 未变的依赖直接复用，避免每次编辑脚本都重新下载。
func (a *App) downloadScriptAssets(script *UserScript) {
	depsDir := a.getUserScriptDepsDir(script.ID)
	if len(script.Requires) == 0 && len(script.Resources) == 0 {
		os.RemoveAll(depsDir)
		script.RequireAssets = nil
		script.ResourceAssets = nil
		return
	}

	if err := os.MkdirAll(depsDir, 0755); err != nil {
		a.Log("warn", fmt.Sprintf("创建依赖目录失败: %v", err))
		return
	}

	existing := make(map[string]string, len(script.RequireAssets)+len(script.ResourceAssets))
	for _, asset := range append(append([]ScriptAsset{}, script.RequireAssets...), script.ResourceAssets...) {
		existing[asset.URL] = asset.File
	}

	fetch := func(index int, name, rawURL string) (ScriptAsset, bool) {
		fileName := scriptAssetFileName(index, rawURL)
		target := filepath.Join(depsDir, fileName)

		// URL 未变且文件仍在，直接复用
		if prev, ok := existing[rawURL]; ok && prev == fileName {
			if _, err := os.Stat(target); err == nil {
				return ScriptAsset{Name: name, URL: rawURL, File: fileName}, true
			}
		}

		data, err := a.fetchAsset(rawURL)
		if err != nil {
			a.Log("warn", fmt.Sprintf("脚本 [%s] 的依赖 %s %v", script.Name, rawURL, err))
			return ScriptAsset{}, false
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			a.Log("warn", fmt.Sprintf("写入依赖失败: %v", err))
			return ScriptAsset{}, false
		}
		return ScriptAsset{Name: name, URL: rawURL, File: fileName}, true
	}

	requireAssets := make([]ScriptAsset, 0, len(script.Requires))
	for i, rawURL := range script.Requires {
		if asset, ok := fetch(i, "", rawURL); ok {
			requireAssets = append(requireAssets, asset)
		}
	}

	resourceAssets := make([]ScriptAsset, 0, len(script.Resources))
	for i, decl := range script.Resources {
		name, rawURL, ok := parseResourceDeclaration(decl)
		if !ok {
			a.Log("warn", fmt.Sprintf("脚本 [%s] 的 @resource 声明格式非法: %q", script.Name, decl))
			continue
		}
		if asset, ok := fetch(1000+i, name, rawURL); ok {
			resourceAssets = append(resourceAssets, asset)
		}
	}

	script.RequireAssets = requireAssets
	script.ResourceAssets = resourceAssets

	total := len(script.Requires) + len(script.Resources)
	got := len(requireAssets) + len(resourceAssets)
	if got == total {
		a.Log("info", fmt.Sprintf("脚本 [%s] 的 %d 个外部依赖已全部下载", script.Name, total))
	} else {
		a.Log("warn", fmt.Sprintf("脚本 [%s] 有 %d/%d 个依赖下载失败，启用后不会正常工作",
			script.Name, total-got, total))
	}
}

func (a *App) loadUserScripts() {
	data, err := os.ReadFile(a.getUserScriptStoragePath())
	if err != nil {
		a.userScripts = []UserScript{}
		return
	}
	if err := json.Unmarshal(data, &a.userScripts); err != nil {
		fmt.Printf("解析用户脚本索引失败，已重置: %v\n", err)
		a.userScripts = []UserScript{}
	}
}

func (a *App) saveUserScripts() error {
	data, err := json.MarshalIndent(a.userScripts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.getUserScriptStoragePath(), data, 0644)
}

// isValidMatchPattern 判断是否为 Gecko 可接受的匹配模式。
func isValidMatchPattern(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "<all_urls>" {
		return true
	}
	if strings.HasPrefix(pattern, "file:///") {
		return true
	}
	return matchPatternRe.MatchString(pattern)
}

// parseUserScriptMeta 解析标准 UserScript 元数据块。
// 解析失败不阻断保存：脚本仍可保存，由用户在界面上手工补齐匹配规则。
func parseUserScriptMeta(source string) UserScript {
	script := UserScript{RunAt: "document_end", World: "isolated"}

	block := metaBlockPattern.FindStringSubmatch(source)
	if len(block) < 2 {
		return script
	}

	for _, line := range strings.Split(block[1], "\n") {
		m := metaLinePattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 3 {
			continue
		}
		key := strings.ToLower(m[1])
		val := strings.TrimSpace(m[2])
		if val == "" {
			continue
		}

		switch key {
		case "name":
			script.Name = val
		case "description":
			// @description:zh-CN 等本地化变体的 key 不等于 "description"，会被忽略，此处只取通用描述
			script.Description = val
		case "version":
			script.Version = val
		case "require":
			script.Requires = append(script.Requires, val)
		case "resource":
			script.Resources = append(script.Resources, val)
		case "match", "include":
			// @include 允许 * 与正则等非标准写法，这里只收 Gecko 能接受的，
			// 其余丢弃以免污染 manifest 导致整个扩展加载失败。
			if isValidMatchPattern(val) {
				script.Matches = append(script.Matches, val)
			}
		case "run-at":
			// 油猴写法 document-start 转为 WebExtension 的 document_start
			normalized := strings.ReplaceAll(val, "-", "_")
			switch normalized {
			case "document_start", "document_end", "document_idle":
				script.RunAt = normalized
			}
		case "grant":
			// @grant none 在油猴中表示注入主世界，语义与直觉相反。
			// 此处仅视作"不需要 GM API"，是否进入 page 模式由界面开关独立决定。
			if !strings.EqualFold(val, "none") {
				script.Grants = append(script.Grants, val)
			}
		}
	}

	return script
}

func defaultAutomationConfig() AutomationConfig {
	return AutomationConfig{
		Enabled:       true,
		APIListenAddr: "127.0.0.1:9090",
	}
}

func (a *App) getAutomationConfigPath() string {
	return filepath.Join(a.getDataDir(), "automation.json")
}

func (a *App) loadAutomationConfig() error {
	config := defaultAutomationConfig()
	path := a.getAutomationConfigPath()

	data, err := os.ReadFile(path)
	if err == nil {
		if unmarshalErr := json.Unmarshal(data, &config); unmarshalErr != nil {
			return fmt.Errorf("解析自动化配置失败: %w", unmarshalErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取自动化配置失败: %w", err)
	}

	if config.APIListenAddr == "" {
		config.APIListenAddr = "127.0.0.1:9090"
	}

	if strings.TrimSpace(config.APIToken) == "" {
		token, tokenErr := generateAutomationToken()
		if tokenErr != nil {
			return tokenErr
		}
		config.APIToken = token
	}

	a.automationConfig = config
	return a.saveAutomationConfig()
}

func (a *App) saveAutomationConfig() error {
	path := a.getAutomationConfigPath()
	data, err := json.MarshalIndent(a.automationConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func generateAutomationToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成自动化 token 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func reserveTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("无法解析监听端口")
	}
	return addr.Port, nil
}

func buildAutomationConnectURL(port int) string {
	return fmt.Sprintf("ws://127.0.0.1:%d/session", port)
}

func normalizeBidiConnectURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.HasSuffix(trimmed, "/session") {
		return strings.TrimRight(trimmed, "/") + "/session"
	}
	return trimmed
}

func extractBidiConnectURL(line string) (string, bool) {
	match := bidiEndpointPattern.FindString(line)
	if match == "" {
		return "", false
	}
	return normalizeBidiConnectURL(match), true
}

func (a *App) automationSessionCount() int {
	a.automationMu.RLock()
	defer a.automationMu.RUnlock()
	return len(a.automationSessions)
}

func (a *App) buildAutomationInfo() AutomationInfo {
	listenAddr := a.automationListenAddr
	if listenAddr == "" {
		listenAddr = a.automationConfig.APIListenAddr
	}

	return AutomationInfo{
		Enabled:         a.automationConfig.Enabled,
		ListenAddr:      listenAddr,
		BaseURL:         "http://" + listenAddr,
		AuthScheme:      "Bearer",
		Protocol:        "bidi",
		SessionCount:    a.automationSessionCount(),
		TokenConfigured: strings.TrimSpace(a.automationConfig.APIToken) != "",
	}
}

func (a *App) copyAutomationSession(session *AutomationSession) AutomationSession {
	if session == nil {
		return AutomationSession{}
	}
	return *session
}

func (a *App) listAutomationSessions() []AutomationSession {
	a.automationMu.RLock()
	defer a.automationMu.RUnlock()

	sessions := make([]AutomationSession, 0, len(a.automationSessions))
	for _, session := range a.automationSessions {
		sessions = append(sessions, *session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt > sessions[j].StartedAt
	})
	return sessions
}

func (a *App) findAutomationSessionByProfileLocked(profileID string) *AutomationSession {
	for _, session := range a.automationSessions {
		if session.ProfileID == profileID && session.Status != "stopped" && session.Status != "error" {
			return session
		}
	}
	return nil
}

func (a *App) updateAutomationSession(sessionID string, updater func(*AutomationSession)) {
	a.automationMu.Lock()
	defer a.automationMu.Unlock()

	session, ok := a.automationSessions[sessionID]
	if !ok {
		return
	}
	updater(session)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (a *App) dialAutomationSession(connectURL string, timeout time.Duration) (*websocket.Conn, error) {
	deadline := time.Now().Add(timeout)
	dialer := websocket.Dialer{
		HandshakeTimeout: minDuration(3*time.Second, timeout),
	}

	var lastErr error
	for time.Now().Before(deadline) {
		conn, _, err := dialer.Dial(connectURL, nil)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("连接超时")
	}
	return nil, lastErr
}

func sendBiDiCommand(conn *websocket.Conn, commandID int64, method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	if params == nil {
		params = map[string]interface{}{}
	}

	if err := conn.SetWriteDeadline(time.Now().Add(minDuration(5*time.Second, timeout))); err != nil {
		return nil, err
	}
	if err := conn.WriteJSON(bidiCommandRequest{
		ID:     commandID,
		Method: method,
		Params: params,
	}); err != nil {
		return nil, err
	}

	// 整体只设置一次读超时。gorilla 的连接在一次读超时后即进入失败态，
	// 若按超时重试，下一次读取会直接 panic 而非返回错误。
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	for {
		var response bidiCommandResponse
		if err := conn.ReadJSON(&response); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return nil, fmt.Errorf("%s timed out after %s", method, timeout)
			}
			return nil, err
		}

		// 跳过事件推送与其他命令的响应；读超时已在整体层面兜底
		if response.ID != commandID {
			continue
		}
		if response.Error != "" {
			message := strings.TrimSpace(response.Message)
			if message == "" {
				message = strings.TrimSpace(response.Error)
			}
			if message == "" {
				message = "unknown bidi error"
			}
			return nil, fmt.Errorf("%s failed: %s", method, message)
		}
		return response.Result, nil
	}
}

func extractRootContextID(raw json.RawMessage) (string, error) {
	var result bidiGetTreeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("解析 browsingContext.getTree 结果失败: %w", err)
	}
	if len(result.Contexts) == 0 || strings.TrimSpace(result.Contexts[0].Context) == "" {
		return "", fmt.Errorf("未找到可用的浏览上下文")
	}
	return result.Contexts[0].Context, nil
}

func (a *App) navigateAutomationSession(session AutomationSession, targetURL string) error {
	if strings.TrimSpace(targetURL) == "" {
		return nil
	}

	a.automationMu.RLock()
	if current := a.automationSessions[session.SessionID]; current != nil {
		session = *current
	}
	a.automationMu.RUnlock()

	connectURL := strings.TrimSpace(session.ConnectURL)
	if connectURL == "" {
		connectURL = buildAutomationConnectURL(session.DebugPort)
	}
	connectURL = normalizeBidiConnectURL(connectURL)

	conn, err := a.dialAutomationSession(connectURL, 12*time.Second)
	if err != nil {
		return fmt.Errorf("连接自动化会话失败: %w", err)
	}
	defer conn.Close()

	commandID := int64(1)
	if _, err := sendBiDiCommand(conn, commandID, "session.new", map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": map[string]interface{}{},
		},
	}, 5*time.Second); err != nil {
		a.Log("warn", fmt.Sprintf("自动化会话 [%s] session.new 兼容性提示: %v", session.SessionID, err))
	}
	commandID++

	treeResult, err := sendBiDiCommand(conn, commandID, "browsingContext.getTree", map[string]interface{}{}, 8*time.Second)
	if err != nil {
		return err
	}
	commandID++

	contextID, err := extractRootContextID(treeResult)
	if err != nil {
		return err
	}

	if _, err := sendBiDiCommand(conn, commandID, "browsingContext.navigate", map[string]interface{}{
		"context": contextID,
		"url":     targetURL,
		"wait":    "none",
	}, 8*time.Second); err != nil {
		return err
	}

	return nil
}

func (a *App) ensureAutomationSession(profile BrowserProfile) (AutomationSession, bool, error) {
	a.automationMu.Lock()
	if existing := a.findAutomationSessionByProfileLocked(profile.ID); existing != nil {
		snapshot := *existing
		a.automationMu.Unlock()
		return snapshot, true, nil
	}
	a.automationMu.Unlock()

	exePath, userDataDir, err := a.prepareProfileLaunch(profile)
	if err != nil {
		return AutomationSession{}, false, err
	}

	debugPort, err := reserveTCPPort()
	if err != nil {
		return AutomationSession{}, false, fmt.Errorf("分配自动化端口失败: %v", err)
	}

	session := &AutomationSession{
		SessionID:   uuid.New().String(),
		ProfileID:   profile.ID,
		ProfileName: profile.Name,
		StartedAt:   time.Now().Unix(),
		Status:      "starting",
		DebugPort:   debugPort,
		ConnectURL:  buildAutomationConnectURL(debugPort),
		Protocol:    "bidi",
	}

	cmd := exec.Command(exePath, a.buildBrowserArgs(userDataDir, "", debugPort)...)
	cmd.Env = a.buildCamoufoxEnv(profile)

	stdout, stdoutErr := cmd.StdoutPipe()
	if stdoutErr != nil {
		return AutomationSession{}, false, fmt.Errorf("创建自动化输出管道失败: %v", stdoutErr)
	}
	stderr, stderrErr := cmd.StderrPipe()
	if stderrErr != nil {
		return AutomationSession{}, false, fmt.Errorf("创建自动化错误管道失败: %v", stderrErr)
	}

	a.automationMu.Lock()
	if existing := a.findAutomationSessionByProfileLocked(profile.ID); existing != nil {
		snapshot := *existing
		a.automationMu.Unlock()
		return snapshot, true, nil
	}
	a.automationSessions[session.SessionID] = session
	a.automationRuntimes[session.SessionID] = &automationSessionRuntime{
		cmd:     cmd,
		profile: profile,
	}
	a.automationMu.Unlock()

	if err := cmd.Start(); err != nil {
		a.automationMu.Lock()
		delete(a.automationSessions, session.SessionID)
		delete(a.automationRuntimes, session.SessionID)
		a.automationMu.Unlock()
		return AutomationSession{}, false, fmt.Errorf("自动化浏览器启动失败: %v", err)
	}

	go a.watchAutomationPipe(session.SessionID, "stdout", stdout)
	go a.watchAutomationPipe(session.SessionID, "stderr", stderr)

	a.updateAutomationSession(session.SessionID, func(current *AutomationSession) {
		current.PID = cmd.Process.Pid
		current.Status = "running"
	})

	a.Log("info", fmt.Sprintf("自动化会话 [%s] 已启动，环境 [%s]，BiDi: %s", session.SessionID, profile.Name, session.ConnectURL))
	a.monitorBrowserExit(cmd, profile, session.SessionID)

	a.automationMu.RLock()
	snapshot := a.copyAutomationSession(a.automationSessions[session.SessionID])
	a.automationMu.RUnlock()
	return snapshot, false, nil
}

func (a *App) watchAutomationPipe(sessionID, stream string, reader io.ReadCloser) {
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if connectURL, ok := extractBidiConnectURL(line); ok {
			a.updateAutomationSession(sessionID, func(session *AutomationSession) {
				session.ConnectURL = connectURL
			})
		}

		if strings.Contains(strings.ToLower(line), "webdriver bidi") || strings.Contains(strings.ToLower(line), "remote") {
			a.Log("info", fmt.Sprintf("自动化会话 [%s] %s: %s", sessionID, stream, line))
		}
	}
	if err := scanner.Err(); err != nil {
		a.Log("warn", fmt.Sprintf("读取自动化会话管道时出错: %v", err))
	}
}

func (a *App) GetProxies() []ProxyEntry {
	return a.proxies
}

func (a *App) AddProxy(name, proxyStr string) (ProxyEntry, error) {
	entry := ProxyEntry{
		ID:        uuid.New().String(),
		Name:      name,
		Proxy:     proxyStr,
		Status:    "unknown",
		UpdatedAt: time.Now().Unix(),
	}
	a.proxies = append(a.proxies, entry)
	err := a.saveProxies()
	a.Log("info", fmt.Sprintf("添加代理成功: %s (%s)", name, proxyStr))
	return entry, err
}

func (a *App) DeleteProxy(id string) error {
	for i, p := range a.proxies {
		if p.ID == id {
			a.proxies = append(a.proxies[:i], a.proxies[i+1:]...)
			a.Log("info", fmt.Sprintf("删除代理成功: %s", id))
			return a.saveProxies()
		}
	}
	return fmt.Errorf("未找到代理")
}

func (a *App) UpdateProxy(updated ProxyEntry) error {
	for i, p := range a.proxies {
		if p.ID == updated.ID {
			a.proxies[i] = updated
			return a.saveProxies()
		}
	}
	return fmt.Errorf("未找到代理")
}

// --- 用户脚本对外接口 ---

// GetUserScripts 返回全部用户脚本的元数据（不含正文）
func (a *App) GetUserScripts() []UserScript {
	if a.userScripts == nil {
		return []UserScript{}
	}
	return a.userScripts
}

// GetUserScriptSource 读取脚本正文
func (a *App) GetUserScriptSource(id string) (string, error) {
	data, err := os.ReadFile(a.getUserScriptSourcePath(id))
	if err != nil {
		return "", fmt.Errorf("读取脚本内容失败: %v", err)
	}
	return string(data), nil
}

// SaveUserScript 保存脚本。id 为空表示新建。
// 元数据由正文解析得出，用户在界面上的手工调整通过 UpdateUserScriptMeta 覆盖。
func (a *App) SaveUserScript(id, source string) (UserScript, error) {
	if strings.TrimSpace(source) == "" {
		return UserScript{}, fmt.Errorf("脚本内容不能为空")
	}

	if err := os.MkdirAll(a.getUserScriptSourceDir(), 0755); err != nil {
		return UserScript{}, fmt.Errorf("创建脚本目录失败: %v", err)
	}

	parsed := parseUserScriptMeta(source)

	var target *UserScript
	for i := range a.userScripts {
		if a.userScripts[i].ID == id {
			target = &a.userScripts[i]
			break
		}
	}

	if target == nil {
		// 新建：默认不启用，避免脚本一保存就立刻在所有环境生效
		script := UserScript{
			ID:      uuid.New().String(),
			Enabled: false,
		}
		a.userScripts = append(a.userScripts, script)
		target = &a.userScripts[len(a.userScripts)-1]
	}

	target.Name = parsed.Name
	if strings.TrimSpace(target.Name) == "" {
		target.Name = "未命名脚本"
	}
	target.Description = parsed.Description
	target.Version = parsed.Version
	target.Matches = parsed.Matches
	target.RunAt = parsed.RunAt
	target.Grants = parsed.Grants
	target.Requires = parsed.Requires
	target.Resources = parsed.Resources
	// World 由界面开关控制，重新解析正文时不应把用户的选择覆盖掉
	if target.World == "" {
		target.World = parsed.World
	}
	target.UpdatedAt = time.Now().Unix()

	if err := os.WriteFile(a.getUserScriptSourcePath(target.ID), []byte(source), 0644); err != nil {
		return UserScript{}, fmt.Errorf("写入脚本文件失败: %v", err)
	}

	// 下载 @require / @resource；失败不阻断保存，由兼容性检查汇报
	a.downloadScriptAssets(target)

	result := *target
	if err := a.saveUserScripts(); err != nil {
		return result, err
	}

	a.Log("info", fmt.Sprintf("已保存用户脚本: %s", result.Name))
	if len(result.Matches) == 0 {
		a.Log("warn", fmt.Sprintf("脚本 [%s] 未解析到合法的 @match 规则，启用后不会生效", result.Name))
	}
	if blockers := unsupportedScriptFeatures(result); len(blockers) > 0 {
		a.Log("warn", fmt.Sprintf("脚本 [%s] 使用了当前引擎尚未支持的能力（%s），启用后不会正常工作",
			result.Name, strings.Join(blockers, "、")))
	}
	return result, nil
}

// unsupportedScriptFeatures 返回脚本用到、但本引擎不支持的能力。
//
// 这些能力缺失会让脚本"安装成功却静默失效"，因此需要在界面上明确告知，
// 而不是让用户自己去控制台找 "$ is not defined"。
//
// 注意：GM_* API 与 @resource 是**刻意不实现**的，不是待办项。
// 需要它们的多是日常浏览增强脚本，其界面改动在页面上高度可见，与本项目的
// 反检测目标相悖；且 GM_xmlhttpRequest 需为扩展申请 <all_urls> 跨域权限，
// 会实质扩大暴露面。详见 .antigravity_docs/plans/userscript_engine_plan.md 第 1.3 节。
func unsupportedScriptFeatures(script UserScript) []string {
	var blockers []string

	// @require 已支持，但下载不全时脚本一样跑不起来
	if missing := len(script.Requires) - len(script.RequireAssets); missing > 0 {
		blockers = append(blockers, fmt.Sprintf("%d 个 @require 依赖未下载成功", missing))
	}
	// @resource 需要 GM_getResourceText / GM_getResourceURL 才能取用，尚未实现
	if len(script.Resources) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d 个 @resource 外部资源", len(script.Resources)))
	}
	if len(script.Grants) > 0 {
		blockers = append(blockers, fmt.Sprintf("GM API（%s）", strings.Join(script.Grants, ", ")))
	}
	return blockers
}

// RedownloadScriptAssets 重新下载指定脚本的外部依赖，供下载失败后重试
func (a *App) RedownloadScriptAssets(id string) (UserScript, error) {
	for i := range a.userScripts {
		if a.userScripts[i].ID != id {
			continue
		}
		// 清空既有记录以强制重新拉取，避免命中"复用"分支
		a.userScripts[i].RequireAssets = nil
		a.userScripts[i].ResourceAssets = nil
		a.downloadScriptAssets(&a.userScripts[i])

		result := a.userScripts[i]
		if err := a.saveUserScripts(); err != nil {
			return result, err
		}
		return result, nil
	}
	return UserScript{}, fmt.Errorf("未找到脚本")
}

// SetUserScriptWorld 设置脚本的运行世界。
// page 模式会在页面 window 上留下可被检测的痕迹，仅在脚本需访问页面 JS 变量时使用。
func (a *App) SetUserScriptWorld(id, world string) error {
	if world != "isolated" && world != "page" {
		return fmt.Errorf("无效的运行模式: %s", world)
	}
	for i := range a.userScripts {
		if a.userScripts[i].ID == id {
			a.userScripts[i].World = world
			a.userScripts[i].UpdatedAt = time.Now().Unix()
			return a.saveUserScripts()
		}
	}
	return fmt.Errorf("未找到脚本")
}

// SetUserScriptEnabled 设置脚本的全局启用状态
func (a *App) SetUserScriptEnabled(id string, enabled bool) error {
	for i := range a.userScripts {
		if a.userScripts[i].ID == id {
			a.userScripts[i].Enabled = enabled
			a.userScripts[i].UpdatedAt = time.Now().Unix()
			return a.saveUserScripts()
		}
	}
	return fmt.Errorf("未找到脚本")
}

// DeleteUserScript 删除脚本，并清理所有环境中对它的引用
func (a *App) DeleteUserScript(id string) error {
	found := false
	for i, s := range a.userScripts {
		if s.ID == id {
			a.userScripts = append(a.userScripts[:i], a.userScripts[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("未找到脚本")
	}

	if err := os.Remove(a.getUserScriptSourcePath(id)); err != nil && !os.IsNotExist(err) {
		a.Log("warn", fmt.Sprintf("删除脚本文件失败: %v", err))
	}
	// 依赖按脚本隔离存放，可直接整目录清理
	if err := os.RemoveAll(a.getUserScriptDepsDir(id)); err != nil {
		a.Log("warn", fmt.Sprintf("清理依赖缓存失败: %v", err))
	}

	// 清理各环境的引用，避免留下悬空 ID
	profilesDirty := false
	for i := range a.profiles {
		filtered := make([]string, 0, len(a.profiles[i].EnabledScripts))
		for _, sid := range a.profiles[i].EnabledScripts {
			if sid != id {
				filtered = append(filtered, sid)
			}
		}
		if len(filtered) != len(a.profiles[i].EnabledScripts) {
			a.profiles[i].EnabledScripts = filtered
			profilesDirty = true
		}
	}
	if profilesDirty {
		if err := a.saveProfiles(); err != nil {
			return err
		}
	}

	a.Log("info", fmt.Sprintf("已删除用户脚本: %s", id))
	return a.saveUserScripts()
}

// SetProfileScripts 设置某个环境启用的脚本清单
func (a *App) SetProfileScripts(profileID string, scriptIDs []string) error {
	known := make(map[string]bool, len(a.userScripts))
	for _, s := range a.userScripts {
		known[s.ID] = true
	}

	filtered := make([]string, 0, len(scriptIDs))
	for _, id := range scriptIDs {
		if known[id] {
			filtered = append(filtered, id)
		}
	}

	for i := range a.profiles {
		if a.profiles[i].ID == profileID {
			a.profiles[i].EnabledScripts = filtered
			a.Log("info", fmt.Sprintf("环境 [%s] 已启用 %d 个脚本，重启环境后生效",
				a.profiles[i].Name, len(filtered)))
			return a.saveProfiles()
		}
	}
	return fmt.Errorf("未找到环境")
}

// maxUserScriptSize 限制单个脚本体积。真实脚本可达数 MB（如 LinkSwift 约 760KB），
// 此处留出充裕余量，同时避免误拖大文件把界面拖垮。
const maxUserScriptSize = 16 << 20 // 16 MiB

// ScriptInstallOutcome 描述一个文件的安装结果，供批量拖放时逐个反馈
type ScriptInstallOutcome struct {
	FileName    string     `json:"file_name"`
	OK          bool       `json:"ok"`
	Error       string     `json:"error"`
	Script      UserScript `json:"script"`
	Unsupported []string   `json:"unsupported"` // 当前引擎不支持、会导致脚本失效的能力
}

// InstallUserScriptsFromPaths 从本地文件路径批量安装脚本，供窗口拖放使用。
//
// 单个文件失败不影响其余文件，逐个返回结果由前端汇总提示。
func (a *App) InstallUserScriptsFromPaths(paths []string) []ScriptInstallOutcome {
	outcomes := make([]ScriptInstallOutcome, 0, len(paths))

	for _, path := range paths {
		name := filepath.Base(path)
		outcome := ScriptInstallOutcome{FileName: name}

		info, err := os.Stat(path)
		switch {
		case err != nil:
			outcome.Error = fmt.Sprintf("无法读取: %v", err)
		case info.IsDir():
			outcome.Error = "这是一个文件夹，请拖入脚本文件"
		case info.Size() > maxUserScriptSize:
			outcome.Error = fmt.Sprintf("文件过大（%.1f MB），已跳过", float64(info.Size())/(1<<20))
		case !isLikelyUserScriptFile(name):
			outcome.Error = "不是脚本文件，仅支持 .user.js / .js / .txt"
		default:
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				outcome.Error = fmt.Sprintf("读取失败: %v", readErr)
				break
			}
			script, saveErr := a.SaveUserScript("", string(data))
			if saveErr != nil {
				outcome.Error = saveErr.Error()
				break
			}
			outcome.OK = true
			outcome.Script = script
			outcome.Unsupported = unsupportedScriptFeatures(script)
		}

		if !outcome.OK && outcome.Error != "" {
			a.Log("warn", fmt.Sprintf("拖入的文件 [%s] 未能安装: %s", name, outcome.Error))
		}
		outcomes = append(outcomes, outcome)
	}

	return outcomes
}

// isLikelyUserScriptFile 判断文件名是否可能是用户脚本。
// 放宽到 .txt 是因为部分站点下载下来的脚本会被浏览器改成该后缀。
func isLikelyUserScriptFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".user.js") ||
		strings.HasSuffix(lower, ".js") ||
		strings.HasSuffix(lower, ".txt")
}

// ImportUserScriptFromFile 从本地 .user.js 文件导入脚本
func (a *App) ImportUserScriptFromFile() (UserScript, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择用户脚本文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "UserScript (*.user.js;*.js)", Pattern: "*.user.js;*.js"},
		},
	})
	if err != nil || path == "" {
		return UserScript{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return UserScript{}, fmt.Errorf("读取脚本文件失败: %v", err)
	}

	return a.SaveUserScript("", string(data))
}

func (a *App) TestProxyEntry(id string) (string, error) {
	var target *ProxyEntry
	for i, p := range a.proxies {
		if p.ID == id {
			target = &a.proxies[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("代理不存在")
	}

	res, err := a.TestProxy(target.Proxy)
	if err == nil {
		target.Status = "online"
		target.Latency = res
	} else {
		target.Status = "offline"
		target.Latency = "N/A"
	}
	target.UpdatedAt = time.Now().Unix()
	a.saveProxies()
	return res, err
}

// --- 日志系统 ---

func (a *App) Log(level, message string) {
	timestamp := time.Now().Format("15:04:05")
	logEntry := map[string]string{
		"time":    timestamp,
		"level":   level,
		"message": message,
	}
	if a.ctx != nil {
		// 发送事件到前端
		runtime.EventsEmit(a.ctx, "log_update", logEntry)
	}
}

func normalizeStartURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("默认标签页格式无效，请输入有效域名或 http(s) 地址")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("默认标签页仅支持 http 或 https")
	}

	return parsed.String(), nil
}

func normalizeCategory(raw string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
}

func normalizeImportedProfile(profile BrowserProfile) (BrowserProfile, error) {
	profile.Category = normalizeCategory(profile.Category)

	normalizedStartURL, err := normalizeStartURL(profile.StartURL)
	if err != nil {
		return BrowserProfile{}, err
	}
	profile.StartURL = normalizedStartURL

	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = "导入环境"
	}
	if strings.TrimSpace(profile.Platform) == "" {
		profile.Platform = "Windows"
	}
	if strings.TrimSpace(profile.Cookies) == "" {
		profile.Cookies = "[]"
	}

	return profile, nil
}

// CreateProfile 创建新环境
func (a *App) CreateProfile(name, proxy, ua, startURL, category string) (BrowserProfile, error) {
	normalizedStartURL, err := normalizeStartURL(startURL)
	if err != nil {
		return BrowserProfile{}, err
	}

	newProfile := BrowserProfile{
		ID:       uuid.New().String(),
		Name:     name,
		Category: normalizeCategory(category),
		Proxy:    proxy,
		StartURL: normalizedStartURL,
		UA:       ua,
		Platform: "Windows",
		Cookies:  "[]",
		CreateAt: time.Now().Unix(),
	}
	a.profiles = append(a.profiles, newProfile)
	err = a.saveProfiles()
	return newProfile, err
}

// DeleteProfile 删除环境
func (a *App) DeleteProfile(id string) error {
	for i, p := range a.profiles {
		if p.ID == id {
			a.profiles = append(a.profiles[:i], a.profiles[i+1:]...)
			return a.saveProfiles()
		}
	}
	return fmt.Errorf("未找到环境: %s", id)
}

// UpdateProfile 更新环境配置
func (a *App) UpdateProfile(updated BrowserProfile) error {
	normalizedStartURL, err := normalizeStartURL(updated.StartURL)
	if err != nil {
		return err
	}
	updated.StartURL = normalizedStartURL
	updated.Category = normalizeCategory(updated.Category)

	for i, p := range a.profiles {
		if p.ID == updated.ID {
			a.profiles[i] = updated
			return a.saveProfiles()
		}
	}
	return fmt.Errorf("未找到环境: %s", updated.ID)
}

// startup is called when the app starts. The context is saved
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	if a.storageMigrationNote != "" {
		a.Log("info", a.storageMigrationNote)
	}

	// 如果启动时带有 URL 参数，仅记录，由前端决定如何处理
	if a.StartupURL != "" {
		a.Log("info", fmt.Sprintf("检测到待处理 URL: %s，请选择环境启动...", a.StartupURL))
	}

	if err := a.startAutomationServer(); err != nil {
		a.Log("error", fmt.Sprintf("本地自动化 API 启动失败: %v", err))
	} else if a.automationConfig.Enabled {
		a.Log("info", fmt.Sprintf("本地自动化 API 已启动: http://%s", a.automationListenAddr))
	}

	// 监听来自其他实例的消息 (单实例 IPC)
	if a.listener != nil {
		go func() {
			for {
				conn, err := a.listener.Accept()
				if err != nil {
					return
				}
				buf := make([]byte, 2048)
				n, err := conn.Read(buf)
				if err == nil && n > 0 {
					receivedURL := string(buf[:n])
					a.Log("info", fmt.Sprintf("收到外部新链接: %s", receivedURL))
					// 通知前端更新 pendingURL
					runtime.EventsEmit(a.ctx, "external_url_received", receivedURL)
				}
				conn.Close()
			}
		}()
	}
}

func (a *App) startAutomationServer() error {
	if !a.automationConfig.Enabled {
		a.automationListenAddr = ""
		return nil
	}

	if a.automationServer != nil {
		return nil
	}

	listenAddr := strings.TrimSpace(a.automationConfig.APIListenAddr)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:9090"
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
	}

	actualAddr := ln.Addr().String()
	a.automationListenAddr = actualAddr
	a.automationConfig.APIListenAddr = actualAddr
	if saveErr := a.saveAutomationConfig(); saveErr != nil {
		ln.Close()
		return saveErr
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/automation/info", a.handleAutomationInfo)
	mux.HandleFunc("/api/v1/automation/profiles", a.handleAutomationProfiles)
	mux.HandleFunc("/api/v1/automation/sessions", a.handleAutomationSessions)
	mux.HandleFunc("/api/v1/automation/sessions/", a.handleAutomationSessionByID)
	mux.HandleFunc("/api/v1/automation/token/rotate", a.handleAutomationRotateToken)

	server := &http.Server{
		Handler:           a.withAutomationCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.automationServer = server

	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			a.Log("error", fmt.Sprintf("本地自动化 API 异常退出: %v", serveErr))
		}
	}()

	return nil
}

func (a *App) stopAutomationServer() error {
	if a.automationServer == nil {
		a.automationListenAddr = ""
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := a.automationServer.Shutdown(ctx)
	a.automationServer = nil
	a.automationListenAddr = ""
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (a *App) withAutomationCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireAutomationAuth(w http.ResponseWriter, r *http.Request, requestID string) bool {
	token := strings.TrimSpace(a.automationConfig.APIToken)
	if token == "" {
		a.writeAutomationJSON(w, http.StatusServiceUnavailable, requestID, nil, &automationErrorPayload{
			Code:    "automation_unavailable",
			Message: "本地自动化 token 未初始化",
		})
		return false
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	expected := "Bearer " + token
	if authHeader != expected {
		a.writeAutomationJSON(w, http.StatusUnauthorized, requestID, nil, &automationErrorPayload{
			Code:    "unauthorized",
			Message: "缺少有效的 Bearer token",
		})
		return false
	}
	return true
}

func (a *App) writeAutomationJSON(w http.ResponseWriter, statusCode int, requestID string, data interface{}, apiErr *automationErrorPayload) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(automationResponse{
		Success:   apiErr == nil,
		Data:      data,
		Error:     apiErr,
		RequestID: requestID,
	})
}

func (a *App) handleAutomationInfo(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	if r.Method != http.MethodGet {
		a.writeAutomationJSON(w, http.StatusMethodNotAllowed, requestID, nil, &automationErrorPayload{
			Code:    "method_not_allowed",
			Message: "仅支持 GET",
		})
		return
	}
	if !a.requireAutomationAuth(w, r, requestID) {
		return
	}
	a.writeAutomationJSON(w, http.StatusOK, requestID, a.buildAutomationInfo(), nil)
}

func (a *App) handleAutomationProfiles(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	if r.Method != http.MethodGet {
		a.writeAutomationJSON(w, http.StatusMethodNotAllowed, requestID, nil, &automationErrorPayload{
			Code:    "method_not_allowed",
			Message: "仅支持 GET",
		})
		return
	}
	if !a.requireAutomationAuth(w, r, requestID) {
		return
	}
	summaries := make([]AutomationProfileSummary, 0, len(a.profiles))
	for _, profile := range a.profiles {
		summaries = append(summaries, AutomationProfileSummary{
			ID:       profile.ID,
			Name:     profile.Name,
			Category: profile.Category,
			Proxy:    profile.Proxy,
			StartURL: profile.StartURL,
			Platform: profile.Platform,
			CreateAt: profile.CreateAt,
		})
	}
	a.writeAutomationJSON(w, http.StatusOK, requestID, summaries, nil)
}

func (a *App) handleAutomationSessions(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	if !a.requireAutomationAuth(w, r, requestID) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.writeAutomationJSON(w, http.StatusOK, requestID, a.listAutomationSessions(), nil)
	case http.MethodPost:
		var req automationCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			a.writeAutomationJSON(w, http.StatusBadRequest, requestID, nil, &automationErrorPayload{
				Code:    "invalid_request",
				Message: "请求体不是有效的 JSON",
			})
			return
		}
		session, err := a.StartAutomationSession(req.ProfileID, req.StartURL)
		if err != nil {
			a.writeAutomationJSON(w, http.StatusBadRequest, requestID, nil, &automationErrorPayload{
				Code:    "start_failed",
				Message: err.Error(),
			})
			return
		}
		a.writeAutomationJSON(w, http.StatusOK, requestID, session, nil)
	default:
		a.writeAutomationJSON(w, http.StatusMethodNotAllowed, requestID, nil, &automationErrorPayload{
			Code:    "method_not_allowed",
			Message: "仅支持 GET 或 POST",
		})
	}
}

func (a *App) handleAutomationSessionByID(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	if !a.requireAutomationAuth(w, r, requestID) {
		return
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/api/v1/automation/sessions/")
	if sessionID == "" {
		a.writeAutomationJSON(w, http.StatusBadRequest, requestID, nil, &automationErrorPayload{
			Code:    "invalid_request",
			Message: "缺少 session_id",
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.automationMu.RLock()
		session, ok := a.automationSessions[sessionID]
		var snapshot AutomationSession
		if ok {
			snapshot = *session
		}
		a.automationMu.RUnlock()
		if !ok {
			a.writeAutomationJSON(w, http.StatusNotFound, requestID, nil, &automationErrorPayload{
				Code:    "not_found",
				Message: "自动化会话不存在",
			})
			return
		}
		a.writeAutomationJSON(w, http.StatusOK, requestID, snapshot, nil)
	case http.MethodDelete:
		if err := a.StopAutomationSession(sessionID); err != nil {
			a.writeAutomationJSON(w, http.StatusBadRequest, requestID, nil, &automationErrorPayload{
				Code:    "stop_failed",
				Message: err.Error(),
			})
			return
		}
		a.writeAutomationJSON(w, http.StatusOK, requestID, map[string]string{"session_id": sessionID, "status": "stopping"}, nil)
	default:
		a.writeAutomationJSON(w, http.StatusMethodNotAllowed, requestID, nil, &automationErrorPayload{
			Code:    "method_not_allowed",
			Message: "仅支持 GET 或 DELETE",
		})
	}
}

func (a *App) handleAutomationRotateToken(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	if r.Method != http.MethodPost {
		a.writeAutomationJSON(w, http.StatusMethodNotAllowed, requestID, nil, &automationErrorPayload{
			Code:    "method_not_allowed",
			Message: "仅支持 POST",
		})
		return
	}
	if !a.requireAutomationAuth(w, r, requestID) {
		return
	}

	token, err := a.RotateAutomationToken()
	if err != nil {
		a.writeAutomationJSON(w, http.StatusInternalServerError, requestID, nil, &automationErrorPayload{
			Code:    "rotate_failed",
			Message: err.Error(),
		})
		return
	}
	a.writeAutomationJSON(w, http.StatusOK, requestID, map[string]string{"token": token}, nil)
}

// GetStartupURL 返回程序启动时携带的 URL 参数
func (a *App) GetStartupURL() string {
	return a.StartupURL
}

// getCamoufoxPath 尝试获取 Camoufox 执行文件路径
func (a *App) getCamoufoxPath() (string, error) {
	// 获取程序自身路径和当前工作目录
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	workingDir, _ := os.Getwd()

	// 搜索路径优先级:
	// 1. 程序所在目录
	// 2. 程序所在目录的父目录 (开发环境下 build/bin 的上一级)
	// 3. 程序所在目录的父目录的父目录
	// 4. 当前工作目录
	searchRoots := []string{
		exeDir,
		filepath.Dir(exeDir),
		filepath.Dir(filepath.Dir(exeDir)),
		workingDir,
	}

	for _, root := range searchRoots {
		// 检查原始目录和版本号目录
		targets := []string{
			filepath.Join(root, "camoufox.exe"),
			filepath.Join(root, "camoufox", "camoufox.exe"),
			filepath.Join(root, "camoufox-135.0.1-beta.24-win.x86_64", "camoufox.exe"),
		}
		for _, target := range targets {
			if _, err := os.Stat(target); err == nil {
				return target, nil
			}
		}
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	searchPath := filepath.Join(localAppData, "camoufox", "camoufox", "Cache")
	var foundPath string
	filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == "camoufox.exe" {
			foundPath = path
			return fmt.Errorf("found")
		}
		return nil
	})

	if foundPath != "" {
		return foundPath, nil
	}
	return "", fmt.Errorf("未找到 camoufox.exe")
}

// setupStealthPrefs 配置 Firefox 的代理与防检测首选项
func (a *App) setupStealthPrefs(userDataDir, proxyStr string) error {
	prefsPath := filepath.Join(userDataDir, "prefs.js")

	// 读取已有的 prefs.js，避免破坏用户其他自定义首选项
	var existingContent string
	if _, err := os.Stat(prefsPath); err == nil {
		if data, readErr := os.ReadFile(prefsPath); readErr == nil {
			existingContent = string(data)
		}
	}

	// 准备要追加或覆写的 prefs 列表
	prefsMap := map[string]string{
		"dom.webdriver.enabled":             "false", // 隐藏 navigator.webdriver
		"marionette.enabled":                "false", // 屏蔽 Marionette 反检测痕迹
		"media.peerconnection.enabled":       "true",  // 启用 WebRTC
		"media.navigator.enabled":           "true",
		"privacy.resistFingerprinting":      "false", // 避免干扰自定义指纹
		"devtools.debugger.remote-enabled":  "false",

		// 用户脚本引擎所需。均为 about:config 层面配置，页面 JS 无法读取，不构成指纹泄漏。
		"extensions.autoDisableScopes":  "0",     // 关键：否则 profile 内的扩展会被静默禁用
		"extensions.enabledScopes":      "5",     // SCOPE_PROFILE|SCOPE_APPLICATION，与 camoufox.cfg 一致
		"xpinstall.signatures.required": "false", // 允许自建的未签名引擎（Camoufox 默认已关闭强制签名）
	}

	// 解析代理配置
	if proxyStr != "" {
		var proxyType int = 1
		var httpHost, httpPort string
		var sslHost, sslPort string
		var socksHost, socksPort string
		var socksVersion int = 5

		tempProxy := proxyStr
		if strings.Contains(tempProxy, "://") {
			parts := strings.Split(tempProxy, "://")
			protocol := parts[0]
			addr := parts[1]

			hostPort := strings.Split(addr, ":")
			if len(hostPort) == 2 {
				if protocol == "http" || protocol == "https" {
					httpHost, httpPort = hostPort[0], hostPort[1]
					sslHost, sslPort = hostPort[0], hostPort[1]
				} else if strings.Contains(protocol, "socks") {
					socksHost, socksPort = hostPort[0], hostPort[1]
				}
			}
		} else {
			hostPort := strings.Split(tempProxy, ":")
			if len(hostPort) == 2 {
				httpHost, httpPort = hostPort[0], hostPort[1]
				sslHost, sslPort = hostPort[0], hostPort[1]
			}
		}

		prefsMap["network.proxy.type"] = fmt.Sprintf("%d", proxyType)
		if httpHost != "" && httpPort != "" {
			prefsMap["network.proxy.http"] = fmt.Sprintf(`"%s"`, httpHost)
			prefsMap["network.proxy.http_port"] = httpPort
			prefsMap["network.proxy.ssl"] = fmt.Sprintf(`"%s"`, sslHost)
			prefsMap["network.proxy.ssl_port"] = sslPort
		}
		if socksHost != "" && socksPort != "" {
			prefsMap["network.proxy.socks"] = fmt.Sprintf(`"%s"`, socksHost)
			prefsMap["network.proxy.socks_port"] = socksPort
			prefsMap["network.proxy.socks_version"] = fmt.Sprintf("%d", socksVersion)
		}
		prefsMap["network.proxy.socks_remote_dns"] = "true"
		prefsMap["network.proxy.share_proxy_settings"] = "true"
	}

	// 重构 prefs.js 的内容
	lines := strings.Split(existingContent, "\n")
	newLines := make([]string, 0, len(lines)+len(prefsMap))

	// 过滤掉已有冲突的 key 并在后面重新写入
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 检查这行是否包含我们要设置的 key
		matched := false
		for key := range prefsMap {
			if strings.Contains(trimmed, fmt.Sprintf(`"%s"`, key)) {
				matched = true
				break
			}
		}
		if !matched {
			newLines = append(newLines, line)
		}
	}

	// 写入新的配置
	for key, val := range prefsMap {
		newLines = append(newLines, fmt.Sprintf(`user_pref("%s", %s);`, key, val))
	}

	output := strings.Join(newLines, "\n") + "\n"
	return os.WriteFile(prefsPath, []byte(output), 0644)
}

// generateFingerprintConfig 为指定环境生成随机且唯一的指纹配置
func (a *App) generateFingerprintConfig(profile BrowserProfile) map[string]interface{} {
	config := make(map[string]interface{})

	// 1. 基于 profile.ID 建立哈希随机种子，确保环境指纹一致固定
	seed := hashStringToInt64(profile.ID)
	r := mrand.New(mrand.NewSource(seed))


	// 2. 匹配操作系统预设
	platform := profile.Platform
	if platform != "Windows" && platform != "macOS" && platform != "Linux" {
		platform = "Windows"
	}
	preset := HardwarePresets[platform]

	// 3. 随机选择 GPU 与分辨率
	gpu := preset.GPUs[r.Intn(len(preset.GPUs))]
	res := preset.Resolutions[r.Intn(len(preset.Resolutions))]

	// 4. 随机但一致的 Canvas aaOffset (偏移值在 10 ~ 30)
	canvasOffset := r.Intn(20) + 10

	config["navigator.userAgent"] = profile.UA
	config["navigator.platform"] = getNativePlatformString(platform)
	config["navigator.language"] = "zh-CN"
	config["navigator.languages"] = []string{"zh-CN", "zh", "en-US", "en"}

	// WebGL 混淆
	config["webGl:vendor"] = gpu.Vendor
	config["webGl:renderer"] = gpu.Renderer

	// Canvas 噪音
	config["canvas:aaOffset"] = canvasOffset

	// 屏幕分辨率
	config["screen.width"] = res.Width
	config["screen.height"] = res.Height

	config["timezone"] = "Asia/Shanghai"
	config["locale:all"] = "zh-CN"

	return config
}

// injectCamouConfig 将配置 JSON 分片注入环境变量
func (a *App) injectCamouConfig(config map[string]interface{}) {
	data, _ := json.Marshal(config)
	configStr := string(data)

	// Windows 环境变量大小限制约 2047-8191 字符，这里保守使用 2000
	chunkSize := 2000
	for i := 0; i < len(configStr); i += chunkSize {
		end := i + chunkSize
		if end > len(configStr) {
			end = len(configStr)
		}
		envName := fmt.Sprintf("CAMOU_CONFIG_%d", (i/chunkSize)+1)
		os.Setenv(envName, configStr[i:end])
	}
}

// setupCookies 物理注入 Cookie 到 Firefox 的 cookies.sqlite
func (a *App) setupCookies(userDataDir, cookieJSON string) error {
	if cookieJSON == "" || cookieJSON == "[]" {
		return nil
	}

	dbPath := filepath.Join(userDataDir, "cookies.sqlite")

	// 清理可能遗留的 WAL 缓存文件，确保写入生效
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	var cookies []map[string]interface{}
	if err := json.Unmarshal([]byte(cookieJSON), &cookies); err != nil {
		return fmt.Errorf("解析 Cookie JSON 失败: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("打开 Cookie 数据库失败: %v", err)
	}
	defer db.Close()

	// Firefox 要求使用 WAL 模式
	db.Exec("PRAGMA journal_mode=WAL;")

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS moz_cookies (id INTEGER PRIMARY KEY, originAttributes TEXT NOT NULL DEFAULT '', name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, lastAccessed INTEGER, creationTime INTEGER, isSecure INTEGER, isHttpOnly INTEGER, inBrowserElement INTEGER DEFAULT 0, sameSite INTEGER DEFAULT 0, rawSameSite INTEGER DEFAULT 0, CONSTRAINT moz_uniqueid UNIQUE (name, host, path, originAttributes))`)
	if err != nil {
		return fmt.Errorf("创建/检查表失败: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO moz_cookies (name, value, host, path, expiry, isSecure, isHttpOnly) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("准备 SQL 语句失败: %v", err)
	}
	defer stmt.Close()

	for _, c := range cookies {
		name := ""
		if v, ok := c["name"]; ok && v != nil {
			name = fmt.Sprint(v)
		} else if v, ok := c["key"]; ok && v != nil {
			name = fmt.Sprint(v)
		}

		value := ""
		if v, ok := c["value"]; ok && v != nil {
			value = fmt.Sprint(v)
		}

		host := ""
		if v, ok := c["domain"]; ok && v != nil {
			host = fmt.Sprint(v)
		} else if v, ok := c["host"]; ok && v != nil {
			host = fmt.Sprint(v)
		}

		path := "/"
		if v, ok := c["path"]; ok && v != nil {
			path = fmt.Sprint(v)
		}

		expiry := int64(0)
		if v, ok := c["expirationDate"].(float64); ok {
			expiry = int64(v)
		} else if v, ok := c["expiry"].(float64); ok {
			expiry = int64(v)
		}

		secure := 0
		if v, ok := c["secure"].(bool); ok && v {
			secure = 1
		}

		httponly := 0
		if v, ok := c["httpOnly"].(bool); ok && v {
			httponly = 1
		}

		if name == "" || host == "" {
			continue
		}

		_, err = stmt.Exec(name, value, host, path, expiry, secure, httponly)
		if err != nil {
			a.Log("warn", fmt.Sprintf("注入 Cookie [%s] 失败: %v", name, err))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 Cookie 数据失败: %v", err)
	}

	a.Log("info", "Cookie 注入成功")
	return nil
}

// userScriptExtID 是脚本引擎扩展的固定 ID。
// 页面无法据此探测扩展：Gecko 为每个 profile 分配随机 moz-extension UUID，
// 实测按 ID 请求扩展资源一律被拦截。
const userScriptExtID = "userscript-engine@mybrowser.local"

// resolveEnabledScripts 返回本环境实际生效的脚本：全局启用 ∩ 环境启用 ∩ 具备合法匹配规则。
func (a *App) resolveEnabledScripts(profile BrowserProfile) []UserScript {
	if len(profile.EnabledScripts) == 0 {
		return nil
	}

	wanted := make(map[string]bool, len(profile.EnabledScripts))
	for _, id := range profile.EnabledScripts {
		wanted[id] = true
	}

	result := make([]UserScript, 0, len(profile.EnabledScripts))
	for _, s := range a.userScripts {
		if !s.Enabled || !wanted[s.ID] {
			continue
		}
		// 无匹配规则的脚本一律跳过，避免意外全站注入扩大暴露面
		if len(s.Matches) == 0 {
			a.Log("warn", fmt.Sprintf("脚本 [%s] 无匹配规则，已跳过", s.Name))
			continue
		}
		result = append(result, s)
	}
	return result
}

// setupUserScripts 按环境启用清单生成用户脚本引擎扩展。
//
// 未启用任何脚本时不落地任何文件，使浏览器暴露面与未引入本功能时完全一致。
// 每次启动前重建扩展目录，保证脚本改动立即生效且禁用后不留残迹。
func (a *App) setupUserScripts(userDataDir string, profile BrowserProfile) error {
	extDir := filepath.Join(userDataDir, "extensions", userScriptExtID)

	if err := os.RemoveAll(extDir); err != nil {
		return fmt.Errorf("清理旧脚本扩展失败: %w", err)
	}

	scripts := a.resolveEnabledScripts(profile)
	if len(scripts) == 0 {
		return nil
	}

	if err := os.MkdirAll(extDir, 0755); err != nil {
		return fmt.Errorf("创建脚本扩展目录失败: %w", err)
	}

	contentScripts := make([]map[string]interface{}, 0, len(scripts))
	for _, s := range scripts {
		source, err := os.ReadFile(a.getUserScriptSourcePath(s.ID))
		if err != nil {
			a.Log("warn", fmt.Sprintf("脚本 [%s] 源文件读取失败，已跳过: %v", s.Name, err))
			continue
		}

		// 二次校验：索引可能被手工编辑过，非法模式会导致整个扩展被拒绝加载
		validMatches := make([]string, 0, len(s.Matches))
		for _, m := range s.Matches {
			if isValidMatchPattern(m) {
				validMatches = append(validMatches, m)
			} else {
				a.Log("warn", fmt.Sprintf("脚本 [%s] 的匹配规则 %q 非法，已忽略", s.Name, m))
			}
		}
		if len(validMatches) == 0 {
			continue
		}

		// @require 的库必须在主脚本之前执行，且顺序与声明一致
		requireSources, reqErr := a.readScriptRequires(s)
		if reqErr != nil {
			a.Log("warn", fmt.Sprintf("脚本 [%s] 的依赖缺失，已跳过: %v", s.Name, reqErr))
			continue
		}

		jsFiles := make([]string, 0, len(requireSources)+1)
		if s.World == "page" {
			// 主世界模式下 content_scripts 里的文件仍在沙箱执行，
			// 依赖必须与主脚本拼在一起注入，否则主脚本取不到这些库
			combined := strings.Join(append(requireSources, string(source)), "\n;\n")
			source = []byte(wrapForPageWorld(combined))
		} else {
			for i, dep := range requireSources {
				depName := fmt.Sprintf("req_%s_%02d.js", s.ID, i)
				if err := os.WriteFile(filepath.Join(extDir, depName), []byte(dep), 0644); err != nil {
					return fmt.Errorf("写入脚本 [%s] 的依赖失败: %w", s.Name, err)
				}
				jsFiles = append(jsFiles, depName)
			}
		}

		fileName := "us_" + s.ID + ".js"
		if err := os.WriteFile(filepath.Join(extDir, fileName), source, 0644); err != nil {
			return fmt.Errorf("写入脚本 [%s] 失败: %w", s.Name, err)
		}
		jsFiles = append(jsFiles, fileName)

		contentScripts = append(contentScripts, map[string]interface{}{
			"matches":    validMatches,
			"js":         jsFiles,
			"run_at":     s.RunAt,
			"all_frames": false,
		})
	}

	// 全部脚本都不可用时回退到零暴露面，而不是留下一个空壳扩展
	if len(contentScripts) == 0 {
		return os.RemoveAll(extDir)
	}

	manifest := map[string]interface{}{
		"manifest_version": 2,
		"name":             "Engine",
		"version":          "1.0",
		"browser_specific_settings": map[string]interface{}{
			"gecko": map[string]string{"id": userScriptExtID},
		},
		"content_scripts": contentScripts,
		// 刻意不声明 web_accessible_resources：该字段是扩展被页面枚举的主要途径，
		// 按最小暴露面原则省略。
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644); err != nil {
		return err
	}

	a.Log("info", fmt.Sprintf("已为环境 [%s] 注入 %d 个用户脚本", profile.Name, len(contentScripts)))
	return nil
}

// readScriptRequires 按声明顺序读出该脚本所有 @require 依赖的本地缓存内容。
//
// 只要有一个依赖缺失就返回错误：宁可整个脚本不注入，也不注入一个注定报错的半成品。
func (a *App) readScriptRequires(script UserScript) ([]string, error) {
	if len(script.Requires) == 0 {
		return nil, nil
	}
	if len(script.RequireAssets) != len(script.Requires) {
		return nil, fmt.Errorf("声明了 %d 个 @require，仅 %d 个下载成功",
			len(script.Requires), len(script.RequireAssets))
	}

	depsDir := a.getUserScriptDepsDir(script.ID)
	sources := make([]string, 0, len(script.RequireAssets))
	for _, asset := range script.RequireAssets {
		data, err := os.ReadFile(filepath.Join(depsDir, asset.File))
		if err != nil {
			return nil, fmt.Errorf("依赖 %s 读取失败: %w", asset.URL, err)
		}
		sources = append(sources, string(data))
	}
	return sources, nil
}

// wrapForPageWorld 将脚本注入页面主世界，使其可访问页面自身的 JS 变量。
//
// 该模式会在 window 上留下可被页面枚举的痕迹（实测泄漏 3 项探针），
// 仅在脚本确实需要读写页面 JS 变量时使用。
func wrapForPageWorld(source string) string {
	// 借 JSON 编码将源码安全转义为 JS 字符串字面量，避免引号与换行破坏语法
	payload, err := json.Marshal(source)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(`(function(){try{
var s=document.createElement('script');
s.textContent=%s;
(document.head||document.documentElement).appendChild(s);
s.remove();
}catch(e){}})();`, payload)
}

// SyncCookies 从浏览器的物理数据库中提取 Cookie 并同步到配置文件
func (a *App) SyncCookies(profileID string) error {
	var profile *BrowserProfile
	var profileIdx int
	found := false
	for i, p := range a.profiles {
		if p.ID == profileID {
			profile = &a.profiles[i]
			profileIdx = i
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("环境不存在")
	}

	userDataDir := filepath.Join(a.getDataDir(), "profiles", profileID)
	// Firefox 的 Cookie 可能会写入 WAL，提取前我们需要将其刷入或直接连入
	dbPath := filepath.Join(userDataDir, "cookies.sqlite")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("尚未生成 Cookie 数据库，请先启动浏览器并登录")
	}

	// 使用 readonly 模式连接
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("无法关联 Cookie 数据库: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, value, host, path, expiry, isSecure, isHttpOnly FROM moz_cookies`)
	if err != nil {
		return fmt.Errorf("提权查询 Cookie 失败（如果浏览器未完全关闭且锁占，可能无法同步）: %v", err)
	}
	defer rows.Close()

	var cookies []map[string]interface{}
	for rows.Next() {
		var name, value, host, path sql.NullString
		var expiry, isSecure, isHttpOnly sql.NullInt64
		if err := rows.Scan(&name, &value, &host, &path, &expiry, &isSecure, &isHttpOnly); err != nil {
			continue
		}
		cookie := map[string]interface{}{
			"name":           name.String,
			"value":          value.String,
			"domain":         host.String,
			"path":           path.String,
			"expirationDate": expiry.Int64,
			"secure":         isSecure.Int64 == 1,
			"httpOnly":       isHttpOnly.Int64 == 1,
		}
		cookies = append(cookies, cookie)
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("遍历 Cookie 数据失败: %v", err)
	}

	// 防止写入为 "null"
	if len(cookies) == 0 {
		profile.Cookies = "[]"
	} else {
		data, err := json.Marshal(cookies)
		if err != nil {
			return fmt.Errorf("格式化 Cookie JSON 失败: %v", err)
		}
		profile.Cookies = string(data)
	}

	a.profiles[profileIdx] = *profile
	a.Log("info", fmt.Sprintf("✅ 同步环境 [%s] 状态存档成功", profile.Name))
	return a.saveProfiles()
}

// ResetCookies 重置指定的环境的 Cookie 记录并物理删除数据库文件
func (a *App) ResetCookies(profileID string) error {
	var profileIdx int
	found := false
	for i, p := range a.profiles {
		if p.ID == profileID {
			a.profiles[i].Cookies = "[]"
			profileIdx = i
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("环境不存在")
	}

	// 物理删除 cookies.sqlite 文件
	userDataDir := filepath.Join(a.getDataDir(), "profiles", profileID)
	dbPath := filepath.Join(userDataDir, "cookies.sqlite")
	if _, err := os.Stat(dbPath); err == nil {
		err := os.Remove(dbPath)
		if err != nil {
			a.Log("error", fmt.Sprintf("清空物理 Cookie 失败: %v", err))
			return fmt.Errorf("物理文件删除失败（请确认浏览器已关闭）: %v", err)
		}
	}

	// 同时清理 sessionstore 等可能包含状态的文件
	_ = profileIdx // 保持变量以匹配 SyncCookies 逻辑风格，或直接移除
	sessionPath := filepath.Join(userDataDir, "sessionstore-backups")
	os.RemoveAll(sessionPath)

	a.Log("info", fmt.Sprintf("重置环境 [%s] 成功，已清空所有登录状态", a.profiles[profileIdx].Name))
	return a.saveProfiles()
}

// TestProxy 验证代理连通性
func (a *App) TestProxy(proxyStr string) (string, error) {
	if proxyStr == "" {
		return "直连", nil
	}

	u, err := url.Parse(proxyStr)
	if err != nil {
		return "", fmt.Errorf("格式非法: %v", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	if u.Scheme == "http" || u.Scheme == "https" {
		proxyURL, _ := url.Parse(proxyStr)
		client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	} else if strings.Contains(u.Scheme, "socks") {
		// 这里简单处理，Camoufox 本身支持 socks，Go 测试连通性也可以使用类似逻辑
		// 为保持代码轻量，此处仅验证格式，或尝试建立基础 TCP 连接
		return "SOCKS 代理格式有效，连通性请在启动后验证", nil
	}

	resp, err := client.Get("https://www.google.com")
	if err != nil {
		return "", fmt.Errorf("无法连接: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return "连接成功", nil
	}
	return fmt.Sprintf("状态码: %d", resp.StatusCode), nil
}

func (a *App) getProfileByID(profileID string) (BrowserProfile, error) {
	for _, p := range a.profiles {
		if p.ID == profileID {
			return p, nil
		}
	}
	return BrowserProfile{}, fmt.Errorf("环境不存在")
}

func (a *App) ensureUniqueProfileName(baseName string) string {
	if strings.TrimSpace(baseName) == "" {
		baseName = "导入环境"
	}

	newName := baseName
	counter := 1
	for {
		exists := false
		for _, profile := range a.profiles {
			if profile.Name == newName {
				exists = true
				break
			}
		}
		if !exists {
			return newName
		}
		newName = fmt.Sprintf("%s (%d)", baseName, counter)
		counter++
	}
}

func safeJoinProfileDataPath(baseDir, archivePath string) (string, error) {
	cleaned := filepath.Clean(strings.TrimPrefix(archivePath, "data/"))
	if cleaned == "." || cleaned == "" {
		return "", nil
	}

	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("环境包包含非法路径: %s", archivePath)
	}

	targetPath := filepath.Join(baseDir, cleaned)
	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("环境包包含越界路径: %s", archivePath)
	}

	return targetPath, nil
}

func (a *App) exportProfileBundle(profile BrowserProfile, targetPath string) error {
	newZipFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer newZipFile.Close()

	zipWriter := zip.NewWriter(newZipFile)
	defer zipWriter.Close()

	metaData, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}

	metadataFile, err := zipWriter.Create("metadata.json")
	if err != nil {
		return err
	}
	if _, err := metadataFile.Write(metaData); err != nil {
		return err
	}

	// 脚本正文存放在 profile 目录之外，需单独打包，否则导入端只会得到一份空的启用清单
	if err := a.writeUserScriptsToBundle(zipWriter, profile); err != nil {
		return err
	}

	userDataDir := filepath.Join(a.getDataDir(), "profiles", profile.ID)
	if _, err := os.Stat(userDataDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	return filepath.Walk(userDataDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			// extensions/ 每次启动都按启用清单重建，打包进去只会带来体积膨胀与陈旧内容
			if info.Name() == "extensions" && filepath.Dir(path) == userDataDir {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(userDataDir, path)
		if err != nil {
			return err
		}

		archiveFile, err := zipWriter.Create(filepath.ToSlash(filepath.Join("data", relPath)))
		if err != nil {
			return err
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		_, err = io.Copy(archiveFile, srcFile)
		return err
	})
}

// writeUserScriptsToBundle 把本环境引用到的用户脚本写入环境包。
//
// 打包的是"被引用"而非"实际生效"的脚本：全局停用的脚本同样随包带走，
// 否则在目标机器上重新启用后会发现脚本不存在。
func (a *App) writeUserScriptsToBundle(zipWriter *zip.Writer, profile BrowserProfile) error {
	if len(profile.EnabledScripts) == 0 {
		return nil
	}

	wanted := make(map[string]bool, len(profile.EnabledScripts))
	for _, id := range profile.EnabledScripts {
		wanted[id] = true
	}

	bundled := make([]UserScript, 0, len(profile.EnabledScripts))
	for _, s := range a.userScripts {
		if !wanted[s.ID] {
			continue
		}

		source, err := os.ReadFile(a.getUserScriptSourcePath(s.ID))
		if err != nil {
			a.Log("warn", fmt.Sprintf("脚本 [%s] 源文件缺失，未随环境包导出: %v", s.Name, err))
			continue
		}

		writer, err := zipWriter.Create("scripts/" + s.ID + ".user.js")
		if err != nil {
			return err
		}
		if _, err := writer.Write(source); err != nil {
			return err
		}
		bundled = append(bundled, s)
	}

	if len(bundled) == 0 {
		return nil
	}

	index, err := json.MarshalIndent(bundled, "", "  ")
	if err != nil {
		return err
	}
	writer, err := zipWriter.Create("scripts/index.json")
	if err != nil {
		return err
	}
	_, err = writer.Write(index)
	return err
}

// restoreBundledUserScripts 还原环境包内的用户脚本，返回旧 ID 到新 ID 的映射。
//
// 导入的脚本一律保持全局停用：环境包可能来自他人，其中的脚本会在匹配站点上
// 执行任意代码，需由用户确认内容后再显式启用。
func (a *App) restoreBundledUserScripts(zipReader *zip.Reader) (map[string]string, error) {
	var index []UserScript
	sources := make(map[string][]byte)

	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() || !strings.HasPrefix(file.Name, "scripts/") {
			continue
		}

		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, err
		}

		if file.Name == "scripts/index.json" {
			if err := json.Unmarshal(content, &index); err != nil {
				return nil, fmt.Errorf("环境包脚本索引损坏: %w", err)
			}
			continue
		}
		if strings.HasSuffix(file.Name, ".user.js") {
			id := strings.TrimSuffix(strings.TrimPrefix(file.Name, "scripts/"), ".user.js")
			sources[id] = content
		}
	}

	if len(index) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(a.getUserScriptSourceDir(), 0755); err != nil {
		return nil, fmt.Errorf("创建脚本目录失败: %w", err)
	}

	// 按正文建索引，重复导入同一个环境包时复用已有脚本而不是产生副本
	existingByContent := make(map[string]string, len(a.userScripts))
	for _, s := range a.userScripts {
		if data, err := os.ReadFile(a.getUserScriptSourcePath(s.ID)); err == nil {
			existingByContent[string(data)] = s.ID
		}
	}

	idMap := make(map[string]string, len(index))
	imported := 0
	needsAssets := 0

	for _, s := range index {
		source, ok := sources[s.ID]
		if !ok {
			a.Log("warn", fmt.Sprintf("环境包内脚本 [%s] 缺少正文，已跳过", s.Name))
			continue
		}

		if existingID, duplicated := existingByContent[string(source)]; duplicated {
			idMap[s.ID] = existingID
			continue
		}

		newID := uuid.New().String()
		if err := os.WriteFile(a.getUserScriptSourcePath(newID), source, 0644); err != nil {
			return nil, fmt.Errorf("还原脚本 [%s] 失败: %w", s.Name, err)
		}

		restored := s
		restored.ID = newID
		restored.Enabled = false
		restored.UpdatedAt = time.Now().Unix()
		// 依赖缓存不随环境包携带（都是可重新获取的公共 CDN 资源），
		// 清空记录后由界面提示用户重新下载，而不是留下指向不存在文件的记录
		restored.RequireAssets = nil
		restored.ResourceAssets = nil

		a.userScripts = append(a.userScripts, restored)
		existingByContent[string(source)] = newID
		idMap[s.ID] = newID
		imported++
		if len(restored.Requires) > 0 || len(restored.Resources) > 0 {
			needsAssets++
		}
	}

	if imported > 0 {
		if err := a.saveUserScripts(); err != nil {
			return nil, err
		}
		a.Log("info", fmt.Sprintf(
			"环境包内含 %d 个用户脚本，已导入并保持停用，请确认内容后再在脚本页启用", imported))
		if needsAssets > 0 {
			a.Log("warn", fmt.Sprintf(
				"其中 %d 个脚本有外部依赖，需在脚本页点击「重新下载依赖」后才能使用", needsAssets))
		}
	}

	return idMap, nil
}

// remapImportedScriptIDs 将环境包内的旧脚本 ID 映射为本机的新 ID。
// 未能还原的脚本会被丢弃，避免在启用清单里留下悬空引用。
func remapImportedScriptIDs(ids []string, idMap map[string]string) []string {
	if len(ids) == 0 {
		return nil
	}

	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if newID, ok := idMap[id]; ok {
			result = append(result, newID)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (a *App) importProfileBundle(sourcePath string) (BrowserProfile, error) {
	zipReader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return BrowserProfile{}, err
	}
	defer zipReader.Close()

	var (
		profile       BrowserProfile
		metadataFound bool
	)

	for _, file := range zipReader.File {
		if file.Name != "metadata.json" {
			continue
		}

		reader, err := file.Open()
		if err != nil {
			return BrowserProfile{}, err
		}

		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return BrowserProfile{}, err
		}
		if err := json.Unmarshal(content, &profile); err != nil {
			return BrowserProfile{}, fmt.Errorf("环境包元数据损坏: %w", err)
		}

		metadataFound = true
		break
	}

	if !metadataFound || profile.ID == "" {
		return BrowserProfile{}, fmt.Errorf("无效的环境包")
	}

	profile, err = normalizeImportedProfile(profile)
	if err != nil {
		return BrowserProfile{}, err
	}

	profile.ID = uuid.New().String()
	profile.Name = a.ensureUniqueProfileName(profile.Name)
	profile.CreateAt = time.Now().Unix()

	// 还原随包携带的用户脚本，并把启用清单里的旧 ID 换成本机新 ID
	scriptIDMap, err := a.restoreBundledUserScripts(&zipReader.Reader)
	if err != nil {
		return BrowserProfile{}, err
	}
	profile.EnabledScripts = remapImportedScriptIDs(profile.EnabledScripts, scriptIDMap)

	newUserDataDir := filepath.Join(a.getDataDir(), "profiles", profile.ID)
	if err := os.MkdirAll(newUserDataDir, 0755); err != nil {
		return BrowserProfile{}, err
	}

	for _, file := range zipReader.File {
		if !strings.HasPrefix(file.Name, "data/") {
			continue
		}

		targetPath, err := safeJoinProfileDataPath(newUserDataDir, file.Name)
		if err != nil {
			return BrowserProfile{}, err
		}
		if targetPath == "" {
			continue
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return BrowserProfile{}, err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return BrowserProfile{}, err
		}

		srcFile, err := file.Open()
		if err != nil {
			return BrowserProfile{}, err
		}

		dstFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			srcFile.Close()
			return BrowserProfile{}, err
		}

		_, copyErr := io.Copy(dstFile, srcFile)
		closeErr := dstFile.Close()
		srcFile.Close()
		if copyErr != nil {
			return BrowserProfile{}, copyErr
		}
		if closeErr != nil {
			return BrowserProfile{}, closeErr
		}
	}

	a.profiles = append(a.profiles, profile)
	if err := a.saveProfiles(); err != nil {
		return BrowserProfile{}, err
	}

	return profile, nil
}

func (a *App) prepareProfileLaunch(profile BrowserProfile) (string, string, error) {
	exePath, err := a.getCamoufoxPath()
	if err != nil {
		return "", "", err
	}

	userDataDir := filepath.Join(a.getDataDir(), "profiles", profile.ID)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return "", "", err
	}

	if err := a.setupStealthPrefs(userDataDir, profile.Proxy); err != nil {
		fmt.Printf("配置首选项与代理失败: %v\n", err)
	}

	if err := a.setupCookies(userDataDir, profile.Cookies); err != nil {
		fmt.Printf("注入 Cookie 失败: %v\n", err)
	}

	// 按本环境启用清单生成用户脚本扩展；未启用脚本时不产生任何文件
	if err := a.setupUserScripts(userDataDir, profile); err != nil {
		a.Log("warn", fmt.Sprintf("用户脚本注入失败: %v", err))
	}

	return exePath, userDataDir, nil
}

func (a *App) buildCamoufoxEnv(profile BrowserProfile) []string {
	config := a.generateFingerprintConfig(profile)
	data, _ := json.Marshal(config)

	baseEnv := os.Environ()
	filtered := make([]string, 0, len(baseEnv)+8)
	for _, entry := range baseEnv {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "CAMOU_CONFIG_") || strings.HasPrefix(upper, "CAMOU_UA=") {
			continue
		}
		filtered = append(filtered, entry)
	}

	configStr := string(data)
	chunkSize := 2000
	for i := 0; i < len(configStr); i += chunkSize {
		end := i + chunkSize
		if end > len(configStr) {
			end = len(configStr)
		}
		envName := fmt.Sprintf("CAMOU_CONFIG_%d", (i/chunkSize)+1)
		filtered = append(filtered, envName+"="+configStr[i:end])
	}

	filtered = append(filtered, "CAMOU_UA="+profile.UA)
	return filtered
}

func (a *App) buildBrowserArgs(userDataDir, startURL string, debugPort int) []string {
	args := []string{
		"--profile", userDataDir,
		"--no-remote",
	}

	if debugPort > 0 {
		args = append(args, "--remote-debugging-port", fmt.Sprintf("%d", debugPort))
	}

	if startURL != "" {
		args = append(args, startURL)
	}

	return args
}

func (a *App) monitorBrowserExit(cmd *exec.Cmd, profile BrowserProfile, sessionID string) {
	go func() {
		_ = cmd.Wait()

		if sessionID != "" {
			a.updateAutomationSession(sessionID, func(session *AutomationSession) {
				session.Status = "stopped"
			})
		}

		a.Log("info", fmt.Sprintf("环境 [%s] 已关闭，正在自动存档 Cookie 状态...", profile.Name))
		if err := a.SyncCookies(profile.ID); err != nil {
			a.Log("warn", fmt.Sprintf("自动存档失败: %v", err))
		}

		if sessionID != "" {
			a.automationMu.Lock()
			delete(a.automationSessions, sessionID)
			delete(a.automationRuntimes, sessionID)
			a.automationMu.Unlock()
		}
	}()
}

// LaunchBrowser 启动指定的浏览器环境
func (a *App) LaunchBrowser(profileID string, startURL string) error {
	profile, err := a.getProfileByID(profileID)
	if err != nil {
		return err
	}

	exePath, userDataDir, err := a.prepareProfileLaunch(profile)
	if err != nil {
		return err
	}

	if startURL == "" {
		startURL = profile.StartURL
	}

	cmd := exec.Command(exePath, a.buildBrowserArgs(userDataDir, startURL, 0)...)
	cmd.Env = a.buildCamoufoxEnv(profile)
	err = cmd.Start()
	if err != nil {
		a.Log("error", fmt.Sprintf("进程启动失败: %v", err))
	} else {
		a.Log("info", fmt.Sprintf("环境 [%s] 已成功启动 (PID: %d)", profile.Name, cmd.Process.Pid))
		a.monitorBrowserExit(cmd, profile, "")
	}
	return err
}

func (a *App) StartAutomationSession(profileID string, startURL string) (AutomationSession, error) {
	if !a.automationConfig.Enabled {
		return AutomationSession{}, fmt.Errorf("本地自动化 API 当前未启用")
	}

	normalizedStartURL, err := normalizeStartURL(startURL)
	if err != nil {
		return AutomationSession{}, err
	}

	profile, err := a.getProfileByID(profileID)
	if err != nil {
		return AutomationSession{}, err
	}

	targetURL := normalizedStartURL
	if targetURL == "" {
		targetURL = profile.StartURL
	}
	if targetURL != "" {
		targetURL, err = normalizeStartURL(targetURL)
		if err != nil {
			return AutomationSession{}, err
		}
	}

	session, reused, err := a.ensureAutomationSession(profile)
	if err != nil {
		return AutomationSession{}, err
	}

	if targetURL != "" {
		if err := a.navigateAutomationSession(session, targetURL); err != nil {
			a.updateAutomationSession(session.SessionID, func(current *AutomationSession) {
				current.LastError = err.Error()
			})
			return AutomationSession{}, fmt.Errorf("打开链接失败: %w", err)
		}
		a.updateAutomationSession(session.SessionID, func(current *AutomationSession) {
			current.StartURL = targetURL
			current.LastError = ""
		})
		if reused {
			a.Log("info", fmt.Sprintf("已复用自动化会话 [%s] 并打开链接: %s", session.SessionID, targetURL))
		} else {
			a.Log("info", fmt.Sprintf("自动化会话 [%s] 已完成首跳: %s", session.SessionID, targetURL))
		}
	}

	a.automationMu.RLock()
	snapshot := a.copyAutomationSession(a.automationSessions[session.SessionID])
	a.automationMu.RUnlock()
	return snapshot, nil
}

func (a *App) StopAutomationSession(sessionID string) error {
	a.automationMu.Lock()
	runtime, ok := a.automationRuntimes[sessionID]
	session := a.automationSessions[sessionID]
	if ok && session != nil {
		session.Status = "stopping"
	}
	a.automationMu.Unlock()

	if !ok || runtime == nil || runtime.cmd == nil || runtime.cmd.Process == nil {
		return fmt.Errorf("自动化会话不存在")
	}

	if err := runtime.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("关闭自动化会话失败: %v", err)
	}

	a.Log("info", fmt.Sprintf("已发送停止指令到自动化会话 [%s]", sessionID))
	return nil
}

// GetProfiles 获取所有环境列表
func (a *App) GetProfiles() []BrowserProfile {
	return a.profiles
}

// --- 导入导出迁移功能 ---

// ExportCookies 将指定环境的 Cookie 导出到文件
func (a *App) ExportCookies(profileID string) error {
	var profile *BrowserProfile
	for i, p := range a.profiles {
		if p.ID == profileID {
			profile = &a.profiles[i]
			break
		}
	}
	if profile == nil {
		return fmt.Errorf("环境不存在")
	}

	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出 Cookie 数据",
		DefaultFilename: fmt.Sprintf("cookies_%s.json", profile.Name),
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil || path == "" {
		return err
	}

	return os.WriteFile(path, []byte(profile.Cookies), 0644)
}

// ExportProfile 将整个环境打包为 MBP 迁移文件
func (a *App) ExportProfile(profileID string) error {
	profile, err := a.getProfileByID(profileID)
	if err != nil {
		return fmt.Errorf("环境不存在")
	}

	targetPath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出全量环境包",
		DefaultFilename: fmt.Sprintf("%s.mbp", profile.Name),
		Filters: []runtime.FileFilter{
			{DisplayName: "MyBrowser Profile (*.mbp)", Pattern: "*.mbp"},
		},
	})
	if err != nil || targetPath == "" {
		return err
	}

	if err := a.exportProfileBundle(profile, targetPath); err != nil {
		return err
	}

	a.Log("info", fmt.Sprintf("环境 [%s] 已成功打包导出到: %s", profile.Name, targetPath))
	return nil
}

// RegisterAsDefaultBrowser 将当前程序注册为 Windows 可识别的浏览器
func (a *App) RegisterAsDefaultBrowser() (string, error) {
	exePath, err := getExecutablePath()
	if err != nil {
		return "", fmt.Errorf("获取程序路径失败: %v", err)
	}

	// 检查是否在开发环境下（路径通常包含 wails-dev 或临时目录）
	isDev := strings.Contains(strings.ToLower(exePath), "wails-dev") || strings.Contains(strings.ToLower(exePath), "temp")
	if isDev {
		a.Log("warn", "检测到处于开发环境 (dev)，注册的路径可能是临时的。建议使用 'wails build' 正式编译后再注册。")
	}

	a.Log("info", fmt.Sprintf("正在注册浏览器路径: %s", exePath))

	exeName := filepath.Base(exePath)

	// 准备注册表项 (核心兼容版)
	commands := [][]string{
		// 1. 核心浏览器客户端注册 (StartMenuInternet)
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser", "/ve", "/d", "MyBrowser", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities", "/v", "ApplicationName", "/d", "MyBrowser", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities", "/v", "ApplicationIcon", "/d", exePath + ",0", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities", "/v", "ApplicationDescription", "/d", "MyBrowser Antidetect Browser", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\DefaultIcon", "/ve", "/d", exePath + ",0", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\shell\\open\\command", "/ve", "/d", "\"" + exePath + "\"", "/f"},

		// 2. 关联文件与 URL 协议
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities\\FileAssociations", "/v", ".htm", "/d", "MyBrowserURL", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities\\FileAssociations", "/v", ".html", "/d", "MyBrowserURL", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities\\URLAssociations", "/v", "http", "/d", "MyBrowserURL", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities\\URLAssociations", "/v", "https", "/d", "MyBrowserURL", "/f"},

		// 3. 定义 MyBrowserURL 协议处理类
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\MyBrowserURL", "/ve", "/d", "MyBrowser URL", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\MyBrowserURL", "/v", "FriendlyAppName", "/d", "MyBrowser", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\MyBrowserURL", "/v", "URL Protocol", "/d", "", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\MyBrowserURL\\shell\\open\\command", "/ve", "/d", "\"" + exePath + "\" \"%1\"", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\MyBrowserURL\\DefaultIcon", "/ve", "/d", exePath + ",0", "/f"},

		// 4. 应用级注册 (让系统设置能搜到)
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\RegisteredApplications", "/v", "MyBrowser", "/d", "Software\\Clients\\StartMenuInternet\\MyBrowser\\Capabilities", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\Applications\\" + exeName + "\\shell\\open\\command", "/ve", "/d", "\"" + exePath + "\" \"%1\"", "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Classes\\Applications\\" + exeName + "\\DefaultIcon", "/ve", "/d", exePath + ",0", "/f"},

		// 5. App Paths 注册 (第三方选择器定位用)
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\App Paths\\" + exeName, "/ve", "/d", exePath, "/f"},
		{"reg", "add", "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\App Paths\\" + exeName, "/v", "Path", "/d", filepath.Dir(exePath), "/f"},
	}

	for _, cmdArgs := range commands {
		if out, err := runHiddenCombinedCommand(cmdArgs[0], cmdArgs[1:]...); err != nil {
			a.Log("error", fmt.Sprintf("修改注册表失败 (可能被杀毒软件拦截): %v, 输出: %s", err, string(out)))
			return "", fmt.Errorf("修改注册表失败: %v", err)
		}
	}

	msg := "已成功将 MyBrowser 注册。请在 Windows 设置 -> 默认应用 -> 浏览器中选择 MyBrowser。"
	if isDev {
		msg += " (注意：当前为开发路径，建议编译后再执行)"
	}
	a.Log("info", msg)
	return msg, nil
}

// OpenDefaultAppsSettings 打开 Windows 默认应用设置页面
func (a *App) OpenDefaultAppsSettings() {
	// 使用 ms-settings 协议直接唤起设置页并定位到相关应用
	a.Log("info", "正在唤起 Windows 默认应用设置页面...")
	if err := startHiddenCommand("cmd", "/c", "start", "ms-settings:defaultapps"); err != nil {
		a.Log("warn", fmt.Sprintf("打开 Windows 默认应用设置失败: %v", err))
	}
}

// OpenDataDirectory 打开当前正在使用的数据目录
func (a *App) OpenDataDirectory() error {
	dataDir := a.getDataDir()
	if dataDir == "" {
		return fmt.Errorf("数据目录未初始化")
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("创建数据目录失败: %v", err)
	}

	// Use the shell to open Explorer so we can hide the transient cmd window
	// without hiding the actual file explorer window itself.
	if err := startHiddenCommand("cmd", "/c", "start", "", dataDir); err != nil {
		return fmt.Errorf("打开数据目录失败: %v", err)
	}

	a.Log("info", fmt.Sprintf("已打开数据目录: %s", dataDir))
	return nil
}

// GetStorageDirectory 返回当前实际使用的数据目录
func (a *App) GetStorageDirectory() string {
	return a.getDataDir()
}

// GetStorageMode 返回当前存储模式: localappdata 或 portable
func (a *App) GetStorageMode() string {
	return a.getStorageModeLabel()
}

func (a *App) GetAutomationInfo() AutomationInfo {
	return a.buildAutomationInfo()
}

func (a *App) GetAutomationSessions() []AutomationSession {
	return a.listAutomationSessions()
}

func (a *App) GetAutomationToken() string {
	return a.automationConfig.APIToken
}

func (a *App) SetAutomationEnabled(enabled bool) error {
	if enabled == a.automationConfig.Enabled {
		return nil
	}

	if !enabled && a.automationSessionCount() > 0 {
		return fmt.Errorf("请先停止当前自动化会话，再关闭自动化控制台")
	}

	a.automationConfig.Enabled = enabled

	if enabled {
		if err := a.startAutomationServer(); err != nil {
			a.automationConfig.Enabled = false
			_ = a.saveAutomationConfig()
			return fmt.Errorf("启用自动化控制台失败: %v", err)
		}
		if err := a.saveAutomationConfig(); err != nil {
			return err
		}
		a.Log("info", fmt.Sprintf("本地自动化控制台已启用: http://%s", a.automationListenAddr))
		return nil
	}

	if err := a.stopAutomationServer(); err != nil {
		a.automationConfig.Enabled = true
		_ = a.saveAutomationConfig()
		return fmt.Errorf("停用自动化控制台失败: %v", err)
	}
	if err := a.saveAutomationConfig(); err != nil {
		return err
	}
	a.Log("info", "本地自动化控制台已停用。")
	return nil
}

func (a *App) RotateAutomationToken() (string, error) {
	token, err := generateAutomationToken()
	if err != nil {
		return "", err
	}

	a.automationConfig.APIToken = token
	if err := a.saveAutomationConfig(); err != nil {
		return "", err
	}

	a.Log("info", "本地自动化 API token 已轮换，请同步更新脚本中的 Bearer token。")
	return token, nil
}

// UnregisterAsDefaultBrowser 清理当前程序添加的浏览器注册表项
func (a *App) UnregisterAsDefaultBrowser() (string, error) {
	exePath, err := getExecutablePath()
	if err != nil {
		return "", fmt.Errorf("获取程序路径失败: %v", err)
	}

	exeName := filepath.Base(exePath)
	commands := [][]string{
		{"reg", "delete", "HKEY_CURRENT_USER\\Software\\Clients\\StartMenuInternet\\MyBrowser", "/f"},
		{"reg", "delete", "HKEY_CURRENT_USER\\Software\\RegisteredApplications", "/v", "MyBrowser", "/f"},
		{"reg", "delete", "HKEY_CURRENT_USER\\Software\\Classes\\MyBrowserURL", "/f"},
		{"reg", "delete", "HKEY_CURRENT_USER\\Software\\Classes\\Applications\\" + exeName, "/f"},
		{"reg", "delete", "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\App Paths\\" + exeName, "/f"},
	}

	for _, cmdArgs := range commands {
		out, cmdErr := runHiddenCombinedCommand(cmdArgs[0], cmdArgs[1:]...)
		if cmdErr != nil {
			outputText := strings.ToLower(string(out))
			// 注册表不存在时不视为失败，便于重复清理。
			if strings.Contains(outputText, "unable to find") || strings.Contains(outputText, "找不到") || strings.Contains(outputText, "系统找不到指定的注册表项") {
				continue
			}
			a.Log("error", fmt.Sprintf("清理注册表失败: %v, 输出: %s", cmdErr, string(out)))
			return "", fmt.Errorf("清理注册表失败: %v", cmdErr)
		}
	}

	msg := "已清理 MyBrowser 的注册表项。如系统默认浏览器列表仍显示旧记录，可在 Windows 默认应用中改选其他浏览器后再查看。"
	a.Log("info", msg)
	return msg, nil
}

// ImportProfile 导入 MBP 环境包
func (a *App) ImportProfile() error {
	sourcePath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择导入的环境包",
		Filters: []runtime.FileFilter{
			{DisplayName: "MyBrowser Profile (*.mbp)", Pattern: "*.mbp"},
		},
	})
	if err != nil || sourcePath == "" {
		return err
	}

	profile, err := a.importProfileBundle(sourcePath)
	if err != nil {
		return err
	}

	a.Log("info", fmt.Sprintf("成功导入环境: %s", profile.Name))
	return nil
}

// ImportCookiesFromFile 从文件读取 Cookie JSON
func (a *App) ImportCookiesFromFile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择包含 Cookie 的 JSON 文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取失败: %v", err)
	}

	content := string(data)
	// 基础校验：是否包含 "["。如果是单一对象也会在 setup 时被处理，这里只做简单的完整性判断
	if !strings.Contains(content, "[") {
		return "", fmt.Errorf("文件内容似乎不是合法的 Cookie 数组格式")
	}

	a.Log("info", fmt.Sprintf("从文件 [%s] 读取 Cookie 成功", filepath.Base(path)))
	return content, nil
}

// CreateDesktopShortcut 在桌面上生成本程序的快捷方式
func (a *App) CreateDesktopShortcut() error {
	exePath, err := getExecutablePath()
	if err != nil {
		return fmt.Errorf("无法获取程序路径: %v", err)
	}

	homeDir, err := getUserHomeDir()
	if err != nil {
		return fmt.Errorf("无法获取用户主目录: %v", err)
	}

	desktopPath := filepath.Join(homeDir, "Desktop")
	shortcutPath := filepath.Join(desktopPath, "MyBrowser Pro.lnk")

	// 使用 PowerShell 创建快捷方式
	psCommand := fmt.Sprintf(`$WshShell = New-Object -comObject WScript.Shell; $Shortcut = $WshShell.CreateShortcut('%s'); $Shortcut.TargetPath = '%s'; $Shortcut.WorkingDirectory = '%s'; $Shortcut.Save()`, shortcutPath, exePath, filepath.Dir(exePath))

	output, err := runHiddenCombinedCommand("powershell", "-NoProfile", "-Command", psCommand)
	if err != nil {
		return fmt.Errorf("PowerShell 创建失败: %v, 输出: %s", err, string(output))
	}

	a.Log("info", fmt.Sprintf("成功在桌面生成快捷方式: %s", shortcutPath))
	return nil
}
