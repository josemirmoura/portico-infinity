#!/usr/bin/env python3
"""Explicit operator opt-in for Infinity recursive deletion under existing /opt scope.

Only modifies filesystem.actions to include remove_recursive. Does not increase
read/write path roots, scope ceiling or host/shell/Docker privileges.
Use from the independently authenticated operator console, never from MCP.
"""
from __future__ import annotations

import argparse
from pathlib import Path
import os
import re
import stat
import sys
import tempfile

EXPECTED_HEAD = "1b2bfba22ca9d3a513470781f904adcc8d31472d"


class PolicyError(ValueError):
    pass


def section(text: str, name: str) -> tuple[int, int, str]:
    matches = list(re.finditer(r"(?m)^([a-z_]+):[ \t]*$", text))
    hits = [i for i, m in enumerate(matches) if m.group(1) == name]
    if len(hits) != 1:
        raise PolicyError(f"expected exactly one {name} section")
    i = hits[0]
    start = matches[i].end()
    end = matches[i + 1].start() if i + 1 < len(matches) else len(text)
    return start, end, text[start:end]


def values(block: str, key: str) -> list[str]:
    match = re.search(rf"(?m)^  {re.escape(key)}:[ \t]*\n", block)
    if match is None:
        raise PolicyError(f"missing {key} list")
    items = []
    for line in block[match.end():].splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if re.fullmatch(r"    - [a-z_./-]+[ \t]*", line):
            items.append(line.strip()[2:].strip())
        else:
            break
    return items


def prepare(policy: str, env: str) -> tuple[str, bool]:
    if len(re.findall(r"(?m)^VPS_AGENT_SCOPE_ROOT=/opt[ \t]*$", env)) != 1:
        raise PolicyError("physical ceiling must be /opt")
    if len(re.findall(r"(?m)^VPS_AGENT_WHOLE_HOST=0[ \t]*$", env)) != 1:
        raise PolicyError("whole host must remain disabled")
    if not re.search(r"(?m)^mode: scoped[ \t]*$", policy):
        raise PolicyError("policy mode must remain scoped")
    _, _, features = section(policy, "features")
    if not re.search(r"(?m)^  full_mode_enabled: false[ \t]*$", features):
        raise PolicyError("full mode must remain disabled")
    fs_start, fs_end, filesystem = section(policy, "filesystem")
    for key in ("read", "write"):
        if values(filesystem, key) != ["/opt"]:
            raise PolicyError(f"filesystem.{key} must be exactly /opt")
    actions = values(filesystem, "actions")
    if len(actions) != len(set(actions)) or "remove" not in actions or "*" in actions:
        raise PolicyError("unexpected existing filesystem actions")
    if "remove_recursive" in actions:
        return policy, False
    action_block = re.search(
        r"(?m)^  actions:[ \t]*\n((?:    - [a-z_]+[ \t]*\n)+)", filesystem
    )
    if action_block is None:
        raise PolicyError("noncanonical filesystem.actions; refuse to edit")
    body = action_block.group(1)
    if "    - remove\n" not in body:
        raise PolicyError("remove action not in canonical list")
    replacement = body.replace("    - remove\n", "    - remove\n    - remove_recursive\n", 1)
    updated_fs = filesystem[: action_block.start(1)] + replacement + filesystem[action_block.end(1) :]
    updated = policy[:fs_start] + updated_fs + policy[fs_end:]
    _, _, final_fs = section(updated, "filesystem")
    if values(final_fs, "actions") != actions[:actions.index("remove") + 1] + ["remove_recursive"] + actions[actions.index("remove") + 1:]:
        raise PolicyError("postcondition failed")
    return updated, True


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--policy", type=Path, default=Path("/srv/portico-infinity/config/policy.yaml"))
    parser.add_argument("--env", type=Path, default=Path("/srv/portico-infinity/.env"))
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--ack-opt-recursive", action="store_true",
                        help="acknowledge recursive deletion will be available within ALL current /opt writable roots")
    args = parser.parse_args()
    try:
        if os.geteuid() != 0:
            raise PolicyError("must run as the independent root operator")
        if args.policy.is_symlink() or args.env.is_symlink():
            raise PolicyError("refusing symlink inputs")
        for p in (args.policy, args.env):
            st = p.stat()
            if not stat.S_ISREG(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
                raise PolicyError("policy or env has unsafe owner/mode")
        if args.apply and not args.ack_opt_recursive:
            raise PolicyError("--apply requires --ack-opt-recursive")
        original = args.policy.read_text(encoding="utf-8")
        result, changed = prepare(original, args.env.read_text(encoding="utf-8"))
        if not changed:
            print("ALREADY_ENABLED: remove_recursive is in filesystem.actions")
            return 0
        if not args.apply:
            print("VALID: may add remove_recursive to filesystem.actions under /opt; no changes made")
            return 0
        st = args.policy.stat()
        fd, name = tempfile.mkstemp(prefix=".policy-recursive-", dir=str(args.policy.parent))
        try:
            os.fchmod(fd, stat.S_IMODE(st.st_mode))
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                f.write(result)
                f.flush()
                os.fsync(f.fileno())
            os.replace(name, args.policy)
        finally:
            if os.path.exists(name):
                os.unlink(name)
        print("POLICY_UPDATED: remove_recursive enabled under existing /opt roots")
        return 0
    except (OSError, PolicyError, ValueError) as exc:
        print(f"INFINITY RECURSIVE ACTION DENIED: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
