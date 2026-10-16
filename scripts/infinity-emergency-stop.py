#!/usr/bin/env python3
"""Out-of-band, operator-only emergency STOP for the isolated Infinity runtime.

Never removes files, volumes, networks or images. Never touches Community.
The AI cannot invoke this through the Infinity MCP policy.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

CONTROLLER = Path("/srv/portico-infinity")
PROJECT = "portico-infinity"
TARGETS = ("gateway", "broker")
HEX_ID = re.compile(r"^[a-f0-9]{12,64}$")


class EmergencyStopError(RuntimeError):
    pass


def run_docker(args):
    process = subprocess.run(["docker", *args], capture_output=True, text=True, check=False, timeout=20)
    if process.returncode:
        raise EmergencyStopError("Docker command failed: " + args[0])
    return process.stdout


def discover(run=run_docker):
    ids = run(["ps", "-aq", "--filter", "label=com.docker.compose.project=" + PROJECT,
               "--format", "{{.ID}}"]).splitlines()
    ids = [s.strip() for s in ids if s.strip()]
    if len(set(ids)) != len(ids):
        raise EmergencyStopError("duplicate container identifiers in inventory")
    result = []
    for container_id in ids:
        if not HEX_ID.fullmatch(container_id):
            raise EmergencyStopError("invalid container identifier")
        try:
            container = json.loads(run(["inspect", container_id]))
        except (ValueError, TypeError) as exc:
            raise EmergencyStopError("invalid Docker inspect JSON") from exc
        if not isinstance(container, list) or len(container) != 1:
            raise EmergencyStopError("ambiguous Docker inspect result")
        item = container[0]
        labels = (item.get("Config") or {}).get("Labels") or {}
        if (labels.get("com.docker.compose.project") != PROJECT or
            labels.get("com.docker.compose.project.working_dir") != str(CONTROLLER)):
            raise EmergencyStopError("foreign or unverifiable Compose owner")
        service = labels.get("com.docker.compose.service")
        if not isinstance(service, str) or not service:
            raise EmergencyStopError("missing Compose service label")
        running = (item.get("State") or {}).get("Running")
        if not isinstance(running, bool):
            raise EmergencyStopError("unverifiable container run state")
        result.append({"id": container_id, "service": service, "running": running})
    return result


def stop_guarded(run=run_docker, execute=False):
    items = discover(run)
    if not execute:
        return items
    # Inventory AND ownership are checked for the entire project before
    # any stop. Stop only our exact Gateway and Broker labels.
    for service in TARGETS:
        for item in items:
            if item["service"] != service or not item["running"]:
                continue
            run(["stop", "--time", "10", item["id"]])
            updated = json.loads(run(["inspect", item["id"]]))
            if not isinstance(updated, list) or len(updated) != 1 or (
                updated[0].get("State") or {}).get("Running") is not False:
                raise EmergencyStopError("container stop could not be verified")
    return items


def verify_controller():
    if os.geteuid() != 0:
        raise EmergencyStopError("operator must run emergency stop as root")
    if Path.cwd().resolve() != CONTROLLER:
        raise EmergencyStopError("run only from /srv/portico-infinity")
    for path in (Path("/srv"), CONTROLLER):
        info = path.lstat()
        if path.is_symlink() or info.st_uid != 0 or info.st_mode & 0o022:
            raise EmergencyStopError("unsafe root-owned controller path")


def main():
    parser = argparse.ArgumentParser(description="Stop only Infinity Gateway/Broker after ownership verification")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--dry-run", action="store_true", help="read-only owner-verified inventory")
    group.add_argument("--execute", action="store_true", help="stop only Infinity gateway and broker")
    args = parser.parse_args()
    try:
        verify_controller()
        results = stop_guarded(execute=args.execute)
    except (EmergencyStopError, OSError, subprocess.TimeoutExpired) as exc:
        print("INFINITY EMERGENCY STOP DENIED: " + str(exc), file=sys.stderr)
        return 2
    targeted = [x for x in results if x["service"] in TARGETS and x["running"]]
    print(("STOP VERIFIED: " if args.execute else "INVENTORY VERIFIED: ") +
          str(len(targeted)) + " Infinity Gateway/Broker container(s)")
    if args.dry_run:
        for item in targeted:
            print(item["service"] + ": " + item["id"])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
