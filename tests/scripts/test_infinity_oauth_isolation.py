"""Infinity OAuth isolation contract: no Community volume or Traefik identity reuse.

Read-only source checks and optional docker compose config rendering.
No Docker containers are created by this test.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
INFINITY = ROOT / "compose.infinity-auth.yaml"
COMMUNITY = ROOT / "compose.integrated-auth.yaml"
SETUP = ROOT / "scripts/setup-integrated-auth.sh"


def traefik_keys(text):
    out = set()
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("- traefik."):
            out.add(stripped.split("=", 1)[0])
    return out


class InfinityOAuthContract(unittest.TestCase):
    def test_unique_traefik_route_and_service_labels(self):
        community, infinity = COMMUNITY.read_text(), INFINITY.read_text()
        old_keys = traefik_keys(community)
        new_keys = traefik_keys(infinity)
        self.assertTrue(old_keys and new_keys)
        route_items = lambda keys: {x for x in keys if any(
            segment in x for segment in (
                "traefik.http.routers.", "traefik.http.services.",
                "traefik.http.middlewares."
            )
        )}
        a, b = route_items(old_keys), route_items(new_keys)
        self.assertTrue(a and b)
        self.assertFalse(a & b, "Infinity routes/services clash with Community")
        self.assertTrue(any("portico-infinity-" in key for key in b))
        self.assertFalse(any("vps-agent-" in key for key in b))
        self.assertEqual(set(community.splitlines()) ^ set(infinity.splitlines()),
                         {line for line in (community.splitlines() + infinity.splitlines())
                          if line.lstrip().startswith("- traefik.") and
                          ("vps-agent-" in line or "portico-infinity-" in line)})

    def test_bootstrap_volume_and_compose_selected_per_project(self):
        setup = SETUP.read_text()
        self.assertIn("AUTH_COMPOSE_FILE=compose.infinity-auth.yaml", setup)
        self.assertIn("ZITADEL_BOOTSTRAP_VOLUME=portico-infinity_zitadel-bootstrap", setup)
        self.assertIn('compose=(docker compose -f compose.yaml -f "$AUTH_COMPOSE_FILE")', setup)
        self.assertIn('export COMPOSE_PROJECT_NAME="$AUTH_PROJECT_NAME"', setup)
        self.assertIn("INFINITY AUTH DENIED: no reusable Traefik", setup)
        volume_lines = [line.strip() for line in setup.splitlines()
                        if line.strip().startswith("-v ") and
                        "ZITADEL_BOOTSTRAP_VOLUME" in line]
        self.assertEqual(len(volume_lines), 3)
        self.assertTrue(all("mcp-vps-agent_zitadel-bootstrap" not in line for line in volume_lines))

    def test_compose_render_separates_runtime_resources(self):
        if not shutil.which("docker"):
            self.skipTest("Docker Compose unavailable in local Python runtime")
        env = dict(os.environ)
        env.update({
            "COMPOSE_PROJECT_NAME": "portico-infinity",
            "VPS_AGENT_ADMIN_TOKEN": "disposable-ci-placeholder",
            "VPS_AGENT_EDGE_NETWORK": "infinity-disposable-edge",
            "VPS_AGENT_DOMAIN": "infinity.example.invalid",
            "VPS_AGENT_SCOPE_ROOT": "/opt",
            "VPS_AGENT_INFINITY_PROTECTED_ROOT": "/srv/portico-infinity",
            "ZITADEL_MASTERKEY": "01234567890123456789012345678901",
            "ZITADEL_SESSION_COOKIE_SECRET": "disposable-infinity-identity-cookie-secret",
            "ZITADEL_POSTGRES_PASSWORD": "disposable-infinity-database-secret",
            "ZITADEL_BOOTSTRAP_ADMIN_PASSWORD": "Disposab1e123!Password",
            "ZITADEL_BOOTSTRAP_PAT_EXPIRATION": "2099-01-01T00:00:00Z",
            "ZITADEL_LOGIN_PAT_EXPIRATION": "2099-01-01T00:00:00Z",
        })
        proc = subprocess.run(
            ["docker", "compose", "-f", "compose.yaml", "-f",
             "compose.infinity-auth.yaml", "config", "--format", "json"],
            cwd=ROOT, env=env, capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr[-3000:])
        conf = json.loads(proc.stdout)
        self.assertEqual(conf["name"], "portico-infinity")
        self.assertTrue(any(
            info.get("name", "").startswith("portico-infinity_")
            for info in conf.get("volumes", {}).values()
        ))
        for name in ("gateway", "zitadel-api", "zitadel-login"):
            labels = conf["services"][name].get("labels") or {}
            if isinstance(labels, list):
                labels = dict(s.split("=", 1) for s in labels)
            for label in labels:
                if label.startswith("traefik.http."):
                    self.assertNotIn("vps-agent-", label)


if __name__ == "__main__":
    unittest.main()
