#!/usr/bin/env python3
"""Build a portable source-to-native-case ledger for standalone Playwright conversion."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
from pathlib import Path
from typing import Any

SCRIPTS_ROOT = Path(__file__).resolve().parents[2]
REPOSITORY_ROOT = SCRIPTS_ROOT.parent

REGISTERED_STANDALONE = {
    "scripts/verify-ui/workflows/population-member-removal-workflow.mjs": "registered population-member removal workflow",
    "scripts/verify-ui/workflows/root-quantity-pivot-workflow.mjs": "registered root quantity pivot workflow",
}

CDA_FIELDS_STANDALONE = {
    "scripts/verify-cda-coded-field-lifecycle-browser.mjs",
    "scripts/verify-cda-cohort-fields-browser.mjs",
    "scripts/verify-cda-compound-fields-browser.mjs",
    "scripts/verify-cda-contributor-code-browser.mjs",
    "scripts/verify-cda-contributor-exists-browser.mjs",
    "scripts/verify-cda-contributor-rules-browser.mjs",
    "scripts/verify-cda-source-fields-browser.mjs",
    "scripts/verify-cda-repeated-contributor-any-browser.mjs",
}

NON_BROWSER_CLASSIFICATION = {
    "scripts/verify-b07-faults.mjs": {
        "kind": "integration-cli",
        "disposition": "retained-api-integration-cli",
        "reason": "Drives bounded fault scenarios and validates existing development-stack evidence; it does not own a browser lifecycle.",
    },
    "scripts/verify-b08-artifacts.mjs": {
        "kind": "integration-cli",
        "disposition": "retained-api-integration-cli",
        "reason": "Validates published artifact behavior from the owned stack; it does not own a browser lifecycle.",
    },
    "scripts/verify-builder-reconciliation.mjs": {
        "kind": "unit-check-cli",
        "disposition": "retained-unit-check-cli",
        "reason": "Invokes a focused UI unit-contract command and records its result; it is not a browser workflow.",
    },
    "scripts/verify-ui/helpers/verify-cda-builder-table-management-contract.mjs": {
        "kind": "pure-oracle-helper",
        "disposition": "retained-pure-helper",
        "reason": "Exports action names and pure assertions consumed by the Builder table-management workflow.",
    },
    "scripts/verify-ui/workflows/verify-cda-builder.mjs": {
        "kind": "workflow-dispatcher",
        "disposition": "requires-explicit-port-or-removal",
        "reason": "Dispatches multiple Builder workflows from a CLI action; each caller role must be represented by native cases or removed after caller migration.",
    },
    "scripts/verify-cda-mixed-scope-selectors.mjs": {
        "kind": "api-only-cli",
        "disposition": "retained-api-only-cli",
        "reason": "Exercises API construction behavior and writes evidence without owning a browser lifecycle.",
    },
    "scripts/verify-cda-pivot-oracle.mjs": {
        "kind": "evidence-oracle-cli",
        "disposition": "retained-evidence-oracle-cli",
        "reason": "Checks independent assertions against captured Pivot browser evidence; it is an oracle, not a browser entrypoint.",
    },
    "scripts/verify-construction-removals.mjs": {
        "kind": "api-only-cli",
        "disposition": "retained-api-only-cli",
        "reason": "Proposes construction-removal API changes against an owned target and never applies them or opens a browser.",
    },
    "scripts/verify-correlated-concepts.mjs": {
        "kind": "api-only-cli",
        "disposition": "retained-api-only-cli",
        "reason": "Checks API output against captured owned-target evidence; it does not own a browser lifecycle.",
    },
    "scripts/verify-population-row.mjs": {
        "kind": "api-only-cli",
        "disposition": "retained-api-only-cli",
        "reason": "Checks population API behavior from captured selection evidence; it does not own a browser lifecycle.",
    },
    "scripts/verify-selections.mjs": {
        "kind": "api-only-cli",
        "disposition": "retained-api-only-cli",
        "reason": "Checks selection API behavior from captured owned-target evidence; it does not own a browser lifecycle.",
    },
}

OWNER_PATTERNS = (
    re.compile(
        r"\b(?:launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser)\s*\(|"
        r"\bimport\s*\{[^}]*\b(?:launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser)\b[^}]*\}|"
        r"\bexport\s+(?:async\s+)?(?:function|const|let)\s+(?:launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser)\b|"
        r"\b(?:chromium|firefox|webkit)\.(?:launch(?:PersistentContext)?|connect(?:OverCDP)?)\s*\(|"
        r"\bcdp\s*\.\s*send\s*\(|\bremote-debugging-port\b|chrome-remote-interface",
        re.S,
    ),
)
SOURCE_PATTERN = re.compile(r"^verify[-_].+\.mjs$")
INFRASTRUCTURE_SPEC_CLASSIFICATION = {
    "scripts/verify-ui/specs/native-failure-evidence.spec.mjs": {
        "kind": "native-harness-contract-spec",
        "reason": "Exercises bounded and redacted failure-evidence capture in isolation; it has no legacy browser workflow source and is not a domain acceptance case.",
    },
    "scripts/verify-ui/specs/native-action-readiness.spec.mjs": {
        "kind": "native-harness-contract-spec",
        "reason": "Exercises Playwright action-readiness helper behavior as infrastructure evidence, not a product acceptance lifecycle.",
    },
    "scripts/verify-ui/specs/native-authoring-oracles.spec.mjs": {
        "kind": "native-harness-contract-spec",
        "reason": "Exercises native authoring-oracle helpers as infrastructure evidence, not a product acceptance lifecycle.",
    },
    "scripts/verify-ui/specs/native-raw-fields-locator.spec.mjs": {
        "kind": "native-harness-contract-spec",
        "reason": "Exercises raw fields-locator selection as infrastructure evidence, not a product acceptance lifecycle.",
    },
    "scripts/verify-ui/specs/authoring-summary.spec.mjs": {
        "kind": "native-harness-contract-spec",
        "reason": "Checks exact native HTML summary locator behavior and strictness with an isolated page fixture; it is a helper contract with no legacy browser entrypoint or product acceptance lifecycle.",
    },
}
LOOM_DEV_COMMANDS = {
    "verifyCurrentBuilderDOM": ["verify-current"],
    "verifyJ01ExternalBrowserScenario": ["verify-j01 (CDA external-manifest branch)"],
    "verifyJ01BrowserScenario": ["verify-j01 (standard branch)"],
    "verifyJ02BrowserScenario": ["verify-j02"],
    "verifyJ03BrowserScenario": ["verify-j03"],
    "verifyJ04PatientOperatorScenario": ["verify-j04-patient"],
    "verifyJ04BrowserScenario": ["verify-j04"],
    "verifyJ05BrowserScenario": ["verify-j05"],
    "verifyBrowserScenario": ["verify-fast", "verify-full"],
}
ADDITIONAL_STANDALONE_BROWSER_SOURCE_PATHS = {
    "scripts/measurements/construction-preview/construction_preview_bench.mjs": {
        "launchPattern": re.compile(r"\blaunchBrowser\s*\("),
        "owner": "BrowserSession.start",
        "reason": "Standalone construction-preview benchmark browser workflow; tracked separately because it is outside the verify[-_]* inventory.",
    },
}
LEGACY_LAUNCHER_RETIREMENT_EVIDENCE = {
    "scripts/lib/playwright-cda-actions.mjs": {
        "preimageSha256": "0537044c88acd08f9877896f178543197b61fb78141c3c7f93e65d09d0148dfe",
        "preimageCommit": "009ce99cb959f8f12c71e88ddd818ece543ae537",
        "patchSha256": "c04bab4a2339306ee1bb27209089b166b9bbc5f785f677f92353527bb75d23c9",
        "reason": "The legacy CDA launch/action wrapper was removed after its browser workflows moved to official Playwright Test ownership.",
    },
}


def sha256(path: Path) -> str | None:
    if not path.is_file():
        return None
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def relpath(value: str | Path) -> str:
    return Path(value).as_posix().lstrip("./")


def portable_stage_location(path: Path) -> str:
    private_tmp = Path("/private/tmp")
    try:
        return path.resolve().relative_to(private_tmp).as_posix()
    except ValueError:
        return f"{path.parent.name}/{path.name}"


def source_paths(root: Path, inventory: dict[str, Any]) -> tuple[dict[str, Path], set[str]]:
    found: dict[str, Path] = {}
    scripts_root = root / "scripts"
    for source_dir in (scripts_root, scripts_root / "verify-ui" / "workflows", scripts_root / "verify-ui" / "helpers"):
        if not source_dir.is_dir():
            continue
        for path in source_dir.iterdir():
            if path.is_file() and SOURCE_PATTERN.match(path.name) and not path.name.endswith(".test.mjs"):
                found[path.relative_to(root).as_posix()] = path
    ui_paths = set()
    ui_root = root / "ui" / "packages"
    if ui_root.exists():
        for path in ui_root.rglob("*.mjs"):
            if path.is_file() and path.parent.name == "scripts" and SOURCE_PATTERN.match(path.name) and not path.name.endswith(".test.mjs"):
                relative = path.relative_to(root).as_posix()
                found[relative] = path
                ui_paths.add(relative)
    # The official runner snapshot predates some package-local scripts. Keep every
    # package-local standalone entrypoint discoverable even if it has no old row.
    for consumer in inventory.get("browserLauncherConsumers", []):
        rel = relpath(consumer.get("file", ""))
        if rel.startswith("ui/packages/") and SOURCE_PATTERN.match(Path(rel).name):
            candidate = root / rel
            if candidate.is_file():
                found[rel] = candidate
                ui_paths.add(rel)
    return found, ui_paths


def load_worker_map(path: Path) -> dict[str, dict[str, Any]]:
    data = json.loads(path.read_text())
    if isinstance(data.get("sourceFiles"), list) and data.get("nativeSpec"):
        normalized: dict[str, dict[str, Any]] = {}
        native_spec = data["nativeSpec"]
        native_spec_path = native_spec.get("path") if isinstance(native_spec, dict) else native_spec
        for source in data["sourceFiles"]:
            source_path = relpath(source.get("path", ""))
            if not source_path:
                continue
            cases = []
            for case in source.get("cases", []):
                case_row = dict(case)
                case_row["scenarioID"] = data.get("scenarioID")
                case_row["caseTitle"] = case_row.get("test") or case_row.get("testTitle") or case_row.get("caseTitle") or case_row.get("caseName")
                case_row["nativeSpecPaths"] = [relpath(native_spec_path)] if native_spec_path else []
                case_row["preservedAssertions"] = list(case_row.get("preservedAssertions", []))
                case_row["preservedAssertions"].extend(f"Raw oracle: {item}" for item in case_row.get("rawOracles", []))
                cases.append(case_row)
            normalized[source_path] = {
                "ownerPartition": "native-standalone-cda-cohort",
                "nativeSpecPaths": [relpath(native_spec_path)] if native_spec_path else [],
                "workflowPaths": [source_path],
                "nativeCases": cases,
                "legacyBrowserOwnershipRemoved": True,
                "runtimeEvidence": {"status": "not-run"},
                "reason": f"Imported from the isolated cohort case map: {source.get('workflowExport', 'native workflow')} retains mapped assertions and raw-oracle coverage.",
            }
        return normalized
    if isinstance(data.get("sources"), list) and data.get("nativeSpec"):
        normalized = {}
        native_spec_path = data.get("nativeSpec")
        for source in data["sources"]:
            source_path = relpath(source.get("sourceWorkflow", source.get("path", "")))
            if not source_path:
                continue
            case_row = dict(source)
            case_row["caseTitle"] = case_row.get("testTitle") or case_row.get("caseTitle") or case_row.get("caseName")
            case_row["nativeSpecPaths"] = [relpath(source.get("nativeSpec") or native_spec_path)]
            case_row["preservedAssertions"] = list(case_row.get("preservedAssertions", []))
            if case_row.get("rawOracle"):
                case_row["preservedAssertions"].append(f"Raw oracle: {case_row['rawOracle']}")
            normalized[source_path] = {
                "ownerPartition": "native-standalone-cda-rows",
                "nativeSpecPaths": case_row["nativeSpecPaths"],
                "workflowPaths": [source_path],
                "nativeCases": [case_row],
                "legacyBrowserOwnershipRemoved": True,
                "runtimeEvidence": {"status": "not-run"},
                "reason": f"Imported from the isolated CDA rows case map: {source.get('caseName', 'workflow')} preserves test and raw-oracle coverage.",
            }
        return normalized
    entries = data.get("sources", data)
    normalized: dict[str, dict[str, Any]] = {}
    if isinstance(entries, list):
        for item in entries:
            source = relpath(item.get("sourcePath", item.get("source", "")))
            if source:
                mapped = dict(item)
                for artifact_field in ("nativeSpecArtifacts", "workflowArtifacts", "oracleHelperArtifacts"):
                    if isinstance(mapped.get(artifact_field), list):
                        mapped.pop(artifact_field)
                if mapped.get("nativeReplacementNotRequired"):
                    mapped["noNativeReplacement"] = True
                disposition_evidence = mapped.get("dispositionEvidence")
                if isinstance(disposition_evidence, dict) and disposition_evidence.get("retirementEvidence"):
                    mapped.setdefault("retirementEvidence", disposition_evidence["retirementEvidence"])
                normalized[source] = mapped
    elif isinstance(entries, dict):
        for key, value in entries.items():
            if isinstance(value, str):
                value = {"nativeSpecPaths": [value]}
            elif isinstance(value, list):
                value = {"nativeSpecPaths": value}
            normalized[relpath(key)] = value
    return normalized


def merge_worker_maps(paths: list[Path]) -> tuple[dict[str, dict[str, Any]], list[dict[str, Any]]]:
    merged: dict[str, dict[str, Any]] = {}
    inputs = []
    list_fields = ("nativeSpecPaths", "workflowPaths", "oracleHelperPaths", "retainedOracleHelpers", "caseTitles", "domainScenarioIDs")
    for path in paths:
        source_map = load_worker_map(path)
        native_case_rows = [
            case
            for value in source_map.values()
            for case in value.get("nativeCases", [])
            if isinstance(case, dict)
        ]
        input_row = {
            "label": f"{path.parent.name}/{path.name}",
            "sha256": sha256(path),
            "sourceCount": len(source_map),
            "caseMappingRows": len(native_case_rows),
            "uniqueCaseTitles": len({case.get("caseTitle") for case in native_case_rows if case.get("caseTitle")}),
        }
        try:
            document = json.loads(path.read_text())
        except (OSError, json.JSONDecodeError):
            document = {}
        upstream_inputs = document.get("sourceScope", {}).get("sourceMapInputs", document.get("inputMaps", []))
        if upstream_inputs:
            for upstream in upstream_inputs:
                if isinstance(upstream, dict):
                    inputs.append(dict(upstream))
        elif document.get("schemaVersion") != 2:
            inputs.append(input_row)
        for source, incoming in source_map.items():
            current = merged.setdefault(source, {})
            for key, value in incoming.items():
                if key in list_fields and isinstance(value, list):
                    old = current.get(key, [])
                    current[key] = list(dict.fromkeys([*old, *value]))
                elif key == "nativeCases" and isinstance(value, list):
                    cases = current.setdefault(key, [])
                    case_identity = lambda item: (item.get("scenarioID"), item.get("caseName") if item.get("caseName") is not None else item.get("caseTitle"))
                    by_key = {case_identity(item): item for item in cases if isinstance(item, dict)}
                    for case in value:
                        case_key = case_identity(case) if isinstance(case, dict) else (None, None)
                        prior = by_key.get(case_key)
                        if prior is None:
                            cases.append(case)
                            by_key[case_key] = case
                        elif isinstance(case, dict):
                            combined = {**prior, **case}
                            combined["preservedAssertions"] = list(dict.fromkeys([*prior.get("preservedAssertions", []), *case.get("preservedAssertions", [])]))
                            if prior.get("caseVariants") and not case.get("caseVariants"):
                                combined["caseVariants"] = prior["caseVariants"]
                            cases[cases.index(prior)] = combined
                            by_key[case_key] = combined
                elif key in {"legacyBrowserOwnershipRemoved", "oldBrowserOwnershipRemoved"}:
                    current[key] = bool(current.get(key) or value)
                elif key == "runtimeEvidence" and current.get(key, {}).get("status") == "not-run":
                    current[key] = value
                elif value not in (None, [], {}):
                    current[key] = value
    return merged, inputs


def registry_snapshot(root: Path, registry_path: Path) -> list[dict[str, Any]]:
    if not registry_path.is_absolute():
        registry_path = root / registry_path
    env = os.environ.copy()
    env["STANDALONE_LEDGER_REGISTRY_PATH"] = str(registry_path)
    code = """
      import { pathToFileURL } from 'node:url';
      const { caseNamesFor, registry, scenarioCaseFor } = await import(pathToFileURL(process.env.STANDALONE_LEDGER_REGISTRY_PATH));
      const cases = registry.flatMap((scenario) => caseNamesFor(scenario).map((caseName) => {
        const caseDefinition = scenarioCaseFor(scenario, caseName);
        const customCaseDefinition = scenarioCaseFor(scenario, caseName, true);
        const customPreservedAssertions = JSON.stringify(customCaseDefinition.requiredChecks) === JSON.stringify(caseDefinition.requiredChecks)
          ? null
          : customCaseDefinition.requiredChecks;
        return {
          scenario: scenario.id,
          case: caseName,
          source: scenario.script ? (scenario.script.includes('/') ? scenario.script : `scripts/verify-ui/workflows/${scenario.script}`) : null,
          nativeSpecPath: caseDefinition?.playwrightTest ?? null,
          preservedAssertions: caseDefinition?.requiredChecks ?? [],
          customPreservedAssertions,
          scenarioWorkflow: scenario.workflow ?? null,
          scenarioCoverage: scenario.coverage ?? [],
          domainLifecycleStatus: 'unverified',
          runtimeEvidence: { status: 'not-run' },
        };
      }));
      process.stdout.write(JSON.stringify(cases));
    """
    result = subprocess.run(["node", "--input-type=module", "--eval", code], cwd=root, env=env, text=True, capture_output=True)
    if result.returncode != 0:
        raise RuntimeError(f"Could not load registry {registry_path}: {result.stderr.strip()}")
    return json.loads(result.stdout)


def has_browser_owner(path: Path | None) -> bool:
    if path is None or not path.is_file():
        return False
    source = path.read_text(errors="replace")
    return any(pattern.search(source) for pattern in OWNER_PATTERNS)


def loom_dev_journeys(root: Path, embedded_maps: list[Path], baseline_journeys: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    source_path = root / "scripts" / "loom-dev.mjs"
    if not source_path.is_file():
        return [], []
    lines = source_path.read_text(errors="replace").splitlines()
    declarations = []
    declaration_pattern = re.compile(r"^(?:const\s+(\w+)\s*=\s*async\b|async\s+function\s+(\w+))")
    for index, line in enumerate(lines, start=1):
        match = declaration_pattern.match(line)
        if match:
            declarations.append((index, match.group(1) or match.group(2)))
    mapped_by_function: dict[str, dict[str, Any]] = {}
    for item in baseline_journeys:
        function = item.get("function")
        if function:
            mapped_by_function[function] = item
    inputs = []
    for map_path in embedded_maps:
        data = json.loads(map_path.read_text())
        inputs.append({"label": f"{map_path.parent.name}/{map_path.name}", "sha256": sha256(map_path)})
        entries = data.get("journeys", data.get("sources", {}))
        if isinstance(entries, dict):
            entries = entries.get("scripts/loom-dev.mjs", entries)
            if isinstance(entries, dict):
                entries = entries.get("journeys", [entries])
        if isinstance(entries, list):
            for item in entries:
                if isinstance(item, dict):
                    function = item.get("function") or item.get("functionName")
                    if function:
                        mapped_by_function[function] = item
    journeys = []
    source_digest = sha256(source_path)
    call_pattern = re.compile(r"\blaunchPlaywrightEvidenceBrowser\s*\(")
    live_calls = []
    for line_number, line in enumerate(lines, start=1):
        if not call_pattern.search(line):
            continue
        function = next((name for start, name in reversed(declarations) if start < line_number), "unknown")
        live_calls.append((line_number, function))
    live_functions = {function for _, function in live_calls}
    journey_sources = list(live_calls)
    for item in baseline_journeys:
        function = item.get("function")
        if function and function not in live_functions:
            journey_sources.append((item.get("launchLine"), function))
    for line_number, function in journey_sources:
        mapping = mapped_by_function.get(function, {})
        native_specs = [relpath(item) for item in mapping.get("nativeSpecPaths", [])]
        journeys.append({
            "journeyID": mapping.get("journeyID") or f"loom-dev:{function}",
            "sourcePath": "scripts/loom-dev.mjs",
            "sourceSha256": source_digest,
            "launchLine": line_number,
            "function": function,
            "sourceLaunchStillPresent": function in live_functions,
            "commands": mapping.get("commands") or LOOM_DEV_COMMANDS.get(function, []),
            "nativeSpecPaths": native_specs,
            "nativeSpecArtifacts": artifact_hashes(root, native_specs, mapping.get("specArtifacts", {})),
            "nativeCases": mapping.get("nativeCases", []),
            "legacyBrowserOwnershipRemoved": bool(mapping.get("legacyBrowserOwnershipRemoved") or mapping.get("oldBrowserOwnershipRemoved")),
            "runtimeEvidence": mapping.get("runtimeEvidence", {"status": "not-run"}),
            "reason": mapping.get("reason", "Embedded in the Loom development CLI; tracked as a separate browser journey outside the verify[-_]* source-file inventory."),
        })
    return journeys, inputs


def artifact_hashes(root: Path, paths: list[str], artifacts: dict[str, str]) -> list[dict[str, Any]]:
    entries = []
    for value in paths:
        rel = relpath(value)
        hint = artifacts.get(rel)
        candidates = [root / rel] + ([Path(hint)] if hint else [])
        digest = None
        selected = None
        for candidate in candidates:
            found = sha256(candidate)
            if found:
                digest = found
                selected = candidate
                break
        entries.append({"path": rel, "sha256": digest, "artifactSource": "canonical-source-root" if selected == root / rel else ("staged-artifact" if selected else None)})
    return entries


def discovery_index(document: dict[str, Any], source_root: Path) -> dict[tuple[str, str, str], list[dict[str, Any]]]:
    index: dict[tuple[str, str, str], list[dict[str, Any]]] = {}
    for session in document.get("sessions", []):
        session_id = session.get("sessionID")
        config_path = relpath(session.get("configPath", ""))
        config = source_root / config_path
        if not session_id or not config.is_file() or sha256(config) != session.get("configSha256"):
            raise ValueError(f"discovery config hash is missing or stale: {config_path}")
        test_count = 0
        for spec in session.get("specs", []):
            spec_path = relpath(spec.get("path", ""))
            spec_file = source_root / spec_path
            if not spec_path or not spec_file.is_file() or sha256(spec_file) != spec.get("sha256"):
                raise ValueError(f"discovery spec hash is missing or stale: {spec_path}")
            for test in spec.get("cases", []):
                test_count += 1
                title = test.get("title")
                if not title:
                    raise ValueError(f"discovery test has no title: {spec_path}")
                key = (session_id, spec_path, title)
                index.setdefault(key, []).append(test)
        if test_count != session.get("testCount") or len(session.get("specs", [])) != session.get("specFileCount"):
            raise ValueError(f"discovery case/spec count drift in session: {session_id}")
    return index


def bind_discovered_cases(
    case_rows: list[dict[str, Any]],
    source_path: str,
    owner_spec_paths: list[str],
    index: dict[tuple[str, str, str], list[dict[str, Any]]],
    overrides: dict[tuple[str, str, str], dict[str, Any]],
) -> list[str]:
    errors = []
    for case in case_rows:
        case_name = str(case.get("caseName") or case.get("caseTitle") or "")
        scenario_id = str(case.get("scenarioID") or "")
        override = overrides.get((source_path, scenario_id, case_name))
        spec_paths = [relpath(item) for item in case.get("nativeSpecPaths", [])] or owner_spec_paths
        if override:
            spec_path = relpath(override.get("specPath", ""))
            title = override.get("testTitle")
            session_id = override.get("sessionID", "main")
            if spec_path not in spec_paths:
                errors.append(f"override spec is outside source mapping: {source_path} {scenario_id}/{case_name} -> {spec_path}")
                continue
        else:
            candidates = []
            for candidate_title in (case.get("caseTitle"), case_name):
                if not candidate_title:
                    continue
                for spec_path_candidate in spec_paths:
                    for session_id_candidate in ("main", "construction-preview-bench"):
                        key = (session_id_candidate, spec_path_candidate, candidate_title)
                        if key in index:
                            candidates.append((session_id_candidate, spec_path_candidate, candidate_title))
            candidates = list(dict.fromkeys(candidates))
            if len(candidates) != 1:
                errors.append(f"native case must have one exact official discovery binding: {source_path} {scenario_id}/{case_name} ({case.get('caseTitle')})")
                continue
            session_id, spec_path, title = candidates[0]

        key = (session_id, spec_path, title)
        matches = index.get(key, [])
        if not matches:
            errors.append(f"declared native case is absent from official discovery: {source_path} {scenario_id}/{case_name} -> {session_id}:{spec_path}:{title}")
            continue
        case["caseTitle"] = title
        case["discoveryBinding"] = {
            "sessionID": session_id,
            "specPath": spec_path,
            "testTitle": title,
            "matchingTestCount": len(matches),
            "matchingTestIDs": [item.get("id") for item in matches if item.get("id")],
        }
        if override and override.get("expectedVariants") is not None:
            case["expectedVariants"] = override["expectedVariants"]
        elif case.get("caseVariants") is not None:
            case["expectedVariants"] = case["caseVariants"]
        elif case.get("fixtureVariant") is not None:
            case["expectedVariants"] = case["fixtureVariant"]
        elif case_name or case.get("caseTitle"):
            case["expectedVariants"] = case_name or case["caseTitle"]
        if override and override.get("variantDiscoveryBindings"):
            case["variantDiscoveryBindings"] = override["variantDiscoveryBindings"]
            for binding in case["variantDiscoveryBindings"]:
                variant_key = (binding["sessionID"], spec_path, binding["testTitle"])
                if not index.get(variant_key):
                    errors.append(f"expected variant is absent from official discovery: {source_path} {scenario_id}/{case_name} -> {variant_key}")
    return errors


def additional_browser_journeys(
    root: Path,
    map_paths: list[Path],
    baseline_groups: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    baseline_by_path = {item.get("sourcePath"): item for item in baseline_groups if item.get("sourcePath")}
    mappings_by_path: dict[str, dict[str, Any]] = {}
    # A generated ledger is also a valid source map for the next refresh. Keep
    # its explicit journey mappings when no fresh worker map is supplied so a
    # portable rebuild does not silently turn already-mapped owners back into
    # pending rows.
    for source_path, group in baseline_by_path.items():
        for item in group.get("journeys", []):
            if not isinstance(item, dict):
                continue
            owner = item.get("function") or item.get("owner") or item.get("journeyID")
            mappings_by_path[f"{source_path}::{owner or 'default'}"] = item
            mappings_by_path.setdefault(source_path, {"journeys": []})["journeys"].append(item)
    inputs = []
    for map_path in map_paths:
        data = json.loads(map_path.read_text())
        inputs.append({"label": f"{map_path.parent.name}/{map_path.name}", "sha256": sha256(map_path)})
        path = relpath(data.get("sourcePath", ""))
        entries = data.get("journeys", data.get("sources", []))
        if isinstance(entries, dict):
            if path and path in entries:
                entries = entries[path]
            if isinstance(entries, dict):
                entries = entries.get("journeys", [entries])
        if isinstance(entries, list) and path:
            for item in entries:
                if isinstance(item, dict):
                    item.setdefault("sourcePreimageSha256", data.get("sourcePreimageSha256"))
                    item.setdefault("sourceResultSha256", data.get("sourceResultSha256"))
                    item.setdefault("patchSha256", data.get("patchSha256"))
                    item.setdefault("reviewCorrectionPatchSha256", data.get("reviewCorrectionPatchSha256"))
                    owner = item.get("function") or item.get("owner") or item.get("journeyID")
                    mappings_by_path[f"{path}::{owner or 'default'}"] = item
                    mappings_by_path.setdefault(path, {"journeys": []})["journeys"].append(item)

    groups = []
    pending = []
    for source_path, config in ADDITIONAL_STANDALONE_BROWSER_SOURCE_PATHS.items():
        source_file = root / source_path
        if not source_file.is_file():
            continue
        lines = source_file.read_text(errors="replace").splitlines()
        source_digest = sha256(source_file)
        launch_pattern = config["launchPattern"]
        live_lines = [index for index, line in enumerate(lines, start=1) if launch_pattern.search(line)]
        baseline = baseline_by_path.get(source_path, {})
        baseline_journeys = baseline.get("journeys", [])
        journey_lines = [(line, config["owner"]) for line in live_lines]
        live_owners = {config["owner"]} if live_lines else set()
        for item in baseline_journeys:
            owner = item.get("function") or config["owner"]
            if owner not in live_owners:
                journey_lines.append((item.get("launchLine"), owner))
        mapped = mappings_by_path.get(source_path, {})
        mapped_rows = mapped.get("journeys", []) if isinstance(mapped, dict) and "journeys" in mapped else []
        if isinstance(mapped_rows, list):
            for item in mapped_rows:
                if not isinstance(item, dict):
                    continue
                owner = item.get("function") or item.get("owner") or config["owner"]
                if owner not in live_owners and not any(existing_owner == owner for _, existing_owner in journey_lines):
                    journey_lines.append((item.get("launchLine"), owner))
        journeys = []
        for launch_line, owner in journey_lines:
            mapping = mappings_by_path.get(f"{source_path}::{owner}", mappings_by_path.get(source_path, {}))
            native_specs = [relpath(item) for item in mapping.get("nativeSpecPaths", [])]
            row = {
                "journeyID": mapping.get("journeyID") or f"additional-browser:{Path(source_path).stem}:{owner}",
                "sourcePath": source_path,
                "sourceSha256": source_digest,
                "launchLine": launch_line,
                "function": owner,
                "sourceLaunchStillPresent": owner in live_owners,
                "commands": mapping.get("commands", []),
                "nativeSpecPaths": native_specs,
                "nativeSpecArtifacts": artifact_hashes(root, native_specs, mapping.get("specArtifacts", {})),
                "nativeCases": mapping.get("nativeCases", []),
                "legacyBrowserOwnershipRemoved": bool(mapping.get("legacyBrowserOwnershipRemoved") or mapping.get("oldBrowserOwnershipRemoved")),
                "sourcePreimageSha256": mapping.get("sourcePreimageSha256"),
                "sourceResultSha256": mapping.get("sourceResultSha256"),
                "portPatchSha256": mapping.get("patchSha256"),
                "reviewCorrectionPatchSha256": mapping.get("reviewCorrectionPatchSha256"),
                "runtimeEvidence": mapping.get("runtimeEvidence", {"status": "not-run"}),
                "reason": mapping.get("reason", config["reason"]),
            }
            journeys.append(row)
            if not row["nativeSpecPaths"] or not row["nativeCases"] or not row["legacyBrowserOwnershipRemoved"]:
                pending.append(row["journeyID"])
        groups.append({
            "sourcePath": source_path,
            "sourceSha256": source_digest,
            "scopeNote": config["reason"],
            "journeys": journeys,
            "pendingJourneyIDs": [item["journeyID"] for item in journeys if item["journeyID"] in pending],
        })
    return groups, inputs


def source_owner(path: str, browser: bool, ui: bool) -> str:
    name = Path(path).name
    if path in REGISTERED_STANDALONE:
        return "registered-case-port"
    if path in CDA_FIELDS_STANDALONE:
        return "native-standalone-cda-fields"
    if ui:
        return "native-standalone-ui-extra"
    if name == "verify-cda-builder-table-management-contract.mjs":
        return "native-standalone-builder-helper"
    if name.startswith("verify-cda-builder"):
        return "native-standalone-builder"
    if name.startswith("verify-cda-") and re.search(r"group|pivot|unpivot", name) and "quantity-root-category" not in name:
        return "native-standalone-reshape"
    if name.startswith("verify-cda-") and name != "verify-cda-population-member-removal-browser.mjs":
        return "native-standalone-cda-other"
    if name == "verify_ml_dataframer_prototype.mjs":
        return "native-standalone-extra"
    return "native-standalone-misc"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", "--stage-root", dest="source_root", type=Path, default=REPOSITORY_ROOT, help="read-only source checkout used for inventory and canonical artifacts")
    parser.add_argument("--runner-inventory", type=Path, default=Path("docs/verification/playwright/runner-inventory.snapshot.json"))
    parser.add_argument("--worker-map", type=Path, action="append", default=[], help="worker map JSON; may be supplied more than once, later maps can enrich earlier cases")
    parser.add_argument("--batch-manifest", type=Path, action="append", default=[], help="staged batch provenance manifest; may be supplied more than once")
    parser.add_argument("--embedded-map", type=Path, action="append", default=[], help="map for browser journeys embedded in a non-verify entrypoint such as scripts/loom-dev.mjs")
    parser.add_argument("--additional-browser-map", type=Path, action="append", default=[], help="source-to-native-case map for a browser entrypoint outside the verify[-_]* inventory")
    parser.add_argument("--discovery", type=Path, default=Path("docs/verification/playwright/discovery.snapshot.json"), help="normalized official Playwright --list JSON snapshot (discovery only)")
    parser.add_argument("--discovery-overrides", type=Path, action="append", default=None, help="explicit exact-title bindings for source case names that differ from discovered test titles")
    parser.add_argument("--registry", type=Path, default=Path("scripts/verify-ui/registry.mjs"))
    parser.add_argument("--output", type=Path, default=Path("docs/verification/playwright/source-conversion-manifest.json"))
    parser.add_argument("--preimages", type=Path, default=Path("docs/verification/playwright/source-preimages.json"), help="persistent source preimage hash ledger")
    parser.add_argument("--markdown-output", type=Path, default=Path("docs/verification/playwright/source-conversion-manifest.md"), help="human-readable inventory report")
    parser.add_argument("--require-complete", action="store_true", help="fail if any legacy browser workflow still needs a mapping or disposition")
    args = parser.parse_args()

    source_root = args.source_root.resolve()
    inventory_path = args.runner_inventory if args.runner_inventory.is_absolute() else REPOSITORY_ROOT / args.runner_inventory
    inventory = json.loads(inventory_path.read_text())
    discovery_path = args.discovery if args.discovery.is_absolute() else REPOSITORY_ROOT / args.discovery
    discovery_document = json.loads(discovery_path.read_text())
    if discovery_document.get("status") != "discovery-only" or discovery_document.get("runtimeStatus") != "not-run":
        raise SystemExit("discovery snapshot must be explicitly discovery-only with runtimeStatus not-run")
    try:
        discovery_rows = discovery_index(discovery_document, source_root)
    except ValueError as error:
        raise SystemExit(str(error)) from error
    discovery_overrides = {}
    discovery_override_inputs = []
    override_paths = args.discovery_overrides or [Path("docs/verification/playwright/discovery-case-overrides.json")]
    for path in override_paths:
        override_path = path if path.is_absolute() else REPOSITORY_ROOT / path
        override_document = json.loads(override_path.read_text())
        discovery_override_inputs.append({"label": f"{override_path.parent.name}/{override_path.name}", "sha256": sha256(override_path)})
        for item in override_document.get("cases", []):
            key = (relpath(item.get("sourcePath", "")), str(item.get("scenarioID") or ""), str(item.get("caseName") or ""))
            if key in discovery_overrides:
                raise SystemExit(f"duplicate discovery title override for {key}")
            discovery_overrides[key] = item
    default_source_map = REPOSITORY_ROOT / "docs/verification/playwright/source-conversion-manifest.json"
    worker_map_args = args.worker_map or ([default_source_map] if default_source_map.is_file() else [])
    worker_map_paths = [path if path.is_absolute() else REPOSITORY_ROOT / path for path in worker_map_args]
    worker_map, map_inputs = merge_worker_maps(worker_map_paths)
    batch_inputs = []
    batch_mappings = []
    for path in args.batch_manifest:
        manifest_path = path if path.is_absolute() else REPOSITORY_ROOT / path
        batch = json.loads(manifest_path.read_text())
        batch_sources = batch.get("sources", {})
        batch_mappings = batch.get("mappings", [])
        def case_rows(mapping: dict[str, Any]) -> list[dict[str, Any]]:
            names = mapping.get("caseNames")
            titles = mapping.get("specTitles")
            if isinstance(names, list) and names:
                rows = []
                for index, name in enumerate(names):
                    title = None
                    if isinstance(titles, list) and titles:
                        if len(titles) == len(names):
                            title = titles[index]
                        elif len(titles) == 1:
                            title = titles[0]
                    title = title or mapping.get("specTitle") or name
                    rows.append({"scenarioID": mapping.get("scenarioID"), "caseName": name, "caseTitle": title or name})
                return rows
            if isinstance(titles, list) and titles:
                return [{"scenarioID": mapping.get("scenarioID"), "caseName": mapping.get("caseName"), "caseTitle": title} for title in titles]
            return [{
                "scenarioID": mapping.get("scenarioID"),
                "caseName": mapping.get("caseName"),
                "caseTitle": mapping.get("specTitle") or mapping.get("caseTitle") or mapping.get("caseName"),
                **({"caseVariants": mapping["caseVariants"]} if mapping.get("caseVariants") else {}),
            }]

        batch_case_rows = [case for mapping in batch_mappings if isinstance(mapping, dict) for case in case_rows(mapping)]
        mapped_case_rows = sum(
            len(value.get("nativeCases", []))
            for value in batch_sources.values()
            if isinstance(value, dict)
        ) if isinstance(batch_sources, dict) else 0
        action_cases = batch.get("actionToNativeCase", [])
        if batch_mappings:
            unique_batch_cases = len({(item.get("scenarioID"), item.get("caseName") or item.get("caseTitle")) for item in batch_case_rows})
        elif action_cases:
            unique_batch_cases = len({
                (item.get("fixture_scenario_id"), item.get("test_title"))
                for item in action_cases if isinstance(item, dict)
            })
        else:
            unique_batch_cases = len({
                title
                for value in batch_sources.values()
                if isinstance(value, dict)
                for title in value.get("caseTitles", [])
            }) if isinstance(batch_sources, dict) else 0
        batch_inputs.append({
            "batch": batch.get("batch", manifest_path.stem),
            "stageLabel": batch.get("stageLabel") or Path(batch.get("stage", manifest_path.parent.name)).name,
            "sourceLocation": portable_stage_location(manifest_path),
            "manifestFile": manifest_path.name,
            "manifestSha256": sha256(manifest_path),
            "patchFile": Path(batch.get("patch_path", batch.get("patchPath", ""))).name or None,
            "patchLocation": portable_stage_location(Path(batch.get("patch_path", batch.get("patchPath", "")))) if batch.get("patch_path", batch.get("patchPath")) else None,
            "patchSha256": batch.get("patch_sha256", batch.get("patchSha256")),
            "mappingCount": len(batch_mappings) if batch_mappings else len(batch_sources) if isinstance(batch_sources, dict) else 0,
            "caseCount": unique_batch_cases,
            "caseMappingRows": mapped_case_rows or (len(batch_case_rows) if batch_mappings else len(action_cases)),
            "verificationScope": batch.get("verification_scope"),
        })
        for entry in batch.get("mappings", []):
            source = relpath(entry.get("source", ""))
            if not source:
                continue
            cases = case_rows(entry)
            for case in cases:
                assertions = entry.get("preservedAssertions")
                if not isinstance(assertions, list) or not assertions:
                    oracle = entry.get("preservedOracles")
                    assertions = [oracle] if isinstance(oracle, str) and oracle else []
                case["preservedAssertions"] = assertions
                case["nativeSpecPaths"] = [relpath(entry.get("spec") or entry.get("nativeSpecPath") or "scripts/verify-ui/specs/standalone-cda-other.spec.mjs")]
                if entry.get("workflow"):
                    case["workflow"] = f"{source}::{entry['workflow']}"
            mapped = {
                "nativeSpecPaths": [relpath(entry.get("spec") or entry.get("nativeSpecPath") or "scripts/verify-ui/specs/standalone-cda-other.spec.mjs")],
                "workflowPaths": [source] if entry.get("workflow") else [],
                "nativeCases": cases,
                "legacyBrowserOwnershipRemoved": True,
                "runtimeEvidence": {"status": "not-run"},
                "reason": "Imported from the staged source-to-native-case map; preserved assertion/oracle summary is carried with the spec mapping.",
            }
            existing = worker_map.setdefault(source, {})
            existing["nativeSpecPaths"] = list(dict.fromkeys([*existing.get("nativeSpecPaths", []), *mapped["nativeSpecPaths"]]))
            existing["workflowPaths"] = list(dict.fromkeys([*existing.get("workflowPaths", []), *mapped["workflowPaths"]]))
            existing_cases = existing.setdefault("nativeCases", [])
            for case in mapped["nativeCases"]:
                key = (case.get("scenarioID"), case.get("caseName"), case.get("caseTitle"))
                prior_index = next((index for index, prior in enumerate(existing_cases) if (prior.get("scenarioID"), prior.get("caseName"), prior.get("caseTitle")) == key), None)
                if prior_index is None:
                    existing_cases.append(case)
                elif len(case.get("preservedAssertions", [])) > len(existing_cases[prior_index].get("preservedAssertions", [])):
                    existing_cases[prior_index] = case
            existing["legacyBrowserOwnershipRemoved"] = True
            existing["runtimeEvidence"] = {"status": "not-run"}
            existing.setdefault("reason", mapped["reason"])
    for map_path in worker_map_paths:
        document = json.loads(map_path.read_text())
        scope = document.get("sourceScope", {})
        batch_inputs.extend(item for item in scope.get("sourceMapBatches", document.get("sourceMapBatches", [])) if isinstance(item, dict))
    unique_batch_inputs = []
    seen_batches = set()
    for item in batch_inputs:
        key = (item.get("stageLabel"), item.get("manifestFile"), item.get("manifestSha256"))
        if key not in seen_batches:
            seen_batches.add(key)
            unique_batch_inputs.append(item)
    batch_inputs = unique_batch_inputs

    source_files, ui_paths = source_paths(source_root, inventory)
    browser_records: dict[str, dict[str, Any]] = {}
    all_old_consumers = inventory.get("browserLauncherConsumers", [])
    for item in all_old_consumers:
        path = relpath(item.get("file", ""))
        if item.get("role") == "standalone verifier entrypoint" and path.startswith("scripts/"):
            browser_records[path] = item

    out_path = args.output if args.output.is_absolute() else REPOSITORY_ROOT / args.output
    previous_manifest = json.loads(out_path.read_text()) if out_path.exists() else {}
    previous_by_path = {relpath(item.get("sourcePath", "")): item for item in previous_manifest.get("sources", [])}

    all_paths = set(source_files) | set(browser_records) | set(worker_map) | set(previous_by_path)
    for item in all_old_consumers:
        rel = relpath(item.get("file", ""))
        # Keep old consumer-only source paths in the ledger, including removed files.
        if item.get("role") == "standalone verifier entrypoint" and rel.startswith("scripts/"):
            all_paths.add(rel)
    preimages_path = args.preimages if args.preimages.is_absolute() else REPOSITORY_ROOT / args.preimages
    preimages = json.loads(preimages_path.read_text()) if preimages_path.exists() else {}
    for rel in sorted(all_paths):
        current_path = source_files.get(rel, source_root / rel)
        if rel not in preimages:
            prior = previous_by_path.get(rel, {}).get("preimageSha256")
            mapped_preimage = worker_map.get(rel, {}).get("workerSourcePreimageSha256") or worker_map.get(rel, {}).get("sourcePreimageSha256")
            preimages[rel] = prior or mapped_preimage or sha256(current_path)
    preimages_path.parent.mkdir(parents=True, exist_ok=True)
    preimages_path.write_text(json.dumps(preimages, indent=2, sort_keys=True) + "\n")

    records = []
    pending = []
    unclassified = []
    source_rows = []
    for source_path in sorted(all_paths):
        path = source_files.get(source_path)
        worker = worker_map.get(source_path, {})
        browser = browser_records.get(source_path)
        ui = source_path in ui_paths
        previous = previous_by_path.get(source_path, {})
        current_hash = sha256(path) if path else None
        owner_present = has_browser_owner(path)
        native_specs = [relpath(item) for item in worker.get("nativeSpecPaths", [])]
        workflows = [relpath(item) for item in worker.get("workflowPaths", [])]
        oracle_helpers = [relpath(item) for item in worker.get("oracleHelperPaths", [])]
        native_cases = worker.get("nativeCases", [])
        old_owner_removed = bool(worker.get("legacyBrowserOwnershipRemoved") or worker.get("oldBrowserOwnershipRemoved"))
        runtime = worker.get("runtimeEvidence", {"status": "not-run"})
        if not isinstance(runtime, dict):
            runtime = {"status": "not-run", "note": str(runtime)}
        runtime.setdefault("status", "not-run")
        specs = artifact_hashes(source_root, native_specs, worker.get("specArtifacts", {}))
        workflow_artifacts = artifact_hashes(source_root, workflows, worker.get("workflowArtifacts", {}))
        oracle_artifacts = artifact_hashes(source_root, oracle_helpers, worker.get("oracleHelperArtifacts", {}))
        case_complete = bool(native_cases) and all(
            isinstance(case, dict) and case.get("scenarioID") and case.get("caseTitle")
            and isinstance(case.get("preservedAssertions"), list) and case["preservedAssertions"]
            for case in native_cases
        )
        spec_complete = bool(specs) and all(item.get("sha256") for item in specs)
        helper_artifacts_complete = all(item.get("sha256") for item in workflow_artifacts + oracle_artifacts)
        explicit = worker.get("disposition")
        classification = NON_BROWSER_CLASSIFICATION.get(source_path)

        if browser:
            kind = "registered-browser-entrypoint" if source_path in REGISTERED_STANDALONE else "standalone-browser-entrypoint"
            reason = worker.get("reason") or (REGISTERED_STANDALONE.get(source_path) if source_path in REGISTERED_STANDALONE else "Listed as a standalone browser verifier in the official runner inventory.")
        elif ui:
            kind = "preserved-ui-package-browser-entrypoint"
            reason = worker.get("reason") or "Package-local standalone browser verifier discovered in the source tree; preserved outside the root runner inventory."
        elif classification:
            kind = classification["kind"]
            reason = worker.get("reason") or classification["reason"]
        elif worker.get("disposition") or worker.get("retainedPureOracleHelpers") or worker.get("retainedOracleHelpers"):
            kind = "mapped-native-workflow-or-helper"
            reason = worker.get("reason") or "Mapped by a staged standalone source-to-native-case manifest."
        else:
            kind = "unclassified"
            reason = "No browser inventory entry, native case map, or retained-source classification was provided."
            unclassified.append(source_path)

        if explicit in {"obsolete-deleted", "pure-helper-retained", "retained-pure-oracle-helper"} or explicit and explicit.startswith("retained-"):
            disposition = explicit
        elif classification and classification["kind"] == "workflow-dispatcher" and native_specs and spec_complete and case_complete and old_owner_removed and not owner_present and helper_artifacts_complete and path is not None:
            disposition = "native-spec-mapped-source"
        elif kind in {"standalone-browser-entrypoint", "registered-browser-entrypoint", "preserved-ui-package-browser-entrypoint", "mapped-native-workflow-or-helper"}:
            source_present_or_relocated = path is not None or bool(worker.get("sourceRelocatedToNativeWorkflow"))
            if native_specs and spec_complete and case_complete and old_owner_removed and not owner_present and helper_artifacts_complete and source_present_or_relocated:
                disposition = "native-spec-mapped-source"
            elif native_specs:
                disposition = "native-spec-mapping-incomplete"
            else:
                disposition = "native-spec-required"
        elif classification:
            disposition = explicit or classification["disposition"]
        else:
            disposition = explicit or "remaining-nonmechanical-gap"

        if explicit == "obsolete-deleted":
            no_native_replacement = bool(worker.get("noNativeReplacement"))
            replacement_evidence_present = bool(worker.get("retirementEvidence"))
            replacement_mapping_missing = not no_native_replacement and (not native_specs or not spec_complete or not case_complete)
            retirement_evidence_missing = no_native_replacement and not replacement_evidence_present
            if path is not None or replacement_mapping_missing or retirement_evidence_missing or not old_owner_removed:
                pending.append(source_path)
                if path is not None:
                    disposition = "obsolete-deletion-pending"
        elif disposition == "native-spec-mapped-source":
            pass
        elif classification and disposition.startswith("retained-"):
            if path is None or owner_present or not reason:
                pending.append(source_path)
        elif explicit in {"pure-helper-retained", "retained-pure-oracle-helper"}:
            if path is None or owner_present or not reason:
                pending.append(source_path)
        else:
            pending.append(source_path)
        if owner_present and old_owner_removed:
            pending.append(source_path)

        if classification and path is not None:
            disposition_evidence = {
                "sourcePath": source_path,
                "sourceSha256": current_hash,
                "officialStandaloneBrowserInventory": source_path in browser_records,
                "browserLaunchOrCDPOwnershipFound": owner_present,
                "classificationReason": reason,
            }
        elif explicit in {"obsolete-deleted", "obsolete-deletion-pending"}:
            disposition_evidence = {
                "sourcePath": source_path,
                "sourcePresent": path is not None,
                "sourceSha256": current_hash,
                "specPaths": native_specs,
                "caseMappingCount": len(native_cases),
                "noNativeReplacement": bool(worker.get("noNativeReplacement")),
                "retirementEvidence": worker.get("retirementEvidence"),
            }
        elif worker.get("sourceRelocatedToNativeWorkflow"):
            disposition_evidence = {
                "sourcePath": source_path,
                "sourcePresent": path is not None,
                "sourceSha256": current_hash,
                "preimageSha256": preimages.get(source_path),
                "replacementWorkflowPaths": workflows,
                "replacementWorkflowArtifacts": workflow_artifacts,
                "specPaths": native_specs,
                "caseMappingCount": len(native_cases),
            }
        else:
            disposition_evidence = None

        cases_with_specs = []
        for case in native_cases:
            if isinstance(case, dict):
                item_case = dict(case)
                case_specs = item_case.get("nativeSpecPaths") or ([item_case["nativeSpecPath"]] if item_case.get("nativeSpecPath") else native_specs)
                item_case["nativeSpecPaths"] = [relpath(value) for value in case_specs]
                cases_with_specs.append(item_case)
            else:
                cases_with_specs.append(case)

        entry = {
            "sourcePath": source_path,
            "preimageSha256": preimages.get(source_path),
            "currentSha256": current_hash,
            "sourceStillPresent": path is not None,
            "ownerPartition": worker.get("ownerPartition") or source_owner(source_path, bool(browser), ui),
            "kind": kind,
            "disposition": disposition,
            "reason": reason,
            "dispositionEvidence": disposition_evidence,
            "browserInventory": ({"launchCalls": browser.get("launchCalls", []), "domainHelpers": browser.get("domainHelpers", [])} if browser else None),
            "nativeSpecPaths": native_specs,
            "nativeSpecArtifacts": specs,
            "workflowPaths": workflows,
            "workflowArtifacts": workflow_artifacts,
            "oracleHelperPaths": oracle_helpers,
            "oracleHelperArtifacts": oracle_artifacts,
            "nativeCases": cases_with_specs,
            "directNativeBody": bool(worker.get("directNativeBody")),
            "legacyBrowserOwnershipRemoved": old_owner_removed,
            "nativeReplacementNotRequired": bool(worker.get("noNativeReplacement")),
            "sourceRelocatedToNativeWorkflow": bool(worker.get("sourceRelocatedToNativeWorkflow")),
            "sourceStillOwnsBrowser": owner_present,
            "runtimeEvidence": runtime,
        }
        records.append(entry)
        source_rows.append(entry)

    embedded_map_paths = [path if path.is_absolute() else REPOSITORY_ROOT / path for path in args.embedded_map]
    previous_embedded = previous_manifest.get("additionalBrowserWorkflowSources", [])
    baseline_journeys = next((item.get("journeys", []) for item in previous_embedded if item.get("sourcePath") == "scripts/loom-dev.mjs"), [])
    embedded_journeys, embedded_map_inputs = loom_dev_journeys(source_root, embedded_map_paths, baseline_journeys)
    if not embedded_map_paths:
        embedded_map_inputs = previous_manifest.get("sourceScope", {}).get("embeddedJourneyMapInputs", [])
    additional_map_paths = [path if path.is_absolute() else REPOSITORY_ROOT / path for path in args.additional_browser_map]
    previous_additional = previous_manifest.get("additionalStandaloneBrowserWorkflowSources", [])
    additional_groups, additional_map_inputs = additional_browser_journeys(source_root, additional_map_paths, previous_additional)
    if not additional_map_paths:
        additional_map_inputs = previous_manifest.get("sourceScope", {}).get("additionalBrowserMapInputs", [])
    binding_errors = []
    for path, worker in worker_map.items():
        binding_errors.extend(bind_discovered_cases(worker.get("nativeCases", []), path, worker.get("nativeSpecPaths", []), discovery_rows, discovery_overrides))
    for journey in embedded_journeys:
        binding_errors.extend(bind_discovered_cases(journey.get("nativeCases", []), journey.get("sourcePath", "scripts/loom-dev.mjs"), journey.get("nativeSpecPaths", []), discovery_rows, discovery_overrides))
    for group in additional_groups:
        for journey in group.get("journeys", []):
            binding_errors.extend(bind_discovered_cases(journey.get("nativeCases", []), journey.get("sourcePath", group.get("sourcePath", "")), journey.get("nativeSpecPaths", []), discovery_rows, discovery_overrides))
    if binding_errors:
        raise SystemExit("\n".join(sorted(set(binding_errors))))

    # Merge a repeated row imported from both the previous generated ledger
    # and its stage map. Keep explicit source case names when a title-only row
    # is merely the old alias for that one named case; multiple explicit cases
    # with distinct variants sharing one test title remain separate.
    for worker in worker_map.values():
        rows = [item for item in worker.get("nativeCases", []) if isinstance(item, dict)]
        grouped: dict[tuple[str, str, str], list[dict[str, Any]]] = {}
        for case in rows:
            binding = case.get("discoveryBinding", {})
            key = (str(case.get("scenarioID") or ""), str(binding.get("specPath") or ""), str(binding.get("testTitle") or ""))
            grouped.setdefault(key, []).append(case)
        normalized_rows = []
        for key, group in grouped.items():
            by_identity: dict[str, dict[str, Any]] = {}
            for case in group:
                identity = json.dumps({
                    "scenarioID": case.get("scenarioID"),
                    "caseName": case.get("caseName"),
                    "caseTitle": case.get("caseTitle"),
                    "expectedVariants": case.get("expectedVariants"),
                }, sort_keys=True)
                prior = by_identity.get(identity)
                if prior is None:
                    by_identity[identity] = case
                else:
                    prior["preservedAssertions"] = list(dict.fromkeys([
                        *(prior.get("preservedAssertions") or []), *(case.get("preservedAssertions") or []),
                    ]))
            distinct = list(by_identity.values())
            title_aliases = [case for case in distinct if not case.get("caseName") or case.get("caseName") == case.get("caseTitle")]
            named_cases = [case for case in distinct if case not in title_aliases]
            if title_aliases and len(named_cases) == 1:
                named_cases[0]["preservedAssertions"] = list(dict.fromkeys([
                    *(named_cases[0].get("preservedAssertions") or []),
                    *(assertion for alias in title_aliases for assertion in alias.get("preservedAssertions", [])),
                ]))
                distinct = named_cases
            elif len(title_aliases) > 1:
                title_aliases[0]["preservedAssertions"] = list(dict.fromkeys([
                    *(assertion for alias in title_aliases for assertion in alias.get("preservedAssertions", [])),
                ]))
                distinct = [*named_cases, title_aliases[0]]
            normalized_rows.extend(distinct)
        worker["nativeCases"] = normalized_rows

    # The source records were assembled before the final discovery bindings were
    # attached above. Copy the bound rows back into those records so the
    # portable ledger carries the exact official --list title/session/spec.
    record_by_path = {item["sourcePath"]: item for item in records}
    for source_path, worker in worker_map.items():
        record = record_by_path.get(source_path)
        if record is None:
            continue
        cases_with_specs = []
        for case in worker.get("nativeCases", []):
            if isinstance(case, dict):
                item_case = dict(case)
                case_specs = item_case.get("nativeSpecPaths") or ([item_case["nativeSpecPath"]] if item_case.get("nativeSpecPath") else record["nativeSpecPaths"])
                item_case["nativeSpecPaths"] = [relpath(value) for value in case_specs]
                cases_with_specs.append(item_case)
            else:
                cases_with_specs.append(case)
        record["nativeCases"] = cases_with_specs

    # Keep a compact, independent source-case identity list beside the emitted
    # rows. The checker compares it with the final rows so deleting one variant
    # cannot pass merely because another variant retains the same Playwright
    # title.
    expected_discovery_owners = []
    def remember_discovery_owner(owner_kind: str, source_path: str, case: dict[str, Any]) -> None:
        binding = case.get("discoveryBinding")
        if not binding:
            return
        identity = {
            "ownerKind": owner_kind,
            "sourcePath": relpath(source_path),
            "scenarioID": case.get("scenarioID"),
            "caseName": case.get("caseName"),
            "expectedVariants": case.get("expectedVariants"),
        }
        for discovered_binding in [binding, *case.get("variantDiscoveryBindings", [])]:
            expected_discovery_owners.append({
                **identity,
                "sessionID": discovered_binding["sessionID"],
                "specPath": binding["specPath"],
                "testTitle": discovered_binding["testTitle"],
            })

    for source_path, worker in worker_map.items():
        for case in worker.get("nativeCases", []):
            if isinstance(case, dict):
                remember_discovery_owner("source", source_path, case)
    for journey in embedded_journeys:
        for case in journey.get("nativeCases", []):
            if isinstance(case, dict):
                remember_discovery_owner("embedded", journey.get("sourcePath", "scripts/loom-dev.mjs"), case)
    for group in additional_groups:
        for journey in group.get("journeys", []):
            for case in journey.get("nativeCases", []):
                if isinstance(case, dict):
                    remember_discovery_owner("additional-browser", journey.get("sourcePath", group.get("sourcePath", "")), case)
    expected_discovery_owners.sort(key=lambda item: (
        item["ownerKind"], item["sourcePath"], item["scenarioID"] or "", item["caseName"] or "",
        item["sessionID"], item["specPath"], item["testTitle"], json.dumps(item["expectedVariants"], sort_keys=True),
    ))
    embedded_pending = [item["journeyID"] for item in embedded_journeys if not item["nativeSpecPaths"] or not item["nativeCases"] or not item["legacyBrowserOwnershipRemoved"]]
    additional_pending = [journey["journeyID"] for group in additional_groups for journey in group["journeys"] if not journey["nativeSpecPaths"] or not journey["nativeCases"] or not journey["legacyBrowserOwnershipRemoved"]]
    launcher_implementations = []
    for item in inventory.get("launcherImplementations", []):
        path = relpath(item.get("file", ""))
        source_path = source_root / path
        source_present = source_path.is_file()
        owner_present = has_browser_owner(source_path) if source_present else False
        retirement = LEGACY_LAUNCHER_RETIREMENT_EVIDENCE.get(path) if not source_present else None
        record = {
            "path": path,
            "purpose": item.get("purpose"),
            "sha256": sha256(source_path),
            "sourceStillPresent": source_present,
            "directBrowserOwnershipPresent": owner_present,
            "disposition": "legacy-browser-owner-remains" if owner_present else ("deleted-after-native-port" if retirement else ("retained-native-page-action-helper" if source_present else "missing-without-retirement-evidence")),
            "evidence": "Current source is checked for direct browser-launch call ownership; shared helpers remain open until all legacy callers are ported." if owner_present else (retirement["reason"] if retirement else "No direct browser-launch call remains in this helper; its native page/action responsibilities are retained."),
            "runtimeEvidence": {"status": "not-run"},
        }
        if retirement:
            record["preimageSha256"] = retirement["preimageSha256"]
            record["preimageCommit"] = retirement["preimageCommit"]
            record["dispositionEvidence"] = {
                "patchSha256": retirement["patchSha256"],
                "reason": retirement["reason"],
            }
        launcher_implementations.append(record)
    registry_path = args.registry if args.registry.is_absolute() else source_root / args.registry
    registry_cases = registry_snapshot(source_root, registry_path)
    registry_pending = []
    registry_specs = set()
    main_discovery_specs = {
        spec["path"]: spec
        for session in discovery_document.get("sessions", []) if session.get("sessionID") == "main"
        for spec in session.get("specs", [])
    }
    for item in registry_cases:
        spec_path = item.get("nativeSpecPath")
        if spec_path:
            spec_path = relpath(spec_path)
            registry_specs.add(spec_path)
            found = main_discovery_specs.get(spec_path, {})
            item["discoveryTestTitles"] = sorted({test.get("title") for test in found.get("cases", []) if test.get("title")})
            item["discoveryTestCount"] = len(found.get("cases", []))
            item["discoverySpecSha256"] = found.get("sha256")
        item["preservedAssertions"] = item.pop("preservedAssertions", [])
        item["mappingStatus"] = "native-spec-mapped; lifecycle-unverified"
        item["domainLifecycleStatus"] = "unverified"
        item["runtimeEvidence"] = {"status": "not-run"}
        if not spec_path or not (source_root / spec_path).is_file():
            registry_pending.append(f"{item.get('scenario')}/{item.get('case')}")

    # Classify every old launcher search hit. The inventory contains helper roles as
    # well as direct standalone sources, so keep those roles explicit in the ledger.
    consumer_accounting = []
    for consumer in all_old_consumers:
        path = relpath(consumer.get("file", ""))
        role = consumer.get("role", "unknown")
        source_record = next((item for item in records if item["sourcePath"] == path), None)
        if role == "standalone verifier entrypoint":
            status = source_record["disposition"] if source_record else "missing-source-record"
            evidence = "Standalone source row retains the old launcher inventory entry and its conversion mapping/disposition."
        elif role == "migration checker (symbol-name match only)":
            status = "inventory-keyword-false-positive"
            evidence = "The inventory records no launch calls; this hit came from launcher symbol names in the checker itself."
        elif role == "verify-ui workflow/helper":
            status = "retained-legacy-runner-helper"
            evidence = "Kept as an explicit shared-runner/helper consumer pending migration or retirement of the old verify-ui runner path."
        elif role == "benchmark entrypoint":
            if additional_pending:
                status = "additional-browser-workflow-mappings-pending"
            elif additional_groups:
                status = "mapped-additional-browser-workflows"
            else:
                status = "retained-benchmark-browser-tool"
            evidence = f"{sum(len(group['journeys']) for group in additional_groups)} construction-preview benchmark browser launch site(s) are inventoried separately; {len(additional_pending)} mappings remain open."
        elif role == "development-tool browser entrypoint":
            if embedded_pending:
                status = "embedded-browser-workflow-mappings-pending"
            elif embedded_journeys:
                status = "mapped-embedded-browser-workflows"
            else:
                status = "retained-development-browser-tool"
            evidence = f"{len(embedded_journeys)} embedded launch call sites in scripts/loom-dev.mjs are inventoried separately; {len(embedded_pending)} journey mappings remain open."
        else:
            status = "unclassified-old-launch-consumer"
            evidence = "No explicit old-launch consumer disposition was assigned."
        consumer_accounting.append({"file": path, "role": role, "launchCalls": consumer.get("launchCalls", []), "disposition": status, "evidence": evidence})

    actual_source_paths = sorted(source_files)
    inventoried_standalone = sorted(browser_records)
    missing_inventoried = sorted(set(inventoried_standalone) - set(source_files))
    unexpected_root = sorted(set(path for path in actual_source_paths if path.startswith("scripts/")) - set(browser_records))
    unmapped_maps = sorted(set(worker_map) - set(source_files) - set(browser_records))
    mapped_specs = {path for item in records for path in item["nativeSpecPaths"]}
    mapped_specs.update(path for journey in embedded_journeys for path in journey["nativeSpecPaths"])
    mapped_specs.update(path for group in additional_groups for journey in group["journeys"] for path in journey["nativeSpecPaths"])
    infrastructure_specs = []
    for path, classification in INFRASTRUCTURE_SPEC_CLASSIFICATION.items():
        spec_file = source_root / path
        if spec_file.is_file():
            found = main_discovery_specs.get(path, {})
            infrastructure_specs.append({
                "path": path,
                "kind": classification["kind"],
                "sha256": sha256(spec_file),
                "discoveryTestTitles": sorted({test.get("title") for test in found.get("cases", []) if test.get("title")}),
                "discoveryTestCount": len(found.get("cases", [])),
                "discoverySpecSha256": found.get("sha256"),
                "reason": classification["reason"],
                "runtimeEvidence": {"status": "not-run"},
            })
    infrastructure_spec_paths = {item["path"] for item in infrastructure_specs}
    orphan_specs = []
    playwright_dir = source_root / "scripts" / "verify-ui" / "specs"
    all_playwright_specs = []
    if playwright_dir.exists():
        for path in sorted(playwright_dir.glob("*.spec.mjs")):
            rel = path.relative_to(source_root).as_posix()
            all_playwright_specs.append(rel)
            if rel not in mapped_specs and rel not in registry_specs and rel not in infrastructure_spec_paths:
                orphan_specs.append(rel)

    pending_paths = set(pending)
    pending_by_partition: dict[str, list[str]] = {}
    for item in records:
        reasons = []
        if item["sourcePath"] in pending_paths:
            if item["disposition"] == "obsolete-deletion-pending":
                reasons.append("obsolete legacy source is still present in the source tree")
            if not item["nativeSpecPaths"] and item["kind"] in {"standalone-browser-entrypoint", "registered-browser-entrypoint", "preserved-ui-package-browser-entrypoint", "workflow-dispatcher"}:
                reasons.append("native spec mapping is missing")
            if item["nativeSpecPaths"] and not item["nativeCases"]:
                reasons.append("native spec mapping has no source-to-case records")
            if item["nativeCases"] and any(not case.get("preservedAssertions") for case in item["nativeCases"] if isinstance(case, dict)):
                reasons.append("one or more cases lack preserved assertion/oracle summaries")
            if item["nativeSpecPaths"] and any(not spec.get("sha256") for spec in item["nativeSpecArtifacts"]):
                reasons.append("one or more mapped native specs are missing")
            if item["nativeSpecPaths"] and not item["legacyBrowserOwnershipRemoved"]:
                reasons.append("legacy browser ownership removal is not recorded")
            if item["sourceStillOwnsBrowser"]:
                reasons.append("source still owns a browser or CDP call")
            if not reasons:
                reasons.append("explicit retained/deleted disposition or artifact evidence is incomplete")
            item["pendingReasons"] = reasons
            pending_by_partition.setdefault(item["ownerPartition"], []).append(item["sourcePath"])
        else:
            item["pendingReasons"] = []

    partition_counts: dict[str, int] = {}
    for item in records:
        partition = item["ownerPartition"]
        partition_counts[partition] = partition_counts.get(partition, 0) + 1
    counts = {
        "allSourceRecords": len(records),
        "currentSourceFiles": len(actual_source_paths),
        "currentRootVerifySources": sum(path.startswith("scripts/") for path in actual_source_paths),
        "currentUiPackageVerifySources": len(ui_paths),
        "sourceRowsWithNativeCaseMappings": sum(bool(item["nativeCases"]) for item in records),
        "sourceToNativeCaseMappingRows": sum(len(item["nativeCases"]) for item in records),
        "distinctSourceNativeCaseKeys": len({
            (case.get("scenarioID"), case.get("caseName") or case.get("caseTitle"))
            for item in records for case in item["nativeCases"] if isinstance(case, dict)
        }),
        "sourceRowsWithLegacyBrowserOwnershipRemoved": sum(item["legacyBrowserOwnershipRemoved"] for item in records),
        "relocatedSourcesWithNativeWorkflow": sum(item["sourceRelocatedToNativeWorkflow"] for item in records),
        "officialRootBrowserEntrypoints": len(browser_records),
        "additionalUiPackageBrowserEntrypoints": len(ui_paths),
        "registeredBrowserEntrypoints": len(REGISTERED_STANDALONE),
        "registryCases": len(registry_cases),
        "registryRequiredCheckEntries": sum(
            len(item.get("preservedAssertions", [])) + len(item.get("customPreservedAssertions") or [])
            for item in registry_cases
        ),
        "registryCustomAssertionVariants": sum(item.get("customPreservedAssertions") is not None for item in registry_cases),
        "registryCaseMappingsPresent": len(registry_cases) - len(registry_pending),
        "additionalBrowserWorkflowSources": (1 if embedded_journeys else 0) + len(additional_groups),
        "additionalEmbeddedBrowserJourneys": len(embedded_journeys),
        "mappedEmbeddedBrowserJourneys": len(embedded_journeys) - len(embedded_pending),
        "pendingEmbeddedBrowserJourneys": len(embedded_pending),
        "convertedSources": sum(item["disposition"] == "native-spec-mapped-source" for item in records),
        "obsoleteSourcesRemoved": sum(item["disposition"] == "obsolete-deleted" for item in records),
        "retainedApiOrHelperSources": sum(item["disposition"].startswith("retained-") for item in records),
        "unclassifiedSources": len(unclassified),
        "pendingSourceMappingsOrDisposition": len(set(pending)),
        "oldBrowserLauncherConsumerSources": len(all_old_consumers),
        "accountedOldBrowserLauncherConsumerSources": sum(item["disposition"] != "unclassified-old-launch-consumer" for item in consumer_accounting),
        "loomDevEmbeddedLaunchSites": len(embedded_journeys),
        "accountedLoomDevEmbeddedLaunchSites": len(embedded_journeys) - len(embedded_pending),
        "additionalStandaloneBrowserWorkflowSources": len(additional_groups),
        "additionalStandaloneBrowserLaunchSites": sum(len(group["journeys"]) for group in additional_groups),
        "mappedAdditionalStandaloneBrowserLaunchSites": sum(len(group["journeys"]) for group in additional_groups) - len(additional_pending),
        "pendingAdditionalStandaloneBrowserLaunchSites": len(additional_pending),
        "legacyLauncherImplementationFiles": len(launcher_implementations),
        "unresolvedLegacyLauncherImplementationOwners": sum(item["directBrowserOwnershipPresent"] for item in launcher_implementations),
        "orphanNativeSpecs": len(orphan_specs),
        "infrastructureSpecs": len(infrastructure_specs),
        "nativePlaywrightSpecFiles": len(all_playwright_specs),
        "mappedNativePlaywrightSpecFiles": sum(path in mapped_specs or path in registry_specs for path in all_playwright_specs),
        "unmappedWorkerSources": len(unmapped_maps),
    }
    root_verify = [path for path in actual_source_paths if path.startswith("scripts/")]
    manifest = {
        "schemaVersion": 2,
        "sourceSnapshot": {
            "inventoryCommit": inventory.get("snapshotCommit"),
            "sourceRoot": ".",
            "stageRoot": ".",
            "runnerInventoryPath": "docs/verification/playwright/runner-inventory.snapshot.json",
            "runnerInventorySha256": sha256(inventory_path),
            "registryPath": registry_path.relative_to(source_root).as_posix() if registry_path.is_relative_to(source_root) else registry_path.name,
            "registrySha256": sha256(registry_path),
            "preimagesCapturedFromStageCopy": True,
        },
        "sourceScope": {
        "rootPattern": "scripts/verify[-_]*.mjs and scripts/verify-ui/{workflows,helpers}/verify[-_]*.mjs (top-level; excludes *.test.mjs)",
            "uiPattern": "ui/packages/**/scripts/verify[-_]*.mjs (top-level per package script directory; excludes *.test.mjs)",
            "currentSourcePaths": actual_source_paths,
            "sourceMapInputs": map_inputs,
            "sourceMapBatches": batch_inputs,
            "embeddedJourneyMapInputs": embedded_map_inputs,
            "additionalBrowserMapInputs": additional_map_inputs,
            "discoverySnapshot": {
                "path": "docs/verification/playwright/discovery.snapshot.json",
                "sha256": sha256(discovery_path),
            },
            "discoveryOverrideInputs": discovery_override_inputs,
            "expectedDiscoveryCaseOwners": expected_discovery_owners,
            "unmappedWorkerSources": unmapped_maps,
            "orphanNativeSpecs": orphan_specs,
            "infrastructureSpecPaths": sorted(infrastructure_spec_paths),
        },
        "counts": counts,
        "infrastructureSpecs": infrastructure_specs,
        "ownershipPartitions": partition_counts,
        "mechanicalConversionComplete": not unclassified and not set(pending) and not registry_pending and not orphan_specs and not unmapped_maps and not embedded_pending and not additional_pending and not any(item["directBrowserOwnershipPresent"] or item["disposition"] == "missing-without-retirement-evidence" for item in launcher_implementations),
        "readyForBrowserTesting": not unclassified and not set(pending) and not registry_pending and not orphan_specs and not unmapped_maps and not embedded_pending and not additional_pending and not any(item["directBrowserOwnershipPresent"] or item["disposition"] == "missing-without-retirement-evidence" for item in launcher_implementations),
        "runtimeEvidenceStatus": "not-run",
        "runtimeEvidenceNote": "Static source conversion and --list discovery do not establish that any mapped browser workflow ran.",
        "testDiscoveryEvidence": {
            "status": "discovery-only",
            "playwrightListPassed": True,
            "discoveredTestCount": next((item["testCount"] for item in discovery_document["sessions"] if item["sessionID"] == "main"), 0),
            "discoveredSpecFileCount": next((item["specFileCount"] for item in discovery_document["sessions"] if item["sessionID"] == "main"), 0),
            "dedicatedBenchmarkTestCount": next((item["testCount"] for item in discovery_document["sessions"] if item["sessionID"] == "construction-preview-bench"), 0),
            "dedicatedBenchmarkSpecFileCount": next((item["specFileCount"] for item in discovery_document["sessions"] if item["sessionID"] == "construction-preview-bench"), 0),
            "discoverySnapshotPath": "docs/verification/playwright/discovery.snapshot.json",
            "discoverySnapshotSha256": sha256(discovery_path),
            "discoverySnapshotMatchesCurrentSourceTree": True,
            "runtimeLifecycleStatus": "not-run",
            "note": "Official Playwright --list snapshots cover the main suite, the dedicated construction-preview benchmark config, and configured related ONE/ALL variants. They confirm discovery only; no test body or domain lifecycle is represented as run.",
        },
        "registryCases": registry_cases,
        "registryCasesPending": registry_pending,
        "additionalBrowserWorkflowSources": [{
            "sourcePath": "scripts/loom-dev.mjs",
            "sourceSha256": sha256(source_root / "scripts" / "loom-dev.mjs"),
            "scopeNote": "These embedded journeys are outside the 86-file verify[-_]* inventory.",
            "journeys": embedded_journeys,
            "pendingJourneyIDs": embedded_pending,
        }],
        "additionalStandaloneBrowserWorkflowSources": additional_groups,
        "additionalStandaloneBrowserWorkflowSourcesPending": additional_pending,
        "legacyLauncherImplementations": launcher_implementations,
        "oldBrowserLauncherConsumers": consumer_accounting,
        "unclassifiedSources": sorted(set(unclassified)),
        "pendingNativeMappingsOrDisposition": sorted(set(pending)),
        "pendingSourcesByOwnerPartition": {key: sorted(value) for key, value in sorted(pending_by_partition.items())},
        "sourceSetReconciliation": {
            "discoveredRootVerifySources": len(root_verify),
            "legacyRootVerifySourceRecords": sum(path.startswith("scripts/") for path in all_paths),
            "inventoriedBrowserSources": len(browser_records),
            "inventoryMissingSourcePaths": missing_inventoried,
        "approvedDeletedBrowserSources": sorted(path for path in missing_inventoried if worker_map.get(path, {}).get("disposition") == "obsolete-deleted"),
        "approvedRelocatedBrowserSources": sorted(path for path in missing_inventoried if worker_map.get(path, {}).get("sourceRelocatedToNativeWorkflow")),
        "unaccountedMissingBrowserSources": sorted(path for path in missing_inventoried if worker_map.get(path, {}).get("disposition") != "obsolete-deleted" and not worker_map.get(path, {}).get("sourceRelocatedToNativeWorkflow")),
            "unexpectedRootSourcesComparedWithBrowserInventory": unexpected_root,
        },
        "sources": records,
    }
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(manifest, indent=2) + "\n")

    if args.markdown_output:
        markdown = args.markdown_output if args.markdown_output.is_absolute() else REPOSITORY_ROOT / args.markdown_output
        lines = [
            "# Standalone Playwright source conversion ledger",
            "",
            "Source conversion status is separate from browser execution status.",
            f"Source set: 85 root inventory rows plus {counts['currentUiPackageVerifySources']} additional package-local source ({counts['allSourceRecords']} records); {counts['obsoleteSourcesRemoved']} root sources intentionally deleted; {counts['currentRootVerifySources']} root verify files and {counts['currentUiPackageVerifySources']} package-local verify file remain ({counts['currentSourceFiles']} current files total).",
            f"Browser inventory: {counts['officialRootBrowserEntrypoints']} root standalone entrypoints; {counts['additionalUiPackageBrowserEntrypoints']} package-local entrypoint; {counts['registryCases']} registered Playwright cases.",
            f"Mapped sources: {counts['convertedSources']}; retained API/helper sources: {counts['retainedApiOrHelperSources']}; pending source mappings/dispositions: {counts['pendingSourceMappingsOrDisposition']}.",
            f"Native case map: {counts['sourceRowsWithNativeCaseMappings']} source rows / {counts['sourceToNativeCaseMappingRows']} source-to-case rows ({counts['distinctSourceNativeCaseKeys']} distinct source-case keys); {counts['sourceRowsWithLegacyBrowserOwnershipRemoved']} source rows record legacy browser ownership removed.",
            f"Native spec accounting: {counts['nativePlaywrightSpecFiles']} current spec files; {counts['mappedNativePlaywrightSpecFiles']} mapped to legacy sources or registry cases; {counts['infrastructureSpecs']} explicitly classified harness specs; {counts['orphanNativeSpecs']} orphan specs.",
            f"Runtime evidence: {manifest['runtimeEvidenceStatus']}; official Playwright --list discovery: {manifest['testDiscoveryEvidence']['discoveredTestCount']} tests in {manifest['testDiscoveryEvidence']['discoveredSpecFileCount']} files (discovery only; lifecycle not run).",
            f"Embedded Loom dev launch sites: {counts['loomDevEmbeddedLaunchSites']}; mappings open: {counts['pendingEmbeddedBrowserJourneys']}.",
            f"Additional non-verify browser sources: {counts['additionalStandaloneBrowserWorkflowSources']}; launch sites: {counts['additionalStandaloneBrowserLaunchSites']}; mappings open: {counts['pendingAdditionalStandaloneBrowserLaunchSites']}; legacy helper owners still active: {counts['unresolvedLegacyLauncherImplementationOwners']}.",
            "",
            "| Source | Owner | Disposition | Cases | Browser owner removed | Runtime | Native specs | Reason |",
            "| --- | --- | --- | ---: | --- | --- | --- | --- |",
        ]
        for item in records:
            cases = ", ".join(f"{case.get('scenarioID')}: {case.get('caseTitle')} ({len(case.get('preservedAssertions', []))} assertions)" for case in item["nativeCases"] if isinstance(case, dict)) or "—"
            specs = ", ".join(item["nativeSpecPaths"]) or "—"
            reason = item["reason"].replace("|", "\\|")
            lines.append(f"| `{item['sourcePath']}` | {item['ownerPartition']} | {item['disposition']} | {len(item['nativeCases'])} | {item['legacyBrowserOwnershipRemoved']} | {item['runtimeEvidence'].get('status', 'not-run')} | {specs} | {reason} |")
        lines.extend(["", "## Pending mappings and dispositions", ""])
        lines.extend(f"- `{path}`" for path in manifest["pendingNativeMappingsOrDisposition"])
        lines.extend(["", "## Embedded Loom development browser journeys", ""])
        for group in manifest["additionalBrowserWorkflowSources"]:
            lines.extend(f"- `{item['journeyID']}` at `scripts/loom-dev.mjs:{item['launchLine']}` ({item['function']}; {', '.join(item['commands']) or 'command pending'}): {('mapped' if item['nativeSpecPaths'] and item['nativeCases'] else 'mapping pending')}" for item in group["journeys"])
        lines.extend(["", "## Additional non-verify browser workflow sources", ""])
        for group in manifest["additionalStandaloneBrowserWorkflowSources"]:
            lines.append(f"- `{group['sourcePath']}` ({group['scopeNote']})")
            lines.extend(f"  - `{item['journeyID']}` at line {item['launchLine']} ({item['function']}): {('mapped; old ownership removed' if item['nativeSpecPaths'] and item['nativeCases'] and item['legacyBrowserOwnershipRemoved'] else 'mapping or launcher removal pending')}" for item in group["journeys"])
        lines.extend(["", "## Shared browser-launch implementations", ""])
        lines.extend(f"- `{item['path']}`: {item['disposition']} — {item['evidence']}" for item in launcher_implementations)
        lines.extend(["", "## Registered native cases", ""])
        lines.extend(f"- `{item['scenario']}/{item['case']}` → `{item.get('nativeSpecPath') or 'missing spec mapping'}`; lifecycle unverified" for item in registry_cases)
        markdown.parent.mkdir(parents=True, exist_ok=True)
        markdown.write_text("\n".join(lines) + "\n")

    print(json.dumps({"manifest": out_path.name, "counts": counts, "pendingNativeMappingsOrDisposition": sorted(set(pending)), "registryCasesPending": registry_pending, "orphanNativeSpecs": orphan_specs, "pendingEmbeddedBrowserJourneys": embedded_pending, "pendingAdditionalBrowserJourneys": additional_pending, "unresolvedLegacyLauncherImplementationOwners": [item["path"] for item in launcher_implementations if item["directBrowserOwnershipPresent"]]}, indent=2))
    if args.require_complete and not manifest["mechanicalConversionComplete"]:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
