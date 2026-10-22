"""Non-destructive tests for external operator-owned filecopy revocation.

Only temporary files and fake systemctl callbacks are used.
"""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[2] / "scripts/infinity-revoke-filecopy-subject.py"
SPEC = importlib.util.spec_from_file_location("infinity_filecopy_revocation", SOURCE)
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)

ID = "vps-agent-filecopy-" + ("a" * 32)


class RevocationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        os.chmod(self.root, 0o700)

    def record(self, name, value):
        path = self.root / name
        path.write_text(json.dumps(value), encoding="utf-8")
        path.chmod(0o600)

    def test_hash_is_deterministic_and_subject_scoped(self):
        self.assertEqual(MOD.marker_name("user-a"), MOD.marker_name("user-a"))
        self.assertNotEqual(MOD.marker_name("user-a"), MOD.marker_name("user-b"))
        self.assertRaises(MOD.RevocationError, MOD.marker_name, "")
        self.assertRaises(MOD.RevocationError, MOD.marker_name, "x" * 1025)

    def test_confirmed_revocation_can_precede_first_filecopy(self):
        folder = self.root / "new-filecopy-state"
        self.assertFalse(folder.exists())
        MOD.ensure_state_dir(folder)
        self.assertTrue(folder.is_dir())
        self.assertEqual(folder.stat().st_mode & 0o777, 0o700)
        MOD.ensure_state_dir(folder)
        self.assertTrue(MOD.create_fence(folder, "preemptively-revoked"))
        self.assertFalse(MOD.create_fence(folder, "preemptively-revoked"))

    def test_revocation_refuses_writable_state_parent(self):
        parent = self.root / "writable"
        parent.mkdir(mode=0o777)
        parent.chmod(0o777)
        with self.assertRaises(MOD.RevocationError):
            MOD.ensure_state_dir(parent / "jobs")
        self.assertFalse((parent / "jobs").exists())

    def test_marker_is_durable_idempotent_and_private(self):
        self.assertTrue(MOD.create_fence(self.root, "operator"))
        self.assertFalse(MOD.create_fence(self.root, "operator"))
        marker = self.root / MOD.marker_name("operator")
        self.assertEqual(marker.read_bytes(), b"revoked\n")
        self.assertEqual(marker.stat().st_mode & 0o777, 0o600)

    def test_symlink_fence_fails_closed(self):
        target = self.root / "foreign"
        target.write_text("not trusted")
        (self.root / MOD.marker_name("operator")).symlink_to(target)
        with self.assertRaises(MOD.RevocationError):
            MOD.create_fence(self.root, "operator")

    def test_job_selection_is_exact(self):
        self.record(ID + ".request.json", {"job_id": ID, "subject": "alice"})
        self.assertEqual(MOD.subject_jobs(self.root, "alice"), [ID])
        self.assertEqual(MOD.subject_jobs(self.root, "bob"), [])

    def test_malformed_or_untrusted_manifest_is_rejected(self):
        self.record(ID + ".request.json", {"job_id": ID, "subject": "alice"})
        (self.root / (ID + ".request.json")).chmod(0o666)
        with self.assertRaises(MOD.RevocationError):
            MOD.subject_jobs(self.root, "alice")

    def test_typed_unit_is_stopped_and_cancelled_progress_verified(self):
        self.record(ID + ".progress.json", {"job_id": ID, "phase": "cancelled"})
        calls = []
        def fake_systemctl(*args):
            calls.append(args)
            if args[0] == "show":
                if "--property=LoadState,ActiveState" in args:
                    return "LoadState=loaded\nActiveState=activating\n"
                return "ActiveState=inactive\n"
            if args[0] == "stop":
                return ""
            raise AssertionError(args)

        self.assertEqual(MOD.reconcile_job(self.root, ID, fake_systemctl), "cancelled")
        self.assertTrue(any(call[0] == "stop" and call[1] == ID + ".service" for call in calls))

    def test_active_unit_without_durable_result_is_uncertain(self):
        def fake_systemctl(*args):
            return ("LoadState=loaded\nActiveState=active\n"
                    if "--property=LoadState,ActiveState" in args else "ActiveState=inactive\n")
        self.assertEqual(MOD.reconcile_job(self.root, ID, fake_systemctl), "uncertain")

    def test_rejects_foreign_unit_and_unsupported_state(self):
        self.assertRaises(MOD.RevocationError, MOD.reconcile_job,
                          self.root, "ssh", lambda *_: "")
        with self.assertRaises(MOD.RevocationError):
            MOD.reconcile_job(self.root, ID, lambda *_: "LoadState=loaded\nActiveState=unknown\n")


if __name__ == "__main__":
    unittest.main()
