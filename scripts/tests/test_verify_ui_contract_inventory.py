import hashlib
import copy
import json
import tempfile
import unittest
from pathlib import Path

from scripts import verify_ui_contract_inventory as verifier


def write_source(root, relative, text):
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    return path


def make_source_tree(root):
    write_source(root, "ui/apps/demo/src/demo-route.mjs", "export const route = true;\n")
    write_source(root, "ui/apps/demo/src/main.tsx", "export const main = true;\n")
    write_source(root, "ui/packages/loom-ui/src/Builder.tsx", "export const Builder = true;\n")
    write_source(root, "ui/packages/loom-ui/src/api.ts", "export const api = true;\n")
    types_source = """const constructionCombineSchema = z.object({
  kind: z.enum(['KEY_JOIN', 'APPEND']),
});
const constructionOperationSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('PIVOT') }).strict(),
  z.object({ kind: z.literal('CODED_GROUP') }).strict(),
  z.object({ kind: z.literal('COMBINE'), combine: constructionCombineSchema }).strict(),
]);
const rowDefinitionSelectionSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('RECORDS') }).strict(),
  z.object({ kind: z.literal('FIELD_GROUP') }).strict(),
  z.object({ kind: z.literal('EXPLICIT_GROUP') }).strict(),
  z.object({ kind: z.literal('EXPANDED') }).strict(),
]);
const tableShapeProposalIntentSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('NONE') }).strict(),
  z.object({ kind: z.literal('GROUPED_PIVOT') }).strict(),
  z.object({ kind: z.literal('UNPIVOT') }).strict(),
]);
"""
    write_source(root, "ui/packages/loom-ui/src/types.ts", types_source)
    write_source(
        root,
        "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx",
        "export const workspace = true;\n",
    )
    write_source(
        root,
        "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx",
        """const allOperationFamilies = [
  { family: 'ADD_COLUMNS' },
  { family: 'KEEP_ROWS' },
  { family: 'CALCULATE' },
  { family: 'RESHAPE' },
  { family: 'COMBINE' },
];
""",
    )
    write_source(
        root,
        "ui/packages/loom-ui/src/features/ExplorerBuilder/builderContractAdapters/peripheral.tsx",
        "export const peripheralAdapter = true;\n",
    )


def make_manifest(root, pending=False):
    watched_files = []
    for relative in sorted(verifier.discover_watched_source_files(root)):
        watched_files.append(
            {
                "path": relative,
                "sha256": hashlib.sha256((root / relative).read_bytes()).hexdigest(),
            }
        )

    kinds = {
        "PIVOT",
        "CODED_GROUP",
        "COMBINE:KEY_JOIN",
        "COMBINE:APPEND",
        "ROW_DEFINITION:RECORDS",
        "ROW_DEFINITION:FIELD_GROUP",
        "ROW_DEFINITION:EXPLICIT_GROUP",
        "ROW_DEFINITION:EXPANDED",
        "TABLE_SHAPE:DERIVED_COLUMN",
        "TABLE_SHAPE:GROUPED_PIVOT",
        "TABLE_SHAPE:UNPIVOT",
    }
    families = ["ADD_COLUMNS", "KEEP_ROWS", "CALCULATE", "RESHAPE", "COMBINE"]
    affordances = []
    bindings = []
    for index, kind in enumerate(sorted(kinds)):
        entry_id = "builder.reshape.group_coded" if kind == "CODED_GROUP" else "test.token." + str(index)
        coverage = {
            "modelStatus": "pending" if pending else "modeled",
            "adapterStatus": "pending" if pending else "mapped",
            "adapterId": None if pending else "adapter." + str(index),
            "runnerActionId": None if pending else "action." + str(index),
            "runnerActions": {
                "status": "pending" if pending else "mapped",
                "bindings": {} if pending else {"test_action": "action." + str(index)},
            },
            "modelStateId": None if pending else "state." + str(index),
        }
        affordances.append(
            {
                "id": entry_id,
                "availability": {"status": "capability-conditional", "conditions": []},
                "operation": {
                    "family": families[index % len(families)],
                    "backendKinds": [kind],
                },
                "coverage": coverage,
                "dimensions": [
                    {
                        "id": "input_" + str(index),
                        "cardinality": {"min": 0, "max": None},
                        "evidence": ["ui/packages/loom-ui/src/types.ts:1"],
                    }
                ],
                "evidence": ["ui/packages/loom-ui/src/types.ts:1"],
            }
        )
        bindings.append(
            {
                "id": entry_id,
                "backendKinds": [kind],
                "evidence": ["ui/packages/loom-ui/src/types.ts:1"],
            }
        )
    for index, family in enumerate(families):
        affordances.append(
            {
                "id": "test.family." + str(index),
                "availability": {"status": "reachable", "conditions": []},
                "operation": {"family": family, "backendKinds": []},
                "coverage": {
                    "modelStatus": "pending" if pending else "modeled",
                    "adapterStatus": "pending" if pending else "mapped",
                    "adapterId": None if pending else "family.adapter." + str(index),
                    "runnerActionId": None if pending else "family.action." + str(index),
                    "runnerActions": {
                        "status": "pending" if pending else "mapped",
                        "bindings": {} if pending else {"test_action": "family.action." + str(index)},
                    },
                    "modelStateId": None if pending else "family.state." + str(index),
                },
                "dimensions": [],
                "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx:1"],
            }
        )
    return {
        "schemaVersion": 1,
        "sourceCommit": verifier.SOURCE_COMMIT,
        "status": "test-fixture",
        "affordances": affordances,
        "disabledAffordances": [
            {
                "id": "test.disabled_overlay",
                "canonicalIds": ["builder.reshape.group_coded"],
                "classification": "visible-disabled",
                "control": "Group",
                "state": "capability unavailable",
                "evidence": ["ui/packages/loom-ui/src/types.ts:1"],
            }
        ],
        "excludedSurfaces": [],
        "sourceCoverage": {
            "method": "test fixture",
            "watchedFiles": watched_files,
            "mountedOperationSources": [
                "ui/packages/loom-ui/src/types.ts",
                "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx",
            ],
            "discriminantExtractors": [
                {
                    "file": "ui/packages/loom-ui/src/types.ts",
                    "schema": "constructionOperationSchema",
                    "mappingField": "affordances[].operation.backendKinds",
                    "excludes": ["COMBINE"],
                    "combineSubkindSchema": "constructionCombineSchema",
                },
                {
                    "file": "ui/packages/loom-ui/src/types.ts",
                    "schema": "rowDefinitionSelectionSchema",
                    "mappingField": "affordances[].operation.backendKinds",
                    "prefix": "ROW_DEFINITION:",
                },
                {
                    "file": "ui/packages/loom-ui/src/types.ts",
                    "schema": "tableShapeProposalIntentSchema",
                    "mappingField": "affordances[].operation.backendKinds",
                    "prefix": "TABLE_SHAPE:",
                },
                {
                    "file": "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx",
                    "schema": "allOperationFamilies",
                    "mappingField": "affordances[].operation.family",
                },
            ],
            "bindings": bindings,
            "surfaceMappings": [
                {
                    "id": "test.surface_mapping",
                    "canonicalId": "builder.reshape.group_coded",
                    "dimensions": [
                        {"id": "choice", "cardinality": {"minimum": 1, "maximum": None}}
                    ],
                    "evidence": ["ui/packages/loom-ui/src/types.ts:1"],
                }
            ],
        },
    }


class VerifyUiContractInventoryTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="verify-ui-contract-test-")
        self.root = Path(self.temporary.name)
        make_source_tree(self.root)
        self.manifest = make_manifest(self.root)

    def tearDown(self):
        self.temporary.cleanup()

    def test_complete_fixture_passes_strict_source_and_coverage_checks(self):
        self.assertEqual([], verifier.validate_manifest(self.manifest, self.root, require_complete=True))

    def test_pending_rows_pass_source_only_but_fail_strict_coverage(self):
        manifest = make_manifest(self.root, pending=True)
        self.assertEqual([], verifier.validate_manifest(manifest, self.root, require_complete=False))
        errors = verifier.validate_manifest(manifest, self.root, require_complete=True)
        self.assertTrue(any("model coverage is not modeled" in error for error in errors))
        self.assertTrue(any("adapter coverage is not mapped" in error for error in errors))
        self.assertTrue(any("runner action coverage is not mapped" in error for error in errors))
        self.assertTrue(any("missing coverage.runnerActionId" in error for error in errors))

    def test_runner_action_coverage_requires_explicit_nonempty_mappings(self):
        manifest = make_manifest(self.root)
        manifest["affordances"][0]["coverage"]["runnerActions"] = {"status": "mapped", "bindings": {}}
        errors = verifier.validate_manifest(manifest, self.root, require_complete=True)
        self.assertTrue(any("mapped but has no action bindings" in error for error in errors))

        del manifest["affordances"][0]["coverage"]["runnerActions"]
        errors = verifier.validate_manifest(manifest, self.root, require_complete=False)
        self.assertTrue(any("coverage row is missing fields: runnerActions" in error for error in errors))

    def test_request_contract_requires_method_checkpoint_and_explicit_body_query_shape(self):
        manifest = copy.deepcopy(self.manifest)
        entry = manifest["affordances"][0]
        request = {
            "checkpointId": "primary:1:test-step",
            "flowId": "primary",
            "order": 1,
            "stepId": "test-step",
            "kind": "command",
            "method": "POST",
            "endpoint": "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/commands",
            "actionId": "test-action",
            "payloadShape": "body object schema TestRequest",
            "requestContract": {
                "method": "POST",
                "constructor": "test API constructor",
                "body": {"presence": "object", "schema": "TestRequest", "requiredFields": ["snapshotToken"], "optionalFields": []},
                "query": {"presence": "absent"},
                "headers": {"optional": []},
                "evidence": ["ui/packages/loom-ui/src/api.ts:1"],
            },
            "constructor": "test API constructor",
            "sourceConstructors": [],
            "evidence": ["ui/packages/loom-ui/src/api.ts:1"],
        }
        entry["requests"] = [request]
        entry["requestFlows"] = [{"id": "primary", "steps": [copy.deepcopy(request)]}]
        self.assertEqual([], verifier.validate_manifest(manifest, self.root, require_complete=False))

        request.pop("method")
        errors = verifier.validate_manifest(manifest, self.root, require_complete=False)
        self.assertTrue(any("method must be an explicit supported HTTP method" in error for error in errors))

        request["method"] = "POST"
        request["payloadShape"] = None
        errors = verifier.validate_manifest(manifest, self.root, require_complete=False)
        self.assertTrue(any("payloadShape must explicitly describe body/query presence" in error for error in errors))

    def test_excluded_surfaces_need_source_backed_reason_and_unmounted_proof(self):
        manifest = make_manifest(self.root)
        manifest["excludedSurfaces"] = [
            {"id": "legacy.panel", "basis": "unmounted", "reason": "No mounted caller", "evidence": ["ui/packages/loom-ui/src/types.ts:1"]}
        ]
        errors = verifier.validate_manifest(manifest, self.root, require_complete=False)
        self.assertTrue(any("needs source proof" in error for error in errors))

        manifest["excludedSurfaces"][0]["proof"] = {"mountedCallerFound": False}
        self.assertEqual([], verifier.validate_manifest(manifest, self.root, require_complete=False))

    def test_required_omission_and_scratch_source_mutation_probes(self):
        results = verifier._run_mutation_probes(self.manifest, self.root)
        self.assertEqual(2, len(results), results)
        self.assertTrue(all(result.startswith("PASS ") for result in results), results)
        self.assertIn("unbound CODED_GROUP", results[0])
        self.assertIn("SCRATCH_ADDED", results[1])

    def test_source_hash_and_file_set_drift_fail(self):
        path = self.root / "ui/packages/loom-ui/src/Builder.tsx"
        path.write_text("export const Builder = false;\n", encoding="utf-8")
        errors = verifier.validate_manifest(self.manifest, self.root, require_complete=False)
        self.assertTrue(any("source hash drift" in error for error in errors))

        extra = self.root / "ui/packages/loom-ui/src/features/ExplorerBuilder/NewPanel.tsx"
        extra.write_text("export const NewPanel = true;\n", encoding="utf-8")
        errors = verifier.validate_manifest(self.manifest, self.root, require_complete=False)
        self.assertTrue(any("source file-set drift: new/unwatched active files" in error for error in errors))

    def test_duplicate_ids_bad_cardinality_and_bad_line_refs_fail(self):
        manifest = json.loads(json.dumps(self.manifest))
        duplicate = json.loads(json.dumps(manifest["affordances"][0]))
        manifest["affordances"].append(duplicate)
        manifest["affordances"][1]["dimensions"][0]["cardinality"] = {"min": 2, "max": 1}
        manifest["affordances"][2]["evidence"] = ["ui/packages/loom-ui/src/types.ts:999999"]
        errors = verifier.validate_manifest(manifest, self.root, require_complete=False)
        self.assertTrue(any("duplicate affordance id" in error for error in errors))
        self.assertTrue(any("minimum exceeds maximum" in error for error in errors))
        self.assertTrue(any("exceeds" in error for error in errors))

    def test_missing_source_coverage_fails_closed_without_crashing(self):
        manifest = json.loads(json.dumps(self.manifest))
        manifest.pop("sourceCoverage")
        errors = verifier.validate_manifest(manifest, self.root, require_complete=False)
        self.assertTrue(any("sourceCoverage must be an object" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
