import json
import time
import random
import requests
import websocket

BASE_URL = "http://127.0.0.1:9090"
TOKEN = "YOUR_LOCAL_API_TOKEN"
PROFILE_ID = "YOUR_PROFILE_ID"

# ----------------- 贝塞尔曲线鼠标轨迹生成算法 -----------------
def generate_bezier_points(start_x, start_y, end_x, end_y, steps=25):
    """
    生成起点到终点之间的二阶贝塞尔曲线坐标序列
    """
    mid_x = (start_x + end_x) / 2
    mid_y = (start_y + end_y) / 2
    
    dx = end_x - start_x
    dy = end_y - start_y
    dist = (dx**2 + dy**2) ** 0.5
    
    if dist < 15:
        return [(end_x, end_y)]
        
    # 计算法向量以产生垂直于主线段的偏离
    nx = -dy / dist
    ny = dx / dist
    
    # 随机偏移距离 (主距离的 15% - 30% 范围)
    offset = random.uniform(-dist * 0.25, dist * 0.25)
    control_x = mid_x + nx * offset
    control_y = mid_y + ny * offset
    
    points = []
    for i in range(steps + 1):
        t = i / steps
        # 二阶贝塞尔公式
        x = (1 - t)**2 * start_x + 2 * (1 - t) * t * control_x + t**2 * end_x
        y = (1 - t)**2 * start_y + 2 * (1 - t) * t * control_y + t**2 * end_y
        points.append((int(x), int(y)))
    return points

# ----------------- BiDi 协议基础收发 -----------------
def send_bidi(ws, command_id, method, params=None, timeout=10.0):
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

# ----------------- 拟真行为控制接口 -----------------
def move_mouse_stealth(ws, command_id, context_id, start_x, start_y, end_x, end_y, steps=30):
    """
    使用贝塞尔曲线轨迹和非均匀速度，通过 BiDi 接口拟真移动鼠标
    """
    points = generate_bezier_points(start_x, start_y, end_x, end_y, steps=steps)
    
    # 将坐标点转化为一组 pointerMove 动作序列
    actions_seq = []
    for pt in points:
        # 动作执行耗时在 3ms 到 10ms 之间波动，造成物理滑行速度不一致特征
        duration = random.randint(3, 10)
        actions_seq.append({
            "type": "pointerMove",
            "x": pt[0],
            "y": pt[1],
            "duration": duration,
            "origin": "viewport"
        })
        
    params = {
        "context": context_id,
        "actions": [
            {
                "type": "pointer",
                "id": "stealth_mouse",
                "parameters": {"pointerType": "mouse"},
                "actions": actions_seq
            }
        ]
    }
    
    send_bidi(ws, command_id, "input.performActions", params)
    return points[-1] # 返回当前鼠标坐标以便连贯下一次运动

def click_element_stealth(ws, command_id, context_id, x, y):
    """
    在指定坐标模拟物理按下和松开鼠标（带有 50ms - 150ms 压键时延）
    """
    # 1. 移动到目标坐标
    params_move = {
        "context": context_id,
        "actions": [
            {
                "type": "pointer",
                "id": "stealth_mouse",
                "parameters": {"pointerType": "mouse"},
                "actions": [{"type": "pointerMove", "x": x, "y": y, "duration": 50, "origin": "viewport"}]
            }
        ]
    }
    send_bidi(ws, command_id, "input.performActions", params_move)
    command_id += 1
    
    # 2. 模拟点击动作按下和松开
    params_click = {
        "context": context_id,
        "actions": [
            {
                "type": "pointer",
                "id": "stealth_mouse",
                "parameters": {"pointerType": "mouse"},
                "actions": [
                    {"type": "pointerDown", "button": 0},
                    {"type": "pause", "duration": random.randint(50, 150)}, # 模拟人手指压住鼠标的持载时间
                    {"type": "pointerUp", "button": 0}
                ]
            }
        ]
    }
    send_bidi(ws, command_id, "input.performActions", params_click)
    return command_id + 1

def type_text_stealth(ws, command_id, context_id, text):
    """
    以人类输入习惯（随机时延 80ms - 200ms）在当前焦点元素上敲击输入文字
    """
    for char in text:
        actions = [
            {
                "type": "key",
                "id": "stealth_keyboard",
                "actions": [
                    {"type": "keyDown", "value": char},
                    {"type": "keyUp", "value": char}
                ]
            }
        ]
        send_bidi(ws, command_id, "input.performActions", {
            "context": context_id,
            "actions": actions
        })
        command_id += 1
        # 人类打字间歇
        time.sleep(random.uniform(0.08, 0.20))
    return command_id

# ----------------- 示例运行演示 -----------------
if __name__ == "__main__":
    if TOKEN == "YOUR_LOCAL_API_TOKEN" or PROFILE_ID == "YOUR_PROFILE_ID":
        print("请在脚本开头配置有效的 TOKEN 和 PROFILE_ID。")
        exit(1)

    headers = {
        "Authorization": f"Bearer {TOKEN}",
        "Content-Type": "application/json",
    }

    # 1. 向 MyBrowser API 请求启动一个自动化环境会话
    print(f"[*] 正在尝试拉起环境 [{PROFILE_ID}]...")
    resp = requests.post(
        f"{BASE_URL}/api/v1/automation/sessions",
        headers=headers,
        json={"profile_id": PROFILE_ID},
        timeout=15,
    )
    resp.raise_for_status()
    session = resp.json()["data"]
    
    # 2. 建立 WebSocket 连接到魔改浏览器的 BiDi 接口
    connect_url = session["connect_url"]
    print(f"[+] 成功连接至 BiDi Socket: {connect_url}")
    ws = websocket.create_connection(connect_url, timeout=15, suppress_origin=True)

    cmd_id = 1
    try:
        # 3. 初始化会话
        try:
            send_bidi(ws, cmd_id, "session.new", {"capabilities": {"alwaysMatch": {}}})
        except Exception as e:
            print(f"[!] session.new 提示: {e}")
        cmd_id += 1

        # 4. 获取默认的浏览上下文 ID
        tree = send_bidi(ws, cmd_id, "browsingContext.getTree")
        context_id = tree["result"]["contexts"][0]["context"]
        cmd_id += 1

        # 5. 导航到测试页面
        test_url = "https://example.com"
        print(f"[*] 正在导航到: {test_url}")
        send_bidi(ws, cmd_id, "browsingContext.navigate", {
            "context": context_id,
            "url": test_url,
            "wait": "complete"
        })
        cmd_id += 1

        # 6. 执行贝塞尔鼠标移动仿真示例
        # 假设我们鼠标初始在左上角 (0, 0)，要滑动到页面中间某个元素 (400, 300)
        print("[*] 正在执行贝塞尔拟真鼠标轨迹滑动 (0, 0) -> (400, 300)...")
        last_pos = move_mouse_stealth(ws, cmd_id, context_id, 0, 0, 400, 300)
        cmd_id += 1

        # 7. 模拟在 (400, 300) 坐标进行拟真点击
        print("[*] 正在在该坐标模拟物理持载点击...")
        cmd_id = click_element_stealth(ws, cmd_id, context_id, 400, 300)
        
        print("[+] 仿真执行完成！")
    finally:
        ws.close()
