import json
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "tests.yaml"
UI_PACKAGE = ROOT / "ui" / "packages" / "loom-ui" / "package.json"


def step_body(source: str, name: str) -> str:
    lines = source.splitlines()
    marker = f"    - name: {name}"
    start = lines.index(marker) + 1
    body = []
    for line in lines[start:]:
        if line.startswith("    - name:"):
            break
        body.append(line)
    return "\n".join(body)


class UIWorkflowContractTest(unittest.TestCase):
    def test_required_ui_gate_runs_from_the_normal_test_workflow(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        package = json.loads(UI_PACKAGE.read_text(encoding="utf-8"))

        setup_node = step_body(workflow, "Set up Node")
        install = step_body(workflow, "Install UI dependencies")
        tests = step_body(workflow, "Run UI behavioral tests")
        build = step_body(workflow, "Build UI")

        self.assertIn("uses: actions/setup-node@v4", setup_node)
        self.assertIn("cache-dependency-path: ui/package-lock.json", setup_node)
        self.assertIn("working-directory: ui", install)
        self.assertIn("npm ci --ignore-scripts --no-audit --no-fund", install)
        self.assertIn("working-directory: ui", tests)
        self.assertIn("run: npm test", tests)
        self.assertIn("working-directory: ui", build)
        self.assertIn("run: npm run build", build)
        self.assertLess(
            workflow.index("Install UI dependencies"),
            workflow.index("Run UI behavioral tests"),
        )
        self.assertLess(
            workflow.index("Run UI behavioral tests"),
            workflow.index("Build UI"),
        )
        test_script = package["scripts"]["test"]
        self.assertIn("npm run check-boundaries", test_script)
        self.assertIn("npm run typecheck:tests", test_script)
        self.assertIn("vitest run", test_script)


if __name__ == "__main__":
    unittest.main()
