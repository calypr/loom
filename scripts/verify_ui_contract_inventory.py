#!/usr/bin/env python3
"""Verify UI inventory source coverage and model/adapter bindings.

The strict default is intended for the comprehensive verification gate. Use
``--source-only`` while the model or runner registry is still being populated.
Neither mode treats a backend allowlist as evidence of mounted UI support.
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import re
import shutil
import sys
import tempfile
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Set, Tuple


if __package__:
    from .normalize_ui_contract_inventory import load_inventory
else:
    from normalize_ui_contract_inventory import load_inventory


SOURCE_COMMIT = "dc331783dde3f61e764bab3a421b66b28698c9d4"
DEFAULT_MANIFEST = Path("docs/ui-contract-inventory/ui-supported-affordances.json")
SOURCE_EXTENSIONS = {".ts", ".tsx", ".js", ".jsx", ".mjs"}
REFERENCE_RE = re.compile(
    r"\b((?:ui|scripts)/[A-Za-z0-9_./-]+\.(?:tsx?|jsx?|mjs|go)):(\d+)(?:-(\d+))?\b"
)
EXPECTED_COVERAGE_FIELDS = {
    "modelStatus",
    "adapterStatus",
    "adapterId",
    "runnerActionId",
    "runnerActions",
    "modelStateId",
}
MODEL_STATUSES = {"pending", "modeled"}
ADAPTER_STATUSES = {"pending", "mapped"}
RUNNER_ACTION_STATUSES = {"pending", "mapped"}
REQUEST_METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE"}
REQUEST_KINDS = {"read", "capability", "assessment", "proposal", "preview", "command", "reconcile", "refresh", "inspect"}
ACTIVE_AVAILABILITY = {"reachable", "capability-conditional", "context-conditional", "visible-disabled"}
EXCLUSION_BASES = {"unmounted", "out-of-task"}


class SourceReadError(Exception):
    pass


def _is_product_source(path: Path) -> bool:
    if path.suffix not in SOURCE_EXTENSIONS:
        return False
    if "builderContractAdapters" in path.parts:
        # These are verification drivers, not mounted product UI source. The
        # capture artifact fingerprints the adapter harness independently.
        return False
    name = path.name
    if ".test." in name or ".unit." in name or ".spec." in name:
        return False
    return "__tests__" not in path.parts


def discover_watched_source_files(root: Path) -> Set[str]:
    """Return the current mounted Builder source set owned by this inventory."""
    files: Set[str] = set()
    roots = (
        root / "ui/apps/demo/src",
        root / "ui/packages/loom-ui/src/features/ExplorerBuilder",
    )
    for source_root in roots:
        if not source_root.is_dir():
            raise SourceReadError("required source tree is missing: %s" % source_root.relative_to(root))
        for path in source_root.rglob("*"):
            if path.is_file() and _is_product_source(path):
                files.add(path.relative_to(root).as_posix())

    for relative in (
        "ui/packages/loom-ui/src/Builder.tsx",
        "ui/packages/loom-ui/src/api.ts",
        "ui/packages/loom-ui/src/types.ts",
    ):
        if not (root / relative).is_file():
            raise SourceReadError("required source file is missing: %s" % relative)
        files.add(relative)
    return files


def _is_safe_repo_path(relative: str) -> bool:
    candidate = Path(relative)
    return not candidate.is_absolute() and ".." not in candidate.parts


def _skip_string_or_comment(text: str, index: int) -> int:
    """Return the first position after a quoted string or comment."""
    char = text[index]
    if char in "'\"`":
        quote = char
        index += 1
        while index < len(text):
            if text[index] == "\\":
                index += 2
                continue
            if text[index] == quote:
                return index + 1
            index += 1
        raise SourceReadError("unterminated string while scanning TypeScript source")
    if char == "/" and index + 1 < len(text):
        if text[index + 1] == "/":
            end = text.find("\n", index + 2)
            return len(text) if end < 0 else end + 1
        if text[index + 1] == "*":
            end = text.find("*/", index + 2)
            if end < 0:
                raise SourceReadError("unterminated comment while scanning TypeScript source")
            return end + 2
    return index


def _matching_delimiter(text: str, start: int) -> int:
    pairs = {"[": "]", "{": "}", "(": ")"}
    first = text[start]
    if first not in pairs:
        raise SourceReadError("expected opening delimiter at offset %d" % start)
    stack = [pairs[first]]
    index = start + 1
    while index < len(text):
        skipped = _skip_string_or_comment(text, index)
        if skipped != index:
            index = skipped
            continue
        char = text[index]
        if char in pairs:
            stack.append(pairs[char])
        elif char in "]})":
            if not stack or char != stack[-1]:
                raise SourceReadError("unbalanced delimiter at offset %d" % index)
            stack.pop()
            if not stack:
                return index
        index += 1
    raise SourceReadError("unterminated delimiter at offset %d" % start)


def _split_top_level(text: str, delimiter: str = ",") -> List[str]:
    pieces: List[str] = []
    stack: List[str] = []
    pairs = {"[": "]", "{": "}", "(": ")"}
    start = 0
    index = 0
    while index < len(text):
        skipped = _skip_string_or_comment(text, index)
        if skipped != index:
            index = skipped
            continue
        char = text[index]
        if char in pairs:
            stack.append(pairs[char])
        elif char in "]})":
            if not stack or char != stack[-1]:
                raise SourceReadError("unbalanced delimiter at offset %d" % index)
            stack.pop()
        elif char == delimiter and not stack:
            pieces.append(text[start:index])
            start = index + 1
        index += 1
    pieces.append(text[start:])
    return pieces


def _declaration_array(text: str, name: str, context: str) -> List[str]:
    match = re.search(
        r"\b(?:const|let|var)\s+" + re.escape(name) + r"\s*=",
        text,
    )
    if not match:
        raise SourceReadError("could not find source declaration %s" % name)
    opening = text.find("[", match.end())
    if opening < 0:
        raise SourceReadError("declaration %s has no array" % name)
    prefix = text[match.end() : opening + 1]
    if context and not re.search(context, prefix):
        raise SourceReadError("declaration %s does not match its expected array form" % name)
    closing = _matching_delimiter(text, opening)
    return _split_top_level(text[opening + 1 : closing])


def _object_property_segments(object_body: str) -> List[str]:
    return _split_top_level(object_body)


def _literal_value_from_segment(segment: str, field: str) -> Optional[str]:
    match = re.match(
        r"\s*(?:" + re.escape(field) + r"|'" + re.escape(field) + r"'|\"" + re.escape(field)
        + r"\")\s*:\s*z\.literal\(\s*(['\"])(.*?)\1\s*\)",
        segment,
        re.DOTALL,
    )
    return match.group(2) if match else None


def _object_member_property_literal(member: str, field: str) -> Optional[str]:
    object_call = re.search(r"\bz\.object\s*\(\s*\{", member)
    if not object_call:
        return None
    opening = member.find("{", object_call.start())
    closing = _matching_delimiter(member, opening)
    for segment in _object_property_segments(member[opening + 1 : closing]):
        value = _literal_value_from_segment(segment, field)
        if value is not None:
            return value
    return None


def _extract_zod_union_literals(text: str, name: str, field: str = "kind") -> List[str]:
    context = r"z\.discriminatedUnion\s*\(\s*['\"]" + re.escape(field) + r"['\"]\s*,\s*"
    members = _declaration_array(text, name, context)
    values = []
    for member in members:
        value = _object_member_property_literal(member, field)
        if value is not None:
            values.append(value)
    if not values:
        raise SourceReadError("schema %s contains no literal %s discriminants" % (name, field))
    return values


def _extract_enum_property(text: str, name: str, field: str) -> List[str]:
    declaration = re.search(
        r"\b(?:const|let|var)\s+" + re.escape(name) + r"\s*=\s*z\.object\s*\(\s*\{",
        text,
    )
    if not declaration:
        raise SourceReadError("could not find source object schema %s" % name)
    opening = text.find("{", declaration.start())
    closing = _matching_delimiter(text, opening)
    for segment in _object_property_segments(text[opening + 1 : closing]):
        property_match = re.match(
            r"\s*" + re.escape(field) + r"\s*:\s*z\.enum\s*\(\s*\[",
            segment,
        )
        if not property_match:
            continue
        array_start = segment.find("[", property_match.start())
        array_end = _matching_delimiter(segment, array_start)
        values = re.findall(r"(['\"])(.*?)\1", segment[array_start + 1 : array_end])
        result = [value for _, value in values]
        if result:
            return result
    raise SourceReadError("schema %s has no literal enum property %s" % (name, field))


def _extract_object_array_property(text: str, name: str, field: str) -> List[str]:
    members = _declaration_array(text, name, r"")
    values = []
    for member in members:
        object_match = re.search(r"\{", member)
        if not object_match:
            continue
        opening = object_match.start()
        closing = _matching_delimiter(member, opening)
        for segment in _object_property_segments(member[opening + 1 : closing]):
            match = re.match(
                r"\s*" + re.escape(field) + r"\s*:\s*(['\"])(.*?)\1",
                segment,
                re.DOTALL,
            )
            if match:
                values.append(match.group(2))
                break
    if not values:
        raise SourceReadError("array %s contains no literal %s values" % (name, field))
    return values


def extract_source_domains(manifest: Dict[str, Any], root: Path) -> Tuple[Set[str], Set[str]]:
    coverage = manifest.get("sourceCoverage")
    if not isinstance(coverage, dict):
        raise SourceReadError("sourceCoverage must be an object")
    extractors = coverage.get("discriminantExtractors")
    if not isinstance(extractors, list) or not extractors:
        raise SourceReadError("sourceCoverage.discriminantExtractors must be a non-empty array")

    extractor_by_schema: Dict[str, Dict[str, Any]] = {}
    for extractor in extractors:
        if not isinstance(extractor, dict):
            raise SourceReadError("discriminantExtractors entries must be objects")
        schema = extractor.get("schema")
        if not isinstance(schema, str) or schema in extractor_by_schema:
            raise SourceReadError("discriminant extractor schema names must be unique strings")
        extractor_by_schema[schema] = extractor

    expected_schemas = {
        "constructionOperationSchema",
        "rowDefinitionSelectionSchema",
        "tableShapeProposalIntentSchema",
        "allOperationFamilies",
    }
    if not expected_schemas.issubset(extractor_by_schema):
        missing = sorted(expected_schemas - set(extractor_by_schema))
        raise SourceReadError("missing required discriminant extractors: %s" % ", ".join(missing))
    unknown = sorted(set(extractor_by_schema) - expected_schemas)
    if unknown:
        raise SourceReadError("unsupported discriminant extractors: %s" % ", ".join(unknown))

    backend_kinds: Set[str] = set()
    families: Set[str] = set()
    for schema, extractor in extractor_by_schema.items():
        relative = extractor.get("file")
        if not isinstance(relative, str):
            raise SourceReadError("extractor %s needs a file" % schema)
        if not _is_safe_repo_path(relative):
            raise SourceReadError("extractor source escapes repository root: %s" % relative)
        watched_paths = {
            item.get("path")
            for item in coverage.get("watchedFiles", [])
            if isinstance(item, dict) and isinstance(item.get("path"), str)
        }
        if relative not in watched_paths:
            raise SourceReadError("extractor source is not covered by watchedFiles: %s" % relative)
        source_path = root / relative
        if not source_path.is_file():
            raise SourceReadError("extractor source is missing: %s" % relative)
        text = source_path.read_text(encoding="utf-8")
        mapping_field = extractor.get("mappingField")

        if schema == "constructionOperationSchema":
            if mapping_field != "affordances[].operation.backendKinds":
                raise SourceReadError("constructionOperationSchema must map to operation.backendKinds")
            excluded = extractor.get("excludes", [])
            if not isinstance(excluded, list) or any(not isinstance(item, str) for item in excluded):
                raise SourceReadError("constructionOperationSchema excludes must be a string array")
            values = _extract_zod_union_literals(text, schema)
            backend_kinds.update(item for item in values if item not in set(excluded))

            combine_name = extractor.get("combineSubkindSchema")
            if not isinstance(combine_name, str):
                raise SourceReadError("constructionOperationSchema needs combineSubkindSchema")
            combine_values = _extract_enum_property(text, combine_name, "kind")
            backend_kinds.update("COMBINE:" + item for item in combine_values)
        elif schema in {"rowDefinitionSelectionSchema", "tableShapeProposalIntentSchema"}:
            if mapping_field != "affordances[].operation.backendKinds":
                raise SourceReadError("%s must map to operation.backendKinds" % schema)
            prefix = extractor.get("prefix")
            if not isinstance(prefix, str) or not prefix:
                raise SourceReadError("%s needs a non-empty prefix" % schema)
            values = _extract_zod_union_literals(text, schema)
            if schema == "tableShapeProposalIntentSchema":
                # NONE is the no-reshape mode. The active action represented by
                # its derived-column payload is the inventory's DERIVED_COLUMN.
                values = ["DERIVED_COLUMN" if item == "NONE" else item for item in values]
            backend_kinds.update(prefix + item for item in values)
        elif schema == "allOperationFamilies":
            if mapping_field != "affordances[].operation.family":
                raise SourceReadError("allOperationFamilies must map to operation.family")
            families.update(_extract_object_array_property(text, schema, "family"))

    if not backend_kinds or not families:
        raise SourceReadError("source extraction produced an empty backend or family domain")
    return backend_kinds, families


def _walk(value: Any, path: str = "$", strings: Optional[List[Tuple[str, str]]] = None,
          cardinalities: Optional[List[Tuple[str, Any]]] = None) -> Tuple[List[Tuple[str, str]], List[Tuple[str, Any]]]:
    if strings is None:
        strings = []
    if cardinalities is None:
        cardinalities = []
    if isinstance(value, str):
        strings.append((path, value))
    elif isinstance(value, dict):
        if "cardinality" in value:
            cardinalities.append((path + ".cardinality", value["cardinality"]))
        for key, item in value.items():
            _walk(item, path + "." + str(key), strings, cardinalities)
    elif isinstance(value, list):
        for index, item in enumerate(value):
            _walk(item, "%s[%d]" % (path, index), strings, cardinalities)
    return strings, cardinalities


def _validate_line_references(manifest: Dict[str, Any], root: Path) -> List[str]:
    errors: List[str] = []
    line_count_cache: Dict[str, int] = {}
    strings, _ = _walk(manifest)
    for location, text in strings:
        for match in REFERENCE_RE.finditer(text):
            relative, first_s, last_s = match.groups()
            first = int(first_s)
            last = int(last_s) if last_s else first
            if not _is_safe_repo_path(relative):
                errors.append("%s: source reference escapes repository root: %s" % (location, relative))
                continue
            source = root / relative
            if first < 1 or last < first:
                errors.append("%s: invalid source line range %s:%d-%d" % (location, relative, first, last))
                continue
            if not source.is_file():
                errors.append("%s: source reference file does not exist: %s" % (location, relative))
                continue
            try:
                if relative not in line_count_cache:
                    with source.open("r", encoding="utf-8") as stream:
                        line_count_cache[relative] = sum(1 for _ in stream)
                line_count = line_count_cache[relative]
            except OSError as error:
                errors.append("%s: cannot read %s: %s" % (location, relative, error))
                continue
            if last > line_count:
                errors.append(
                    "%s: source range %s:%d-%d exceeds %d lines"
                    % (location, relative, first, last, line_count)
                )
    return errors


def _validate_cardinalities(manifest: Dict[str, Any]) -> List[str]:
    errors: List[str] = []
    _, cardinalities = _walk(manifest)
    for location, value in cardinalities:
        if not isinstance(value, dict):
            errors.append("%s must be an object" % location)
            continue
        if {"min", "max"}.issubset(value):
            low, high = value["min"], value["max"]
        elif {"minimum", "maximum"}.issubset(value):
            low, high = value["minimum"], value["maximum"]
        else:
            errors.append("%s must define min/max or minimum/maximum" % location)
            continue
        if isinstance(low, bool) or not isinstance(low, int) or low < 0:
            errors.append("%s minimum must be a non-negative integer" % location)
        if high is not None and (isinstance(high, bool) or not isinstance(high, int) or high < 0):
            errors.append("%s maximum must be a non-negative integer or null" % location)
        if isinstance(low, int) and not isinstance(low, bool) and isinstance(high, int) and not isinstance(high, bool) and low > high:
            errors.append("%s minimum exceeds maximum" % location)
    return errors


def _validate_unique_ids(manifest: Dict[str, Any]) -> Tuple[List[str], Dict[str, Dict[str, Any]]]:
    errors: List[str] = []
    entries = manifest.get("affordances")
    if not isinstance(entries, list):
        return ["affordances must be an array"], {}
    by_id: Dict[str, Dict[str, Any]] = {}
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            errors.append("affordances[%d] must be an object" % index)
            continue
        entry_id = entry.get("id")
        if not isinstance(entry_id, str) or not entry_id.strip():
            errors.append("affordances[%d].id must be a non-empty string" % index)
            continue
        if entry_id in by_id:
            errors.append("duplicate affordance id: %s" % entry_id)
        by_id[entry_id] = entry
        evidence = entry.get("evidence")
        if not isinstance(evidence, list) or not evidence:
            errors.append("%s needs source evidence" % entry_id)
        dimensions = entry.get("dimensions", [])
        if not isinstance(dimensions, list):
            errors.append("%s dimensions must be an array" % entry_id)
        else:
            dimension_ids: Set[str] = set()
            for dimension_index, dimension in enumerate(dimensions):
                if not isinstance(dimension, dict):
                    errors.append("%s dimensions[%d] must be an object" % (entry_id, dimension_index))
                    continue
                dimension_id = dimension.get("id")
                if not isinstance(dimension_id, str) or not dimension_id.strip():
                    errors.append("%s dimensions[%d].id must be a non-empty string" % (entry_id, dimension_index))
                elif dimension_id in dimension_ids:
                    errors.append("%s has duplicate dimension id: %s" % (entry_id, dimension_id))
                else:
                    dimension_ids.add(dimension_id)

    source_coverage = manifest.get("sourceCoverage")
    if not isinstance(source_coverage, dict):
        errors.append("sourceCoverage must be an object")
        source_coverage = {}
    bindings = source_coverage.get("bindings", [])
    if not isinstance(bindings, list):
        errors.append("sourceCoverage.bindings must be an array")
        bindings = []
    binding_ids: Set[str] = set()
    for index, binding in enumerate(bindings):
        if not isinstance(binding, dict):
            errors.append("sourceCoverage.bindings[%d] must be an object" % index)
            continue
        binding_id = binding.get("id")
        if not isinstance(binding_id, str) or not binding_id.strip():
            errors.append("sourceCoverage.bindings[%d].id must be a non-empty string" % index)
            continue
        if binding_id in binding_ids:
            errors.append("duplicate source binding id: %s" % binding_id)
        binding_ids.add(binding_id)
        if binding_id not in by_id:
            errors.append("source binding references missing affordance id: %s" % binding_id)
        evidence = binding.get("evidence")
        if not isinstance(evidence, list) or not evidence:
            errors.append("source binding %s needs evidence" % binding_id)

    disabled = manifest.get("disabledAffordances", [])
    if not isinstance(disabled, list):
        errors.append("disabledAffordances must be an array")
        disabled = []
    disabled_ids: Set[str] = set()
    for index, item in enumerate(disabled):
        if not isinstance(item, dict):
            errors.append("disabledAffordances[%d] must be an object" % index)
            continue
        item_id = item.get("id")
        if not isinstance(item_id, str) or not item_id.strip():
            errors.append("disabledAffordances[%d].id must be a non-empty string" % index)
            continue
        if item_id in disabled_ids or item_id in by_id:
            errors.append("duplicate inventory id: %s" % item_id)
        disabled_ids.add(item_id)
        evidence = item.get("evidence")
        if not isinstance(evidence, list) or not evidence:
            errors.append("disabledAffordances[%s] needs source evidence" % item_id)
        canonical_ids = item.get("canonicalIds")
        if not isinstance(canonical_ids, list) or not canonical_ids:
            errors.append("disabledAffordances[%s].canonicalIds must be a non-empty array" % item_id)
        else:
            for canonical_id in canonical_ids:
                if not isinstance(canonical_id, str) or canonical_id not in by_id:
                    errors.append(
                        "disabledAffordances[%s] references missing canonical affordance: %r"
                        % (item_id, canonical_id)
                    )

    exclusions = manifest.get("excludedSurfaces")
    if not isinstance(exclusions, list):
        errors.append("excludedSurfaces must be an array")
        exclusions = []
    exclusion_ids: Set[str] = set()
    for index, exclusion in enumerate(exclusions):
        if not isinstance(exclusion, dict):
            errors.append("excludedSurfaces[%d] must be an object" % index)
            continue
        exclusion_id = exclusion.get("id")
        if not isinstance(exclusion_id, str) or not exclusion_id.strip():
            errors.append("excludedSurfaces[%d].id must be a non-empty string" % index)
        elif exclusion_id in exclusion_ids or exclusion_id in by_id or exclusion_id in disabled_ids:
            errors.append("duplicate excluded surface id: %s" % exclusion_id)
        else:
            exclusion_ids.add(exclusion_id)
        basis = exclusion.get("basis")
        if not isinstance(basis, str) or basis not in EXCLUSION_BASES:
            errors.append("excluded surface %r has unknown basis %r" % (exclusion_id, basis))
        reason = exclusion.get("reason")
        if not isinstance(reason, str) or not reason.strip():
            errors.append("excluded surface %r needs a reason" % exclusion_id)
        evidence = exclusion.get("evidence")
        if not isinstance(evidence, list) or not evidence:
            errors.append("excluded surface %r needs source evidence" % exclusion_id)
        proof = exclusion.get("proof")
        if basis == "unmounted" and not isinstance(proof, dict):
            errors.append("unmounted surface %r needs source proof" % exclusion_id)

    aliases = manifest.get("aliases", [])
    if not isinstance(aliases, list):
        errors.append("aliases must be an array")
    else:
        alias_ids: Set[str] = set()
        for index, alias in enumerate(aliases):
            if not isinstance(alias, dict):
                errors.append("aliases[%d] must be an object" % index)
                continue
            alias_id = alias.get("id")
            canonical_id = alias.get("canonicalId")
            if not isinstance(alias_id, str) or not alias_id.strip():
                errors.append("aliases[%d].id must be a non-empty string" % index)
            elif alias_id in alias_ids or alias_id in by_id:
                errors.append("duplicate inventory alias id: %s" % alias_id)
            else:
                alias_ids.add(alias_id)
            if not isinstance(canonical_id, str) or canonical_id not in by_id:
                errors.append("aliases[%s] references missing canonical affordance: %r" % (alias_id, canonical_id))

    source_coverage = manifest.get("sourceCoverage")
    mappings = source_coverage.get("surfaceMappings", []) if isinstance(source_coverage, dict) else []
    if not isinstance(mappings, list):
        errors.append("sourceCoverage.surfaceMappings must be an array")
    else:
        for index, mapping in enumerate(mappings):
            if not isinstance(mapping, dict):
                errors.append("sourceCoverage.surfaceMappings[%d] must be an object" % index)
                continue
            canonical_id = mapping.get("canonicalId")
            if not isinstance(canonical_id, str) or canonical_id not in by_id:
                errors.append(
                    "sourceCoverage.surfaceMappings[%d] references missing canonical affordance: %r"
                    % (index, canonical_id)
                )
            evidence = mapping.get("evidence")
            if not isinstance(evidence, list) or not evidence:
                errors.append("sourceCoverage.surfaceMappings[%d] needs source evidence" % index)
    return errors, by_id


def _validate_coverage(by_id: Dict[str, Dict[str, Any]], require_complete: bool) -> List[str]:
    errors: List[str] = []
    for entry_id, entry in by_id.items():
        availability = entry.get("availability")
        status = availability.get("status") if isinstance(availability, dict) else None
        if not isinstance(status, str) or status not in ACTIVE_AVAILABILITY:
            errors.append("%s has unknown availability status %r" % (entry_id, status))
        coverage = entry.get("coverage")
        if not isinstance(coverage, dict):
            errors.append("%s is missing its coverage row" % entry_id)
            continue
        missing = EXPECTED_COVERAGE_FIELDS - set(coverage)
        if missing:
            errors.append("%s coverage row is missing fields: %s" % (entry_id, ", ".join(sorted(missing))))
            continue
        model_status = coverage.get("modelStatus")
        adapter_status = coverage.get("adapterStatus")
        if not isinstance(model_status, str) or model_status not in MODEL_STATUSES:
            errors.append("%s has unknown modelStatus %r" % (entry_id, model_status))
        if not isinstance(adapter_status, str) or adapter_status not in ADAPTER_STATUSES:
            errors.append("%s has unknown adapterStatus %r" % (entry_id, adapter_status))
        runner_actions = coverage.get("runnerActions")
        if not isinstance(runner_actions, dict):
            errors.append("%s coverage.runnerActions must be an object" % entry_id)
            runner_actions = {}
        runner_action_status = runner_actions.get("status")
        if not isinstance(runner_action_status, str) or runner_action_status not in RUNNER_ACTION_STATUSES:
            errors.append("%s has unknown coverage.runnerActions.status %r" % (entry_id, runner_action_status))
        runner_action_bindings = runner_actions.get("bindings")
        if not isinstance(runner_action_bindings, dict):
            errors.append("%s coverage.runnerActions.bindings must be an object" % entry_id)
            runner_action_bindings = {}
        else:
            for action_id, runner_action_id in runner_action_bindings.items():
                if not isinstance(action_id, str) or not action_id.strip():
                    errors.append("%s coverage.runnerActions has an empty local action id" % entry_id)
                if not isinstance(runner_action_id, str) or not runner_action_id.strip():
                    errors.append("%s coverage.runnerActions[%r] needs a non-empty runnerActionId" % (entry_id, action_id))
            if runner_action_status == "mapped" and not runner_action_bindings:
                errors.append("%s coverage.runnerActions is mapped but has no action bindings" % entry_id)
        if require_complete:
            if model_status != "modeled":
                errors.append("%s model coverage is not modeled (status=%r)" % (entry_id, model_status))
            if adapter_status != "mapped":
                errors.append("%s adapter coverage is not mapped (status=%r)" % (entry_id, adapter_status))
            if runner_action_status != "mapped":
                errors.append("%s runner action coverage is not mapped (status=%r)" % (entry_id, runner_action_status))
            if not runner_action_bindings:
                errors.append("%s is missing coverage.runnerActions bindings" % entry_id)
            for field in ("modelStateId", "adapterId", "runnerActionId"):
                value = coverage.get(field)
                if not isinstance(value, str) or not value.strip():
                    errors.append("%s is missing coverage.%s" % (entry_id, field))
    return errors


def _validate_request_contracts(manifest: Dict[str, Any]) -> List[str]:
    errors: List[str] = []
    for entry in manifest.get("affordances", []) if isinstance(manifest.get("affordances"), list) else []:
        if not isinstance(entry, dict):
            continue
        entry_id = entry.get("id", "<unknown>")
        requests = entry.get("requests", [])
        flows = entry.get("requestFlows", [])
        if not isinstance(requests, list):
            errors.append("%s requests must be an array" % entry_id)
            continue
        if not isinstance(flows, list):
            errors.append("%s requestFlows must be an array" % entry_id)
            flows = []
        if requests and not flows:
            errors.append("%s has request rows but no requestFlows" % entry_id)

        seen_checkpoints: Set[str] = set()
        request_by_checkpoint: Dict[str, Dict[str, Any]] = {}
        for index, request in enumerate(requests):
            location = "%s requests[%d]" % (entry_id, index)
            if not isinstance(request, dict):
                errors.append("%s must be an object" % location)
                continue
            flow_id = request.get("flowId")
            step_id = request.get("stepId")
            action_id = request.get("actionId")
            order = request.get("order")
            method = request.get("method")
            endpoint = request.get("endpoint")
            kind = request.get("kind")
            checkpoint = request.get("checkpointId")
            if not isinstance(flow_id, str) or not flow_id.strip():
                errors.append("%s flowId must be a non-empty string" % location)
            if not isinstance(step_id, str) or not step_id.strip():
                errors.append("%s stepId must be a non-empty string" % location)
            if isinstance(order, bool) or not isinstance(order, int) or order < 1:
                errors.append("%s order must be a positive integer" % location)
            if method not in REQUEST_METHODS:
                errors.append("%s method must be an explicit supported HTTP method; got %r" % (location, method))
            if not isinstance(endpoint, str) or not endpoint.startswith("/api/") or re.search(r"\s", endpoint):
                errors.append("%s endpoint must be a concrete API path string" % location)
            if kind not in REQUEST_KINDS:
                errors.append("%s kind must be an explicit semantic request kind; got %r" % (location, kind))
            if not isinstance(action_id, str) or not action_id.strip():
                errors.append("%s actionId must be a non-empty string" % location)
            if not isinstance(checkpoint, str) or not checkpoint.strip():
                errors.append("%s checkpointId is required" % location)
            elif isinstance(flow_id, str) and isinstance(order, int) and not isinstance(order, bool) and isinstance(step_id, str):
                expected_checkpoint = "%s:%s:%s" % (flow_id, order, step_id)
                if checkpoint != expected_checkpoint:
                    errors.append("%s checkpointId must equal %s" % (location, expected_checkpoint))
                if checkpoint in seen_checkpoints:
                    errors.append("%s duplicates checkpointId %s" % (location, checkpoint))
                seen_checkpoints.add(checkpoint)
                request_by_checkpoint[checkpoint] = request
            payload_shape = request.get("payloadShape")
            if not isinstance(payload_shape, str) or not payload_shape.strip():
                errors.append("%s payloadShape must explicitly describe body/query presence" % location)
            contract = request.get("requestContract")
            if not isinstance(contract, dict):
                errors.append("%s requestContract must be an object" % location)
                continue
            if contract.get("method") != method:
                errors.append("%s requestContract.method must match request method" % location)
            source_evidence = contract.get("evidence")
            if not isinstance(source_evidence, list) or not source_evidence or any(not isinstance(item, str) or not item for item in source_evidence):
                errors.append("%s requestContract.evidence must contain source references" % location)
            for key in ("body", "query"):
                part = contract.get(key)
                if not isinstance(part, dict):
                    errors.append("%s requestContract.%s must be an object" % (location, key))
                    continue
                presence = part.get("presence")
                if presence not in {"absent", "object"}:
                    errors.append("%s requestContract.%s.presence must be absent|object" % (location, key))
                    continue
                if presence == "object":
                    if key == "body" and (not isinstance(part.get("schema"), str) or not part.get("schema", "").strip()):
                        errors.append("%s requestContract.body.schema is required for an object body" % location)
                    for field_name in ("requiredFields", "optionalFields"):
                        fields = part.get(field_name)
                        if not isinstance(fields, list) or any(not isinstance(value, str) or not value.strip() for value in fields):
                            errors.append("%s requestContract.%s.%s must be a string array" % (location, key, field_name))
                            continue
                        if len(fields) != len(set(fields)):
                            errors.append("%s requestContract.%s.%s contains duplicates" % (location, key, field_name))
                    required = part.get("requiredFields", [])
                    optional = part.get("optionalFields", [])
                    if isinstance(required, list) and isinstance(optional, list) and set(required).intersection(optional):
                        errors.append("%s requestContract.%s fields cannot be both required and optional" % (location, key))
                    variants = part.get("variantFields")
                    if variants is not None and (not isinstance(variants, dict) or not variants or any(not isinstance(name, str) or not isinstance(fields, list) or any(not isinstance(value, str) for value in fields) for name, fields in variants.items())):
                        errors.append("%s requestContract.%s.variantFields must map variants to string arrays" % (location, key))
            body = contract.get("body")
            if isinstance(payload_shape, str) and isinstance(body, dict):
                if body.get("presence") == "absent" and not payload_shape.lower().startswith("no body"):
                    errors.append("%s requestContract says body absent but payloadShape does not" % location)
                if body.get("presence") == "object" and payload_shape.lower().startswith("no body"):
                    errors.append("%s requestContract says object body but payloadShape says no body" % location)
                if method == "GET" and body.get("presence") != "absent":
                    errors.append("%s GET request must explicitly declare an absent body" % location)
            headers = contract.get("headers")
            if not isinstance(headers, dict) or not isinstance(headers.get("optional", []), list):
                errors.append("%s requestContract.headers.optional must be a string array" % location)
            elif any(not isinstance(value, str) or not value.strip() for value in headers.get("optional", [])):
                errors.append("%s requestContract.headers.optional contains an invalid header" % location)
            if not isinstance(request.get("constructor"), str) or not request.get("constructor", "").strip():
                errors.append("%s constructor must preserve its source/API constructor" % location)
            source_constructors = request.get("sourceConstructors", [])
            if not isinstance(source_constructors, list):
                errors.append("%s sourceConstructors must be an array" % location)

        flow_checkpoints: Set[str] = set()
        for flow_index, flow in enumerate(flows):
            if not isinstance(flow, dict):
                errors.append("%s requestFlows[%d] must be an object" % (entry_id, flow_index))
                continue
            steps = flow.get("steps")
            if not isinstance(steps, list):
                errors.append("%s requestFlows[%d].steps must be an array" % (entry_id, flow_index))
                continue
            for step_index, step in enumerate(steps):
                if not isinstance(step, dict):
                    errors.append("%s requestFlows[%d].steps[%d] must be an object" % (entry_id, flow_index, step_index))
                    continue
                checkpoint = step.get("checkpointId")
                if not isinstance(checkpoint, str) or checkpoint not in request_by_checkpoint:
                    errors.append("%s request flow step has no matching request checkpoint: %r" % (entry_id, checkpoint))
                    continue
                if checkpoint in flow_checkpoints:
                    errors.append("%s requestFlows repeats checkpointId %s" % (entry_id, checkpoint))
                flow_checkpoints.add(checkpoint)
                request = request_by_checkpoint[checkpoint]
                for field_name in ("flowId", "order", "stepId", "kind", "method", "endpoint", "actionId", "payloadShape", "requestContract"):
                    if step.get(field_name) != request.get(field_name):
                        errors.append("%s request flow checkpoint %s mismatches %s" % (entry_id, checkpoint, field_name))
        if flow_checkpoints != set(request_by_checkpoint):
            missing = sorted(set(request_by_checkpoint) - flow_checkpoints)
            extra = sorted(flow_checkpoints - set(request_by_checkpoint))
            errors.append("%s requestFlows/request checkpoint sets differ (missing=%s extra=%s)" % (entry_id, missing, extra))
    return errors


def _validate_source_hashes_and_file_set(manifest: Dict[str, Any], root: Path) -> List[str]:
    errors: List[str] = []
    coverage = manifest.get("sourceCoverage")
    if not isinstance(coverage, dict):
        return ["sourceCoverage must be an object"]
    watched = coverage.get("watchedFiles")
    if not isinstance(watched, list) or not watched:
        return ["sourceCoverage.watchedFiles must be a non-empty array"]
    expected: Dict[str, str] = {}
    for index, item in enumerate(watched):
        if not isinstance(item, dict):
            errors.append("sourceCoverage.watchedFiles[%d] must be an object" % index)
            continue
        relative = item.get("path")
        digest = item.get("sha256")
        if not isinstance(relative, str) or not relative:
            errors.append("sourceCoverage.watchedFiles[%d].path must be a string" % index)
            continue
        if not _is_safe_repo_path(relative):
            errors.append("watched source path escapes repository root: %s" % relative)
            continue
        if relative in expected:
            errors.append("duplicate watched source path: %s" % relative)
        if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
            errors.append("%s has invalid sha256" % relative)
            continue
        expected[relative] = digest

    try:
        actual_paths = discover_watched_source_files(root)
    except (OSError, SourceReadError) as error:
        errors.append(str(error))
        actual_paths = set()
    missing = sorted(actual_paths - set(expected))
    stale = sorted(set(expected) - actual_paths)
    if missing:
        errors.append("source file-set drift: new/unwatched active files: %s" % ", ".join(missing))
    if stale:
        errors.append("source file-set drift: watched files no longer active/present: %s" % ", ".join(stale))

    for relative, expected_digest in expected.items():
        source = root / relative
        if not source.is_file():
            errors.append("watched source file is missing: %s" % relative)
            continue
        actual_digest = hashlib.sha256(source.read_bytes()).hexdigest()
        if actual_digest != expected_digest:
            errors.append("source hash drift: %s expected %s got %s" % (relative, expected_digest, actual_digest))

    mounted = coverage.get("mountedOperationSources")
    if not isinstance(mounted, list) or not mounted:
        errors.append("sourceCoverage.mountedOperationSources must be a non-empty array")
    else:
        for relative in mounted:
            if not isinstance(relative, str) or not _is_safe_repo_path(relative) or relative not in expected:
                errors.append("mounted operation source is not watched: %r" % relative)
            elif not (root / relative).is_file():
                errors.append("mounted operation source is missing: %s" % relative)
    return errors


def _validate_bindings(manifest: Dict[str, Any], observed_kinds: Set[str],
                       observed_families: Set[str], by_id: Dict[str, Dict[str, Any]]) -> List[str]:
    errors: List[str] = []
    coverage = manifest.get("sourceCoverage", {})
    bindings = coverage.get("bindings", []) if isinstance(coverage, dict) else []
    entry_kinds: Dict[str, Set[str]] = {}
    family_rows: Dict[str, Set[str]] = {}
    for entry_id, entry in by_id.items():
        operation = entry.get("operation")
        if not isinstance(operation, dict):
            errors.append("%s is missing operation" % entry_id)
            continue
        kinds = operation.get("backendKinds")
        if not isinstance(kinds, list) or any(not isinstance(kind, str) or not kind for kind in kinds):
            errors.append("%s operation.backendKinds must be a string array" % entry_id)
            continue
        if len(kinds) != len(set(kinds)):
            errors.append("%s operation.backendKinds contains duplicates" % entry_id)
        entry_kinds[entry_id] = set(kinds)
        family = operation.get("family")
        if not isinstance(family, str) or not family:
            errors.append("%s operation.family must be a non-empty string" % entry_id)
        else:
            family_rows.setdefault(family, set()).add(entry_id)

    binding_rows: Dict[str, Set[str]] = {}
    for binding in bindings if isinstance(bindings, list) else []:
        if not isinstance(binding, dict) or not isinstance(binding.get("id"), str):
            continue
        binding_kinds = binding.get("backendKinds")
        if not isinstance(binding_kinds, list) or any(not isinstance(kind, str) or not kind for kind in binding_kinds):
            errors.append("source binding %s backendKinds must be a string array" % binding.get("id"))
            continue
        binding_rows[binding["id"]] = set(binding_kinds)
        if binding["id"] in entry_kinds and set(binding_kinds) != entry_kinds[binding["id"]]:
            errors.append("source binding %s backendKinds do not match its affordance row" % binding["id"])

    inventory_kinds = set().union(*entry_kinds.values()) if entry_kinds else set()
    bound_kinds = set().union(*binding_rows.values()) if binding_rows else set()
    for kind in sorted(inventory_kinds - observed_kinds):
        errors.append("inventory backend kind has no active source discriminant: %s" % kind)
    for kind in sorted(bound_kinds - observed_kinds):
        errors.append("source binding contains an unobserved backend kind: %s" % kind)

    for kind in sorted(observed_kinds):
        row_ids = {entry_id for entry_id, kinds in entry_kinds.items() if kind in kinds}
        if not row_ids:
            errors.append("unbound UI schema token %s" % kind)
            continue
        matched = row_ids.intersection(binding_rows)
        if not any(kind in binding_rows.get(entry_id, set()) for entry_id in matched):
            errors.append("unbound UI schema token %s: no sourceCoverage.bindings row" % kind)

    for family in sorted(observed_families):
        if family not in family_rows:
            errors.append("unbound mounted operation family %s" % family)
    return errors


def validate_manifest(manifest: Dict[str, Any], root: Path, require_complete: bool = True) -> List[str]:
    errors: List[str] = []
    if not isinstance(manifest, dict):
        return ["manifest must be an object"]
    if manifest.get("sourceCommit") != SOURCE_COMMIT:
        errors.append("sourceCommit must equal %s" % SOURCE_COMMIT)
    errors.extend(_validate_source_hashes_and_file_set(manifest, root))
    id_errors, by_id = _validate_unique_ids(manifest)
    errors.extend(id_errors)
    errors.extend(_validate_coverage(by_id, require_complete))
    errors.extend(_validate_request_contracts(manifest))
    errors.extend(_validate_cardinalities(manifest))
    errors.extend(_validate_line_references(manifest, root))
    try:
        observed_kinds, observed_families = extract_source_domains(manifest, root)
    except (OSError, SourceReadError) as error:
        errors.append("source discriminant extraction failed: %s" % error)
    else:
        errors.extend(_validate_bindings(manifest, observed_kinds, observed_families, by_id))
    return errors


def _update_source_hash(manifest: Dict[str, Any], root: Path, relative: str) -> None:
    digest = hashlib.sha256((root / relative).read_bytes()).hexdigest()
    for item in manifest["sourceCoverage"]["watchedFiles"]:
        if item["path"] == relative:
            item["sha256"] = digest
            return
    raise SourceReadError("test mutation source is not watched: %s" % relative)


def _run_mutation_probes(manifest: Dict[str, Any], root: Path) -> List[str]:
    results: List[str] = []
    baseline_errors = validate_manifest(manifest, root, require_complete=False)
    if baseline_errors:
        return ["self-test baseline source-only validation failed: " + baseline_errors[0]]

    omitted = copy.deepcopy(manifest)
    omitted["affordances"] = [
        entry for entry in omitted["affordances"] if entry.get("id") != "builder.reshape.group_coded"
    ]
    omission_errors = validate_manifest(omitted, root, require_complete=False)
    if not any("unbound UI schema token CODED_GROUP" in error for error in omission_errors):
        return ["self-test omission probe failed to expose unbound CODED_GROUP"]
    results.append("PASS omission probe: removing builder.reshape.group_coded exposes unbound CODED_GROUP")

    with tempfile.TemporaryDirectory(prefix="ui-contract-source-probe-") as temporary:
        scratch_root = Path(temporary)
        for item in manifest["sourceCoverage"]["watchedFiles"]:
            relative = item["path"]
            source = root / relative
            destination = scratch_root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, destination)
        scratch = copy.deepcopy(manifest)
        types_path = "ui/packages/loom-ui/src/types.ts"
        source_path = scratch_root / types_path
        source = source_path.read_text(encoding="utf-8")
        anchor = "const constructionOperationSchema = z.discriminatedUnion('kind', ["
        if anchor not in source:
            return ["self-test source mutation anchor not found"]
        source = source.replace(
            anchor,
            anchor + "\n  z.object({ kind: z.literal('SCRATCH_ADDED'), scratch: z.string() }).strict(),",
            1,
        )
        source_path.write_text(source, encoding="utf-8")
        _update_source_hash(scratch, scratch_root, types_path)
        added_errors = validate_manifest(scratch, scratch_root, require_complete=False)
        if not any("unbound UI schema token SCRATCH_ADDED" in error for error in added_errors):
            return ["self-test scratch source probe failed to expose unbound SCRATCH_ADDED"]
    results.append("PASS source-add probe: a scratch SCRATCH_ADDED discriminant is rejected as unbound")
    return results


def _parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="source checkout root")
    parser.add_argument("--manifest", type=Path, default=None, help="inventory JSON (default: under --root)")
    parser.add_argument("--check", action="store_true", help="run strict model/adapter coverage validation (default)")
    parser.add_argument(
        "--source-only",
        action="store_true",
        help="validate inventory structure and source coverage while allowing explicit pending rows",
    )
    parser.add_argument("--self-test", action="store_true", help="run required omission and source-add probes")
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = _parse_args(argv)
    root = args.root.resolve()
    manifest_path = args.manifest or (root / DEFAULT_MANIFEST)
    if not manifest_path.is_absolute():
        manifest_path = root / manifest_path
    try:
        manifest = load_inventory(root, manifest_path)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print("FAIL cannot read manifest %s: %s" % (manifest_path, error), file=sys.stderr)
        return 1

    if args.self_test:
        results = _run_mutation_probes(manifest, root)
        for result in results:
            print(result)
        return 0 if len(results) == 2 and all(result.startswith("PASS ") for result in results) else 1

    errors = validate_manifest(manifest, root, require_complete=not args.source_only)
    if errors:
        print("FAIL UI contract inventory: %d issue(s)" % len(errors), file=sys.stderr)
        for error in errors:
            print("- " + error, file=sys.stderr)
        return 1
    entries = manifest.get("affordances", [])
    print(
        "PASS UI contract inventory: %d affordances, source hashes and file set current, source bindings complete%s"
        % (len(entries), "" if args.source_only else ", model/adapter rows complete")
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
