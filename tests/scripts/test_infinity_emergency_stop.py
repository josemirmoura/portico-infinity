"""Emergency stop protects unrelated Docker projects, even in a failure.

All tests use a fake Docker callback and create no host resources.
"""
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "scripts/infinity-emergency-stop.py"
SPEC = importlib.util.spec_from_file_location("infinity_emergency_stop", SOURCE)
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)

ID_A = "a" * 12
ID_B = "b" * 12
ID_C = "c" * 12


class DockerFixture:
    def __init__(self, extra_foreign=False):
        self.calls = []
        self.rows = {
            ID_A: ("gateway", True, "portico-infinity", "/srv/portico-infinity"),
            ID_B: ("broker", True, "portico-infinity", "/srv/portico-infinity"),
            ID_C: ("zitadel-postgres", True, "portico-infinity", "/srv/portico-infinity"),
        }
        if extra_foreign:
            self.rows[ID_C] = ("broker", True, "portico-infinity", "/srv/foreign")
    def run(self, command):
        self.calls.append(tuple(command))
        if command[0] == "ps":
            return "\n".join(self.rows.keys()) + "\n"
        if command[0] == "inspect":
            service, active, project, workdir = self.rows[command[1]]
            return json.dumps([{
                "Config": {"Labels": {
                    "com.docker.compose.project": project,
                    "com.docker.compose.project.working_dir": workdir,
                    "com.docker.compose.service": service}},
                "State": {"Running": active},
            }])
        if command[0] == "stop":
            id_ = command[-1]
            service, active, project, workdir = self.rows[id_]
            self.rows[id_] = (service, False, project, workdir)
            return id_
        raise AssertionError("unexpected mock command: " + repr(command))


class InfinityEmergencyStopTests(unittest.TestCase):
    def test_only_infinity_gateway_and_broker_stopped(self):
        fixture = DockerFixture()
        MOD.stop_guarded(fixture.run, execute=True)
        self.assertFalse(fixture.rows[ID_A][1])
        self.assertFalse(fixture.rows[ID_B][1])
        self.assertTrue(fixture.rows[ID_C][1])
        stopped = [cmd[-1] for cmd in fixture.calls if cmd[0] == "stop"]
        self.assertEqual(stopped, [ID_A, ID_B])

    def test_preview_never_modifies(self):
        fixture = DockerFixture()
        result = MOD.stop_guarded(fixture.run, execute=False)
        self.assertEqual(len(result), 3)
        self.assertFalse(any(call[0] == "stop" for call in fixture.calls))

    def test_foreign_owner_denies_before_any_mutation(self):
        fixture = DockerFixture(extra_foreign=True)
        with self.assertRaises(MOD.EmergencyStopError):
            MOD.stop_guarded(fixture.run, execute=True)
        self.assertFalse(any(call[0] == "stop" for call in fixture.calls))

    def test_missing_container_metadata_denies(self):
        fixture = DockerFixture()
        fixture.rows[ID_B] = ("", True, "portico-infinity", "/srv/portico-infinity")
        with self.assertRaises(MOD.EmergencyStopError):
            MOD.stop_guarded(fixture.run, execute=True)
        self.assertFalse(any(call[0] == "stop" for call in fixture.calls))

    def test_invalid_identifier_denies(self):
        fixture = DockerFixture()
        fixture.rows["evil-command"] = fixture.rows.pop(ID_A)
        with self.assertRaises(MOD.EmergencyStopError):
            MOD.stop_guarded(fixture.run, execute=True)
        self.assertFalse(any(call[0] == "stop" for call in fixture.calls))


if __name__ == "__main__":
    unittest.main()
