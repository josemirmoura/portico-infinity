"""Regression tests for read-only Infinity Compose systemd/SQLite inventory."""
import hashlib
import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "infinity-compose-inventory.py"
spec = importlib.util.spec_from_file_location("compose_inventory", SCRIPT)
inventory = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inventory)

ONE = "vps-agent-compose-" + "a" * 32
TWO = "vps-agent-compose-" + "b" * 32
THREE = "vps-agent-compose-" + "c" * 32


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.db = Path(self.temp.name) / "state.db"
        with sqlite3.connect(self.db) as connection:
            connection.execute("""
                CREATE TABLE jobs (
                    job_id TEXT PRIMARY KEY,
                    tool TEXT, state TEXT, unit_name TEXT,
                    exit_code INTEGER, created_at INTEGER
                )
            """)
            connection.execute("""
                CREATE TABLE compose_unit_cleanup (
                    job_id TEXT PRIMARY KEY, unit_name TEXT, status TEXT
                )
            """)

    def job(self, identity=ONE, state="done", exit_code=0, unit=None, tool="compose.up", order=1, receipt="done"):
        if unit is None:
            unit = identity + ".service"
        with sqlite3.connect(self.db) as connection:
            connection.execute(
                "INSERT INTO jobs(job_id,tool,state,unit_name,exit_code,created_at)"
                " VALUES(?,?,?,?,?,?)",
                (identity, tool, state, unit, exit_code, order),
            )
            if receipt:
                connection.execute(
                    "INSERT INTO compose_unit_cleanup(job_id,unit_name,status) VALUES(?,?,?)",
                    (identity, unit, receipt),
                )

    @staticmethod
    def fake_systemctl(*args):
        if args[0] == "list-units":
            return ""
        if args[0] == "show":
            return "LoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\n"
        raise AssertionError("unexpected command " + repr(args))

    def test_terminal_receipt_done_and_absent_unit_do_not_modify_db(self):
        self.job()
        before = hashlib.sha256(self.db.read_bytes()).hexdigest()
        with patch.object(inventory, "systemctl", side_effect=self.fake_systemctl) as calls:
            result = inventory.inspect(self.db, 100)
        self.assertEqual(result["result"], "NO_BLOCKERS_IN_SCANNED_ROWS")
        self.assertEqual(result["counts"]["ledger_terminal_proven"], 1)
        self.assertEqual(result["counts"]["receipt_done"], 1)
        self.assertEqual(result["counts"]["unit_absent"], 1)
        self.assertEqual(before, hashlib.sha256(self.db.read_bytes()).hexdigest())
        self.assertTrue(all(x.args[0] in ("show", "list-units") for x in calls.call_args_list))

    def test_missing_unit_never_proves_an_old_running_job_success(self):
        self.job(state="running", exit_code=None, receipt=None)
        with patch.object(inventory, "systemctl", side_effect=self.fake_systemctl):
            result = inventory.inspect(self.db, 100)
        self.assertEqual(result["result"], "REVIEW_REQUIRED")
        self.assertEqual(result["counts"]["ledger_active"], 1)
        self.assertEqual(result["counts"]["receipt_missing"], 1)
        self.assertEqual(result["counts"]["unit_absent"], 1)

    def test_done_receipt_with_loaded_unit_requires_review(self):
        self.job()
        def systemctl(*args):
            if args[0] == "list-units":
                return ONE + ".service loaded active exited description\n"
            return (
                "Id=" + ONE + ".service\n"
                "LoadState=loaded\nActiveState=active\nSubState=exited\nResult=success\n"
            )
        with patch.object(inventory, "systemctl", side_effect=systemctl):
            result = inventory.inspect(self.db, 100)
        self.assertEqual(result["result"], "REVIEW_REQUIRED")
        self.assertEqual(result["counts"]["receipt_done_unit_not_absent"], 1)

    def test_foreign_unit_identity_never_queried_with_systemctl_show(self):
        self.job(unit="bad.service")
        with patch.object(inventory, "systemctl", side_effect=self.fake_systemctl) as calls:
            result = inventory.inspect(self.db, 100)
        self.assertEqual(result["counts"]["invalid_unit"], 1)
        self.assertEqual(result["result"], "REVIEW_REQUIRED")
        self.assertFalse(any(x.args[0][0] == "show" for x in calls.call_args_list))

    def test_orphan_loaded_unit_flagged_for_review(self):
        self.job()
        def systemctl(*args):
            if args[0] == "list-units":
                return THREE + ".service loaded active exited test\n"
            return self.fake_systemctl(*args)
        with patch.object(inventory, "systemctl", side_effect=systemctl):
            result = inventory.inspect(self.db, 100)
        self.assertEqual(result["counts"]["orphan_loaded_without_paged_ledger"], 1)
        self.assertEqual(result["result"], "REVIEW_REQUIRED")

    def test_bounded_scan_is_explicitly_partial(self):
        self.job(identity=ONE, order=1)
        self.job(identity=TWO, order=2)
        with patch.object(inventory, "systemctl", side_effect=self.fake_systemctl):
            result = inventory.inspect(self.db, 1)
        self.assertEqual(result["jobs_inspected"], 1)
        self.assertTrue(result["scan_truncated"])
        self.assertEqual(result["result"], "REVIEW_REQUIRED")

    def test_legacy_state_without_receipts_is_supported_read_only(self):
        self.job(receipt=None)
        with sqlite3.connect(self.db) as db:
            db.execute("DROP TABLE compose_unit_cleanup")
        with patch.object(inventory, "systemctl", side_effect=self.fake_systemctl):
            result = inventory.inspect(self.db, 100)
        self.assertEqual(result["counts"]["receipt_missing"], 1)
        self.assertEqual(result["counts"]["ledger_terminal_proven"], 1)

    def test_db_symlink_denied(self):
        alias = Path(self.temp.name) / "alias.db"
        alias.symlink_to(self.db)
        with self.assertRaises(RuntimeError):
            inventory.inventory_rows(alias, 100)


if __name__ == "__main__":
    unittest.main()
