#!/usr/bin/env python3
"""Isolated owner-only Infinity LOCAL uninstall, guarded by a verified backup.

Never removes the work ceiling /opt, host worker, user account, WireGuard,
public DNS/TLS, VPS bridge, Community or Cloud. External dismantling is a
separate operator-reviewed process. Never run on production without consent.
"""
from __future__ import annotations

import argparse
import datetime
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys

CONTROLLER = Path("/srv/portico-infinity")
BACKUP_SCRIPT = CONTROLLER / "scripts/infinity-uninstall-backup.py"
BACKUP_ROOT = Path("/var/backups/portico-infinity")
HOST_WORKER = Path("/usr/local/libexec/portico-infinity/portico-filecopy-worker")


class LocalRemovalBlocked(RuntimeError):
    pass


def load_trusted_backup():
    if os.geteuid() != 0 or Path.cwd().resolve() != CONTROLLER:
        raise LocalRemovalBlocked("owner must run as root from Infinity controller")
    for path in (Path("/"), Path("/srv"), CONTROLLER, CONTROLLER / "scripts",
                 CONTROLLER / "scripts/infinity-lifecycle.py",
                 BACKUP_SCRIPT,
                 CONTROLLER / "scripts/infinity-uninstall-local.py"):
        meta = path.lstat()
        if (stat.S_ISLNK(meta.st_mode) or meta.st_uid != 0 or meta.st_mode & 0o022
                or not (stat.S_ISDIR(meta.st_mode) or stat.S_ISREG(meta.st_mode))):
            raise LocalRemovalBlocked("untrusted local-uninstall program or ancestor")
    spec = importlib.util.spec_from_file_location("infinity_uninstall_backup", BACKUP_SCRIPT)
    if spec is None or spec.loader is None:
        raise LocalRemovalBlocked("verified backup module unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.require_context()  # verified root-only tree, zero uncertain workers
    return module


def inspect_mounts(controller: Path = CONTROLLER, mountinfo: Path = Path("/proc/self/mountinfo")) -> None:
    # Mounts can map FOREIGN user data inside the controller even when it is
    # root-owned. Neither shutil.rmtree nor a Docker volume label proves owner.
    with mountinfo.open(encoding="utf-8") as handle:
        for row in handle:
            columns = row.split()
            if len(columns) < 5:
                raise LocalRemovalBlocked("cannot parse kernel mount inventory")
            mountpoint = re.sub(r"\\([0-7]{3})",
                lambda m: chr(int(m.group(1), 8)), columns[4])
            path = Path(mountpoint)
            if path == controller or controller in path.parents:
                raise LocalRemovalBlocked("mounted filesystem beneath Infinity controller")


def require_host_worker_retired(worker: Path = HOST_WORKER) -> None:
    """Retire the privileged host executable before erasing its controller."""
    if worker.exists() or worker.is_symlink():
        raise LocalRemovalBlocked("privileged host filecopy worker remains installed: decommission it separately before local uninstall")


def prepare(backup, label: str, command=None):
    backup.require_context()
    require_host_worker_retired()
    journal = backup.verify_backup(label)
    current = backup.validated_snapshot(command)
    if journal.get("inventory_sha256") != backup.snapshot_signature(current):
        raise LocalRemovalBlocked("post-backup Docker inventory drift: do not remove anything")
    if journal.get("inventory") != current:
        raise LocalRemovalBlocked("backup does not match exact current owned resources")
    inspect_mounts()
    if not shutil.rmtree.avoids_symlink_attacks:
        raise LocalRemovalBlocked("recursive controller removal lacks fd-based symlink protection")
    return journal, current


def confirmation_strings(journal: dict) -> tuple[str, str]:
    sha = journal.get("sha256")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-f]{64}", sha):
        raise LocalRemovalBlocked("verified backup digest absent")
    return "REMOVE-LOCAL-INFINITY-" + sha[:20], "RESTORE-TESTED-" + sha[:20]


def operator_plan(journal: dict, snapshot: dict) -> dict:
    confirm, restored = confirmation_strings(journal)
    return {
        "backup": journal["archive"], "backup_sha256": journal["sha256"],
        "controller": snapshot["controller"],
        "containers": len(snapshot["containers"]),
        "networks": snapshot["networks"],
        "volumes": [v["name"] for v in snapshot["volumes"]],
        "external_assets_preserved": snapshot["external_gates"],
        "workspaces_preserved": "/opt and every configured physical scope",
        "requires_explicit_confirm": confirm,
        "requires_independent_restore_attestation": restored,
        "automatic_deletion": False,
    }


def write_intent(root: Path, label: str, plan: dict):
    destination = root / (f"infinity-uninstall-{label}.local-intent.json")
    if destination.exists() or destination.is_symlink():
        raise LocalRemovalBlocked("local uninstall intent already exists; requires manual reconciliation")
    fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump({"status":"removal-started", "started_at_utc":datetime.datetime.now(
            datetime.timezone.utc).isoformat(),"plan":plan},handle,indent=2)
        handle.write("\n")
        handle.flush()
        os.fsync(handle.fileno())
    dirfd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(dirfd)
    finally:
        os.close(dirfd)
    return destination


def finish_intent(file: Path, plan: dict):
    # The prior root-owned 0600 journal is retained OUTSIDE the deleted
    # controller as evidence, without secret contents.
    with file.open("w", encoding="utf-8") as handle:
        json.dump({"status":"local-resources-removed",
                   "finished_at_utc":datetime.datetime.now(datetime.timezone.utc).isoformat(),
                   "plan":plan,
                   "still_requires_external_operator_cleanup":True},handle,indent=2)
        handle.write("\n")
        handle.flush()
        os.fsync(handle.fileno())


def verify_volume_before_remove(backup, item: dict, command) -> None:
    name = item["name"]
    if not backup.ALLOWED_VOLUME.fullmatch(name):
        raise LocalRemovalBlocked("invalid owned volume identity")
    record = backup.life._single_json(command(["docker","volume","inspect",name]))
    if (record.get("Name") != name or
            (record.get("Labels") or {}).get("com.docker.compose.project") != backup.life.PROJECT or
            record.get("Mountpoint") != item["mountpoint"]):
        raise LocalRemovalBlocked("Docker volume changed identity before removal")
    users = backup.life._ids(command(["docker","ps","-aq","--no-trunc",
                        "--filter","volume="+name,"--format","{{.ID}}"]))
    if users:
        raise LocalRemovalBlocked("volume still mounted by a container; reject removal")


def local_remove(backup, label: str, journal: dict, snapshot: dict, *,
                 command=None) -> dict:
    command = command or backup.life.run
    require_host_worker_retired()
    if backup.validated_snapshot(command) != snapshot:
        raise LocalRemovalBlocked("inventory changed between confirmation and deletion")
    backup.life.ensure_no_active_workers(command)
    backup.life.ensure_no_unreconciled_filecopy_checkpoints()
    inspect_mounts()
    plan = operator_plan(journal, snapshot)
    intent = write_intent(BACKUP_ROOT, label, plan)
    # Phase A: reuse the already CI-proven typed lifecycle, NOT docker compose
    # down -v (which could drop identity before backup certification).
    backup.life.change_runtime("uninstall", command)
    remaining = backup.life._ids(command(["docker","ps","-aq","--no-trunc",
                    "--filter","label=com.docker.compose.project="+backup.life.PROJECT,
                    "--format","{{.ID}}"]))
    if remaining:
        raise LocalRemovalBlocked("Compose containers survived removal")
    # Phase B: independent, exact label+mountpoint checks before each Docker
    # volume deletion. Docker refuses removal while ANY consumer holds it.
    for volume in snapshot["volumes"]:
        verify_volume_before_remove(backup, volume, command)
        command(["docker","volume","rm",volume["name"]])
    remaining_volumes = [v.strip() for v in command(["docker","volume","ls",
        "--filter","label=com.docker.compose.project="+backup.life.PROJECT,
        "--format","{{.Name}}"]).splitlines() if v.strip()]
    if remaining_volumes:
        raise LocalRemovalBlocked("owned Docker volumes remain after deletion")
    # Phase C: only the fixed, verified protected controller directory.
    # Do not cross mounts, do not follow symlinks. /opt is never traversed.
    inspect_mounts()
    backup.protected(CONTROLLER,directory=True)
    os.chdir("/")
    shutil.rmtree(CONTROLLER)
    if CONTROLLER.exists() or CONTROLLER.is_symlink():
        raise LocalRemovalBlocked("controller could not be conclusively removed")
    finish_intent(intent, plan)
    return plan


def main():
    parser = argparse.ArgumentParser(description="Infinity local resources removal AFTER verified backup and independent restore")
    parser.add_argument("--backup-label", required=True)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--dry-run",action="store_true")
    group.add_argument("--remove-local",action="store_true")
    parser.add_argument("--confirm", default="")
    parser.add_argument("--restore-attested", default="")
    args=parser.parse_args()
    try:
        backup=load_trusted_backup()
        journal,snapshot=prepare(backup,args.backup_label)
        plan=operator_plan(journal,snapshot)
        if args.dry_run:
            print(json.dumps(plan,indent=2))
            return 0
        confirm,restored=confirmation_strings(journal)
        if args.confirm!=confirm or args.restore_attested!=restored:
            raise LocalRemovalBlocked("explicit digest-bound authorization and independently tested restore required")
        local_remove(backup,args.backup_label,journal,snapshot)
        print("INFINITY LOCAL RESOURCES REMOVED: typed Docker resources, private volumes and controller only")
        print("PRESERVED: verified external backup, /opt, bridge VPS, WG/firewall, DNS/TLS, host worker, Community/Cloud")
        return 0
    except (LocalRemovalBlocked,OSError,ValueError,RuntimeError) as exc:
        print("INFINITY LOCAL UNINSTALL BLOCKED/INCOMPLETE: "+str(exc),file=sys.stderr)
        return 2


if __name__=="__main__":
    raise SystemExit(main())
