#!/usr/bin/env python3
"""Read-only, root-console audit of the PRE-EXISTING Infinity shell account.

The Infinity installer requires an existing non-root --run-as account. It does
not create that principal or own its lifecycle. This tool NEVER modifies a
host account, supplementary groups, home, password, running processes or data.
"""
from __future__ import annotations
import argparse
import grp
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import stat
import sys

CONTROLLER=Path("/srv/portico-infinity")
POLICY=CONTROLLER/"config/policy.yaml"
SCRIPT=CONTROLLER/"scripts/infinity-executor-account-audit.py"
LIFECYCLE=CONTROLLER/"scripts/infinity-lifecycle.py"
USER=re.compile(r"[a-z_][a-z0-9_-]{0,31}\$?\Z")
ADMIN=frozenset({"sudo","wheel","docker","lxd","libvirt","adm","systemd-journal","shadow","root"})


class AccountAuditBlocked(RuntimeError):
    pass


def extract_run_as(policy: str) -> str:
    """Parse only simple unquoted YAML shell.run_as and reject any ambiguity.

    Deliberately NOT a general YAML parser: anchors, tags, multiline values and
    duplicates are untrusted and must fail closed, not execute as Python.
    """
    section=None
    run_as=[]
    shell_sections=0
    for raw in policy.splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        if "\t" in raw or len(raw)>4096:
            raise AccountAuditBlocked("policy contains ambiguous indentation or oversized row")
        if not raw.startswith(" "):
            matched=re.fullmatch(r"([a-zA-Z_][a-zA-Z_0-9-]*):(?:\s*(?:#.*)?)?",raw)
            if matched is None:
                raise AccountAuditBlocked("unsupported top-level policy syntax")
            section=matched.group(1)
            if section=="shell":shell_sections+=1
            continue
        if section=="shell" and re.match(r"^  run_as\s*:",raw):
            matched=re.fullmatch(r"  run_as: ([a-z_][a-z0-9_-]{0,31}\$?)\s*",raw)
            if matched is None:
                raise AccountAuditBlocked("ambiguous shell.run_as syntax")
            run_as.append(matched.group(1))
    if shell_sections!=1 or len(run_as)!=1 or USER.fullmatch(run_as[0]) is None:
        raise AccountAuditBlocked("missing or ambiguous shell.run_as")
    return run_as[0]


def ensure_root_controller():
    if os.geteuid()!=0 or Path.cwd().resolve()!=CONTROLLER:
        raise AccountAuditBlocked("root operator in /srv/portico-infinity required")
    for path in (Path("/"),Path("/srv"),CONTROLLER,CONTROLLER/"scripts",
                 SCRIPT,LIFECYCLE,CONTROLLER/"config",POLICY):
        info=path.lstat()
        if (stat.S_ISLNK(info.st_mode) or info.st_uid!=0 or info.st_mode & 0o022
                or not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode))):
            raise AccountAuditBlocked("untrusted controller or policy path")
    spec=importlib.util.spec_from_file_location("infinity_lifecycle",LIFECYCLE)
    if spec is None or spec.loader is None:
        raise AccountAuditBlocked("trusted lifecycle unavailable")
    life=importlib.util.module_from_spec(spec)
    spec.loader.exec_module(life)
    life.verify_operator()
    return life


def account_record(user: str, *, lookup=pwd.getpwnam,
                   memberships=os.getgrouplist, group_lookup=grp.getgrgid) -> dict:
    if USER.fullmatch(user) is None:
        raise AccountAuditBlocked("invalid account identifier")
    try:
        principal=lookup(user)
    except KeyError as exc:
        raise AccountAuditBlocked("configured executor account is absent") from exc
    if principal.pw_name!=user or principal.pw_uid==0 or principal.pw_gid==0:
        raise AccountAuditBlocked("configured executor is root or ambiguous")
    groups=set(memberships(user,principal.pw_gid))
    names={group_lookup(g).gr_name for g in groups}
    return {"username":user,"uid":principal.pw_uid,"primary_gid":principal.pw_gid,
            "groups":sorted(names),
            "administrative_membership":sorted(ADMIN & names),
            "account_created_by_infinity":False,
            "safe_automatic_account_deletion":False,
            "reason":"installer accepts pre-existing --run-as; account may be shared"}


def process_references(uid: int, proc: Path=Path("/proc")) -> dict:
    if not proc.is_dir():
        raise AccountAuditBlocked("cannot inspect host process tree")
    seen=0
    count=0
    for item in proc.iterdir():
        if not item.name.isdecimal():continue
        seen+=1
        if seen>131072:
            raise AccountAuditBlocked("process table is too large for bounded audit")
        try:
            with (item/"status").open("rt",encoding="ascii") as handle:
                for line in handle:
                    if line.startswith("Uid:"):
                        nums=[int(x) for x in line.split()[1:5]]
                        if len(nums)!=4:
                            raise AccountAuditBlocked("malformed process identity")
                        if uid in nums:count+=1
                        break
                else:
                    raise AccountAuditBlocked("process without visible UID")
        except FileNotFoundError:
            # PID naturally exited during the read. Do not infer inactivity.
            continue
        except (PermissionError,OSError) as exc:
            raise AccountAuditBlocked("cannot safely inspect host process ownership") from exc
    return {"processes_seen":seen,"executor_uid_processes_seen":count,
            "race_note":"live process inventory is advisory and cannot establish exclusive ownership"}


def audit(*, policy: Path=POLICY, trusted=False) -> dict:
    if not trusted:
        ensure_root_controller()
    info=policy.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_uid!=0 or
            info.st_mode & 0o022 or info.st_size>1<<20):
        raise AccountAuditBlocked("unsafe policy metadata")
    handle=os.open(policy,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC)
    try:
        data=os.read(handle,(1<<20)+1)
    finally:
        os.close(handle)
    if len(data)>1<<20:
        raise AccountAuditBlocked("policy too large")
    try:
        user=extract_run_as(data.decode("utf-8"))
    except UnicodeError as exc:
        raise AccountAuditBlocked("policy not valid UTF-8") from exc
    account=account_record(user)
    account["live_process_observation"]=process_references(account["uid"])
    account["recommended_uninstall_action"]="PRESERVE account and all home/workspace files"
    return account


def main():
    parser=argparse.ArgumentParser(description="Read-only Infinity executor account inventory (never userdel)")
    parser.add_argument("--inspect",action="store_true",required=True)
    args=parser.parse_args()
    try:
        result=audit()
        print(json.dumps(result,indent=2,sort_keys=True))
        if result["administrative_membership"]:
            print("INFINITY ACCOUNT AUDIT WARNING: configured account has administrative groups",
                  file=sys.stderr)
        return 0
    except (AccountAuditBlocked,ValueError,OSError,RuntimeError) as exc:
        print("INFINITY ACCOUNT AUDIT BLOCKED: "+str(exc),file=sys.stderr)
        return 2


if __name__=="__main__":
    raise SystemExit(main())
