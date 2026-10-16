"""Infinity /opt policy regression tests (safe: no Docker and no host changes)."""
from __future__ import annotations
import importlib.util
from pathlib import Path
import stat
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
HELPER = ROOT / "scripts" / "configure-infinity-opt.py"
SPEC = importlib.util.spec_from_file_location("configure_infinity_opt", HELPER)
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)
BASE = (ROOT / "config" / "policy.example.yaml").read_text(encoding="utf-8")


def default_policy():
    return BASE.replace("/opt/my-app", "/opt").replace(
        "run_as: vps-agent-exec", "run_as: nobody"
    )


class InfinityOptPolicyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="portico-infinity-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.policy = self.root / "policy.yaml"
        self.env = self.root / ".env"
        self.policy.write_text(default_policy(), encoding="utf-8")
        self.env.write_text(
            "VPS_AGENT_SCOPE_ROOT=/opt\nVPS_AGENT_WHOLE_HOST=0\n",
            encoding="utf-8",
        )

    def configure(self):
        MOD.configure(self.policy, self.env)

    def assert_rejected_unchanged(self):
        before = self.policy.read_bytes()
        with self.assertRaises(MOD.PolicyError):
            self.configure()
        self.assertEqual(before, self.policy.read_bytes())

    def test_typed_file_operations_and_non_root_shell(self):
        self.configure()
        text = self.policy.read_text(encoding="utf-8")
        lists = MOD.delegate.parse_lists(text)
        self.assertEqual(lists[("filesystem", "actions")], list(MOD.FILE_ACTIONS))
        for field in MOD.ROOT_KEYS:
            self.assertEqual(lists[field], ["/opt"])
        self.assertIn("remove_recursive", lists[("filesystem", "actions")])
        self.assertNotIn("*", lists[("filesystem", "actions")])
        self.assertIn("full_mode_enabled: false", text)
        self.assertEqual(stat.S_IMODE(self.policy.stat().st_mode), 0o600)

    def test_idempotent(self):
        self.configure()
        once = self.policy.read_bytes()
        self.configure()
        self.assertEqual(once, self.policy.read_bytes())

    def test_refuses_whole_host(self):
        self.env.write_text("VPS_AGENT_SCOPE_ROOT=/opt\nVPS_AGENT_WHOLE_HOST=1\n")
        self.assert_rejected_unchanged()

    def test_refuses_scope_escape(self):
        self.env.write_text("VPS_AGENT_SCOPE_ROOT=/\nVPS_AGENT_WHOLE_HOST=0\n")
        self.assert_rejected_unchanged()

    def test_refuses_non_opt_roots(self):
        self.policy.write_text(default_policy().replace("/opt", "/"))
        self.assert_rejected_unchanged()

    def test_refuses_full_mode(self):
        self.policy.write_text(default_policy().replace(
            "full_mode_enabled: false", "full_mode_enabled: true"))
        self.assert_rejected_unchanged()

    def test_refuses_root_shell(self):
        self.policy.write_text(default_policy().replace("run_as: nobody", "run_as: root"))
        self.assert_rejected_unchanged()

    def test_refuses_docker_actions(self):
        self.policy.write_text(default_policy().replace(
            "docker:\n  inspect: []\n  manage: []\n  actions: []",
            "docker:\n  inspect: []\n  manage: []\n  actions:\n    - restart"))
        self.assert_rejected_unchanged()

    def test_refuses_network_unrestricted(self):
        self.policy.write_text(default_policy().replace(
            "mode: blocked", "mode: unrestricted"))
        self.assert_rejected_unchanged()

    def test_refuses_symlink_policy(self):
        renamed = self.root / "original.yaml"
        self.policy.rename(renamed)
        self.policy.symlink_to(renamed)
        self.assert_rejected_unchanged()


if __name__ == "__main__":
    unittest.main()
