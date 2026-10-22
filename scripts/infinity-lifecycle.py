#!/usr/bin/env python3
"""Owner-only, fail-closed lifecycle for the isolated Infinity Docker runtime.

No operation deletes the controller, audit log, OAuth volumes, model files or
anything below the configured work ceiling. All Docker targets are identified
and ownership-checked before any mutation. Use only on an owner-controlled host.
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

CONTROLLER = Path("/srv/portico-infinity")
PROJECT = "portico-infinity"
SERVICES = frozenset({
    "gateway", "broker", "operator-portal",
    "zitadel-api", "zitadel-login", "zitadel-postgres",
})
NETWORKS = frozenset({
    "portico-infinity_default", "portico-infinity_integrated-auth",
})
HEX_ID = re.compile(r"[0-9a-f]{12,64}\Z")


class LifecycleError(RuntimeError):
    pass


def run(args: list[str]) -> str:
    try:
        result = subprocess.run(args, capture_output=True, text=True,
                                check=False, timeout=30)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise LifecycleError("cannot execute lifecycle dependency") from exc
    if result.returncode:
        # Never echo external command stderr: it might contain credentials.
        raise LifecycleError("command failed: " + " ".join(args[:2]))
    return result.stdout


def _single_json(value: str) -> dict:
    try:
        records = json.loads(value)
    except (ValueError, TypeError) as exc:
        raise LifecycleError("malformed Docker inspect response") from exc
    if not isinstance(records, list) or len(records) != 1 or not isinstance(records[0], dict):
        raise LifecycleError("ambiguous Docker inspect response")
    return records[0]


def _ids(text: str) -> list[str]:
    result = [item.strip() for item in text.splitlines() if item.strip()]
    if len(result) != len(set(result)) or not all(HEX_ID.fullmatch(i) for i in result):
        raise LifecycleError("invalid or repeated Docker ID")
    return result


def inventory(command=run) -> tuple[list[dict], list[dict], list[str]]:
    containers: list[dict] = []
    ids = _ids(command(["docker", "ps", "-aq", "--no-trunc",
                        "--filter", "label=com.docker.compose.project=" + PROJECT,
                        "--format", "{{.ID}}"]))
    for ident in ids:
        item = _single_json(command(["docker", "inspect", ident]))
        labels = (item.get("Config") or {}).get("Labels") or {}
        service = labels.get("com.docker.compose.service")
        state = (item.get("State") or {}).get("Running")
        if (labels.get("com.docker.compose.project") != PROJECT
                or labels.get("com.docker.compose.project.working_dir") != str(CONTROLLER)
                or service not in SERVICES or not isinstance(state, bool)
                or item.get("Id", "").lower() != ident):
            raise LifecycleError("unverifiable or foreign Compose container")
        containers.append({"id": ident, "service": service, "running": state})

    networks: list[dict] = []
    net_ids = _ids(command(["docker", "network", "ls", "--no-trunc",
                            "--filter", "label=com.docker.compose.project=" + PROJECT,
                            "--format", "{{.ID}}"]))
    for ident in net_ids:
        item = _single_json(command(["docker", "network", "inspect", ident]))
        labels = item.get("Labels") or {}
        name = item.get("Name")
        attached = item.get("Containers") or {}
        if (item.get("Id", "").lower() != ident or name not in NETWORKS
                or labels.get("com.docker.compose.project") != PROJECT
                or not isinstance(attached, dict)
                or any(container_id not in ids for container_id in attached)):
            raise LifecycleError("unverifiable, foreign or shared Docker network")
        networks.append({"id": ident, "name": name, "attached": sorted(attached)})

    # Informational only. Private volumes are NEVER deleted by this tool.
    volumes = command(["docker", "volume", "ls",
                       "--filter", "label=com.docker.compose.project=" + PROJECT,
                       "--format", "{{.Name}}"]).splitlines()
    volumes = [name.strip() for name in volumes if name.strip()]
    if len(volumes) != len(set(volumes)) or any(
            not re.fullmatch(r"portico-infinity_[A-Za-z0-9_-]+", name)
            for name in volumes):
        raise LifecycleError("unverifiable Docker volume inventory")
    for name in volumes:
        item = _single_json(command(["docker", "volume", "inspect", name]))
        if (item.get("Name") != name or
                (item.get("Labels") or {}).get("com.docker.compose.project") != PROJECT):
            raise LifecycleError("foreign Docker volume in inventory")
    return containers, networks, volumes


def ensure_no_active_workers(command=run) -> None:
    # Actual Infinity systemd-run job IDs use vps-agent-filecopy-<32 hex>.
    # The earlier portico-filecopy-only probe missed running jobs and could
    # allow the controller to stop with an independent privileged copy active.
    # Include activating oneshots: Type=oneshot workers remain "activating"
    # until the binary exits. A retained active/exited unit is conservatively
    # rejected until the operator reconciles/cleans it out of band.
    for prefix in ("vps-agent-filecopy-", "portico-filecopy-"):
        output = command(["systemctl", "list-units", "--type=service",
                          "--state=active,activating,deactivating,reloading",
                          "--no-legend", "--no-pager", "--full",
                          prefix + "*.service"])
        if any(prefix in line and ".service" in line for line in output.splitlines()):
            raise LifecycleError("active file-copy worker: stop/reconcile it out-of-band first")


def change_runtime(action: str, command=run) -> tuple[int, int]:
    # All safety checks complete before the first mutation.
    containers, networks, volumes = inventory(command)
    ensure_no_active_workers(command)
    ensure_no_active_workers(command)
    if inventory(command) != (containers, networks, volumes):
        raise LifecycleError("runtime inventory changed during preflight")
    for item in containers:
        ident = item["id"]
        command(["docker", "update", "--restart=no", ident])
        if item["running"]:
            command(["docker", "stop", "--time", "15", ident])
        after = _single_json(command(["docker", "inspect", ident]))
        if ((after.get("State") or {}).get("Running") is not False
                or after.get("Id", "").lower() != ident):
            raise LifecycleError("container did not stop cleanly")
    if action == "deactivate":
        return len(containers), 0

    # Uninstall RUNTIME ONLY: remove our stopped containers and private networks.
    # Identity volumes, state/audit, policy, code, tunnel, DNS and /opt survive.
    for item in containers:
        command(["docker", "rm", item["id"]])
    remaining = _ids(command(["docker", "ps", "-aq", "--no-trunc",
                              "--filter", "label=com.docker.compose.project=" + PROJECT,
                              "--format", "{{.ID}}"]))
    if remaining:
        raise LifecycleError("unexpected project containers remain; networks preserved")
    for network in networks:
        item = _single_json(command(["docker", "network", "inspect", network["id"]]))
        if (item.get("Id", "").lower() != network["id"] or
                item.get("Name") != network["name"] or
                (item.get("Labels") or {}).get("com.docker.compose.project") != PROJECT
                or item.get("Containers")):
            raise LifecycleError("network changed or remains attached")
        command(["docker", "network", "rm", network["id"]])
    return len(containers), len(networks)


def verify_operator() -> None:
    if os.geteuid() != 0 or Path.cwd().resolve() != CONTROLLER:
        raise LifecycleError("run as root from /srv/portico-infinity only")
    for path in (Path("/"), Path("/srv"), CONTROLLER,
                 CONTROLLER / "scripts", CONTROLLER / "scripts/infinity-lifecycle.py"):
        metadata = path.lstat()
        if (stat.S_ISLNK(metadata.st_mode) or metadata.st_uid != 0
                or metadata.st_mode & 0o022
                or not (stat.S_ISREG(metadata.st_mode) or stat.S_ISDIR(metadata.st_mode))):
            raise LifecycleError("unsafe controller ownership or filesystem type")


def main() -> int:
    parser = argparse.ArgumentParser(description="Infinity owner-only Docker lifecycle")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--dry-run", action="store_true",
                       help="read-only inventory; show preserved resources")
    group.add_argument("--deactivate", action="store_true",
                       help="disable restarts and stop owned Infinity containers")
    group.add_argument("--uninstall-runtime", action="store_true",
                       help="stop/remove owned Infinity containers and private networks; preserve data")
    parser.add_argument("--confirm", default="",
                        help="must equal the exact operation-specific confirmation string")
    args = parser.parse_args()
    try:
        verify_operator()
        if args.dry_run:
            containers, networks, volumes = inventory()
            print("INFINITY READ-ONLY INVENTORY")
            for kind, entries in (("containers", containers), ("networks", networks),
                                  ("preserved volumes", volumes)):
                print(kind + ": " + str(len(entries)))
            print("PRESERVED: /opt, controller, audit, policy, OAuth volumes, backups")
            return 0
        expected = "DEACTIVATE-INFINITY" if args.deactivate else "UNINSTALL-INFINITY-RUNTIME"
        if args.confirm != expected:
            raise LifecycleError("explicit operator confirmation required: " + expected)
        count, removed_networks = change_runtime(
            "deactivate" if args.deactivate else "uninstall")
        print("INFINITY LIFECYCLE VERIFIED: containers=" + str(count) +
              ", removed_networks=" + str(removed_networks) +
              "; state, volumes, audit, /opt preserved")
        return 0
    except (LifecycleError, OSError) as exc:
        print("INFINITY LIFECYCLE BLOCKED: " + str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
