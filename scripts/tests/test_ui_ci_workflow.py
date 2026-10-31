import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "tests.yaml"
VITEST_CONFIG = ROOT / "ui" / "packages" / "loom-ui" / "vitest.config.ts"


class UIWorkflowContractTest(unittest.TestCase):
    def test_ui_tests_are_sharded_with_bounded_runtime_and_a_separate_build(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        vitest = VITEST_CONFIG.read_text(encoding="utf-8")

        self.assertIn("ui-unit:", workflow)
        self.assertIn("shard: [1, 2, 3, 4]", workflow)
        self.assertIn("--phase behavior --shard ${{ matrix.shard }}/4", workflow)
        self.assertIn("ui-build:", workflow)
        self.assertIn("run: npm run build", workflow)
        self.assertIn("node-unit:", workflow)
        self.assertIn("--test-shard=${{ matrix.shard }}/4", workflow)
        self.assertIn("--test-timeout=30000", workflow)
        self.assertNotIn("run: npm test", workflow)
        self.assertGreater(workflow.count("timeout-minutes: 10"), 0)
        self.assertNotRegex(workflow, r"timeout-minutes:\s*(?!10\b)\d+")
        self.assertIn("go-catalog-witness:", workflow)
        self.assertIn("LOOM_CATALOG_WITNESS_SHARD", workflow)
        self.assertIn("go-arango:", workflow)
        self.assertIn("testTimeout: 30_000", vitest)
        self.assertIn("hookTimeout: 30_000", vitest)


if __name__ == "__main__":
    unittest.main()
