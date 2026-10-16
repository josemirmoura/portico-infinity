#!/usr/bin/env python3
"""Preflight for the isolated Pórtico Infinity control plane.

Reject insecure or symlinked /srv checkouts, writable nested files and
administrative AI-shell identities before invoking Docker or writing state.
"""
from __future__ import annotations

import argparse
import grp
import os
from pathlib import Path
import pwd
import stat
import sys

CONTROLLER = Path("/srv/portico-infinity")
ADMIN_GROUPS = frozenset({"sudo", "wheel", "docker", "lxd", "libvirt"})
MAX_ITEMS = 50000


class LayoutError(ValueError):
    pass


def _check_entry(path: Path, owner_uid: int) -> None:
    st = path.lstat()
    if stat.S_ISLNK(st.st_mode):
        raise LayoutError(f"symlink is not permitted in controller: {path}")
    if st.st_uid != owner_uid:
        raise LayoutError(f"unexpected owner for {path}: {st.st_uid}")
    if st.st_mode & 0o022:
        raise LayoutError(f"group/other write permission on {path}")
    if not (stat.S_ISREG(st.st_mode) or stat.S_ISDIR(st.st_mode)):
        raise LayoutError(f"unexpected special file in controller: {path}")
    if st.st_mode & (stat.S_ISUID | stat.S_ISGID):
        raise LayoutError(f"setuid/setgid item in controller: {path}")


def audit_tree(root: Path, owner_uid: int, *, check_ancestors: bool) -> int:
    if not root.is_absolute() or Path(os.path.normpath(str(root))) != root:
        raise LayoutError("controller path must be absolute and canonical")
    if check_ancestors:
        cursor = root.parent
        while True:
            _check_entry(cursor, 0)
            if cursor == Path("/"):
                break
            cursor = cursor.parent
    if not root.exists():
        raise LayoutError(f"controller does not exist: {root}")
    count = 0
    stack = [root]
    while stack:
        path = stack.pop()
        _check_entry(path, owner_uid)
        count += 1
        if count > MAX_ITEMS:
            raise LayoutError("controller exceeds security preflight file limit")
        if path.is_dir():
            with os.scandir(path) as entries:
                stack.extend(Path(entry.path) for entry in entries)
    return count


def audit_user(username: str, controller_owner: int) -> None:
    try:
        user = pwd.getpwnam(username)
    except KeyError as exc:
        raise LayoutError(f"unknown AI shell identity: {username}") from exc
    if user.pw_uid == 0 or user.pw_uid == controller_owner:
        raise LayoutError("AI shell identity may not be root or control-plane owner")
    groups = {group.gr_name for group in grp.getgrall()
              if username in group.gr_mem or group.gr_gid == user.pw_gid}
    if groups & ADMIN_GROUPS:
        raise LayoutError(f"AI shell identity in privileged group(s): {', '.join(sorted(groups & ADMIN_GROUPS))}")


def main() -> int:
    parser = argparse.ArgumentParser(description="Check root-owned Pórtico Infinity controller layout")
    parser.add_argument("--run-as", required=True, help="Unprivileged AI shell identity")
    args = parser.parse_args()
    try:
        if os.geteuid() != 0:
            raise LayoutError("production installer must run as root")
        n = audit_tree(CONTROLLER, 0, check_ancestors=True)
        audit_user(args.run_as, 0)
    except (LayoutError, OSError) as exc:
        print(f"INFINITY LAYOUT DENIED: {exc}", file=sys.stderr)
        return 2
    print(f"INFINITY LAYOUT OK: {n} protected files/directories; root-owned /srv, separate shell identity")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
