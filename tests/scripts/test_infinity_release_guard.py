"""Negative-path gate for the inherited Community publisher in Infinity.

The Infinity v1.0 workflow must be created only after the owner's UAT/GO;
the inherited pipeline must never publish Infinity packages or tags.
"""
from pathlib import Path
import os
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "release.yml"
COMMUNITY = "josemirmoura/portico-community"


def job_section(source: str, job: str) -> str:
    marker = f"  {job}:\n"
    if marker not in source:
        raise AssertionError(f"missing release job: {job}")
    return source.split(marker, 1)[1].split("\n  ", 1)[0]


class InheritedReleaseGateTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = WORKFLOW.read_text(encoding="utf-8")

    def test_guard_executes_before_checkout_and_builds(self):
        validate = job_section(self.source, "validate")
        opening = """          if [ "${GITHUB_REPOSITORY:-}" != 'josemirmoura/portico-community' ]; then"""
        self.assertIn(opening, validate)
        self.assertIn("            exit 1", validate)
        self.assertIn("          fi", validate)
        self.assertLess(validate.index(opening), validate.index("- uses: actions/checkout@v7"))
        self.assertIn("test \"$GITHUB_ACTOR\" = 'josemirmoura'", validate)
        self.assertIn('test "$PUBLISH_CONFIRMATION" = "PUBLICAR $RELEASE_TAG"', validate)

        begin = validate.index(opening)
        end = validate.index("          fi", begin) + len("          fi")
        block = "\n".join(line[10:] for line in validate[begin:end].splitlines()) + "\n"
        for repository, expected in (
            (COMMUNITY, 0),
            ("josemirmoura/portico-infinity", 1),
            ("josemirmoura/mcp-vps-agent-gateway", 1),
            ("another-owner/portico-community", 1),
            ("", 1),
        ):
            with self.subTest(repository=repository):
                env = {**os.environ, "GITHUB_REPOSITORY": repository}
                run = subprocess.run(
                    ["bash", "-e", "-c", block],
                    env=env, text=True, capture_output=True, check=False,
                )
                self.assertEqual(run.returncode, expected, run.stderr)

    def test_publish_capable_jobs_are_repository_gated(self):
        for job in ("images", "github-release"):
            with self.subTest(job=job):
                section = job_section(self.source, job)
                self.assertIn(
                    "if: github.repository == 'josemirmoura/portico-community'",
                    section,
                )
        self.assertIn("needs: [validate, security, image-security]", job_section(self.source, "prepare-tag"))
        self.assertIn("needs: prepare-tag", job_section(self.source, "images"))
        self.assertIn("needs: images", job_section(self.source, "github-release"))

    def test_owner_dispatch_required(self):
        self.assertIn("  workflow_dispatch:", self.source)
        self.assertNotIn("  release:\n", self.source)
        self.assertIn("test \"$GITHUB_EVENT_NAME\" = 'workflow_dispatch'", job_section(self.source, "validate"))


if __name__ == "__main__":
    unittest.main()
