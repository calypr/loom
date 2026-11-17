import json
import re
import subprocess
from collections import Counter
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "tests.yaml"
FORMAL_WORKFLOW = ROOT / ".github" / "workflows" / "builder-formal.yml"


class UIWorkflowContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        result = subprocess.run(
            ["node", "--input-type=module", "-e",
             "import {sourceProofs} from './scripts/lib/formal-source-proofs.mjs'; "
             "console.log(JSON.stringify(sourceProofs.map(({model, property, kind}, index) => "
             "({index, model, property, kind}))));"],
            cwd=ROOT, check=True, capture_output=True, text=True, timeout=30,
        )
        cls.proofs = json.loads(result.stdout)

    def assert_formal_coverage(self, source: str) -> None:
        jobs = dict(re.findall(
            r"^  ([a-z][\w-]*):\n(.*?)(?=^  [a-z][\w-]*:\n|\Z)",
            source.split("jobs:\n", 1)[1], re.M | re.S,
        ))
        core = jobs["audit"]
        for command in (
            r"node scripts/verify-builder-formal-core\.mjs --self-test --models-only --out \S+",
            r"node scripts/verify-builder-formal-core\.mjs --phase generated --out \S+",
            r"node scripts/verify-builder-formal-core\.mjs --phase trace-controls --out \S+",
            r"node --test scripts/lib/formal-framework\.test\.mjs",
            r"../../node_modules/\.bin/tsc -p tsconfig\.test\.json",
            r"python3 scripts/verify_ui_contract_inventory\.py --source-only",
            r"node scripts/verify-builder-formal-core\.mjs --phase journey --feature operation-history-routine --out \S+",
        ):
            self.assertRegex(core, rf"(?m)^        run: {command}$")
        self.assertIn("working-directory: ui/packages/loom-ui", core)

        proof_job = jobs["source-proofs"]
        self.assertEqual(1, len(re.findall(
            r"node scripts/verify-builder-formal-core\.mjs --phase source-proofs\b",
            proof_job,
        )), "each matrix job must execute its proof selection exactly once")
        matrix = re.search(r"(?m)^        shard: \[([\d, ]+)\]$", proof_job)
        self.assertIsNotNone(matrix)
        shards = [int(value) for value in matrix[1].split(",")]
        self.assertEqual([1, 2, 3, 4], sorted(shards))
        command = re.search(
            r"(?m)^        run: node scripts/verify-builder-formal-core\.mjs "
            r"--phase source-proofs --shard \$\{\{ matrix\.shard \}\}/(\d+) "
            r"--out /tmp/loom-source-proofs-\$\{\{ matrix\.shard \}\}$",
            proof_job,
        )
        self.assertIsNotNone(command)
        count = int(command[1])
        self.assertEqual(len(shards), count)
        self.assertIn("fail-fast: false", proof_job)
        self.assertIn("if: always()", proof_job)
        self.assertIn("name: formal-source-proofs-${{ matrix.shard }}", proof_job)
        self.assertIn("path: /tmp/loom-source-proofs-${{ matrix.shard }}", proof_job)
        assignments = [[proof["index"] for proof in self.proofs
                        if proof["index"] % count == shard - 1] for shard in shards]
        self.assertTrue(self.proofs)
        self.assertTrue(all(assignments))
        self.assertEqual(Counter(proof["index"] for proof in self.proofs),
                         Counter(index for shard in assignments for index in shard))

    def test_formal_partition_rejects_missing_or_narrowed_coverage(self) -> None:
        formal = FORMAL_WORKFLOW.read_text(encoding="utf-8")
        self.assert_formal_coverage(formal)
        mutations = (
            ("--self-test --models-only", "--models-only"),
            ("--phase generated --out", "--phase generated --model core-protocol --out"),
            ("--phase trace-controls", "--phase implementation"),
            ("node --test scripts/lib/formal-framework.test.mjs", "true"),
            ("../../node_modules/.bin/tsc -p tsconfig.test.json", "true"),
            ("python3 scripts/verify_ui_contract_inventory.py --source-only", "true"),
            ("shard: [1, 2, 3, 4]", "shard: [1, 2, 3]"),
            ("shard: [1, 2, 3, 4]", "shard: [1, 2, 3, 3]"),
            ("--shard ${{ matrix.shard }}/4", "--shard ${{ matrix.shard }}/5"),
            ("fail-fast: false", "fail-fast: true"),
            ("      - name: Preserve source-proof evidence",
             "      - name: Duplicate proof selection\n"
             "        run: node scripts/verify-builder-formal-core.mjs --phase source-proofs "
             "--shard ${{ matrix.shard }}/4 --out /tmp/duplicate-source-proofs-${{ matrix.shard }}\n"
             "      - name: Preserve source-proof evidence"),
        )
        for before, after in mutations:
            with self.subTest(mutation=before):
                self.assertIn(before, formal)
                with self.assertRaises(AssertionError):
                    self.assert_formal_coverage(formal.replace(before, after, 1))

    def test_ci_runs_complete_suites_with_ten_minute_budgets(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        formal = FORMAL_WORKFLOW.read_text(encoding="utf-8")

        for source in (workflow, formal):
            jobs = re.findall(r"^  [a-z][\w-]*:\n(.*?)(?=^  [a-z][\w-]*:\n|\Z)", source.split("jobs:\n", 1)[1], re.M | re.S)
            self.assertTrue(jobs)
            for job in jobs:
                self.assertRegex(job, r"(?m)^    timeout-minutes: 10$")
            self.assertNotIn("timeout 30s", source)
            self.assertNotIn("-timeout=30s", source)
            self.assertNotIn("--test-timeout=30000", source)

        self.assertNotIn("matrix:", workflow)
        self.assertNotRegex(workflow, r"--(?:test-)?shard\b")
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
        self.assert_formal_coverage(formal)


if __name__ == "__main__":
    unittest.main()
