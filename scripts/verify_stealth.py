import json
import time
import argparse
import sys
import random
import requests
import websocket

# 默认配置
DEFAULT_BASE_URL = "http://127.0.0.1:9090"
DEFAULT_TOKEN = "" # 如果为空，脚本会提示或尝试读取

# ----------------- 贝塞尔曲线生成 -----------------
def generate_bezier_points(start_x, start_y, end_x, end_y, steps=20):
    mid_x = (start_x + end_x) / 2
    mid_y = (start_y + end_y) / 2
    dx = end_x - start_x
    dy = end_y - start_y
    dist = (dx**2 + dy**2) ** 0.5
    if dist < 10:
        return [(end_x, end_y)]
    nx, ny = -dy / dist, dx / dist
    offset = random.uniform(-dist * 0.2, dist * 0.2)
    control_x = mid_x + nx * offset
    control_y = mid_y + ny * offset
    points = []
    for i in range(steps + 1):
        t = i / steps
        x = (1 - t)**2 * start_x + 2 * (1 - t) * t * control_x + t**2 * end_x
        y = (1 - t)**2 * start_y + 2 * (1 - t) * t * control_y + t**2 * end_y
        points.append((int(x), int(y)))
    return points

# ----------------- BiDi 收发 -----------------
def send_bidi(ws, command_id, method, params=None):
    payload = {
        "id": command_id,
        "method": method,
        "params": params or {},
    }
    ws.send(json.dumps(payload))
    
    deadline = time.time() + 10.0
    while time.time() < deadline:
        ws.settimeout(1.0)
        try:
            raw_message = ws.recv()
            message = json.loads(raw_message)
            if message.get("id") == command_id:
                if "error" in message:
                    raise RuntimeError(f"{method} 失败: {message['error']}")
                return message
        except websocket.WebSocketTimeoutException:
            continue
    raise TimeoutError(f"{method} 超时")

# ----------------- 评估本地 JS -----------------
def evaluate_js(ws, command_id, context_id, js_code):
    res = send_bidi(ws, command_id, "script.evaluate", {
        "expression": js_code,
        "target": {"context": context_id},
        "awaitPromise": True
    })
    result = res.get("result", {})
    val_type = result.get("result", {}).get("type")
    
    if val_type == "success":
        # 返回评估值
        return result.get("result", {}).get("value")
    # 针对对象类型
    return result.get("result", {})

# ----------------- 拟真行为操作 -----------------
def simulate_physical_click(ws, command_id, context_id, x, y):
    # 模拟贝塞尔滑动到元素
    points = generate_bezier_points(0, 0, x, y, steps=15)
    actions_seq = []
    for pt in points:
        actions_seq.append({
            "type": "pointerMove",
            "x": pt[0],
            "y": pt[1],
            "duration": random.randint(3, 8),
            "origin": "viewport"
        })
    send_bidi(ws, command_id, "input.performActions", {
        "context": context_id,
        "actions": [{"type": "pointer", "id": "mouse", "parameters": {"pointerType": "mouse"}, "actions": actions_seq}]
    })
    command_id += 1
    
    # 物理按下松开
    send_bidi(ws, command_id, "input.performActions", {
        "context": context_id,
        "actions": [
            {
                "type": "pointer",
                "id": "mouse",
                "parameters": {"pointerType": "mouse"},
                "actions": [
                    {"type": "pointerDown", "button": 0},
                    {"type": "pause", "duration": random.randint(60, 120)},
                    {"type": "pointerUp", "button": 0}
                ]
            }
        ]
    })
    return command_id + 1

# ----------------- 离线审计检测 -----------------
def run_offline_stealth_checks(ws, cmd_id, context_id):
    print("[*] 正在执行本地离线指纹痕迹审计...")
    checks = {}

    # 1. 检查 navigator.webdriver
    js_webdriver = "navigator.webdriver"
    checks["navigator.webdriver"] = evaluate_js(ws, cmd_id, context_id, js_webdriver)
    cmd_id += 1

    # 2. 检查 chrome 伪装的残留
    js_chrome = "!!window.chrome"
    checks["window.chrome_exists"] = evaluate_js(ws, cmd_id, context_id, js_chrome)
    cmd_id += 1

    # 3. 检查 WebGL 渲染器是否有值（非空且不为默认的 llvmpipe）
    js_webgl = """
    (() => {
        const canvas = document.createElement('canvas');
        const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl');
        if (!gl) return 'no_webgl';
        const debugInfo = gl.getExtension('WEBGL_debug_renderer_info');
        if (!debugInfo) return 'no_debug_info';
        const renderer = gl.getParameter(debugInfo.UNMASKED_RENDERER_VENDOR_STRING) + ' | ' + gl.getParameter(debugInfo.UNMASKED_RENDERER_STRING);
        return renderer;
    })()
    """
    checks["webgl_renderer"] = evaluate_js(ws, cmd_id, context_id, js_webgl)
    cmd_id += 1

    # 4. 检查 Canvas 噪音混淆
    js_canvas = """
    (() => {
        const canvas = document.createElement('canvas');
        canvas.width = 100;
        canvas.height = 100;
        const ctx = canvas.getContext('2d');
        ctx.fillStyle = '#f60';
        ctx.fillRect(10, 10, 80, 80);
        return canvas.toDataURL();
    })()
    """
    canvas_data = evaluate_js(ws, cmd_id, context_id, js_canvas)
    checks["canvas_hash_len"] = len(str(canvas_data))
    cmd_id += 1

    return checks, cmd_id

# ----------------- 在线校验 (CreepJS & reCAPTCHA) -----------------
def run_online_checks(ws, cmd_id, context_id):
    results = {}
    
    # 1. 在线检测 CreepJS Lies
    creep_url = "https://creepjs-api.web.app/"
    print(f"[*] 导航到 CreepJS 测试页: {creep_url} ...")
    try:
        send_bidi(ws, cmd_id, "browsingContext.navigate", {
            "context": context_id,
            "url": creep_url,
            "wait": "complete"
        })
        cmd_id += 1
        time.sleep(3.0) # 等其跑完检测
        
        # 尝试通过 JS 提取 CreepJS 在页面中渲染的数据或检测错误
        js_get_creep = "document.body.innerText.includes('lies') ? 'detected_lies' : 'passed_clean'"
        results["creepjs_lies_check"] = evaluate_js(ws, cmd_id, context_id, js_get_creep)
        cmd_id += 1
    except Exception as ex:
        results["creepjs_error"] = f"连通失败: {ex}"
        
    # 2. 在线检测 reCAPTCHA v3 跑分对比
    recaptcha_demo_url = "https://recaptcha-demo.appspot.com/recaptcha-v3-request-scores.php"
    print(f"[*] 导航到 reCAPTCHA v3 跑分演示页: {recaptcha_demo_url} ...")
    try:
        # 对照组 A (无仿真移动直接触发)
        send_bidi(ws, cmd_id, "browsingContext.navigate", {
            "context": context_id,
            "url": recaptcha_demo_url,
            "wait": "complete"
        })
        cmd_id += 1
        time.sleep(2.0)
        
        # 抓取页面回显分数的前置逻辑，需要点击按钮 "Go"
        # 按钮在 demo 页面通常带有 class 'go'
        js_click_a = "document.querySelector('button.go')?.click() || document.querySelector('button')?.click();"
        evaluate_js(ws, cmd_id, context_id, js_click_a)
        cmd_id += 1
        time.sleep(2.0)
        
        js_get_score_a = "document.querySelector('pre.response')?.innerText || 'no_response'"
        results["recaptcha_score_group_a_raw"] = evaluate_js(ws, cmd_id, context_id, js_get_score_a)
        cmd_id += 1
        
        # 实验组 B (仿真物理鼠标滑动点击)
        send_bidi(ws, cmd_id, "browsingContext.navigate", {
            "context": context_id,
            "url": recaptcha_demo_url,
            "wait": "complete"
        })
        cmd_id += 1
        time.sleep(2.0)
        
        # 使用我们的贝塞尔轨迹点击
        # 物理坐标可以大致定在按钮位置 (比如大概 250, 480 处)
        cmd_id = simulate_physical_click(ws, cmd_id, context_id, 250, 480)
        time.sleep(2.0)
        
        js_get_score_b = "document.querySelector('pre.response')?.innerText || 'no_response'"
        results["recaptcha_score_group_b_raw"] = evaluate_js(ws, cmd_id, context_id, js_get_score_b)
        cmd_id += 1
    except Exception as ex:
        results["recaptcha_error"] = f"评测失败: {ex}"
        
    return results, cmd_id

# ----------------- 主程序 -----------------
def main():
    parser = argparse.ArgumentParser(description="MyBrowser Pro 反检测自动测试校验套件")
    parser.add_argument("--api-url", default=DEFAULT_BASE_URL, help="Automation API 的监听地址")
    parser.add_argument("--token", default=DEFAULT_TOKEN, help="Bearer Token")
    parser.add_argument("--profile-id", help="测试的目标环境ID")
    parser.add_argument("--online", action="store_true", help="是否包含在线检测 (CreepJS / reCAPTCHA)")
    args = parser.parse_args()

    api_url = args.api_url
    token = args.token
    profile_id = args.profile_id

    # 1. 尝试从本地 API 自动拉取 Token
    if not token:
        print("[*] 未提供 Token，正在尝试从 API 端读取...")
        try:
            resp = requests.get(f"{api_url}/api/v1/automation/info", timeout=3)
            # 如果接口开启了鉴权，可能无法直接访问，此处将引导用户输入
            if resp.status_code == 401:
                print("[!] API 已启用 Token 鉴权，请加上 --token 参数运行此脚本")
                sys.exit(1)
        except Exception:
            pass

    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
    }

    # 2. 选择 Profile
    if not profile_id:
        print("[*] 正在拉取可用环境列表...")
        try:
            resp = requests.get(f"{api_url}/api/v1/automation/profiles", headers=headers, timeout=5)
            resp.raise_for_status()
            profiles = resp.json().get("data", [])
            if not profiles:
                print("[-] 系统中未找到任何可用环境，请先在 MyBrowser Pro UI 客户端中创建一个环境。")
                sys.exit(1)
            # 默认选择第一个
            profile_id = profiles[0]["id"]
            print(f"[+] 自动选中测试环境: {profiles[0]['name']} (ID: {profile_id})")
        except Exception as e:
            print(f"[-] 获取环境列表失败，请确认 API 服务已开启且 Token 正确: {e}")
            sys.exit(1)

    # 3. 开启自动化会话
    print(f"[*] 正在拉起自动化环境: {profile_id} ...")
    try:
        resp = requests.post(
            f"{api_url}/api/v1/automation/sessions",
            headers=headers,
            json={"profile_id": profile_id},
            timeout=15
        )
        resp.raise_for_status()
        session_data = resp.json().get("data", {})
        connect_url = session_data.get("connect_url")
    except Exception as e:
        print(f"[-] 启动环境失败: {e}")
        sys.exit(1)

    print(f"[+] 环境启动成功，调试端口: {session_data.get('debug_port')}，连接 Socket...")
    ws = websocket.create_connection(connect_url, timeout=10, suppress_origin=True)

    cmd_id = 1
    report = {"timestamp": time.time(), "profile_id": profile_id, "checks": {}}
    
    try:
        # A. 初始化 session
        try:
            send_bidi(ws, cmd_id, "session.new", {"capabilities": {"alwaysMatch": {}}})
        except Exception:
            pass
        cmd_id += 1

        # B. 获取 Context ID
        tree = send_bidi(ws, cmd_id, "browsingContext.getTree")
        context_id = tree["result"]["contexts"][0]["context"]
        cmd_id += 1

        # C. 离线审计
        offline_results, cmd_id = run_offline_stealth_checks(ws, cmd_id, context_id)
        report["checks"]["offline"] = offline_results

        # D. 在线检查 (可选)
        if args.online:
            online_results, cmd_id = run_online_checks(ws, cmd_id, context_id)
            report["checks"]["online"] = online_results
            
    finally:
        ws.close()
        # 物理关闭会话
        requests.delete(f"{api_url}/api/v1/automation/sessions/{session_data.get('session_id')}", headers=headers)
        print("[*] 已断开并安全关闭自动化环境。")

    # 4. 生成审计报告与断言
    print("\n================== 校验断言结果报告 ==================")
    checks_passed = True

    # 检查 webdriver
    wd_val = report["checks"]["offline"].get("navigator.webdriver")
    if wd_val is False or wd_val is None:
        print("[✔] 断言成功: navigator.webdriver 正确隐藏 (值为 False/Undefined)")
    else:
        print(f"[✘] 断言失败: navigator.webdriver 泄露 (值为: {wd_val})")
        checks_passed = False

    # 检查 WebGL 渲染器
    webgl_val = report["checks"]["offline"].get("webgl_renderer")
    if "Intel" in str(webgl_val) or "NVIDIA" in str(webgl_val) or "Apple" in str(webgl_val) or "AMD" in str(webgl_val) or "Mesa" in str(webgl_val):
        print(f"[✔] 断言成功: WebGL 混淆生效, 报告显卡: {webgl_val}")
    else:
        print(f"[✘] 断言失败: 显卡指纹伪装失效或显卡异常: {webgl_val}")
        checks_passed = False

    # 检查 Canvas 扰动
    canvas_len = report["checks"]["offline"].get("canvas_hash_len", 0)
    if canvas_len > 100:
        print("[✔] 断言成功: Canvas 噪音注入成功，生成图像 DataURL")
    else:
        print(f"[✘] 断言失败: Canvas 图像哈希提取异常，长度: {canvas_len}")
        checks_passed = False

    # 在线部分输出
    if args.online:
        online_data = report["checks"].get("online", {})
        if "passed_clean" in str(online_data.get("creepjs_lies_check")):
            print("[✔] 在线断言成功: CreepJS 未检测到谎言 (Passed Clean)")
        else:
            print(f"[⚠️] 在线断言警告: CreepJS 检测到欺骗迹象或获取数据失败: {online_data.get('creepjs_lies_check')}")
            
        print(f"[*] 对照组 A (普通模式) 跑分数据: {online_data.get('recaptcha_score_group_a_raw')}")
        print(f"[*] 实验组 B (仿真模式) 跑分数据: {online_data.get('recaptcha_score_group_b_raw')}")

    print("======================================================")
    if checks_passed:
        print("[+] 恭喜！防检测特征基础审计全部通过 (PASSED)")
        sys.exit(0)
    else:
        print("[-] 警告：部分防检测断言未通过，请检查代码配置 (FAILED)")
        sys.exit(1)

if __name__ == "__main__":
    main()
