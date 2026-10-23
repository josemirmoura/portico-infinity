"""Non-destructive host worker retirement negative tests."""
import importlib.util
import hashlib
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SOURCE=Path(__file__).resolve().parents[2]/"scripts/infinity-host-worker-lifecycle.py"
SPEC=importlib.util.spec_from_file_location("infinity_host_worker_lifecycle",SOURCE)
MOD=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)


class FakeLife:
    def __init__(self):
        self.active=False
        self.uncertain=False
        self.units=""
        self.unit_files=""
        self.calls=[]
    def ensure_no_active_workers(self,command):
        if self.active:raise RuntimeError("active worker")
    def ensure_no_unreconciled_filecopy_checkpoints(self):
        if self.uncertain:raise RuntimeError("uncertain job")
    def run(self,args):
        self.calls.append(tuple(args))
        if args[:2]==["systemctl","list-units"]:
            return self.units
        if args[:2]==["systemctl","list-unit-files"]:
            return self.unit_files
        raise AssertionError("unrecognized unit query")


class WorkerLifecycleTests(unittest.TestCase):
    def test_active_uncertain_or_loaded_unit_blocks_without_mutation(self):
        for state in ("active","uncertain","units","unit_files"):
            with self.subTest(state=state):
                fake=FakeLife()
                if state=="active":fake.active=True
                elif state=="uncertain":fake.uncertain=True
                elif state=="units":fake.units="vps-agent-filecopy-"+("a"*32)+".service loaded inactive dead\n"
                else:fake.unit_files="vps-agent-filecopy-"+("a"*32)+".service enabled\n"
                with self.assertRaises((RuntimeError,MOD.WorkerBlocked)):
                    MOD.ensure_quiescent(fake)
                self.assertFalse(any(x[0] in ("docker","rm","unlink") for x in fake.calls))

    def test_process_still_executing_same_inode_blocks(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            real=root/"binary"
            real.write_bytes(b"only a synthetic process fixture")
            proc=root/"proc"/"123"
            proc.mkdir(parents=True)
            (proc/"exe").symlink_to(real)
            info=real.stat()
            with self.assertRaisesRegex(MOD.WorkerBlocked,"still running"):
                MOD.ensure_no_running_binary({"device":info.st_dev,"inode":info.st_ino},
                                             proc=root/"proc")

    def test_bad_sha_or_confirmation_cannot_unlink(self):
        with tempfile.TemporaryDirectory() as temp:
            folder=Path(temp)/"portico-infinity"
            folder.mkdir()
            binary=folder/"portico-filecopy-worker"
            binary.write_bytes(b"only synthetic binary")
            digest=hashlib.sha256(binary.read_bytes()).hexdigest()
            metadata={"sha256":digest,"size":binary.stat().st_size,
                      "device":binary.stat().st_dev,"inode":binary.stat().st_ino}
            fake=FakeLife()
            with (mock.patch.object(MOD,"WORKER_DIR",folder),
                  mock.patch.object(MOD,"WORKER",binary),
                  mock.patch.object(MOD,"binary",return_value=metadata),
                  mock.patch.object(MOD,"ensure_no_running_binary",return_value=None)):
                for sha,confirm in ((digest,"BAD"),("b"*64,"REMOVE-INFINITY-HOST-WORKER-"+digest[:20])):
                    with self.assertRaisesRegex(MOD.WorkerBlocked,"confirmation"):
                        MOD.remove(fake,sha,confirm)
                    self.assertTrue(binary.exists())
                self.assertTrue(MOD.remove(fake,digest,"REMOVE-INFINITY-HOST-WORKER-"+digest[:20]))
                self.assertFalse(binary.exists())

    def test_absent_binary_is_idempotent_and_does_not_mutate(self):
        fake=FakeLife()
        with mock.patch.object(MOD,"binary",return_value=None):
            self.assertFalse(MOD.remove(fake,"",""))
        self.assertEqual(len(fake.calls),2)

    def test_source_has_no_global_destructive_commands(self):
        script=SOURCE.read_text()
        for forbidden in ("rm -rf /opt","userdel","groupdel","wg-quick down",
                          "systemctl stop traefik","docker volume rm",
                          "shutil.rmtree","sudo apt"):
            self.assertNotIn(forbidden,script)


if __name__=="__main__":
    unittest.main()
