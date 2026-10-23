#!/usr/bin/env python3
"""Remove ONLY the Infinity host filecopy worker executable after safe shutdown.

The runtime controller, durable state, executor account, /opt and shared
systemd units are NOT deleted. Run only with explicit owner approval.
"""
from __future__ import annotations
import argparse
import hashlib
import importlib.util
import os
from pathlib import Path
import re
import stat
import sys

CONTROLLER=Path("/srv/portico-infinity")
SCRIPT=CONTROLLER/"scripts/infinity-host-worker-lifecycle.py"
LIFECYCLE=CONTROLLER/"scripts/infinity-lifecycle.py"
WORKER_DIR=Path("/usr/local/libexec/portico-infinity")
WORKER=WORKER_DIR/"portico-filecopy-worker"
UNIT="vps-agent-filecopy-*.service"
HASH=re.compile(r"[a-f0-9]{64}\Z")


class WorkerBlocked(RuntimeError):
    pass


def protected(path: Path, *, directory: bool) -> None:
    for obj in (*reversed(path.parents),path):
        data=obj.lstat()
        if (stat.S_ISLNK(data.st_mode) or data.st_uid!=0 or
                data.st_mode & 0o022):
            raise WorkerBlocked("worker/controller path is not root-protected")
        if not (stat.S_ISDIR(data.st_mode) if (obj!=path or directory)
                else stat.S_ISREG(data.st_mode)):
            raise WorkerBlocked("worker/controller path is not a trusted filesystem type")


def owner_context():
    if os.geteuid()!=0 or Path.cwd().resolve()!=CONTROLLER:
        raise WorkerBlocked("only root in /srv/portico-infinity may decommission worker")
    protected(CONTROLLER,directory=True)
    protected(CONTROLLER/"scripts",directory=True)
    protected(SCRIPT,directory=False)
    protected(LIFECYCLE,directory=False)
    spec=importlib.util.spec_from_file_location("infinity_lifecycle",LIFECYCLE)
    if spec is None or spec.loader is None:
        raise WorkerBlocked("trusted lifecycle implementation unavailable")
    module=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.verify_operator()
    return module


def binary() -> dict | None:
    if not WORKER_DIR.exists() and not WORKER_DIR.is_symlink():
        return None
    protected(WORKER_DIR,directory=True)
    if not WORKER.exists() and not WORKER.is_symlink():
        return None
    protected(WORKER,directory=False)
    fd=os.open(WORKER,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC)
    try:
        meta=os.fstat(fd)
        if (not stat.S_ISREG(meta.st_mode) or meta.st_uid!=0 or
                meta.st_nlink!=1 or meta.st_mode & 0o022 or meta.st_size<=0):
            raise WorkerBlocked("host binary is not a trusted exclusive regular file")
        h=hashlib.sha256()
        while True:
            chunk=os.read(fd,1<<20)
            if not chunk:break
            h.update(chunk)
        if os.fstat(fd).st_size!=meta.st_size:
            raise WorkerBlocked("host binary changed during verification")
        return {"sha256":h.hexdigest(),"size":meta.st_size,
                "device":meta.st_dev,"inode":meta.st_ino}
    finally:
        os.close(fd)


def ensure_quiescent(life, command=None) -> None:
    command=command or life.run
    life.ensure_no_active_workers(command)
    life.ensure_no_unreconciled_filecopy_checkpoints()
    # A retained transient *inactive* unit can still reference the binary.
    result=command(["systemctl","list-units","--all","--type=service",
                    "--no-legend","--no-pager","--full",UNIT])
    if "vps-agent-filecopy-" in result:
        raise WorkerBlocked("retained transient systemd worker unit requires explicit reconciliation")
    files=command(["systemctl","list-unit-files","--no-legend","--no-pager",UNIT])
    if "vps-agent-filecopy-" in files:
        raise WorkerBlocked("persistent systemd worker service file requires explicit reconciliation")


def ensure_no_running_binary(meta: dict, proc: Path=Path("/proc")) -> None:
    if not proc.is_dir():
        raise WorkerBlocked("Linux process inventory unavailable")
    for item in proc.iterdir():
        if not item.name.isdecimal():continue
        try:
            running=(item/"exe").stat()
        except (FileNotFoundError,ProcessLookupError):
            continue
        except (PermissionError,OSError) as exc:
            raise WorkerBlocked("cannot safely inspect all host processes") from exc
        if (running.st_dev==meta["device"] and running.st_ino==meta["inode"]):
            raise WorkerBlocked("a host process is still running the Infinity worker")


def remove(life,expected_sha: str,confirmation: str,command=None) -> bool:
    command=command or life.run
    ensure_quiescent(life,command)
    before=binary()
    if before is None:
        return False  # Already removed; no control-plane mutations.
    if (not HASH.fullmatch(expected_sha) or before["sha256"]!=expected_sha or
            confirmation!="REMOVE-INFINITY-HOST-WORKER-"+expected_sha[:20]):
        raise WorkerBlocked("explicit verified SHA256-bound owner confirmation required")
    ensure_no_running_binary(before)
    ensure_quiescent(life,command)
    again=binary()
    if again!=before:
        raise WorkerBlocked("worker changed between review and removal")
    # A root-owned non-writable ancestor prevents untrusted pathname swaps.
    os.unlink(WORKER)
    if WORKER.is_symlink() or WORKER.exists():
        raise WorkerBlocked("worker pathname still exists after exact unlink")
    try:
        WORKER_DIR.rmdir()  # only if empty; preserve unrelated files.
    except OSError:
        pass
    return True


def main():
    parser=argparse.ArgumentParser(description="Owner-only Infinity host worker decommission")
    group=parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--inspect",action="store_true")
    group.add_argument("--remove-worker",action="store_true")
    parser.add_argument("--expected-sha256",default="")
    parser.add_argument("--confirm",default="")
    args=parser.parse_args()
    try:
        life=owner_context()
        ensure_quiescent(life)
        current=binary()
        if args.inspect:
            if current is None:
                print("INFINITY HOST WORKER: absent; no removal needed")
            else:
                print("INFINITY HOST WORKER ROOT-OWNED: size=",current["size"],
                      "sha256=",current["sha256"])
                print("PRESERVED: controller, state, executor user, /opt, VPN, proxy and other systemd services")
            return 0
        if current is None:
            print("INFINITY HOST WORKER: already absent")
            return 0
        if not remove(life,args.expected_sha256,args.confirm):
            raise WorkerBlocked("unexpected absent worker after preflight")
        print("INFINITY HOST WORKER REMOVED: exact root-owned binary only")
        print("PRESERVED: controller and audit, state, account, /opt, foreign services and networks")
        return 0
    except (WorkerBlocked,OSError,ValueError) as exc:
        print("INFINITY HOST WORKER DECOMMISSION BLOCKED: "+str(exc),file=sys.stderr)
        return 2


if __name__=="__main__":
    raise SystemExit(main())
