# MyBrowser Pro - 开源高性能指纹浏览器

MyBrowser Pro 是一款基于 Go + Wails + Vue 3 开发的轻量级、深度反检测浏览器。它集成了 **Camoufox** 内核，通过 C++ 底层注入技术提供高强度的指纹保护，专为隐私保护、多账号管理及电商/社媒环境设计。

![MyBrowser Screenshot](./docs/images/readme-screenshot.png)

> Desktop UI preview with redacted demo profiles and local-only storage controls.

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

## 🤖 Local Automation API

- MyBrowser Pro 内置了本地开发者 API，可在 `自动化控制台` 侧边栏中查看实际监听地址、Bearer token、当前活动会话以及 BiDi 连接地址。
- API 只监听 `127.0.0.1`，默认启用 Bearer token 鉴权；普通“启动环境”不会暴露调试口，只有“自动化启动”才会创建 BiDi 会话。
- v1 首发只承诺 **Firefox / Camoufox 的 WebDriver BiDi 接入**，不承诺 Chromium CDP 或 Playwright `connect_over_cdp` 兼容。
- 日常简单操作可以直接在自动化控制台里选环境、贴链接、一键打开；这条 UI 链路与下方 Python 示例走的是同一套 BiDi 导航流程。Python 示例则保留给批量、多步、联动脚本等高级场景。

### 常用接口

- `GET /api/v1/automation/info`
- `GET /api/v1/automation/profiles`
- `GET /api/v1/automation/sessions`
- `POST /api/v1/automation/sessions`
- `GET /api/v1/automation/sessions/{session_id}`
- `DELETE /api/v1/automation/sessions/{session_id}`
- `POST /api/v1/automation/token/rotate`

### cURL 示例

```bash
curl -X POST "http://127.0.0.1:9090/api/v1/automation/sessions" \
  -H "Authorization: Bearer YOUR_LOCAL_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"profile_id\":\"YOUR_PROFILE_ID\"}"
```

### Python websocket-client BiDi 示例

先安装依赖：

```bash
pip install requests websocket-client
```

官方示例会直接连接本地 `connect_url`，并使用 `suppress_origin=True` 规避 Firefox / Camoufox 对本地 WebSocket 握手的严格 Origin 校验；BiDi 是事件流协议，所以示例会按命令 `id` 循环等待真正的回执，而不是假设 `recv()` 的第一条就是响应。导航时固定使用 `wait: "none"`，把 Cloudflare / ChatGPT 这类长时间验证页面交给 GUI 浏览器自己处理，避免脚本侧超时崩溃。

```python
import json
import time

import requests
import websocket

BASE_URL = "http://127.0.0.1:9090"
TOKEN = "YOUR_LOCAL_API_TOKEN"
PROFILE_ID = "YOUR_PROFILE_ID"
TARGET_URL = "https://example.com/"


def send_bidi(ws, command_id, method, params=None, timeout=30.0):
    payload = {
        "id": command_id,
        "method": method,
        "params": params or {},
    }
    ws.send(json.dumps(payload))

    deadline = time.time() + timeout
    while time.time() < deadline:
        remaining = max(0.1, deadline - time.time())
        ws.settimeout(min(1.0, remaining))
        raw_message = ws.recv()
        message = json.loads(raw_message)

        if "id" not in message:
            continue
        if message["id"] != command_id:
            continue
        if "error" in message:
            raise RuntimeError(f"{method} failed: {message['error']}")
        return message

    raise TimeoutError(f"{method} timed out after {timeout} seconds")


headers = {
    "Authorization": f"Bearer {TOKEN}",
    "Content-Type": "application/json",
}

resp = requests.post(
    f"{BASE_URL}/api/v1/automation/sessions",
    headers=headers,
    json={"profile_id": PROFILE_ID},
    timeout=15,
)
resp.raise_for_status()

session = resp.json()["data"]
connect_url = session["connect_url"]

ws = websocket.create_connection(
    connect_url,
    timeout=30,
    suppress_origin=True,
)

command_id = 1
try:
    try:
        send_bidi(
            ws,
            command_id,
            "session.new",
            {"capabilities": {"alwaysMatch": {}}},
        )
    except RuntimeError as err:
        print(f"session.new returned a compatibility warning: {err}")
    command_id += 1

    tree = send_bidi(ws, command_id, "browsingContext.getTree")
    contexts = tree["result"].get("contexts", [])
    if not contexts:
        raise RuntimeError("No browsing context returned by browsingContext.getTree")
    context_id = contexts[0]["context"]
    command_id += 1

    navigation = send_bidi(
        ws,
        command_id,
        "browsingContext.navigate",
        {
            "context": context_id,
            "url": TARGET_URL,
            "wait": "none",
        },
    )
    print("Navigate dispatched:", navigation)
finally:
    ws.close()
```

- 同步示例文件： [examples/local_automation_bidi.py](examples/local_automation_bidi.py)

## 🔒 隐私声明
本项目为纯本地开源工具。除非您手动配置了外部代理，否则所有流量和环境数据仅驻留在您的设备上。

## 📄 开源协议
MIT License

