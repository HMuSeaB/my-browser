package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
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
	ctx                  context.Context
	profiles             []BrowserProfile
	proxies              []ProxyEntry
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
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *bidiError      `json:"error,omitempty"`
}

type bidiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type bidiGetTreeResult struct {
	Contexts []struct {
		Context string `json:"context"`
	} `json:"contexts"`
}

var bidiEndpointPattern = regexp.MustCompile(`ws://(?:127\.0\.0\.1|localhost):\d+(?:/[^\s"]*)?`)

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
		} else if statErr != nil && !os.IsNotExist(statErr) {
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

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		if err := conn.SetReadDeadline(time.Now().Add(minDuration(time.Second, remaining))); err != nil {
			return nil, err
		}

		var response bidiCommandResponse
		if err := conn.ReadJSON(&response); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return nil, err
		}

		if response.ID != commandID {
			continue
		}
		if response.Error != nil {
			message := strings.TrimSpace(response.Error.Message)
			if message == "" {
				message = strings.TrimSpace(response.Error.Error)
			}
			if message == "" {
				message = "unknown bidi error"
			}
			return nil, fmt.Errorf("%s failed: %s", method, message)
		}
		return response.Result, nil
	}

	return nil, fmt.Errorf("%s timed out after %s", method, timeout)
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

// setupProxy 配置 Firefox 的代理设置
func (a *App) setupProxy(userDataDir, proxyStr string) error {
	if proxyStr == "" {
		return nil
	}

	prefsPath := filepath.Join(userDataDir, "prefs.js")
	var proxyType int = 1
	var httpHost, httpPort string
	var sslHost, sslPort string
	var socksHost, socksPort string
	var socksVersion int = 5

	// 解析逻辑优化
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
		// 默认处理为 http
		hostPort := strings.Split(tempProxy, ":")
		if len(hostPort) == 2 {
			httpHost, httpPort = hostPort[0], hostPort[1]
			sslHost, sslPort = hostPort[0], hostPort[1]
		}
	}

	content := fmt.Sprintf(`
user_pref("network.proxy.type", %d);
user_pref("network.proxy.http", "%s");
user_pref("network.proxy.http_port", %s);
user_pref("network.proxy.ssl", "%s");
user_pref("network.proxy.ssl_port", %s);
user_pref("network.proxy.socks", "%s");
user_pref("network.proxy.socks_port", %s);
user_pref("network.proxy.socks_version", %d);
user_pref("network.proxy.socks_remote_dns", true);
user_pref("network.proxy.share_proxy_settings", true);
`, proxyType, httpHost, httpPort, sslHost, sslPort, socksHost, socksPort, socksVersion)

	return os.WriteFile(prefsPath, []byte(content), 0644)
}

// generateFingerprintConfig 为指定环境生成随机且唯一的指纹配置
func (a *App) generateFingerprintConfig(profile BrowserProfile) map[string]interface{} {
	// 这里模拟 Camoufox 的配置生成
	// 实际生产中可以根据 profile.ID 种子化随机数，确保同一环境指纹固定
	config := make(map[string]interface{})

	config["navigator.userAgent"] = profile.UA
	config["navigator.platform"] = profile.Platform
	config["navigator.language"] = "zh-CN"
	config["navigator.languages"] = []string{"zh-CN", "zh", "en-US", "en"}

	// WebGL 混淆
	config["webGl:vendor"] = "Google Inc. (Intel)"
	config["webGl:renderer"] = "ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0)"

	// Canvas 噪音噪音
	config["canvas:aaOffset"] = 12 // 固定偏移或随机

	// 屏幕分辨率 (可选)
	config["screen.width"] = 1920
	config["screen.height"] = 1080

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

func (a *App) prepareProfileLaunch(profile BrowserProfile) (string, string, error) {
	exePath, err := a.getCamoufoxPath()
	if err != nil {
		return "", "", err
	}

	userDataDir := filepath.Join(a.getDataDir(), "profiles", profile.ID)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return "", "", err
	}

	if err := a.setupProxy(userDataDir, profile.Proxy); err != nil {
		fmt.Printf("配置代理失败: %v\n", err)
	}

	if err := a.setupCookies(userDataDir, profile.Cookies); err != nil {
		fmt.Printf("注入 Cookie 失败: %v\n", err)
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

	// 创建 ZIP 存档
	newZipFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer newZipFile.Close()

	zipWriter := zip.NewWriter(newZipFile)
	defer zipWriter.Close()

	// 1. 写入元数据
	metaData, _ := json.MarshalIndent(profile, "", "  ")
	f, err := zipWriter.Create("metadata.json")
	if err != nil {
		return err
	}
	f.Write(metaData)

	// 2. 写入物理文件 (profiles/<id>/*)
	userDataDir := filepath.Join(a.getDataDir(), "profiles", profileID)
	filepath.Walk(userDataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(userDataDir, path)
		f, err := zipWriter.Create(filepath.Join("data", relPath))
		if err != nil {
			return err
		}
		srcFile, _ := os.Open(path)
		defer srcFile.Close()
		io.Copy(f, srcFile)
		return nil
	})

	a.Log("info", fmt.Sprintf("环境 [%s] 已成功打包导出到: %s", profile.Name, targetPath))
	return nil
}

// RegisterAsDefaultBrowser 将当前程序注册为 Windows 可识别的浏览器
func (a *App) RegisterAsDefaultBrowser() (string, error) {
	exePath, err := os.Executable()
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
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		// 隐藏黑色 CMD 窗口
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if out, err := cmd.CombinedOutput(); err != nil {
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
	exec.Command("cmd", "/c", "start", "ms-settings:defaultapps").Run()
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
	cmd := exec.Command("cmd", "/c", "start", "", dataDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
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
	exePath, err := os.Executable()
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
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, cmdErr := cmd.CombinedOutput()
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

	zipReader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return err
	}
	defer zipReader.Close()

	var profile BrowserProfile
	// 1. 读取元数据
	for _, f := range zipReader.File {
		if f.Name == "metadata.json" {
			rc, _ := f.Open()
			content, _ := io.ReadAll(rc)
			rc.Close()
			json.Unmarshal(content, &profile)
			break
		}
	}

	if profile.ID == "" {
		return fmt.Errorf("无效的环境包")
	}

	// 2. 生成新 ID 避免重复，并准备目录
	newID := uuid.New().String()
	profile.ID = newID

	// 智能重命名逻辑：仅在名称冲突时添加编号
	baseName := profile.Name
	newName := baseName
	counter := 1
	for {
		exists := false
		for _, p := range a.profiles {
			if p.Name == newName {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
		newName = fmt.Sprintf("%s (%d)", baseName, counter)
		counter++
	}
	profile.Name = newName
	profile.CreateAt = time.Now().Unix()
	newUserDataDir := filepath.Join(a.getDataDir(), "profiles", newID)
	os.MkdirAll(newUserDataDir, 0755)

	// 3. 解压物理文件
	for _, f := range zipReader.File {
		if strings.HasPrefix(f.Name, "data/") {
			relPath := strings.TrimPrefix(f.Name, "data/")
			targetPath := filepath.Join(newUserDataDir, relPath)
			os.MkdirAll(filepath.Dir(targetPath), 0755)

			dstFile, _ := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			rc, _ := f.Open()
			io.Copy(dstFile, rc)
			rc.Close()
			dstFile.Close()
		}
	}

	// 4. 加入列表并保存
	a.profiles = append(a.profiles, profile)
	a.saveProfiles()

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
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取程序路径: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("无法获取用户主目录: %v", err)
	}

	desktopPath := filepath.Join(homeDir, "Desktop")
	shortcutPath := filepath.Join(desktopPath, "MyBrowser Pro.lnk")

	// 使用 PowerShell 创建快捷方式
	psCommand := fmt.Sprintf(`$WshShell = New-Object -comObject WScript.Shell; $Shortcut = $WshShell.CreateShortcut('%s'); $Shortcut.TargetPath = '%s'; $Shortcut.WorkingDirectory = '%s'; $Shortcut.Save()`, shortcutPath, exePath, filepath.Dir(exePath))

	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCommand)
	// 隐藏黑色 CMD 窗口
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell 创建失败: %v, 输出: %s", err, string(output))
	}

	a.Log("info", fmt.Sprintf("成功在桌面生成快捷方式: %s", shortcutPath))
	return nil
}
