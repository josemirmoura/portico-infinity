"""Owner-only account audit tests; no real /etc/passwd mutations."""
import importlib.util
import json
from pathlib import Path
import pwd
import grp
import os
import tempfile
import unittest
from unittest import mock

FILE=Path(__file__).resolve().parents[2]/"scripts/infinity-executor-account-audit.py"
SPEC=importlib.util.spec_from_file_location("infinity_executor_account_audit",FILE)
MOD=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)


class AccountAuditTests(unittest.TestCase):
    def test_realistic_policy_extracts_only_shell_identity(self):
        txt="""version: 1
mode: scoped
filesystem:
  read:
    - /opt
shell:
  enabled: true
  run_as: synthetic-exec
  max_tasks: 256
users:
  manage: []
"""
        self.assertEqual(MOD.extract_run_as(txt),"synthetic-exec")

    def test_ambiguous_duplicate_and_synthetic_yaml_denied(self):
        bad=(
            "shell:\n  run_as: root\nshell:\n  run_as: bot\n",
            "shell:\n  run_as: bot\n  run_as: attacker\n",
            "shell:\n  run_as: bot # override\n",
            "shell: {run_as: bot}\n",
            "shell:\n  run_as: !!python/object/apply:os.system []\n",
            "shell:\n  run_as: \"bot\"\n",
            "shell:\n  run_as: bot\n  run_as: ../root\n",
            "shell:\n  run_as: bot\n\tbad: true\n",
            "mode: scoped\n",
        )
        for doc in bad:
            with self.subTest(doc=doc[:40]),self.assertRaises(MOD.AccountAuditBlocked):
                MOD.extract_run_as(doc)

    def test_identity_must_be_nonroot_and_existing(self):
        fake=pwd.struct_passwd(("synthetic-exec","x",54321,54321,"","/nonexistent","/usr/sbin/nologin"))
        fakegrp=grp.struct_group(("synthetic-exec","x",54321,[]))
        report=MOD.account_record("synthetic-exec",lookup=lambda name:fake,
                                  memberships=lambda user,gid:[gid],
                                  group_lookup=lambda gid:fakegrp)
        self.assertEqual(report["uid"],54321)
        self.assertFalse(report["account_created_by_infinity"])
        self.assertFalse(report["safe_automatic_account_deletion"])
        root=pwd.struct_passwd(("root","x",0,0,"","/root","/bin/bash"))
        with self.assertRaises(MOD.AccountAuditBlocked):
            MOD.account_record("root",lookup=lambda name:root)
        with self.assertRaises(MOD.AccountAuditBlocked):
            MOD.account_record("absent",lookup=lambda n:(_ for _ in ()).throw(KeyError(n)))
        with self.assertRaises(MOD.AccountAuditBlocked):
            MOD.account_record("../etc/shadow")

    def test_privileged_group_is_reported_not_deleted(self):
        user=pwd.struct_passwd(("bot","x",1001,1001,"","/nonexistent","/bin/sh"))
        def gr(gid):
            return grp.struct_group(("docker" if gid==999 else "bot","x",gid,[]))
        report=MOD.account_record("bot",lookup=lambda _:user,
                                  memberships=lambda _u,_gid:[1001,999],group_lookup=gr)
        self.assertEqual(report["administrative_membership"],["docker"])
        self.assertFalse(report["safe_automatic_account_deletion"])

    def test_bounded_proc_counts_without_disclosing_process_arguments(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            for pid,uid in [(123,1001),(456,2000),(789,1001)]:
                target=root/str(pid)
                target.mkdir()
                (target/"status").write_text(f"Name:\tsynthetic\nUid:\t{uid} {uid} {uid} {uid}\n")
            result=MOD.process_references(1001,root)
            self.assertEqual(result["executor_uid_processes_seen"],2)
            self.assertEqual(result["processes_seen"],3)
            self.assertNotIn("cmdline",str(result))

    def test_missing_or_untrusted_policy_refused(self):
        with tempfile.TemporaryDirectory() as d:
            path=Path(d)/"policy.yaml"
            path.write_text("shell:\n  run_as: bot\n")
            with mock.patch.object(MOD,"ensure_root_controller",return_value=None),\
                 mock.patch.object(MOD,"account_record",return_value={"uid":1001}),\
                 mock.patch.object(MOD,"process_references",return_value={"processes_seen":0}):
                with mock.patch.object(MOD,"extract_run_as",wraps=MOD.extract_run_as) as parser:
                    if os.geteuid()==0:
                        self.assertEqual(MOD.audit(policy=path)["uid"],1001)
                        parser.assert_called_once()
                    else:
                        # Root-only real policy files must NOT become readable
                        # merely because a CI test runs under an unprivileged
                        # hosted runner. The separate sudo CI step proves the
                        # positive path with genuine root-owned metadata.
                        with self.assertRaisesRegex(MOD.AccountAuditBlocked,"unsafe policy"):
                            MOD.audit(policy=path)
                path.chmod(0o666)
                with self.assertRaises(MOD.AccountAuditBlocked):
                    MOD.audit(policy=path)
                path.chmod(0o600)
                path.unlink()
                path.symlink_to("/etc/passwd")
                with self.assertRaises((OSError,MOD.AccountAuditBlocked)):
                    MOD.audit(policy=path)

    def test_script_contains_no_account_mutating_actions(self):
        src=FILE.read_text()
        for forbidden in ("userdel","groupdel","usermod","useradd","passwd -d",
                          "pkill","killall","sudo user","rm -rf /home"):
            self.assertNotIn(forbidden,src.split("def main():")[1])


if __name__=="__main__":
    unittest.main()
