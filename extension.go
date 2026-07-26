package main

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BrowserExtension 代表一个已安装的浏览器扩展。
//
// 原始文件夹不被修改：安装时复制一份到 <dataDir>/extensions/<ID>/ 原样保存，
// 只有生成到 profile 的那一份才会被补上 Firefox 所需的扩展 ID。
type BrowserExtension struct {
	ID          string `json:"id"`       // 内部 ID
	GeckoID     string `json:"gecko_id"` // Firefox 扩展 ID
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	ManifestVer int    `json:"manifest_version"`
	HasPopup    bool   `json:"has_popup"`
	// GeckoIDInjected 表示该 ID 是我们生成的（原 manifest 未声明）。
	// 生成到 profile 时需要注入，原始副本保持不变。
	GeckoIDInjected bool     `json:"gecko_id_injected"`
	Permissions     []string `json:"permissions"`
	HostPermissions []string `json:"host_permissions"`
	// Incompatible 记录 Firefox 不支持的特征，装上也不会正常工作
	Incompatible []string `json:"incompatible"`
	// Pinned 决定图标是直接放在地址栏旁（nav-bar），还是收进拼图面板
	// （unified-extensions-area）。实测由 manifest 的 action.default_area 控制。
	Pinned      bool  `json:"pinned"`
	Enabled     bool  `json:"enabled"`
	InstalledAt int64 `json:"installed_at"`
}

// --- 存储 ---

func (a *App) getExtensionStoragePath() string {
	return filepath.Join(a.getDataDir(), "extensions.json")
}

func (a *App) getExtensionStoreDir() string {
	return filepath.Join(a.getDataDir(), "extensions")
}

// getExtensionSourceDir 返回某个扩展的原样副本目录
func (a *App) getExtensionSourceDir(id string) string {
	return filepath.Join(a.getExtensionStoreDir(), id)
}

func (a *App) loadExtensions() {
	data, err := os.ReadFile(a.getExtensionStoragePath())
	if err != nil {
		a.extensions = []BrowserExtension{}
		return
	}
	if err := json.Unmarshal(data, &a.extensions); err != nil {
		fmt.Printf("解析扩展索引失败，已重置: %v\n", err)
		a.extensions = []BrowserExtension{}
	}
}

func (a *App) saveExtensions() error {
	data, err := json.MarshalIndent(a.extensions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.getExtensionStoragePath(), data, 0644)
}

// --- manifest 解析 ---

// firefoxUnsupportedPermissions 是 Chrome 独有、Firefox 未实现的权限。
// 声明了这些的扩展即便能装上，相关功能也不会工作。
var firefoxUnsupportedPermissions = map[string]string{
	"offscreen":                   "offscreen（离屏文档，Chrome 专有）",
	"sidePanel":                   "sidePanel（侧边栏，Firefox 用 sidebarAction）",
	"system.display":              "system.display（Chrome 专有）",
	"enterprise.deviceAttributes": "enterprise.*（Chrome 企业策略专有）",
	"enterprise.platformKeys":     "enterprise.*（Chrome 企业策略专有）",
	"tabGroups":                   "tabGroups（Chrome 专有）",
	"fileBrowserHandler":          "fileBrowserHandler（ChromeOS 专有）",
}

// firefoxUnsupportedKeys 是 Firefox 不识别的顶层 manifest 键
var firefoxUnsupportedKeys = map[string]string{
	"externally_connectable": "externally_connectable（Firefox 不支持网页直连扩展）",
	"side_panel":             "side_panel（Chrome 专有）",
	"sandbox":                "sandbox（Firefox 不支持沙箱页面）",
}

// detectFirefoxIncompatibilities 检出 Firefox 不支持的特征。
//
// 目的与用户脚本的 unsupportedScriptFeatures 一致：宁可装之前说清楚，
// 也不要让用户装上之后对着不工作的扩展找原因。
func detectFirefoxIncompatibilities(manifest map[string]interface{}) []string {
	var issues []string

	// Firefox 的 MV3 用 Event Page，不支持 Chrome 式的后台 service worker
	if bg, ok := manifest["background"].(map[string]interface{}); ok {
		_, hasSW := bg["service_worker"]
		_, hasScripts := bg["scripts"]
		_, hasPage := bg["page"]
		if hasSW && !hasScripts && !hasPage {
			issues = append(issues,
				"background.service_worker（Firefox 的 MV3 使用 Event Page，不支持 service worker 后台）")
		}
	}

	seen := map[string]bool{}
	collectPerms := func(key string) {
		list, ok := manifest[key].([]interface{})
		if !ok {
			return
		}
		for _, item := range list {
			name, ok := item.(string)
			if !ok {
				continue
			}
			if desc, bad := firefoxUnsupportedPermissions[name]; bad && !seen[desc] {
				seen[desc] = true
				issues = append(issues, desc)
			}
		}
	}
	collectPerms("permissions")
	collectPerms("optional_permissions")

	for key, desc := range firefoxUnsupportedKeys {
		if _, present := manifest[key]; present {
			issues = append(issues, desc)
		}
	}

	return issues
}

func manifestString(manifest map[string]interface{}, key string) string {
	if v, ok := manifest[key].(string); ok {
		return v
	}
	return ""
}

func manifestStringSlice(manifest map[string]interface{}, key string) []string {
	list, ok := manifest[key].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// existingGeckoID 读取 manifest 中已声明的 Firefox 扩展 ID（新旧两种写法）
func existingGeckoID(manifest map[string]interface{}) string {
	for _, key := range []string{"browser_specific_settings", "applications"} {
		section, ok := manifest[key].(map[string]interface{})
		if !ok {
			continue
		}
		gecko, ok := section["gecko"].(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := gecko["id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// readExtensionManifest 读取并解析扩展目录下的 manifest.json
func readExtensionManifest(dir string) (map[string]interface{}, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("未找到 manifest.json，这不像是一个扩展目录: %w", err)
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("manifest.json 解析失败: %w", err)
	}
	return manifest, nil
}

// resolveExtensionName 处理 __MSG_xxx__ 形式的本地化名称。
// 该写法需要读取 _locales 才能还原，此处退化为可读的占位。
func resolveExtensionName(dir string, raw string) string {
	name := strings.TrimSpace(raw)
	if !strings.HasPrefix(name, "__MSG_") {
		return name
	}

	key := strings.TrimSuffix(strings.TrimPrefix(name, "__MSG_"), "__")
	// 依次尝试常见语言目录
	for _, locale := range []string{"zh_CN", "zh", "en", "en_US"} {
		path := filepath.Join(dir, "_locales", locale, "messages.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var messages map[string]struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &messages) != nil {
			continue
		}
		if entry, ok := messages[key]; ok && strings.TrimSpace(entry.Message) != "" {
			return entry.Message
		}
	}
	return name
}

// --- 安装 ---

const maxExtensionSize = 256 << 20 // 单个扩展解压后上限 256 MiB

// InstallExtensionFromPath 从文件夹、.zip 或 .crx 安装扩展。
//
// 原始路径只读不改：内容会被复制到 <dataDir>/extensions/<内部ID>/ 原样保存。
func (a *App) InstallExtensionFromPath(sourcePath string) (BrowserExtension, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return BrowserExtension{}, fmt.Errorf("无法读取: %v", err)
	}

	internalID := uuid.New().String()
	targetDir := a.getExtensionSourceDir(internalID)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return BrowserExtension{}, fmt.Errorf("创建扩展目录失败: %v", err)
	}

	cleanup := func() { os.RemoveAll(targetDir) }

	if info.IsDir() {
		if err := copyDirectory(sourcePath, targetDir); err != nil {
			cleanup()
			return BrowserExtension{}, fmt.Errorf("复制扩展目录失败: %v", err)
		}
	} else {
		if err := extractExtensionArchive(sourcePath, targetDir); err != nil {
			cleanup()
			return BrowserExtension{}, err
		}
	}

	// 有些打包会多套一层目录，向下找到真正含 manifest.json 的那层
	rootDir, err := locateManifestRoot(targetDir)
	if err != nil {
		cleanup()
		return BrowserExtension{}, err
	}
	if rootDir != targetDir {
		if err := hoistDirectory(rootDir, targetDir); err != nil {
			cleanup()
			return BrowserExtension{}, fmt.Errorf("整理扩展目录失败: %v", err)
		}
	}

	manifest, err := readExtensionManifest(targetDir)
	if err != nil {
		cleanup()
		return BrowserExtension{}, err
	}

	ext := BrowserExtension{
		ID:              internalID,
		Name:            resolveExtensionName(targetDir, manifestString(manifest, "name")),
		Version:         manifestString(manifest, "version"),
		Description:     resolveExtensionName(targetDir, manifestString(manifest, "description")),
		Permissions:     manifestStringSlice(manifest, "permissions"),
		HostPermissions: manifestStringSlice(manifest, "host_permissions"),
		Incompatible:    detectFirefoxIncompatibilities(manifest),
		Enabled:         false, // 与环境包导入一致：装上但不启用，由用户显式确认
		InstalledAt:     time.Now().Unix(),
	}
	if strings.TrimSpace(ext.Name) == "" {
		ext.Name = "未命名扩展"
	}
	if v, ok := manifest["manifest_version"].(float64); ok {
		ext.ManifestVer = int(v)
	}

	// popup 入口：MV3 用 action，MV2 用 browser_action
	for _, key := range []string{"action", "browser_action"} {
		if section, ok := manifest[key].(map[string]interface{}); ok {
			if popup, ok := section["default_popup"].(string); ok && popup != "" {
				ext.HasPopup = true
			}
		}
	}
	// 有弹窗的扩展就是给人点的，默认固定到地址栏旁；没有弹窗则收进拼图面板
	ext.Pinned = ext.HasPopup

	// Firefox 安装到 profile 的扩展必须有显式 ID；缺失时由我们生成，
	// 但只在生成到 profile 的副本里注入，原始副本保持不变
	if id := existingGeckoID(manifest); id != "" {
		ext.GeckoID = id
	} else {
		ext.GeckoID = internalID + "@mybrowser.local"
		ext.GeckoIDInjected = true
	}

	a.extensions = append(a.extensions, ext)
	if err := a.saveExtensions(); err != nil {
		cleanup()
		return BrowserExtension{}, err
	}

	a.Log("info", fmt.Sprintf("已安装扩展: %s %s", ext.Name, ext.Version))
	if len(ext.Incompatible) > 0 {
		a.Log("warn", fmt.Sprintf("扩展 [%s] 使用了 Firefox 不支持的特征：%s",
			ext.Name, strings.Join(ext.Incompatible, "；")))
	}
	return ext, nil
}

// locateManifestRoot 在解压结果中向下查找含 manifest.json 的目录（最多两层）
func locateManifestRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil {
		return dir, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		if _, err := os.Stat(filepath.Join(candidate, "manifest.json")); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 manifest.json，这不像是一个扩展")
}

// hoistDirectory 把 inner 目录的内容上提到 outer
func hoistDirectory(inner, outer string) error {
	entries, err := os.ReadDir(inner)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		src := filepath.Join(inner, entry.Name())
		dst := filepath.Join(outer, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return os.RemoveAll(inner)
}

// copyDirectory 递归复制目录
func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, in)
		return err
	})
}

// extractExtensionArchive 解压 .zip 或 .crx。
// CRX 是在 zip 前加了一段签名头的容器，跳过该头即可当普通 zip 处理。
func extractExtensionArchive(archivePath, targetDir string) error {
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("读取压缩包失败: %v", err)
	}

	offset, err := crxZipOffset(data)
	if err != nil {
		return err
	}
	payload := data[offset:]

	reader, err := zip.NewReader(strings.NewReader(string(payload)), int64(len(payload)))
	if err != nil {
		return fmt.Errorf("解压失败，文件可能已损坏或不是 zip/crx: %v", err)
	}

	var total int64
	for _, file := range reader.File {
		target, err := safeJoinExtractPath(targetDir, file.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}

		total += int64(file.UncompressedSize64)
		if total > maxExtensionSize {
			return fmt.Errorf("扩展解压后超过 %d MiB，已中止", maxExtensionSize>>20)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		dst.Close()
		src.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// crxZipOffset 返回 zip 数据在文件中的起始偏移。
// 普通 zip 返回 0；CRX2/CRX3 需跳过各自的签名头。
func crxZipOffset(data []byte) (int, error) {
	if len(data) < 16 || string(data[0:4]) != "Cr24" {
		return 0, nil // 普通 zip
	}

	version := binary.LittleEndian.Uint32(data[4:8])
	switch version {
	case 2:
		pubKeyLen := int(binary.LittleEndian.Uint32(data[8:12]))
		sigLen := int(binary.LittleEndian.Uint32(data[12:16]))
		offset := 16 + pubKeyLen + sigLen
		if offset > len(data) {
			return 0, fmt.Errorf("CRX2 头部长度异常")
		}
		return offset, nil
	case 3:
		headerLen := int(binary.LittleEndian.Uint32(data[8:12]))
		offset := 12 + headerLen
		if offset > len(data) {
			return 0, fmt.Errorf("CRX3 头部长度异常")
		}
		return offset, nil
	default:
		return 0, fmt.Errorf("不支持的 CRX 版本: %d", version)
	}
}

// safeJoinExtractPath 防止压缩包内的路径穿越（zip slip）
func safeJoinExtractPath(baseDir, entryName string) (string, error) {
	// zip 规范要求条目名为相对路径且以正斜杠分隔，绝不应以分隔符或盘符开头。
	// 这里显式拦截，因为 filepath.IsAbs 的判定随平台而异：
	// "/x" 在 Linux 上是绝对路径，在 Windows 上却不是。
	if strings.HasPrefix(entryName, "/") || strings.HasPrefix(entryName, `\`) {
		return "", fmt.Errorf("压缩包包含非法路径: %s", entryName)
	}
	if len(entryName) >= 2 && entryName[1] == ':' {
		return "", fmt.Errorf("压缩包包含盘符路径: %s", entryName)
	}

	cleaned := filepath.Clean(entryName)
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	if filepath.IsAbs(cleaned) || cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("压缩包包含非法路径: %s", entryName)
	}

	target := filepath.Join(baseDir, cleaned)
	rel, err := filepath.Rel(baseDir, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("压缩包包含越界路径: %s", entryName)
	}
	return target, nil
}
