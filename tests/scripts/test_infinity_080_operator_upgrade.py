"""Contract tests for operator-only Infinity #80 rollout. No live host mutations."""
import pathlib
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "infinity-080-operator-upgrade.sh"


class Infinity080OperatorUpgradeContract(unittest.TestCase):
    def test_script_parses(self):
        subprocess.run(["bash", "-n", str(SCRIPT)], check=True, cwd=ROOT)

    def test_missing_ack_exits_before_any_host_action(self):
        result = subprocess.run(
            ["bash", str(SCRIPT)], cwd=ROOT, capture_output=True, text=True, timeout=8
        )
        self.assertEqual(result.returncode, 64, result.stderr)
        self.assertIn("--ack-operator-upgrade", result.stderr)

    def test_wrong_ack_exits_before_any_host_action(self):
        result = subprocess.run(
            ["bash", str(SCRIPT), "--ack-operator-upgrade", "EXTRA"], cwd=ROOT,
            capture_output=True, text=True, timeout=8,
        )
        self.assertEqual(result.returncode, 64, result.stderr)

    def test_pins_target_and_restricts_changes(self):
        s = SCRIPT.read_text()
        self.assertIn("OLD=48d8c15978a675090a052b977b47abd1fbdd87f9", s)
        self.assertIn("TARGET=53a6b14dae42bbe6cfd8eba9605468e7b28b54f7", s)
        self.assertIn('git diff --quiet "$OLD" "$TARGET"', s)
        self.assertIn("internal/state/ internal/jobs/ internal/securefs/ Dockerfile", s)
        self.assertIn("compose.yaml compose.infinity-auth.remote.yaml config/", s)
        self.assertIn('git rev-list --count "$OLD".."$TARGET"', s)
        self.assertIn("tar -C", s)
        self.assertIn("runtime.sha256", s)
        self.assertIn("ROLLBACK", s)
        self.assertIn('test "$(hostname -s)" = srv-ia', s)
        self.assertIn('test "$(stat -c \'%u:%g\' "$BASE")" = 0:0', s)
        self.assertIn("--limit 1000", s)
        self.assertNotIn("docker compose down", s)
        self.assertNotIn("systemctl reboot", s)


if __name__ == "__main__":
    unittest.main()
