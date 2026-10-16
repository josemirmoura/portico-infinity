"""Contract checks for the separate WireGuard edge installer path."""
from pathlib import Path
import os
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]


class InfinityRemoteInstallTests(unittest.TestCase):
    def test_scripts_parse(self):
        for rel in ("scripts/install-infinity.sh", "scripts/install.sh",
                    "scripts/setup-integrated-auth.sh"):
            with self.subTest(rel=rel):
                p = subprocess.run(["bash", "-n", str(ROOT/rel)],
                                   capture_output=True, text=True, timeout=5)
                self.assertEqual(p.returncode, 0, p.stderr)

    def test_explicit_remote_mode_does_not_affect_community(self):
        install = (ROOT/"scripts/install.sh").read_text()
        auth = (ROOT/"scripts/setup-integrated-auth.sh").read_text()
        self.assertIn("--remote-edge) REMOTE_EDGE=1", install)
        self.assertIn('--wg-bind-address)', install)
        self.assertIn("INFINITY REMOTE DENIED: requires --infinity-opt", install)
        self.assertIn("requested IP is not assigned to live wg-infinity", install)
        self.assertIn("compose.infinity-wg-gateway.yaml", install)
        self.assertIn("INFINITY_WG_BIND_ADDRESS", install)
        self.assertIn("auth_args+=(--remote-edge)", install)
        self.assertIn("AUTH_COMPOSE_FILE=compose.infinity-auth.remote.yaml", auth)
        self.assertIn("must pass --remote-edge for this existing installation", auth)
        self.assertIn("remote edge is only for an Infinity Compose project", auth)
        self.assertIn("if [ \"$REMOTE_EDGE\" -eq 1 ]; then", auth)

    def test_remote_bind_not_accidentally_extended_to_other_interfaces(self):
        for rel in ("compose.infinity-wg-gateway.yaml",
                    "compose.infinity-auth.remote.yaml"):
            data=(ROOT/rel).read_text()
            self.assertIn("INFINITY_WG_BIND_ADDRESS:?", data)
            self.assertIn("ports: !override",data)
            self.assertNotIn("0.0.0.0:",data)
            self.assertNotIn("127.0.0.1:",data)


if __name__=="__main__":
    unittest.main()
