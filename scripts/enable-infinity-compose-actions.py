#!/usr/bin/env python3
"""Opt in to typed Compose actions for separately approved /opt project roots.

Only compose.actions changes. Does not grant Docker socket, root shell, or
compose.manage broad roots. Apply on the protected operator checkout only.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import tempfile

ACTIONS = ("validate", "pull", "up", "down")


def section(source: str, name: str) -> tuple[int, int, str]:
    headers = list(re.finditer(r"(?m)^([a-z_]+):[ \t]*$", source))
    indexes = [i for i, m in enumerate(headers) if m.group(1) == name]
    if len(indexes) != 1:
        raise ValueError(f"missing/duplicate section {name}")
    i = indexes[0]
    start = headers[i].end()
    end = headers[i + 1].start() if i + 1 < len(headers) else len(source)
    return start, end, source[start:end]


def list_value(block: str, key: str) -> list[str] | None:
    m = re.search(rf"(?m)^  {re.escape(key)}:[ \t]*(\[[ \t]*\])?[ \t]*$", block)
    if m is None:
        return None
    if m.group(1):
        return []
    values = []
    for line in block[m.end():].splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if line.startswith("    - "):
            if not re.fullmatch(r"    - [a-z_]+[ \t]*", line):
                return None
            values.append(line.strip()[2:].strip())
        elif line.startswith("    "):
            return None
        else:
            break
    return values


def env_value(source: str, key: str) -> str | None:
    matches = re.findall(rf"(?m)^{re.escape(key)}=(.*)$", source)
    return matches[0].strip().strip("\"'") if len(matches) == 1 else None


def prepare(policy: str, env: str) -> tuple[str, bool]:
    if env_value(env, "VPS_AGENT_SCOPE_ROOT") != "/opt":
        raise ValueError("scope must be exactly /opt")
    if env_value(env, "VPS_AGENT_WHOLE_HOST") != "0":
        raise ValueError("whole host mode must be disabled")
    if not re.search(r"(?m)^mode: scoped[ \t]*$", policy):
        raise ValueError("mode must be scoped")
    _, _, features = section(policy, "features")
    if not re.search(r"(?m)^  full_mode_enabled: false[ \t]*$", features):
        raise ValueError("full mode must remain disabled")
    _, _, docker = section(policy, "docker")
    for key in ("manage", "actions"):
        if list_value(docker, key) != []:
            raise ValueError(f"docker.{key} must remain empty")
    start, end, compose = section(policy, "compose")
    for key in ("inspect", "manage"):
        if list_value(compose, key) != []:
            raise ValueError(f"compose.{key} must remain empty")
    current = list_value(compose, "actions")
    if current == list(ACTIONS):
        return policy, False
    if current != []:
        raise ValueError("compose.actions must be empty or already allowlisted")
    match = re.search(r"(?m)^  actions:[ \t]*\[[ \t]*\][ \t]*$", compose)
    if match is None:
        raise ValueError("expected compose.actions: []")
    new_line = "  actions:\n" + "".join(f"    - {a}\n" for a in ACTIONS)
    changed = compose[:match.start()] + new_line + compose[match.end():].lstrip("\n")
    return policy[:start] + changed + policy[end:], True


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--policy", type=Path, default=Path("config/policy.yaml"))
    ap.add_argument("--env", type=Path, default=Path(".env"))
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--ack-typed-compose", action="store_true")
    args = ap.parse_args()
    try:
        if args.apply and not args.ack_typed_compose:
            raise ValueError("--apply requires --ack-typed-compose")
        for path in (args.policy, args.env):
            if path.is_symlink() or not path.is_file():
                raise ValueError(f"expected existing regular file: {path}")
        old = args.policy.read_text(encoding="utf-8")
        new, changed = prepare(old, args.env.read_text(encoding="utf-8"))
        if not changed:
            print("ALREADY_ENABLED")
            return 0
        if not args.apply:
            print("DRY_RUN_OK: will enable only typed compose.validate/pull/up/down")
            return 0
        if os.geteuid() != 0:
            raise ValueError("--apply requires root operator")
        backups = args.policy.resolve().parent.parent / "backups"
        backups.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(backups, 0o700)
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        backup = backups / f"policy-before-compose-actions-{stamp}.yaml"
        shutil.copy2(args.policy, backup)
        os.chmod(backup, 0o600)
        fd, tmp = tempfile.mkstemp(prefix=".policy-compose-", dir=str(args.policy.parent))
        try:
            os.fchmod(fd, stat.S_IRUSR | stat.S_IWUSR)
            with os.fdopen(fd, "w", encoding="utf-8") as output:
                output.write(new)
                output.flush()
                os.fsync(output.fileno())
            os.replace(tmp, args.policy)
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)
        print(f"POLICY_UPDATED; protected backup={backup}")
        print("RESTART_BROKER_ONLY with the installed Compose overlays")
        return 0
    except (OSError, ValueError) as exc:
        print(f"DENIED: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
