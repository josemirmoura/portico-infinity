"""Auditable read-only WireGuard teardown preflight, no interface/UFW changes."""
import base64
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[2]
SPEC=importlib.util.spec_from_file_location("infinity_wg_audit",ROOT/"scripts/infinity-wg-decommission-audit.py")
MOD=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)
SPEC2=importlib.util.spec_from_file_location("infinity_wg_config",ROOT/"scripts/infinity-wg-configure.py")
INSTALL=importlib.util.module_from_spec(SPEC2)
SPEC2.loader.exec_module(INSTALL)

PRIV_VPS=base64.b64encode(bytes(range(32))).decode("ascii")
PRIV_SRV=base64.b64encode(bytes(range(1,33))).decode("ascii")
PUB_VPS=base64.b64encode(bytes(range(32,64))).decode("ascii")
PUB_SRV=base64.b64encode(bytes(range(64,96))).decode("ascii")


def cfg(role):
    if role=="vps":
        return INSTALL.build_config("vps",private_key=PRIV_VPS,peer_key=PUB_SRV,endpoint_ip=None)
    return INSTALL.build_config("srv-ia",private_key=PRIV_SRV,peer_key=PUB_VPS,endpoint_ip="198.51.100.22")


class FakeReadCommands:
    def __init__(self, role="vps", *, active=True, peers=None, ip=None, enabled="enabled"):
        self.calls=[]
        self.role=role
        self.active=active
        self.peers=peers
        self.ip=ip
        self.enabled=enabled

    def __call__(self, args, *, stdin=None):
        self.calls.append((tuple(args),stdin))
        prefix=tuple(args)
        if prefix==("wg","pubkey"):
            assert stdin in (PRIV_VPS+"\n",PRIV_SRV+"\n")
            return subprocess.CompletedProcess(args,0,PUB_VPS+"\n" if self.role=="vps" else PUB_SRV+"\n","")
        if prefix==("systemctl","is-enabled",MOD.SERVICE):
            return subprocess.CompletedProcess(args,0 if self.enabled=="enabled" else 1,self.enabled+"\n","")
        if prefix==("systemctl","is-active",MOD.SERVICE):
            return subprocess.CompletedProcess(args,0 if self.active else 3,"active\n" if self.active else "inactive\n","")
        if prefix==("ip","-o","link","show","dev","wg-infinity"):
            return subprocess.CompletedProcess(args,0 if self.active else 1,"2: wg-infinity: <UP> mtu 1420\n" if self.active else "","")
        if prefix==("wg","show","wg-infinity","peers"):
            peer=PUB_SRV if self.role=="vps" else PUB_VPS
            return subprocess.CompletedProcess(args,0,self.peers if self.peers is not None else peer+"\n","")
        if prefix==("ip","-o","-4","address","show","dev","wg-infinity"):
            addr=MOD.ROLES[self.role]["address"]
            return subprocess.CompletedProcess(args,0,self.ip if self.ip is not None else f"2: wg-infinity inet {addr} scope global wg-infinity\n","")
        raise AssertionError("unexpected external/possibly mutating command: "+str(args))


class WireGuardAuditTests(unittest.TestCase):
    def test_installer_shapes_vps_and_srv_ia(self):
        for role in ("vps","srv-ia"):
            with self.subTest(role=role):
                result=MOD.parse_config(cfg(role),role)
                self.assertEqual(result["address"],MOD.ROLES[role]["address"])
                self.assertNotEqual(result["local_private"],result["peer_public"])

    def test_reject_commands_extra_peers_routes_duplicate_directives(self):
        base=cfg("vps")
        attacks=(
            base.replace("Address =","PostDown = /bin/bash -c true\nAddress ="),
            base.replace("PrivateKey =","SaveConfig = true\nPrivateKey ="),
            base.replace("PrivateKey =","DNS = 1.1.1.1\nPrivateKey ="),
            base.replace("PrivateKey =","Table = off\nPrivateKey ="),
            base.replace("AllowedIPs = 10.88.88.2/32","AllowedIPs = 0.0.0.0/0"),
            base+"\n[Peer]\nPublicKey = "+PUB_VPS+"\n",
            base.replace("AllowedIPs =","AllowedIPs = 10.88.88.2/32\nAllowedIPs ="),
            base.replace("Address =","Address\t="),
            base.replace("ListenPort = 51822","ListenPort = 22"),
            base.replace("PrivateKey =","FooBar = unexpected\nPrivateKey ="),
        )
        for i,bad in enumerate(attacks):
            with self.subTest(case=i),self.assertRaises(MOD.AuditBlocked):
                MOD.parse_config(bad,"vps")

    def test_only_expected_commands_and_no_mutations(self):
        parsed=MOD.parse_config(cfg("vps"),"vps")
        fake=FakeReadCommands()
        report=MOD.probe_runtime({"peer_public_key":parsed["peer_public"],"address":parsed["address"]},command=fake)
        self.assertTrue(report["runtime_matches_file"])
        self.assertEqual(report["runtime_peers"],1)
        self.assertFalse(report["shared_use_ruled_out"])
        self.assertFalse(any(cmd[0][0] in ("ufw","docker","ip6tables","iptables") or
                            cmd[0][:2]==("systemctl","stop") or
                            cmd[0][:2]==("wg","set") for cmd in fake.calls))

    def test_wrong_peer_ip_or_unit_status_fails_or_reports_mismatch(self):
        parsed=MOD.parse_config(cfg("vps"),"vps")
        expected={"peer_public_key":parsed["peer_public"],"address":parsed["address"]}
        for fake in (
            FakeReadCommands(peers=PUB_VPS+"\n"),
            FakeReadCommands(ip="2: wg-infinity inet 10.88.88.1/24 scope global wg-infinity\n"),
            FakeReadCommands(peers=PUB_SRV+"\n"+PUB_VPS+"\n"),
        ):
            self.assertFalse(MOD.probe_runtime(expected,command=fake)["runtime_matches_file"])
        fake=FakeReadCommands(enabled="strange-status")
        with self.assertRaises(MOD.AuditBlocked):
            MOD.probe_runtime(expected,command=fake)

    def test_inactive_unit_does_not_imply_exclusive_ownership(self):
        p=MOD.parse_config(cfg("srv-ia"),"srv-ia")
        result=MOD.probe_runtime({"peer_public_key":p["peer_public"],"address":p["address"]},
                                 command=FakeReadCommands(role="srv-ia",active=False,enabled="disabled"))
        self.assertFalse(result["interface_present"])
        self.assertIsNone(result["runtime_matches_file"])
        self.assertFalse(result["shared_use_ruled_out"])

    @unittest.skipUnless(os.geteuid()==0,"private fixtures require real root ownership")
    def test_root_fixture_private_identity_and_symlink_fail_closed(self):
        with tempfile.TemporaryDirectory(prefix="portico-wg-private-",dir="/var/lib") as temp:
            directory=Path(temp)
            (directory/MOD.CONF).write_text(cfg("vps"))
            (directory/"infinity-vps.key").write_text(PRIV_VPS+"\n")
            (directory/"infinity-vps.pub").write_text(PUB_VPS+"\n")
            for name in (MOD.CONF,"infinity-vps.key"):
                (directory/name).chmod(0o600)
            (directory/"infinity-vps.pub").chmod(0o644)
            fake=FakeReadCommands()
            report=MOD.assess("vps",directory=directory,command=fake)
            self.assertTrue(report["identity_verified"])
            self.assertFalse(report["vpn_mutation_authorized"])
            self.assertFalse(report["firewall_mutation_authorized"])
            self.assertFalse(report["private_keys_disclosed"])
            self.assertNotIn(PRIV_VPS,str(report))
            self.assertFalse(report["runtime"]["shared_use_ruled_out"])
            key=directory/"infinity-vps.key"
            key.chmod(0o644)
            with self.assertRaises(MOD.AuditBlocked):
                MOD.assess("vps",directory=directory,command=fake)
            key.chmod(0o600)
            key.unlink()
            key.symlink_to("/etc/passwd")
            with self.assertRaises(MOD.AuditBlocked):
                MOD.assess("vps",directory=directory,command=fake)

    def test_not_a_destructive_wireguard_helper(self):
        src=(ROOT/"scripts/infinity-wg-decommission-audit.py").read_text()
        for forbidden in ("systemctl stop","systemctl disable","wg-quick down",
                          "ufw delete","ufw allow","ip link del","os.unlink(","os.remove("):
            self.assertNotIn(forbidden,src)
        self.assertIn("os.O_NOFOLLOW",src)


if __name__=="__main__":
    unittest.main()
