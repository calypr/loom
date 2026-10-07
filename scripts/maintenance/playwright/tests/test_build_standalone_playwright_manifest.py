import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
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


if __name__ == "__main__":
    unittest.main()
