"""Regression tests for operator opt-in to typed Compose actions."""
import importlib.util
from pathlib import Path
import unittest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "enable-infinity-compose-actions.py"
spec = importlib.util.spec_from_file_location("infinity_compose_actions", SCRIPT)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

BASE = """version: 1
mode: scoped
features:
  full_mode_enabled: false
filesystem:
  read:
    - /opt
  write:
    - /opt
docker:
  inspect: []
  manage: []
  actions: []
compose:
  inspect: []
  manage: []
  actions: []
network:
  mode: blocked
  destinations: []
"""
ENV = "VPS_AGENT_SCOPE_ROOT=/opt\nVPS_AGENT_WHOLE_HOST=0\n"


class TestInfinityTypedCompose(unittest.TestCase):
    def test_allowlist_and_idempotence(self):
        updated, changed = mod.prepare(BASE, ENV)
        self.assertTrue(changed)
        for action in ("validate", "pull", "up", "down"):
            self.assertIn(f"    - {action}\n", updated)
        self.assertEqual(mod.prepare(updated, ENV), (updated, False))
        self.assertIn("docker:\n  inspect: []\n  manage: []\n  actions: []", updated)

    def test_fail_closed_on_invalid_scope(self):
        for candidate in (ENV.replace("/opt", "/"), ENV.replace("WHOLE_HOST=0", "WHOLE_HOST=1"), ""):
            with self.subTest(candidate=candidate), self.assertRaises(ValueError):
                mod.prepare(BASE, candidate)

    def test_fail_closed_on_broad_docker_or_root(self):
        bad = (
            BASE.replace("docker:\n  inspect: []\n  manage: []", "docker:\n  inspect: []\n  manage:\n    - /opt"),
            BASE.replace("mode: scoped", "mode: full"),
            BASE.replace("  full_mode_enabled: false", "  full_mode_enabled: true"),
            BASE.replace("compose:\n  inspect: []", "compose:\n  inspect:\n    - /opt"),
        )
        for candidate in bad:
            with self.subTest(candidate=candidate), self.assertRaises(ValueError):
                mod.prepare(candidate, ENV)

    def test_exact_field_change(self):
        updated, _ = mod.prepare(BASE, ENV)
        self.assertEqual(
            updated.replace("  actions:\n    - validate\n    - pull\n    - up\n    - down\n", "  actions: []\n", 1),
            BASE,
        )


if __name__ == "__main__":
    unittest.main()
