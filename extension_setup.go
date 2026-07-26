package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// --- 对外接口 ---

// GetExtensions 返回全部已安装扩展
func (a *App) GetExtensions() []BrowserExtension {
	if a.extensions == nil {
		return []BrowserExtension{}
	}
	return a.extensions
}

// SetExtensionEnabled 设置扩展的全局启用状态
func (a *App) SetExtensionEnabled(id string, enabled bool) error {
	for i := range a.extensions {
		if a.extensions[i].ID == id {
			a.extensions[i].Enabled = enabled
			return a.saveExtensions()
		}
	}
	return fmt.Errorf("未找到扩展")
}

// SetExtensionPinned 设置图标是固定在地址栏旁，还是收进拼图面板。
// 该设置写在 profile 副本的 manifest 里，需重启环境生效。
func (a *App) SetExtensionPinned(id string, pinned bool) error {
	for i := range a.extensions {
		if a.extensions[i].ID != id {
			continue
		}
		if !a.extensions[i].HasPopup {
			return fmt.Errorf("该扩展没有弹窗界面，无法固定到工具栏")
		}
		a.extensions[i].Pinned = pinned
		return a.saveExtensions()
	}
	return fmt.Errorf("未找到扩展")
}

// DeleteExtension 卸载扩展，并清理各环境中对它的引用
func (a *App) DeleteExtension(id string) error {
	found := false
	for i, ext := range a.extensions {
		if ext.ID == id {
			a.extensions = append(a.extensions[:i], a.extensions[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("未找到扩展")
	}

	if err := os.RemoveAll(a.getExtensionSourceDir(id)); err != nil {
		a.Log("warn", fmt.Sprintf("清理扩展文件失败: %v", err))
	}

	profilesDirty := false
	for i := range a.profiles {
		filtered := make([]string, 0, len(a.profiles[i].EnabledExtensions))
		for _, eid := range a.profiles[i].EnabledExtensions {
			if eid != id {
				filtered = append(filtered, eid)
			}
		}
		if len(filtered) != len(a.profiles[i].EnabledExtensions) {
			a.profiles[i].EnabledExtensions = filtered
			profilesDirty = true
		}
	}
	if profilesDirty {
		if err := a.saveProfiles(); err != nil {
			return err
		}
	}

	a.Log("info", fmt.Sprintf("已卸载扩展: %s", id))
	return a.saveExtensions()
}

// SetProfileExtensions 设置某个环境启用的扩展清单
func (a *App) SetProfileExtensions(profileID string, extensionIDs []string) error {
	known := make(map[string]bool, len(a.extensions))
	for _, ext := range a.extensions {
		known[ext.ID] = true
	}

	filtered := make([]string, 0, len(extensionIDs))
	for _, id := range extensionIDs {
		if known[id] {
			filtered = append(filtered, id)
		}
	}

	for i := range a.profiles {
		if a.profiles[i].ID == profileID {
			a.profiles[i].EnabledExtensions = filtered
			a.Log("info", fmt.Sprintf("环境 [%s] 已启用 %d 个扩展，重启环境后生效",
				a.profiles[i].Name, len(filtered)))
			return a.saveProfiles()
		}
	}
	return fmt.Errorf("未找到环境")
}

// InstallExtensionFromDialog 通过文件对话框选择 .zip / .crx 安装
func (a *App) InstallExtensionFromDialog() (BrowserExtension, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择扩展压缩包",
		Filters: []runtime.FileFilter{
			{DisplayName: "浏览器扩展 (*.zip;*.crx)", Pattern: "*.zip;*.crx"},
		},
	})
	if err != nil || path == "" {
		return BrowserExtension{}, err
	}
	return a.InstallExtensionFromPath(path)
}

// InstallExtensionFromDirectoryDialog 通过目录对话框选择已解压的扩展文件夹
func (a *App) InstallExtensionFromDirectoryDialog() (BrowserExtension, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择扩展文件夹（内含 manifest.json）",
	})
	if err != nil || dir == "" {
		return BrowserExtension{}, err
	}
	return a.InstallExtensionFromPath(dir)
}

// DropOutcome 描述一个拖入路径的处理结果
type DropOutcome struct {
	FileName    string   `json:"file_name"`
	Kind        string   `json:"kind"` // script | extension
	OK          bool     `json:"ok"`
	Error       string   `json:"error"`
	Name        string   `json:"name"`
	Unsupported []string `json:"unsupported"`
}

// InstallFromDroppedPaths 统一处理窗口拖入的路径。
//
// 按形态分流：目录与 .zip/.crx 视为浏览器扩展，.user.js/.js/.txt 视为用户脚本。
// 逐个独立处理，单个失败不影响其余。
func (a *App) InstallFromDroppedPaths(paths []string) []DropOutcome {
	outcomes := make([]DropOutcome, 0, len(paths))

	for _, path := range paths {
		name := filepath.Base(path)
		outcome := DropOutcome{FileName: name}

		info, err := os.Stat(path)
		if err != nil {
			outcome.Error = fmt.Sprintf("无法读取: %v", err)
			outcomes = append(outcomes, outcome)
			continue
		}

		lower := strings.ToLower(name)
		isArchive := strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".crx")

		switch {
		case info.IsDir() || isArchive:
			outcome.Kind = "extension"
			ext, err := a.InstallExtensionFromPath(path)
			if err != nil {
				outcome.Error = err.Error()
				break
			}
			outcome.OK = true
			outcome.Name = ext.Name
			outcome.Unsupported = ext.Incompatible

		case isLikelyUserScriptFile(name):
			outcome.Kind = "script"
			// 复用既有的脚本安装逻辑，以带上元数据解析与 @require 依赖下载
			results := a.InstallUserScriptsFromPaths([]string{path})
			if len(results) == 0 {
				outcome.Error = "安装失败"
				break
			}
			outcome.OK = results[0].OK
			outcome.Error = results[0].Error
			outcome.Name = results[0].Script.Name
			outcome.Unsupported = results[0].Unsupported

		default:
			outcome.Error = "无法识别：脚本请拖入 .user.js，扩展请拖入文件夹或 .zip/.crx"
		}

		if !outcome.OK && outcome.Error != "" {
			a.Log("warn", fmt.Sprintf("拖入的 [%s] 未能安装: %s", name, outcome.Error))
		}
		outcomes = append(outcomes, outcome)
	}

	return outcomes
}

// --- 生成到 profile ---

// extensionStampFile 记录该目录是由哪个扩展、哪个版本生成的，
// 未变化时跳过重复复制（扩展动辄数 MB，每次启动都重铺代价太高）
const extensionStampFile = ".mybrowser-stamp"

// resolveEnabledExtensions 返回本环境实际生效的扩展：全局启用 ∩ 环境启用
func (a *App) resolveEnabledExtensions(profile BrowserProfile) []BrowserExtension {
	if len(profile.EnabledExtensions) == 0 {
		return nil
	}

	wanted := make(map[string]bool, len(profile.EnabledExtensions))
	for _, id := range profile.EnabledExtensions {
		wanted[id] = true
	}

	result := make([]BrowserExtension, 0, len(profile.EnabledExtensions))
	for _, ext := range a.extensions {
		if ext.Enabled && wanted[ext.ID] {
			result = append(result, ext)
		}
	}
	return result
}

// setupExtensions 按环境启用清单把扩展铺进 profile。
//
// 关键点：原始副本永不修改，仅在写入 profile 的这一份 manifest 上注入
// Firefox 所需的扩展 ID（若原 manifest 未声明）。
func (a *App) setupExtensions(userDataDir string, profile BrowserProfile) error {
	extRoot := filepath.Join(userDataDir, "extensions")
	enabled := a.resolveEnabledExtensions(profile)

	keep := make(map[string]bool, len(enabled)+1)
	// 用户脚本引擎由 setupUserScripts 管理，此处不得误删
	keep[userScriptExtID] = true
	for _, ext := range enabled {
		keep[ext.GeckoID] = true
	}

	// 清掉本环境已取消启用的扩展，避免残留继续生效
	if entries, err := os.ReadDir(extRoot); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || keep[entry.Name()] {
				continue
			}
			if err := os.RemoveAll(filepath.Join(extRoot, entry.Name())); err != nil {
				a.Log("warn", fmt.Sprintf("清理旧扩展 [%s] 失败: %v", entry.Name(), err))
			}
		}
	}

	if len(enabled) == 0 {
		return nil
	}
	if err := os.MkdirAll(extRoot, 0755); err != nil {
		return fmt.Errorf("创建扩展目录失败: %w", err)
	}

	for _, ext := range enabled {
		target := filepath.Join(extRoot, ext.GeckoID)
		// 固定位置写在 manifest 里，改动后必须重铺，故并入指纹
		stamp := fmt.Sprintf("%s|%s|%d|pinned=%t", ext.ID, ext.Version, ext.InstalledAt, ext.Pinned)

		if current, err := os.ReadFile(filepath.Join(target, extensionStampFile)); err == nil {
			if string(current) == stamp {
				continue // 内容未变，跳过重复铺设
			}
		}

		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("清理扩展 [%s] 失败: %w", ext.Name, err)
		}

		source := a.getExtensionSourceDir(ext.ID)
		if _, err := os.Stat(source); err != nil {
			a.Log("warn", fmt.Sprintf("扩展 [%s] 的文件缺失，已跳过: %v", ext.Name, err))
			continue
		}
		if err := copyDirectory(source, target); err != nil {
			return fmt.Errorf("铺设扩展 [%s] 失败: %w", ext.Name, err)
		}

		if err := patchProfileManifest(target, ext); err != nil {
			a.Log("warn", fmt.Sprintf("调整扩展 [%s] 的 manifest 失败: %v", ext.Name, err))
		}

		if err := os.WriteFile(filepath.Join(target, extensionStampFile), []byte(stamp), 0644); err != nil {
			a.Log("warn", fmt.Sprintf("写入扩展标记失败: %v", err))
		}
	}

	a.Log("info", fmt.Sprintf("已为环境 [%s] 装载 %d 个扩展", profile.Name, len(enabled)))
	return nil
}

// patchProfileManifest 调整写入 profile 的那份 manifest：
//   - 原 manifest 未声明扩展 ID 时补入（Firefox 装入 profile 必须有显式 ID）
//   - 按需设置图标落位（nav-bar 直接可见，默认收进拼图面板）
//
// 只改这一份副本；用户的原始文件夹与 <dataDir>/extensions/ 下的副本均保持不变。
// 用 map 解析以保留所有未知字段，避免重写时丢失内容。
func patchProfileManifest(extDir string, ext BrowserExtension) error {
	path := filepath.Join(extDir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("manifest 解析失败: %w", err)
	}

	if ext.GeckoIDInjected {
		settings, ok := manifest["browser_specific_settings"].(map[string]interface{})
		if !ok {
			settings = map[string]interface{}{}
		}
		gecko, ok := settings["gecko"].(map[string]interface{})
		if !ok {
			gecko = map[string]interface{}{}
		}
		gecko["id"] = ext.GeckoID
		if _, exists := gecko["strict_min_version"]; !exists {
			gecko["strict_min_version"] = "115.0"
		}
		settings["gecko"] = gecko
		manifest["browser_specific_settings"] = settings
	}

	// 实测：不设 default_area 时图标落在 unified-extensions-area（拼图面板），
	// 设为 navbar 则直接出现在地址栏旁边。
	if ext.HasPopup {
		area := "menupanel"
		if ext.Pinned {
			area = "navbar"
		}
		for _, key := range []string{"action", "browser_action"} {
			if section, ok := manifest[key].(map[string]interface{}); ok {
				section["default_area"] = area
				manifest[key] = section
			}
		}
	}

	patched, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, patched, 0644)
}

// unsupportedExtensionSummary 汇总扩展在 Firefox 下的不兼容项，供界面提示
func unsupportedExtensionSummary(ext BrowserExtension) string {
	if len(ext.Incompatible) == 0 {
		return ""
	}
	return strings.Join(ext.Incompatible, "；")
}
