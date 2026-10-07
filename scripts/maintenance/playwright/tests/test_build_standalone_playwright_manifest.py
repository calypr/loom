import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[4]
BUILDER = REPOSITORY_ROOT / "scripts/maintenance/playwright/build-standalone-playwright-manifest.py"


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n")


class HistoricalPreimageTest(unittest.TestCase):
    def test_current_source_bytes_do_not_become_an_unknown_historical_preimage(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir) / "source"
            source = root / "scripts/verify-b07-faults.mjs"
            source.parent.mkdir(parents=True)
            source.write_text("export const currentSource = 'after conversion';\n")
            current_hash = sha256(source)

            persistent_source = root / "scripts/verify-b08-artifacts.mjs"
            persistent_source.write_text("export const persistentSource = true;\n")
            persistent_preimage = "a" * 64
            mapped_source = root / "scripts/verify-builder-reconciliation.mjs"
            mapped_source.write_text("export const mappedSource = true;\n")
            mapped_preimage = "b" * 64

            main_config = root / "scripts/playwright.config.mjs"
            main_config.parent.mkdir(parents=True, exist_ok=True)
            main_config.write_text("export default {};\n")
            benchmark_config = root / "scripts/measurements/construction-preview/construction-preview-bench.config.mjs"
            benchmark_config.parent.mkdir(parents=True, exist_ok=True)
            benchmark_config.write_text("export default {};\n")

            registry = root / "scripts/verify-ui/registry.mjs"
            registry.parent.mkdir(parents=True, exist_ok=True)
            registry.write_text(
                "export const registry = [];\n"
                "export const caseNamesFor = () => [];\n"
                "export const scenarioCaseFor = () => ({ requiredChecks: [] });\n"
            )

            runner_inventory = root / "docs/verification/playwright/runner-inventory.snapshot.json"
            write_json(runner_inventory, {"snapshotCommit": "fixture", "browserLauncherConsumers": [], "launcherImplementations": []})
            discovery = root / "docs/verification/playwright/discovery.snapshot.json"
            write_json(discovery, {
                "status": "discovery-only",
                "runtimeStatus": "not-run",
                "sessions": [
                    {
                        "sessionID": "main",
                        "configPath": "scripts/playwright.config.mjs",
                        "configSha256": sha256(main_config),
                        "testCount": 0,
                        "specFileCount": 0,
                        "specs": [],
                    },
                    {
                        "sessionID": "construction-preview-bench",
                        "configPath": "scripts/measurements/construction-preview/construction-preview-bench.config.mjs",
                        "configSha256": sha256(benchmark_config),
                        "testCount": 0,
                        "specFileCount": 0,
                        "specs": [],
                    },
                ],
            })
            source_map = root / "source-map.json"
            write_json(source_map, {"sources": [{
                "sourcePath": "scripts/verify-builder-reconciliation.mjs",
                "sourcePreimageSha256": mapped_preimage,
            }]})
            overrides = root / "discovery-overrides.json"
            write_json(overrides, {"cases": []})
            preimages = root / "source-preimages.json"
            write_json(preimages, {"scripts/verify-b08-artifacts.mjs": persistent_preimage})
            output = root / "source-conversion-manifest.json"
            write_json(output, {})
            markdown = root / "source-conversion-manifest.md"

            command = [
                sys.executable,
                str(BUILDER),
                "--source-root", str(root),
                "--runner-inventory", str(runner_inventory),
                "--worker-map", str(source_map),
                "--discovery", str(discovery),
                "--discovery-overrides", str(overrides),
                "--registry", str(registry),
                "--preimages", str(preimages),
                "--output", str(output),
                "--markdown-output", str(markdown),
            ]
            result = subprocess.run(
                command,
                cwd=REPOSITORY_ROOT,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

            manifest = json.loads(output.read_text())
            row = next(item for item in manifest["sources"] if item["sourcePath"] == "scripts/verify-b07-faults.mjs")
            self.assertEqual(row["currentSha256"], current_hash)
            self.assertIsNone(row["preimageSha256"])
            self.assertEqual(row["historicalPreimageStatus"], "unknown")
            saved_preimages = json.loads(preimages.read_text())
            self.assertNotIn("scripts/verify-b07-faults.mjs", saved_preimages)
            self.assertEqual(saved_preimages["scripts/verify-b08-artifacts.mjs"], persistent_preimage)
            self.assertEqual(saved_preimages["scripts/verify-builder-reconciliation.mjs"], mapped_preimage)
            self.assertEqual(manifest["unknownHistoricalPreimages"], ["scripts/verify-b07-faults.mjs"])
            self.assertEqual(manifest["counts"]["unknownHistoricalSourcePreimages"], 1)
            self.assertFalse(manifest["mechanicalConversionComplete"])
            self.assertFalse(manifest["readyForBrowserTesting"])
            persistent_row = next(item for item in manifest["sources"] if item["sourcePath"] == "scripts/verify-b08-artifacts.mjs")
            mapped_row = next(item for item in manifest["sources"] if item["sourcePath"] == "scripts/verify-builder-reconciliation.mjs")
            self.assertEqual(persistent_row["preimageSha256"], persistent_preimage)
            self.assertEqual(mapped_row["preimageSha256"], mapped_preimage)

            saved_preimages["scripts/verify-b07-faults.mjs"] = "c" * 64
            write_json(preimages, saved_preimages)
            result = subprocess.run(
                command,
                cwd=REPOSITORY_ROOT,
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            completed_manifest = json.loads(output.read_text())
            completed_row = next(item for item in completed_manifest["sources"] if item["sourcePath"] == "scripts/verify-b07-faults.mjs")
            self.assertEqual(completed_row["preimageSha256"], "c" * 64)
            self.assertEqual(completed_row["historicalPreimageStatus"], "known")
            self.assertEqual(completed_manifest["unknownHistoricalPreimages"], [])
            self.assertEqual(completed_manifest["counts"]["unknownHistoricalSourcePreimages"], 0)
            self.assertTrue(completed_manifest["mechanicalConversionComplete"])
            self.assertTrue(completed_manifest["readyForBrowserTesting"])


class NewNativeSourcePreimageTest(unittest.TestCase):
    def test_builder_emits_new_native_source_provenance_without_a_preimage_hash(self) -> None:
        source_path = "scripts/verify-ui/workflows/verify-cda-zero-column-related-medication.mjs"
        source_preimages = json.loads((REPOSITORY_ROOT / "docs/verification/playwright/source-preimages.json").read_text())
        provenance = source_preimages[source_path]

        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir) / "source"
            source = root / source_path
            source.parent.mkdir(parents=True)
            source.write_text("export const newNativeSource = true;\n")

            main_config = root / "scripts/playwright.config.mjs"
            main_config.parent.mkdir(parents=True, exist_ok=True)
            main_config.write_text("export default {};\n")
            benchmark_config = root / "scripts/measurements/construction-preview/construction-preview-bench.config.mjs"
            benchmark_config.parent.mkdir(parents=True)
            benchmark_config.write_text("export default {};\n")
            registry = root / "scripts/verify-ui/registry.mjs"
            registry.parent.mkdir(parents=True, exist_ok=True)
            registry.write_text(
                "export const registry = [];\n"
                "export const caseNamesFor = () => [];\n"
                "export const scenarioCaseFor = () => ({ requiredChecks: [] });\n"
            )

            inventory = root / "runner-inventory.json"
            write_json(inventory, {"snapshotCommit": "fixture", "browserLauncherConsumers": [], "launcherImplementations": []})
            discovery = root / "discovery.json"
            write_json(discovery, {
                "status": "discovery-only",
                "runtimeStatus": "not-run",
                "sessions": [
                    {
                        "sessionID": "main",
                        "configPath": "scripts/playwright.config.mjs",
                        "configSha256": sha256(main_config),
                        "testCount": 0,
                        "specFileCount": 0,
                        "specs": [],
                    },
                    {
                        "sessionID": "construction-preview-bench",
                        "configPath": "scripts/measurements/construction-preview/construction-preview-bench.config.mjs",
                        "configSha256": sha256(benchmark_config),
                        "testCount": 0,
                        "specFileCount": 0,
                        "specs": [],
                    },
                ],
            })
            preimages = root / "source-preimages.json"
            write_json(preimages, {source_path: provenance})
            worker_map = root / "empty-worker-map.json"
            write_json(worker_map, {"sources": [{
                "sourcePath": source_path,
                "disposition": "retained-pure-oracle-helper",
                "reason": "complete conversion fixture aside from unverified new-source provenance",
            }]})
            output = root / "fresh-manifest.json"
            write_json(output, {})
            markdown = root / "fresh-manifest.md"

            result = subprocess.run([
                sys.executable,
                str(BUILDER),
                "--source-root", str(root),
                "--runner-inventory", str(inventory),
                "--worker-map", str(worker_map),
                "--discovery", str(discovery),
                "--registry", str(registry),
                "--preimages", str(preimages),
                "--output", str(output),
                "--markdown-output", str(markdown),
            ], cwd=REPOSITORY_ROOT, text=True, capture_output=True, check=False)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

            manifest = json.loads(output.read_text())
            row = next(item for item in manifest["sources"] if item["sourcePath"] == source_path)
            self.assertIsNone(row["preimageSha256"])
            self.assertEqual(row["historicalPreimageStatus"], "new-native-source-unverified")
            self.assertEqual(row["historicalPreimageProvenance"], provenance)
            self.assertIn(source_path, manifest["unknownHistoricalPreimages"])
            self.assertEqual(manifest["counts"]["unknownHistoricalSourcePreimages"], 1)
            self.assertFalse(manifest["mechanicalConversionComplete"])
            self.assertFalse(manifest["readyForBrowserTesting"])
            self.assertEqual(json.loads(preimages.read_text())[source_path], provenance)

            complete_result = subprocess.run([
                sys.executable,
                str(BUILDER),
                "--source-root", str(root),
                "--runner-inventory", str(inventory),
                "--worker-map", str(worker_map),
                "--discovery", str(discovery),
                "--registry", str(registry),
                "--preimages", str(preimages),
                "--output", str(output),
                "--markdown-output", str(markdown),
                "--require-complete",
            ], cwd=REPOSITORY_ROOT, text=True, capture_output=True, check=False)
            self.assertEqual(complete_result.returncode, 1, complete_result.stdout + complete_result.stderr)


class CanonicalManifestJourneyTest(unittest.TestCase):
    def test_canonical_manifest_wins_over_stale_embedded_map_in_fresh_output(self) -> None:
        previous_manifest_path = REPOSITORY_ROOT / "docs/verification/playwright/source-conversion-manifest.json"
        previous_manifest = json.loads(previous_manifest_path.read_text())
        previous_group = next(
            item for item in previous_manifest["additionalBrowserWorkflowSources"]
            if item.get("sourcePath") == "scripts/loom-dev.mjs"
        )
        expected_journeys = previous_group["journeys"]
        current_spec = "scripts/verify-ui/specs/dev-journeys.spec.mjs"
        stale_spec = "scripts/playwright/dev-journeys.spec.mjs"
        self.assertEqual(len(expected_journeys), 9)
        self.assertEqual(
            {path for journey in expected_journeys for path in journey["nativeSpecPaths"]},
            {current_spec},
        )

        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir) / "source"
            scripts = root / "scripts"
            (scripts / "verify-ui/specs").mkdir(parents=True)
            (scripts / "playwright").mkdir(parents=True)
            (scripts / "loom-dev.mjs").write_text("export const devJourneySource = true;\n")
            current_spec_file = root / current_spec
            stale_spec_file = root / stale_spec
            current_spec_file.write_text("export {}; // current dev journey spec\n")
            stale_spec_file.write_text("export {}; // historical dev journey spec\n")

            main_config = scripts / "playwright.config.mjs"
            main_config.write_text("export default {};\n")
            benchmark_config = scripts / "measurements/construction-preview/construction-preview-bench.config.mjs"
            benchmark_config.parent.mkdir(parents=True)
            benchmark_config.write_text("export default {};\n")

            registry = scripts / "verify-ui/registry.mjs"
            registry.write_text(
                "export const registry = [];\n"
                "export const caseNamesFor = () => [];\n"
                "export const scenarioCaseFor = () => ({ requiredChecks: [] });\n"
            )

            registrations = []
            seen_registrations = set()
            for journey in expected_journeys:
                for case in journey["nativeCases"]:
                    binding = case["discoveryBinding"]
                    registration = (binding["testTitle"], binding["matchingTestIDs"][0])
                    if registration not in seen_registrations:
                        seen_registrations.add(registration)
                        registrations.append({
                            "title": registration[0],
                            "id": registration[1],
                            "line": 1,
                            "column": 1,
                            "occurrence": 1,
                        })
            self.assertEqual(len(registrations), 9)

            inventory = root / "runner-inventory.json"
            write_json(inventory, {"snapshotCommit": "fixture", "browserLauncherConsumers": [], "launcherImplementations": []})
            discovery = root / "discovery.json"
            write_json(discovery, {
                "status": "discovery-only",
                "runtimeStatus": "not-run",
                "sessions": [
                    {
                        "sessionID": "main",
                        "configPath": "scripts/playwright.config.mjs",
                        "configSha256": sha256(main_config),
                        "testCount": len(registrations) * 2,
                        "specFileCount": 2,
                        "specs": [
                            {"path": current_spec, "sha256": sha256(current_spec_file), "cases": registrations},
                            {"path": stale_spec, "sha256": sha256(stale_spec_file), "cases": registrations},
                        ],
                    },
                    {
                        "sessionID": "construction-preview-bench",
                        "configPath": "scripts/measurements/construction-preview/construction-preview-bench.config.mjs",
                        "configSha256": sha256(benchmark_config),
                        "testCount": 0,
                        "specFileCount": 0,
                        "specs": [],
                    },
                ],
            })

            # Reproduce the retained map's obsolete spec path while keeping the
            # current manifest rows and bindings as the generation baseline.
            stale_group = deepcopy(previous_group)
            for journey in stale_group["journeys"]:
                journey["nativeSpecPaths"] = [stale_spec]
                for case in journey["nativeCases"]:
                    case["nativeSpecPaths"] = [stale_spec]
                    case["discoveryBinding"]["specPath"] = stale_spec
            embedded_map = root / "embedded-map.json"
            write_json(embedded_map, {
                "sourcePath": "scripts/loom-dev.mjs",
                "journeys": stale_group["journeys"],
            })

            worker_map = root / "empty-worker-map.json"
            write_json(worker_map, {"sources": []})
            overrides = root / "discovery-overrides.json"
            write_json(overrides, {"cases": []})
            preimages = root / "source-preimages.json"
            write_json(preimages, {})
            output = root / "fresh-output.json"
            markdown = root / "fresh-output.md"

            command = [
                sys.executable,
                str(BUILDER),
                "--source-root", str(root),
                "--runner-inventory", str(inventory),
                "--worker-map", str(worker_map),
                "--embedded-map", str(embedded_map),
                "--discovery", str(discovery),
                "--discovery-overrides", str(overrides),
                "--registry", str(registry),
                "--preimages", str(preimages),
                "--output", str(output),
                "--markdown-output", str(markdown),
            ]
            self.assertFalse(output.exists())
            result = subprocess.run(command, cwd=REPOSITORY_ROOT, text=True, capture_output=True, check=False)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

            manifest = json.loads(output.read_text())
            generated_group = next(
                item for item in manifest["additionalBrowserWorkflowSources"]
                if item.get("sourcePath") == "scripts/loom-dev.mjs"
            )
            generated_journeys = generated_group["journeys"]
            self.assertEqual(len(generated_journeys), 9)
            for expected, actual in zip(expected_journeys, generated_journeys):
                self.assertEqual(actual["journeyID"], expected["journeyID"])
                self.assertEqual(actual["function"], expected["function"])
                self.assertEqual(actual["commands"], expected["commands"])
                self.assertEqual(actual["nativeSpecPaths"], [current_spec])
                self.assertFalse(actual["sourceLaunchStillPresent"])
                self.assertEqual(actual["runtimeEvidence"]["status"], "not-run")
                self.assertEqual(actual["legacyBrowserOwnershipRemoved"], expected["legacyBrowserOwnershipRemoved"])
                self.assertEqual(len(actual["nativeCases"]), len(expected["nativeCases"]))
                for expected_case, actual_case in zip(expected["nativeCases"], actual["nativeCases"]):
                    self.assertEqual(actual_case["scenarioID"], expected_case["scenarioID"])
                    self.assertEqual(actual_case["caseTitle"], expected_case["caseTitle"])
                    self.assertEqual(actual_case["caseName"], expected_case["caseName"])
                    self.assertEqual(actual_case["preservedAssertions"], expected_case["preservedAssertions"])
                    self.assertEqual(actual_case.get("fixtureVariant"), expected_case.get("fixtureVariant"))
                    self.assertEqual(actual_case["expectedVariants"], expected_case["expectedVariants"])
                    self.assertEqual(actual_case["discoveryBinding"], expected_case["discoveryBinding"])
            self.assertEqual(manifest["counts"]["mappedEmbeddedBrowserJourneys"], 9)
            self.assertEqual(manifest["counts"]["pendingEmbeddedBrowserJourneys"], 0)
            self.assertNotIn(current_spec, manifest["sourceScope"]["orphanNativeSpecs"])
            self.assertEqual(manifest["counts"]["orphanNativeSpecs"], 0)
            self.assertEqual(manifest["counts"]["mappedNativePlaywrightSpecFiles"], 1)
            self.assertEqual(manifest["runtimeEvidenceStatus"], "not-run")

            def runtime_evidence_statuses(value: object) -> list[str]:
                statuses = []
                if isinstance(value, dict):
                    runtime = value.get("runtimeEvidence")
                    if isinstance(runtime, dict) and "status" in runtime:
                        statuses.append(runtime["status"])
                    for child in value.values():
                        statuses.extend(runtime_evidence_statuses(child))
                elif isinstance(value, list):
                    for child in value:
                        statuses.extend(runtime_evidence_statuses(child))
                return statuses

            self.assertTrue(runtime_evidence_statuses(manifest))
            self.assertEqual(set(runtime_evidence_statuses(manifest)), {"not-run"})


if __name__ == "__main__":
    unittest.main()
