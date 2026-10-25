#!/usr/bin/env python3
"""Read-only operator inventory of Infinity Compose job units and cleanup receipts.

Never changes SQLite, systemd, containers, permissions, audit history or jobs.
Only systemd unit names matching the Infinity Compose prefix may be probed.
An absent unit is NOT proof that the operation succeeded. This is a diagnostic,
not a repair/retirement command; REVIEW does not automatically mean a defect.
"""
from __future__ import annotations

import argparse
from collections import Counter
import json
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
from urllib.parse import quote

DEFAULT_DB = Path("/srv/portico-infinity/state/state.db")
UNIT = re.compile(r"vps-agent-compose-[0-9a-f]{32}\.service\Z")
TOOLS = ("compose.up", "compose.down", "compose.pull")
ACTIVE = frozenset({
    "starting", "running", "active", "activating",
    "reloading", "deactivating", "cancelling",
})


def systemctl(*args: str) -> str:
    result = subprocess.run(
        ["systemctl", *args], capture_output=True, text=True,
        timeout=12, check=False,
    )
    if result.returncode != 0:
        raise RuntimeError("systemctl read-only query failed")
    return result.stdout


def fields(raw: str) -> dict[str, str]:
    return dict(line.split("=", 1) for line in raw.splitlines() if "=" in line)


def unit_classification(name: str) -> str:
    if not UNIT.fullmatch(name):
        return "invalid_unit"
    try:
        state = fields(systemctl(
            "show", name, "--no-pager",
            "--property=Id,LoadState,ActiveState,SubState,Result",
        ))
    except (RuntimeError, subprocess.TimeoutExpired):
        return "unit_query_failed"
    if state.get("LoadState") == "not-found" and state.get("ActiveState") == "inactive":
        return "unit_absent"
    if state.get("LoadState") == "loaded" and state.get("Id") == name:
        active, sub = state.get("ActiveState"), state.get("SubState")
        if (active, sub) in (("active", "exited"), ("inactive", "dead"), ("failed", "failed")):
            return "unit_retained_terminal"
        if active in ("active", "activating", "deactivating", "reloading"):
            return "unit_active_or_transition"
    return "unit_ambiguous"


def inventory_rows(db_path: Path, limit: int) -> tuple[list[dict], bool]:
    if db_path.is_symlink() or not db_path.is_file():
        raise RuntimeError("state database missing or unsafe symlink")
    uri = "file:" + quote(str(db_path.absolute()), safe="/") + "?mode=ro"
    # No journal mode changes, ALTER statements or checkpoints.
    with sqlite3.connect(uri, uri=True, timeout=5) as db:
        db.execute("PRAGMA query_only=ON")
        tables = {x[0] for x in db.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        )}
        if "jobs" not in tables:
            raise RuntimeError("jobs ledger table not found")
        receipt_join = (
            "LEFT JOIN compose_unit_cleanup c ON c.job_id=j.job_id"
            if "compose_unit_cleanup" in tables else ""
        )
        receipt_cols = (
            "c.status AS receipt_status,c.unit_name AS receipt_unit"
            if receipt_join else "NULL AS receipt_status,NULL AS receipt_unit"
        )
        query = f"""
            SELECT j.job_id,j.tool,j.state,j.unit_name,j.exit_code,
                   {receipt_cols}
            FROM jobs j {receipt_join}
            WHERE j.tool IN ('compose.up','compose.down','compose.pull')
            ORDER BY j.created_at,j.job_id LIMIT ?
        """
        db.row_factory = sqlite3.Row
        rows = [dict(row) for row in db.execute(query, (limit + 1,))]
    return rows[:limit], len(rows) > limit


def loaded_orphans(ledger_units: set[str]) -> tuple[int, int]:
    raw = systemctl(
        "list-units", "--all", "--type=service", "--no-legend",
        "--plain", "--full", "--no-pager",
    )
    seen: set[str] = set()
    for line in raw.splitlines():
        for piece in line.split()[:2]:
            if UNIT.fullmatch(piece):
                seen.add(piece)
    return len(seen - ledger_units), len(seen)


def inspect(db_path: Path, limit: int) -> dict:
    rows, truncated = inventory_rows(db_path, limit)
    counts: Counter[str] = Counter()
    known: set[str] = set()
    for row in rows:
        name = row["unit_name"] or ""
        identity = row["job_id"] or ""
        if name != identity + ".service" or not UNIT.fullmatch(name):
            counts["invalid_unit"] += 1
            continue
        known.add(name)
        if row["state"] in ACTIVE:
            counts["ledger_active"] += 1
        elif row["state"] == "reconcile_required":
            counts["ledger_reconcile_required"] += 1
        elif row["state"] in ("done", "failed") and row["exit_code"] is not None:
            counts["ledger_terminal_proven"] += 1
        else:
            counts["ledger_other_or_unproven"] += 1
        result = unit_classification(name)
        counts[result] += 1
        receipt = row["receipt_status"]
        if receipt is None:
            counts["receipt_missing"] += 1
        elif row["receipt_unit"] != name:
            counts["receipt_mismatch"] += 1
        elif receipt == "done":
            counts["receipt_done"] += 1
            if result != "unit_absent":
                counts["receipt_done_unit_not_absent"] += 1
        elif receipt == "pending":
            counts["receipt_pending"] += 1
        else:
            counts["receipt_invalid"] += 1
    orphan_count, loaded_count = loaded_orphans(known)
    counts["orphan_loaded_without_paged_ledger"] = orphan_count
    # A truncated scan cannot establish that a loaded unit is truly orphaned.
    review = truncated or any(counts[x] for x in (
        "invalid_unit", "ledger_active", "ledger_reconcile_required",
        "ledger_other_or_unproven", "unit_active_or_transition",
        "unit_ambiguous", "unit_query_failed", "receipt_pending",
        "receipt_mismatch", "receipt_invalid", "receipt_done_unit_not_absent",
        "orphan_loaded_without_paged_ledger",
    ))
    return {
        "mode": "read_only",
        "jobs_inspected": len(rows),
        "scan_truncated": truncated,
        "loaded_infinity_units_seen": loaded_count,
        "counts": dict(sorted(counts.items())),
        "result": "REVIEW_REQUIRED" if review else "NO_BLOCKERS_IN_SCANNED_ROWS",
        "interpretation": "Absent systemd units do not establish the original command result",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-db", type=Path, default=DEFAULT_DB)
    parser.add_argument("--limit", type=int, default=100)
    args = parser.parse_args()
    if not 1 <= args.limit <= 1000:
        parser.error("--limit must be in 1..1000")
    try:
        print(json.dumps(inspect(args.state_db, args.limit), sort_keys=True))
        return 0
    except (RuntimeError, OSError, sqlite3.Error, subprocess.TimeoutExpired) as exc:
        print("COMPOSE_INVENTORY_UNAVAILABLE:", str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
