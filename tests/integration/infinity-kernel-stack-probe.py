#!/usr/bin/env python3
"""Exercise a real scoped MCP Broker and Gateway before/after QEMU KERNEL reset.

Synthetic static bearer only, NOT OAuth or ZITADEL. Never print the bearer.
"""
from __future__ import annotations
import argparse
import json
from pathlib import Path
import time
import urllib.error
import urllib.request

PROTOCOL="2026-07-28"

def tool(endpoint: str, token: str, name: str, arguments: dict | None=None) -> dict:
    data={"jsonrpc":"2.0","id":52,"method":"tools/call","params":{
        "name":name,"arguments":arguments or {},
        "_meta":{"io.modelcontextprotocol/protocolVersion":PROTOCOL,
                 "io.modelcontextprotocol/clientInfo":{"name":"vm-kernel-test","version":"1"},
                 "io.modelcontextprotocol/clientCapabilities":{}}
    }}
    request=urllib.request.Request(endpoint,data=json.dumps(data).encode(),
        method="POST",headers={
           "Authorization":"Bearer "+token,
           "Content-Type":"application/json",
           "Accept":"application/json, text/event-stream",
           "MCP-Protocol-Version":PROTOCOL,
           "Mcp-Method":"tools/call","Mcp-Name":name})
    client=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with client.open(request,timeout=18) as response:
        assert response.status==200,"unexpected MCP HTTP status"
        doc=json.load(response)
    assert not doc.get("error"),"MCP protocol error"
    assert isinstance(doc.get("result"),dict),"MCP result absent"
    return doc["result"]

def probe(endpoint: str, token: str, phase: str, scope: str) -> None:
    assert phase in ("before","after")
    assert scope.startswith("/opt/infinity-kernel-e2e-")
    for attempt in range(90):
        try:
            status=tool(endpoint,token,"system.info")
            assert status.get("isError") is not True,"authenticated MCP rejected"
            break
        except (urllib.error.URLError,OSError):
            if attempt==89:raise
            time.sleep(1)
    persisted=tool(endpoint,token,"file.read",{"path":scope+"/durable-before.txt"})
    assert persisted.get("isError") is not True
    assert "before-real-kernel-reset" in json.dumps(persisted)
    if phase=="after":
        prior=tool(endpoint,token,"file.read",{"path":scope+"/written-before.txt"})
        assert prior.get("isError") is not True and "marker-before-kernel-reboot" in json.dumps(prior)
    body="marker-"+phase+"-kernel-reboot"
    writing=tool(endpoint,token,"file.write",{"path":scope+"/written-"+phase+".txt","content":body})
    assert writing.get("isError") is not True,"scoped write refused"
    verified=tool(endpoint,token,"file.read",{"path":scope+"/written-"+phase+".txt"})
    assert verified.get("isError") is not True and body in json.dumps(verified)
    outside=tool(endpoint,token,"file.read",{"path":"/etc/shadow"})
    assert outside.get("isError") is True,"forbidden host file readable"
    controller=tool(endpoint,token,"file.read",{"path":"/srv/portico-infinity/policy.yaml"})
    assert controller.get("isError") is True,"controller escaped scoped policy"
    privileged=tool(endpoint,token,"shell.exec_admin",
                    {"command":"id","cwd":scope,"grant_id":"not-issued"})
    assert privileged.get("isError") is True,"admin shell allowed"
    try:
        tool(endpoint,"deliberately-invalid-synthetic-bearer","system.info")
    except urllib.error.HTTPError as exc:
        assert exc.code==401,"bad bearer wrong status"
    else:
        raise AssertionError("bad bearer accepted")
    print("INFINITY_QEMU_REAL_MCP_SCOPED_PERSISTENCE_"+phase.upper()+": PASS")
    print("INFINITY_QEMU_REAL_MCP_ADMIN_SCOPE_AND_INVALID_BEARER_DENIAL_"+phase.upper()+": PASS")

if __name__=="__main__":
    a=argparse.ArgumentParser()
    a.add_argument("--phase",choices=("before","after"),required=True)
    a.add_argument("--scope",required=True)
    a.add_argument("--token-file",type=Path,required=True)
    a.add_argument("--endpoint",required=True)
    args=a.parse_args()
    secret=args.token_file.read_text().strip()
    assert len(secret)>=32,"synthetic token file missing"
    probe(args.endpoint,secret,args.phase,args.scope)
