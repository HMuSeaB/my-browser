package main

import (
	"crypto/md5"
	"encoding/binary"
)

// GPUConfig WebGL 显卡配置
type GPUConfig struct {
	Vendor   string `json:"vendor"`
	Renderer string `json:"renderer"`
}

// ScreenConfig 屏幕分辨率配置
type ScreenConfig struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// HardwarePreset 对应特定操作系统的硬件配置预设
type HardwarePreset struct {
	Platform    string
	GPUs        []GPUConfig
	Resolutions []ScreenConfig
}

// HardwarePresets 静态存储真实环境的硬件指纹映射，避免 Lie 检测器的统计学冲突
var HardwarePresets = map[string]HardwarePreset{
	"Windows": {
		Platform: "Win32",
		GPUs: []GPUConfig{
			{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 4070 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon(TM) Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		},
		Resolutions: []ScreenConfig{
			{1920, 1080},
			{2560, 1440},
			{1366, 768},
			{1536, 864},
		},
	},
	"macOS": {
		Platform: "MacIntel",
		GPUs: []GPUConfig{
			{"Apple Inc.", "Apple M1"},
			{"Apple Inc.", "Apple M2"},
			{"Apple Inc.", "Apple M3"},
			{"Intel Inc.", "Intel(R) Iris(R) Plus Graphics 640"},
		},
		Resolutions: []ScreenConfig{
			{1440, 900},
			{1680, 1050},
			{2560, 1600},
			{3024, 1964},
		},
	},
	"Linux": {
		Platform: "Linux x86_64",
		GPUs: []GPUConfig{
			{"Mesa", "AMD Radeon RX 6700 XT (radeonsi, navi22, LLVM 15.0.7, DRM 3.49, 6.2.0-37-generic)"},
			{"NVIDIA Corporation", "NVIDIA GeForce GTX 1660/PCIe/SSE2"},
			{"Intel", "Intel(R) UHD Graphics (CML GT2)"},
		},
		Resolutions: []ScreenConfig{
			{1920, 1080},
			{1366, 768},
			{2560, 1440},
		},
	},
}

// hashStringToInt64 将 ProfileID 等特征字符串转化为唯一的随机种子，确保环境指纹一致性
func hashStringToInt64(s string) int64 {
	h := md5.Sum([]byte(s))
	return int64(binary.BigEndian.Uint64(h[0:8]))
}

// getNativePlatformString 根据操作系统类型返回规范的 navigator.platform 字符串
func getNativePlatformString(platform string) string {
	if preset, ok := HardwarePresets[platform]; ok {
		return preset.Platform
	}
	return "Win32" // 默认
}
