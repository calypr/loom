import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "tests.yaml"
FORMAL_WORKFLOW = ROOT / ".github" / "workflows" / "builder-formal.yml"


class UIWorkflowContractTest(unittest.TestCase):
    def test_ci_runs_complete_suites_with_ten_minute_budgets(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        formal = FORMAL_WORKFLOW.read_text(encoding="utf-8")

        # CI executes complete suites. Focused local shard commands remain optional.
        for source in (workflow, formal):
            self.assertNotIn("matrix:", source)
            self.assertNotRegex(source, r"--(?:test-)?shard\b")
            jobs = re.findall(r"^  [a-z][\w-]*:\n(.*?)(?=^  [a-z][\w-]*:\n|\Z)", source.split("jobs:\n", 1)[1], re.M | re.S)
            self.assertTrue(jobs)
            for job in jobs:
                self.assertIn("timeout-minutes: 10", job)
            self.assertNotIn("timeout 30s", source)
            self.assertNotIn("-timeout=30s", source)
            self.assertNotIn("--test-timeout=30000", source)

        self.assertIn("--phase behavior --out", workflow)
        self.assertIn("run: npm run build", workflow)
        self.assertIn("run: npm run check-boundaries", workflow)
        self.assertIn("npm run typecheck:tests", workflow)
        self.assertIn("--test-timeout=600000", workflow)
        self.assertIn('go test -timeout=10m -count=1 "${packages[@]}"', workflow)
        self.assertNotIn("LOOM_CATALOG_WITNESS_SHARD", workflow)
        self.assertNotIn("-skip '^TestSemanticCoverageGeneratedTemplatesHavePayloadWitnesses$'", workflow)
        self.assertIn("python3 -m unittest discover -s scripts/tests", workflow)
        self.assertIn("python3 -m unittest scripts.test_validate_architecture_plan", workflow)
        self.assertIn("go-arango:", workflow)
        self.assertIn("go-clickhouse:", workflow)
        self.assertIn("go-publication-rows:", workflow)
        self.assertIn("--self-test --out", formal)


if __name__ == "__main__":
    unittest.main()
