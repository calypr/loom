import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts import normalize_ui_contract_inventory as normalizer


SOURCE_ROOT = Path(__file__).resolve().parents[2]


class NormalizeUiContractInventoryTest(unittest.TestCase):
    def _copy_inputs(self, destination: Path) -> None:
        for relative in (
            "docs/ui-contract-inventory",
            "ui/apps/demo/src",
            "ui/packages/loom-ui/src/features/ExplorerBuilder",
        ):
            shutil.copytree(SOURCE_ROOT / relative, destination / relative)
        for relative in (
            "ui/packages/loom-ui/src/Builder.tsx",
            "ui/packages/loom-ui/src/api.ts",
            "ui/packages/loom-ui/src/types.ts",
        ):
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(SOURCE_ROOT / relative, target)
        script = destination / "scripts/normalize_ui_contract_inventory.py"
        script.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(SOURCE_ROOT / "scripts/normalize_ui_contract_inventory.py", script)

    def _run_in_repository(self, repository: Path, marker: str, message: str) -> bytes:
        self._copy_inputs(repository)
        (repository / "verification-head-marker.txt").write_text(marker, encoding="utf-8")
        subprocess.run(["git", "init", "-q", str(repository)], check=True)
        subprocess.run(["git", "-C", str(repository), "config", "user.name", "Inventory Test"], check=True)
        subprocess.run(["git", "-C", str(repository), "config", "user.email", "inventory-test@example.invalid"], check=True)
        subprocess.run(["git", "-C", str(repository), "add", "."], check=True)
        subprocess.run(["git", "-C", str(repository), "commit", "-q", "-m", message], check=True)
        subprocess.run(["python3", "scripts/normalize_ui_contract_inventory.py"], cwd=repository, check=True, capture_output=True, text=True)
        return (repository / "docs/ui-contract-inventory/ui-supported-affordances.json").read_bytes()

    def test_identical_source_inputs_are_stable_across_verification_heads(self):
        with tempfile.TemporaryDirectory(prefix="ui-contract-head-a-") as first_dir, tempfile.TemporaryDirectory(prefix="ui-contract-head-b-") as second_dir:
            first = self._run_in_repository(Path(first_dir), "marker-a", "verification head A")
            second = self._run_in_repository(Path(second_dir), "marker-b", "verification head B")
        self.assertEqual(first, second)
        self.assertNotIn(b"generatedAtCommit", first)
        self.assertIn(('"sourceCommit": "' + normalizer.BASE + '"').encode(), first)
        manifest = json.loads(first)
        entry = next(item for item in manifest["affordances"] if item["id"] == "builder.population.exclude_unmapped_member")
        self.assertEqual(
            ["selectionVariantEndpoint", "routeLookupEndpoint", "applyEndpoint"],
            [item["stepId"] for item in entry["requests"]],
        )
        route_lookup = entry["requests"][1]
        self.assertEqual({"min": 1, "max": None}, route_lookup["repeat"]["cardinality"])
        self.assertEqual("cursor", route_lookup["repeat"]["requestCursorField"])
        self.assertEqual("nextCursor", route_lookup["repeat"]["responseCursorField"])
        self.assertIn("exact prior attached route found", route_lookup["repeat"]["applyAllowedWhen"])
        flow_steps = entry["requestFlows"][0]["steps"]
        self.assertEqual([1, 2, 3], [step["order"] for step in flow_steps])
        for step in flow_steps:
            for field in ("checkpointId", "flowId", "kind", "method", "endpoint", "actionId", "payloadShape", "requestContract", "constructor", "condition", "evidence"):
                self.assertIn(field, step)
        requests = [request for affordance in manifest["affordances"] for request in affordance["requests"]]
        self.assertEqual(159, len(requests))
        self.assertTrue(all(isinstance(request["payloadShape"], str) and request["payloadShape"] for request in requests))
        self.assertTrue(all(request["requestContract"]["body"]["presence"] in {"absent", "object"} for request in requests))
        self.assertTrue(all(request["requestContract"]["query"]["presence"] in {"absent", "object"} for request in requests))

        related_expand = next(item for item in manifest["affordances"] if item["id"] == "builder.reshape.related_expand")
        endpoints = [request for request in related_expand["requests"] if request["stepId"] == "endpoint"]
        self.assertEqual(2, len(endpoints))
        self.assertEqual(2, len({request["checkpointId"] for request in endpoints}))
        self.assertEqual([1, 2], [request["order"] for request in endpoints])
        for request in endpoints:
            self.assertEqual("object", request["requestContract"]["body"]["presence"])
            self.assertTrue(request["requestContract"]["body"]["requiredFields"])

    def test_unknown_endpoint_never_gets_an_inferred_method_or_body_shape(self):
        unknown = {
            "flowId": "primary",
            "order": 1,
            "stepId": "unknown-request",
            "method": None,
            "endpoint": "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/unmapped",
            "payloadShape": None,
            "evidence": ["ui/packages/loom-ui/src/api.ts:1"],
        }
        with self.assertRaisesRegex(SystemExit, "no source-backed HTTP method"):
            normalizer.finalize_request_record(unknown, "test.unknown")
        known = {
            "flowId": "primary",
            "order": 1,
            "stepId": "commands",
            "method": None,
            "endpoint": "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/commands",
            "payloadShape": None,
            "evidence": [],
        }
        normalized = normalizer.finalize_request_record(known, "test.known")
        self.assertEqual("POST", normalized["method"])
        self.assertEqual("object", normalized["requestContract"]["body"]["presence"])


if __name__ == "__main__":
    unittest.main()
