"""Safe local Infinity first-install simulation, no real Docker or host mutation.

Copies versioned installer sources to a disposable directory, replaces only
Docker and the post-init external verification with test doubles. The real
init.sh and configurator create and verify the policy inside that directory.
"""
from __future__ import annotations
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
COPY = (
    ".env.example", "compose.yaml", "VERSION",
    "scripts/install.sh", "scripts/install-infinity.sh", "scripts/init.sh",
    "scripts/authority-summary.py",
    "scripts/configure-infinity-opt.py", "scripts/delegate-root.py",
    "scripts/lib/product.sh", "scripts/lib/i18n.py",
    "config/policy.example.yaml",
)


class InfinityLocalBootstrapTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="infinity-disposable-install-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        for rel in COPY:
            source, dest = ROOT / rel, self.root / rel
            self.assertTrue(source.is_file(), rel)
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, dest)
        bin_dir = self.root / "fake-bin"
        bin_dir.mkdir()
        docker_log = self.root / "docker.log"
        self.docker_log = docker_log
        docker = bin_dir / "docker"
        docker.write_text(
            '#!/usr/bin/env bash\n'
            'set -eu\n'
            'printf "%s\\n" "$*" >> "$INFINITY_TEST_DOCKER_LOG"\n'
            'exit 0\n',
            encoding="utf-8",
        )
        docker.chmod(0o755)
        (self.root / "scripts" / "preflight.py").write_text(
            "print('fake preflight: isolated test')\n", encoding="utf-8"
        )
        (self.root / "scripts" / "verify.sh").write_text(
            '#!/usr/bin/env bash\n'
            'test -f config/policy.yaml\n'
            'test -f .env\n'
            "echo 'FAKE VERIFY OK: local only'\n", encoding="utf-8"
        )
        self.env = dict(os.environ)
        self.env["PATH"] = str(bin_dir) + os.pathsep + self.env.get("PATH", "")
        self.env["INFINITY_TEST_DOCKER_LOG"] = str(docker_log)
        self.env["VPS_AGENT_LANG"] = "pt-BR"
        self.env["VPS_AGENT_LANG_EXPLICIT"] = "1"
        self.env["SUDO_USER"] = "infinity-test" # mocked id below
        fake_id = bin_dir / "id"
        fake_id.write_text(
            '#!/bin/sh\n'
            'case "$1" in\n'
            '  -un) echo infinity-test;;\n'
            '  -u) if [ "$#" -eq 1 ]; then echo 1001; else echo 51321; fi;;\n'
            '  -gn) echo infinity-test;;\n'
            '  *) exit 1;;\n'
            'esac\n', encoding="utf-8"
        )
        fake_id.chmod(0o755)

    def run_script(self, *extra):
        return subprocess.run(
            ["bash", "scripts/install-infinity.sh", *extra],
            cwd=self.root, env=self.env, text=True,
            input="", capture_output=True, timeout=30, check=False,
        )

    def test_valid_local_bootstrap_creates_exact_scoped_policy(self):
        result = self.run_script(
            "--local-only", "--lang", "pt-BR", "--run-as", "infinity-test",
            "--yes", "--ack-infinity-opt",
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("CHECKPOINT LOCAL CONCLUÍDO", result.stdout)
        self.assertIn("INFINITY OPT:", result.stdout)
        policy = (self.root / "config" / "policy.yaml").read_text()
        self.assertIn("    - chmod", policy)
        self.assertIn("    - chown", policy)
        self.assertNotIn("    - remove_recursive", policy)
        self.assertIn("full_mode_enabled: false", policy)
        self.assertIn("run_as: infinity-test", policy)
        self.assertIn("  admin: broker-only", policy)
        env = (self.root / ".env").read_text()
        self.assertIn("VPS_AGENT_SCOPE_ROOT=/opt", env)
        self.assertIn("VPS_AGENT_WHOLE_HOST=0", env)
        self.assertIn("COMPOSE_PROJECT_NAME=portico-infinity", env)
        self.assertIn("VPS_AGENT_INFINITY_PROTECTED_ROOT=/srv/portico-infinity", env)
        self.assertIn("VPS_AGENT_PORT=18081", env)
        self.assertTrue(self.docker_log.exists())
        commands = self.docker_log.read_text()
        self.assertIn("compose version", commands)
        self.assertIn("up -d --build", commands)

    def test_missing_approval_does_not_create_state_or_call_docker(self):
        result = self.run_script(
            "--local-only", "--run-as", "infinity-test", "--yes"
        )
        self.assertEqual(result.returncode, 4, result.stdout + result.stderr)
        self.assertIn("--ack-infinity-opt", result.stderr)
        self.assertFalse((self.root / ".env").exists())
        self.assertFalse((self.root / "config" / "policy.yaml").exists())
        self.assertFalse(self.docker_log.exists())

    def test_rejects_profile_and_scope_override(self):
        for illegal in ("--profile", "--scope", "--ack-whole-host", "--create-scope"):
            with self.subTest(illegal=illegal):
                result = self.run_script(illegal, "--yes", "--ack-infinity-opt")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.root / ".env").exists())
                self.assertFalse(self.docker_log.exists())


if __name__ == "__main__":
    unittest.main()
