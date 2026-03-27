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
