"""Non-destructive regression suite for the Infinity owner-only lifecycle.

No Docker or systemd commands reach the test host; all resources are synthetic.
"""
from __future__ import annotations

import importlib.util
import json
import os
import tempfile
from pathlib import Path
import unittest

SOURCE = Path(__file__).resolve().parents[2] / "scripts/infinity-lifecycle.py"
SPEC = importlib.util.spec_from_file_location("infinity_lifecycle", SOURCE)
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)

A, B, C, N = ("a" * 64, "b" * 64, "c" * 64, "d" * 64)


class FakeHost:
    def __init__(self):
        self.calls = []
        self.containers = {
            A: ["gateway", True, "/srv/portico-infinity", "portico-infinity"],
            B: ["broker", True, "/srv/portico-infinity", "portico-infinity"],
            C: ["zitadel-api", True, "/srv/portico-infinity", "portico-infinity"],
        }
        self.networks = {N: {
            "Id": N, "Name": "portico-infinity_default",
            "Labels": {"com.docker.compose.project": "portico-infinity"},
            "Containers": {key: {} for key in self.containers},
        }}
        self.volumes = {"portico-infinity_zitadel-postgres-data": {
            "Name": "portico-infinity_zitadel-postgres-data",
            "Labels": {"com.docker.compose.project": "portico-infinity"},
        }}
        self.workers_active = False
        self.worker_name = "vps-agent-filecopy-" + ("f" * 32) + ".service"
        self.worker_phase = "activating"
        self.restart_disabled = set()

    def command(self, args):
        args = tuple(args)
        self.calls.append(args)
        if args[0] == "systemctl":
            if not self.workers_active:
                return ""
            if not any(self.worker_name.startswith(part.split("*", 1)[0])
                       for part in args if part.endswith("*.service")):
                return ""
            states = next((v.partition("=")[2].split(",") for v in args
                           if v.startswith("--state=")), [])
            if self.worker_phase not in states:
                return ""
            return (self.worker_name + " loaded " +
                    self.worker_phase + " start running\n")
        if args[:2] == ("docker", "ps"):
            return "\n".join(self.containers) + "\n"
        if args[:2] == ("docker", "inspect"):
            ident = args[2]
            row = self.containers[ident]
            return json.dumps([{
                "Id": ident,
                "Config": {"Labels": {
                    "com.docker.compose.project": row[3],
                    "com.docker.compose.project.working_dir": row[2],
                    "com.docker.compose.service": row[0],
                }},
                "State": {"Running": row[1]},
            }])
        if args[:2] == ("docker", "network"):
            if args[2] == "ls":
                return "\n".join(self.networks) + "\n"
            if args[2] == "inspect":
                return json.dumps([self.networks[args[3]]])
            if args[2] == "rm":
                del self.networks[args[3]]
                return args[3]
        if args[:2] == ("docker", "volume"):
            if args[2] == "ls":
                return "\n".join(self.volumes) + "\n"
            if args[2] == "inspect":
                return json.dumps([self.volumes[args[3]]])
        if args[:2] == ("docker", "update"):
            self.restart_disabled.add(args[-1])
            return args[-1]
        if args[:2] == ("docker", "stop"):
            self.containers[args[-1]][1] = False
            return args[-1]
        if args[:2] == ("docker", "rm"):
            del self.containers[args[-1]]
            for network in self.networks.values():
                network["Containers"].pop(args[-1], None)
            return args[-1]
        raise AssertionError("unexpected external invocation: " + repr(args))

    def mutations(self):
        return [cmd for cmd in self.calls if cmd[:2] in {
            ("docker", "update"), ("docker", "stop"), ("docker", "rm")
        } or cmd[:3] == ("docker", "network", "rm")]


class InfinityLifecycleTests(unittest.TestCase):
    def new_filecopy_fixture(self, directory, phase="running", include_progress=True):
        state = Path(directory) / "filecopy-jobs"
        state.mkdir(mode=0o700)
        ident = "vps-agent-filecopy-" + ("a" * 32)
        manifest = {"job_id": ident, "subject": "synthetic-operator",
                    "source": "/opt/a.gguf", "destination": "/opt/b.gguf",
                    "scope_root": "/opt", "max_bytes": 536870912}
        request = state / (ident + ".request.json")
        request.write_text(json.dumps(manifest), encoding="utf-8")
        request.chmod(0o600)
        if include_progress:
            progress = {"job_id": ident, "phase": phase,
                        "bytes_done": 67108864, "bytes_total": 536870912}
            if phase == "done":
                progress.update({"bytes_done": 536870912,
                                 "sha256": "a" * 64})
            checkpoint = state / (ident + ".progress.json")
            checkpoint.write_text(json.dumps(progress), encoding="utf-8")
            checkpoint.chmod(0o600)
        return state, ident

    def test_host_reboot_vanished_unit_with_running_progress_blocks_lifecycle(self):
        for action in ("deactivate", "uninstall"):
            with self.subTest(action=action), tempfile.TemporaryDirectory() as d:
                state, ident = self.new_filecopy_fixture(d)
                host = FakeHost()  # systemctl sees NO transient unit after host reboot.
                with self.assertRaisesRegex(MOD.LifecycleError, "unreconciled filecopy job"):
                    MOD.change_runtime(action, host.command, filecopy_state=state)
                self.assertEqual(host.mutations(), [])

    def test_missing_worker_checkpoint_blocks_before_docker_stop(self):
        with tempfile.TemporaryDirectory() as d:
            state, _ = self.new_filecopy_fixture(d, include_progress=False)
            host = FakeHost()
            with self.assertRaisesRegex(MOD.LifecycleError, "missing manifest/progress"):
                MOD.change_runtime("deactivate", host.command, filecopy_state=state)
            self.assertEqual(host.mutations(), [])

    def test_uncertain_and_queued_checkpoints_block_even_if_systemd_is_empty(self):
        for phase in ("queued", "reconcile_required"):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as d:
                state, _ = self.new_filecopy_fixture(d, phase=phase)
                host = FakeHost()
                with self.assertRaises(MOD.LifecycleError):
                    MOD.change_runtime("uninstall", host.command, filecopy_state=state)
                self.assertEqual(host.mutations(), [])

    def test_persisted_terminal_jobs_do_not_block_safe_runtime_removal(self):
        for phase in ("done", "cancelled", "failed"):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as d:
                state, _ = self.new_filecopy_fixture(d, phase=phase)
                host = FakeHost()
                self.assertEqual(MOD.change_runtime("deactivate", host.command,
                                                    filecopy_state=state), (3, 0))
                self.assertEqual(len(host.restart_disabled), 3)

    def test_symlink_and_foreign_permissions_fail_closed(self):
        with tempfile.TemporaryDirectory() as d:
            state, ident = self.new_filecopy_fixture(d, phase="done")
            target = state / (ident + ".progress.json")
            target.chmod(0o666)
            with self.assertRaisesRegex(MOD.LifecycleError, "unsafe filecopy checkpoint"):
                MOD.ensure_no_unreconciled_filecopy_checkpoints(state)
            target.chmod(0o600)
            target.unlink()
            target.symlink_to("/etc/passwd")
            with self.assertRaisesRegex(MOD.LifecycleError, "unsafe filecopy checkpoint"):
                MOD.ensure_no_unreconciled_filecopy_checkpoints(state)

    def test_corrupt_or_mismatched_terminal_hash_blocks(self):
        with tempfile.TemporaryDirectory() as d:
            state, ident = self.new_filecopy_fixture(d, phase="done")
            target = state / (ident + ".progress.json")
            data = json.loads(target.read_text())
            data["sha256"] = "not-a-sha"
            target.write_text(json.dumps(data))
            with self.assertRaisesRegex(MOD.LifecycleError, "terminal filecopy integrity"):
                MOD.ensure_no_unreconciled_filecopy_checkpoints(state)

    def test_empty_fresh_worker_state_is_safe(self):
        with tempfile.TemporaryDirectory() as d:
            state = Path(d) / "filecopy-jobs"
            self.assertIsNone(MOD.ensure_no_unreconciled_filecopy_checkpoints(state))
            state.mkdir(mode=0o700)
            self.assertIsNone(MOD.ensure_no_unreconciled_filecopy_checkpoints(state))

    def test_read_only_inventory(self):
        host = FakeHost()
        containers, networks, volumes = MOD.inventory(host.command)
        self.assertEqual(len(containers), 3)
        self.assertEqual(len(networks), 1)
        self.assertEqual(len(volumes), 1)
        self.assertEqual(host.mutations(), [])

    def test_deactivation_preserves_runtime_and_private_data(self):
        host = FakeHost()
        count, networks_removed = MOD.change_runtime("deactivate", host.command)
        self.assertEqual((count, networks_removed), (3, 0))
        self.assertEqual(set(host.containers), {A, B, C})
        self.assertTrue(all(not row[1] for row in host.containers.values()))
        self.assertEqual(host.restart_disabled, {A, B, C})
        self.assertEqual(len(host.networks), 1)
        self.assertEqual(len(host.volumes), 1)

    def test_uninstall_removes_only_owned_runtime(self):
        host = FakeHost()
        count, networks_removed = MOD.change_runtime("uninstall", host.command)
        self.assertEqual((count, networks_removed), (3, 1))
        self.assertEqual(host.containers, {})
        self.assertEqual(host.networks, {})
        self.assertEqual(len(host.volumes), 1)
        self.assertFalse(any(cmd[:2] == ("docker", "volume") and cmd[2] == "rm"
                             for cmd in host.calls))

    def test_foreign_container_aborts_before_stop(self):
        host = FakeHost()
        host.containers[B][2] = "/srv/not-ours"
        with self.assertRaises(MOD.LifecycleError):
            MOD.change_runtime("deactivate", host.command)
        self.assertEqual(host.mutations(), [])

    def test_unknown_service_aborts_before_stop(self):
        host = FakeHost()
        host.containers[A][0] = "third-party"
        with self.assertRaises(MOD.LifecycleError):
            MOD.change_runtime("uninstall", host.command)
        self.assertEqual(host.mutations(), [])

    def test_attached_foreign_container_prevents_network_removal(self):
        host = FakeHost()
        host.networks[N]["Containers"]["e" * 64] = {}
        with self.assertRaises(MOD.LifecycleError):
            MOD.change_runtime("uninstall", host.command)
        self.assertEqual(host.mutations(), [])

    def test_foreign_volume_aborts_before_mutation(self):
        host = FakeHost()
        key = next(iter(host.volumes))
        host.volumes[key]["Labels"]["com.docker.compose.project"] = "foreign"
        with self.assertRaises(MOD.LifecycleError):
            MOD.change_runtime("deactivate", host.command)
        self.assertEqual(host.mutations(), [])

    def test_active_worker_aborts_before_mutation(self):
        host = FakeHost()
        host.workers_active = True
        with self.assertRaisesRegex(MOD.LifecycleError, "active file-copy worker"):
            MOD.change_runtime("deactivate", host.command)
        self.assertEqual(host.mutations(), [])

    def test_real_vps_agent_worker_activating_blocks_both_operations(self):
        # The real worker uses this exact unit prefix and is Type=oneshot:
        # "activating" is the normal phase while copying a large GGUF.
        for action in ("deactivate", "uninstall"):
            with self.subTest(action=action):
                host = FakeHost()
                host.workers_active = True
                host.worker_phase = "activating"
                with self.assertRaisesRegex(MOD.LifecycleError, "active file-copy worker"):
                    MOD.change_runtime(action, host.command)
                self.assertEqual(host.mutations(), [])
                self.assertTrue(any(
                    cmd[0] == "systemctl" and
                    "vps-agent-filecopy-*.service" in cmd and
                    "--state=active,activating,deactivating,reloading,failed" in cmd
                    for cmd in host.calls
                ))

    def test_failed_filecopy_worker_blocks_deactivation_and_uninstall(self):
        # After a worker crashes, the final copy/cleanup status may be
        # uncertain. An operator must reconcile the failed unit explicitly.
        for action in ("deactivate", "uninstall"):
            with self.subTest(action=action):
                host = FakeHost()
                host.workers_active = True
                host.worker_phase = "failed"
                with self.assertRaisesRegex(MOD.LifecycleError, "active file-copy worker"):
                    MOD.change_runtime(action, host.command)
                self.assertEqual(host.mutations(), [])
                self.assertTrue(any(
                    cmd[0] == "systemctl"
                    and "--all" in cmd
                    and "failed" in next((v for v in cmd if v.startswith("--state=")), "")
                    for cmd in host.calls
                ))

    def test_inactive_filecopy_unit_does_not_block_normal_lifecycle(self):
        host = FakeHost()
        host.workers_active = True
        host.worker_phase = "inactive"
        self.assertEqual(MOD.change_runtime("deactivate", host.command), (3, 0))
        self.assertEqual(len(host.restart_disabled), 3)

    def test_legacy_filecopy_worker_also_blocks(self):
        host = FakeHost()
        host.workers_active = True
        host.worker_name = "portico-filecopy-legacy.service"
        with self.assertRaisesRegex(MOD.LifecycleError, "active file-copy worker"):
            MOD.change_runtime("deactivate", host.command)
        self.assertEqual(host.mutations(), [])

    def test_worker_appearing_after_initial_inventory_blocks(self):
        host = FakeHost()
        original = host.command
        reads = 0

        def racing(args):
            nonlocal reads
            result = original(args)
            if args[0] == "systemctl":
                reads += 1
                if reads == 2:
                    # Worker starts *after* initial two-prefix systemd probe.
                    # The second probe must detect it before any Docker write.
                    host.workers_active = True
            return result

        with self.assertRaisesRegex(MOD.LifecycleError, "active file-copy worker"):
            MOD.change_runtime("uninstall", racing)
        self.assertEqual(host.mutations(), [])

    def test_network_membership_race_blocks_before_mutation(self):
        host = FakeHost()
        original = host.command
        reads = 0

        def racing(args):
            nonlocal reads
            if args[:3] == ["docker", "network", "ls"]:
                reads += 1
                if reads == 2:
                    # A known member can disappear even without changing the
                    # container inventory. This must invalidate the preflight.
                    del host.networks[N]["Containers"][A]
            return original(args)

        with self.assertRaisesRegex(MOD.LifecycleError, "runtime inventory changed"):
            MOD.change_runtime("uninstall", racing)
        self.assertEqual(host.mutations(), [])

    def test_preflight_race_aborts_before_mutation(self):
        host = FakeHost()
        original = host.command
        reads = 0

        def racing(args):
            nonlocal reads
            if args[:2] == ["docker", "ps"]:
                reads += 1
                if reads == 2:
                    host.containers["e" * 64] = [
                        "gateway", True, "/srv/portico-infinity", "portico-infinity",
                    ]
            return original(args)

        with self.assertRaisesRegex(MOD.LifecycleError, "runtime inventory changed"):
            MOD.change_runtime("uninstall", racing)
        self.assertEqual(host.mutations(), [])

    def test_uninstall_is_idempotent_after_removal(self):
        host = FakeHost()
        MOD.change_runtime("uninstall", host.command)
        self.assertEqual(MOD.change_runtime("uninstall", host.command), (0, 0))


if __name__ == "__main__":
    unittest.main()
