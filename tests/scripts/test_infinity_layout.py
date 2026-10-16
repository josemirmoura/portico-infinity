"""Unit tests for Pórtico Infinity protected control-plane layout.

Runs exclusively in disposable temp directories, without privilege changes.
"""
from __future__ import annotations

import importlib.util
import os
from pathlib import Path
import stat
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "scripts" / "check-infinity-layout.py"
SPEC = importlib.util.spec_from_file_location("infinity_layout", SOURCE)
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)


class InfinityLayoutTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="infinity-layout-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name) / "controller"
        self.root.mkdir(mode=0o700)
        self.src = self.root / "scripts"
        self.src.mkdir(mode=0o700)
        self.source = self.src / "gateway.sh"
        self.source.write_text("echo test\n")
        self.source.chmod(0o600)
        self.uid = os.getuid()

    def audit(self):
        return MOD.audit_tree(self.root, self.uid, check_ancestors=False)

    def test_safe_controller(self):
        self.assertEqual(self.audit(), 3)

    def test_group_writable_subdirectory_fails(self):
        self.src.chmod(0o770)
        with self.assertRaises(MOD.LayoutError):
            self.audit()

    def test_world_writable_file_fails(self):
        self.source.chmod(0o666)
        with self.assertRaises(MOD.LayoutError):
            self.audit()

    def test_symlink_file_fails(self):
        alias = self.src / "alias"
        alias.symlink_to(self.source)
        with self.assertRaises(MOD.LayoutError):
            self.audit()

    def test_symlink_directory_fails(self):
        (self.root / "shortcut").symlink_to(self.src, target_is_directory=True)
        with self.assertRaises(MOD.LayoutError):
            self.audit()

    def test_unsafe_root_fails(self):
        self.root.chmod(0o777)
        with self.assertRaises(MOD.LayoutError):
            self.audit()

    def test_missing_root_fails(self):
        with self.assertRaises(MOD.LayoutError):
            MOD.audit_tree(self.root / "absent", self.uid, check_ancestors=False)

    def test_wrong_owner_fails(self):
        with self.assertRaises(MOD.LayoutError):
            MOD.audit_tree(self.root, self.uid + 100, check_ancestors=False)

    def test_special_file_fails(self):
        import socket
        sock = socket.socket(socket.AF_UNIX)
        try:
            address = str(self.root / "socket")
            sock.bind(address)
            with self.assertRaises(MOD.LayoutError):
                self.audit()
        finally:
            sock.close()

    def test_setuid_fails(self):
        self.source.chmod(0o4600)
        if self.source.stat().st_mode & stat.S_ISUID:
            with self.assertRaises(MOD.LayoutError):
                self.audit()


if __name__ == "__main__":
    unittest.main()
