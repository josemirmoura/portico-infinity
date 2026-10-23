"""Non-destructive local-uninstall invariant tests. All host calls are fake."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SOURCE=Path(__file__).resolve().parents[2]/"scripts/infinity-uninstall-local.py"
SPEC=importlib.util.spec_from_file_location("infinity_uninstall_local",SOURCE)
MOD=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)


class FakeLife:
    PROJECT="portico-infinity"
    @staticmethod
    def _single_json(raw):
        val=json.loads(raw)
        assert isinstance(val,list) and len(val)==1
        return val[0]
    @staticmethod
    def _ids(raw):
        return [line for line in raw.splitlines() if line]

class FakeBackup:
    ALLOWED_VOLUME=__import__("re").compile(r"portico-infinity_[A-Za-z0-9_-]+\Z")
    life=FakeLife()


class UninstallLocalTests(unittest.TestCase):
    def test_confirmations_bound_to_exact_backup_checksum(self):
        h="a"*64
        c,r=MOD.confirmation_strings({"sha256":h})
        self.assertEqual(c,"REMOVE-LOCAL-INFINITY-"+h[:20])
        self.assertEqual(r,"RESTORE-TESTED-"+h[:20])
        for bad in ("", "abcd", "z"*64, "a"*63+"z", None):
            with self.subTest(bad=bad), self.assertRaises(MOD.LocalRemovalBlocked):
                MOD.confirmation_strings({"sha256":bad})

    def test_readonly_operator_plan_never_auto_deletes(self):
        h="a"*64
        doc={"archive":"infinity-uninstall-runner-12345678.tar","sha256":h}
        snap={"controller":"/srv/portico-infinity",
              "containers":["a"*64],"networks":["portico-infinity_default"],
              "volumes":[{"name":"portico-infinity_zitadel-bootstrap"}],
              "external_gates":["VPS bridge requires independent consent"]}
        obj=MOD.operator_plan(doc,snap)
        self.assertFalse(obj["automatic_deletion"])
        self.assertEqual(obj["volumes"],["portico-infinity_zitadel-bootstrap"])
        self.assertEqual(len(obj["requires_explicit_confirm"].split("-")[-1]),20)

    def test_foreign_mount_under_controller_aborts(self):
        with tempfile.TemporaryDirectory() as d:
            mountfile=Path(d)/"mountinfo"
            mountfile.write_text("1 2 8:1 / / rw - ext4 /dev/sda rw\n"
                "3 2 8:1 / /srv/portico-infinity/state/private rw - ext4 /dev/sda rw\n")
            with self.assertRaisesRegex(MOD.LocalRemovalBlocked,"mounted filesystem"):
                MOD.inspect_mounts(mountinfo=mountfile)
            mountfile.write_text("1 2 8:1 / / rw - ext4 /dev/sda rw\n"
                "2 1 8:1 / /opt/other rw - ext4 /dev/sda rw\n")
            MOD.inspect_mounts(mountinfo=mountfile)

    def test_foreign_volume_label_blocks_before_rm(self):
        entry={"name":"portico-infinity_zitadel-bootstrap",
               "mountpoint":"/var/lib/docker/volumes/portico-infinity_zitadel-bootstrap/_data"}
        seen=[]
        def command(args):
            seen.append(args)
            if args[:3]==["docker","volume","inspect"]:
                return json.dumps([{"Name":entry["name"],
                    "Mountpoint":entry["mountpoint"],
                    "Labels":{"com.docker.compose.project":"some-other-project"}}])
            raise AssertionError("mutation or unexpected command before owner check")
        with self.assertRaisesRegex(MOD.LocalRemovalBlocked,"changed identity"):
            MOD.verify_volume_before_remove(FakeBackup(),entry,command)
        self.assertEqual(len(seen),1)

    def test_foreign_volume_consumer_blocks_before_rm(self):
        entry={"name":"portico-infinity_zitadel-bootstrap",
               "mountpoint":"/var/lib/docker/volumes/portico-infinity_zitadel-bootstrap/_data"}
        def command(args):
            if args[:3]==["docker","volume","inspect"]:
                return json.dumps([{"Name":entry["name"],
                    "Mountpoint":entry["mountpoint"],
                    "Labels":{"com.docker.compose.project":"portico-infinity"}}])
            if args[:2]==["docker","ps"]:
                return "f"*64+"\n"
            raise AssertionError("unexpected Docker mutation")
        with self.assertRaisesRegex(MOD.LocalRemovalBlocked,"still mounted"):
            MOD.verify_volume_before_remove(FakeBackup(),entry,command)

    def test_conflicting_intent_cannot_overwrite_prior_evidence(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            p=MOD.write_intent(root,"synthetic-only",{"test":True})
            self.assertEqual(p.stat().st_mode & 0o777,0o600)
            with self.assertRaisesRegex(MOD.LocalRemovalBlocked,"already exists"):
                MOD.write_intent(root,"synthetic-only",{"test":False})

    def test_local_controller_removal_blocks_installed_host_worker(self):
        with tempfile.TemporaryDirectory() as directory:
            worker=Path(directory)/"portico-filecopy-worker"
            worker.write_bytes(b"synthetic executable must not become orphaned")
            with self.assertRaisesRegex(MOD.LocalRemovalBlocked,"worker remains installed"):
                MOD.require_host_worker_retired(worker)
            worker.unlink()
            self.assertIsNone(MOD.require_host_worker_retired(worker))

    def test_no_unrestricted_host_deletion_or_external_teardown(self):
        txt=SOURCE.read_text(encoding="utf-8")
        for forbidden in ("rm -rf /opt","wg-quick","ufw delete","docker system prune",
                          "docker compose down -v","shutil.rmtree(Path('/opt'))"):
            self.assertNotIn(forbidden,txt)


if __name__=="__main__":
    unittest.main()
