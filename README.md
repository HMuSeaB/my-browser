# MyBrowser Pro - 开源高性能指纹浏览器

MyBrowser Pro 是一款基于 Go + Wails + Vue 3 开发的轻量级、深度反检测浏览器。它集成了 **Camoufox** 内核，通过 C++ 底层注入技术提供高强度的指纹保护，专为隐私保护、多账号管理及电商/社媒环境设计。

![UI Preview](https://raw.githubusercontent.com/wailsapp/wails/master/website/static/img/wails-logo.png) <!-- TODO: 替换为实机截图 -->

## 🚀 核心特性

- **深度指纹混淆**：基于 Camoufox，覆盖 Canvas、WebGL、Audio、Font、WebRTC、Timezone 等全维度指纹保护。
- **物理级 Cookie 同步**：支持“先启动登录、后一键同步”的闭环流程。通过提取物理 `cookies.sqlite` 实现 Google/Facebook 账号免密登录。
- **全协议代理支持**：支持 HTTP/HTTPS/SOCKS5 代理。开启 **DNS 防泄漏**，确保网络层面的彻底隔离。
- **本地存储隐私安全**：默认将 Cookie 与环境配置存储在 `%LOCALAPPDATA%\MyBrowser`，绝不上传云端。若程序同目录存在 `portable.flag`，则自动切换到便携模式并使用同目录下的 `MyBrowserData`。首次启动公开版时，如检测到旧版 `data` 目录，会自动迁移并保留旧目录以便核对。
- **极致轻量**：Go 语言后端实现，相比 Python 方案，控制层内存占用降低 80%+。

## 🛠️ 安装与运行

### 1. 前置环境
- [Go](https://go.dev/dl/) 1.21+
- [Node.js](https://nodejs.org/) & npm
- [Wails CLI](https://wails.io/docs/gettingstarted/installation) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Python 3.9+ (用于 Cookie 数据提取桥接)

### 2. 下载浏览器内核
项目启动前需获取 Camoufox 运行环境：
1. 运行 `pip install camoufox`
2. 运行 `camoufox fetch`
3. 或者将下载好的 `camoufox-xxx-win.x86_64` 放置在项目根目录下。

### 3. 构建与启动
```bash
# 开发模式
wails dev

# 编译为 .exe (单文件)
wails build
```

### 4. 前端构建说明
- 当前仓库会保留 `frontend/dist`，因为桌面端入口会直接嵌入它用于打包。
- 修改 `frontend/src`、`frontend/wailsjs` 或界面文案后，请先在 `frontend` 目录执行一次 `npm run build`，再提交到仓库。
- 若 `frontend/dist` 与源码不同步，公开仓库中的桌面构建结果可能不是最新界面。

## 💾 存储模式

- **默认安装模式**：数据保存在 `%LOCALAPPDATA%\MyBrowser`，适合日常安装、升级和卸载，程序文件与用户数据分离。
- **便携模式**：在程序同目录放置一个空文件 `portable.flag` 后，应用会自动把数据切换到同目录下的 `MyBrowserData`。
- **旧数据迁移**：如果检测到旧版 `data` 目录，而新目录还没有数据，程序会自动迁移，并保留旧目录供您确认。

## 🧹 卸载与清理

- 删除程序本体不会自动删除数据目录。
- 若使用默认安装模式，数据通常位于 `%LOCALAPPDATA%\MyBrowser`。
- 若使用便携模式，数据位于程序同目录下的 `MyBrowserData`。
- 应用内的“清理浏览器注册”只会移除 MyBrowser 写入的注册表项，不会删除您的环境数据。

## 📖 快速上手流程
1. **新建环境**：填入名称，协议可选 SOCKS5 (默认 7891) 或 HTTP (默认 7890)。
2. **连接测试**：在设置中心点击“测试”验证代理是否通畅。
3. **环境隔离**：点击“🚀 启动环境”，在浏览器中完成账号登录。
4. **状态保存**：关闭浏览器后，点击“💾 同步 Cookie”保存登录状态。
5. **指纹验证**：点击“🔍”图标直达 Pixelscan 验证防关联强度。

## 🔒 隐私声明
本项目为纯本地开源工具。除非您手动配置了外部代理，否则所有流量和环境数据仅驻留在您的设备上。

## 📄 开源协议
MIT License
