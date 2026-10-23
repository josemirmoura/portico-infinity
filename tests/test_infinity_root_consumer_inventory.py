"""Synthetic regression tests. Never query real host systemd in unit tests."""
from __future__ import annotations

import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest import mock

MODULE = Path(__file__).resolve().parents[1] / "scripts" / "infinity-root-consumer-inventory.py"
spec = importlib.util.spec_from_file_location("infinity_root_inventory", MODULE)
inv = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inv)


class RootConsumerInventoryTests(unittest.TestCase):
    def unit(self, **changes):
        data = dict.fromkeys(inv.FIELDS, "")
        data.update({
            "Id": "portico-ci-test.service",
            "LoadState": "loaded",
            "User": "",
            "DynamicUser": "no",
            "ExecStart": "{ path=/bin/sh ; argv[]=/bin/sh /opt/ci-project/root.sh ; ignore_errors=no }",
            "WorkingDirectory": "/opt/ci-project",
        })
        data.update(changes)
        return data

    def test_direct_root_consumers_and_no_secret_arguments_leaked(self):
        doc = self.unit(ExecStart="{ path=/bin/sh ; argv[]=/bin/sh /opt/proj/root.sh --password=SECRET }")
        item = inv.inspect_unit("portico-ci-test.service", doc, "/opt")
        self.assertEqual(item["classification"], "root_consumer_review")
        self.assertEqual(item["referencing_fields"], ["ExecStart", "WorkingDirectory"])
        self.assertNotIn("SECRET", json.dumps(item))
        self.assertNotIn("/opt/proj/root.sh", json.dumps(item))

    def test_nonroot_reference_is_not_labeled_root(self):
        for identity, dynamic in (("nobody", "no"), ("", "yes")):
            with self.subTest(user=identity, dynamic=dynamic):
                item = inv.inspect_unit(
                    "portico-ci-test.service",
                    self.unit(User=identity, DynamicUser=dynamic),
                    "/opt")
                self.assertEqual(item["classification"], "nonroot_consumer_reference")

    def test_unrelated_prefix_and_relative_paths_are_not_false_hits(self):
        for value in ("/optical/test.sh", "/opt-backup/install.sh",
                      "/home/optools/bin", "/usr/bin/opt", "/opt.project"):
            with self.subTest(value=value):
                self.assertFalse(inv.scope_reference(value, "/opt"))
        for value in ("/opt", "/opt/models/model.gguf", "argv[]=/bin/sh /opt/install.sh"):
            with self.subTest(value=value):
                self.assertTrue(inv.scope_reference(value, "/opt"))

    def test_alternate_scope_and_systemd_environment_files(self):
        data = self.unit(
            ExecStart="{ path=/usr/bin/env ; argv[]=/usr/bin/env true }",
            WorkingDirectory="/",
            EnvironmentFiles="/data/project/runner.env (ignore_errors=no)",
        )
        item = inv.inspect_unit("portico-ci-test.service", data, "/data")
        self.assertEqual(item["referencing_fields"], ["EnvironmentFiles"])
        self.assertIsNone(inv.inspect_unit("portico-ci-test.service", data, "/opt"))

    def test_untrusted_units_and_missing_properties_fail_closed(self):
        for bad in ("--help.service", "../test.service", "bad/name.service"):
            with self.subTest(bad=bad):
                with self.assertRaises(inv.InventoryIncomplete):
                    inv.show_unit(bad)
        with mock.patch.object(inv, "run_systemctl", return_value="Id=portico-ci-test.service\nLoadState=loaded\n"):
            with self.assertRaises(inv.InventoryIncomplete):
                inv.show_unit("portico-ci-test.service")

    def test_systemctl_unset_optional_fields_are_empty_not_missing_service(self):
        lines = [
            "Id=portico-ci-test.service",
            "LoadState=loaded", "User=", "DynamicUser=no",
            "ExecStart={ path=/bin/sh ; argv[]=/bin/sh /opt/root-script.sh }",
        ]
        with mock.patch.object(inv, "run_systemctl", return_value="\\n".join(lines)+"\\n"):
            doc = inv.show_unit("portico-ci-test.service")
        self.assertEqual(doc["EnvironmentFiles"], "")
        self.assertEqual(doc["ExecStartPre"], "")
        self.assertEqual(doc["ExecStartPost"], "")
        self.assertEqual(inv.inspect_unit("portico-ci-test.service", doc, "/opt")["classification"],
                         "root_consumer_review")

    def test_cannot_claim_clean_report_after_systemd_partial_failure(self):
        with mock.patch.object(inv, "list_units", return_value=[
                "portico-ci-test.service", "another.service"
             ]), mock.patch.object(inv, "show_unit", side_effect=[
                self.unit(),
                inv.InventoryIncomplete("second service not readable"),
             ]):
            with self.assertRaises(inv.InventoryIncomplete):
                inv.inventory("/opt")

    def test_strict_mode_and_nonstrict_status(self):
        with mock.patch.object(inv, "list_units", return_value=["portico-ci-test.service"]), \
             mock.patch.object(inv, "show_unit", return_value=self.unit()):
            doc = inv.inventory("/opt")
            self.assertEqual(doc["status"], "review_required")
            self.assertEqual(doc["root_consumer_count"], 1)
            self.assertEqual(doc["systemd_services_inspected"], 1)

    def test_never_reuse_operator_systemd_names_with_shell_characters(self):
        with mock.patch.object(inv, "run_systemctl",
                               return_value="vps-agent.service loaded active running\n"
                                            "malicious/../../unit.service loaded active running\n"):
            with self.assertRaises(inv.InventoryIncomplete):
                inv.list_units()

    def test_failed_unit_bullet_is_not_mistaken_for_a_unit_name(self):
        with mock.patch.object(inv, "run_systemctl",
                               return_value="● failed.service loaded failed failed\n"
                                            "normal.service loaded active running\n"):
            self.assertEqual(inv.list_units(), ["failed.service", "normal.service"])

    def test_physical_scope_validation(self):
        for bad in ("/", "/etc", "opt", "/opt/../etc", "/opt/", "/srv"):
            with self.subTest(bad=bad):
                with self.assertRaises(inv.InventoryIncomplete):
                    inv.canonical_scope(bad)
        self.assertEqual(inv.canonical_scope("/data/portico-project"), "/data/portico-project")


if __name__ == "__main__":
    unittest.main()
