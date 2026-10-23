"""Synthetic-only full-uninstall backup safety gate tests.

No Docker/systemd calls or production files are modified.
"""
from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SOURCE = Path(__file__).resolve().parents[2] / "scripts/infinity-uninstall-backup.py"
SPEC = importlib.util.spec_from_file_location("infinity_uninstall_backup", SOURCE)
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)


class FakeLifecycle:
    PROJECT = "portico-infinity"

    def __init__(self):
        self.volume = "portico-infinity_zitadel-postgres-data"
        self.mount = "/var/lib/docker/volumes/" + self.volume + "/_data"
        self.running = False
        self.shared = False
        self.bad_label = False
        self.calls = []
        self.container = "a" * 64

    def inventory(self, cmd):
        return ([{"id": self.container, "service": "zitadel-postgres", "running": self.running}],
                [{"id": "b" * 64, "name": "portico-infinity_integrated-auth", "attached": []}],
                [self.volume])

    @staticmethod
    def _single_json(data):
        value = json.loads(data)
        if len(value) != 1:
            raise MOD.BackupBlocked("bad inspect JSON")
        return value[0]

    @staticmethod
    def _ids(data):
        return [s for s in data.splitlines() if s]

    def command(self, args):
        self.calls.append(tuple(args))
        if args[:3] == ["docker", "volume", "inspect"]:
            return json.dumps([{
                "Name": self.volume,
                "Labels": {"com.docker.compose.project": "foreign" if self.bad_label else self.PROJECT},
                "Mountpoint": self.mount,
            }])
        if args[:2] == ["docker", "ps"]:
            return self.container + ("\n" + ("f" * 64) if self.shared else "") + "\n"
        raise AssertionError("read-only command allowlist violated")


class BackupSafetyTests(unittest.TestCase):
    def setUp(self):
        self.life = FakeLifecycle()
        self.original = MOD.life
        MOD.life = self.life

    def tearDown(self):
        MOD.life = self.original

    def test_plan_requires_stopped_containers_and_owned_volumes(self):
        with mock.patch.object(MOD, "protected", return_value=None):
            for attr, expected in (("running", "stop Infinity"),
                                   ("shared", "shared with non-Infinity"),
                                   ("bad_label", "foreign identity")):
                with self.subTest(attr=attr):
                    setattr(self.life, attr, True)
                    with self.assertRaisesRegex(MOD.BackupBlocked, expected):
                        MOD.validated_snapshot(self.life.command)
                    setattr(self.life, attr, False)
            snapshot = MOD.validated_snapshot(self.life.command)
            self.assertEqual(len(snapshot["volumes"]), 1)
            self.assertEqual(snapshot["external_gates"][0][:10], "VPS bridge")
            self.assertTrue(all(args[1] in ("volume", "ps") for args in self.life.calls))
            self.assertNotIn("rm", repr(self.life.calls))

    def test_invalid_docker_mountpoint_aborts_closed(self):
        self.life.mount = "/tmp/foreign/project"
        with self.assertRaisesRegex(MOD.BackupBlocked, "nonstandard/remote"):
            MOD.validated_snapshot(self.life.command)

    def test_filename_validation_and_deterministic_snapshot_digest(self):
        for bad in ("..", "X", "../../opt", "test aaaa", "b" * 65, "é123456789"):
            with self.subTest(bad=bad), self.assertRaises(MOD.BackupBlocked):
                MOD.safe_backup_name(bad)
        self.assertEqual(MOD.safe_backup_name("synthetic123"), "infinity-uninstall-synthetic123.tar")
        item = {"schema": 1, "containers": [], "volumes": []}
        self.assertEqual(MOD.snapshot_signature(item), MOD.snapshot_signature(dict(reversed(list(item.items())))))

    def test_private_archive_roundtrip_and_sha_detects_tampering(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            root.chmod(0o700)
            controller = root / "controller"
            volume = root / "volume"
            controller.mkdir()
            volume.mkdir()
            (controller / "policy.yaml").write_text("version: 1\n", encoding="utf-8")
            (controller / ".env").write_text("synthetic-private-token", encoding="utf-8")
            (volume / "postgres.txt").write_text("synthetic identity data", encoding="utf-8")
            snapshot = {"schema": 1, "controller": str(controller),
                        "containers": [], "networks": [],
                        "volumes": [{"name": "portico-infinity_zitadel-postgres-data",
                                     "mountpoint": str(volume)}],
                        "external_gates": ["external owner checks required"]}
            with (
                mock.patch.object(MOD, "CONTROLLER", controller),
                mock.patch.object(MOD, "BACKUP_ROOT", root),
                mock.patch.object(MOD, "protected", return_value=None),
                mock.patch.object(MOD, "validated_snapshot", return_value=snapshot),
            ):
                label = "isolated-ci-2026"
                result = MOD.make_backup(snapshot, label, root=root)
                verified = MOD.verify_backup(label, root=root)
                self.assertEqual(result["sha256"], verified["sha256"])
                self.assertEqual((root / result["archive"]).stat().st_mode & 0o777, 0o600)
                self.assertEqual((root / (result["archive"] + ".json")).stat().st_mode & 0o777, 0o600)
                self.assertEqual(verified["restore_status"], "NOT_TESTED: dry-run restore required before irreversible uninstall")
                with self.assertRaisesRegex(MOD.BackupBlocked, "already exists"):
                    MOD.make_backup(snapshot, label, root=root)
                with (root / result["archive"]).open("ab") as payload:
                    payload.write(b"tampered")
                with self.assertRaisesRegex(MOD.BackupBlocked, "size mismatch"):
                    MOD.verify_backup(label, root=root)

    def test_snapshot_change_during_backup_never_publishes_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            root.chmod(0o700)
            controller = root / "controller"
            controller.mkdir()
            (controller / "data").write_text("sensitive synthetic file")
            before = {"schema":1, "volumes":[], "containers":[], "networks":[]}
            after = {**before, "containers":["new foreign id"]}
            with (
                mock.patch.object(MOD, "CONTROLLER", controller),
                mock.patch.object(MOD, "BACKUP_ROOT", root),
                mock.patch.object(MOD, "protected", return_value=None),
                mock.patch.object(MOD, "validated_snapshot", return_value=after),
            ):
                with self.assertRaisesRegex(MOD.BackupBlocked, "changed during backup"):
                    MOD.make_backup(before, "synthetic-race", root=root)
                self.assertFalse((root / "infinity-uninstall-synthetic-race.tar").exists())
                self.assertFalse(list(root.glob(".infinity-staging-*")))

    def test_unsafe_private_backup_dir_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            backup = Path(directory) / "private"
            backup.mkdir(mode=0o755)
            with mock.patch.object(MOD, "BACKUP_ROOT", backup):
                with self.assertRaises(MOD.BackupBlocked):
                    MOD.verify_backup("isolated-ci", root=backup)

    def test_no_destructive_cli_mode_is_exposed(self):
        from pathlib import Path
        source = Path(SOURCE).read_text()
        self.assertNotIn("uninstall-runtime", source)
        self.assertNotIn("docker volume rm", source)
        self.assertIn("destruction_authorized\": False", source)


if __name__ == "__main__":
    unittest.main()
