#!/usr/bin/env python3
"""Operator-only, fail-closed retention for finished Pórtico Compose transient units.

Dry-run by default. Apply requires root, explicit operator acknowledgement and\nan exact target-hostname match. Never runs from MCP or as the AI shell identity.
Archives bounded logs and systemd/SQLite terminal evidence OUTSIDE /opt before
releasing a completed unit. Does not change the job database or audit history.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import time

STATE = Path("/srv/portico-infinity/state/state.db")
ARCHIVE = Path("/var/backups/portico-infinity/compose-job-retention")
ID_RE = re.compile(r"vps-agent-compose-[0-9a-f]{32}\Z")
TERMINAL = frozenset({"done", "failed"})
DESCRIPTION = "Portico authorized Docker Compose task"
LOG_LIMIT = 65536


class UnsafeJob(Exception):
    pass


def run(*args: str) -> str:
    result = subprocess.run(args, capture_output=True, text=True, timeout=20, check=False)
    if result.returncode:
        raise UnsafeJob("systemd or journal command returned failure")
    return result.stdout


def parse_unit(raw: str) -> dict[str, str]:
    output: dict[str, str] = {}
    for line in raw.splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            output[k] = v
    return output


def unit_status(unit: str) -> dict[str, str]:
    return parse_unit(run(
        "systemctl", "show", unit, "--no-pager",
        "--property=Id,Description,WorkingDirectory,LoadState,ActiveState,SubState,Result,ExecMainStatus,Type,Transient",
    ))


def unit_is_owned_and_finished(job: dict, unit: dict[str, str]) -> bool:
    name = job["job_id"]
    if not ID_RE.fullmatch(name) or job["unit_name"] != name + ".service":
        return False
    if job["tool"] not in ("compose.pull", "compose.up", "compose.down"):
        return False
    if job["state"] not in TERMINAL or job["exit_code"] is None:
        return False
    if unit.get("LoadState") != "loaded":
        return False
    if unit.get("Id") != job["unit_name"] or unit.get("Description") != DESCRIPTION:
        return False
    if unit.get("Type") != "oneshot" or unit.get("Transient") != "yes":
        return False
    # The transient service must belong to the exact project persisted with the job.
    if not job.get("resource") or unit.get("WorkingDirectory") != job["resource"]:
        return False
    active, sub = unit.get("ActiveState"), unit.get("SubState")
    if (active, sub) not in (("active", "exited"), ("failed", "failed"), ("inactive", "dead")):
        return False
    try:
        actual_exit = int(unit.get("ExecMainStatus", ""))
    except ValueError:
        return False
    if actual_exit != job["exit_code"]:
        return False
    if job["state"] == "done":
        return actual_exit == 0 and unit.get("Result") == "success"
    return actual_exit != 0 and unit.get("Result") != "success"


def protected_archive(path: Path) -> None:
    if not path.is_absolute() or path.resolve() != path or path == Path("/opt") or Path("/opt") in path.parents:
        raise UnsafeJob("archive path must be canonical and outside /opt")
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    if path.is_symlink():
        raise UnsafeJob("archive is symlink")
    st = path.stat()
    if st.st_uid != 0 or st.st_mode & 0o077:
        raise UnsafeJob("archive must belong to root and not permit group/other access")


def atomic_private(path: Path, data: bytes) -> None:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, "wb") as writer:
            writer.write(data)
            writer.flush()
            os.fsync(writer.fileno())
    except BaseException:
        path.unlink(missing_ok=True)
        raise


def archive_job(path: Path, job: dict, unit: dict[str, str]) -> None:
    identity = job["job_id"]
    meta_file = path / (identity + ".json")
    log_file = path / (identity + ".log")
    # Never overwrite an old evidence bundle or follow a pre-existing alias.
    if meta_file.exists() or meta_file.is_symlink() or log_file.exists() or log_file.is_symlink():
        raise UnsafeJob("archive evidence already exists; manual reconciliation required")
    logs = run("journalctl", "--no-pager", "--output=short-iso", "--unit", job["unit_name"], "-n", "150").encode()
    logs = logs[-LOG_LIMIT:]
    meta = {
        "job_id": job["job_id"],
        "unit": job["unit_name"],
        "tool": job["tool"],
        "state": job["state"],
        "exit_code": job["exit_code"],
        "updated_at_unix_ns": job["updated_at"],
        "archived_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "systemd": {k: unit.get(k, "") for k in (
            "Id", "Description", "WorkingDirectory", "LoadState", "ActiveState", "SubState",
            "Result", "ExecMainStatus", "Type", "Transient",
        )},
        "log_bytes": len(logs),
        "log_sha256": hashlib.sha256(logs).hexdigest(),
    }
    encoded = (json.dumps(meta, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    atomic_private(log_file, logs)
    try:
        atomic_private(meta_file, encoded)
    except BaseException:
        log_file.unlink(missing_ok=True)
        raise
    # File fsync alone does not guarantee the two directory entries survive a
    # crash. Never release systemd's terminal evidence before syncing both.
    directory_fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)


def persisted_job(db_path: Path, identity: str) -> dict | None:
    """Read one durable SQLite snapshot without modifying audit/job records."""
    if not ID_RE.fullmatch(identity):
        raise UnsafeJob("unsafe job identity in retention recheck")
    if db_path.is_symlink() or not db_path.is_file():
        raise UnsafeJob("SQLite state database must be an existing non-symlink")
    with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=3) as db:
        db.execute("PRAGMA query_only=ON")
        db.row_factory = sqlite3.Row
        row = db.execute("""
            SELECT job_id, tool, resource, unit_name, state, exit_code, updated_at
            FROM jobs WHERE job_id=?
        """, (identity,)).fetchone()
        return dict(row) if row else None


def candidates(db_path: Path, min_age_days: int, limit: int) -> list[dict]:
    if db_path.is_symlink() or not db_path.is_file():
        raise UnsafeJob("SQLite state database must be an existing non-symlink")
    cutoff_ns = time.time_ns() - min_age_days * 86400 * 1_000_000_000
    uri = f"file:{db_path}?mode=ro"
    with sqlite3.connect(uri, uri=True, timeout=3) as db:
        db.execute("PRAGMA query_only=ON")
        db.row_factory = sqlite3.Row
        rows = db.execute("""
            SELECT job_id, tool, resource, unit_name, state, exit_code, updated_at
            FROM jobs
            WHERE tool IN ('compose.pull', 'compose.up', 'compose.down')
              AND state IN ('done','failed') AND updated_at <= ?
            ORDER BY updated_at LIMIT ?
        """, (cutoff_ns, limit)).fetchall()
        return [dict(r) for r in rows]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--state-db", type=Path, default=STATE)
    ap.add_argument("--archive-dir", type=Path, default=ARCHIVE)
    ap.add_argument("--retention-days", type=int, default=7)
    ap.add_argument("--limit", type=int, default=25)
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--ack-operator-cleanup", action="store_true")
    ap.add_argument("--expected-hostname", default="", help="required on --apply; exact short hostname of the authorized Linux host")
    args = ap.parse_args()
    if args.retention_days < 1 or args.limit < 1 or args.limit > 100:
        ap.error("retention-days >= 1, limit between 1 and 100")
    if args.apply and (os.geteuid() != 0 or not args.ack_operator_cleanup):
        ap.error("apply requires root console and --ack-operator-cleanup")
    if args.apply:
        actual_hostname = os.uname().nodename.split(".")[0]
        if not args.expected_hostname or args.expected_hostname != actual_hostname:
            ap.error("--apply requires --expected-hostname exactly matching this host")
    try:
        rows = candidates(args.state_db, args.retention_days, args.limit)
        examined = eligible = cleaned = 0
        for job in rows:
            examined += 1
            unit = job["unit_name"]
            if not ID_RE.fullmatch(job["job_id"]) or unit != job["job_id"] + ".service":
                print("SKIP: malformed unit identity")
                continue
            current = unit_status(unit)
            if not unit_is_owned_and_finished(job, current):
                print(f"SKIP: unverified finished Compose unit {job['job_id']}")
                continue
            eligible += 1
            if not args.apply:
                print(f"ELIGIBLE: {job['job_id']} ({job['tool']}, {job['state']})")
                continue
            protected_archive(args.archive_dir)
            # The DB is the durable source of truth for terminal outcome.
            # A stale candidate or an uncertain dispatch must never be pruned.
            if persisted_job(args.state_db, job["job_id"]) != job:
                print("SKIP: persisted job state changed before archive")
                continue
            checked = unit_status(unit)
            if checked != current or not unit_is_owned_and_finished(job, checked):
                print("SKIP: systemd state changed while assessing")
                continue
            archive_job(args.archive_dir, job, checked)
            # Archive may take time; revalidate BOTH durable SQLite and
            # transient unit after the fsynced evidence, before any mutation.
            # Never auto-delete an archive on mismatch: operator reconciles.
            if persisted_job(args.state_db, job["job_id"]) != job:
                print("SKIP: persisted job changed after archive; manual reconciliation required")
                continue
            last = unit_status(unit)
            if last != checked or not unit_is_owned_and_finished(job, last):
                print("SKIP: unit changed after archive; manual reconciliation required")
                continue
            # NEVER apply systemctl actions on running jobs or arbitrary names.
            if last["ActiveState"] == "failed":
                run("systemctl", "reset-failed", unit)
            else:
                run("systemctl", "stop", unit)
            cleaned += 1
            print(f"COLLECTED: {job['job_id']} (evidence preserved as root-only files)")
        print(f"COMPOSE_RETENTION: examined={examined} eligible={eligible} collected={cleaned} dry_run={not args.apply}")
        return 0
    except (UnsafeJob, sqlite3.Error, OSError, subprocess.TimeoutExpired) as exc:
        print("COMPOSE_RETENTION_DENIED:", str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
