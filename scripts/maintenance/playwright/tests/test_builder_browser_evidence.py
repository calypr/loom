from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Callable


REPOSITORY_ROOT = Path(
    os.environ.get("LOOM_BROWSER_EVIDENCE_REPOSITORY_ROOT", Path(__file__).resolve().parents[4])
).resolve()
GENERATOR = Path(os.environ.get(
    "LOOM_BROWSER_EVIDENCE_GENERATOR",
    REPOSITORY_ROOT / "scripts/generate-builder-browser-evidence.mjs",
)).resolve()
AUDIT_PATH = Path("verification/builder-interactions/builder-journey-audit.json")
EVIDENCE_PATH = Path("verification/builder-interactions/browser-evidence.json")
RUNTIME_ROOT = Path("docs/verification/playwright/runtime/builder-group-entry-20261023")
SOURCE_AFTER = RUNTIME_ROOT / "final-908/verification-freeze/source-after.json"
RAW_NATIVE_REPORT = Path(
    "docs/verification/playwright/runtime/builder-group-entry-20261023/"
    "final-908/raw-basic-alias/native-report.json"
)
RAW_ALIAS_RUN = "nativeRawBasicAlias908"
EXPECTED_RAW_TRACE = "builder-authoring:group-entry-raw-basic"


def link_or_copy(source: str | Path, destination: str | Path) -> str:
    if Path(destination).exists():
        return str(destination)
    try:
        os.link(source, destination)
    except OSError:
        shutil.copy2(source, destination)
    return str(destination)


def write_text_atomically(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_path = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            stream.write(content)
        os.replace(temporary_path, path)
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)


def write_bytes_atomically(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_path = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(content)
        os.replace(temporary_path, path)
    finally:
        if os.path.exists(temporary_path):
            os.unlink(temporary_path)


def write_json(path: Path, value: object) -> None:
    write_text_atomically(path, json.dumps(value, indent=2) + "\n")


def read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def journey_audit(root: Path) -> tuple[dict, dict]:
    audit = read_json(root / AUDIT_PATH)
    journey = next(item for item in audit["journeys"] if item["id"] == "edit-remove-reload")
    return audit, journey


def retained_run(root: Path, run_id: str) -> tuple[dict, dict]:
    audit, journey = journey_audit(root)
    return audit, journey["evidence"][run_id]


def artifact_path(root: Path, run_id: str, artifact_name: str) -> Path:
    _, run = retained_run(root, run_id)
    if artifact_name.startswith("freeze."):
        phase = artifact_name.removeprefix("freeze.")
        descriptor = run["verificationFreeze"]["artifacts"][phase]
        return root / descriptor["path"]
    descriptor = run.get("artifacts", {}).get(artifact_name, run.get(artifact_name))
    return root / descriptor["path"]


def native_source_manifest(root: Path, run_id: str = RAW_ALIAS_RUN) -> dict[str, str]:
    native_path = artifact_path(root, run_id, "nativeReport")
    return read_json(native_path)["sourceFingerprintManifest"]["before"]


def proof_scope_fingerprint(contract_commit: str) -> str:
    contract_path = "verification/builder-interactions/builder-preview-contract.json"
    contract_bytes = subprocess.check_output(
        ["git", "-C", str(REPOSITORY_ROOT), "show", f"{contract_commit}:{contract_path}"]
    )
    contract = json.loads(contract_bytes)
    entries = []
    for source in contract["sources"]:
        source_bytes = subprocess.check_output(
            ["git", "-C", str(REPOSITORY_ROOT), "show", f"{contract_commit}:{source['path']}"]
        )
        entries.append({
            "path": source["path"],
            "sha256": hashlib.sha256(source_bytes).hexdigest(),
        })
    script = """import { createHash } from 'node:crypto';
let input = ''; for await (const chunk of process.stdin) input += chunk;
const entries = JSON.parse(input);
entries.sort((left, right) => left.path.localeCompare(right.path));
const canonical = value => Array.isArray(value) ? '[' + value.map(canonical).join(',') + ']' : value && typeof value === 'object' ? '{' + Object.keys(value).sort().map(key => JSON.stringify(key) + ':' + canonical(value[key])).join(',') + '}' : JSON.stringify(value);
process.stdout.write(createHash('sha256').update(canonical(entries)).digest('hex'));"""
    return subprocess.check_output(
        ["node", "--input-type=module", "-e", script],
        input=json.dumps(entries),
        text=True,
    )


def refresh_artifact_hash(root: Path, run_id: str, artifact_name: str) -> None:
    audit, journey = journey_audit(root)
    run = journey["evidence"][run_id]
    descriptor = run.get("artifacts", {}).get(artifact_name, run.get(artifact_name))
    descriptor["sha256"] = sha256(root / descriptor["path"])
    write_json(root / AUDIT_PATH, audit)


def refresh_freeze_hash(root: Path, run_id: str, phase: str) -> None:
    audit, journey = journey_audit(root)
    reference = journey["evidence"][run_id]["verificationFreeze"]["artifacts"][phase]
    reference["sha256"] = sha256(root / reference["path"])
    write_json(root / AUDIT_PATH, audit)


def mutate_json(path: Path, mutation: Callable[[dict], None]) -> dict:
    value = read_json(path)
    mutation(value)
    write_json(path, value)
    return value


def registry_checks(root: Path, scenario: str, case_name: str, custom: bool) -> list[str]:
    registry_url = (root / "scripts/verify-ui/registry.mjs").resolve().as_uri()
    script = """const registry = await import(process.argv[1]);
const checks = registry.scenarioCaseFor(process.argv[2], process.argv[3], process.argv[4] === 'true').requiredChecks;
process.stdout.write(JSON.stringify(checks));"""
    output = subprocess.check_output(
        ["node", "--input-type=module", "-e", script, registry_url, scenario, case_name, str(custom).lower()],
        cwd=REPOSITORY_ROOT,
        text=True,
    )
    return json.loads(output)


def replace_string_values(value: object, original: str, replacement: str) -> None:
    if isinstance(value, dict):
        for key, nested in value.items():
            if nested == original:
                value[key] = replacement
            else:
                replace_string_values(nested, original, replacement)
    elif isinstance(value, list):
        for index, nested in enumerate(value):
            if nested == original:
                value[index] = replacement
            else:
                replace_string_values(nested, original, replacement)


def copy_fixture_inputs(destination: Path) -> None:
    destination.mkdir(parents=True)
    source_manifest = read_json(REPOSITORY_ROOT / SOURCE_AFTER)["manifest"]
    source_audit = read_json(REPOSITORY_ROOT / AUDIT_PATH)
    journey = next(item for item in source_audit["journeys"] if item["id"] == "edit-remove-reload")
    runs = journey["evidence"]
    raw_alias_commit = runs[RAW_ALIAS_RUN]["sourceIdentity"]["commit"]

    for relative_path, expected_hash in source_manifest.items():
        source = REPOSITORY_ROOT / relative_path
        target = destination / relative_path
        target.parent.mkdir(parents=True, exist_ok=True)
        if source.is_file() and sha256(source) == expected_hash:
            link_or_copy(source, target)
            continue
        source_bytes = subprocess.check_output(
            ["git", "-C", str(REPOSITORY_ROOT), "show", f"{raw_alias_commit}:{relative_path}"]
        )
        actual_hash = hashlib.sha256(source_bytes).hexdigest()
        if actual_hash != expected_hash:
            raise AssertionError(
                f"pinned 908 source blob hash mismatch for {relative_path}: "
                f"expected {expected_hash}, found {actual_hash}"
            )
        write_bytes_atomically(target, source_bytes)

    shutil.copytree(
        REPOSITORY_ROOT / RUNTIME_ROOT,
        destination / RUNTIME_ROOT,
        copy_function=link_or_copy,
    )

    contract_path = "verification/builder-interactions/builder-preview-contract.json"
    pinned_contract = subprocess.check_output(
        ["git", "-C", str(REPOSITORY_ROOT), "show", f"{raw_alias_commit}:{contract_path}"]
    )
    contract = json.loads(pinned_contract)
    for source in contract["sources"]:
        relative_path = source["path"]
        if relative_path in source_manifest and source_manifest[relative_path] != source["sha256"]:
            raise AssertionError(
                f"908 watched/proof manifests disagree for {relative_path}: "
                f"{source_manifest[relative_path]} vs {source['sha256']}"
            )
        target = destination / relative_path
        if target.is_file() and sha256(target) == source["sha256"]:
            continue
        source_bytes = subprocess.check_output(
            ["git", "-C", str(REPOSITORY_ROOT), "show", f"{raw_alias_commit}:{relative_path}"]
        )
        actual_hash = hashlib.sha256(source_bytes).hexdigest()
        if actual_hash != source["sha256"]:
            raise AssertionError(
                f"pinned 908 proof-source blob hash mismatch for {relative_path}: "
                f"expected {source['sha256']}, found {actual_hash}"
            )
        write_bytes_atomically(target, source_bytes)

    contract_target = destination / contract_path
    write_bytes_atomically(contract_target, pinned_contract)

    for relative_path in (
        AUDIT_PATH,
        EVIDENCE_PATH,
        Path("verification/builder-interactions/builder-journey-contract.json"),
        Path("verification/builder-interactions/cda-mini-journeys.json"),
    ):
        source = REPOSITORY_ROOT / relative_path
        target = destination / relative_path
        current_bytes = source.read_bytes()
        if not target.is_file() or target.read_bytes() != current_bytes:
            write_bytes_atomically(target, current_bytes)


class BuilderBrowserEvidenceNegativeControlsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.temporary_directory = tempfile.TemporaryDirectory(
            prefix="loom-browser-evidence-fixtures-",
        )
        cls.temporary_root = Path(cls.temporary_directory.name)
        cls.baseline = cls.temporary_root / "baseline"
        copy_fixture_inputs(cls.baseline)

    @classmethod
    def tearDownClass(cls) -> None:
        cls.temporary_directory.cleanup()

    def fixture(self, name: str) -> Path:
        destination = self.temporary_root / name
        shutil.copytree(self.baseline, destination, copy_function=link_or_copy)
        return destination

    def run_generator(self, root: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                "node",
                str(GENERATOR),
                "--root",
                str(root),
                "--output",
                "generated-browser-evidence.json",
                *arguments,
            ],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )

    def assert_rejected(self, root: Path, diagnostic: str) -> None:
        result = self.run_generator(root)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(diagnostic, result.stdout + result.stderr)

    def test_retained_generation_is_byte_stable_and_keeps_status_layers_separate(self) -> None:
        root = self.fixture("stable")
        result = self.run_generator(root, "--report", RAW_NATIVE_REPORT.as_posix())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        output = root / "generated-browser-evidence.json"
        first_generation = output.read_bytes()

        repeated = self.run_generator(root, "--report", RAW_NATIVE_REPORT.as_posix())
        self.assertEqual(repeated.returncode, 0, repeated.stdout + repeated.stderr)
        self.assertEqual(output.read_bytes(), first_generation)

        evidence = json.loads(first_generation)
        current_route = next(
            case for case in evidence["cases"]
            if case["id"] == EXPECTED_RAW_TRACE
        )
        self.assertEqual(current_route["status"], "current")
        self.assertEqual(current_route["sourceStatus"], "current")

        raw_alias = next(
            run for run in evidence["retainedJourneys"]
            if run["runId"] == "raw-basic-alias-908"
        )
        self.assertEqual(raw_alias["status"], "historical")
        self.assertIn(raw_alias["sourceStatus"], {"current", "stale"})
        if raw_alias["sourceStatus"] == "stale":
            self.assertTrue(raw_alias["sourceStatusReason"])
        retained_rows = evidence["retainedJourneys"]
        self.assertEqual(
            {run["runId"] for run in retained_rows},
            {"authored-three", "raw-basic", "raw-basic-alias-908", "basic-budget-28"},
        )
        self.assertEqual(len({run["id"] for run in retained_rows}), 4)
        for run in retained_rows:
            self.assertEqual(run["status"], "historical")
            self.assertIn(run["sourceStatus"], {"current", "stale"})
            if run["sourceStatus"] == "stale":
                self.assertTrue(run["sourceStatusReason"])

        budget = next(
            run for run in evidence["retainedJourneys"]
            if run["runId"] == "basic-budget-28"
        )
        budget_source = read_json(artifact_path(root, "manualBasicBudgetProbe908", "report"))
        expected_budget = budget_source["journey"]["backendCallCount"]
        lower, upper = budget["budget"]["requiredRange"]
        self.assertEqual(budget["budget"]["backendResponses"], expected_budget)
        self.assertIsNone(budget.get("nativeCase"))
        self.assertNotIn("assertions", budget)
        self.assertGreaterEqual(expected_budget, lower)
        self.assertLessEqual(expected_budget, upper)
        self.assertEqual(
            budget["budget"]["requiredRange"],
            retained_run(root, "manualBasicBudgetProbe908")[1]["counts"]["requiredRange"],
        )

        legacy = next(case for case in evidence["cases"] if case["id"] == "builder-controls:recompile")
        self.assertEqual(legacy["status"], "unavailable")
        self.assertEqual(legacy["sourceStatus"], "unavailable")
        self.assertIn("excluded from current route coverage", legacy["historicalMetadata"]["authority"])
        legacy_report = legacy["historicalMetadata"]["recordedReport"]
        self.assertTrue(legacy["coveredRoutes"])
        self.assertTrue(legacy["historicalMetadata"]["recordedReportSha256"])
        self.assertFalse((root / legacy_report).exists())
        self.assertEqual(evidence["boundaries"]["cda"]["status"], "not-run")

        require_current = self.run_generator(root, "--require-current")
        diagnostic = require_current.stdout + require_current.stderr
        self.assertNotEqual(require_current.returncode, 0, diagnostic)
        self.assertIn("--require-current rejected", diagnostic)
        for run in retained_rows:
            self.assertIn(run["id"], diagnostic)
        self.assertIn("builder-controls:recompile", diagnostic)

    def test_from_evidence_missing_report_fails_without_changing_output(self) -> None:
        root = self.fixture("from-evidence-missing-report")
        report_path = root / RAW_NATIVE_REPORT
        generated = self.run_generator(root, "--report", RAW_NATIVE_REPORT.as_posix())
        self.assertEqual(generated.returncode, 0, generated.stdout + generated.stderr)
        output = root / "generated-browser-evidence.json"
        original_output = output.read_bytes()
        self.assertTrue(report_path.exists())

        report_path.unlink()
        result = self.run_generator(root, "--from-evidence")
        diagnostic = result.stdout + result.stderr
        self.assertNotEqual(result.returncode, 0, diagnostic)
        self.assertIn(
            "--from-evidence report path is missing: " + RAW_NATIVE_REPORT.as_posix(),
            diagnostic,
        )
        self.assertEqual(output.read_bytes(), original_output)

    def test_direct_report_input_rejects_a_missing_registered_assertion_by_name(self) -> None:
        root = self.fixture("direct-report-missing-assertion")
        report_path = root / RAW_NATIVE_REPORT
        report = read_json(report_path)
        missing_check = report["requiredChecks"][0]
        mutate_json(
            report_path,
            lambda value: value.update(
                assertions=[item for item in value["assertions"] if item["name"] != missing_check]
            ),
        )

        result = self.run_generator(root, "--report", RAW_NATIVE_REPORT.as_posix())
        diagnostic = result.stdout + result.stderr
        self.assertNotEqual(result.returncode, 0, diagnostic)
        self.assertIn("registered required check has no native assertion", diagnostic)
        self.assertIn(missing_check, diagnostic)

    def test_direct_read_only_custom_report_uses_custom_registered_check_variant(self) -> None:
        root = self.fixture("direct-read-only-custom-report")
        source_report = root / RAW_NATIVE_REPORT
        report_path = root / "custom-current-report.json"
        report = read_json(source_report)
        custom_checks = registry_checks(root, "viewer-query", "output", custom=True)
        owned_checks = registry_checks(root, "viewer-query", "output", custom=False)
        self.assertNotEqual(custom_checks, owned_checks)
        report["scenario"] = "viewer-query"
        report["case"] = "output"
        report["target"]["kind"] = "read-only-custom"
        report["requiredChecks"] = custom_checks
        report["missingRequiredChecks"] = []
        report["assertions"] = [
            {"name": name, "status": "passed"}
            for name in custom_checks
        ]
        write_json(report_path, report)

        result = self.run_generator(root, "--report", report_path.relative_to(root).as_posix())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = read_json(root / "generated-browser-evidence.json")
        custom_case = next(case for case in evidence["cases"] if case["id"] == "viewer-query:output")
        self.assertEqual(custom_case["status"], "current")
        self.assertEqual(custom_case["sourceStatus"], "current")
        self.assertEqual({item["name"] for item in custom_case["assertions"]}, set(custom_checks))

    def test_direct_read_only_custom_report_rejects_missing_custom_assertion_by_name(self) -> None:
        root = self.fixture("direct-read-only-custom-missing-assertion")
        source_report = root / RAW_NATIVE_REPORT
        report_path = root / "custom-current-report.json"
        report = read_json(source_report)
        custom_checks = registry_checks(root, "viewer-query", "output", custom=True)
        missing_check = custom_checks[-1]
        report["scenario"] = "viewer-query"
        report["case"] = "output"
        report["target"]["kind"] = "read-only-custom"
        report["requiredChecks"] = custom_checks
        report["missingRequiredChecks"] = []
        report["assertions"] = [
            {"name": name, "status": "passed"}
            for name in custom_checks
            if name != missing_check
        ]
        write_json(report_path, report)

        result = self.run_generator(root, "--report", report_path.relative_to(root).as_posix())
        diagnostic = result.stdout + result.stderr
        self.assertNotEqual(result.returncode, 0, diagnostic)
        self.assertIn("registered required check has no native assertion", diagnostic)
        self.assertIn(missing_check, diagnostic)

    def test_missing_registered_assertion_is_rejected_after_report_rehash(self) -> None:
        root = self.fixture("missing-required-assertion")
        native_path = artifact_path(root, RAW_ALIAS_RUN, "nativeReport")
        native = read_json(native_path)
        missing_check = native["requiredChecks"][0]
        mutate_json(
            native_path,
            lambda report: report.update(
                assertions=[item for item in report["assertions"] if item["name"] != missing_check]
            ),
        )
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "nativeReport")
        self.assert_rejected(
            root,
            f"retained run raw-basic-alias-908: registered required check has no native assertion: {missing_check}",
        )

    def test_failed_required_assertion_is_rejected_after_report_rehash(self) -> None:
        root = self.fixture("failed-required-assertion")
        native_path = artifact_path(root, RAW_ALIAS_RUN, "nativeReport")
        native = read_json(native_path)
        failed_check = native["requiredChecks"][0]

        def fail_assertion(report: dict) -> None:
            assertion = next(item for item in report["assertions"] if item["name"] == failed_check)
            assertion["status"] = "failed"

        mutate_json(native_path, fail_assertion)
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "nativeReport")
        self.assert_rejected(
            root,
            f"retained run raw-basic-alias-908: native assertion failed: {failed_check}",
        )

    def test_wrong_native_case_identity_is_rejected_after_report_rehash(self) -> None:
        root = self.fixture("wrong-case-identity")
        native_path = artifact_path(root, RAW_ALIAS_RUN, "nativeReport")
        mutate_json(native_path, lambda report: report.update(case="group-entry-other"))
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "nativeReport")
        self.assert_rejected(root, "retained run raw-basic-alias-908: native case identity mismatch")

    def test_wrong_trace_journey_identity_is_rejected_after_trace_rehash(self) -> None:
        root = self.fixture("wrong-journey-identity")
        trace_path = artifact_path(root, RAW_ALIAS_RUN, "trace")

        def rename_trace(trace_document: dict) -> None:
            trace_document["traces"][0]["name"] = "builder-authoring:wrong-journey"

        mutate_json(trace_path, rename_trace)
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "trace")
        self.assert_rejected(
            root,
            f"retained run raw-basic-alias-908: trace/TLA journey identity mismatch; expected {EXPECTED_RAW_TRACE}",
        )

    def test_audited_trace_name_cannot_override_the_tested_registry(self) -> None:
        root = self.fixture("wrong-audited-trace-name")
        wrong_name = "builder-authoring:wrong-journey"
        audit, journey = journey_audit(root)
        run = journey["evidence"][RAW_ALIAS_RUN]
        run["traceMutationControls"]["positiveTraceName"] = wrong_name
        run["selectedNativeCase"]["traceName"] = wrong_name
        write_json(root / AUDIT_PATH, audit)

        for artifact_name in ("trace", "tlaReport", "journeyReport"):
            path = artifact_path(root, RAW_ALIAS_RUN, artifact_name)
            document = read_json(path)
            replace_string_values(document, EXPECTED_RAW_TRACE, wrong_name)
            write_json(path, document)
            refresh_artifact_hash(root, RAW_ALIAS_RUN, artifact_name)

        self.assert_rejected(
            root,
            "retained run raw-basic-alias-908: audited trace name differs from tested registry: "
            + f"expected {EXPECTED_RAW_TRACE}, recorded {wrong_name}",
        )

    def test_missing_registered_repeat_edit_action_is_rejected_after_trace_and_receipt_rehash(self) -> None:
        root = self.fixture("missing-repeat-edit")
        trace_path = artifact_path(root, RAW_ALIAS_RUN, "trace")
        trace = read_json(trace_path)
        actions = trace["traces"][0]["actions"]
        trace["traces"][0]["actions"] = [action for action in actions if action["label"] != "EditGroupAgain"]
        write_json(trace_path, trace)

        tla_path = artifact_path(root, RAW_ALIAS_RUN, "tlaReport")
        mutate_json(
            tla_path,
            lambda report: report["traces"][0].update(actionCount=len(trace["traces"][0]["actions"])),
        )
        audit, journey = journey_audit(root)
        journey["evidence"][RAW_ALIAS_RUN]["counts"]["traceActionCount"] = len(trace["traces"][0]["actions"])
        write_json(root / AUDIT_PATH, audit)
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "trace")
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "tlaReport")
        self.assert_rejected(
            root,
            "retained run raw-basic-alias-908: retained trace is missing registered required action EditGroupAgain",
        )

    def test_failed_tla_receipt_is_rejected_after_receipt_rehash(self) -> None:
        root = self.fixture("failed-tla-receipt")
        tla_path = artifact_path(root, RAW_ALIAS_RUN, "tlaReport")
        mutate_json(tla_path, lambda report: report["traces"][0].update(status="fail"))
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "tlaReport")
        self.assert_rejected(
            root,
            "retained run raw-basic-alias-908: TLA receipt status is fail, expected pass",
        )

    def test_mismatched_tla_source_is_rejected_after_receipt_rehash(self) -> None:
        root = self.fixture("mismatched-tla-source")
        tla_path = artifact_path(root, RAW_ALIAS_RUN, "tlaReport")
        mutate_json(
            tla_path,
            lambda report: report.update(sourceFingerprint="a" * 64),
        )
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "tlaReport")
        self.assert_rejected(
            root,
            "retained run raw-basic-alias-908: trace/TLA source fingerprint does not match retained source identity",
        )

    def test_missing_required_tla_event_is_rejected_after_receipt_rehash(self) -> None:
        root = self.fixture("missing-required-tla-event")
        contract = read_json(root / "verification/builder-interactions/builder-journey-contract.json")
        missing_event = contract["phase1"]["requiredEvents"][0]
        tla_path = artifact_path(root, RAW_ALIAS_RUN, "tlaReport")

        def remove_required_event_coverage(receipt: dict) -> None:
            coverage = receipt.setdefault("coverage", {})
            coverage.setdefault("requiredEvents", {})["missing"] = [missing_event]

        mutate_json(tla_path, remove_required_event_coverage)
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "tlaReport")
        self.assert_rejected(
            root,
            "TLA receipt is missing required events: " + missing_event,
        )

    def test_wrong_historical_source_scope_rejects_after_all_artifact_rehashes(self) -> None:
        root = self.fixture("wrong-historical-source-scope")
        audit, journey = journey_audit(root)
        run = journey["evidence"][RAW_ALIAS_RUN]
        trace_path = artifact_path(root, RAW_ALIAS_RUN, "trace")
        tla_path = artifact_path(root, RAW_ALIAS_RUN, "tlaReport")
        report_path = artifact_path(root, RAW_ALIAS_RUN, "journeyReport")
        trace = read_json(trace_path)
        wrong_fingerprint = proof_scope_fingerprint(trace["contractSourceCommit"])
        self.assertNotEqual(wrong_fingerprint, run["sourceIdentity"]["proofSourceFingerprint"])
        run["sourceIdentity"]["proofSourceFingerprint"] = wrong_fingerprint

        trace["sourceFingerprint"] = wrong_fingerprint
        write_json(trace_path, trace)
        mutate_json(tla_path, lambda receipt: receipt.update(sourceFingerprint=wrong_fingerprint))

        def substitute_report_scope(report: dict) -> None:
            report["sourceFingerprint"] = wrong_fingerprint
            report["tla"]["sourceFingerprint"] = wrong_fingerprint

        mutate_json(report_path, substitute_report_scope)
        write_json(root / AUDIT_PATH, audit)
        for artifact_name in ("trace", "tlaReport", "journeyReport"):
            refresh_artifact_hash(root, RAW_ALIAS_RUN, artifact_name)

        result = self.run_generator(root)
        diagnostic = result.stdout + result.stderr
        self.assertNotEqual(result.returncode, 0, diagnostic)
        self.assertIn(
            "retained proof fingerprint contradicts the verified 76-file historical source scope",
            diagnostic,
        )
        self.assertIn("recorded " + wrong_fingerprint, diagnostic)

    def test_unexplained_redaction_on_a_nonsensitive_source_path_is_rejected(self) -> None:
        root = self.fixture("unexplained-source-redaction")
        native_path = artifact_path(root, RAW_ALIAS_RUN, "nativeReport")
        manifest_before = native_source_manifest(root)
        nonsensitive_path = next(
            path for path, value in manifest_before.items() if value != "[REDACTED]"
        )

        def redact_nonsensitive_path(report: dict) -> None:
            manifest = report["sourceFingerprintManifest"]
            manifest["before"][nonsensitive_path] = "[REDACTED]"
            manifest["after"][nonsensitive_path] = "[REDACTED]"

        mutate_json(native_path, redact_nonsensitive_path)
        refresh_artifact_hash(root, RAW_ALIAS_RUN, "nativeReport")
        self.assert_rejected(
            root,
            "native source manifest redaction is not explained by the tested sanitizer: "
            + nonsensitive_path,
        )

    def test_redacted_freeze_entry_must_match_its_tested_git_blob(self) -> None:
        root = self.fixture("wrong-redacted-freeze-hash")
        original_manifest = native_source_manifest(root)
        redacted_path = next(
            path for path, value in original_manifest.items() if value == "[REDACTED]"
        )

        for phase in ("source-before", "source-after"):
            freeze_path = artifact_path(root, RAW_ALIAS_RUN, "freeze." + phase)
            mutate_json(
                freeze_path,
                lambda capture: capture["manifest"].update({redacted_path: "f" * 64}),
            )
            refresh_freeze_hash(root, RAW_ALIAS_RUN, phase)

        self.assert_rejected(
            root,
            "freeze manifest hash for redacted path does not match tested commit: " + redacted_path,
        )

    def test_missing_retained_artifact_reports_its_path(self) -> None:
        root = self.fixture("missing-retained-artifact")
        missing_path = artifact_path(root, RAW_ALIAS_RUN, "tlaReport")
        missing_path.unlink()
        self.assert_rejected(
            root,
            f"retained run raw-basic-alias-908 tlaReport artifact is missing: {missing_path.relative_to(root).as_posix()}",
        )

    def test_source_change_is_explicitly_stale_and_require_current_rejects_it(self) -> None:
        root = self.fixture("stale-current-source")
        source_path = root / "scripts/verify-ui/workflows/builder-group-entry.mjs"
        original = source_path.read_text(encoding="utf-8")
        write_text_atomically(source_path, original + "\nexport const fixtureSourceMutation = true;\n")

        result = self.run_generator(root, "--report", RAW_NATIVE_REPORT.as_posix())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = read_json(root / "generated-browser-evidence.json")
        current_route = next(case for case in evidence["cases"] if case["id"] == EXPECTED_RAW_TRACE)
        self.assertEqual(current_route["status"], "historical")
        self.assertEqual(current_route["sourceStatus"], "stale")
        self.assertIn("differs from current watched-tree fingerprint", current_route["sourceStatusReason"])

        rejected = self.run_generator(
            root,
            "--report",
            RAW_NATIVE_REPORT.as_posix(),
            "--require-current",
        )
        self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
        self.assertIn("--require-current rejected evidence row(s)", rejected.stdout + rejected.stderr)
        self.assertIn(f"{EXPECTED_RAW_TRACE} (historical/stale:", rejected.stdout + rejected.stderr)

    def test_current_sanitizer_mutation_leaves_retained_proof_valid_but_stale(self) -> None:
        root = self.fixture("mutated-current-sanitizer")
        sanitizer_path = root / "scripts/verify-ui/helpers/playwright-browser.mjs"
        original = sanitizer_path.read_text(encoding="utf-8")
        write_text_atomically(
            sanitizer_path,
            original + "\nexport const fixtureSanitizerMutation = true;\n",
        )

        result = self.run_generator(root)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        evidence = read_json(root / "generated-browser-evidence.json")
        raw_alias = next(
            item for item in evidence["retainedJourneys"]
            if item["runId"] == "raw-basic-alias-908"
        )
        self.assertEqual(raw_alias["status"], "historical")
        self.assertEqual(raw_alias["sourceStatus"], "stale")
        self.assertTrue(raw_alias["sourceStatusReason"])

    def test_truncated_watched_fingerprints_are_rejected_as_a_recurrence_control(self) -> None:
        cases = (
            (
                "truncated-watched-source",
                lambda run: run["sourceIdentity"].update(watchedTreeFingerprint="9f47a6f4b3a9b4c97b25a6949f51f49fb7304c67afeb84cc95e6f0560484995"),
                "retained run raw-basic-alias-908: watched-tree fingerprint must be a full 64-character SHA-256",
            ),
            (
                "truncated-freeze-source",
                lambda run: run["verificationFreeze"]["sourceFingerprint"].update(sha256="9f47a6f4b3a9b4c97b25a6949f51f49fb7304c67afeb84cc95e6f0560484995"),
                "retained run raw-basic-alias-908: verification freeze source fingerprint does not match native report",
            ),
        )
        for name, mutation, diagnostic in cases:
            with self.subTest(name=name):
                root = self.fixture(name)
                audit, journey = journey_audit(root)
                mutation(journey["evidence"][RAW_ALIAS_RUN])
                write_json(root / AUDIT_PATH, audit)
                self.assert_rejected(root, diagnostic)

    def test_59_response_alias_cannot_replace_the_registered_budget_probe(self) -> None:
        root = self.fixture("budget-59-substitution")
        report_path = artifact_path(root, "manualBasicBudgetProbe908", "report")
        mutate_json(
            report_path,
            lambda report: report["journey"].update(backendCallCount=59),
        )
        audit, journey = journey_audit(root)
        budget = journey["evidence"]["manualBasicBudgetProbe908"]
        budget["counts"]["backendResponses"] = 59
        journey["manualBasicBudgetProbe"]["backendResponses"] = 59
        write_json(root / AUDIT_PATH, audit)
        refresh_artifact_hash(root, "manualBasicBudgetProbe908", "report")
        lower, upper = budget["counts"]["requiredRange"]
        self.assert_rejected(
            root,
            f"retained run basic-budget-28: budget backend-call count 59 is outside {lower}..{upper}",
        )

    def test_widened_budget_range_cannot_replace_the_source_commit_boundary(self) -> None:
        root = self.fixture("budget-range-source-substitution")
        report_path = artifact_path(root, "manualBasicBudgetProbe908", "report")
        mutate_json(
            report_path,
            lambda report: report["journey"].update(backendCallCount=59),
        )
        audit, journey = journey_audit(root)
        run = journey["evidence"]["manualBasicBudgetProbe908"]
        historical_range = journey["manualBasicBudgetProbe"]["backendResponseRange"]
        source_commit = run["sourceIdentity"]["commit"]
        widened_range = [10, 100]
        journey["manualBasicBudgetProbe"]["backendResponseRange"] = widened_range
        run["counts"]["requiredRange"] = widened_range
        run["counts"]["backendResponses"] = 59
        journey["manualBasicBudgetProbe"]["backendResponses"] = 59
        write_json(root / AUDIT_PATH, audit)
        refresh_artifact_hash(root, "manualBasicBudgetProbe908", "report")
        self.assert_rejected(
            root,
            "basic-budget-28: run budget range differs from historical audit source "
            + source_commit
            + ":verification/builder-interactions/builder-journey-audit.json: expected "
            + json.dumps(historical_range, separators=(",", ":"))
            + ", found "
            + json.dumps(widened_range, separators=(",", ":")),
        )

    def test_historical_recompile_blob_pin_rejects_a_mismatched_object_id(self) -> None:
        root = self.fixture("mismatched-legacy-blob")
        audit = read_json(root / AUDIT_PATH)
        pin = audit["browserEvidenceInputs"][0]["source"]
        pin["gitBlobObjectId"] = "0" * 40
        write_json(root / AUDIT_PATH, audit)
        self.assert_rejected(
            root,
            "historical Recompile Git blob object ID mismatch: expected " + "0" * 40,
        )


if __name__ == "__main__":
    unittest.main()
