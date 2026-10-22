#!/usr/bin/env python3
"""Fail-closed preflight for the persistent Infinity filesystem ceiling.

Run before init.sh creates .env or policy. The operator chooses one existing,
canonical directory; no implicit creation, whole-host or control-plane overlap.
"""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import stat
import sys

# Root-equivalent system and identity paths cannot be Infinity privileged roots.
# /opt remains the default; /mnt, /media, /data and similarly protected
# operator-created directories are possible explicit alternatives.
RESERVED = (
    "/srv", "/etc", "/usr", "/var", "/root", "/proc", "/sys", "/dev",
    "/run", "/boot", "/home", "/tmp", "/bin", "/sbin", "/lib", "/lib64",
)
VALID_PATH = re.compile(r"/[A-Za-z0-9._/-]+", re.ASCII)


class ScopeError(ValueError):
    pass


def validate_scope(raw: str) -> str:
    if not isinstance(raw, str) or not VALID_PATH.fullmatch(raw):
        raise ScopeError("scope must be an absolute path with safe ASCII components")
    if raw == "/" or os.path.normpath(raw) != raw:
        raise ScopeError("whole-host, noncanonical, dot or trailing-slash scope denied")
    if any(raw == prefix or raw.startswith(prefix + "/") for prefix in RESERVED):
        raise ScopeError("scope overlaps a protected host or identity directory")
    cursor = Path("/")
    parts = Path(raw).parts[1:]
    for index, part in enumerate(parts):
        if part in ("", ".", ".."):
            raise ScopeError("invalid scope component")
        cursor = cursor / part
        try:
            info = os.lstat(cursor)
        except OSError as exc:
            raise ScopeError("scope and all ancestors must already exist") from exc
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise ScopeError("scope may not traverse symlinks or non-directories")
        # The chosen workspace itself may be writable by its project user.
        # It cannot be replaced by that user if every *parent* is protected.
        # A writable ancestor, in contrast, could swap out the selected root.
        if index < len(parts) - 1:
            if info.st_mode & 0o022:
                raise ScopeError("scope ancestors must not be group/other writable")
            if info.st_uid != 0:
                raise ScopeError("scope ancestors must be root-owned")
    return raw


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate the operator-chosen Infinity root without modifying it")
    parser.add_argument("--scope", required=True)
    args = parser.parse_args()
    try:
        accepted = validate_scope(args.scope)
    except ScopeError as exc:
        print("INFINITY SCOPE DENIED: " + str(exc), file=sys.stderr)
        return 2
    print("INFINITY SCOPE OK: " + accepted)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
