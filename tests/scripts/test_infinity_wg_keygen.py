"""Contract for the non-destructive WireGuard key generation entrypoint."""
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "infinity-wg-keygen.sh"


class InfinityWireGuardKeygenTests(unittest.TestCase):
    def test_bash_syntax(self):
        check = subprocess.run(["bash", "-n", str(SCRIPT)],
                               capture_output=True, text=True, timeout=4)
        self.assertEqual(check.returncode, 0, check.stderr)

    def test_help_is_safe_and_does_not_need_root(self):
        check = subprocess.run(["bash", str(SCRIPT), "--help"],
                               capture_output=True, text=True, timeout=4)
        self.assertEqual(check.returncode, 0, check.stderr)
        self.assertIn("srv-ia", check.stdout)
        self.assertIn("vps", check.stdout)

    def test_invalid_role_never_mutates_machine(self):
        check = subprocess.run(["bash", str(SCRIPT), "invalid-role"],
                               capture_output=True, text=True, timeout=4)
        self.assertEqual(check.returncode, 2, check.stdout+check.stderr)
        self.assertIn("identidade inválida", check.stderr)

    def test_private_key_not_logged_or_transferred_by_script(self):
        source = SCRIPT.read_text()
        self.assertIn("umask 077", source)
        self.assertIn("wg genkey", source)
        self.assertIn("wg pubkey", source)
        self.assertIn("-m 0600", source)
        self.assertNotIn("cat -- \"$private\"", source)
        self.assertNotIn("curl -", source)
        self.assertNotIn("git push", source)


if __name__ == "__main__":
    unittest.main()
