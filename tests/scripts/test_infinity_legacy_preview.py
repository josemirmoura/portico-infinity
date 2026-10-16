"""Prevent accidental invocation of an obsolete insecure Infinity preview."""
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
SHIM = ROOT / "scripts" / "install-infinity-preview.sh"

class LegacyInfinityPreviewTests(unittest.TestCase):
    def test_legacy_preview_refuses_execution(self):
        p = subprocess.run(["bash", str(SHIM), "--yes"], check=False,
                           capture_output=True, text=True, timeout=5)
        self.assertEqual(p.returncode, 3, p.stdout+p.stderr)
        self.assertIn("desativado", p.stderr)
        self.assertIn("scripts/install-infinity.sh", p.stderr)

    def test_help_has_no_side_effects(self):
        p = subprocess.run(["bash", str(SHIM), "--help"], check=False,
                           capture_output=True, text=True, timeout=5)
        self.assertEqual(p.returncode, 0)
        self.assertIn("/srv/portico-infinity", p.stdout)

if __name__ == "__main__":
    unittest.main()
