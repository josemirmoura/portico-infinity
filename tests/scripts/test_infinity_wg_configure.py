"""Unit and safety checks for offline two-host WireGuard configuration."""
from __future__ import annotations

import base64
import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
FILE = ROOT / "scripts/infinity-wg-configure.py"
spec = importlib.util.spec_from_file_location("infinity_wg_configure", FILE)
wgconf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wgconf)

KEY_VPS = base64.b64encode(bytes(range(32))).decode()
KEY_SRV = base64.b64encode(bytes(range(1, 33))).decode()


class InfinityWGConfigTests(unittest.TestCase):
    def test_public_key_format(self):
        self.assertEqual(wgconf.canonical_key(KEY_VPS), KEY_VPS)
        for value in ("", "abc", "A" * 44, "AAA /stuff", "=" * 44):
            with self.subTest(value=value):
                with self.assertRaises(wgconf.UnsafeEnvironment):
                    wgconf.canonical_key(value)

    def test_vps_has_no_default_route_or_peer_endpoint(self):
        data = wgconf.build_config(
            "vps", private_key=KEY_VPS, peer_key=KEY_SRV, endpoint_ip=None)
        self.assertIn("Address = 10.88.88.1/30", data)
        self.assertIn("AllowedIPs = 10.88.88.2/32", data)
        self.assertIn("ListenPort = 51822", data)
        self.assertNotIn("Endpoint = ", data)
        for forbidden in ("0.0.0.0/0", "SaveConfig", "PostUp", "PreDown"):
            self.assertNotIn(forbidden, data)
        with self.assertRaises(wgconf.UnsafeEnvironment):
            wgconf.build_config("vps", private_key=KEY_VPS, peer_key=KEY_SRV,
                                endpoint_ip="192.0.2.40")

    def test_srv_initiates_tunnel_without_redirecting_home_internet(self):
        data = wgconf.build_config("srv-ia", private_key=KEY_SRV,
                                   peer_key=KEY_VPS, endpoint_ip="192.0.2.40")
        self.assertIn("Address = 10.88.88.2/30", data)
        self.assertIn("AllowedIPs = 10.88.88.1/32", data)
        self.assertIn("Endpoint = 192.0.2.40:51822", data)
        self.assertIn("PersistentKeepalive = 25", data)
        self.assertNotIn("ListenPort", data)
        self.assertNotIn("0.0.0.0/0", data)
        with self.assertRaises(wgconf.UnsafeEnvironment):
            wgconf.build_config("srv-ia", private_key=KEY_SRV,
                                peer_key=KEY_VPS, endpoint_ip=None)
        for ip in ("127.0.0.1", "0.0.0.0", "224.0.0.1", "invalid"):
            with self.subTest(ip=ip):
                with self.assertRaises(wgconf.UnsafeEnvironment):
                    wgconf.build_config("srv-ia", private_key=KEY_SRV,
                                        peer_key=KEY_VPS, endpoint_ip=ip)

    def test_colliding_route_is_denied(self):
        fake = subprocess.CompletedProcess(
            args=[], returncode=0,
            stdout="default via 192.168.2.253 dev wlp6s0\n"
                   "10.88.88.0/30 dev wg-else proto kernel\n",
            stderr="")
        with patch.object(wgconf.subprocess, "run", return_value=fake):
            with self.assertRaisesRegex(wgconf.UnsafeEnvironment, "collides"):
                wgconf.check_local_route_collision()

    def test_normal_routes_are_accepted(self):
        fake = subprocess.CompletedProcess(
            args=[], returncode=0,
            stdout="default via 192.168.2.253 dev wlp6s0\n"
                   "10.77.77.0/24 dev enp7s0 proto kernel\n"
                   "172.18.0.0/16 dev br-abc\n"
                   "192.168.2.0/24 dev wlp6s0\n",
            stderr="")
        with patch.object(wgconf.subprocess, "run", return_value=fake):
            wgconf.check_local_route_collision()

    def test_no_write_when_not_root(self):
        with patch.object(wgconf.os, "geteuid", return_value=1000):
            with self.assertRaisesRegex(wgconf.UnsafeEnvironment, "root"):
                wgconf.prepare("srv-ia", KEY_VPS, "192.0.2.40")

    def test_configuration_does_not_auto_start_network(self):
        source = FILE.read_text()
        for mutation in ("ufw allow", "wg-quick up", "systemctl start",
                         "ip link add", "docker run", "sudo "):
            self.assertNotIn(mutation, source)
        self.assertIn("os.O_EXCL", source)
        self.assertIn("os.O_NOFOLLOW", source)
        self.assertIn("0o600", source)


if __name__ == "__main__":
    unittest.main()
