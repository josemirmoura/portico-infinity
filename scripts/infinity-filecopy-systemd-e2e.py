#!/usr/bin/env python3
"""Actual systemd + standalone filecopy worker acceptance, disposable runner ONLY.

Never run on SRV-IA/VPS. This deliberately creates /srv/portico-infinity and
a scratch /opt directory, and installs a synthetic-only worker binary.
It does NOT enable file.copy.start in a live Broker.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time

CONTROLLER = Path("/srv/portico-infinity")
WORKER = Path("/usr/local/libexec/portico-infinity/portico-filecopy-worker")
STATE = CONTROLLER / "state/filecopy-jobs"
HEX = re.compile(r"[0-9a-f]{32}\Z")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError("E2E BLOCKED/FAILED: " + message)


def cmd(*args: str, expect_success: bool = True, timeout: int = 45) -> subprocess.CompletedProcess:
    proc = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    if (proc.returncode == 0) != expect_success:
        raise RuntimeError(
            "E2E command returned unexpected exit status: " +
            " ".join(args[:2]) + " (" + str(proc.returncode) + ")"
        )
    return proc


def job_id(suffix: str) -> str:
    run = os.environ.get("GITHUB_RUN_ID", "")
    require(run.isdecimal() and len(run) <= 24, "missing validated GitHub run ID")
    digest = hashlib.sha256((run + ":" + suffix).encode()).hexdigest()[:32]
    require(bool(HEX.fullmatch(digest)), "bad job digest")
    return "vps-agent-filecopy-" + digest


def write_manifest(job: str, root: Path, source: Path, destination: Path, max_bytes: int, sha: str = "") -> None:
    manifest = {
        "job_id": job,
        "subject": "disposable-systemd-synthetic-operator",
        "source": str(source),
        "destination": str(destination),
        "scope_root": str(root),
        "max_bytes": max_bytes,
        "expected_sha256": sha,
    }
    target = STATE / (job + ".request.json")
    with target.open("x", encoding="utf-8") as fp:
        os.chmod(target, 0o600)
        json.dump(manifest, fp)
        fp.flush()
        os.fsync(fp.fileno())


def progress(job: str) -> dict | None:
    target = STATE / (job + ".progress.json")
    if not target.is_file():
        return None
    with target.open(encoding="utf-8") as fp:
        result = json.load(fp)
    require(result.get("job_id") == job, "foreign progress job identifier")
    return result


def dispatch(job: str, root: Path) -> None:
    # Mirrors the independent broker's typed systemd-run profile, not a shell
    # supplied by MCP input. No general sudo command or Docker access is given.
    cmd(
        "systemd-run",
        "--unit=" + job, "--no-block", "--quiet",
        "--description=Portico disposable typed binary filecopy",
        "--property=Type=oneshot", "--property=RemainAfterExit=yes",
        "--property=NoNewPrivileges=yes", "--property=UMask=0077",
        "--property=TimeoutStartSec=300", "--property=MemoryMax=1073741824",
        "--property=TasksMax=64", "--property=ProtectSystem=strict",
        "--property=ReadWritePaths=" + str(root) + " " + str(STATE),
        "--property=ProtectHome=yes", "--property=PrivateDevices=yes",
        "--property=PrivateNetwork=yes",
        "--", str(WORKER), job,
    )


def wait_phase(job: str, terminal: set[str], timeout: int) -> dict:
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            last = progress(job)
        except (json.JSONDecodeError, OSError):
            pass  # Atomic renames should prevent partial status reads.
        if last is not None and last.get("phase") in terminal:
            return last
        time.sleep(0.05)
    raise RuntimeError("timed out waiting for worker checkpoint: " + str(last))


def file_hash(path: Path) -> str:
    sha = hashlib.sha256()
    with path.open("rb") as fp:
        while True:
            block = fp.read(1 << 20)
            if not block:
                break
            sha.update(block)
    return sha.hexdigest()


def check_systemd_properties(job: str, expected: str) -> None:
    got = cmd("systemctl", "show", job + ".service",
              "--no-pager", "--property=LoadState,ActiveState,SubState,Result").stdout
    values = dict(line.strip().split("=", 1) for line in got.splitlines() if "=" in line)
    require(values.get("LoadState") == "loaded", "systemd unit was not retained")
    require(values.get("Result") == expected, "unexpected systemd result: " + str(values))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--build-binary", type=Path, required=True)
    args = parser.parse_args()
    require(os.geteuid() == 0, "requires root in isolated GitHub-hosted runner")
    require(os.environ.get("CI") == "true" and
            os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted",
            "only disposable GitHub-hosted CI allowed")
    require(os.environ.get("GITHUB_EVENT_NAME") == "pull_request",
            "only gated pull_request can execute this destructive lab fixture")
    require(CONTROLLER.is_symlink() is False and not CONTROLLER.exists(),
            "do not modify an existing Infinity controller")
    require(not WORKER.exists() and not WORKER.is_symlink(),
            "do not overwrite an existing worker")
    require(args.build_binary.is_file() and not args.build_binary.is_symlink(),
            "verified built test binary required")
    run = os.environ["GITHUB_RUN_ID"]
    root = Path("/opt/infinity-systemd-e2e-" + run)
    require(not root.exists() and not root.is_symlink(), "scratch root already exists")
    require(not root.parent.is_symlink(), "scratch parent cannot be symlink")
    require(shutil.disk_usage(root.parent).free >= 4 * (1 << 30),
            "insufficient scratch disk; abort without side effects")
    units = [job_id("complete"), job_id("cancel"), job_id("unsafe")]
    for item in units:
        state = cmd("systemctl", "show", item + ".service",
                    "--no-pager", "--property=LoadState").stdout
        require("LoadState=not-found" in state, "job name collision before E2E")

    print("ISOLATED FILECOPY SYSTEMD E2E: preflight passed", flush=True)
    try:
        root.mkdir(mode=0o700)
        (root / "models").mkdir(mode=0o700)
        STATE.mkdir(mode=0o700, parents=True)
        WORKER.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        shutil.copyfile(args.build_binary, WORKER)
        WORKER.chmod(0o755)
        require(WORKER.stat().st_uid == 0, "installed worker must be root-owned")

        source = root / "models" / "source.gguf"
        dest = root / "models" / "copy.gguf"
        # A sparse 128 MiB synthetic binary is used to exercise actual systemd
        # isolation, not the full multigig engine test already in PR #28.
        with source.open("xb") as fp:
            fp.truncate(128 << 20)
        expected = file_hash(source)
        write_manifest(units[0], root, source, dest, 256 << 20, expected)
        dispatch(units[0], root)
        done = wait_phase(units[0], {"done", "failed", "cancelled", "reconcile_required"}, 90)
        require(done["phase"] == "done", "systemd copy did not finish: " + str(done))
        check_systemd_properties(units[0], "success")
        require(dest.exists() and dest.stat().st_size == 128 << 20, "destination missing/truncated")
        require(file_hash(dest) == expected == done.get("sha256"), "destination SHA mismatch")
        require(done.get("bytes_done") == 128 << 20, "checkpoint bytes missing")
        cmd(str(WORKER), units[0], expect_success=False)
        require(file_hash(dest) == expected, "replay modified published destination")
        print("REAL SYSTEMD-WORKER COPY + SHA + PROGRESS + REPLAY DENIAL: PASS", flush=True)

        # Deliberate, privileged malicious manifest stays outside the legitimate
        # permissions contract and is rejected by the worker before any write.
        unsafe = root / "models" / ".env"
        write_manifest(units[2], root, source, unsafe, 256 << 20)
        cmd(str(WORKER), units[2], expect_success=False)
        require(not unsafe.exists(), "sensitive path was written")
        print("MALICIOUS MANIFEST REJECTED: PASS", flush=True)

        # Prove cancellation in a *separate* typed systemd unit. Sparse input
        # avoids storing gigabytes in the runner; stop after durable progress.
        big = root / "models" / "large-source.gguf"
        cancelled_dest = root / "models" / "should-not-publish.gguf"
        with big.open("xb") as fp:
            fp.truncate(8 << 30)
        write_manifest(units[1], root, big, cancelled_dest, 9 << 30)
        dispatch(units[1], root)
        deadline = time.monotonic() + 60
        checkpoint = 0
        while time.monotonic() < deadline:
            p = progress(units[1])
            if p:
                checkpoint = int(p.get("bytes_done") or 0)
                if checkpoint >= 64 << 20:
                    break
                if p["phase"] in ("done", "failed", "cancelled"):
                    raise RuntimeError("worker became terminal before cancellation: " + str(p))
            time.sleep(0.025)
        require(checkpoint >= 64 << 20, "worker did not report durable progress before stop")
        cmd("systemctl", "stop", units[1] + ".service", timeout=45)
        cancelled = wait_phase(units[1], {"cancelled", "failed", "done", "reconcile_required"}, 45)
        require(cancelled["phase"] == "cancelled",
                "SIGTERM did not persist cancelled phase: " + str(cancelled))
        require(not cancelled_dest.exists(), "cancelled job published final file")
        cmd(str(WORKER), units[1], expect_success=False)
        require(not cancelled_dest.exists(), "worker replay published cancelled job")
        print("SYSTEMD SIGTERM + DURABLE CANCEL + NO REPLAY: PASS", flush=True)
        print("SYSTEMD FILECOPY DISPOSABLE E2E: PASS", flush=True)
        return 0
    finally:
        # May only clean what this very process created in ephemeral CI.
        for unit in units:
            subprocess.run(["systemctl", "stop", unit + ".service"],
                           capture_output=True, timeout=30, check=False)
            subprocess.run(["systemctl", "reset-failed", unit + ".service"],
                           capture_output=True, timeout=15, check=False)
        if root.exists():
            shutil.rmtree(root)
        if CONTROLLER.exists():
            shutil.rmtree(CONTROLLER)
        if WORKER.exists():
            WORKER.unlink()
        try:
            WORKER.parent.rmdir()
        except OSError:
            pass


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, subprocess.TimeoutExpired) as exc:
        print("SYSTEMD FILECOPY E2E FAILED: " + str(exc), file=sys.stderr)
        raise SystemExit(1)
