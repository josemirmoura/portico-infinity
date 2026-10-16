"""Remote SRV-IA identity stack cannot publish ports to Wi-Fi or production Traefik."""
from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
OVERLAY = ROOT / "compose.infinity-auth.remote.yaml"


class InfinityRemoteOAuthComposeTest(unittest.TestCase):
    def test_remote_uses_private_wireguard_only_and_no_traefik_local_routes(self):
        body = OVERLAY.read_text()
        self.assertNotIn("public-edge", body)
        self.assertNotIn("traefik.", body)
        self.assertIn("ports: !override", body)
        self.assertIn("INFINITY_WG_BIND_ADDRESS:?", body)
        self.assertNotIn("0.0.0.0:", body)

    def test_compose_render_bound_to_wireguard_and_separate_project(self):
        if not shutil.which("docker"):
            self.skipTest("docker compose CLI unavailable")
        env = os.environ.copy()
        env.update({
            "COMPOSE_PROJECT_NAME": "portico-infinity",
            "INFINITY_WG_BIND_ADDRESS": "10.88.88.2",
            "VPS_AGENT_SCOPE_ROOT": "/opt",
            "VPS_AGENT_BIND_ADDRESS": "127.0.0.1",
            "VPS_AGENT_ADMIN_TOKEN": "disposable-no-secret",
            "VPS_AGENT_DOMAIN": "infinity.example.invalid",
            "ZITADEL_MASTERKEY": "0"*32,
            "ZITADEL_SESSION_COOKIE_SECRET": "1"*32,
            "ZITADEL_POSTGRES_PASSWORD": "disposable-only",
            "ZITADEL_BOOTSTRAP_ADMIN_PASSWORD": "T1!disposable-only",
            "ZITADEL_BOOTSTRAP_PAT_EXPIRATION": "2099-01-01T00:00:00Z",
            "ZITADEL_LOGIN_PAT_EXPIRATION": "2099-01-01T00:00:00Z"
        })
        cmd=["docker","compose","-f","compose.yaml",
             "-f","compose.infinity-auth.remote.yaml","config","--format","json"]
        p=subprocess.run(cmd,cwd=ROOT,env=env,text=True,capture_output=True,timeout=30)
        self.assertEqual(p.returncode,0,p.stderr[-2400:])
        config=json.loads(p.stdout)
        self.assertEqual(config["name"],"portico-infinity")
        pairs={"gateway":(18081,8080),"zitadel-api":(18082,8080),
               "zitadel-login":(18083,3000)}
        for service,(published,target) in pairs.items():
            item=config["services"][service]
            ports=item.get("ports",[])
            self.assertEqual(len(ports),1,(service,ports))
            p=ports[0]
            self.assertEqual(p["host_ip"],"10.88.88.2",(service,p))
            self.assertEqual(p["published"],str(published),(service,p))
            self.assertEqual(p["target"],target,(service,p))
            self.assertFalse(any("traefik." in k for k in item.get("labels",{})))
            self.assertNotIn("public-edge",item["networks"])
        self.assertNotIn("public-edge",config["networks"])
        self.assertNotIn("ports",config["services"]["broker"])


if __name__=="__main__":
    unittest.main()
