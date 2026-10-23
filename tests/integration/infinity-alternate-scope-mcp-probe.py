#!/usr/bin/env python3
"""Standalone MCP E2E for alternate Infinity physical scope, only disposable CI.

Uses a synthetic static bearer. No host or production endpoints are permitted.
"""
import argparse
import json
from pathlib import Path
import time
import urllib.error
import urllib.request

PROTOCOL = "2026-07-28"
ENDPOINT = "http://127.0.0.1:18083/mcp"


def tool(token, name, args=None):
    payload = {
        "jsonrpc": "2.0", "id": 140, "method": "tools/call",
        "params": {
            "name": name, "arguments": args or {},
            "_meta": {
                "io.modelcontextprotocol/protocolVersion": PROTOCOL,
                "io.modelcontextprotocol/clientInfo": {
                    "name": "disposable-alternate-root", "version": "1"
                },
                "io.modelcontextprotocol/clientCapabilities": {},
            },
        },
    }
    request = urllib.request.Request(
        ENDPOINT, method="POST", data=json.dumps(payload).encode(),
        headers={
            "Authorization": "Bearer " + token,
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": PROTOCOL,
            "Mcp-Method": "tools/call",
            "Mcp-Name": name,
        },
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=20) as response:
        assert response.status == 200, "unexpected MCP HTTP status"
        value = json.load(response)
    assert not value.get("error"), "MCP returned JSON-RPC error"
    assert isinstance(value.get("result"), dict), "MCP result absent"
    return value["result"]


def assert_tool(token, name, args, allowed):
    result = tool(token, name, args)
    assert (result.get("isError") is not True) == allowed, (
        f"{name}: unexpected {'allow' if allowed else 'deny'} decision"
    )
    return result


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--scope", required=True)
    p.add_argument("--token-file", type=Path, required=True)
    args = p.parse_args()
    scope = args.scope
    assert scope.startswith("/mnt/portico-e2e-")
    assert scope.count("/") == 2 and ".." not in scope
    workspace = scope + "/project"
    sibling = scope + "/sibling"
    token = args.token_file.read_text().strip()
    assert len(token) >= 40, "missing synthetic token"
    assert_tool(token, "system.info", {}, True)
    target = workspace + "/actual-mcp.txt"
    assert_tool(token, "file.write", {"path": target, "content": "alternate-root-e2e"}, True)
    result = assert_tool(token, "file.read", {"path": target}, True)
    assert "alternate-root-e2e" in json.dumps(result), "MCP persisted file mismatch"
    assert_tool(token, "file.write", {"path": "/opt/alt-scope-escape", "content": "bad"}, False)
    assert_tool(token, "file.write", {"path": "/srv/portico-infinity/escape", "content": "bad"}, False)
    assert_tool(token, "file.write", {"path": "/etc/portico-e2e-escape", "content": "bad"}, False)
    assert_tool(token, "file.read", {"path": "/etc/shadow"}, False)
    assert_tool(token, "shell.exec_admin", {
        "command": "id", "cwd": workspace, "grant_id": "not-issued"
    }, False)
    assert_tool(token, "shell.exec", {
        "command": "printf real-shell-alt-ok > shell-proof.txt",
        "cwd": workspace, "runtime_seconds": 20
    }, True)
    proof = Path(workspace, "shell-proof.txt")
    for _ in range(65):
        if proof.exists():
            assert proof.read_text() == "real-shell-alt-ok", "shell wrote unexpected output"
            break
        time.sleep(1)
    else:
        raise AssertionError("real non-root systemd shell did not write proof")

    assert_tool(token, "shell.exec", {
        "command": "if cat .env >/dev/null 2>&1; then printf LEAKED > masked.txt; else printf DENIED > masked.txt; fi",
        "cwd": workspace, "runtime_seconds": 20
    }, True)
    masked = Path(workspace, "masked.txt")
    for _ in range(65):
        if masked.exists():
            assert masked.read_text() == "DENIED", "Unix-readable .env was exposed"
            break
        time.sleep(1)
    else:
        raise AssertionError("secret masking job did not finish")
    assert Path(sibling, ".env").exists()
    # A sibling is another valid workspace inside the authorized physical ceiling.
    # Only noncanonical, symlinked, ceiling-root or out-of-scope cwd are invalid.
    for cwd in [scope, sibling + "/../project", scope + "/link", "/opt", "/srv/portico-infinity"]:
        assert_tool(token, "shell.exec", {
            "command": "id -u", "cwd": cwd, "runtime_seconds": 20
        }, False)
    assert_tool(token, "file.remove", {"path": target}, True)
    assert not Path(target).exists(), "MCP remove did not commit"
    try:
        tool("invalid-disposable-token", "system.info")
    except urllib.error.HTTPError as e:
        assert e.code == 401, "wrong status for invalid bearer"
    else:
        raise AssertionError("invalid bearer accepted")
    print("INFINITY_REAL_DOCKER_ALTERNATE_SCOPE_MCP_AND_NONROOT_SHELL: PASS")
    print("INFINITY_REAL_DOCKER_ALTERNATE_SCOPE_SECURITY_AND_SECRET_MASKING: PASS")


if __name__ == "__main__":
    main()
