#!/usr/bin/env python3
"""Real systemd retention E2E using ONLY synthetic resources on GitHub-hosted CI.

Runs as root inside a disposable GitHub runner. It never accesses host projects,
production state, Nara data, operational service units, or network credentials.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import stat
import subprocess
import sys
import time


def call(*argv: str, check: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(argv, text=True, capture_output=True, check=check, timeout=30)


def unit_props(unit: str) -> dict[str, str]:
    p = call("systemctl", "show", unit, "--no-pager",
             "--property=LoadState,ActiveState,SubState,Result,ExecMainStatus,Id")
    return dict(line.split("=", 1) for line in p.stdout.splitlines() if "=" in line)


def require_disposable() -> str:
    environment = os.environ
    run_id = environment.get("GITHUB_RUN_ID", "")
    if (os.geteuid() != 0 or
        environment.get("RUNNER_ENVIRONMENT") != "github-hosted" or
        environment.get("GITHUB_EVENT_NAME") != "pull_request" or
        environment.get("GITHUB_REPOSITORY") != "josemirmoura/portico-infinity" or
        not run_id.isdecimal() or len(run_id) > 24 or
        Path("/srv/portico-infinity").exists()):
        raise RuntimeError("refusing to run real systemd fixture outside GitHub-hosted disposable PR")
    return run_id


def main() -> None:
    run_id = require_disposable()
    base = Path("/data")
    if base.is_symlink() or (base.exists() and not base.is_dir()):
        raise RuntimeError("unsafe /data fixture ancestor")
    if not base.exists():
        base.mkdir(mode=0o755)
    if base.stat().st_uid != 0 or base.stat().st_mode & 0o022:
        raise RuntimeError("fixture ancestor must be root-owned and not writable by others")
    root = base / ("portico-retention-ci-" + run_id)
    if root.exists() or root.is_symlink():
        raise RuntimeError("CI fixture must never reuse an existing directory")
    root.mkdir(mode=0o700)
    (root / "archive").mkdir(mode=0o700)

    def name(kind: str) -> str:
        digest = hashlib.sha256((kind + ":" + run_id).encode()).hexdigest()[:32]
        return "vps-agent-compose-" + digest

    good, foreign = name("good"), name("foreign")
    units = [good + ".service", foreign + ".service"]
    cli = Path(__file__).resolve().parents[2] / "scripts" / "collect-infinity-compose-units.py"
    args = ["python3", str(cli), "--state-db", str(root / "state.db"),
            "--archive-dir", str(root / "archive"), "--retention-days", "1",
            "--limit", "10"]

    try:
        for identity, desc in (
            (good, "Portico authorized Docker Compose task"),
            (foreign, "External unrelated unit, never collect"),
        ):
            call("systemd-run", "--unit=" + identity, "--no-block", "--quiet",
                 "--description=" + desc, "--working-directory=" + str(root),
                 "--property=Type=oneshot", "--property=RemainAfterExit=yes",
                 "--property=NoNewPrivileges=yes", "--", "/usr/bin/true")

        for _ in range(60):
            first, other = unit_props(units[0]), unit_props(units[1])
            if (first.get("ActiveState") == "active" and
                first.get("SubState") == "exited" and
                other.get("ActiveState") == "active" and
                other.get("SubState") == "exited"):
                break
            time.sleep(1)
        else:
            raise RuntimeError("real synthetic systemd services did not reach active/exited")

        con = sqlite3.connect(root / "state.db")
        con.execute("""CREATE TABLE jobs (
            job_id TEXT PRIMARY KEY, tool TEXT, resource TEXT, unit_name TEXT,
            state TEXT, exit_code INTEGER, updated_at INTEGER
        )""")
        older = time.time_ns() - 4 * 86400 * 1_000_000_000
        for identity, state, code in (
            (good, "done", 0),
            (foreign, "done", 0),
            (name("uncertain"), "running", None),
        ):
            con.execute("INSERT INTO jobs VALUES (?,?,?,?,?,?,?)",
                        (identity, "compose.up", str(root), identity + ".service",
                         state, code, older))
        con.commit()
        con.close()

        dry = call(*args)
        assert "ELIGIBLE: " + good in dry.stdout, dry.stdout
        assert "SKIP: unverified finished Compose unit " + foreign in dry.stdout, dry.stdout
        assert not tuple((root / "archive").iterdir()), "dry-run modified filesystem"

        wrong = call(*args, "--apply", "--ack-operator-cleanup",
                     "--expected-hostname", "definitely-not-this-runner", check=False)
        assert wrong.returncode != 0, "wrong host was allowed to mutate"
        assert not tuple((root / "archive").iterdir()), "wrong host created archive"

        host = os.uname().nodename.split(".")[0]
        done = call(*args, "--apply", "--ack-operator-cleanup",
                    "--expected-hostname", host)
        assert "COLLECTED: " + good in done.stdout, done.stdout
        assert "COLLECTED: " + foreign not in done.stdout, done.stdout

        assert unit_props(units[0]).get("ActiveState") != "active", "completed unit not released"
        assert unit_props(units[1]).get("ActiveState") == "active", "foreign unit touched"
        metadata = root / "archive" / (good + ".json")
        journal = root / "archive" / (good + ".log")
        assert metadata.is_file() and journal.is_file()
        assert stat.S_IMODE(metadata.stat().st_mode) == 0o600
        assert stat.S_IMODE(journal.stat().st_mode) == 0o600
        assert len(journal.read_bytes()) <= 65536
        archived = json.loads(metadata.read_text())
        assert archived["job_id"] == good and archived["state"] == "done"
        assert archived["exit_code"] == 0
        assert archived["log_sha256"] == hashlib.sha256(journal.read_bytes()).hexdigest()
        assert not (root / "archive" / (foreign + ".json")).exists()
        print("INFINITY_COMPOSE_RETENTION_REAL_SYSTEMD_AND_PRIVATE_EVIDENCE: PASS")

        repeat = call(*args, "--apply", "--ack-operator-cleanup",
                      "--expected-hostname", host)
        assert "collected=0" in repeat.stdout
        assert unit_props(units[1]).get("ActiveState") == "active"
        con = sqlite3.connect(root / "state.db")
        assert con.execute("SELECT state,exit_code FROM jobs WHERE job_id=?",
                           (good,)).fetchone() == ("done", 0)
        assert con.execute("SELECT state FROM jobs WHERE job_id=?",
                           (foreign,)).fetchone() == ("done",)
        con.close()
        print("INFINITY_COMPOSE_RETENTION_IDEMPOTENCY_FOREIGN_AND_SQLITE_PRESERVED: PASS")
    finally:
        # Even failed fixture runs only stop the two hashed, CI-owned units.
        require_disposable()
        for unit in units:
            call("systemctl", "stop", unit, check=False)
            call("systemctl", "reset-failed", unit, check=False)
        assert root.name == "portico-retention-ci-" + run_id
        shutil.rmtree(root)


if __name__ == "__main__":
    main()
