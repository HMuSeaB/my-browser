<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { GetProfiles, LaunchBrowser, UpdateProfile, CreateProfile, DeleteProfile, SyncCookies, ResetCookies, TestProxy, GetProxies, AddProxy, DeleteProxy, TestProxyEntry, ExportCookies, ExportProfile, ImportProfile, ImportCookiesFromFile, RegisterAsDefaultBrowser, OpenDefaultAppsSettings, GetStartupURL, CreateDesktopShortcut, OpenDataDirectory, UnregisterAsDefaultBrowser, GetStorageDirectory, GetStorageMode, GetAutomationInfo, GetAutomationSessions, GetAutomationToken, StartAutomationSession, StopAutomationSession, RotateAutomationToken, SetAutomationEnabled } from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime'

import { 
  Monitor, 
  ShieldCheck, 
  TerminalSquare, 
  Bot,
  Play,
  Settings,
  X,
  Trash2,
  RefreshCw,
  Plus,
  Box,
  Share,
  Download,
  Link,
  ChevronRight,
  Sun,
  Moon,
  Palette,
  Search
} from 'lucide-vue-next'
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
const activeProfileCategory = ref('all')
const profileDetailId = ref('')

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
const notices = ref([])
let automationPollTimer = null
let noticeSeed = 0
const themeMode = ref('dark') // 'light' | 'dark'
const accentColor = ref('#0fbcf9')

const hexToRgb = (hex) => {
  if (!hex || typeof hex !== 'string' || hex.length < 7) return '15, 188, 249'
  try {
    const r = parseInt(hex.slice(1, 3), 16)
    const g = parseInt(hex.slice(3, 5), 16)
    const b = parseInt(hex.slice(5, 7), 16)
    return `${r}, ${g}, ${b}`
  } catch(e) {
    return '15, 188, 249'
  }
}

const applyDynamicTheme = () => {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.dataset.theme = themeMode.value === 'light' ? 'nexu-light' : 'ocean'
  root.style.setProperty('--primary', accentColor.value)
  root.style.setProperty('--primary-rgb', hexToRgb(accentColor.value))
  
  // 保存到本地
  localStorage.setItem('mybrowser-mode', themeMode.value)
  localStorage.setItem('mybrowser-accent', accentColor.value)
}

const toggleThemeMode = () => {
  try {
    themeMode.value = (themeMode.value === 'light' ? 'dark' : 'light')
    applyDynamicTheme()
  } catch(e) { console.error(e) }
}

const updateAccentColor = (color) => {
  accentColor.value = color
  applyDynamicTheme()
}

const presetColors = [
  '#0fbcf9', // Crystal Blue
  '#22c55e', // Emerald
  '#eab308', // Amber
  '#ec4899', // Pink
  '#8b5cf6', // Indigo
  '#f97316'  // Orange
]

const newProfile = ref({
  name: '',
  proxyType: 'socks5://',
  proxyAddr: '127.0.0.1:7891', // Clash 默认 SOCKS 端口
  startUrl: '',
  ua: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0'
})

// 已被 applyDynamicTheme 取代

// 已由 applyDynamicTheme 取代

const fetchProfiles = async () => {
  try {
    const res = await GetProfiles()
    profiles.value = res
    if (!selectedAutomationProfileId.value && res.length > 0) {
      selectedAutomationProfileId.value = res[0].id
    }
  } catch (err) {
    console.error('获取环境失败:', err)
    pushNotice(formatErrorMessage(err, '获取环境数据失败'), 'error', '系统错误')
  } finally {
    loading.value = false
  }
}

const pushNotice = (message, type = 'info', title = '') => {
  const id = `${Date.now()}-${noticeSeed++}`
  notices.value.push({ id, message, type, title })
  // 5秒后自动关闭
  setTimeout(() => removeNotice(id), 5000)
}

const removeNotice = (id) => {
  notices.value = notices.value.filter((n) => n.id !== id)
}

const formatErrorMessage = (err, fallback) => {
  if (!err) return fallback
  if (typeof err === 'string') return err
  if (err.message) return err.message
  return String(err)
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

const profileCategoryTabs = computed(() => {
  const profileCount = profiles.value.length
  const automatedCount = profiles.value.filter((profile) => !!getAutomationSessionForProfile(profile.id)).length
  const proxiedCount = profiles.value.filter((profile) => !!profile.proxy).length
  const directCount = profiles.value.filter((profile) => !profile.proxy).length
  const recentCount = Math.min(profileCount, 6)

  return [
    { key: 'all', label: '全部环境', count: profileCount },
    { key: 'recent', label: '最近创建', count: recentCount },
    { key: 'automation', label: '自动化中', count: automatedCount },
    { key: 'proxy', label: '已配置代理', count: proxiedCount },
    { key: 'direct', label: '直连环境', count: directCount },
  ]
})

const filteredProfileCards = computed(() => {
  const source = [...filteredProfiles.value]

  source.sort((a, b) => {
    const aAutomation = getAutomationSessionForProfile(a.id) ? 1 : 0
    const bAutomation = getAutomationSessionForProfile(b.id) ? 1 : 0
    if (aAutomation !== bAutomation) return bAutomation - aAutomation
    return (b.create_at || 0) - (a.create_at || 0)
  })

  if (activeProfileCategory.value === 'recent') {
    return source.slice(0, 6)
  }

  if (activeProfileCategory.value === 'automation') {
    return source.filter((profile) => !!getAutomationSessionForProfile(profile.id))
  }

  if (activeProfileCategory.value === 'proxy') {
    return source.filter((profile) => !!profile.proxy)
  }

  if (activeProfileCategory.value === 'direct') {
    return source.filter((profile) => !profile.proxy)
  }

  return source
})

const detailProfile = computed(() => {
  if (!profileDetailId.value) return null
  return getProfileById(profileDetailId.value) || null
})

const detailProfileSession = computed(() => {
  if (!detailProfile.value) return null
  return getAutomationSessionForProfile(detailProfile.value.id) || null
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

const onlineProxyCount = computed(() => proxies.value.filter((proxy) => proxy.status === 'online').length)

const runningAutomationCount = computed(() => automationSessions.value.filter((session) => session.status === 'running').length)

const currentViewTitle = computed(() => {
  if (currentView.value === 'profiles') return detailProfile.value ? '环境详情' : '环境'
  if (currentView.value === 'proxies') return '代理'
  if (currentView.value === 'automation') return '自动化'
  if (currentView.value === 'logs') return '日志'
  return '工作台'
})

const currentViewDescription = computed(() => {
  if (currentView.value === 'profiles') {
    if (detailProfile.value) return '查看当前环境配置，并集中处理 Cookie、导出和自动化操作'
    return '管理浏览环境、默认页和 Cookie 状态'
  }
  if (currentView.value === 'proxies') return '维护代理池并检查连通性'
  if (currentView.value === 'automation') return '查看本地 API、会话状态和接入示例'
  if (currentView.value === 'logs') return '跟踪运行日志和最近事件'
  return ''
})

const currentViewStats = computed(() => {
  if (currentView.value === 'profiles') {
    if (detailProfile.value) {
      return [
        { label: '当前环境', value: detailProfile.value.name },
        { label: '自动化状态', value: detailProfileSession.value ? '运行中' : '待命中' },
      ]
    }
    return [
      { label: '环境', value: `${filteredProfileCards.value.length}/${profiles.value.length}` },
      { label: '自动化中', value: String(runningAutomationCount.value) },
    ]
  }

  if (currentView.value === 'proxies') {
    return [
      { label: '代理', value: `${filteredProxies.value.length}/${proxies.value.length}` },
      { label: '在线', value: String(onlineProxyCount.value) },
    ]
  }

  if (currentView.value === 'automation') {
    return [
      { label: '活动会话', value: String(runningAutomationCount.value) },
      { label: '控制台', value: automationInfo.value.enabled ? '已启用' : '已关闭' },
      { label: '选中环境', value: selectedAutomationProfile.value?.name || '未选择' },
    ]
  }

  if (currentView.value === 'logs') {
    return [
      { label: '日志', value: `${filteredLogs.value.length}/${logs.value.length}` },
      { label: '最近状态', value: logs.value[logs.value.length - 1]?.level?.toUpperCase() || 'EMPTY' },
    ]
  }

  return []
})

const appToneClass = computed(() => {
  try {
    if (currentView.value === 'profiles') {
      return detailProfile.value ? 'tone-profile-detail' : 'tone-profiles'
    }
    if (currentView.value === 'proxies') return 'tone-proxies'
    if (currentView.value === 'automation') return 'tone-automation'
    if (currentView.value === 'logs') return 'tone-logs'
  } catch(e) { console.error(e) }
  return 'tone-default'
})

const selectedAutomationProfile = computed(() => {
  return profiles.value.find((profile) => profile.id === selectedAutomationProfileId.value) || null
})

const selectedAutomationSession = computed(() => {
  if (!selectedAutomationProfileId.value) return null
  return getAutomationSessionForProfile(selectedAutomationProfileId.value) || null
})

const effectiveAutomationTargetURL = computed(() => {
  const manualURL = (automationStartURL.value || '').trim()
  if (manualURL) return manualURL
  return selectedAutomationProfile.value?.start_url || ''
})

const automationLaunchSummary = computed(() => {
  if (!selectedAutomationProfile.value) {
    return {
      type: 'idle',
      title: '先选择一个环境',
      description: '选中环境后，这里会显示本次是一键新开还是直接复用已有自动化会话。',
    }
  }

  if (selectedAutomationSession.value) {
    return {
      type: 'online',
      title: `将复用现有会话 · 端口 ${selectedAutomationSession.value.debug_port}`,
      description: effectiveAutomationTargetURL.value
        ? `点击后会直接在 ${selectedAutomationProfile.value.name} 里跳转到目标链接。`
        : `当前未填写链接，将继续沿用 ${selectedAutomationProfile.value.name} 的默认标签页。`,
    }
  }

  return {
    type: 'pending',
    title: '将启动新自动化会话',
    description: effectiveAutomationTargetURL.value
      ? `首次启动后会自动打开目标链接。`
      : `当前未填写链接，将在启动后使用该环境的默认标签页。`,
  }
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

// 已归并到顶部统一管理

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

// 已归并到顶部的 applyDynamicTheme

const copyText = async (text, label) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    pushNotice(`${label} 已复制到剪贴板`, 'success', '复制成功')
  } catch (err) {
    window.prompt(`请手动复制${label}:`, text)
  }
}

const handleStartAutomation = async (profileID = selectedAutomationProfileId.value, startURL = automationStartURL.value) => {
  if (!automationInfo.value.enabled) {
    pushNotice('请先启用自动化控制台，再使用一键打开。', 'warning', '未启用')
    return
  }
  if (!profileID) {
    pushNotice('请先选择一个环境。', 'warning', '缺少环境')
    return
  }
  const existingSession = getAutomationSessionForProfile(profileID)
  const effectiveTargetURL = (startURL || '').trim() || getProfileById(profileID)?.start_url || ''
  automationLoading.value = true
  try {
    const session = await StartAutomationSession(profileID, startURL)
    currentView.value = 'automation'
    automationStartURL.value = ''
    await fetchAutomationState()
    if (existingSession) {
      if (effectiveTargetURL) {
        pushNotice(`已在当前会话中打开链接。\nBiDi 地址：${session.connect_url}`, 'success', '已复用会话')
      } else {
        pushNotice(`该环境已有活动自动化会话，已直接复用。\nBiDi 地址：${session.connect_url}`, 'info', '已连接')
      }
    } else {
      if (effectiveTargetURL) {
        pushNotice(`已启动自动化会话并打开链接。\nBiDi 地址：${session.connect_url}`, 'success', '打开成功')
      } else {
        pushNotice(`自动化会话已启动。\nBiDi 地址：${session.connect_url}`, 'success', '会话已启动')
      }
    }
  } catch (err) {
    pushNotice(formatErrorMessage(err, '自动化启动失败'), 'error', '打开失败')
  } finally {
    automationLoading.value = false
  }
}

const handleStopAutomation = async (sessionID) => {
  if (!confirm('这会关闭该自动化浏览器窗口，并同步保存当前 Cookie。是否继续？')) return
  try {
    await StopAutomationSession(sessionID)
    setTimeout(fetchAutomationState, 300)
    pushNotice('自动化会话正在关闭，Cookie 会在退出后自动同步。', 'info', '已发送停止指令')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '停止自动化会话失败'), 'error', '停止失败')
  }
}

const handleRotateAutomationToken = async () => {
  if (!confirm('轮换后旧 token 会立即失效，脚本需要改用新 token。是否继续？')) return
  try {
    automationToken.value = await RotateAutomationToken()
    await fetchAutomationState()
    pushNotice('本地 API token 已轮换，旧 token 已立即失效。', 'success', 'Token 已更新')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '轮换 token 失败'), 'error', '轮换失败')
  }
}

const handleToggleAutomation = async () => {
  const nextEnabled = !automationInfo.value.enabled
  if (!nextEnabled && !confirm('停用后本地自动化 API 将停止监听，新的脚本将无法接入。是否继续？')) return

  try {
    await SetAutomationEnabled(nextEnabled)
    await fetchAutomationState()
    pushNotice(
      nextEnabled ? '自动化控制台已启用，可以开始一键打开链接。' : '自动化控制台已停用，新脚本将无法继续接入。',
      nextEnabled ? 'success' : 'info',
      nextEnabled ? '已启用' : '已停用'
    )
  } catch (err) {
    pushNotice(formatErrorMessage(err, `${nextEnabled ? '启用' : '停用'}自动化控制台失败`), 'error', nextEnabled ? '启用失败' : '停用失败')
  }
}

const getAutomationSessionForProfile = (profileID) => {
  return automationSessions.value.find((session) => session.profile_id === profileID)
}

const getProfileById = (profileID) => {
  return profiles.value.find((profile) => profile.id === profileID)
}

const openProfileDetail = (profileID) => {
  profileDetailId.value = profileID
}

const closeProfileDetail = () => {
  profileDetailId.value = ''
}

const openAutomationWorkspace = (profileID) => {
  currentView.value = 'automation'
  selectedAutomationProfileId.value = profileID
  automationStartURL.value = ''
}

const shortId = (value) => {
  if (!value) return ''
  return value.slice(0, 8)
}

const formatRelativeTime = (unixSeconds) => {
  if (!unixSeconds) return '刚创建'
  const delta = Date.now() - (unixSeconds * 1000)
  const minutes = Math.max(1, Math.floor(delta / 60000))
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} 天前`
  return new Date(unixSeconds * 1000).toLocaleDateString('zh-CN')
}

const handleLaunch = async (id, url = "") => {
  const finalURL = pendingURL.value || url
  const profile = getProfileById(id)
  try {
    await LaunchBrowser(id, finalURL)
    if (pendingURL.value) {
      pendingURL.value = '' // 启动后清除任务
    }
    pushNotice(
      finalURL ? `${profile?.name || '环境'} 已启动，并会直接打开指定链接。` : `${profile?.name || '环境'} 已启动。`,
      'success',
      '启动成功'
    )
  } catch (err) {
    pushNotice(formatErrorMessage(err, '启动环境失败'), 'error', '启动失败')
  }
}

const handleVerify = (id) => {
  handleLaunch(id, "https://pixelscan.net")
}

const handleSyncCookies = async (id) => {
  try {
    await SyncCookies(id)
    await fetchProfiles()
    pushNotice('登录状态已同步到本地环境。', 'success', '同步完成')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '同步 Cookie 失败'), 'error', '同步失败')
  }
}

const handleResetCookies = async (id) => {
  if (!confirm('确定要重置 Cookie 吗？这会清空已保存的数据并物理删除登录文件。')) return
  try {
    await ResetCookies(id)
    await fetchProfiles()
    pushNotice('已清空当前环境保存的 Cookie 数据。', 'success', '重置完成')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '重置 Cookie 失败'), 'error', '重置失败')
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
  if (!newProfile.value.name) {
    pushNotice('请先填写环境名称。', 'warning', '信息不完整')
    return
  }
  const fullProxy = newProfile.value.proxyAddr ? newProfile.value.proxyType + newProfile.value.proxyAddr : ''
  try {
    await CreateProfile(newProfile.value.name, fullProxy, newProfile.value.ua, newProfile.value.startUrl)
    showCreateModal.value = false
    resetNewProfile()
    await fetchProfiles()
    pushNotice('新环境已经创建完成，可以直接启动。', 'success', '创建成功')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '创建环境失败'), 'error', '创建失败')
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
    await fetchProfiles()
    pushNotice('环境已删除。', 'success', '删除完成')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '删除环境失败'), 'error', '删除失败')
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
    await fetchProfiles()
    pushNotice('环境设置已更新。', 'success', '保存成功')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '保存设置失败'), 'error', '保存失败')
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
    await fetchProfiles()
    pushNotice('Cookie 数据已保存。', 'success', '保存成功')
  } catch (err) {
    pushNotice('Cookie JSON 格式不正确，请先修正后再保存。', 'error', '格式错误')
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
  if (!newProxy.value.name || !newProxy.value.addr) {
    pushNotice('请填写完整的代理名称和地址。', 'warning', '信息不完整')
    return
  }
  try {
    await AddProxy(newProxy.value.name, newProxy.value.addr)
    newProxy.value = { name: '', addr: '' }
    await fetchProxies()
    pushNotice('代理已加入代理池。', 'success', '添加成功')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '添加代理失败'), 'error', '添加失败')
  }
}

const handleDeleteProxy = async (id) => {
  if (!confirm('确定删除该代理吗？')) return
  try {
    await DeleteProxy(id)
    await fetchProxies()
    pushNotice('代理已从代理池移除。', 'success', '删除完成')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '删除代理失败'), 'error', '删除失败')
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
    if (err) pushNotice(formatErrorMessage(err, '导出 Cookie 失败'), 'error', '导出失败')
  }
}

const handleExportProfile = async (id) => {
  try {
    await ExportProfile(id)
    pushNotice('环境包已经导出完成。', 'success', '导出成功')
  } catch (err) {
    if (err) pushNotice(formatErrorMessage(err, '导出环境包失败'), 'error', '导出失败')
  }
}

const handleImportProfile = async () => {
  try {
    await ImportProfile()
    await fetchProfiles()
    pushNotice('环境包已导入。', 'success', '导入成功')
  } catch (err) {
    if (err) pushNotice(formatErrorMessage(err, '导入环境包失败'), 'error', '导入失败')
  }
}

const handleImportFromFile = async () => {
  try {
    const content = await ImportCookiesFromFile()
    if (content) {
      cookieJson.value = content
      pushNotice('Cookie 文件已载入编辑器。', 'success', '导入成功')
    }
  } catch (err) {
    if (err) pushNotice(formatErrorMessage(err, '读取 Cookie 文件失败'), 'error', '导入失败')
  }
}

const handleRegisterBrowser = async () => {
  try {
    const res = await RegisterAsDefaultBrowser()
    let tip = res + '\n\n提示：某些第三方浏览器管理器也会扫描到 MyBrowser。'
    tip += '\n\n【重要】如果您处于开发模式(dev)，注册路径是临时的。建议 build 正式版后再执行注册。'
    pushNotice('默认浏览器注册已经写入，可到系统设置中继续确认。', 'success', '注册完成')
    if (confirm(tip + '\n\n是否立即打开 Windows 默认应用设置页进行确认？')) {
        await OpenDefaultAppsSettings()
    }
  } catch (err) {
    pushNotice(formatErrorMessage(err, '注册默认浏览器失败，建议检查是否被安全软件拦截。'), 'error', '注册失败')
  }
}

const handleCreateDesktopShortcut = async () => {
  try {
    await CreateDesktopShortcut()
    pushNotice('桌面快捷方式已生成。', 'success', '创建完成')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '生成桌面快捷方式失败'), 'error', '创建失败')
  }
}

const handleOpenDataDirectory = async () => {
  try {
    await OpenDataDirectory()
    pushNotice('数据目录已打开。', 'info', '已打开')
  } catch (err) {
    pushNotice(formatErrorMessage(err, '打开数据目录失败'), 'error', '打开失败')
  }
}

const handleUnregisterBrowser = async () => {
  if (!confirm('这会清理 MyBrowser 写入的浏览器注册表项，但不会删除你的数据目录。是否继续？')) return
  try {
    const res = await UnregisterAsDefaultBrowser()
    pushNotice('浏览器注册信息已清理。', 'success', '清理完成')
    if (confirm(res + '\n\n是否立即打开 Windows 默认应用设置页进行确认？')) {
      await OpenDefaultAppsSettings()
    }
  } catch (err) {
    pushNotice(formatErrorMessage(err, '清理浏览器注册失败'), 'error', '清理失败')
  }
}

onMounted(async () => {
  try {
    const savedMode = localStorage.getItem('mybrowser-mode')
    const savedAccent = localStorage.getItem('mybrowser-accent')
    if (savedMode) themeMode.value = savedMode
    if (savedAccent) accentColor.value = savedAccent
    applyDynamicTheme()
  } catch (err) {
    console.warn('读取个性化设置失败:', err)
  }

  fetchProfiles()
  fetchProxies()
  fetchAutomationState()
  
  // 检查是否有待启动的外部 URL
  try {
    const url = await GetStartupURL()
    if (url) {
        pendingURL.value = url
        pushNotice('检测到来自外部的链接请求，可以直接在某个环境里打开。', 'info', '外部链接已接收')
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
    pushNotice('收到新的外部链接请求，请选择目标环境打开。', 'info', '新链接已就绪')
  })
})

onUnmounted(() => {
  stopAutomationPolling()
})
</script>

<template>
  <div class="app-layout" :class="appToneClass">
    <div class="glass-bg"></div>
    <TransitionGroup name="notice-pop" tag="div" class="notice-stack" v-if="notices.length > 0">
      <div v-for="notice in notices" :key="notice.id" class="notice-card" :class="notice.type">
        <div class="notice-copy">
          <strong v-if="notice.title">{{ notice.title }}</strong>
          <span>{{ notice.message }}</span>
        </div>
        <button class="notice-close" @click="removeNotice(notice.id)">✕</button>
      </div>
    </TransitionGroup>

    <aside class="sidebar glass">
      <div class="logo">
        <div class="dot pulse"></div>
        <h1>MyBrowser</h1>
        <button @click="toggleThemeMode" class="theme-mode-toggle" :title="themeMode === 'light' ? '切换到深色' : '切换到浅色'">
          <Moon v-if="themeMode === 'light'" :size="18" />
          <Sun v-else :size="18" />
        </button>
      </div>
      <nav class="nav-links">
        <div class="nav-item" :class="{ active: currentView === 'profiles' }" @click="currentView = 'profiles'"><Monitor :size="18" :stroke-width="2"/>环境</div>
        <div class="nav-item" :class="{ active: currentView === 'proxies' }" @click="currentView = 'proxies'"><ShieldCheck :size="18" :stroke-width="2"/>代理</div>
        <div class="nav-item" :class="{ active: currentView === 'logs' }" @click="currentView = 'logs'"><TerminalSquare :size="18" :stroke-width="2"/>日志</div>
        <div class="nav-item" :class="{ active: currentView === 'automation' }" @click="currentView = 'automation'"><Bot :size="18" :stroke-width="2"/>自动化</div>
        <div class="nav-item register-btn" @click="handleRegisterBrowser"><Link :size="16" :stroke-width="2"/>注册默认浏览器</div>
        <div class="nav-item register-btn" @click="handleCreateDesktopShortcut" style="margin-top: 10px;"><Share :size="16" :stroke-width="2"/>创建桌面快捷方式</div>
        <div class="nav-item register-btn" @click="handleOpenDataDirectory" style="margin-top: 10px;"><Box :size="16" :stroke-width="2"/>打开数据目录</div>
        <div class="nav-item register-btn warn" @click="handleUnregisterBrowser" style="margin-top: 10px;"><Trash2 :size="16" :stroke-width="2"/>清理注册信息</div>
      </nav>
      <div class="theme-panel">
        <div class="theme-panel-head">
          <strong>界面个性化设计</strong>
          <span>深度自定义您的专属主配色</span>
        </div>
        <div class="custom-theme-box">
          <div class="color-picker-wrapper">
             <input type="color" v-model="accentColor" @input="updateAccentColor($event.target.value)" class="color-input" />
             <Palette :size="16" class="palette-icon" />
          </div>
          <div class="preset-chips">
            <button 
              v-for="color in presetColors" 
              :key="color" 
              class="color-chip" 
              :style="{ background: color }"
              :class="{ active: accentColor === color }"
              @click="updateAccentColor(color)"
            ></button>
          </div>
        </div>
      </div>
      <div class="privacy-note">
        <p><b>本地存储</b></p>
        <p v-if="storageMode === 'portable'">当前为便携模式，数据保存在程序同目录的 `MyBrowserData`。</p>
        <p v-else>当前为本地模式，数据保存在 `%LOCALAPPDATA%\MyBrowser`。</p>
        <p class="privacy-tip">如检测到旧版 `data` 目录，应用会自动迁移并保留旧目录供核对。</p>
        <p v-if="storageDir"><code>{{ storageDir }}</code></p>
      </div>
    </aside>

    <main class="main-content">
      <header class="top-bar glass">
        <div class="top-bar-copy">
          <div class="top-bar-title-row">
            <h2>{{ currentViewTitle }}</h2>
            <span>{{ currentViewDescription }}</span>
          </div>
          <div class="top-bar-meta">
            <span v-for="stat in currentViewStats" :key="`${currentView}-${stat.label}`" class="summary-chip">
              <b>{{ stat.value }}</b>
              <span>{{ stat.label }}</span>
            </span>
          </div>
        </div>
        <div class="top-bar-tools">
          <template v-if="currentView === 'profiles' && detailProfile">
            <div class="actions detail-top-actions">
              <button @click="closeProfileDetail" class="btn-ghost">返回环境列表</button>
              <button @click="openSettings(detailProfile)" class="btn-ghost">环境设置</button>
            </div>
          </template>
          <template v-else>
            <div class="search-box">
              <input v-model="searchQuery" type="text" :placeholder="searchPlaceholder" class="search-input" />
            </div>
            <div class="actions">
               <template v-if="currentView === 'profiles'">
                 <button @click="handleImportProfile" class="btn-ghost" style="margin-right: 8px;">导入环境包</button>
                 <button @click="showCreateModal = true" class="btn-create">新建环境</button>
               </template>
               <div v-else-if="currentView === 'proxies'" class="proxy-add-form">
                  <input v-model="newProxy.name" placeholder="代理名称" />
                  <input v-model="newProxy.addr" placeholder="socks5://1.2.3.4:7891" />
                  <button @click="handleAddProxy" class="btn-create add">添加代理</button>
               </div>
               <button v-else-if="currentView === 'automation'" @click="fetchAutomationState" class="btn-ghost">刷新状态</button>
               <button v-else-if="currentView === 'logs'" @click="logs = []" class="btn-ghost">清空日志</button>
            </div>
          </template>
        </div>
      </header>

      <div class="content-body">
        <div v-if="pendingURL" class="pending-url-banner glass">
           <div class="banner-content">
              <span class="text">检测到外部链接：<code>{{ pendingURL }}</code></span>
              <span class="tip">点击任一环境的“启动环境”，即可在指定环境中直接打开它。</span>
           </div>
           <button @click="pendingURL = ''" class="btn-close">✕</button>
        </div>

        <div v-if="loading" class="loader-wrap">
          <div class="spinner"></div>
        </div>

        <template v-else>
          <Transition name="view-swap" mode="out-in">
            <div
              :key="currentView === 'profiles' && detailProfile ? `profiles-detail-${detailProfile.id}` : currentView"
              class="view-shell"
            >
          <!-- 1. 环境列表视图 -->
          <div v-if="currentView === 'profiles'" class="profiles-workspace">
            <template v-if="!detailProfile">
              <div class="profiles-filter-bar">
                <button
                  v-for="tab in profileCategoryTabs"
                  :key="tab.key"
                  class="category-pill"
                  :class="{ active: activeProfileCategory === tab.key }"
                  @click="activeProfileCategory = tab.key"
                >
                  <span>{{ tab.label }}</span>
                  <b>{{ tab.count }}</b>
                </button>
              </div>

              <div v-if="filteredProfileCards.length === 0" class="empty-state glass">当前分类下没有匹配到环境。</div>

              <TransitionGroup v-else name="tile-shift" tag="div" class="profiles-grid">
                <article
                  v-for="(p, index) in filteredProfileCards"
                  :key="p.id"
                  class="profile-tile glass"
                  :style="{ '--tile-index': index }"
                >
                  <div class="profile-tile-head">
                    <div>
                      <div class="profile-title-row">
                        <h3>{{ p.name }}</h3>
                        <span v-if="getAutomationSessionForProfile(p.id)" class="status-chip online subtle">自动化中</span>
                        <span v-else class="status-chip subtle">{{ p.proxy ? '已配置代理' : '直连' }}</span>
                      </div>
                      <div class="profile-id-row">
                        <code>{{ shortId(p.id) }}</code>
                        <span class="profile-created-at">{{ formatRelativeTime(p.create_at) }}</span>
                      </div>
                    </div>
                  </div>

                  <div class="profile-meta-grid">
                    <div class="profile-meta-line">
                      <span class="label">代理</span>
                      <strong>{{ p.proxy || '直连访问' }}</strong>
                    </div>
                    <div class="profile-meta-line">
                      <span class="label">默认页</span>
                      <span class="start-url">{{ p.start_url || '新标签页' }}</span>
                    </div>
                    <div v-if="getAutomationSessionForProfile(p.id)" class="profile-meta-line meta-wide">
                      <span class="label">BiDi</span>
                      <span class="automation-active">端口 {{ getAutomationSessionForProfile(p.id).debug_port }}</span>
                    </div>
                  </div>

                  <div class="profile-primary-actions">
                    <button @click.stop="handleLaunch(p.id)" class="btn-launch"><Play :size="16" :stroke-width="2.5" /> 启动环境</button>
                  </div>

                  <div class="profile-secondary-actions compact">
                    <button @click.stop="getAutomationSessionForProfile(p.id) ? openAutomationWorkspace(p.id) : handleStartAutomation(p.id, '')" class="btn-action-ghost automation-action">
                      <Bot :size="14" :stroke-width="2.5" /> {{ getAutomationSessionForProfile(p.id) ? '前往会话' : '自动化打开' }}
                    </button>
                    <button @click.stop="openProfileDetail(p.id)" class="btn-action-ghost"><Settings :size="14" :stroke-width="2.5" /> 管理环境</button>
                  </div>
                </article>
              </TransitionGroup>
            </template>

            <section v-else class="profile-detail-page glass">
              <div class="detail-page-head">
                <div>
                  <div class="detail-page-breadcrumb">环境 / {{ detailProfile.name }}</div>
                  <h3>{{ detailProfile.name }}</h3>
                  <div class="profile-id-row">
                    <code>{{ detailProfile.id }}</code>
                    <span class="profile-created-at">{{ formatRelativeTime(detailProfile.create_at) }}</span>
                  </div>
                </div>
                <div class="detail-page-head-actions">
                  <button @click="copyText(detailProfile.id, 'Profile ID')" class="btn-inline-copy">复制 ID</button>
                </div>
              </div>

              <div class="detail-overview-grid">
                <div class="detail-card">
                  <span class="label">代理</span>
                  <strong>{{ detailProfile.proxy || '直连访问' }}</strong>
                </div>
                <div class="detail-card">
                  <span class="label">内核</span>
                  <strong>Camoufox (Firefox)</strong>
                </div>
                <div class="detail-card">
                  <span class="label">默认页</span>
                  <strong>{{ detailProfile.start_url || '新标签页' }}</strong>
                </div>
                <div class="detail-card">
                  <span class="label">状态</span>
                  <strong>{{ detailProfileSession ? `自动化中 · 端口 ${detailProfileSession.debug_port}` : '待命中' }}</strong>
                </div>
              </div>

              <div class="detail-action-group">
                <h4>快速操作</h4>
                <div class="detail-action-grid">
                  <button @click="handleLaunch(detailProfile.id)" class="btn-launch">启动环境</button>
                  <button @click="handleVerify(detailProfile.id)" class="btn-verify wide" title="指纹验证">验证</button>
                  <button @click="detailProfileSession ? openAutomationWorkspace(detailProfile.id) : handleStartAutomation(detailProfile.id, '')" class="btn-action-ghost automation-action">自动化打开</button>
                  <button @click="openSettings(detailProfile)" class="btn-action-ghost">环境设置</button>
                </div>
              </div>

              <div class="detail-action-group">
                <h4>Cookie 与数据</h4>
                <div class="detail-action-grid secondary">
                  <button @click="openCookieEditor(detailProfile)" class="btn-action-ghost">编辑 Cookie</button>
                  <button @click="handleSyncCookies(detailProfile.id)" class="btn-action-ghost">同步状态</button>
                  <button @click="handleExportCookies(detailProfile.id)" class="btn-action-ghost">导出 Cookie</button>
                  <button @click="handleExportProfile(detailProfile.id)" class="btn-action-ghost">导出环境包</button>
                </div>
              </div>

              <div class="detail-action-group danger">
                <h4>危险操作</h4>
                <div class="detail-action-grid secondary">
                  <button @click="handleResetCookies(detailProfile.id)" class="btn-action-ghost warn">重置 Cookie</button>
                  <button @click="handleDelete(detailProfile.id)" class="btn-action-ghost warn">删除环境</button>
                </div>
              </div>

              <div v-if="detailProfileSession" class="detail-session-panel">
                <div class="detail-section-head">
                  <h4>当前自动化会话</h4>
                  <button @click="copyText(detailProfileSession.connect_url, 'BiDi 地址')" class="btn-inline-copy">复制地址</button>
                </div>
                <code class="session-url">{{ detailProfileSession.connect_url }}</code>
              </div>
            </section>
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
                    <button @click="handleTestProxyEntry(px.id)" class="btn-icon">测试</button>
                    <button @click="handleDeleteProxy(px.id)" class="btn-icon del">删除</button>
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
                  <button @click="copyText(automationInfo.base_url, 'API 地址')" class="btn-action-ghost">复制 API 地址</button>
                  <button @click="copyText(automationToken, 'Token')" class="btn-action-ghost">复制 Token</button>
                  <button @click="handleRotateAutomationToken" class="btn-action-ghost warn">轮换 Token</button>
                </div>
              </section>

              <section class="automation-card glass">
                <div class="automation-card-head">
                  <h3>一键打开</h3>
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
                  <label>打开链接（可选）</label>
                  <input v-model="automationStartURL" placeholder="例如 https://example.com" />
                  <span class="hint">留空则沿用该环境的默认标签页；如果该环境已在运行，会直接复用当前会话并跳转。</span>
                </div>
                <div class="automation-launch-summary" :class="automationLaunchSummary.type">
                  <strong>{{ automationLaunchSummary.title }}</strong>
                  <span>{{ automationLaunchSummary.description }}</span>
                  <code v-if="effectiveAutomationTargetURL">{{ effectiveAutomationTargetURL }}</code>
                </div>
                <button @click="handleStartAutomation()" class="btn-solid" :disabled="automationLoading || !automationInfo.enabled">
                  {{ automationLoading ? '打开中...' : '在环境中打开链接' }}
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
                      <div class="automation-session-meta" v-if="session.start_url">
                        <span>当前链接</span>
                        <code class="session-target">{{ session.start_url }}</code>
                      </div>
                      <code class="session-url">{{ session.connect_url }}</code>
                    </div>
                    <div class="automation-session-actions">
                      <button @click="openAutomationWorkspace(session.profile_id)" class="btn-action-ghost">前往会话</button>
                      <button @click="copyText(session.profile_id, 'Profile ID')" class="btn-action-ghost">复制 ID</button>
                      <button @click="copyText(session.connect_url, 'BiDi 地址')" class="btn-action-ghost">复制地址</button>
                      <button @click="handleStopAutomation(session.session_id)" class="btn-action-ghost warn">停止</button>
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
                      <button @click="copyText(pythonAutomationSnippet, 'Python 示例')" class="btn-action-ghost">复制</button>
                    </div>
                    <pre class="automation-snippet">{{ pythonAutomationSnippet }}</pre>
                  </div>
                  <div class="example-block">
                    <div class="example-head">
                      <span>cURL</span>
                      <button @click="copyText(curlAutomationSnippet, 'cURL 示例')" class="btn-action-ghost">复制</button>
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
            </div>
          </Transition>
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
            <button @click="handleImportFromFile" class="btn-ghost" style="margin-right: auto;">导入 JSON 文件</button>
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
  --primary-rgb: 56, 189, 248;
  --primary-soft: rgba(var(--primary-rgb), 0.7);
  --primary-ink: #7dd3fc;
  --primary-surface: rgba(var(--primary-rgb), 0.12);
  --primary-hover: #5ac8fb;
  --bg: #090e17;
  --bg-rgb: 9, 14, 23;
  --bg-elevated: #0f1623;
  --bg-panel: #131c2d;
  --glass: rgba(255, 255, 255, 0.03);
  --glass-strong: rgba(255, 255, 255, 0.06);
  --border: rgba(255, 255, 255, 0.08);
  --text: #f1f5f9;
  --text-dim: #94a3b8;
  --scrollbar-track: transparent;
  --scrollbar-thumb: rgba(255, 255, 255, 0.15);
  --scrollbar-thumb-hover: rgba(255, 255, 255, 0.25);
  --shadow-soft: 0 8px 32px rgba(0, 0, 0, 0.24);
  --shadow-strong: 0 16px 48px rgba(0, 0, 0, 0.32);
  --radius-xl: 32px;
  --radius-lg: 24px;
  --radius-md: 16px;
  --blur-strength: 0px;
  --sidebar-surface: #090e17;
  --panel-surface: #0f1623;
  --surface-highlight: inset 0 1px 0 rgba(255, 255, 255, 0.04);
  --texture-opacity: 0.02;
  --texture-size: 26px;
  --notice-bg: rgba(15, 23, 42, 0.94);
  --notice-text: #f1f5f9;
  --notice-text-dim: #cbd5e1;
  --notice-close-bg: rgba(255, 255, 255, 0.03);
  --notice-close-border: rgba(255, 255, 255, 0.08);
  --code-bg: rgba(255, 255, 255, 0.04);
  --code-bg-strong: #020617;
  --motion-fast: 160ms;
  --motion-base: 240ms;
  --motion-slow: 360ms;
  --motion-emphasis: cubic-bezier(0.22, 1, 0.36, 1);
  --motion-smooth: cubic-bezier(0.2, 0.8, 0.2, 1);
  --lift-rest: translateY(0) scale(1);
  --lift-hover: translateY(-1px) scale(1.002);
  --lift-press: translateY(1px) scale(0.992);
}

:root[data-theme='nexu-light'] {
  --primary: #3b82f6; /* 电光莫兰迪蓝，比之前的霓虹蓝更有张力 */
  --primary-rgb: 59, 130, 246;
  --primary-soft: rgba(var(--primary-rgb), 0.12);
  --primary-ink: #245fce;
  --primary-surface: rgba(var(--primary-rgb), 0.14);
  --primary-hover: #2f6fe4;
  --bg: #f3f4f9; /* 呼吸灰紫底色 */
  --bg-rgb: 243, 244, 249;
  --bg-elevated: #ffffff;
  --bg-panel: rgba(255, 255, 255, 0.45);
  --glass: rgba(255, 255, 255, 0.5);
  --glass-strong: rgba(255, 255, 255, 0.88);
  --border: rgba(var(--primary-rgb), 0.06);
  --text: #1e293b;
  --text-dim: #64748b;
  --scrollbar-track: transparent;
  --scrollbar-thumb: rgba(var(--primary-rgb), 0.12);
  --scrollbar-thumb-hover: rgba(var(--primary-rgb), 0.22);
  --shadow-soft: 0 4px 16px rgba(0,0,0,0.02);
  --shadow-strong: 0 16px 48px rgba(var(--primary-rgb), 0.06); /* 彩色漫反射影 */
  --radius-xl: 36px;
  --radius-lg: 28px;
  --radius-md: 18px;
  --blur-strength: 48px; /* 极致模糊 */
  --sidebar-surface: rgba(255, 255, 255, 0.32);
  --panel-surface: rgba(255, 255, 255, 0.72);
  --surface-highlight: inset 0 1px 1px rgba(255, 255, 255, 0.8), 0 0 0 1px rgba(255, 255, 255, 0.4);
  --texture-opacity: 0.015;
  --texture-size: 22px;
  --notice-bg: rgba(255, 255, 255, 0.94);
  --notice-text: #102033;
  --notice-text-dim: #526375;
  --notice-close-bg: rgba(var(--primary-rgb), 0.08);
  --notice-close-border: rgba(var(--primary-rgb), 0.14);
  --btn-text: #ffffff;
  --code-bg: #f1f5f9;
  --code-bg-strong: #ffffff;
}

/* 主题方案已被动态配色系统取代 */

* { box-sizing: border-box; margin: 0; padding: 0; scrollbar-width: thin; scrollbar-color: var(--scrollbar-thumb) transparent; }
*::-webkit-scrollbar { width: 6px; height: 6px; }
*::-webkit-scrollbar-track { background: transparent; }
*::-webkit-scrollbar-thumb { background: var(--scrollbar-thumb); border-radius: 10px; }
*::-webkit-scrollbar-thumb:hover { background: var(--scrollbar-thumb-hover); }

html { background: var(--bg); }
body { font-family: 'Inter', system-ui, sans-serif; background: var(--bg); color: var(--text); overflow: hidden; transition: background 0.35s ease, color 0.35s ease; }
button {
  font: inherit;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  transform: var(--lift-rest);
  transition:
    transform var(--motion-fast) var(--motion-emphasis),
    background var(--motion-base) ease,
    border-color var(--motion-fast) ease,
    box-shadow var(--motion-base) ease,
    color var(--motion-fast) ease,
    opacity var(--motion-fast) ease;
}
button:hover { transform: var(--lift-hover); }
button:active { transform: var(--lift-press); }
button:disabled { cursor: not-allowed; opacity: 0.6; transform: none; }

.app-layout { display: flex; height: 100vh; position: relative; background: var(--bg); }
.glass-bg { position: absolute; inset: 0; background: radial-gradient(circle at 65% 22%, rgba(var(--primary-rgb), 0.18) 0%, transparent 45%), radial-gradient(circle at 18% 78%, rgba(var(--primary-rgb), 0.12) 0%, transparent 40%); pointer-events: none; overflow: hidden; }
.glass-bg::before,
.glass-bg::after {
  content: '';
  position: absolute;
  width: 34vw;
  height: 34vw;
  border-radius: 999px;
  background: radial-gradient(circle, rgba(var(--primary-rgb), 0.18) 0%, transparent 62%);
  filter: blur(60px);
  animation: ambientFloat 20s ease-in-out infinite alternate;
}
.glass-bg::before {
  top: -12vw;
  right: 8vw;
}
.glass-bg::after {
  left: -10vw;
  bottom: -14vw;
  opacity: 0.7;
  animation-duration: 28s;
}
.glass-bg > * { pointer-events: none; }
.app-layout::after {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: var(--texture-opacity);
  background-image:
    linear-gradient(rgba(255,255,255,0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255,255,255,0.03) 1px, transparent 1px);
  background-size: var(--texture-size) var(--texture-size);
  mask-image: radial-gradient(circle at center, black 42%, transparent 100%);
}

.app-layout.tone-profiles .glass-bg {
  background:
    radial-gradient(circle at 78% 18%, rgba(var(--primary-rgb), 0.11) 0%, transparent 34%),
    radial-gradient(circle at 18% 78%, rgba(var(--primary-rgb), 0.07) 0%, transparent 30%);
}

.app-layout.tone-profile-detail .glass-bg {
  background:
    radial-gradient(circle at 74% 14%, rgba(var(--primary-rgb), 0.14) 0%, transparent 32%),
    radial-gradient(circle at 18% 84%, rgba(var(--primary-rgb), 0.05) 0%, transparent 24%);
}

.app-layout.tone-proxies .glass-bg {
  background:
    linear-gradient(180deg, rgba(var(--primary-rgb), 0.04), transparent 24%),
    radial-gradient(circle at 86% 12%, rgba(var(--primary-rgb), 0.08) 0%, transparent 28%);
}

.app-layout.tone-automation .glass-bg {
  background:
    radial-gradient(circle at 76% 16%, rgba(var(--primary-rgb), 0.15) 0%, transparent 26%),
    linear-gradient(180deg, rgba(var(--primary-rgb), 0.04), transparent 22%);
}

.app-layout.tone-logs .glass-bg {
  background:
    linear-gradient(180deg, rgba(255,255,255,0.018), transparent 18%),
    radial-gradient(circle at 84% 14%, rgba(var(--primary-rgb), 0.08) 0%, transparent 22%);
}

.notice-stack {
  position: fixed;
  top: 22px;
  right: 22px;
  z-index: 120;
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: min(360px, calc(100vw - 32px));
  pointer-events: none;
}

.notice-card {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px;
  border-radius: 16px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: var(--notice-bg);
  box-shadow: 0 16px 40px rgba(2, 6, 23, 0.35);
  pointer-events: auto;
}

.notice-card.success { border-color: rgba(16, 185, 129, 0.38); }
.notice-card.info { border-color: rgba(var(--primary-rgb), 0.35); }
.notice-card.warning { border-color: rgba(251, 191, 36, 0.4); }
.notice-card.error { border-color: rgba(248, 113, 113, 0.42); }

.notice-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.notice-copy strong {
  font-size: 0.88rem;
  color: var(--notice-text);
}

.notice-copy span {
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--notice-text-dim);
}

.notice-close {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: 999px;
  border: 1px solid var(--notice-close-border);
  background: var(--notice-close-bg);
  color: var(--notice-text-dim);
  cursor: pointer;
}

.notice-close:hover {
  color: var(--notice-text);
  background: rgba(var(--primary-rgb), 0.12);
}

.sidebar { width: 280px; padding: 26px 24px; display: flex; flex-direction: column; gap: 20px; overflow-y: auto; background: var(--sidebar-surface); box-shadow: 10px 0 30px rgba(0,0,0,0.04); z-index: 10; }
.logo { display: flex; align-items: center; gap: 12px; }
.logo h1 { font-size: 1.4rem; font-weight: 800; letter-spacing: -0.5px; flex: 1; }
.theme-mode-toggle { background: var(--glass); border: none; padding: 10px; border-radius: 999px; color: var(--text-dim); transition: all 0.2s ease; }
.theme-mode-toggle:hover { background: var(--glass-strong); color: var(--primary); transform: rotate(15deg) scale(1.1); }
.dot.pulse { width: 12px; height: 12px; background: var(--primary); border-radius: 50%; box-shadow: 0 0 15px rgba(var(--primary-rgb), 0.8); }

.nav-links { display: flex; flex-direction: column; gap: 8px; }
.nav-item { position: relative; padding: 14px 18px; border-radius: 999px; cursor: pointer; color: var(--text-dim); overflow: hidden; font-weight: 500; transition: background var(--motion-base) ease, color var(--motion-fast) ease, transform var(--motion-fast) var(--motion-emphasis), box-shadow var(--motion-base) ease; }
.nav-item.active { background: rgba(var(--primary-rgb), 0.1); color: var(--primary); font-weight: 700; box-shadow: 0 4px 12px rgba(var(--primary-rgb), 0.05); }
.nav-item:hover:not(.active) { background: rgba(var(--primary-rgb), 0.05); color: var(--text); transform: translateX(4px); }
.nav-item.register-btn { margin-top: 10px; background: rgba(var(--primary-rgb), 0.05); color: var(--text-dim); text-align: center; font-size: 0.85rem; border: none; }
.nav-item.register-btn:hover { background: rgba(var(--primary-rgb), 0.15); color: var(--text); transform: translateY(-1px); }
.nav-item.register-btn.warn { color: #fbbf24; }
.nav-item.register-btn.warn:hover { border-color: rgba(251, 191, 36, 0.7); color: #fde68a; }

.theme-panel { padding: 14px; border-radius: var(--radius-lg); background: var(--glass); box-shadow: 0 10px 30px rgba(0,0,0,0.05); }
.theme-panel-head { display: flex; flex-direction: column; gap: 4px; margin-bottom: 12px; }
.theme-panel-head strong { font-size: 0.88rem; }
.theme-panel-head span { font-size: 0.74rem; line-height: 1.45; color: var(--text-dim); }
.custom-theme-box { display: flex; flex-direction: column; gap: 12px; }
.color-picker-wrapper { position: relative; width: 100%; height: 42px; border-radius: 999px; overflow: hidden; background: var(--glass); border: 1px solid rgba(var(--primary-rgb), 0.15); transition: border-color 0.2s ease; }
.color-picker-wrapper:hover { border-color: rgba(var(--primary-rgb), 0.4); }
.color-input { position: absolute; inset: -5px; width: calc(100% + 10px); height: calc(100% + 10px); cursor: pointer; border: none; background: none; }
.palette-icon { position: absolute; right: 14px; top: 13px; color: var(--text-dim); pointer-events: none; opacity: 0.6; }
.preset-chips { display: grid; grid-template-columns: repeat(6, 1fr); gap: 6px; }
.color-chip { aspect-ratio: 1; border-radius: 999px; border: 2px solid transparent; cursor: pointer; transition: transform 0.2s cubic-bezier(0.34, 1.56, 0.64, 1), border-color 0.2s ease, box-shadow 0.2s ease; }
.color-chip:hover { transform: scale(1.18); box-shadow: 0 4px 12px rgba(0,0,0,0.1); }
.color-chip.active { border-color: #fff; box-shadow: 0 0 0 1px rgba(var(--primary-rgb), 0.4), 0 4px 12px rgba(var(--primary-rgb), 0.2); }

.privacy-note { margin-top: auto; padding-top: 14px; border-top: 1px solid rgba(255,255,255,0.06); font-size: 0.74rem; color: var(--text-dim); line-height: 1.55; }
.privacy-note p + p { margin-top: 6px; }
.privacy-note .privacy-tip { color: #8ea0b8; }
.privacy-note code { display: block; margin-top: 8px; padding: 6px 8px; border-radius: 8px; background: rgba(0, 0, 0, 0.25); color: var(--text); word-break: break-all; max-height: 58px; overflow-y: auto; }

.main-content { flex: 1; display: flex; flex-direction: column; padding: 30px; }
.top-bar { padding: 18px 24px; border-radius: var(--radius-lg); display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 24px; gap: 20px; background: var(--panel-surface); box-shadow: var(--shadow-soft); z-index: 5; position: relative; }
.top-bar-copy { display: flex; flex-direction: column; gap: 12px; min-width: 0; }
.top-bar-title-row { display: flex; flex-direction: column; gap: 4px; }
.top-bar-title-row h2 { font-size: 1.15rem; letter-spacing: -0.02em; }
.top-bar-title-row span { font-size: 0.82rem; line-height: 1.5; color: var(--text-dim); }
.top-bar-meta { display: flex; gap: 10px; flex-wrap: wrap; }
.summary-chip { display: inline-flex; align-items: baseline; gap: 8px; padding: 7px 14px; border-radius: 999px; background: var(--glass); color: var(--text-dim); font-size: 0.76rem; transition: transform var(--motion-fast) var(--motion-emphasis), background var(--motion-base) ease; border: none; }
.summary-chip:hover { transform: translateY(-1px); background: rgba(var(--primary-rgb), 0.1); color: var(--text); }
.summary-chip b { color: var(--text); font-size: 0.8rem; }
.top-bar-tools { flex: 1; display: flex; justify-content: flex-end; align-items: center; gap: 16px; min-width: 0; flex-wrap: wrap; }
.search-box { flex: 1; min-width: 240px; max-width: 420px; display: flex; align-items: center; padding: 10px 18px; border-radius: 999px; background: var(--glass); transition: transform var(--motion-base) ease, box-shadow var(--motion-base) ease; border: none; }
.search-box:focus-within { background: var(--glass-strong); box-shadow: 0 4px 20px rgba(var(--primary-rgb), 0.15); transform: translateY(-1px); }
.search-input { background: none; border: none; color: var(--text); width: 100%; outline: none; font-size: 0.9rem; }
.actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.btn-ghost { padding: 9px 20px; border-radius: 999px; background: var(--glass); color: var(--text-dim); transition: all var(--motion-fast) ease; font-size: 0.85rem; border: none; font-weight: 600; }
.btn-ghost:hover { background: var(--glass-strong); color: var(--text); transform: translateY(-2px); box-shadow: 0 8px 16px rgba(0,0,0,0.08); }
.detail-top-actions .btn-ghost { }

.btn-create { background: var(--primary); color: var(--btn-text, #000); padding: 10px 24px; border-radius: 999px; font-weight: 700; white-space: nowrap; flex-shrink: 0; box-shadow: 0 8px 20px rgba(var(--primary-rgb), 0.2); }
.btn-create:hover { background: var(--primary-hover); box-shadow: 0 12px 28px rgba(var(--primary-rgb), 0.28); }

.list-view { flex: 1; overflow-y: auto; min-height: 0; }
.content-body { flex: 1; display: flex; flex-direction: column; min-height: 0; }
.view-shell { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.empty-state { display: flex; align-items: center; justify-content: center; min-height: 180px; border-radius: var(--radius-lg); border: 1px dashed var(--border); color: var(--text-dim); }

.profiles-workspace { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 18px; overflow-y: auto; padding-right: 8px; }
.profiles-filter-bar { display: flex; gap: 10px; flex-wrap: wrap; margin-top: -4px; position: sticky; top: 0; z-index: 4; padding: 2px 0 8px; background: linear-gradient(180deg, rgba(var(--bg-rgb), 0.94), rgba(var(--bg-rgb), 0.72) 78%, transparent 100%); backdrop-filter: blur(10px); }
.category-pill { display: inline-flex; align-items: center; gap: 10px; padding: 10px 16px; border-radius: 999px; border: none; background: var(--glass); color: var(--text-dim); font-size: 0.82rem; font-weight: 600; transition: transform var(--motion-fast) ease, background var(--motion-base) ease, box-shadow var(--motion-base) ease; }
.category-pill:hover { transform: translateY(-2px); background: var(--glass-strong); box-shadow: 0 8px 16px rgba(0,0,0,0.08); }
.category-pill b { color: var(--text); font-size: 0.82rem; }
.category-pill.active { background: rgba(var(--primary-rgb), 0.15); color: var(--text); box-shadow: 0 4px 16px rgba(var(--primary-rgb), 0.15); }
.profiles-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 20px; align-content: start; }
.profile-tile { position: relative; overflow: hidden; padding: 20px; border-radius: var(--radius-lg); border: none; display: flex; flex-direction: column; gap: 16px; min-height: 0; transition: transform var(--motion-base) var(--motion-emphasis), background var(--motion-base) ease, box-shadow var(--motion-base) ease; background: var(--panel-surface); box-shadow: var(--shadow-soft); animation: tileEnter calc(var(--motion-slow) + 60ms) var(--motion-smooth) both; animation-delay: calc(var(--tile-index) * 26ms); z-index: 1; }
.profile-tile::after { content: ''; position: absolute; inset: 0; background: linear-gradient(135deg, rgba(var(--primary-rgb), 0.05), transparent 40%, transparent 100%); opacity: 0; transition: opacity var(--motion-base) ease; pointer-events: none; }
.profile-tile:hover { transform: translateY(-4px); box-shadow: var(--shadow-strong); z-index: 2; }
.profile-tile:hover::after { opacity: 1; }
.profile-tile-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 10px; min-height: 54px; }
.profile-tile h3 { font-size: 1rem; line-height: 1.2; word-break: break-word; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.profile-title-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.profile-id-row { display: flex; align-items: center; gap: 8px; margin-top: 4px; }
.profile-created-at { font-size: 0.72rem; color: var(--text-dim); }
.btn-inline-copy { border: none; background: rgba(var(--primary-rgb), 0.1); color: var(--primary); font-size: 0.72rem; padding: 6px 12px; border-radius: 999px; cursor: pointer; font-weight: 600; transition: all 0.2s ease; }
.btn-inline-copy:hover { background: var(--primary); color: white; transform: scale(1.05); }
.btn-inline-copy.subtle { background: var(--glass); color: var(--text-dim); }
.btn-inline-copy.subtle:hover { background: var(--glass-strong); color: var(--text); }
.btn-small { background: var(--glass); color: var(--text-dim); border-radius: 10px; min-width: 52px; height: 34px; padding: 0 10px; font-size: 0.73rem; }
.btn-small.del:hover { color: #ef4444; }

.profile-meta-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; flex: 1; }
.profile-meta-line { display: flex; flex-direction: column; gap: 4px; }
.profile-meta-line strong,
.profile-meta-line span:not(.label) { font-size: 0.84rem; line-height: 1.45; color: var(--text); word-break: break-word; }
.meta-wide { grid-column: 1 / -1; }
.start-url { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.data-row { display: flex; justify-content: space-between; font-size: 0.85rem; }
.label { color: var(--text-dim); }

.btn-group-main { display: flex; gap: 8px; }
.btn-launch { flex: 1; background: linear-gradient(135deg, var(--primary), rgba(var(--primary-rgb), 0.78)); color: var(--btn-text, #000); padding: 12px; border-radius: 999px; font-weight: 800; box-shadow: 0 8px 24px rgba(var(--primary-rgb), 0.18); transition: transform var(--motion-fast) var(--motion-emphasis), box-shadow var(--motion-base) ease; }
.btn-launch:hover { box-shadow: 0 12px 30px rgba(var(--primary-rgb), 0.26); transform: translateY(-1px); }
.btn-verify { width: 78px; background: var(--glass); border: 1px solid var(--border); border-radius: 999px; font-size: 0.86rem; font-weight: 700; }

.btn-group-sub { display: flex; gap: 8px; }
.btn-action-ghost { flex: 1; background: var(--glass); border: none; padding: 10px; border-radius: 999px; font-size: 0.78rem; font-weight: 600; color: var(--text-dim); transition: transform var(--motion-fast) var(--motion-emphasis), background var(--motion-base) ease, box-shadow var(--motion-base) ease, color var(--motion-fast) ease; }
.btn-action-ghost:hover { transform: translateY(-2px); background: var(--glass-strong); color: var(--text); box-shadow: 0 8px 20px rgba(0,0,0,0.06); }
.btn-action-ghost.warn:hover { color: #f87171; background: rgba(248, 113, 113, 0.1); }
.btn-action-ghost.automation-action:hover { color: var(--primary-ink); background: var(--primary-surface); }

.profile-primary-actions { display: grid; grid-template-columns: 1fr; gap: 8px; }
.profile-secondary-actions { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
.profile-secondary-actions.compact { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.profile-secondary-actions .btn-action-ghost { min-height: 40px; }

.app-layout.tone-profiles .top-bar,
.app-layout.tone-profile-detail .top-bar {
  background:
    linear-gradient(180deg, rgba(255,255,255,0.055), rgba(255,255,255,0.024)),
    radial-gradient(circle at right top, rgba(var(--primary-rgb), 0.08), transparent 42%);
}

.app-layout.tone-profiles .profile-tile,
.app-layout.tone-profile-detail .profile-tile {
  border-color: rgba(255,255,255,0.06);
}

.app-layout.tone-profiles .profile-meta-grid,
.app-layout.tone-profile-detail .detail-overview-grid {
  position: relative;
}

.app-layout.tone-profiles .profile-meta-grid::before,
.app-layout.tone-profile-detail .detail-overview-grid::before {
  content: '';
  position: absolute;
  inset: -10px;
  border-radius: calc(var(--radius-lg) + 4px);
  background: linear-gradient(135deg, rgba(var(--primary-rgb), 0.05), transparent 55%);
  pointer-events: none;
  opacity: 0.7;
}

.profile-detail-page { padding: 24px; border-radius: var(--radius-xl); border: 1px solid var(--border); display: flex; flex-direction: column; gap: 20px; background: var(--panel-surface); box-shadow: var(--shadow-strong); }
.detail-page-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
.detail-page-breadcrumb { font-size: 0.78rem; color: var(--text-dim); margin-bottom: 8px; }
.detail-page-head h3 { font-size: 1.45rem; line-height: 1.2; }
.detail-page-head-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.detail-overview-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; }
.detail-card { display: flex; flex-direction: column; gap: 6px; padding: 16px; border-radius: var(--radius-lg); border: 1px solid rgba(255,255,255,0.05); background: rgba(255,255,255,0.03); box-shadow: var(--surface-highlight); }
.detail-card strong { font-size: 0.92rem; line-height: 1.5; word-break: break-word; }
.detail-action-group { display: flex; flex-direction: column; gap: 12px; }
.detail-action-group h4 { font-size: 0.92rem; }
.detail-action-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; }
.detail-action-grid.secondary { grid-template-columns: repeat(4, minmax(0, 1fr)); }
.detail-action-grid .btn-action-ghost,
.detail-action-grid .btn-launch,
.detail-action-grid .btn-verify { min-height: 44px; }
.detail-action-grid .btn-verify.wide { width: 100%; }
.detail-session-panel { display: flex; flex-direction: column; gap: 12px; padding: 16px; border-radius: var(--radius-lg); background: rgba(var(--primary-rgb), 0.06); border: 1px solid rgba(var(--primary-rgb), 0.14); box-shadow: inset 0 1px 0 rgba(255,255,255,0.03); }

/* Modal */
.modal-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.7); backdrop-filter: blur(8px); display: flex; align-items: center; justify-content: center; z-index: 100; }
.modal { width: 480px; padding: 32px; border-radius: var(--radius-xl); border: none; display: flex; flex-direction: column; gap: 24px; box-shadow: var(--shadow-strong); background: var(--bg-panel); }
.modal.wide { width: 800px; }

.field { display: flex; flex-direction: column; gap: 8px; text-align: left; }
.field label { font-size: 0.85rem; font-weight: 600; color: var(--text-dim); }
.field input, .field select, .field textarea { background: var(--glass); border: none; border-radius: var(--radius-md); padding: 14px; color: var(--text); outline: none; transition: background var(--motion-base) ease, box-shadow var(--motion-base) ease; }
.field input:focus, .field select:focus, .field textarea:focus { background: var(--glass-strong); box-shadow: 0 4px 16px rgba(0,0,0,0.05); }
select {
  color: var(--text);
  background-color: var(--bg-elevated);
}
select option {
  color: var(--text);
  background: var(--bg-elevated);
}

.proxy-inputs, .proxy-test-box { display: flex; gap: 8px; }
.field select,
.proxy-inputs select {
  appearance: none;
  background-image: url("data:image/svg+xml;charset=UTF-8,%3csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='%238a8a8a'%3e%3cpath d='M7 10l5 5 5-5z'/%3e%3c/svg%3e");
  background-repeat: no-repeat;
  background-position: right 10px center;
  background-size: 16px;
  padding-right: 34px;
}
.proxy-inputs select {
  width: 100px; 
}
.proxy-inputs input, .proxy-test-box input { flex: 1; }
.btn-test { background: var(--primary); color: var(--btn-text, #000); padding: 0 15px; border-radius: 10px; font-weight: 600; }

.hint { font-size: 0.7rem; color: var(--text-dim); }
.test-res { font-size: 0.8rem; color: #ef4444; }
.test-res.ok { color: #10b981; }

.modal-footer { display: flex; justify-content: flex-end; gap: 12px; }
.btn-ghost { padding: 12px 20px; color: var(--text-dim); }
.btn-solid { background: var(--primary); color: var(--btn-text, #000); padding: 12px 28px; border-radius: 999px; font-weight: 700; transition: transform var(--motion-fast) var(--motion-emphasis), box-shadow var(--motion-base) ease; }
.btn-solid:hover { transform: translateY(-1px); box-shadow: 0 12px 28px rgba(var(--primary-rgb), 0.2); }

.editor { width: 100%; height: 400px; background: #000; color: #10b981; font-family: 'JetBrains Mono', monospace; padding: 20px; border-radius: 12px; font-size: 0.9rem; }

.glass { background: var(--glass); backdrop-filter: blur(var(--blur-strength)); box-shadow: var(--surface-highlight); transition: background 0.28s ease, border-radius 0.28s ease, box-shadow 0.28s ease; }

.fade-enter-active, .fade-leave-active { transition: 0.3s; }
.fade-enter-from, .fade-leave-to { opacity: 0; transform: scale(0.95); }
.view-swap-enter-active,
.view-swap-leave-active {
  transition:
    opacity var(--motion-base) ease,
    transform var(--motion-base) var(--motion-emphasis),
    filter var(--motion-base) ease;
}
.view-swap-enter-from,
.view-swap-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.992);
  filter: blur(8px);
}
.notice-pop-enter-active,
.notice-pop-leave-active {
  transition:
    opacity var(--motion-fast) ease,
    transform var(--motion-base) var(--motion-emphasis);
}
.notice-pop-enter-from,
.notice-pop-leave-to {
  opacity: 0;
  transform: translateY(-10px) scale(0.96);
}
.notice-pop-move,
.tile-shift-move {
  transition: transform var(--motion-slow) var(--motion-emphasis);
}
.tile-shift-enter-active,
.tile-shift-leave-active {
  transition:
    opacity var(--motion-fast) ease,
    transform var(--motion-base) var(--motion-emphasis);
}
.tile-shift-enter-from,
.tile-shift-leave-to {
  opacity: 0;
  transform: translateY(14px) scale(0.985);
}
.tile-shift-leave-active {
  position: absolute;
}

/* Proxy Table Styles */
.proxy-table { width: 100%; border-collapse: collapse; text-align: left; border-radius: var(--radius-md); overflow: hidden; }
.proxy-table th, .proxy-table td { padding: 16px; border-bottom: 1px solid rgba(0,0,0,0.03); }
.proxy-table th { background: rgba(255,255,255,0.02); font-size: 0.85rem; color: var(--text-dim); }
.proxy-table td code { color: var(--primary); }
.proxy-table tbody tr { transition: background var(--motion-base) ease, transform var(--motion-fast) var(--motion-emphasis); }
.empty-table { text-align: center; color: var(--text-dim); padding: 28px 16px; }

.app-layout.tone-proxies .top-bar,
.app-layout.tone-automation .top-bar,
.app-layout.tone-logs .top-bar {
  background: var(--bg-panel);
  box-shadow: var(--shadow-soft);
}

.app-layout.tone-proxies .proxy-table {
  background: var(--bg-panel);
  box-shadow: var(--shadow-soft);
  border: none;
}

.app-layout.tone-proxies .proxy-table th {
  background:
    linear-gradient(180deg, rgba(var(--primary-rgb), 0.08), rgba(255,255,255,0.02));
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 0.76rem;
}

.app-layout.tone-proxies .proxy-table tbody tr:hover {
  background: rgba(var(--primary-rgb), 0.05);
}

.status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 8px; background: #9ca3af; }
.status-dot.online { background: #10b981; box-shadow: 0 0 10px #10b981; }
.status-dot.offline { background: #ef4444; }

.btn-icon { background: var(--glass); border: none; color: var(--text-dim); font-size: 0.8rem; cursor: pointer; padding: 8px 14px; border-radius: 999px; transition: all 0.2s ease; font-weight: 600; }
.btn-icon:hover { color: var(--text); background: var(--glass-strong); box-shadow: 0 4px 12px rgba(0,0,0,0.05); transform: translateY(-1px); }
.btn-icon.del:hover { color: #ffffff; background: #ef4444; }

.proxy-add-form { display: flex; gap: 10px; flex-shrink: 1; min-width: 0; }
.proxy-add-form input { background: var(--glass); border: none; border-radius: 999px; padding: 10px 18px; color: var(--text); width: 170px; min-width: 80px; flex: 1; font-size: 0.85rem; outline: none; }
.btn-create.add { padding: 10px 20px; font-size: 0.85rem; }

.automation-view { flex: 1; overflow-y: auto; min-height: 0; padding-right: 10px; }
.automation-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 20px; }
.automation-card { padding: 24px; border-radius: var(--radius-lg); border: none; display: flex; flex-direction: column; gap: 18px; background: var(--panel-surface); box-shadow: var(--shadow-soft); transition: transform var(--motion-base) var(--motion-emphasis), box-shadow var(--motion-base) ease; }
.automation-card:hover { transform: translateY(-2px); }
.automation-wide { grid-column: 1 / -1; }
.automation-card-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.automation-card-head h3 { font-size: 1.05rem; }
.automation-meta { display: flex; flex-direction: column; gap: 12px; }
.automation-meta code,
.session-url { color: var(--primary-ink); background: var(--code-bg); padding: 6px 10px; border-radius: 10px; word-break: break-all; }
.automation-launch-summary {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 16px;
  border-radius: 16px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.03);
}
.automation-launch-summary strong {
  font-size: 0.9rem;
  color: var(--text);
}
.automation-launch-summary span {
  font-size: 0.82rem;
  line-height: 1.5;
  color: var(--text-dim);
}
.automation-launch-summary code,
.session-target {
  color: var(--primary-ink);
  background: var(--code-bg);
  padding: 8px 10px;
  border-radius: 10px;
  word-break: break-all;
}
.automation-launch-summary.info { border-color: rgba(var(--primary-rgb), 0.25); }
.automation-launch-summary.success { border-color: rgba(16, 185, 129, 0.28); }
.automation-launch-summary.warning { border-color: rgba(251, 191, 36, 0.32); }
.automation-session-list { display: flex; flex-direction: column; gap: 14px; }
.automation-session-row { display: flex; justify-content: space-between; gap: 16px; padding: 16px; border-radius: var(--radius-md); border: none; background: var(--glass); transition: transform var(--motion-fast) var(--motion-emphasis), background var(--motion-base) ease; }
.automation-session-row:hover { transform: translateY(-1px); background: var(--glass-strong); box-shadow: 0 4px 12px rgba(0,0,0,0.03); }
.automation-session-main { display: flex; flex-direction: column; gap: 10px; min-width: 0; flex: 1; }
.automation-session-title { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.automation-session-meta { display: flex; gap: 14px; flex-wrap: wrap; color: var(--text-dim); font-size: 0.82rem; }
.automation-session-actions { display: flex; gap: 8px; align-items: flex-start; justify-content: flex-end; flex-wrap: wrap; width: 440px; }
.status-chip { display: inline-flex; align-items: center; justify-content: center; min-width: 72px; padding: 4px 10px; border-radius: 999px; font-size: 0.75rem; border: 1px solid rgba(255,255,255,0.08); color: var(--text-dim); background: rgba(255,255,255,0.04); }
.status-chip.online { color: var(--primary-ink); border-color: rgba(var(--primary-rgb), 0.35); background: var(--primary-surface); }
.status-chip.subtle { min-width: auto; }
.status-chip-button { cursor: pointer; }
.status-chip-button:hover { border-color: rgba(var(--primary-rgb), 0.45); color: var(--text); }
.automation-empty { color: var(--text-dim); text-align: center; padding: 24px; border: 1px dashed var(--border); border-radius: 16px; }
.automation-example { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.example-block { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.example-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.automation-snippet { margin: 0; padding: 18px; border-radius: 20px; background: var(--code-bg-strong); color: var(--text); font-family: 'JetBrains Mono', monospace; font-size: 0.78rem; line-height: 1.55; white-space: pre-wrap; word-break: break-word; border: none; box-shadow: inset 0 0 0 1px rgba(0,0,0,0.02); }
.automation-active { color: var(--primary-ink); font-weight: 600; }

.app-layout.tone-automation .top-bar {
  background:
    linear-gradient(180deg, rgba(255,255,255,0.04), rgba(255,255,255,0.018)),
    linear-gradient(90deg, rgba(var(--primary-rgb), 0.11), transparent 42%);
}

.app-layout.tone-automation .automation-card {
  box-shadow: var(--shadow-strong);
}

.app-layout.tone-automation .automation-snippet {
  box-shadow: var(--surface-highlight);
}

.app-layout.tone-automation .automation-session-row {
  background:
    linear-gradient(180deg, rgba(255,255,255,0.03), rgba(255,255,255,0.015));
}

/* Logs View Styles */
.logs-view { flex: 1; min-height: 0; padding: 20px; font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; overflow-y: auto; text-align: left; border-radius: var(--radius-lg); background: var(--code-bg-strong); border: none; transition: box-shadow var(--motion-base) ease; box-shadow: var(--shadow-soft); }
.log-line { margin-bottom: 6px; line-height: 1.4; }
.log-line.error { color: #f87171; }
.log-line.info { color: #10b981; }
.log-line.warn { color: #fbbf24; }
.log-line .time { color: #6b7280; font-size: 0.75rem; margin-right: 8px; }
.log-line .level { font-weight: bold; margin-right: 8px; width: 60px; display: inline-block; }
.empty-logs { color: var(--text-dim); text-align: center; margin-top: 40px; }

.app-layout.tone-logs .top-bar {
  background:
    linear-gradient(180deg, rgba(255,255,255,0.035), rgba(255,255,255,0.014)),
    linear-gradient(90deg, rgba(var(--primary-rgb), 0.08), transparent 34%);
}

.app-layout.tone-logs .logs-view {
  background:
    linear-gradient(180deg, rgba(1, 4, 9, 0.98), rgba(0, 0, 0, 0.96));
  box-shadow: inset 0 0 0 1px rgba(var(--primary-rgb), 0.08), 0 18px 38px rgba(0,0,0,0.24);
}

.app-layout.tone-logs .log-line {
  position: relative;
  padding-left: 10px;
}

.app-layout.tone-logs .log-line::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0.32rem;
  bottom: 0.32rem;
  width: 2px;
  border-radius: 999px;
  background: rgba(255,255,255,0.08);
}

.app-layout.tone-logs .log-line.info::before { background: rgba(16, 185, 129, 0.75); }
.app-layout.tone-logs .log-line.warn::before { background: rgba(251, 191, 36, 0.75); }
.app-layout.tone-logs .log-line.error::before { background: rgba(248, 113, 113, 0.82); }

/* Pending URL Banner */
.pending-url-banner {
  margin-bottom: 20px;
  padding: 16px 24px;
  border-radius: var(--radius-md);
  border: 1px solid rgba(var(--primary-rgb), 0.4);
  background: rgba(var(--primary-rgb), 0.1);
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
.profiles-workspace,
.automation-view,
.list-view,
.logs-view,
.editor,
.privacy-note code {
  scroll-behavior: smooth;
  scrollbar-width: thin;
  scrollbar-color: var(--scrollbar-thumb) var(--scrollbar-track);
}

.sidebar::-webkit-scrollbar,
.profiles-workspace::-webkit-scrollbar,
.automation-view::-webkit-scrollbar,
.list-view::-webkit-scrollbar,
.logs-view::-webkit-scrollbar,
.editor::-webkit-scrollbar,
.privacy-note code::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.sidebar::-webkit-scrollbar-track,
.profiles-workspace::-webkit-scrollbar-track,
.automation-view::-webkit-scrollbar-track,
.list-view::-webkit-scrollbar-track,
.logs-view::-webkit-scrollbar-track,
.editor::-webkit-scrollbar-track,
.privacy-note code::-webkit-scrollbar-track {
  background: var(--scrollbar-track);
  border-radius: 999px;
}

.sidebar::-webkit-scrollbar-thumb,
.profiles-workspace::-webkit-scrollbar-thumb,
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
.profiles-workspace::-webkit-scrollbar-thumb:hover,
.automation-view::-webkit-scrollbar-thumb:hover,
.list-view::-webkit-scrollbar-thumb:hover,
.logs-view::-webkit-scrollbar-thumb:hover,
.editor::-webkit-scrollbar-thumb:hover,
.privacy-note code::-webkit-scrollbar-thumb:hover {
  background: linear-gradient(180deg, rgba(var(--primary-rgb), 0.98), var(--scrollbar-thumb-hover));
}

@keyframes ambientFloat {
  from { transform: translate3d(0, 0, 0) scale(1); }
  to { transform: translate3d(4vw, -3vh, 0) scale(1.08); }
}

@keyframes tileEnter {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}

@media (max-width: 980px) {
  .notice-stack {
    top: 14px;
    right: 14px;
    left: 14px;
    width: auto;
  }

  .top-bar {
    flex-direction: column;
    align-items: stretch;
  }

  .top-bar-tools {
    width: 100%;
    justify-content: stretch;
  }

  .search-box {
    max-width: none;
  }

  .actions {
    width: 100%;
    justify-content: flex-start;
  }

  .automation-grid,
  .automation-example {
    grid-template-columns: 1fr;
  }

  .automation-session-row {
    flex-direction: column;
  }

  .automation-session-actions {
    width: 100%;
    justify-content: stretch;
  }
}

@media (max-width: 720px) {
  .profiles-grid {
    grid-template-columns: 1fr;
  }

  .profile-primary-actions,
  .profile-secondary-actions {
    grid-template-columns: 1fr;
  }

  .detail-page-head,
  .detail-overview-grid,
  .detail-action-grid,
  .detail-action-grid.secondary {
    grid-template-columns: 1fr;
    display: grid;
  }

  .btn-verify {
    width: 100%;
  }
}
</style>
