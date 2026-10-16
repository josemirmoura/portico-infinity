"""Ensure Infinity cannot invoke legacy Community update/removal scripts."""
from __future__ import annotations
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class InfinityLifecycleDenialTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory(prefix="infinity-lifecycle-denial-")
        self.addCleanup(tmp.cleanup)
        self.base = Path(tmp.name)
        (self.base / "scripts" / "lib").mkdir(parents=True)
        for rel in ("scripts/remove.sh", "scripts/update.sh", "scripts/lib/product.sh"):
            shutil.copy2(ROOT / rel, self.base / rel)
        (self.base / "VERSION").write_text("0.1.0-test\n")
        (self.base / ".env").write_text("COMPOSE_PROJECT_NAME=portico-infinity\n")
        bindir = self.base / "fake-bin"
        bindir.mkdir()
        self.log = self.base / "docker.log"
        for name in ("docker", "git"):
            script = bindir / name
            script.write_text('#!/bin/sh\\necho "$0 $*" >> "$INFINITY_LIFECYCLE_LOG"\\nexit 0\\n')
            script.chmod(0o755)
        self.env = dict(os.environ)
        self.env["PATH"] = str(bindir) + os.pathsep + self.env.get("PATH", "")
        self.env["INFINITY_LIFECYCLE_LOG"] = str(self.log)
        self.env["VPS_AGENT_LANG"] = "pt-BR"
        self.env["VPS_AGENT_LANG_EXPLICIT"] = "1"

    def test_legacy_removal_refuses_infinity_before_docker(self):
        for args in (["safe"], ["--purge"], ["--purge", "--dry-run"]):
            with self.subTest(args=args):
                cmd = ["bash", "scripts/remove.sh", *args]
                result = subprocess.run(cmd, cwd=self.base, env=self.env, capture_output=True, text=True, timeout=8)
                self.assertEqual(result.returncode, 2, result.stdout+result.stderr)
                self.assertIn("INFINITY LIFECYCLE DENIED", result.stderr)
                self.assertFalse(self.log.exists())
                self.assertFalse((self.base / "backups").exists())

    def test_legacy_update_refuses_before_backup_and_network(self):
        cmd = ["bash", "scripts/update.sh"]
        result = subprocess.run(cmd, cwd=self.base, env=self.env, capture_output=True, text=True, timeout=8)
        self.assertEqual(result.returncode, 2, result.stdout+result.stderr)
        self.assertIn("INFINITY LIFECYCLE DENIED", result.stderr)
        self.assertFalse((self.base / "backups").exists())
        self.assertFalse(self.log.exists())


if __name__ == "__main__":
    unittest.main()
