#!/usr/bin/env python3
"""Real bearer MCP probe for isolated OAuth + filecopy revocation CI.

The caller mounts synthetic lab data at /pki inside a disposable network
container. No host administration or OAuth credentials leave the runner.
"""
import json
import os
import re
import sys
import urllib.error
import urllib.request

if len(sys.argv) != 2 or sys.argv[1] not in ("start", "fence-denies", "token-denies"):
    raise SystemExit("usage: oauth-filecopy-probe.py start|fence-denies|token-denies")
if (os.getenv("CI") != "true" or os.getenv("RUNNER_ENVIRONMENT") != "github-hosted"
        or os.getenv("GITHUB_EVENT_NAME") != "pull_request"):
    raise SystemExit("disposable GitHub PR runner required")

run = os.environ["GITHUB_RUN_ID"]
assert re.fullmatch(r"[0-9]{1,24}", run)
token = json.load(open("/pki/probe-token.json", encoding="utf-8"))["access_token"]
endpoint = "http://portico-ci-e2e-gateway:8080/mcp"
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(name, args):
    request = urllib.request.Request(
        endpoint,
        data=json.dumps({
            "jsonrpc": "2.0", "id": 9, "method": "tools/call",
            "params": {
                "name": name, "arguments": args,
                "_meta": {
                    "io.modelcontextprotocol/protocolVersion": "2026-07-28",
                    "io.modelcontextprotocol/clientInfo": {"name": "oauth-active-filecopy-e2e", "version": "1"},
                    "io.modelcontextprotocol/clientCapabilities": {},
                },
            },
        }).encode(),
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": "2026-07-28",
            "Mcp-Method": "tools/call",
            "Mcp-Name": name,
            "Authorization": "Bearer " + token,
        },
        method="POST",
    )
    with opener.open(request, timeout=40) as response:
        assert response.status == 200
        doc = json.load(response)
    if doc.get("error"):
        raise AssertionError("MCP JSON-RPC request failed: " + str(doc["error"].get("code")))
    return doc["result"]


def start_args(suffix):
    root = "/opt/infinity-oauth-filecopy-" + run
    return {
        "path": root + "/model-source.gguf",
        "destination": root + "/model-destination-" + suffix + ".gguf",
        "max_bytes": 9 << 30,
        "operation_id": "oauth-filecopy-" + suffix + "-" + run,
    }


phase = sys.argv[1]
if phase == "token-denies":
    try:
        call("system.info", {})
    except urllib.error.HTTPError as err:
        if err.code != 401:
            raise
        print("REAL_ZITADEL_REVOKED_TOKEN_MCP_401: PASS")
    else:
        raise AssertionError("deactivated OAuth token was still accepted by Gateway")
elif phase == "fence-denies":
    result = call("file.copy.start", start_args("after-fence"))
    if result.get("isError") is not True:
        raise AssertionError("filecopy fence failed to prevent new dispatch")
    print("REAL_OAUTH_FILECOPY_OPERATOR_FENCE_PREVENTED_NEW_DISPATCH: PASS")
else:
    response = call("file.copy.start", start_args("inflight"))
    if response.get("isError"):
        raise AssertionError("OAuth-authenticated copy denied: " + str(response.get("content", ""))[:500])
    payload = response.get("structuredContent") or {}
    job_id = payload.get("job_id")
    if not isinstance(job_id, str) or not re.fullmatch(r"vps-agent-filecopy-[0-9a-f]{32}", job_id):
        raise AssertionError("missing correctly-scoped typed systemd job id: " + str(payload))
    print(job_id)
