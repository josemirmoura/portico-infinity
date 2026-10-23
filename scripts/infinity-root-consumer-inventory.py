#!/usr/bin/env python3
"""Read-only root-service consumer inventory for a selected Infinity scope.

This is a pre-UAT diagnostic, NOT a confinement mechanism or a safety
certificate. It never reads unit Environment= secrets, invokes shell,
modifies systemd, or accesses files inside the selected workspace.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

UNIT_RE = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.@:+\\-]{0,245}\.service$")
FIELDS = (
    "Id", "LoadState", "ActiveState", "User", "DynamicUser",
    "ExecStart", "ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop",
    "ExecStopPost", "WorkingDirectory", "EnvironmentFiles", "FragmentPath",
    "DropInPaths", "RootDirectory", "RootImage",
)
CONSUMER_FIELDS = tuple(x for x in FIELDS if x not in ("Id", "LoadState", "User", "DynamicUser"))
MAX_UNITS = 2048
MAX_OUTPUT = 4 << 20
LIMITATIONS = [
    "No indirect symlink or dependency resolution; absolute paths to /usr or /etc may reference scope indirectly",
    "Does not inspect cron, timers, containers, privileged scripts, kernel hooks or application-level file reads",
    "A service may generate commands dynamically without a literal scoped path in systemd metadata",
    "Result is an inventory for operator review, not a proof that scoped files cannot elevate privileges",
]


class InventoryIncomplete(RuntimeError):
    pass


def run_systemctl(*args: str) -> str:
    # Fixed typed command, never user-provided shell text or shell=True.
    try:
        proc = subprocess.run(
            ["systemctl", *args], capture_output=True, text=True, timeout=15,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise InventoryIncomplete("systemd inspection command could not complete") from exc
    if proc.returncode != 0 or len(proc.stdout) > MAX_OUTPUT:
        raise InventoryIncomplete("systemd inspection failed or exceeded bounded output")
    return proc.stdout


def list_units() -> list[str]:
    output = run_systemctl("list-units", "--all", "--type=service",
                           "--plain", "--no-legend", "--no-pager", "--no-ask-password")
    units: list[str] = []
    for row in output.splitlines():
        row = row.strip().lstrip("●").strip()
        if not row:
            continue
        name = row.split(None, 1)[0]
        if not UNIT_RE.fullmatch(name):
            raise InventoryIncomplete("unexpected service unit name; inventory incomplete")
        units.append(name)
        if len(units) > MAX_UNITS:
            raise InventoryIncomplete("too many loaded systemd units; inventory incomplete")
    if len(set(units)) != len(units):
        raise InventoryIncomplete("duplicate systemd unit; inventory incomplete")
    if not units:
        raise InventoryIncomplete("no systemd service units returned; inventory incomplete")
    return sorted(units)


def show_unit(name: str) -> dict[str, str]:
    if not UNIT_RE.fullmatch(name):
        raise InventoryIncomplete("invalid service name")
    output = run_systemctl("show", name, "--no-pager", "--no-ask-password",
                           "--property=" + ",".join(FIELDS))
    data: dict[str, str] = {}
    for row in output.splitlines():
        key, sep, value = row.partition("=")
        if not sep or key not in FIELDS or key in data:
            raise InventoryIncomplete("unrecognized or duplicate systemd property")
        data[key] = value
    if data.get("Id") != name:
        raise InventoryIncomplete("unit identity changed between list and show")
    load, active = data.get("LoadState"), data.get("ActiveState")
    # --all includes inactive D-Bus/autostart dependencies that are not
    # installed on some Ubuntu runners. A missing AND inactive service is
    # incapable of consuming workspace files now, but it is counted separately
    # and is not a verified loaded service. Active/unknown missing units fail.
    if load in ("not-found", "masked") and active == "inactive":
        return {"Id": name, "LoadState": load, "ActiveState": active}
    if load != "loaded" or not active:
        raise InventoryIncomplete("service unavailable or uncertain: load_state=" +
                                  (load or "<missing>")[:32] + ", active_state=" +
                                  (active or "<missing>")[:32])
    # systemctl show omits optional properties that have no value at all,
    # e.g. an unset ExecStartPre or EnvironmentFiles. Omission of these
    # optional values is NOT the same as an uninspectable service. Fail
    # closed when the identity, load status, execution or user identity is
    # absent; treat only optional properties as empty.
    required = {"Id", "LoadState", "ActiveState", "User", "DynamicUser", "ExecStart"}
    missing = sorted(required.difference(data))
    if missing:
        raise InventoryIncomplete("systemd omitted required property names: " + ",".join(missing))
    for optional in set(FIELDS).difference(required):
        data.setdefault(optional, "")
    return data


def scope_reference(raw: str, scope: str) -> bool:
    # Find a literal path, not a substring such as /optical or /opt-backup.
    # Never return raw ExecStart or EnvironmentFiles values to output.
    expression = re.compile(r"(?<![A-Za-z0-9_])" + re.escape(scope) +
                            r"(?=/|[^A-Za-z0-9_.-]|$)")
    return bool(expression.search(raw))


def inspect_unit(name: str, data: dict[str, str], scope: str) -> dict | None:
    if data.get("Id") != name:
        raise InventoryIncomplete("service identity changed while inspecting")
    if data.get("LoadState") in ("not-found", "masked") and data.get("ActiveState") == "inactive":
        return None
    referenced = sorted(k for k in CONSUMER_FIELDS if scope_reference(data.get(k, ""), scope))
    if not referenced:
        return None
    user = data.get("User", "").strip()
    dynamic = data.get("DynamicUser", "").strip().lower()
    if dynamic not in ("yes", "no"):
        raise InventoryIncomplete("cannot classify DynamicUser for service")
    # User='' is systemd's default root identity, except DynamicUser=yes.
    privileged = dynamic == "no" and user in ("", "root", "0")
    return {
        "unit": name,
        "classification": "root_consumer_review" if privileged else "nonroot_consumer_reference",
        "referencing_fields": referenced,
    }


def canonical_scope(value: str) -> str:
    if not value.startswith("/") or value in ("/", "/srv", "/etc", "/usr", "/var", "/root"):
        raise InventoryIncomplete("scope must be a non-system absolute path")
    p = Path(value)
    if str(p) != value or ".." in p.parts or "." in p.parts or "\n" in value:
        raise InventoryIncomplete("scope must be a normalized absolute path")
    return value


def inventory(scope: str) -> dict:
    scope = canonical_scope(scope)
    units = list_units()
    findings: list[dict] = []
    unloaded = 0
    for name in units:
        try:
            data = show_unit(name)
            if data.get("LoadState") in ("not-found", "masked"):
                unloaded += 1
            entry = inspect_unit(name, data, scope)
        except InventoryIncomplete as exc:
            raise InventoryIncomplete("cannot fully inspect service " + name + ": " + str(exc)) from exc
        if entry is not None:
            findings.append(entry)
    root_count = sum(x["classification"] == "root_consumer_review" for x in findings)
    return {
        "status": "review_required" if root_count else "no_direct_root_references_detected",
        "scope": scope,
        "systemd_services_inspected": len(units) - unloaded,
        "inactive_unloaded_services": unloaded,
        "root_consumer_count": root_count,
        "findings": findings,
        "limitations": LIMITATIONS,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scope", default="/opt", help="selected physical Infinity ceiling")
    parser.add_argument("--strict", action="store_true",
                        help="exit 3 when a directly referenced root consumer is found")
    args = parser.parse_args()
    try:
        report = inventory(args.scope)
    except InventoryIncomplete as exc:
        print(json.dumps({
            "status": "incomplete", "reason": str(exc),
            "limitations": LIMITATIONS,
        }, sort_keys=True))
        return 2
    print(json.dumps(report, indent=2, sort_keys=True))
    return 3 if args.strict and report["root_consumer_count"] else 0


if __name__ == "__main__":
    sys.exit(main())
