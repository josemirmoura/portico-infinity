#!/usr/bin/env python3
"""Probe the SAME ZITADEL bearer after real Broker/Gateway process restart.

Run only inside the isolated CI bridge (network ci-edge), never on a
production MCP or with production tokens. No token, secret, or protected
response body is printed.
"""
from __future__ import annotations

import json
from pathlib import Path
import time
import urllib.error
import urllib.request

TOKEN_FILE = Path("/pki/probe-token.json")
ENDPOINT = "http://portico-ci-e2e-gateway:8080/mcp"
PROTOCOL = "2026-07-28"


class RestartProbeError(RuntimeError):
    pass


def token_from_file() -> str:
    doc = json.loads(TOKEN_FILE.read_text(encoding="utf-8"))
    value = doc.get("access_token")
    if not isinstance(value, str) or len(value) < 30:
        raise RestartProbeError("missing synthetic OAuth bearer")
    return value


def request_tool(name: str, args: dict, bearer: str) -> dict:
    payload = {
        "jsonrpc": "2.0",
        "id": 523,
        "method": "tools/call",
        "params": {
            "name": name,
            "arguments": args,
            "_meta": {
                "io.modelcontextprotocol/protocolVersion": PROTOCOL,
                "io.modelcontextprotocol/clientInfo": {
                    "name": "ci-oauth-restart-probe", "version": "1"
                },
                "io.modelcontextprotocol/clientCapabilities": {},
            },
        },
    }
    req = urllib.request.Request(
        ENDPOINT, data=json.dumps(payload).encode("utf-8"),
        headers={
            "Authorization": "Bearer " + bearer,
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": PROTOCOL,
            "Mcp-Method": "tools/call",
            "Mcp-Name": name,
        },
        method="POST",
    )
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with client.open(req, timeout=12) as response:
        if response.status != 200:
            raise RestartProbeError("MCP status was not 200")
        body = json.load(response)
    if body.get("error") or not isinstance(body.get("result"), dict):
        raise RestartProbeError("MCP returned JSON-RPC error")
    return body["result"]


def probe(token: str) -> None:
    ready = False
    for _ in range(75):
        try:
            info = request_tool("system.info", {}, token)
            if info.get("isError") is True:
                raise RestartProbeError("authenticated information request rejected")
            ready = True
            break
        except (urllib.error.URLError, OSError, TimeoutError):
            time.sleep(1)
    if not ready:
        raise RestartProbeError("Gateway/Broker failed to recover within bounded retries")

    persisted = request_tool(
        "file.read", {"path": "/opt/infinity-oauth-e2e/written.txt"}, token
    )
    if persisted.get("isError") is True or "e2e-real-zitadel-broker" not in json.dumps(persisted):
        raise RestartProbeError("persisted fixture inaccessible after restart")
    outside = request_tool("file.read", {"path": "/etc/shadow"}, token)
    if outside.get("isError") is not True:
        raise RestartProbeError("out-of-scope secret unexpectedly readable")
    control = request_tool("file.read", {"path": "/srv/portico-infinity/.env"}, token)
    if control.get("isError") is not True:
        raise RestartProbeError("protected controller unexpectedly accessible")
    admin = request_tool("shell.exec_admin", {
        "command": "id", "cwd": "/opt/infinity-oauth-e2e",
        "grant_id": "synthetic-missing"
    }, token)
    if admin.get("isError") is not True:
        raise RestartProbeError("administrator shell unexpectedly permitted")
    try:
        request_tool("system.info", {}, "invalid-disposable-restart-token")
    except urllib.error.HTTPError as exc:
        if exc.code != 401:
            raise RestartProbeError("invalid bearer rejected with wrong status") from None
    else:
        raise RestartProbeError("invalid bearer was accepted after restart")

    print("INFINITY_OAUTH_SAME_BEARER_POST_BROKER_GATEWAY_RESTART: PASS")
    print("INFINITY_PERSISTED_SCOPE_AND_ADMIN_DENIAL_AFTER_RESTART: PASS")


if __name__ == "__main__":
    probe(token_from_file())
