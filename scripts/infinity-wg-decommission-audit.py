#!/usr/bin/env python3
"""Read-only, root-only decommission inventory of wg-infinity on ONE host.

The matching installer creates wg-infinity and key material separately from
Docker and independently of UFW. Neither interface nor firewall may be removed
solely because an Infinity Docker deployment has been removed. Never print
private key material, peer endpoints, or raw firewall output.
"""
from __future__ import annotations
import argparse
import base64
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

WGDIR=Path("/etc/wireguard")
CONF="wg-infinity.conf"
ROLES={
    "vps":{"address":"10.88.88.1/30","allowed":"10.88.88.2/32"},
    "srv-ia":{"address":"10.88.88.2/30","allowed":"10.88.88.1/32"},
}
SERVICE="wg-quick@wg-infinity.service"
KEY=re.compile(r"[A-Za-z0-9+/]{43}=\Z")
MAX_FILE=8192


class AuditBlocked(RuntimeError):
    pass


def secure_file(path: Path, private: bool) -> str:
    # Root-owned ancestors, O_NOFOLLOW, exact mode, single link, size cap;
    # never expose contents in exceptions or stdout.
    for parent in reversed(path.parents):
        info=parent.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid!=0 or
                info.st_mode & 0o022 or stat.S_ISLNK(info.st_mode)):
            raise AuditBlocked("untrusted WireGuard directory")
    info=path.lstat()
    if (not stat.S_ISREG(info.st_mode) or stat.S_ISLNK(info.st_mode) or
            info.st_uid!=0 or info.st_nlink!=1 or info.st_size>MAX_FILE or
            info.st_mode & 0o022 or
            (private and stat.S_IMODE(info.st_mode)!=0o600)):
        raise AuditBlocked("untrusted WireGuard identity file")
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC)
    try:
        opened=os.fstat(fd)
        if (opened.st_ino!=info.st_ino or opened.st_dev!=info.st_dev or
                opened.st_size!=info.st_size):
            raise AuditBlocked("identity file changed during opening")
        data=os.read(fd,MAX_FILE+1)
        if len(data)>MAX_FILE or len(data)!=opened.st_size:
            raise AuditBlocked("identity file changed while reading")
        try:
            return data.decode("ascii")
        except UnicodeError as exc:
            raise AuditBlocked("invalid identity encoding") from exc
    finally:
        os.close(fd)


def wire_key(value: str) -> str:
    if KEY.fullmatch(value) is None:
        raise AuditBlocked("noncanonical WireGuard key")
    try:
        raw=base64.b64decode(value,validate=True)
    except ValueError as exc:
        raise AuditBlocked("invalid WireGuard key encoding") from exc
    if len(raw)!=32 or base64.b64encode(raw).decode("ascii")!=value:
        raise AuditBlocked("invalid WireGuard key material")
    return value


def parse_config(content: str, role: str) -> dict:
    if role not in ROLES:
        raise AuditBlocked("unknown local WireGuard role")
    sections={}
    section=None
    for line in content.splitlines():
        if not line.strip():
            continue
        if len(line)>512 or line.startswith(("#",";")) or "\t" in line:
            raise AuditBlocked("unsupported WireGuard directive")
        if line in ("[Interface]","[Peer]"):
            section=line[1:-1]
            if section in sections:
                raise AuditBlocked("duplicate WireGuard section")
            sections[section]={}
            continue
        if section is None or not re.fullmatch(r"[A-Za-z]+ = [^\s][^\r\n]*",line):
            raise AuditBlocked("noncanonical WireGuard configuration")
        key,value=line.split(" = ",1)
        if key in sections[section]:
            raise AuditBlocked("duplicate WireGuard directive")
        sections[section][key]=value
    if set(sections)!={"Interface","Peer"}:
        raise AuditBlocked("multiple, missing or foreign peers")
    interface=sections["Interface"]
    peer=sections["Peer"]
    expected_i={"Address","PrivateKey","ListenPort"} if role=="vps" else {"Address","PrivateKey"}
    expected_p={"PublicKey","AllowedIPs"} if role=="vps" else {"PublicKey","AllowedIPs","Endpoint","PersistentKeepalive"}
    if set(interface)!=expected_i or set(peer)!=expected_p:
        # This rejects SaveConfig, PostDown/PreDown/PostUp, PreUp, Table,
        # DNS, FwMark, routes, additional hooks and arbitrary commands.
        raise AuditBlocked("foreign WireGuard directives or hooks")
    if (interface["Address"]!=ROLES[role]["address"] or
            peer["AllowedIPs"]!=ROLES[role]["allowed"]):
        raise AuditBlocked("unexpected WireGuard subnet or peer routing")
    if role=="vps":
        if interface["ListenPort"]!="51822":
            raise AuditBlocked("unexpected VPN UDP port")
    else:
        import ipaddress
        match=re.fullmatch(r"([0-9.]+):51822",peer["Endpoint"])
        if not match:
            raise AuditBlocked("unexpected peer endpoint format")
        try:
            ip=ipaddress.IPv4Address(match.group(1))
        except ValueError as exc:
            raise AuditBlocked("invalid endpoint IPv4") from exc
        if ip.is_unspecified or ip.is_multicast or ip.is_loopback or ip.is_link_local:
            raise AuditBlocked("unsafe peer endpoint")
        if peer["PersistentKeepalive"]!="25":
            raise AuditBlocked("unexpected keepalive")
    wire_key(interface["PrivateKey"])
    wire_key(peer["PublicKey"])
    return {"local_private":interface["PrivateKey"],
            "peer_public":peer["PublicKey"],"address":interface["Address"]}


def default_command(args, *, stdin=None):
    try:
        return subprocess.run(args,input=stdin,text=True,capture_output=True,
                              check=False,timeout=12)
    except (OSError,subprocess.TimeoutExpired) as exc:
        raise AuditBlocked("required read-only system utility unavailable") from exc


def check_files(role: str, *, directory: Path=WGDIR, command=default_command) -> dict:
    if os.geteuid()!=0:
        raise AuditBlocked("root console required for private WireGuard inspection")
    if role not in ROLES:
        raise AuditBlocked("invalid host role")
    config=secure_file(directory/CONF,True)
    local=secure_file(directory/("infinity-"+role+".key"),True).strip()
    pub=secure_file(directory/("infinity-"+role+".pub"),False).strip()
    parsed=parse_config(config,role)
    if wire_key(local)!=parsed["local_private"] or wire_key(pub)==parsed["peer_public"]:
        raise AuditBlocked("WireGuard identity does not match installed key material")
    result=command(["wg","pubkey"],stdin=local+"\n")
    if result.returncode!=0 or result.stdout.strip()!=pub:
        raise AuditBlocked("WireGuard private/public keypair mismatch")
    return {"role":role,"local_public_key":pub,"peer_public_key":parsed["peer_public"],
            "address":parsed["address"]}


def probe_runtime(expected: dict, *, command=default_command) -> dict:
    def read(args):
        p=command(args)
        if not isinstance(p.returncode,int) or p.returncode not in (0,1,3,4):
            raise AuditBlocked("system inventory tool returned unexpected status")
        return p
    unit=read(["systemctl","is-enabled",SERVICE])
    enabled=unit.stdout.strip()
    if enabled not in ("enabled","disabled","static","masked","indirect"):
        raise AuditBlocked("cannot classify WireGuard boot policy")
    active=read(["systemctl","is-active",SERVICE])
    status=active.stdout.strip()
    if status not in ("active","inactive","failed","activating","deactivating","unknown"):
        raise AuditBlocked("cannot classify WireGuard service state")
    link=read(["ip","-o","link","show","dev","wg-infinity"])
    live=link.returncode==0
    peers=0
    live_identity_ok=False
    if live:
        actual=read(["wg","show","wg-infinity","peers"])
        keys=[v.strip() for v in actual.stdout.splitlines() if v.strip()]
        peers=len(keys)
        live_identity_ok=(actual.returncode==0 and
                          keys==[expected["peer_public_key"]])
        local_ip=read(["ip","-o","-4","address","show","dev","wg-infinity"])
        # ip -o may include dynamic address metadata; compare by /CIDR token.
        matches=re.findall(r"\binet\s+(\d+\.\d+\.\d+\.\d+/\d+)\b",
                           local_ip.stdout)
        live_identity_ok=live_identity_ok and (
            local_ip.returncode==0 and matches==[expected["address"]])
    return {"service_state":status,"boot_policy":enabled,
            "interface_present":live,"runtime_peers":peers,
            "runtime_matches_file":live_identity_ok if live else None,
            "shared_use_ruled_out":False}


def assess(role: str, *, directory: Path=WGDIR, command=default_command) -> dict:
    keys=check_files(role,directory=directory,command=command)
    runtime=probe_runtime(keys,command=command)
    # UFW rules are operator-owned host state and must not be deleted.
    return {"role":role,"config_exact_installer_shape":True,
            "identity_verified":True,"runtime":runtime,
            "firewall_mutation_authorized":False,
            "vpn_mutation_authorized":False,
            "private_keys_disclosed":False,
            "manual_gates":[
              "verify no other VPN clients, routes, reverse proxies, jobs or management depend on wg-infinity",
              "reconcile and preserve all active jobs and audit/identity backups",
              "inspect UFW UDP 51822 rules and their owner independently; do not delete by port or row number",
              "obtain explicit host-specific owner approval before stopping either peer",
              "retain WireGuard key and config material until separately reviewed for safe retirement",
            ]}


def main() -> int:
    parser=argparse.ArgumentParser(description="Read-only inventory of dedicated Infinity VPN, never deactivate")
    parser.add_argument("--role",required=True,choices=tuple(ROLES))
    parser.add_argument("--inspect",action="store_true",required=True)
    args=parser.parse_args()
    try:
        report=assess(args.role)
        print(json.dumps(report,indent=2,sort_keys=True))
        return 0
    except (AuditBlocked,OSError,ValueError) as exc:
        # Messages deliberately never contain config contents or private key.
        print("INFINITY WG TEARDOWN AUDIT BLOCKED: "+str(exc),file=sys.stderr)
        return 2


if __name__=="__main__":
    raise SystemExit(main())
