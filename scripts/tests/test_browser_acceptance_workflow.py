import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "acceptance.yaml"


def job_body(source: str, name: str) -> str:
    lines = source.splitlines()
    marker = f"    name: {name}"
    start = lines.index(marker)
    body = []
    for line in lines[start:]:
        if line.startswith("  ") and line.endswith(":") and not line.startswith("    "):
            break
        body.append(line)
    return "\n".join(body)


class BrowserAcceptanceWorkflowTest(unittest.TestCase):
    def test_required_browser_job_runs_full_owned_verification_and_cleanup(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        browser = job_body(workflow, "Builder-to-Viewer browser acceptance")

        self.assertIn("uses: actions/setup-node@v4", browser)
        self.assertIn("node-version: 22.x", browser)
        self.assertIn("LOOM_DEV_COMPOSE_PROJECT: loom-dev-ci-${{ github.run_id }}", browser)
        self.assertIn("LOOM_DEV_PROJECT: loom_dev_ci_${{ github.run_id }}", browser)
        self.assertIn("LOOM_DEV_API_PORT: 8280", browser)
        self.assertIn("LOOM_DEV_UI_PORT: 3280", browser)
        self.assertIn("LOOM_DEV_ARTIFACTS: .artifacts/browser/${{ github.run_id }}", browser)
        self.assertIn("run: node scripts/loom-dev.mjs verify-fast", browser)
        self.assertIn("run: node scripts/loom-dev.mjs dev-down --purge", browser)
        self.assertIn("if: always()", browser)
        self.assertLess(
            browser.index("node scripts/loom-dev.mjs verify-fast"),
            browser.index("node scripts/loom-dev.mjs dev-down --purge"),
        )


if __name__ == "__main__":
    unittest.main()
