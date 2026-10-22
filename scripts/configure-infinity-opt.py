#!/usr/bin/env python3
"""Enable only the typed privileged /opt file operations for Infinity.

Requires fresh installer-owned files, no whole-host, no root shell.
This does NOT confer sudo, admin shell, apt, Docker or host-wide access.
"""
from __future__ import annotations
import argparse
import importlib.util
import os
from pathlib import Path
import re
import sys
import tempfile

delegate_file = Path(__file__).resolve().with_name("delegate-root.py")
spec = importlib.util.spec_from_file_location("delegated_roots", delegate_file)
delegate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(delegate)

scope_module_file = Path(__file__).resolve().with_name("infinity-scope.py")
scope_spec = importlib.util.spec_from_file_location("infinity_scope", scope_module_file)
scope_module = importlib.util.module_from_spec(scope_spec)
scope_spec.loader.exec_module(scope_module)

FILE_ACTIONS = ("list", "stat", "read", "mkdir", "write", "patch", "copy", "move", "remove", "hash", "chmod", "chown")
ROOT_KEYS = (("filesystem", "read"), ("filesystem", "write"), ("shell", "cwd_roots"))
DENY_LISTS = (("services", "manage"), ("services", "actions"),
              ("docker", "manage"), ("docker", "actions"),
              ("compose", "manage"), ("compose", "actions"),
              ("packages", "manage"), ("packages", "actions"),
              ("users", "manage"), ("users", "actions"),
              ("groups", "manage"), ("groups", "actions"),
              ("firewall", "manage"), ("firewall", "actions"))


class PolicyError(ValueError):
    pass


def scalars(text):
    values, stack = {}, []
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or line.startswith("- "):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        while stack and stack[-1][0] >= indent:
            stack.pop()
        match = re.fullmatch(r"([a-z_]+):(?:[ ]+([^#]+))?", line)
        if not match:
            raise PolicyError("unrecognized YAML policy syntax")
        key, val = match.groups()
        target = tuple(item[1] for item in stack) + (key,)
        if val is None:
            stack.append((indent, key))
        else:
            if target in values:
                raise PolicyError("duplicate policy key: " + ".".join(target))
            values[target] = val.strip().strip(chr(34)).strip(chr(39))
    return values


def configure(policy_path, env_path):
    if policy_path.is_symlink() or not policy_path.is_file():
        raise PolicyError("policy must be an existing regular file")
    if env_path.is_symlink() or not env_path.is_file():
        raise PolicyError("env must be an existing regular file")
    env = delegate.env_values(env_path)
    if env.get("VPS_AGENT_WHOLE_HOST") != "0":
        raise PolicyError("whole-host mode disabled in Infinity")
    try:
        scope = scope_module.validate_scope(env.get("VPS_AGENT_SCOPE_ROOT", ""))
    except scope_module.ScopeError as exc:
        raise PolicyError("unsafe Infinity scope: " + str(exc)) from exc
    text = policy_path.read_text(encoding="utf-8")
    lists = delegate.parse_lists(text)
    fields = scalars(text)
    for field in ROOT_KEYS:
        if lists.get(field) != [scope]:
            raise PolicyError("unauthorized roots: " + ".".join(field))
    expected = {
        ("mode",): "scoped", ("features", "full_mode_enabled"): "false",
        ("privilege", "admin"): "broker-only", ("shell", "enabled"): "true",
        ("shell", "allow_host_read"): "false", ("network", "mode"): "blocked",
    }
    for field, value in expected.items():
        if fields.get(field) != value:
            raise PolicyError("unsafe field: " + ".".join(field))
    if fields.get(("shell", "run_as"), "") in ("", "root"):
        raise PolicyError("shell must use an existing non-root account")
    if fields.get(("enabled",), "false") == "true":
        raise PolicyError("full mode is not allowed")
    for field in DENY_LISTS:
        if lists.get(field, []):
            raise PolicyError("host administration disabled: " + ".".join(field))
    previous_actions = lists.get(("filesystem", "actions"))
    if previous_actions is None or "*" in previous_actions:
        raise PolicyError("filesystem.actions missing or wildcard")
    result = delegate.replace_list(text, ("filesystem", "actions"), FILE_ACTIONS)
    checked = delegate.parse_lists(result)
    if checked.get(("filesystem", "actions")) != list(FILE_ACTIONS):
        raise PolicyError("filesystem postcondition failed")
    fd, tmp = tempfile.mkstemp(prefix=".infinity-policy-", dir=str(policy_path.parent))
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(result)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, policy_path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def main():
    ap = argparse.ArgumentParser(description="Configure /opt-only typed file privileges, NOT sudo")
    ap.add_argument("--policy", type=Path, default=Path("config/policy.yaml"))
    ap.add_argument("--env", type=Path, default=Path(".env"))
    args = ap.parse_args()
    try:
        configure(args.policy, args.env)
    except (PolicyError, OSError, ValueError) as exc:
        print("INFINITY OPT DENIED: " + str(exc), file=sys.stderr)
        return 2
    print("INFINITY OPT: static " + scope_module.validate_scope(delegate.env_values(args.env)["VPS_AGENT_SCOPE_ROOT"]) + ", typed chmod/chown, non-root shell; root and apt DISABLED")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
