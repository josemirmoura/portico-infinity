"""Focused regression checks for operator-only Compose retention. No systemd calls."""
from __future__ import annotations

import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import time
import unittest
from unittest import mock
import json
import os

SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "collect-infinity-compose-units.py"
spec = importlib.util.spec_from_file_location("compose_retention", SCRIPT)
ret = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ret)


class ComposeRetentionTests(unittest.TestCase):
    def job(self, **changes):
        job = {
            "job_id": "vps-agent-compose-" + "a" * 32,
            "unit_name": "vps-agent-compose-" + "a" * 32 + ".service",
            "tool": "compose.up", "resource": "/opt/test", "state": "done",
            "exit_code": 0,
        }
        job.update(changes)
        return job

    def unit(self, **changes):
        unit = {
            "Id": "vps-agent-compose-" + "a" * 32 + ".service",
            "Description": ret.DESCRIPTION,
            "WorkingDirectory": "/opt/test",
            "LoadState": "loaded",
            "ActiveState": "active", "SubState": "exited",
            "Result": "success", "ExecMainStatus": "0",
            "Type": "oneshot", "Transient": "yes",
        }
        unit.update(changes)
        return unit

    def test_only_matching_terminal_owned_unit_can_collect(self):
        self.assertTrue(ret.unit_is_owned_and_finished(self.job(), self.unit()))
        bad_cases = [
            (self.job(state="running"), self.unit()),
            (self.job(job_id="../service"), self.unit()),
            (self.job(tool="shell.exec"), self.unit()),
            (self.job(exit_code=None), self.unit()),
            (self.job(), self.unit(Description="Different app")),
            (self.job(), self.unit(WorkingDirectory="/opt/other-project")),
            (self.job(), self.unit(Transient="no")),
            (self.job(), self.unit(ActiveState="activating", SubState="start")),
            (self.job(), self.unit(ActiveState="active", SubState="running")),
            (self.job(), self.unit(LoadState="not-found")),
            (self.job(), self.unit(ExecMainStatus="1")),
            (self.job(), self.unit(Result="exit-code")),
        ]
        for job, unit in bad_cases:
            with self.subTest(job=job, unit=unit):
                self.assertFalse(ret.unit_is_owned_and_finished(job, unit))

    def test_failed_must_have_matching_nonzero_exit(self):
        self.assertTrue(ret.unit_is_owned_and_finished(
            self.job(state="failed", exit_code=7),
            self.unit(ActiveState="failed", SubState="failed",
                      Result="exit-code", ExecMainStatus="7"),
        ))
        self.assertFalse(ret.unit_is_owned_and_finished(
            self.job(state="failed", exit_code=7),
            self.unit(ActiveState="failed", SubState="failed",
                      Result="success", ExecMainStatus="7"),
        ))

    def test_unit_status_parser_bounded(self):
        sample = "Id=unit.service\nActiveState=inactive\nResult=success\n"
        self.assertEqual(ret.parse_unit(sample)["ActiveState"], "inactive")

    def test_sql_query_selects_only_expired_terminal_compose_jobs(self):
        with tempfile.TemporaryDirectory() as d:
            dbpath = Path(d) / "state.db"
            db = sqlite3.connect(dbpath)
            db.execute("""CREATE TABLE jobs (
              job_id TEXT, tool TEXT, resource TEXT, unit_name TEXT,
              state TEXT, exit_code INTEGER, updated_at INTEGER
            )""")
            now = time.time_ns()
            one_day = 86400 * 1_000_000_000
            items = [
                ("old-done", "compose.up", "done", 0, now - 9 * one_day),
                ("young", "compose.up", "done", 0, now - one_day),
                ("old-running", "compose.up", "running", None, now - 9 * one_day),
                ("old-shell", "shell.exec", "done", 0, now - 9 * one_day),
            ]
            db.executemany(
                "INSERT INTO jobs VALUES (?,?,?,?,?,?,?)",
                [(j, tool, "/opt/test", j + ".service", state, ec, stamp)
                 for j, tool, state, ec, stamp in items],
            )
            db.commit()
            db.close()
            rows = ret.candidates(dbpath, 7, 25)
            self.assertEqual([x["job_id"] for x in rows], ["old-done"])

    def test_sql_snapshot_must_remain_terminal_and_identical(self):
        with tempfile.TemporaryDirectory() as d:
            dbpath = Path(d) / "state.db"
            db = sqlite3.connect(dbpath)
            db.execute("""CREATE TABLE jobs (
                job_id TEXT, tool TEXT, resource TEXT, unit_name TEXT,
                state TEXT, exit_code INTEGER, updated_at INTEGER
            )""")
            original = self.job()
            original["updated_at"] = time.time_ns() - 10 * 86400 * 1_000_000_000
            db.execute(
                "INSERT INTO jobs VALUES (?,?,?,?,?,?,?)",
                tuple(original[key] for key in
                      ("job_id", "tool", "resource", "unit_name", "state", "exit_code", "updated_at")),
            )
            db.commit()
            snapshot = ret.persisted_job(dbpath, original["job_id"])
            self.assertEqual(snapshot, original)
            db.execute("UPDATE jobs SET state=?, updated_at=? WHERE job_id=?",
                       ("running", time.time_ns(), original["job_id"]))
            db.commit()
            self.assertNotEqual(ret.persisted_job(dbpath, original["job_id"]), snapshot)
            db.close()
            with self.assertRaises(ret.UnsafeJob):
                ret.persisted_job(dbpath, "../other-service")

    def test_archive_evidence_is_bounded_private_and_dir_synced(self):
        with tempfile.TemporaryDirectory() as d:
            folder = Path(d)
            job = self.job()
            job["updated_at"] = time.time_ns()
            log = "safe disposable CI journal event\\n" * 4000
            raw_fsync = os.fsync
            with mock.patch.object(ret, "run", return_value=log) as called, \
                 mock.patch.object(ret.os, "fsync", wraps=raw_fsync) as fsync:
                ret.archive_job(folder, job, self.unit())
            called.assert_called_once_with(
                "journalctl", "--no-pager", "--output=short-iso",
                "--unit", job["unit_name"], "-n", "150")
            self.assertEqual(fsync.call_count, 3,
                             "two files and their containing directory must be fsynced")
            manifest = folder / (job["job_id"] + ".json")
            archive = folder / (job["job_id"] + ".log")
            self.assertTrue(manifest.is_file())
            self.assertTrue(archive.is_file())
            self.assertLessEqual(archive.stat().st_size, ret.LOG_LIMIT)
            self.assertEqual(archive.stat().st_mode & 0o777, 0o600)
            self.assertEqual(manifest.stat().st_mode & 0o777, 0o600)
            data = json.loads(manifest.read_text())
            self.assertEqual(data["exit_code"], 0)
            self.assertEqual(data["state"], "done")
            self.assertEqual(data["log_bytes"], archive.stat().st_size)
            with self.assertRaises(ret.UnsafeJob):
                ret.archive_job(folder, job, self.unit())

    def test_archive_outside_opt_and_root_checks(self):
        with self.assertRaises(ret.UnsafeJob):
            ret.protected_archive(Path("/opt/illegal-retention-store"))


if __name__ == "__main__":
    unittest.main()
