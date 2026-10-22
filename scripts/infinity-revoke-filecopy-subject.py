#!/usr/bin/env python3
"""Operator-only persistent filecopy revocation fence, outside MCP authority.

Use only AFTER inspecting the target identity and BEFORE deactivating it in
ZITADEL. This tool fences typed filecopy jobs, but does NOT revoke OAuth
tokens or affect unrelated services. It NEVER deletes a copied file.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

CONTROLLER = Path("/srv/portico-infinity")
STATE = CONTROLLER / "state/filecopy-jobs"
UNIT_PATTERN = re.compile(r"vps-agent-filecopy-[0-9a-f]{32}\Z")
CONFIRMATION = "REVOKE-INFINITY-FILECOPY-SUBJECT"


class RevocationError(RuntimeError):
    pass


def marker_name(subject: str) -> str:
    encoded = subject.encode("utf-8")
    if not 0 < len(encoded) <= 1024:
        raise RevocationError("invalid OAuth subject length")
    return "revoked-subject-" + hashlib.sha256(encoded).hexdigest() + ".marker"


def verify_dir(path: Path) -> None:
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise RevocationError("unsafe root-owned private filecopy state directory")


def ensure_state_dir(directory: Path) -> None:
    parent = directory.parent
    info = parent.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid()
            or info.st_mode & 0o022):
        raise RevocationError("unsafe root-owned filecopy state parent")
    try:
        directory.mkdir(mode=0o700)
        fd = os.open(parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    except FileExistsError:
        pass
    verify_dir(directory)


def read_record(directory: Path, name: str) -> dict:
    # Open relative to a verified directory, never follow symlinks or read
    # unbounded JSON. The operator uses the exact on-disk job identity.
    fd_dir = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd_dir)
        try:
            info = os.fstat(fd)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_nlink != 1 or info.st_mode & 0o077 or info.st_size > 4096):
                raise RevocationError("unsafe filecopy job record")
            raw = os.read(fd, 4097)
            if len(raw) > 4096:
                raise RevocationError("oversized job record")
            record = json.loads(raw)
        finally:
            os.close(fd)
    finally:
        os.close(fd_dir)
    if not isinstance(record, dict):
        raise RevocationError("invalid job record")
    return record


def subject_jobs(directory: Path, subject: str) -> list[str]:
    result = []
    with os.scandir(directory) as entries:
        for entry in entries:
            if not entry.name.endswith(".request.json"):
                continue
            ident = entry.name[:-len(".request.json")]
            if not UNIT_PATTERN.fullmatch(ident):
                raise RevocationError("unrecognized filecopy job manifest")
            record = read_record(directory, entry.name)
            if record.get("job_id") != ident or not isinstance(record.get("subject"), str):
                raise RevocationError("ambiguous filecopy manifest identity")
            if record["subject"] == subject:
                result.append(ident)
    return sorted(result)


def create_fence(directory: Path, subject: str) -> bool:
    name = marker_name(subject)
    fd_dir = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        try:
            fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                         0o600, dir_fd=fd_dir)
        except FileExistsError:
            info = os.stat(name, dir_fd=fd_dir, follow_symlinks=False)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_nlink != 1 or info.st_mode & 0o077 or info.st_size > 256):
                raise RevocationError("unsafe existing revocation fence")
            return False
        try:
            if os.write(fd, b"revoked\n") != len(b"revoked\n"):
                raise RevocationError("short revocation fence write")
            os.fsync(fd)
        finally:
            os.close(fd)
        os.fsync(fd_dir)
        return True
    finally:
        os.close(fd_dir)


def systemctl(*args: str) -> str:
    try:
        completed = subprocess.run(["systemctl", *args], text=True,
                                   capture_output=True, check=False, timeout=45)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RevocationError("cannot verify or stop typed systemd worker") from exc
    if completed.returncode != 0:
        raise RevocationError("typed systemd worker operation failed; reconcile externally")
    return completed.stdout


def reconcile_job(directory: Path, ident: str, command=systemctl) -> str:
    if not UNIT_PATTERN.fullmatch(ident):
        raise RevocationError("invalid typed filecopy unit identity")
    unit = ident + ".service"
    raw = command("show", unit, "--no-pager", "--property=LoadState,ActiveState")
    props = dict(line.split("=", 1) for line in raw.splitlines() if "=" in line)
    if props.get("LoadState") not in ("loaded", "not-found"):
        raise RevocationError("uncertain systemd unit identity")
    state = props.get("ActiveState")
    if state not in ("active", "activating", "deactivating", "reloading", "inactive", "failed"):
        raise RevocationError("unknown systemd unit state; reconcile externally")
    if state in ("active", "activating", "deactivating", "reloading"):
        command("stop", unit)
        raw = command("show", unit, "--no-pager", "--property=ActiveState")
        result = dict(line.split("=", 1) for line in raw.splitlines() if "=" in line)
        if result.get("ActiveState") not in ("inactive", "failed"):
            raise RevocationError("systemd stop not conclusively acknowledged")
    try:
        progress = read_record(directory, ident + ".progress.json")
    except FileNotFoundError:
        return "uncertain"
    if progress.get("job_id") != ident:
        raise RevocationError("filecopy progress identity mismatch")
    phase = progress.get("phase")
    if phase in ("done", "cancelled", "failed"):
        return phase
    return "uncertain"


def verify_operator() -> None:
    if os.geteuid() != 0 or Path.cwd().resolve() != CONTROLLER:
        raise RevocationError("root console required in /srv/portico-infinity")
    for path in (Path("/"), Path("/srv"), CONTROLLER, CONTROLLER / "scripts",
                 CONTROLLER / "scripts/infinity-revoke-filecopy-subject.py"):
        info = path.lstat()
        if (stat.S_ISLNK(info.st_mode) or info.st_uid != 0
                or info.st_mode & 0o022
                or not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode))):
            raise RevocationError("unsafe controller ownership")
    # State may not exist before the first binary copy. A dry-run must
    # remain read-only; only a confirmed operator revocation may create it.


def main() -> int:
    parser = argparse.ArgumentParser(description="Owner-only Infinity filecopy revocation fence")
    parser.add_argument("--subject", required=True, help="exact subject id to fence (not a token)")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--dry-run", action="store_true")
    group.add_argument("--revoke", action="store_true")
    parser.add_argument("--confirm", default="")
    args = parser.parse_args()
    try:
        marker_name(args.subject)
        verify_operator()
        if args.dry_run:
            if STATE.exists() or STATE.is_symlink():
                verify_dir(STATE)
                jobs = subject_jobs(STATE, args.subject)
            else:
                jobs = []
            print("INFINITY FILECOPY REVOCATION DRY-RUN: matching_jobs=" + str(len(jobs)))
            return 0
        if args.confirm != CONFIRMATION:
            raise RevocationError("explicit independent operator confirmation required")
        ensure_state_dir(STATE)
        # Durable fence FIRST; never permit new dispatch while scanning jobs.
        created = create_fence(STATE, args.subject)
        jobs = subject_jobs(STATE, args.subject)
        counts = {"done": 0, "cancelled": 0, "failed": 0, "uncertain": 0}
        for ident in jobs:
            try:
                outcome = reconcile_job(STATE, ident)
            except (RevocationError, OSError, ValueError):
                # One corrupt job cannot prevent fencing/stopping the others.
                # Its outcome is still NOT considered cancelled.
                outcome = "uncertain"
            counts[outcome] += 1
        print("INFINITY FILECOPY FENCE PERSISTED: " + ("created" if created else "existing")
              + "; jobs=" + str(len(jobs)) + "; " +
              ", ".join(k + "=" + str(counts[k]) for k in counts))
        if counts["uncertain"]:
            print("OPERATOR RECONCILIATION REQUIRED: results may be incomplete", file=sys.stderr)
            return 2
        print("NEXT: revoke OAuth identity/session externally and verify token rejection; "
              "this tool does not revoke ZITADEL tokens")
        return 0
    except (OSError, ValueError, RevocationError, json.JSONDecodeError) as exc:
        print("INFINITY FILECOPY REVOCATION BLOCKED: " + str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
