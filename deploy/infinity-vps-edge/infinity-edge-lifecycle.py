#!/usr/bin/env python3
"""Owner-only, isolated lifecycle for the Infinity VPS HTTPS edge bridge.

This script never deletes the shared Traefik network, parent Traefik container,
DNS, TLS material, WireGuard, firewall, identity, controller, or workspaces.
Only the correctly labeled portico-infinity-edge bridge container is mutable.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

ROOT = Path("/opt/portico-infinity-edge")
PROJECT = "portico-infinity-edge"
SERVICE = "bridge"
SHARED_NETWORK = "traefik-public"
CONFIG = ROOT / "infinity-upstreams.yml"
SCRIPT = ROOT / "infinity-edge-lifecycle.py"
HEX_ID = re.compile(r"[a-f0-9]{12,64}\Z")


class EdgeBlocked(RuntimeError):
    pass


def run(argv: list[str]) -> str:
    try:
        process = subprocess.run(argv, capture_output=True, text=True,
                                 check=False, timeout=35)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise EdgeBlocked("Docker dependency unavailable") from exc
    if process.returncode:
        # Do not print stderr, environment variables, internal URLs or secrets.
        raise EdgeBlocked("Docker operation failed: " + " ".join(argv[:2]))
    return process.stdout


def protected(path: Path, *, directory: bool) -> None:
    ancestors = list(reversed(path.parents))
    for obj in (*ancestors, path):
        try:
            info = obj.lstat()
        except FileNotFoundError as exc:
            raise EdgeBlocked("expected root-owned edge install is absent") from exc
        if (stat.S_ISLNK(info.st_mode) or info.st_uid != 0 or
                info.st_mode & 0o022):
            raise EdgeBlocked("unsafe edge path ownership, link or permission")
        if not (stat.S_ISDIR(info.st_mode) if obj == path and directory
                else stat.S_ISREG(info.st_mode) if obj == path else
                stat.S_ISDIR(info.st_mode)):
            raise EdgeBlocked("unsafe edge path type")


def verify_owner() -> None:
    if os.geteuid() != 0 or Path.cwd().resolve() != ROOT:
        raise EdgeBlocked("run as root from /opt/portico-infinity-edge")
    protected(ROOT, directory=True)
    protected(SCRIPT, directory=False)
    protected(ROOT / "compose.yaml", directory=False)
    protected(CONFIG, directory=False)


def ids(raw: str) -> list[str]:
    lines = [v.strip() for v in raw.splitlines() if v.strip()]
    if len(lines) != len(set(lines)) or not all(HEX_ID.fullmatch(v) for v in lines):
        raise EdgeBlocked("invalid or ambiguous Docker container identity")
    return lines


def one_json(raw: str) -> dict:
    try:
        values = json.loads(raw)
    except (ValueError, TypeError) as exc:
        raise EdgeBlocked("unreadable Docker inspect") from exc
    if not isinstance(values, list) or len(values) != 1 or not isinstance(values[0], dict):
        raise EdgeBlocked("ambiguous Docker inspect")
    return values[0]


def checked_container(ident: str, command=run) -> dict:
    if not HEX_ID.fullmatch(ident):
        raise EdgeBlocked("invalid Docker container ID")
    obj = one_json(command(["docker", "inspect", ident]))
    labels = (obj.get("Config") or {}).get("Labels") or {}
    state = obj.get("State") or {}
    bindings = (obj.get("HostConfig") or {}).get("PortBindings")
    networks = (obj.get("NetworkSettings") or {}).get("Networks")
    mounts = obj.get("Mounts")
    if (obj.get("Id", "").lower() != ident or
            labels.get("com.docker.compose.project") != PROJECT or
            labels.get("com.docker.compose.service") != SERVICE or
            labels.get("com.docker.compose.project.working_dir") != str(ROOT) or
            labels.get("traefik.enable") != "true" or
            not isinstance(state.get("Running"), bool) or
            bindings not in ({}, None) or
            not isinstance(networks, dict) or set(networks) != {SHARED_NETWORK} or
            not isinstance(mounts, list) or len(mounts) != 1):
        raise EdgeBlocked("unverifiable or foreign HTTPS bridge container")
    mount = mounts[0]
    if (mount.get("Type") != "bind" or mount.get("Source") != str(CONFIG) or
            mount.get("Destination") != "/etc/traefik/infinity-upstreams.yml" or
            mount.get("RW") is not False):
        raise EdgeBlocked("unexpected bridge mount or foreign user data")
    return {"id": ident, "running": state["Running"], "restart": (obj.get("HostConfig") or {}).get("RestartPolicy", {}).get("Name")}


def inventory(command=run) -> list[dict]:
    # The Compose file MUST have only the bridge service and external
    # traefik-public. Any project-owned network/volume is unexpected.
    net = command(["docker", "network", "ls", "--no-trunc",
                   "--filter", "label=com.docker.compose.project=" + PROJECT,
                   "--format", "{{.ID}}"])
    vol = command(["docker", "volume", "ls",
                   "--filter", "label=com.docker.compose.project=" + PROJECT,
                   "--format", "{{.Name}}"])
    if net.strip() or vol.strip():
        raise EdgeBlocked("unexpected private edge network or Docker volume")
    found = ids(command(["docker", "ps", "-aq", "--no-trunc",
                         "--filter", "label=com.docker.compose.project=" + PROJECT,
                         "--format", "{{.ID}}"]))
    if len(found) > 1:
        raise EdgeBlocked("multiple containers claim exclusive edge ownership")
    return [checked_container(ident, command) for ident in found]


def change(action: str, command=run) -> tuple[int, int]:
    before = inventory(command)
    if inventory(command) != before:
        raise EdgeBlocked("edge identity changed during preflight")
    touched = 0
    removed = 0
    for item in before:
        ident = item["id"]
        if checked_container(ident, command) != item:
            raise EdgeBlocked("bridge changed before operation")
        command(["docker", "update", "--restart=no", ident])
        touched += 1
        if item["running"]:
            command(["docker", "stop", "--time", "15", ident])
        stopped = checked_container(ident, command)
        if stopped["running"] or stopped["restart"] not in ("no", ""):
            raise EdgeBlocked("bridge did not stop with restart disabled")
        if action == "remove":
            command(["docker", "rm", ident])
            removed += 1
    after = inventory(command)
    if after != ([] if action == "remove" else
                 [{**item, "running": False, "restart": "no"} for item in before]):
        raise EdgeBlocked("unexpected bridge inventory after operation")
    return touched, removed


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Owner-only lifecycle of the isolated Infinity VPS edge BRIDGE ONLY")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--dry-run", action="store_true")
    group.add_argument("--deactivate", action="store_true")
    group.add_argument("--remove-bridge", action="store_true")
    parser.add_argument("--confirm", default="")
    args = parser.parse_args()
    try:
        verify_owner()
        if args.dry_run:
            items = inventory()
            print(json.dumps({
                "owned_project": PROJECT, "owned_service": SERVICE,
                "containers": len(items), "running": sum(x["running"] for x in items),
                "preserved": ["Traefik parent", SHARED_NETWORK, "TLS/ACME", "DNS",
                              "WireGuard", "UFW", "VPS edge configuration",
                              "SRV-IA", "/opt workspaces", "Community", "Cloud"],
                "mutations_performed": False,
            }, indent=2))
            return 0
        expected = ("DEACTIVATE-INFINITY-EDGE" if args.deactivate
                    else "REMOVE-INFINITY-EDGE-BRIDGE")
        if args.confirm != expected:
            raise EdgeBlocked("external operator confirmation required: " + expected)
        stopped, removed = change("deactivate" if args.deactivate else "remove")
        print("INFINITY EDGE BRIDGE LIFECYCLE VERIFIED:",
              "stopped=" + str(stopped), "removed=" + str(removed))
        print("PRESERVED: shared Traefik network, parent proxy, TLS, VPN, DNS and all non-Infinity resources")
        return 0
    except (EdgeBlocked, OSError, ValueError) as exc:
        print("INFINITY EDGE LIFECYCLE BLOCKED: " + str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
