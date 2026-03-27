<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { GetProfiles, LaunchBrowser, UpdateProfile, CreateProfile, DeleteProfile, SyncCookies, ResetCookies, TestProxy, GetProxies, AddProxy, DeleteProxy, TestProxyEntry, ExportCookies, ExportProfile, ImportProfile, ImportCookiesFromFile, RegisterAsDefaultBrowser, OpenDefaultAppsSettings, GetStartupURL, CreateDesktopShortcut, OpenDataDirectory, UnregisterAsDefaultBrowser, GetStorageDirectory, GetStorageMode, GetAutomationInfo, GetAutomationSessions, GetAutomationToken, StartAutomationSession, StopAutomationSession, RotateAutomationToken, SetAutomationEnabled } from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime'

const profiles = ref([])
const loading = ref(true)

const showCookieModal = ref(false)
const showCreateModal = ref(false)
const showSettingsModal = ref(false)
const editingProfile = ref(null)

const cookieJson = ref('')
const proxyTestResult = ref('')
const testingProxy = ref(false)
const currentView = ref('profiles') // 'profiles', 'proxies', 'logs'

const proxies = ref([])
const logs = ref([])
const newProxy = ref({ name: '', addr: '' })
const logContainer = ref(null)
const pendingURL = ref('')
const searchQuery = ref('')

const selectedProxyId = ref('')
const editingProxyId = ref('')
const storageDir = ref('')
const storageMode = ref('localappdata')
const automationInfo = ref({
  enabled: false,
  listen_addr: '127.0.0.1:9090',
  base_url: 'http://127.0.0.1:9090',
  auth_scheme: 'Bearer',
  protocol: 'bidi',
  session_count: 0,
  token_configured: false
})
const automationSessions = ref([])
const automationToken = ref('')
const automationStartURL = ref('')
const selectedAutomationProfileId = ref('')
const automationLoading = ref(false)
let automationPollTimer = null

const newProfile = ref({
  name: '',
  proxyType: 'socks5://',
  proxyAddr: '127.0.0.1:7891', // Clash 默认 SOCKS 端口
  startUrl: '',
  ua: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0'
})

const fetchProfiles = async () => {
  try {
    const res = await GetProfiles()
    profiles.value = res
    if (!selectedAutomationProfileId.value && res.length > 0) {
      selectedAutomationProfileId.value = res[0].id
    }
  } catch (err) {
    console.error('获取环境失败:', err)
  } finally {
    loading.value = false
  }
}

const tokenPreview = computed(() => {
  if (!automationToken.value) return '未生成'
  if (automationToken.value.length <= 12) return automationToken.value
  return `${automationToken.value.slice(0, 8)}...${automationToken.value.slice(-6)}`
})

const normalizedSearchQuery = computed(() => searchQuery.value.trim().toLowerCase())

const searchPlaceholder = computed(() => {
  if (currentView.value === 'profiles') return '搜索环境名称、ID、代理或默认页...'
  if (currentView.value === 'proxies') return '搜索代理名称、地址或状态...'
  if (currentView.value === 'automation') return '搜索会话名称、ID、端口或连接地址...'
  if (currentView.value === 'logs') return '搜索日志级别或内容...'
  return '搜索内容...'
})

const filteredProfiles = computed(() => {
  if (!normalizedSearchQuery.value) return profiles.value
  return profiles.value.filter((profile) => {
    const session = getAutomationSessionForProfile(profile.id)
    const haystack = [
      profile.name,
      profile.id,
      shortId(profile.id),
      profile.proxy || '直连',
      profile.start_url || '新标签页',
      session?.connect_url || '',
    ].join(' ').toLowerCase()
    return haystack.includes(normalizedSearchQuery.value)
  })
})

const filteredProxies = computed(() => {
  if (!normalizedSearchQuery.value) return proxies.value
  return proxies.value.filter((proxy) => {
    const haystack = [
      proxy.name,
      proxy.proxy,
      proxy.status,
      proxy.latency,
    ].join(' ').toLowerCase()
    return haystack.includes(normalizedSearchQuery.value)
  })
})

const filteredAutomationSessions = computed(() => {
  if (!normalizedSearchQuery.value) return automationSessions.value
  return automationSessions.value.filter((session) => {
    const haystack = [
      session.profile_name,
      session.profile_id,
      shortId(session.profile_id),
      session.connect_url,
      session.protocol,
      session.status,
      String(session.debug_port),
      String(session.pid || ''),
    ].join(' ').toLowerCase()
    return haystack.includes(normalizedSearchQuery.value)
  })
})

const filteredLogs = computed(() => {
  if (!normalizedSearchQuery.value) return logs.value
  return logs.value.filter((log) => {
    const haystack = [
      log.time,
      log.level,
      log.message,
    ].join(' ').toLowerCase()
    return haystack.includes(normalizedSearchQuery.value)
  })
})

const pythonAutomationSnippet = `import json
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
`

const curlAutomationSnippet = computed(() => {
  const baseURL = automationInfo.value.base_url || 'http://127.0.0.1:9090'

  return `curl -X POST "${baseURL}/api/v1/automation/sessions" \\
  -H "Authorization: Bearer YOUR_LOCAL_API_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d "{\\"profile_id\\":\\"YOUR_PROFILE_ID\\"}"`
})

const fetchAutomationState = async () => {
  try {
    const [info, sessions, token] = await Promise.all([
      GetAutomationInfo(),
      GetAutomationSessions(),
      GetAutomationToken(),
    ])
    automationInfo.value = info
    automationSessions.value = sessions
    automationToken.value = token
  } catch (err) {
    console.error('获取自动化状态失败:', err)
  }
}

const startAutomationPolling = () => {
  if (automationPollTimer) return
  automationPollTimer = setInterval(() => {
    if (currentView.value === 'automation') {
      fetchAutomationState()
    }
  }, 3000)
}

const stopAutomationPolling = () => {
  if (automationPollTimer) {
    clearInterval(automationPollTimer)
    automationPollTimer = null
  }
}

watch(() => currentView.value, (view) => {
  if (view === 'automation') {
    fetchAutomationState()
    startAutomationPolling()
  } else {
    stopAutomationPolling()
  }
})

const copyText = async (text, label) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    alert(`${label} 已复制到剪贴板`)
  } catch (err) {
    window.prompt(`请手动复制${label}:`, text)
  }
}

const handleStartAutomation = async (profileID = selectedAutomationProfileId.value, startURL = automationStartURL.value) => {
  if (!automationInfo.value.enabled) return alert('请先启用自动化控制台')
  if (!profileID) return alert('请先选择一个环境')
  const existingSession = getAutomationSessionForProfile(profileID)
  automationLoading.value = true
  try {
    const session = await StartAutomationSession(profileID, startURL)
    currentView.value = 'automation'
    automationStartURL.value = ''
    await fetchAutomationState()
    if (existingSession) {
      alert(`该环境已存在活动自动化会话，已直接复用\nBiDi 地址: ${session.connect_url}`)
    } else {
      alert(`自动化会话已启动\nBiDi 地址: ${session.connect_url}`)
    }
  } catch (err) {
    alert('自动化启动失败: ' + err)
  } finally {
    automationLoading.value = false
  }
}

const handleStopAutomation = async (sessionID) => {
  if (!confirm('这会关闭该自动化浏览器窗口，并同步保存当前 Cookie。是否继续？')) return
  try {
    await StopAutomationSession(sessionID)
    setTimeout(fetchAutomationState, 300)
  } catch (err) {
    alert('停止自动化会话失败: ' + err)
  }
}

const handleRotateAutomationToken = async () => {
  if (!confirm('轮换后旧 token 会立即失效，脚本需要改用新 token。是否继续？')) return
  try {
    automationToken.value = await RotateAutomationToken()
    await fetchAutomationState()
    alert('本地 API token 已轮换')
  } catch (err) {
    alert('轮换 token 失败: ' + err)
  }
}

const handleToggleAutomation = async () => {
  const nextEnabled = !automationInfo.value.enabled
  if (!nextEnabled && !confirm('停用后本地自动化 API 将停止监听，新的脚本将无法接入。是否继续？')) return

  try {
    await SetAutomationEnabled(nextEnabled)
    await fetchAutomationState()
    alert(nextEnabled ? '自动化控制台已启用' : '自动化控制台已停用')
  } catch (err) {
    alert((nextEnabled ? '启用' : '停用') + '自动化控制台失败: ' + err)
  }
}

const getAutomationSessionForProfile = (profileID) => {
  return automationSessions.value.find((session) => session.profile_id === profileID)
}

const shortId = (value) => {
  if (!value) return ''
  return value.slice(0, 8)
}

const handleLaunch = async (id, url = "") => {
  const finalURL = pendingURL.value || url
  try {
    await LaunchBrowser(id, finalURL)
    if (pendingURL.value) {
      pendingURL.value = '' // 启动后清除任务
    }
  } catch (err) {
    alert('启动失败: ' + err)
  }
}

const handleVerify = (id) => {
  handleLaunch(id, "https://pixelscan.net")
}

const handleSyncCookies = async (id) => {
  try {
    await SyncCookies(id)
    alert('同步成功！已保存登录状态。')
    fetchProfiles()
  } catch (err) {
    alert('同步出错: ' + err)
  }
}

const handleResetCookies = async (id) => {
  if (!confirm('确定要重置 Cookie 吗？这会清空已保存的数据并物理删除登录文件。')) return
  try {
    await ResetCookies(id)
    alert('重置成功！')
    fetchProfiles()
  } catch (err) {
    alert('重置失败: ' + err)
  }
}

// 监听代理类型切换，自动预设 Clash 端口
watch(() => newProfile.value.proxyType, (newVal) => {
  if (selectedProxyId.value === '') {
    if (newVal === 'http://') {
      newProfile.value.proxyAddr = '127.0.0.1:7890'
    } else {
      newProfile.value.proxyAddr = '127.0.0.1:7891'
    }
  }
})

const onProxySelect = () => {
  if (selectedProxyId.value) {
    const p = proxies.value.find(px => px.id === selectedProxyId.value)
    if (p) {
      if (p.proxy.includes('://')) {
        let parts = p.proxy.split('://')
        newProfile.value.proxyType = parts[0] + '://'
        newProfile.value.proxyAddr = parts[1]
      } else {
        newProfile.value.proxyType = 'http://'
        newProfile.value.proxyAddr = p.proxy
      }
    }
  }
}

const onEditProxySelect = () => {
  if (editingProxyId.value && editingProfile.value) {
    const p = proxies.value.find(px => px.id === editingProxyId.value)
    if (p) {
      editingProfile.value.proxy = p.proxy
    }
  }
}

const handleCreate = async () => {
  if (!newProfile.value.name) return alert('请输入环境名称')
  const fullProxy = newProfile.value.proxyAddr ? newProfile.value.proxyType + newProfile.value.proxyAddr : ''
  try {
    await CreateProfile(newProfile.value.name, fullProxy, newProfile.value.ua, newProfile.value.startUrl)
    showCreateModal.value = false
    resetNewProfile()
    fetchProfiles()
  } catch (err) {
    alert('创建失败: ' + err)
  }
}

const resetNewProfile = () => {
    selectedProxyId.value = ''
    newProfile.value = { 
        name: '', 
        proxyType: 'socks5://', 
        proxyAddr: '127.0.0.1:7891', 
        startUrl: '',
        ua: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0' 
    }
}

const handleDelete = async (id) => {
  if (!confirm('确定删除吗？该操作不可撤销。')) return
  try {
    await DeleteProfile(id)
    fetchProfiles()
  } catch (err) {
    alert('删除失败: ' + err)
  }
}

const openSettings = (profile) => {
  editingProfile.value = JSON.parse(JSON.stringify(profile))
  proxyTestResult.value = ''
  
  // 尝试匹配已有代理池条目
  editingProxyId.value = ''
  const matchedProxy = proxies.value.find(p => p.proxy === editingProfile.value.proxy)
  if (matchedProxy) {
    editingProxyId.value = matchedProxy.id
  }
  
  showSettingsModal.value = true
}

const saveSettings = async () => {
  try {
    await UpdateProfile(editingProfile.value)
    showSettingsModal.value = false
    fetchProfiles()
  } catch (err) {
    alert('保存失败: ' + err)
  }
}

const handleTestProxy = async (proxyStr) => {
  testingProxy.value = true
  proxyTestResult.value = '正在测试...'
  try {
    const res = await TestProxy(proxyStr)
    proxyTestResult.value = res
  } catch (err) {
    proxyTestResult.value = '连接失败: ' + err
  } finally {
    testingProxy.value = false
  }
}

const openCookieEditor = (profile) => {
  editingProfile.value = profile
  try {
    cookieJson.value = JSON.stringify(JSON.parse(profile.cookies || '[]'), null, 2)
  } catch (e) {
    cookieJson.value = profile.cookies || '[]'
  }
  showCookieModal.value = true
}

const saveCookies = async () => {
  try {
    JSON.parse(cookieJson.value)
    editingProfile.value.cookies = cookieJson.value
    await UpdateProfile(editingProfile.value)
    showCookieModal.value = false
  } catch (err) {
    alert('JSON 格式错误')
  }
}

const fetchProxies = async () => {
  try {
    const res = await GetProxies()
    proxies.value = res
  } catch (err) {
    console.error('获取代理失败:', err)
  }
}

const handleAddProxy = async () => {
  if (!newProxy.value.name || !newProxy.value.addr) return alert('请填写完整信息')
  try {
    await AddProxy(newProxy.value.name, newProxy.value.addr)
    newProxy.value = { name: '', addr: '' }
    fetchProxies()
  } catch (err) {
    alert('添加失败: ' + err)
  }
}

const handleDeleteProxy = async (id) => {
  if (!confirm('确定删除该代理吗？')) return
  try {
    await DeleteProxy(id)
    fetchProxies()
  } catch (err) {
    alert('删除失败: ' + err)
  }
}

const handleTestProxyEntry = async (id) => {
  try {
    await TestProxyEntry(id)
    fetchProxies()
  } catch (err) {
    console.warn('测试过程出现连接错误')
    fetchProxies() // 依然更新状态以显示 offline
  }
}

const handleExportCookies = async (id) => {
  try {
    await ExportCookies(id)
  } catch (err) {
    if (err) alert('导出取消或失败: ' + err)
  }
}

const handleExportProfile = async (id) => {
  try {
    await ExportProfile(id)
    alert('环境打包成功！')
  } catch (err) {
    if (err) alert('导出取消或失败: ' + err)
  }
}

const handleImportProfile = async () => {
  try {
    await ImportProfile()
    fetchProfiles()
    alert('环境导入成功！')
  } catch (err) {
    if (err) alert('导入取消或失败: ' + err)
  }
}

const handleImportFromFile = async () => {
  try {
    const content = await ImportCookiesFromFile()
    if (content) {
      cookieJson.value = content
    }
  } catch (err) {
    if (err) alert('读取失败: ' + err)
  }
}

const handleRegisterBrowser = async () => {
  try {
    const res = await RegisterAsDefaultBrowser()
    let tip = res + '\n\n提示：某些第三方浏览器管理器也会扫描到 MyBrowser。'
    tip += '\n\n【重要】如果您处于开发模式(dev)，注册路径是临时的。建议 build 正式版后再执行注册。'
    if (confirm(tip + '\n\n是否立即打开 Windows 默认应用设置页进行确认？')) {
        await OpenDefaultAppsSettings()
    }
  } catch (err) {
    alert('注册失败 (建议检查是否被杀毒软件拦截): ' + err)
  }
}

const handleCreateDesktopShortcut = async () => {
  try {
    await CreateDesktopShortcut()
    alert('桌面快捷方式已生成！赶紧去桌面看看吧 🚀')
  } catch (err) {
    alert('生成快捷方式失败: ' + err)
  }
}

const handleOpenDataDirectory = async () => {
  try {
    await OpenDataDirectory()
  } catch (err) {
    alert('打开数据目录失败: ' + err)
  }
}

const handleUnregisterBrowser = async () => {
  if (!confirm('这会清理 MyBrowser 写入的浏览器注册表项，但不会删除你的数据目录。是否继续？')) return
  try {
    const res = await UnregisterAsDefaultBrowser()
    if (confirm(res + '\n\n是否立即打开 Windows 默认应用设置页进行确认？')) {
      await OpenDefaultAppsSettings()
    }
  } catch (err) {
    alert('清理注册失败: ' + err)
  }
}

onMounted(async () => {
  fetchProfiles()
  fetchProxies()
  fetchAutomationState()
  
  // 检查是否有待启动的外部 URL
  try {
    const url = await GetStartupURL()
    if (url) {
        pendingURL.value = url
    }
  } catch(e) {}

  try {
    storageDir.value = await GetStorageDirectory()
    storageMode.value = await GetStorageMode()
  } catch(e) {}
  
  // 监听后端日志事件
  EventsOn("log_update", (log) => {
    logs.value.push(log)
    // 限制日志条数，防止内存溢出
    if (logs.value.length > 500) logs.value.shift()
    
    // 自动滚动到底部
    setTimeout(() => {
      if (logContainer.value) {
        logContainer.value.scrollTop = logContainer.value.scrollHeight
      }
    }, 50)
  })

  // 监听来自其他实例的新链接 (单实例消息)
  EventsOn("external_url_received", (url) => {
    pendingURL.value = url
  })
})

onUnmounted(() => {
  stopAutomationPolling()
})
</script>

<template>
  <div class="app-layout">
    <div class="glass-bg"></div>

    <aside class="sidebar glass">
      <div class="logo">
        <div class="dot pulse"></div>
        <h1>MyBrowser</h1>
      </div>
      <nav class="nav-links">
        <div class="nav-item" :class="{ active: currentView === 'profiles' }" @click="currentView = 'profiles'">🏠 环境列表</div>
        <div class="nav-item" :class="{ active: currentView === 'proxies' }" @click="currentView = 'proxies'">🛡️ 代理池</div>
        <div class="nav-item" :class="{ active: currentView === 'logs' }" @click="currentView = 'logs'">📊 运行日志</div>
        <div class="nav-item" :class="{ active: currentView === 'automation' }" @click="currentView = 'automation'">🧰 自动化控制台</div>
        <div class="nav-item register-btn" @click="handleRegisterBrowser">🔗 设为默认浏览器</div>
        <div class="nav-item register-btn" @click="handleCreateDesktopShortcut" style="margin-top: 10px;">📌 存到桌面快速打开</div>
        <div class="nav-item register-btn" @click="handleOpenDataDirectory" style="margin-top: 10px;">📂 打开数据目录</div>
        <div class="nav-item register-btn warn" @click="handleUnregisterBrowser" style="margin-top: 10px;">🧹 清理浏览器注册</div>
      </nav>
      <div class="privacy-note">
        <p>🔒 <b>隐私声明</b></p>
        <p v-if="storageMode === 'portable'">当前为便携模式，数据保存在程序同目录的 `MyBrowserData`。</p>
        <p v-else>当前为本地模式，数据保存在 `%LOCALAPPDATA%\MyBrowser`。</p>
        <p class="privacy-tip">如检测到旧版 `data` 目录，应用会自动迁移并保留旧目录供核对。</p>
        <p v-if="storageDir"><code>{{ storageDir }}</code></p>
      </div>
    </aside>

    <main class="main-content">
      <header class="top-bar glass">
        <div class="search-box">
          <input v-model="searchQuery" type="text" :placeholder="searchPlaceholder" class="search-input" />
        </div>
        <div class="actions">
           <button v-if="currentView === 'profiles'" @click="handleImportProfile" class="btn-ghost" style="margin-right: 8px;">📥 导入包</button>
           <button v-if="currentView === 'profiles'" @click="showCreateModal = true" class="btn-create">+ 新建环境</button>
           <div v-else-if="currentView === 'proxies'" class="proxy-add-form">
              <input v-model="newProxy.name" placeholder="代理名称" />
              <input v-model="newProxy.addr" placeholder="socks5://1.2.3.4:7891" />
              <button @click="handleAddProxy" class="btn-create add">确认添加</button>
           </div>
           <button v-else-if="currentView === 'automation'" @click="fetchAutomationState" class="btn-ghost">刷新状态</button>
           <button v-else-if="currentView === 'logs'" @click="logs = []" class="btn-ghost">清空日志</button>
        </div>
      </header>

      <div class="content-body">
        <div v-if="pendingURL" class="pending-url-banner glass">
           <div class="banner-content">
              <span class="icon">🔗</span>
              <span class="text">检测到外部链接：<code>{{ pendingURL }}</code></span>
              <span class="tip">请点击下方环境的“启动”按钮，在指定环境中打开此链接。</span>
           </div>
           <button @click="pendingURL = ''" class="btn-close">✕</button>
        </div>

        <div v-if="loading" class="loader-wrap">
          <div class="spinner"></div>
        </div>

        <template v-else>
          <!-- 1. 环境列表视图 -->
          <div v-if="currentView === 'profiles'" class="grid-view">
            <div v-for="p in filteredProfiles" :key="p.id" class="card glass">
              <div class="card-head">
                <div class="info">
                  <h3>{{ p.name }}</h3>
                  <div class="profile-id-row">
                    <code>{{ shortId(p.id) }}</code>
                    <button @click="copyText(p.id, 'Profile ID')" class="btn-inline-copy">复制 ID</button>
                  </div>
                </div>
                <div class="card-ops">
                   <button @click="handleExportProfile(p.id)" class="btn-small" title="打包导出 (.mbp)">📦</button>
                   <button @click="openSettings(p)" class="btn-small">⚙️</button>
                   <button @click="handleDelete(p.id)" class="btn-small del">🗑️</button>
                </div>
              </div>

              <div class="card-body">
                <div class="data-row">
                  <span class="label">代理:</span>
                  <span class="val">{{ p.proxy || '直连' }}</span>
                </div>
                <div class="data-row">
                  <span class="label">内核:</span>
                  <span class="val">Camoufox (Firefox)</span>
                </div>
                <div class="data-row">
                  <span class="label">默认页:</span>
                  <span class="val start-url">{{ p.start_url || '新标签页' }}</span>
                </div>
                <div class="data-row" v-if="getAutomationSessionForProfile(p.id)">
                  <span class="label">自动化:</span>
                  <span class="val automation-active">运行中 · {{ getAutomationSessionForProfile(p.id).debug_port }}</span>
                </div>
              </div>

              <div class="card-foot">
                <div class="btn-group-main">
                  <button @click="handleLaunch(p.id)" class="btn-launch">🚀 启动环境</button>
                  <button @click="handleVerify(p.id)" class="btn-verify" title="指纹验证">🔍</button>
                </div>
                <div class="btn-group-sub">
                  <button @click="handleStartAutomation(p.id, '')" class="btn-action-ghost automation-action">自动化启动</button>
                  <button @click="handleSyncCookies(p.id)" class="btn-action-ghost">💾 同步</button>
                  <button @click="handleExportCookies(p.id)" class="btn-action-ghost">📤 导出</button>
                  <button @click="handleResetCookies(p.id)" class="btn-action-ghost warn">🧹 重置</button>
                  <button @click="openCookieEditor(p)" class="btn-action-ghost">📂 Cookie</button>
                </div>
              </div>
            </div>
            <div v-if="filteredProfiles.length === 0" class="empty-state glass">没有匹配到环境。</div>
          </div>

          <!-- 2. 代理池视图 -->
          <div v-else-if="currentView === 'proxies'" class="list-view">
            <table class="proxy-table glass">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>解析地址</th>
                  <th>状态</th>
                  <th>延迟</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="px in filteredProxies" :key="px.id">
                  <td>{{ px.name }}</td>
                  <td><code>{{ px.proxy }}</code></td>
                  <td>
                    <span :class="['status-dot', px.status]"></span>
                    {{ px.status === 'online' ? '在线' : px.status === 'offline' ? '离线' : '未知' }}
                  </td>
                  <td>{{ px.latency }}</td>
                  <td>
                    <button @click="handleTestProxyEntry(px.id)" class="btn-icon">⚡</button>
                    <button @click="handleDeleteProxy(px.id)" class="btn-icon del">🗑️</button>
                  </td>
                </tr>
                <tr v-if="filteredProxies.length === 0">
                  <td colspan="5" class="empty-table">没有匹配到代理。</td>
                </tr>
              </tbody>
            </table>
          </div>

          <!-- 3. 运行日志视图 -->
          <div v-else-if="currentView === 'automation'" class="automation-view">
            <div class="automation-grid">
              <section class="automation-card glass">
                <div class="automation-card-head">
                  <h3>本地自动化控制台</h3>
                  <button @click="handleToggleAutomation" class="status-chip status-chip-button" :class="{ online: automationInfo.enabled }">
                    {{ automationInfo.enabled ? '已启用' : '已关闭' }}
                  </button>
                </div>
                <div class="automation-meta">
                  <div class="data-row">
                    <span class="label">监听地址:</span>
                    <code>{{ automationInfo.enabled ? (automationInfo.listen_addr || '127.0.0.1:9090') : '未监听' }}</code>
                  </div>
                  <div class="data-row">
                    <span class="label">协议:</span>
                    <span class="val">{{ automationInfo.protocol?.toUpperCase() || 'BIDI' }}</span>
                  </div>
                  <div class="data-row">
                    <span class="label">鉴权:</span>
                    <span class="val">{{ automationInfo.auth_scheme }} Token</span>
                  </div>
                  <div class="data-row">
                    <span class="label">活动会话:</span>
                    <span class="val">{{ automationInfo.session_count }}</span>
                  </div>
                  <div class="data-row">
                    <span class="label">Token:</span>
                    <code>{{ tokenPreview }}</code>
                  </div>
                </div>
                <div class="btn-group-sub">
                  <button @click="copyText(automationInfo.base_url, 'API 地址')" class="btn-action-ghost">📋 复制 API</button>
                  <button @click="copyText(automationToken, 'Token')" class="btn-action-ghost">🔐 复制 Token</button>
                  <button @click="handleRotateAutomationToken" class="btn-action-ghost warn">🔄 轮换 Token</button>
                </div>
              </section>

              <section class="automation-card glass">
                <div class="automation-card-head">
                  <h3>快捷启动</h3>
                  <span class="status-chip subtle">BiDi</span>
                </div>
                <div class="field">
                  <label>选择环境</label>
                  <select v-model="selectedAutomationProfileId">
                    <option value="">-- 请选择环境 --</option>
                    <option v-for="p in filteredProfiles" :key="p.id" :value="p.id">{{ p.name }} · {{ shortId(p.id) }}</option>
                  </select>
                </div>
                <div class="field">
                  <label>启动地址（可选）</label>
                  <input v-model="automationStartURL" placeholder="例如 https://example.com" />
                  <span class="hint">留空则沿用该环境的默认标签页。</span>
                </div>
                <button @click="handleStartAutomation()" class="btn-solid" :disabled="automationLoading || !automationInfo.enabled">
                  {{ automationLoading ? '启动中...' : '启动自动化会话' }}
                </button>
                <span class="hint" v-if="!automationInfo.enabled">请先在上方启用自动化控制台。</span>
              </section>

              <section class="automation-card glass automation-wide">
                <div class="automation-card-head">
                  <h3>会话列表</h3>
                  <span class="status-chip subtle">{{ automationSessions.length }} 个会话</span>
                </div>
                <div v-if="filteredAutomationSessions.length === 0" class="automation-empty">当前没有匹配到自动化会话。</div>
                <div v-else class="automation-session-list">
                  <div v-for="session in filteredAutomationSessions" :key="session.session_id" class="automation-session-row">
                    <div class="automation-session-main">
                      <div class="automation-session-title">
                        <strong>{{ session.profile_name }}</strong>
                        <span class="status-chip" :class="{ online: session.status === 'running' }">{{ session.status }}</span>
                      </div>
                      <div class="automation-session-meta">
                        <span>PID {{ session.pid || '-' }}</span>
                        <span>端口 {{ session.debug_port }}</span>
                        <span>{{ session.protocol?.toUpperCase() || 'BIDI' }}</span>
                        <span>ID {{ shortId(session.profile_id) }}</span>
                      </div>
                      <code class="session-url">{{ session.connect_url }}</code>
                    </div>
                    <div class="automation-session-actions">
                      <button @click="copyText(session.profile_id, 'Profile ID')" class="btn-action-ghost">🆔 复制 ID</button>
                      <button @click="copyText(session.connect_url, 'BiDi 地址')" class="btn-action-ghost">📋 复制地址</button>
                      <button @click="handleStopAutomation(session.session_id)" class="btn-action-ghost warn">⏹ 停止</button>
                    </div>
                  </div>
                </div>
              </section>

              <section class="automation-card glass automation-wide">
                <div class="automation-card-head">
                  <h3>接入示例</h3>
                  <span class="status-chip subtle">Python / cURL</span>
                </div>
                <div class="automation-example">
                  <div class="example-block">
                    <div class="example-head">
                      <span>Python websocket-client BiDi</span>
                      <button @click="copyText(pythonAutomationSnippet, 'Python 示例')" class="btn-action-ghost">📋 复制</button>
                    </div>
                    <pre class="automation-snippet">{{ pythonAutomationSnippet }}</pre>
                  </div>
                  <div class="example-block">
                    <div class="example-head">
                      <span>cURL</span>
                      <button @click="copyText(curlAutomationSnippet, 'cURL 示例')" class="btn-action-ghost">📋 复制</button>
                    </div>
                    <pre class="automation-snippet">{{ curlAutomationSnippet }}</pre>
                  </div>
                </div>
              </section>
            </div>
          </div>

          <!-- 4. 运行日志视图 -->
          <div v-else-if="currentView === 'logs'" class="logs-view glass" ref="logContainer">
            <div v-for="(log, idx) in filteredLogs" :key="idx" :class="['log-line', log.level]">
              <span class="time">[{{ log.time }}]</span>
              <span class="level">[{{ log.level.toUpperCase() }}]</span>
              <span class="msg">{{ log.message }}</span>
            </div>
            <div v-if="filteredLogs.length === 0" class="empty-logs">{{ logs.length === 0 ? '暂无运行数据...' : '没有匹配到日志。' }}</div>
          </div>
        </template>
      </div>
    </main>

    <!-- Modal: Create -->
    <Transition name="fade">
      <div v-if="showCreateModal" class="modal-backdrop" @click.self="showCreateModal = false">
        <div class="modal glass">
          <div class="modal-header">
            <h3>创建新环境</h3>
          </div>
          <div class="modal-content">
            <div class="field">
              <label>环境名称</label>
              <input v-model="newProfile.name" placeholder="设置一个好记的名字" />
            </div>
            <div class="field">
              <label>代理设置</label>
              <select v-model="selectedProxyId" @change="onProxySelect" style="margin-bottom: 8px;">
                <option value="">-- 自定义手填地址 --</option>
                <option v-for="px in proxies" :key="px.id" :value="px.id">{{ px.name }} ({{ px.proxy }})</option>
              </select>
              <div class="proxy-inputs" v-if="selectedProxyId === ''">
                <select v-model="newProfile.proxyType">
                  <option value="http://">HTTP</option>
                  <option value="socks5://">SOCKS5</option>
                </select>
                <input v-model="newProfile.proxyAddr" placeholder="127.0.0.1:7891" />
              </div>
              <span class="hint" v-if="selectedProxyId === ''">Clash 默认一般为 HTTP(7890) 或 SOCKS5(7891)</span>
            </div>
            <div class="field">
              <label>默认标签页</label>
              <input v-model="newProfile.startUrl" placeholder="例如 google.com 或 https://chatgpt.com" />
              <span class="hint">留空则保持浏览器默认新标签页；未写协议时会自动补全为 https://</span>
            </div>
          </div>
          <div class="modal-footer">
            <button @click="showCreateModal = false" class="btn-ghost">取消</button>
            <button @click="handleCreate" class="btn-solid">立即创建</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Modal: Settings -->
    <Transition name="fade">
      <div v-if="showSettingsModal" class="modal-backdrop" @click.self="showSettingsModal = false">
        <div class="modal glass">
          <div class="modal-header">
            <h3>环境设置 - {{ editingProfile?.name }}</h3>
          </div>
          <div class="modal-content">
            <div class="field">
               <label>环境名称</label>
               <input v-model="editingProfile.name" />
            </div>
            <div class="field">
              <label>代理地址 (完整格式)</label>
              <select v-model="editingProxyId" @change="onEditProxySelect" style="margin-bottom: 8px;">
                <option value="">-- 自定义手填地址 --</option>
                <option v-for="px in proxies" :key="px.id" :value="px.id">{{ px.name }} ({{ px.proxy }})</option>
              </select>
              <div class="proxy-test-box">
                <input v-model="editingProfile.proxy" placeholder="socks5://127.0.0.1:7891" :disabled="editingProxyId !== ''" />
                <button @click="handleTestProxy(editingProfile.proxy)" :disabled="testingProxy" class="btn-test">
                  {{ testingProxy ? '...' : '测试' }}
                </button>
              </div>
              <div v-if="proxyTestResult" class="test-res" :class="{ ok: proxyTestResult.includes('成功') }">
                {{ proxyTestResult }}
              </div>
            </div>
            <div class="field">
              <label>默认标签页</label>
              <input v-model="editingProfile.start_url" placeholder="例如 google.com 或 https://chatgpt.com" />
              <span class="hint">普通启动时会优先打开这里；外部链接拉起和指纹验证仍会覆盖它。</span>
            </div>
          </div>
          <div class="modal-footer">
            <button @click="showSettingsModal = false" class="btn-ghost">取消</button>
            <button @click="saveSettings" class="btn-solid">保存变更</button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Modal: Cookie -->
    <Transition name="fade">
      <div v-if="showCookieModal" class="modal-backdrop" @click.self="showCookieModal = false">
        <div class="modal glass wide">
          <div class="modal-header">
            <h3>Cookie 管理 - {{ editingProfile?.name }}</h3>
          </div>
          <div class="modal-content">
             <textarea v-model="cookieJson" class="editor"></textarea>
          </div>
          <div class="modal-footer">
            <button @click="handleImportFromFile" class="btn-ghost" style="margin-right: auto;">📂 导入 JSON 文件</button>
            <button @click="showCookieModal = false" class="btn-ghost">关闭</button>
            <button @click="saveCookies" class="btn-solid">保存数据</button>
          </div>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style>
:root {
  --primary: #38bdf8;
  --bg: #030712;
  --glass: rgba(255, 255, 255, 0.03);
  --border: rgba(255, 255, 255, 0.08);
  --text: #f3f4f6;
  --text-dim: #9ca3af;
  --scrollbar-track: rgba(15, 23, 42, 0.72);
  --scrollbar-thumb: rgba(56, 189, 248, 0.55);
  --scrollbar-thumb-hover: rgba(125, 211, 252, 0.82);
}

* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: 'Inter', system-ui, sans-serif; background: var(--bg); color: var(--text); overflow: hidden; }

.app-layout { display: flex; height: 100vh; position: relative; }
.glass-bg { position: absolute; inset: 0; background: radial-gradient(circle at 80% 20%, rgba(56, 189, 248, 0.05) 0%, transparent 50%); pointer-events: none; }

.sidebar { width: 280px; padding: 26px 24px; display: flex; flex-direction: column; gap: 24px; border-right: 1px solid var(--border); overflow-y: auto; }
.logo { display: flex; align-items: center; gap: 12px; }
.logo h1 { font-size: 1.4rem; font-weight: 800; letter-spacing: -0.5px; }
.dot.pulse { width: 12px; height: 12px; background: var(--primary); border-radius: 50%; box-shadow: 0 0 15px var(--primary); }

.nav-links { display: flex; flex-direction: column; gap: 10px; }
.nav-item { padding: 12px 16px; border-radius: 12px; cursor: pointer; color: var(--text-dim); transition: 0.2s; }
.nav-item.active { background: var(--glass); color: var(--primary); font-weight: 600; }
.nav-item:hover:not(.active) { background: rgba(255,255,255,0.01); color: var(--text); }
.nav-item.register-btn { margin-top: 10px; border: 1px dashed var(--border); color: var(--primary); text-align: center; font-size: 0.85rem; }
.nav-item.register-btn:hover { border-color: var(--primary); background: var(--glass); }
.nav-item.register-btn.warn { color: #fbbf24; }
.nav-item.register-btn.warn:hover { border-color: rgba(251, 191, 36, 0.7); color: #fde68a; }

.privacy-note { margin-top: auto; padding-top: 14px; border-top: 1px solid rgba(255,255,255,0.06); font-size: 0.74rem; color: var(--text-dim); line-height: 1.55; }
.privacy-note p + p { margin-top: 6px; }
.privacy-note .privacy-tip { color: #8ea0b8; }
.privacy-note code { display: block; margin-top: 8px; padding: 6px 8px; border-radius: 8px; background: rgba(0, 0, 0, 0.25); color: var(--text); word-break: break-all; max-height: 58px; overflow-y: auto; }

.main-content { flex: 1; display: flex; flex-direction: column; padding: 30px; }
.top-bar { padding: 16px 24px; border-radius: 16px; display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; border: 1px solid var(--border); gap: 16px; }
.search-box { flex: 1; max-width: 400px; display: flex; align-items: center; }
.search-input { background: none; border: none; color: white; width: 100%; outline: none; font-size: 0.9rem; }

.btn-create { background: var(--primary); color: #000; padding: 10px 24px; border-radius: 10px; font-weight: 700; white-space: nowrap; flex-shrink: 0; }

.grid-view { display: grid; grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); gap: 24px; overflow-y: auto; padding-right: 10px; flex: 1; min-height: 0; }
.list-view { flex: 1; overflow-y: auto; min-height: 0; }
.content-body { flex: 1; display: flex; flex-direction: column; min-height: 0; }
.card { padding: 24px; border-radius: 20px; border: 1px solid var(--border); display: flex; flex-direction: column; gap: 16px; transition: 0.3s; }
.card:hover { border-color: rgba(56, 189, 248, 0.3); transform: translateY(-3px); }
.empty-state { display: flex; align-items: center; justify-content: center; min-height: 180px; border-radius: 20px; border: 1px dashed var(--border); color: var(--text-dim); }

.card-head { display: flex; justify-content: space-between; align-items: flex-start; }
.card-head h3 { font-size: 1.1rem; }
.card-head code { font-size: 0.7rem; color: var(--text-dim); }
.profile-id-row { display: flex; align-items: center; gap: 8px; margin-top: 6px; }
.btn-inline-copy { border: none; background: rgba(56, 189, 248, 0.12); color: var(--primary); font-size: 0.7rem; padding: 4px 8px; border-radius: 999px; cursor: pointer; }
.btn-inline-copy:hover { background: rgba(56, 189, 248, 0.2); }
.btn-small { background: var(--glass); color: var(--text-dim); border-radius: 8px; width: 32px; height: 32px; font-size: 0.8rem; }
.btn-small.del:hover { color: #ef4444; }

.data-row { display: flex; justify-content: space-between; font-size: 0.85rem; }
.label { color: var(--text-dim); }

.btn-group-main { display: flex; gap: 8px; }
.btn-launch { flex: 1; background: linear-gradient(135deg, #38bdf8, #0ea5e9); color: black; padding: 12px; border-radius: 12px; font-weight: 800; }
.btn-verify { width: 50px; background: var(--glass); border: 1px solid var(--border); border-radius: 12px; font-size: 1.1rem; }

.btn-group-sub { display: flex; gap: 8px; }
.btn-action-ghost { flex: 1; background: none; border: 1px solid var(--border); padding: 8px; border-radius: 10px; font-size: 0.75rem; color: var(--text-dim); }
.btn-action-ghost:hover { background: var(--glass); color: var(--text); }
.btn-action-ghost.warn:hover { color: #f87171; border-color: rgba(248, 113, 113, 0.5); }
.btn-action-ghost.automation-action:hover { color: #7dd3fc; border-color: rgba(56, 189, 248, 0.4); }

/* Modal */
.modal-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.7); backdrop-filter: blur(8px); display: flex; align-items: center; justify-content: center; z-index: 100; }
.modal { width: 480px; padding: 32px; border-radius: 24px; border: 1px solid var(--border); display: flex; flex-direction: column; gap: 24px; }
.modal.wide { width: 800px; }

.field { display: flex; flex-direction: column; gap: 8px; text-align: left; }
.field label { font-size: 0.85rem; font-weight: 600; color: var(--text-dim); }
.field input, .field select, .field textarea { background: var(--glass); border: 1px solid var(--border); border-radius: 10px; padding: 12px; color: white; outline: none; }
select {
  color: var(--text);
  background-color: #0f172a;
}
select option {
  color: #000;
  background: #fff;
}

.proxy-inputs, .proxy-test-box { display: flex; gap: 8px; }
.field select,
.proxy-inputs select {
  appearance: none;
  background-image: url("data:image/svg+xml;charset=UTF-8,%3csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='white'%3e%3cpath d='M7 10l5 5 5-5z'/%3e%3c/svg%3e");
  background-repeat: no-repeat;
  background-position: right 10px center;
  background-size: 16px;
  padding-right: 34px;
}
.proxy-inputs select {
  width: 100px; 
}
.proxy-inputs input, .proxy-test-box input { flex: 1; }
.btn-test { background: var(--primary); color: black; padding: 0 15px; border-radius: 10px; font-weight: 600; }

.hint { font-size: 0.7rem; color: var(--text-dim); }
.test-res { font-size: 0.8rem; color: #ef4444; }
.test-res.ok { color: #10b981; }

.modal-footer { display: flex; justify-content: flex-end; gap: 12px; }
.btn-ghost { padding: 12px 20px; color: var(--text-dim); }
.btn-solid { background: var(--primary); color: black; padding: 12px 28px; border-radius: 12px; font-weight: 700; }

.editor { width: 100%; height: 400px; background: #000; color: #10b981; font-family: 'JetBrains Mono', monospace; padding: 20px; border-radius: 12px; font-size: 0.9rem; }

.glass { background: var(--glass); backdrop-filter: blur(20px); }

.fade-enter-active, .fade-leave-active { transition: 0.3s; }
.fade-enter-from, .fade-leave-to { opacity: 0; transform: scale(0.95); }

/* Proxy Table Styles */
.proxy-table { width: 100%; border-collapse: collapse; text-align: left; border-radius: 12px; overflow: hidden; }
.proxy-table th, .proxy-table td { padding: 16px; border-bottom: 1px solid var(--border); }
.proxy-table th { background: rgba(255,255,255,0.02); font-size: 0.85rem; color: var(--text-dim); }
.proxy-table td code { color: var(--primary); }
.empty-table { text-align: center; color: var(--text-dim); padding: 28px 16px; }

.status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 8px; background: #9ca3af; }
.status-dot.online { background: #10b981; box-shadow: 0 0 10px #10b981; }
.status-dot.offline { background: #ef4444; }

.btn-icon { background: none; border: none; font-size: 1.1rem; cursor: pointer; padding: 4px 8px; }
.btn-icon.del:hover { color: #ef4444; }

.proxy-add-form { display: flex; gap: 8px; flex-shrink: 1; min-width: 0; }
.proxy-add-form input { background: var(--glass); border: 1px solid var(--border); border-radius: 8px; padding: 8px 12px; color: white; width: 160px; min-width: 80px; flex: 1; }
.btn-create.add { padding: 8px 16px; font-size: 0.85rem; }

.automation-view { flex: 1; overflow-y: auto; min-height: 0; padding-right: 10px; }
.automation-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.automation-card { padding: 24px; border-radius: 20px; border: 1px solid var(--border); display: flex; flex-direction: column; gap: 18px; }
.automation-wide { grid-column: 1 / -1; }
.automation-card-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.automation-card-head h3 { font-size: 1.05rem; }
.automation-meta { display: flex; flex-direction: column; gap: 12px; }
.automation-meta code,
.session-url { color: #bae6fd; background: rgba(2, 6, 23, 0.65); padding: 6px 10px; border-radius: 10px; word-break: break-all; }
.automation-session-list { display: flex; flex-direction: column; gap: 14px; }
.automation-session-row { display: flex; justify-content: space-between; gap: 16px; padding: 16px; border-radius: 16px; border: 1px solid rgba(255,255,255,0.06); background: rgba(255,255,255,0.02); }
.automation-session-main { display: flex; flex-direction: column; gap: 10px; min-width: 0; flex: 1; }
.automation-session-title { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.automation-session-meta { display: flex; gap: 14px; flex-wrap: wrap; color: var(--text-dim); font-size: 0.82rem; }
.automation-session-actions { display: flex; gap: 8px; align-items: flex-start; width: 220px; }
.status-chip { display: inline-flex; align-items: center; justify-content: center; min-width: 72px; padding: 4px 10px; border-radius: 999px; font-size: 0.75rem; border: 1px solid rgba(255,255,255,0.08); color: var(--text-dim); background: rgba(255,255,255,0.04); }
.status-chip.online { color: #67e8f9; border-color: rgba(34, 211, 238, 0.35); background: rgba(8, 145, 178, 0.12); }
.status-chip.subtle { min-width: auto; }
.status-chip-button { cursor: pointer; }
.status-chip-button:hover { border-color: rgba(125, 211, 252, 0.45); color: var(--text); }
.automation-empty { color: var(--text-dim); text-align: center; padding: 24px; border: 1px dashed var(--border); border-radius: 16px; }
.automation-example { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.example-block { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.example-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.automation-snippet { margin: 0; padding: 18px; border-radius: 16px; background: #020617; border: 1px solid rgba(148, 163, 184, 0.16); color: #cbd5e1; font-family: 'JetBrains Mono', monospace; font-size: 0.78rem; line-height: 1.55; white-space: pre-wrap; word-break: break-word; }
.automation-active { color: #7dd3fc; font-weight: 600; }

/* Logs View Styles */
.logs-view { flex: 1; min-height: 0; padding: 20px; font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; overflow-y: auto; text-align: left; border-radius: 16px; border: 1px solid var(--border); background: #000; }
.log-line { margin-bottom: 6px; line-height: 1.4; }
.log-line.error { color: #f87171; }
.log-line.info { color: #10b981; }
.log-line.warn { color: #fbbf24; }
.log-line .time { color: #6b7280; font-size: 0.75rem; margin-right: 8px; }
.log-line .level { font-weight: bold; margin-right: 8px; width: 60px; display: inline-block; }
.empty-logs { color: var(--text-dim); text-align: center; margin-top: 40px; }

/* Pending URL Banner */
.pending-url-banner {
  margin-bottom: 20px;
  padding: 16px 24px;
  border-radius: 16px;
  border: 1px solid var(--primary);
  background: rgba(56, 189, 248, 0.1);
  display: flex;
  justify-content: space-between;
  align-items: center;
  animation: slideDown 0.4s ease;
  flex-shrink: 0; /* 确保横幅不会被压缩或撑开整体布局 */
  overflow: hidden;
}
.banner-content { 
  display: flex; 
  flex-direction: column; 
  gap: 4px; 
  text-align: left; 
  flex: 1; 
  min-width: 0; /* 允许 flex 子元素在必要时收缩 */
}
.banner-content .text { font-weight: 600; font-size: 0.95rem; display: flex; align-items: baseline; flex-wrap: wrap; }
.banner-content .text code { 
  color: var(--primary); 
  background: rgba(0,0,0,0.3); 
  padding: 2px 6px; 
  border-radius: 4px; 
  margin-left: 8px;
  word-break: break-all; /* 强制长 URL 换行 */
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.85rem;
}
.banner-content .tip { font-size: 0.8rem; color: var(--text-dim); }
.btn-close { background: none; border: none; color: var(--text-dim); cursor: pointer; font-size: 1.2rem; margin-left: 12px; flex-shrink: 0; }
.btn-close:hover { color: white; }

@keyframes slideDown {
  from { opacity: 0; transform: translateY(-10px); }
  to { opacity: 1; transform: translateY(0); }
}

.sidebar,
.grid-view,
.automation-view,
.list-view,
.logs-view,
.editor,
.privacy-note code {
  scrollbar-width: thin;
  scrollbar-color: var(--scrollbar-thumb) var(--scrollbar-track);
}

.sidebar::-webkit-scrollbar,
.grid-view::-webkit-scrollbar,
.automation-view::-webkit-scrollbar,
.list-view::-webkit-scrollbar,
.logs-view::-webkit-scrollbar,
.editor::-webkit-scrollbar,
.privacy-note code::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.sidebar::-webkit-scrollbar-track,
.grid-view::-webkit-scrollbar-track,
.automation-view::-webkit-scrollbar-track,
.list-view::-webkit-scrollbar-track,
.logs-view::-webkit-scrollbar-track,
.editor::-webkit-scrollbar-track,
.privacy-note code::-webkit-scrollbar-track {
  background: var(--scrollbar-track);
  border-radius: 999px;
}

.sidebar::-webkit-scrollbar-thumb,
.grid-view::-webkit-scrollbar-thumb,
.automation-view::-webkit-scrollbar-thumb,
.list-view::-webkit-scrollbar-thumb,
.logs-view::-webkit-scrollbar-thumb,
.editor::-webkit-scrollbar-thumb,
.privacy-note code::-webkit-scrollbar-thumb {
  background: linear-gradient(180deg, var(--scrollbar-thumb-hover), var(--scrollbar-thumb));
  border: 2px solid var(--scrollbar-track);
  border-radius: 999px;
}

.sidebar::-webkit-scrollbar-thumb:hover,
.grid-view::-webkit-scrollbar-thumb:hover,
.automation-view::-webkit-scrollbar-thumb:hover,
.list-view::-webkit-scrollbar-thumb:hover,
.logs-view::-webkit-scrollbar-thumb:hover,
.editor::-webkit-scrollbar-thumb:hover,
.privacy-note code::-webkit-scrollbar-thumb:hover {
  background: linear-gradient(180deg, #bae6fd, var(--scrollbar-thumb-hover));
}

@media (max-width: 1100px) {
  .automation-grid,
  .automation-example {
    grid-template-columns: 1fr;
  }

  .automation-session-row {
    flex-direction: column;
  }

  .automation-session-actions {
    width: 100%;
  }
}
</style>
