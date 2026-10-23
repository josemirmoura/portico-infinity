#!/usr/bin/env python3
"""Root-only, read-only inventory and verified backup gate before FULL uninstall.

This script NEVER deletes runtime, identity, host services, volumes or workspaces.
External VPS edge, WireGuard, TLS/DNS and operator-owned host worker are separate
explicit uninstall gates, not resources implicitly owned by local Docker Compose.
"""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import tarfile
import tempfile

CONTROLLER = Path("/srv/portico-infinity")
BACKUP_ROOT = Path("/var/backups/portico-infinity")
ALLOWED_VOLUME = re.compile(r"portico-infinity_[A-Za-z0-9_-]+\Z")
ARCHIVE_PATTERN = re.compile(r"infinity-uninstall-[0-9A-Za-z_-]{8,64}[.]tar\Z")
CONFIRM = "BACKUP-INFINITY-BEFORE-UNINSTALL"

spec = importlib.util.spec_from_file_location("infinity_lifecycle",
    Path(__file__).resolve().parent / "infinity-lifecycle.py")
assert spec and spec.loader
life = importlib.util.module_from_spec(spec)
spec.loader.exec_module(life)


class BackupBlocked(RuntimeError):
    pass


def protected(path: Path, *, directory: bool, mode: int | None = None) -> os.stat_result:
    """Reject symlinks in every existing ancestor; ensure root ownership."""
    if not path.is_absolute() or ".." in path.parts:
        raise BackupBlocked("noncanonical protected path")
    for part in [Path("/"), *reversed(list(path.parents)[:-1]), path]:
        try:
            metadata = part.lstat()
        except FileNotFoundError:
            raise BackupBlocked("missing protected path")
        if stat.S_ISLNK(metadata.st_mode) or metadata.st_uid != 0:
            raise BackupBlocked("protected path has symlink/non-root ownership")
        if part != path and (not stat.S_ISDIR(metadata.st_mode) or metadata.st_mode & 0o022):
            raise BackupBlocked("untrusted protected ancestor directory")
        if part == path:
            good_type = stat.S_ISDIR if directory else stat.S_ISREG
            if not good_type(metadata.st_mode) or metadata.st_mode & 0o022:
                raise BackupBlocked("unsafe protected object")
            if mode is not None and metadata.st_mode & 0o777 != mode:
                raise BackupBlocked("private backup mode violation")
    return path.lstat()


def require_context() -> None:
    if os.geteuid() != 0 or Path.cwd().resolve() != CONTROLLER:
        raise BackupBlocked("operator must run as root in Infinity controller")
    life.verify_operator()
    protected(CONTROLLER, directory=True)
    protected(CONTROLLER / "scripts/infinity-uninstall-backup.py", directory=False)
    life.ensure_no_active_workers()
    life.ensure_no_unreconciled_filecopy_checkpoints()


def validated_snapshot(command=life.run, *, controller: Path = CONTROLLER) -> dict:
    containers, networks, volumes = life.inventory(command)
    if any(c["running"] for c in containers):
        raise BackupBlocked("stop Infinity containers using authorized reversible deactivation before backup")
    # The same named volume may be attached to another independent service.
    # Do not back up or later remove it until ownership is provable.
    result = []
    for name in sorted(volumes):
        if not ALLOWED_VOLUME.fullmatch(name):
            raise BackupBlocked("untrusted volume name")
        record = life._single_json(command(["docker", "volume", "inspect", name]))
        if (record.get("Name") != name or
                (record.get("Labels") or {}).get("com.docker.compose.project") != life.PROJECT):
            raise BackupBlocked("foreign identity volume")
        mount = record.get("Mountpoint")
        if not isinstance(mount, str):
            raise BackupBlocked("Docker volume does not expose local filesystem mountpoint")
        location = Path(mount)
        expected = Path("/var/lib/docker/volumes") / name / "_data"
        if location != expected:
            raise BackupBlocked("nonstandard/remote Docker volume: requires separate backup procedure")
        protected(location, directory=True)
        ids = life._ids(command(["docker", "ps", "-aq", "--no-trunc",
                                 "--filter", "volume=" + name, "--format", "{{.ID}}"]))
        if not set(ids).issubset({c["id"] for c in containers}):
            raise BackupBlocked("volume shared with non-Infinity container")
        result.append({"name": name, "mountpoint": mount})
    return {
        "schema": 1, "controller": str(controller),
        "volumes": result, "containers": sorted(c["id"] for c in containers),
        "networks": sorted(n["name"] for n in networks),
        "external_gates": [
            "VPS bridge and owner-specific /opt/portico-infinity-edge: external host manual inventory",
            "WireGuard wg-infinity and firewall rules: potentially shared; external operator review",
            "DNS/TLS/Traefik public entrypoint: provider-owned/shared; external operator review",
            "systemd worker binary and portico-infinity-exec user: independent host review",
        ],
    }


def snapshot_signature(snapshot: dict) -> str:
    return hashlib.sha256(json.dumps(snapshot, sort_keys=True,
        separators=(",", ":")).encode("utf-8")).hexdigest()


def safe_backup_name(token: str) -> str:
    if not re.fullmatch(r"[0-9A-Za-z_-]{8,64}", token):
        raise BackupBlocked("backup label must be 8-64 safe ASCII characters")
    return "infinity-uninstall-" + token + ".tar"


def archive_sources(snapshot: dict):
    # tarfile's default dereference=False preserves symlinks as inert entries.
    yield CONTROLLER, "controller"
    for volume in snapshot["volumes"]:
        yield Path(volume["mountpoint"]), "volumes/" + volume["name"]


def make_backup(snapshot: dict, label: str, *, root: Path = BACKUP_ROOT,
                command=life.run) -> dict:
    # Only the owner can create these data-at-rest *unencrypted* private
    # archives. NEVER attach, upload, print contents or copy to a public CI
    # artifact. Root:root 0700, files 0600, outside /opt and /srv.
    if root != BACKUP_ROOT:
        raise BackupBlocked("backup destination must be independent /var/backups/portico-infinity")
    protected(root, directory=True, mode=0o700)
    name = safe_backup_name(label)
    destination = root / name
    manifest = root / (name + ".json")
    if destination.exists() or destination.is_symlink() or manifest.exists() or manifest.is_symlink():
        raise BackupBlocked("backup already exists; cannot overwrite")
    staging = None
    try:
        fd, tmp = tempfile.mkstemp(prefix=".infinity-staging-", dir=root)
        staging = Path(tmp)
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as stream:
            with tarfile.open(fileobj=stream, mode="w", dereference=False) as archive:
                for source, arcname in archive_sources(snapshot):
                    protected(source, directory=True)
                    archive.add(source, arcname=arcname, recursive=True)
            stream.flush()
            os.fsync(stream.fileno())
        # The scan MUST still match the stopped private Docker resources after
        # the backup. Concurrent deletion/restart fails closed and invalidates it.
        if validated_snapshot(command) != snapshot:
            raise BackupBlocked("Infinity inventory changed during backup")
        digest = hashlib.file_digest(staging.open("rb"), "sha256").hexdigest()
        with tarfile.open(staging, "r") as archive:
            names = {member.name for member in archive.getmembers()}
            if "controller" not in names or any("volumes/" + v["name"] not in names for v in snapshot["volumes"]):
                raise BackupBlocked("backup incomplete")
            if any(member.name.startswith("/") or ".." in Path(member.name).parts
                   for member in archive.getmembers()):
                raise BackupBlocked("archive contains unsafe entry")
        os.rename(staging, destination)  # private backup root; destination did not exist
        staging = None
        document = {"schema": 1, "archive": name, "sha256": digest,
                    "bytes": destination.stat().st_size,
                    "inventory_sha256": snapshot_signature(snapshot),
                    "inventory": snapshot,
                    "restore_status": "NOT_TESTED: dry-run restore required before irreversible uninstall"}
        mfd = os.open(manifest, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(mfd, "w", encoding="utf-8") as out:
            json.dump(document, out, sort_keys=True, indent=2)
            out.write("\n")
            out.flush()
            os.fsync(out.fileno())
        directory_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
        return document
    finally:
        if staging is not None:
            staging.unlink(missing_ok=True)


def verify_backup(label: str, *, root: Path = BACKUP_ROOT) -> dict:
    protected(root, directory=True, mode=0o700)
    name = safe_backup_name(label)
    archive = root / name
    manifest = root / (name + ".json")
    protected(archive, directory=False, mode=0o600)
    protected(manifest, directory=False, mode=0o600)
    with manifest.open("rb") as file:
        doc = json.loads(file.read(65537))
    if doc.get("schema") != 1 or doc.get("archive") != name or not isinstance(doc.get("inventory"), dict):
        raise BackupBlocked("invalid backup manifest schema")
    if doc.get("inventory_sha256") != snapshot_signature(doc["inventory"]):
        raise BackupBlocked("backup inventory digest mismatch")
    if doc.get("bytes") != archive.stat().st_size:
        raise BackupBlocked("archive size mismatch")
    with archive.open("rb") as file:
        actual = hashlib.file_digest(file, "sha256").hexdigest()
    if actual != doc.get("sha256"):
        raise BackupBlocked("archive SHA256 mismatch")
    with tarfile.open(archive, "r") as tf:
        members = tf.getmembers()
        names = {m.name for m in members}
        if "controller" not in names or any(
                "volumes/" + v["name"] not in names for v in doc["inventory"]["volumes"]):
            raise BackupBlocked("backup missing controller or volume")
        if any(x.name.startswith("/") or ".." in Path(x.name).parts for x in members):
            raise BackupBlocked("unsafe archive path")
    return doc


def main() -> int:
    parser = argparse.ArgumentParser(description="Owner-only Infinity full-uninstall preparation; NO deletion")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--inspect", action="store_true")
    group.add_argument("--backup", metavar="LABEL")
    group.add_argument("--verify", metavar="LABEL")
    parser.add_argument("--confirm", default="")
    args = parser.parse_args()
    try:
        require_context()
        if args.verify:
            doc = verify_backup(args.verify)
            print("INFINITY BACKUP VERIFIED: SHA256, sizes and private manifest OK; restore still untested")
            print("backup=", doc["archive"])
            return 0
        snapshot = validated_snapshot()
        if args.inspect:
            print(json.dumps({
                "controller": snapshot["controller"],
                "containers_stopped": len(snapshot["containers"]),
                "private_volumes": [x["name"] for x in snapshot["volumes"]],
                "external_manual_gates": snapshot["external_gates"],
                "inventory_sha256": snapshot_signature(snapshot),
                "backup_required": True, "destruction_authorized": False,
            }, indent=2))
            return 0
        if args.confirm != CONFIRM:
            raise BackupBlocked("explicit backup confirmation required: " + CONFIRM)
        doc = make_backup(snapshot, args.backup)
        verified = verify_backup(args.backup)
        if verified["sha256"] != doc["sha256"]:
            raise BackupBlocked("immediate verification disagreed")
        print("INFINITY PRIVATE BACKUP VERIFIED: SHA256 " + doc["sha256"])
        print("OUTSTANDING: independent restore drill, external bridge/WG/DNS review and separate full uninstall approval")
        return 0
    except (BackupBlocked, life.LifecycleError, OSError, ValueError, tarfile.TarError) as exc:
        # Never print external Docker errors or contents of backup archive.
        print("INFINITY UNINSTALL PREPARATION BLOCKED: " + str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
