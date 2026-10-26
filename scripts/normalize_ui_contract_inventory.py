#!/usr/bin/env python3
"""Normalize UI affordance fragments into the stable Builder contract ABI.

The fragments are evidence inputs, not independent model registries. This
script folds aliases into canonical IDs and preserves source-specific evidence
while keeping semantic domains/request branches machine-readable.
"""

from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs/ui-contract-inventory/ui-supported-affordances.json"
BASE = "dc331783dde3f61e764bab3a421b66b28698c9d4"
FRAGMENTS = [
    "expression.fragment.json",
    "relational.fragment.json",
    "lifecycle.fragment.json",
    "additional-mounted.fragment.json",
]

ALIASES = [
    {"id": "root_selection.initial_table", "canonicalId": "builder.first_table_from_root", "basis": "same first-table-from-root transition"},
    {"id": "lifecycle.row_root_change_review", "canonicalId": "builder.root.change_or_replace", "basis": "same row-root review/apply transition"},
    {"id": "builder.add_columns_source_selection", "canonicalId": "builder.add_columns.source_selection", "basis": "lifecycle path is decomposed into source-browse and child add actions"},
    {"id": "builder.related_field_column", "canonicalId": "builder.add_columns.related_field", "basis": "same RELATED_FIELD operation; additional saved-step entry path"},
]

FAMILY_BY_ID = {
    "builder.filter_rows.scalar_condition": "KEEP_ROWS",
    "builder.filter_rows.related_eligibility": "KEEP_ROWS",
    "builder.calculate.derive": "CALCULATE",
    "builder.calculate.table_shape_derived_column": "CALCULATE",
    "builder.add_columns.source_selection": "ADD_COLUMNS",
    "builder.add_columns.catalog_semantic": "ADD_COLUMNS",
    "builder.add_columns.catalog_field": "ADD_COLUMNS",
    "builder.add_columns.related_field": "ADD_COLUMNS",
    "builder.add_columns.grouped_row_values": "ADD_COLUMNS",
    "builder.column_filter_presentation": "COLUMN_PRESENTATION",
    "builder.reshape.group_field": "RESHAPE",
    "builder.reshape.expand_scalar_list": "RESHAPE",
    "builder.reshape.group_coded": "RESHAPE",
    "builder.row_definition.explicit_groups": "ROW_DEFINITION",
    "builder.reshape.pivot_values": "RESHAPE",
    "builder.reshape.pivot_coded": "RESHAPE",
    "builder.reshape.unpivot": "RESHAPE",
    "builder.row_definition.expanded_source_rows": "ROW_DEFINITION",
    "builder.reshape.related_expand": "RESHAPE",
    "builder.combine.key_join": "COMBINE",
    "builder.combine.append": "COMBINE",
    "builder.combine.membership": "COMBINE",
    "legacy.table_shape.grouped_pivot": "TABLE_SHAPE",
    "legacy.table_shape.unpivot": "TABLE_SHAPE",
    "builder.related_source_column": "ADD_COLUMNS",
    "builder.row_definition.rows_per_starting_record": "ROW_DEFINITION",
}

OPERATION_KINDS_BY_ID = {
    "builder.filter_rows.scalar_condition": ["FILTER"],
    "builder.filter_rows.related_eligibility": ["RELATED_ELIGIBILITY"],
    "builder.calculate.derive": ["DERIVE"],
    "builder.calculate.table_shape_derived_column": ["TABLE_SHAPE:DERIVED_COLUMN"],
    "builder.reshape.group_field": ["GROUP", "ROW_DEFINITION:FIELD_GROUP"],
    "builder.reshape.expand_scalar_list": ["EXPAND"],
    "builder.reshape.group_coded": ["CODED_GROUP"],
    "builder.row_definition.explicit_groups": ["ROW_DEFINITION:EXPLICIT_GROUP"],
    "builder.row_definition.expanded_source_rows": ["ROW_DEFINITION:EXPANDED"],
    "builder.row_definition.rows_per_starting_record": ["ROW_DEFINITION:RECORDS"],
    "builder.reshape.pivot_values": ["PIVOT"],
    "builder.reshape.pivot_coded": ["CODED_PIVOT"],
    "builder.reshape.unpivot": ["UNPIVOT"],
    "builder.reshape.related_expand": ["RELATED_EXPAND"],
    "builder.combine.key_join": ["COMBINE:KEY_JOIN"],
    "builder.combine.append": ["COMBINE:APPEND"],
    "builder.combine.membership": ["COMBINE:MEMBERSHIP"],
    "builder.related_source_column": ["RELATED_SOURCE"],
    "builder.add_columns.related_field": ["RELATED_FIELD"],
}

STAGE_CAPABILITY_IDS = {
    "builder.filter_rows.scalar_condition",
    "builder.filter_rows.related_eligibility",
    "builder.calculate.derive",
    "builder.add_columns.source_selection",
    "builder.add_columns.grouped_row_values",
    "builder.add_columns.related_field",
    "builder.related_source_column",
    "builder.construction_step_editor_routing",
    "builder.construction_proposal_apply_cancel_retry",
    "builder.reshape.group_field",
    "builder.reshape.expand_scalar_list",
    "builder.reshape.group_coded",
    "builder.reshape.pivot_values",
    "builder.reshape.pivot_coded",
    "builder.reshape.unpivot",
    "builder.reshape.related_expand",
}

CONSTRUCTOR_BY_ID = {
    "builder.filter_rows.scalar_condition": ("ConstructionOperationEditor.buildFilterCandidate", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:396-455"]),
    "builder.calculate.derive": ("ConstructionOperationEditor.buildDeriveCandidate", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:627-682"]),
    "builder.reshape.group_field": ("ConstructionReshapeEditor.candidateFor -> groupStepFromForm -> candidateIntentFor", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:873-905", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:984-1034", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:1958-1981"]),
    "builder.combine.key_join": ("ConstructionCombineEditor.buildCandidate", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:246-390"]),
    "builder.combine.append": ("ConstructionCombineEditor.buildCandidate", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:246-390"]),
    "builder.combine.membership": ("ConstructionCombineEditor.buildCandidate", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:246-390"]),
    "builder.related_source_column": ("BuilderWorkspace.buildRelatedSourceCandidate -> constructionLifecycle.proposeCandidateAndWait", ["ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1544-1673", "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1854-1864"]),
    "builder.add_columns.related_field": ("RelatedFieldEditor.candidateFor -> onCandidateChange", ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedFieldEditor.tsx:34-64", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedFieldEditor.tsx:168-181"]),
}


def request_contract(
    method: str,
    symbol: str,
    required_body: list[str] | None = None,
    optional_body: list[str] | None = None,
    required_query: list[str] | None = None,
    optional_query: list[str] | None = None,
    schema: str | None = None,
    source: list[str] | None = None,
    variants: dict[str, list[str]] | None = None,
    headers: list[str] | None = None,
) -> dict:
    body_absent = required_body is None and optional_body is None and schema is None
    query_present = required_query is not None or optional_query is not None
    return {
        "method": method,
        "constructor": symbol,
        "body": ({
            "presence": "absent",
        } if body_absent else {
            "presence": "object",
            "schema": schema or symbol,
            "requiredFields": required_body or [],
            "optionalFields": optional_body or [],
            **({"variantFields": variants} if variants else {}),
        }),
        "query": ({
            "presence": "object",
            "requiredFields": required_query or [],
            "optionalFields": optional_query or [],
        } if query_present else {"presence": "absent"}),
        "headers": {"optional": headers or []},
        "evidence": source or [],
    }


# Request contracts are transcribed from the LoomClient API constructor and
# its request schemas. A new endpoint without a source-backed row here remains
# an explicit normalization failure; HTTP method and body presence are never
# inferred from a POST default.
API_REQUEST_CONTRACTS = {
    "/semantic-inventory": request_contract("POST", "browseSemanticInventory", ["snapshotToken", "rowRoot"], ["frameId", "sourceChoiceId", "selectionMode", "outputId", "resourceType", "query", "cursor", "limit", "fieldPresentation"], schema="semanticInventoryBrowseRequestSchema", source=["ui/packages/loom-ui/src/api.ts:1495-1514", "ui/packages/loom-ui/src/types.ts:3561-3582"]),
    "/construction-choices": request_contract("POST", "searchConstructionChoices", ["snapshotToken", "outputId", "source"], ["occurrenceId", "limit", "cursor"], schema="SearchConstructionChoicesArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1555-1563", "ui/packages/loom-ui/src/api.ts:355-363"]),
    "/construction-choice-proposals": request_contract("POST", "proposeConstructionChoices", ["commandId", "snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "constructionChoices"], ["limit"], schema="constructionChoiceProposalRequestSchema", source=["ui/packages/loom-ui/src/api.ts:1691-1695", "ui/packages/loom-ui/src/types.ts:2664-2675"]),
    "/commands": request_contract("POST", "applyCommands", ["commandId", "semanticsVersion", "snapshotToken", "expectedDraftVersion", "commands"], ["expectedDraftDigest"], required_query=[], optional_query=["auth_resource_path"], schema="ApplyExplorerBuilderCommandsArgs body projection + ExplorerBuilderCommand[]", source=["ui/packages/loom-ui/src/api.ts:151-158", "ui/packages/loom-ui/src/api.ts:1375-1387", "ui/packages/loom-ui/src/api.ts:1450-1465"], headers=["X-Request-ID"]),
    "/related-field-choices": request_contract("POST", "searchRelatedFieldChoices", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "stageId"], ["query", "limit", "cursor"], schema="SearchRelatedFieldChoicesArgs body projection", source=["ui/packages/loom-ui/src/api.ts:378-388", "ui/packages/loom-ui/src/api.ts:1576-1586"]),
    "/construction-proposals": request_contract("POST", "proposeConstruction", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "candidateConstruction"], ["changedStepId", "removeStepIds", "groupSources", "groupSource", "limit", "pivotSources"], required_query=[], optional_query=["auth_resource_path"], schema="constructionProposalRequestSchema", source=["ui/packages/loom-ui/src/api.ts:482-483", "ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1685-1689", "ui/packages/loom-ui/src/types.ts:2073-2089"]),
    "/preview": request_contract("POST", "preview", ["receiptId", "outputId"], ["limit"], schema="PreviewExplorerBuilderArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1697-1698"]),
    "/construction-capabilities": request_contract("POST", "getConstructionCapabilities", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "stageId"], ["groupStepId"], required_query=[], optional_query=["auth_resource_path"], schema="constructionCapabilitiesRequestSchema", source=["ui/packages/loom-ui/src/api.ts:457-460", "ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1652-1662", "ui/packages/loom-ui/src/types.ts:1937-1945"]),
    "/table-shape-capabilities": request_contract("POST", "getTableShapeCapabilities", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId"], required_query=[], optional_query=["auth_resource_path"], schema="tableShapeCapabilitiesRequestSchema", source=["ui/packages/loom-ui/src/api.ts:427-435", "ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1621-1627", "ui/packages/loom-ui/src/types.ts:979-985"]),
    "/table-shape-resolutions": request_contract("POST", "resolveTableShape", ["catalogId", "expectedDraftDigest", "expectedDraftVersion", "kind", "outputId", "snapshotToken"], required_query=[], optional_query=["auth_resource_path"], schema="tableShapeResolutionRequestSchema", source=["ui/packages/loom-ui/src/api.ts:443-444", "ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1640-1644", "ui/packages/loom-ui/src/types.ts:1007-1075"], variants={"kind=PIVOT": ["pivot"], "kind=UNPIVOT": ["unpivot"], "kind=DERIVED": ["derived"]}),
    "/table-shape-proposals": request_contract("POST", "proposeTableShape", ["catalogId", "derivedResolutionIds", "expectedDraftDigest", "expectedDraftVersion", "mode", "outputId", "snapshotToken"], ["limit", "reshapeResolutionId"], required_query=[], optional_query=["auth_resource_path"], schema="tableShapeProposalRequestSchema", source=["ui/packages/loom-ui/src/api.ts:446-447", "ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1646-1650", "ui/packages/loom-ui/src/types.ts:1250-1261"]),
    "/column-source": request_contract("POST", "inspectColumnSource", ["snapshotToken", "outputId", "column"], schema="InspectColumnSourceArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1549-1554"]),
    "/related-expand-choices": request_contract("POST", "searchRelatedExpandChoices", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "stageId", "anchorColumnId", "targetResourceType"], ["limit", "cursor"], schema="SearchRelatedExpandChoicesArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1564-1575"]),
    "/related-expand-contributors": request_contract("POST", "searchRelatedExpandContributors", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "stageId", "routeChoiceId"], ["query", "limit", "cursor"], schema="SearchRelatedExpandContributorsArgs body projection", source=["ui/packages/loom-ui/src/api.ts:390-400", "ui/packages/loom-ui/src/api.ts:1587-1598"]),
    "/frame-source-options": request_contract("POST", "browseFrameSourceOptions", ["snapshotToken", "outputId"], ["resourceType", "query", "cursor", "limit"], schema="BrowseFrameSourceOptionsArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1540-1548"]),
    "/interpretation-revisions": request_contract("POST", "createInterpretationRevisionFromColumn", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "column", "libraryId", "explanation"], ["parentRevisionId"], required_query=[], optional_query=["auth_resource_path"], schema="CreateInterpretationRevisionFromColumnArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1766-1779"]),
    "/interpretation-preview": request_contract("POST", "previewInterpretationCandidate", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "column", "revisionId"], ["limit"], schema="PreviewInterpretationCandidateArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1781-1790"]),
    "/population-routes": request_contract("POST", "searchPopulationRoutes", ["snapshotToken", "outputId", "selectionRevisionId"], ["limit", "cursor"], schema="SearchPopulationRoutesArgs body projection", source=["ui/packages/loom-ui/src/api.ts:403-410", "ui/packages/loom-ui/src/api.ts:1599-1606"]),
    "/population-mapping": request_contract("POST", "populationMapping", ["receiptId", "outputId"], ["cursor", "limit"], schema="PopulationMappingArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1699-1700"]),
    "/row-lineage": request_contract("POST", "rowLineage", ["receiptId", "outputId", "rowId"], ["offset", "limit"], schema="RowLineageArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1710-1717"]),
    "/construction-category-discoveries": request_contract("POST", "discoverConstructionCategories", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "stageId", "categoryColumnId", "valueColumnId"], ["pivotStepId", "groupKeyIds", "pivotSources", "candidateConstruction"], schema="constructionCategoryDiscoveryRequestSchema", source=["ui/packages/loom-ui/src/api.ts:462-470", "ui/packages/loom-ui/src/api.ts:1663-1678", "ui/packages/loom-ui/src/types.ts:2020-2033"]),
    "/row-definition-choices": request_contract("GET", "listRowDefinitionChoices", required_query=["outputId", "snapshotToken"], optional_query=[], source=["ui/packages/loom-ui/src/api.ts:412-415", "ui/packages/loom-ui/src/api.ts:1607-1611"]),
    "/row-definition-proposals": request_contract("POST", "proposeRowDefinition", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "selection"], ["limit"], schema="ProposeRowDefinitionArgs body projection", source=["ui/packages/loom-ui/src/api.ts:417-425", "ui/packages/loom-ui/src/api.ts:1612-1620"]),
    "/population-member-proposals": request_contract("POST", "proposePopulationMemberRemoval", ["snapshotToken", "expectedDraftVersion", "expectedDraftDigest", "outputId", "baseSelectionRevisionId", "removedMember"], ["limit"], required_query=[], optional_query=["auth_resource_path"], schema="ProposePopulationMemberRemovalArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1375-1380", "ui/packages/loom-ui/src/api.ts:1823-1832"]),
    "/explicit-groups": request_contract("POST", "createExplicitGroupRevision", ["snapshotToken", "idempotencyKey", "groups"], required_query=[], optional_query=["auth_resource_path"], schema="explicitGroupCreateRequestSchema", source=["ui/packages/loom-ui/src/api.ts:488-494", "ui/packages/loom-ui/src/api.ts:1833-1840", "ui/packages/loom-ui/src/types.ts:690-695"]),
    "/selections": request_contract("POST", "createSelection", ["snapshotToken", "idempotencyKey", "source"], ["exclusions"], required_query=[], optional_query=["auth_resource_path"], schema="CreateSelectionArgs body projection", source=["ui/packages/loom-ui/src/api.ts:1375-1377", "ui/packages/loom-ui/src/api.ts:1806-1812"], headers=["X-Request-ID"]),
    "/selections/{selectionRevision}": request_contract("GET", "getSelection", required_query=[], optional_query=["cursor", "limit", "auth_resource_path"], source=["ui/packages/loom-ui/src/api.ts:1813-1821"]),
}


def load_fragments() -> list[dict]:
    entries = []
    for name in FRAGMENTS:
        path = ROOT / "docs/ui-contract-inventory" / name
        if not path.exists():
            if name == "additional-mounted.fragment.json":
                continue
            raise SystemExit(f"missing required fragment {path}")
        data = json.loads(path.read_text())
        for row in data.get("entries", []):
            entries.append({"fragment": name, "row": row})
    return entries


def source_refs(value) -> list[str]:
    refs: list[str] = []
    if isinstance(value, str):
        refs.extend(re.findall(r"(?<![A-Za-z0-9_./-])((?:ui|internal)/[A-Za-z0-9_./-]+:\d+(?:-\d+)?)", value))
    elif isinstance(value, dict):
        for child in value.values():
            refs.extend(source_refs(child))
    elif isinstance(value, list):
        for child in value:
            refs.extend(source_refs(child))
    return list(dict.fromkeys(refs))


def cardinality(domain: dict) -> dict:
    c = domain.get("cardinality", {})
    minimum = c.get("minimum", c.get("min", 0))
    maximum = c.get("maximum", c.get("max"))
    if not isinstance(minimum, (int, float)):
        minimum = 0
    if maximum is not None and not isinstance(maximum, (int, float)):
        maximum = None
    return {"min": minimum, "max": maximum}


def normalize_dimension(domain: dict, prefix: str) -> dict:
    values = domain.get("finiteValues", domain.get("values", domain.get("finite_enum_values")))
    provider = domain.get("dynamicProvider", domain.get("provider"))
    if provider is None and domain.get("dynamic_providers"):
        provider = {"providers": domain["dynamic_providers"]}
    kind = domain.get("kind", "dynamic-reference" if provider else "scalar")
    typ = domain.get("type", domain.get("valueType", kind))
    result = {
        "id": str(domain.get("id", f"{prefix}.dimension")),
        "kind": kind,
        "type": typ,
        "dependsOn": domain.get("dependsOn", domain.get("dependencies", [])),
        "cardinality": cardinality(domain),
        "evidence": domain.get("evidence", domain.get("source", [])),
    }
    if isinstance(values, list):
        result["finiteValues"] = values
    if provider is not None:
        result["dynamicProvider"] = provider
    if domain.get("description"):
        result["description"] = domain["description"]
    if domain.get("unit"):
        result["unit"] = domain["unit"]
    return result


def api_request_contract(endpoint: str, method: str | None) -> dict | None:
    path = re.sub(r"\[\?[^\]]*\]", "", endpoint).split("?", 1)[0].rstrip("/.,;")
    for suffix in sorted(API_REQUEST_CONTRACTS, key=len, reverse=True):
        if path.endswith(suffix):
            contract = API_REQUEST_CONTRACTS[suffix]
            if method is not None and method != contract["method"]:
                raise SystemExit(
                    "request method/source contract mismatch for %s: fragment says %s, LoomClient %s says %s"
                    % (endpoint, method, contract["constructor"], contract["method"])
                )
            return json.loads(json.dumps(contract))
    return None


def inline_request_contract(payload_shape: str, method: str, evidence: list[str]) -> dict:
    lowered = payload_shape.strip().lower()
    no_body = lowered.startswith("no body") or " (query)" in lowered
    query_match = re.search(r"\bquery\s*\{([^}]*)\}", payload_shape, re.IGNORECASE)
    query_fields = []
    if query_match:
        query_fields = [
            item.strip().rstrip("?")
            for item in query_match.group(1).split(",")
            if item.strip()
        ]
    elif " (query)" in lowered:
        brace = re.search(r"\{([^}]*)\}", payload_shape)
        if brace:
            query_fields = [
                item.strip().rstrip("?")
                for item in brace.group(1).split(",")
                if item.strip()
            ]
    if no_body:
        body = {"presence": "absent"}
    else:
        notation = re.search(r"\{([^}]*)\}", payload_shape)
        fields = []
        optional = []
        if notation:
            for match in re.finditer(r"(?:^|[,\s])([A-Za-z_$][\w$]*)(\?)?\s*:", notation.group(1)):
                (optional if match.group(2) else fields).append(match.group(1))
        body = {
            "presence": "object",
            "schema": "inventory-inline",
            "notation": payload_shape,
            "requiredFields": fields,
            "optionalFields": optional,
        }
    query = ({
        "presence": "object",
        "requiredFields": query_fields,
        "optionalFields": [],
    } if query_match or " (query)" in lowered else {"presence": "absent"})
    if not evidence:
        raise SystemExit("request payload contract has no source evidence: %s" % payload_shape)
    return {
        "method": method,
        "constructor": "inventory-inline source notation",
        "body": body,
        "query": query,
        "headers": {"optional": ["X-Request-ID"] if "request-id" in lowered else []},
        "evidence": evidence,
    }


def source_constructor_records(value: object, prefix: str = "") -> list[dict]:
    records: list[dict] = []
    if isinstance(value, dict):
        for key, child in value.items():
            path = f"{prefix}.{key}" if prefix else str(key)
            lowered = str(key).lower()
            if isinstance(child, str) and any(token in lowered for token in ("constructor", "request", "candidate", "endpoint")):
                records.append({"sourcePath": path, "description": child})
            records.extend(source_constructor_records(child, path))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            records.extend(source_constructor_records(child, f"{prefix}[{index}]"))
    return records


def unique_source_constructor_records(records: list[dict]) -> list[dict]:
    unique: list[dict] = []
    seen: set[tuple[str, str]] = set()
    for item in records:
        key = (item["sourcePath"], item["description"])
        if key not in seen:
            seen.add(key)
            unique.append(item)
    return unique


def normalize_request(row: dict, flow_id: str | None = None) -> list[dict]:
    requests = row.get("requests")
    request_source = row.get("request") or row.get("payload_constructor")
    if requests is None:
        req = request_source
        if not isinstance(req, dict):
            req = {}
        requests = []
        def collect(value: object, prefix: str = "", parent: dict | None = None) -> None:
            if isinstance(value, dict):
                for key, child in value.items():
                    collect(child, f"{prefix}.{key}" if prefix else key, value)
            elif isinstance(value, list):
                for index, child in enumerate(value):
                    collect(child, f"{prefix}[{index}]", parent)
            elif isinstance(value, str):
                match = re.search(r"\b(GET|POST|PUT|PATCH|DELETE)\s+(/api/v1/\S+)", value)
                if match:
                    siblings = {
                        key: child for key, child in (parent or {}).items()
                        if key != prefix.rsplit(".", 1)[-1]
                    }
                    requests.append({
                        "stepId": prefix.rsplit(".", 1)[-1],
                        "method": match[1],
                        "endpoint": match[2],
                        "constructor": value,
                        "sourcePath": prefix,
                        "sourceSiblings": siblings,
                    })
        collect(req)
        for key in ["capabilities_endpoint", "resolution_endpoint", "proposal_endpoint", "category_discovery_endpoint"]:
            value = row.get(key)
            match = re.search(r"\b(GET|POST|PUT|PATCH|DELETE)\s+(/api/v1/\S+)", value) if isinstance(value, str) else None
            if match:
                requests.append({"stepId": key, "method": match[1], "endpoint": match[2], "constructor": value})
    out = []
    for request in requests:
        if not isinstance(request, dict):
            continue
        variant = (row.get("request") or {}).get("selectedVariant") if isinstance(row.get("request"), dict) else None
        step_name = str(request.get("stepId", ""))
        if variant == "RELATED_SOURCE" and "activeRelatedField" in step_name:
            continue
        if variant == "RELATED_FIELD" and any(token in step_name.lower() for token in ("routedchoice", "routedsource")):
            continue
        endpoint = request.get("endpoint") or request.get("proposal_endpoint") or request.get("applyEndpoint")
        method = request.get("method")
        method_prefix = re.match(r"^(GET|POST|PUT|PATCH|DELETE)\s+", endpoint) if isinstance(endpoint, str) else None
        if method_prefix:
            if method is not None and method != method_prefix.group(1):
                raise SystemExit(
                    "request method disagrees with endpoint constructor for %s: %s vs %s"
                    % (request.get("stepId", "request"), method, method_prefix.group(1))
                )
            method = method_prefix.group(1)
        if isinstance(endpoint, str):
            endpoint = re.sub(r"^(?:GET|POST|PUT|PATCH|DELETE)\s+", "", endpoint).rstrip(",.;")
        step_id = request.get("stepId", request.get("actionId", request.get("variant", "request")))
        api_contract = api_request_contract(endpoint, method) if isinstance(endpoint, str) else None
        if method is None and api_contract is not None:
            method = api_contract["method"]
        if not isinstance(method, str) or not method:
            raise SystemExit("request %s has no source-backed HTTP method" % step_id)
        if not isinstance(endpoint, str) or not endpoint.startswith("/api/"):
            raise SystemExit("request %s has no source-backed API endpoint" % step_id)
        kind = request.get("kind")
        key_and_path = f"{step_id} {endpoint or ''}".lower()
        if kind not in {"read", "capability", "assessment", "proposal", "preview", "command", "reconcile", "refresh", "inspect"}:
            if "row-change" in key_and_path:
                kind = "assessment"
            elif "proposal" in key_and_path:
                kind = "proposal"
            elif "preview" in key_and_path:
                kind = "preview"
            elif endpoint and endpoint.endswith("/reconcile"):
                kind = "reconcile"
            elif endpoint and endpoint.endswith("/builder"):
                kind = "refresh"
            elif "construction-capabilities" in key_and_path or "table-shape-capabilities" in key_and_path:
                kind = "capability"
            elif any(token in key_and_path for token in ("choices", "contributors", "semantic-inventory", "suggestions", "category-discover", "resolution", "frame-source-options", "construction-inputs", "population-routes", "route-search", "memberendpoint")):
                kind = "read"
            elif any(token in key_and_path for token in ("row-source", "row-lineage", "column-source", "inspect", "population-mapping")):
                kind = "inspect"
            elif method == "GET":
                kind = "read"
            else:
                kind = "command"
        this_flow = request.get("flowId", request.get("variant", flow_id or "primary"))
        order = request.get("order")
        if order is None:
            order = 1 + sum(1 for item in out if item["flowId"] == this_flow)
        evidence = list(dict.fromkeys(source_refs(request) + source_refs(request_source) + source_refs(row)))
        payload_shape = request.get("payloadShape", request.get("payload_shape"))
        source_payload_shape = payload_shape
        if api_contract is not None:
            api_contract["method"] = method
            api_contract["evidence"] = list(dict.fromkeys(api_contract["evidence"] + evidence))
        elif payload_shape is not None:
            api_contract = inline_request_contract(str(payload_shape), method, evidence)
        else:
            raise SystemExit(
                "request %s %s has no explicit payloadShape or source-backed API contract"
                % (method, endpoint)
            )
        if payload_shape is None:
            body_contract = api_contract["body"]
            query_contract = api_contract["query"]
            if body_contract["presence"] == "absent":
                query_fields = query_contract.get("requiredFields", []) + query_contract.get("optionalFields", [])
                payload_shape = "no body" + ("; query {%s}" % ",".join(query_fields) if query_contract["presence"] == "object" else "")
            else:
                payload_shape = "body object schema %s" % body_contract["schema"]
        source_fields = request_source if isinstance(request_source, dict) else {}
        source_constructors = unique_source_constructor_records(
            source_constructor_records(source_fields) + source_constructor_records(request.get("sourceSiblings", {}))
        )
        constructor = request.get("constructor")
        if not isinstance(constructor, str) or re.fullmatch(r"(?:GET|POST|PUT|PATCH|DELETE)\s+/api/v1/\S+", constructor):
            constructor = "LoomClient.%s" % api_contract["constructor"] if api_contract else "inventory-inline source constructor"
        out.append({
            "order": order,
            "flowId": this_flow,
            "stepId": step_id,
            "checkpointId": "%s:%s:%s" % (this_flow, order, step_id),
            "kind": kind,
            "method": method,
            "endpoint": endpoint,
            "actionId": request.get("actionId", step_id),
            "payloadShape": payload_shape,
            **({"sourcePayloadShape": source_payload_shape} if source_payload_shape is not None else {}),
            "requestContract": api_contract,
            "constructor": constructor,
            "sourceConstructors": source_constructors,
            "condition": request.get("condition", request.get("when")),
            "evidence": evidence,
        })
    return out


def finalize_request_record(request: dict, affordance_id: str) -> dict:
    endpoint = request.get("endpoint")
    method = request.get("method")
    contract = api_request_contract(endpoint, method) if isinstance(endpoint, str) else None
    if method is None and contract is not None:
        method = contract["method"]
        request["method"] = method
    if method not in {"GET", "POST", "PUT", "PATCH", "DELETE"}:
        raise SystemExit("%s request %r has no source-backed HTTP method" % (affordance_id, request.get("stepId")))
    if not isinstance(endpoint, str) or not endpoint.startswith("/api/"):
        raise SystemExit("%s request %r has no source-backed API endpoint" % (affordance_id, request.get("stepId")))
    evidence = request.get("evidence", [])
    payload_shape = request.get("payloadShape")
    if contract is not None:
        contract["method"] = method
        contract["evidence"] = list(dict.fromkeys(contract["evidence"] + evidence))
    elif isinstance(payload_shape, str) and payload_shape.strip():
        contract = inline_request_contract(payload_shape, method, evidence)
    else:
        raise SystemExit(
            "%s request %s %s has no explicit payloadShape or source-backed API contract"
            % (affordance_id, method, endpoint)
        )
    if contract is not None and contract["constructor"] != "inventory-inline source notation":
        source_payload_shape = request.get("sourcePayloadShape")
        if source_payload_shape is None and request.get("requestContract") is None:
            source_payload_shape = payload_shape
        if source_payload_shape is not None:
            request["sourcePayloadShape"] = source_payload_shape
        body = contract["body"]
        query = contract["query"]
        if body["presence"] == "absent":
            query_fields = query.get("requiredFields", []) + query.get("optionalFields", [])
            payload_shape = "no body" + ("; query {%s}" % ",".join(query_fields) if query["presence"] == "object" else "")
        else:
            payload_shape = "body object schema %s" % body["schema"]
    elif payload_shape is None:
        body = contract["body"]
        query = contract["query"]
        if body["presence"] == "absent":
            query_fields = query.get("requiredFields", []) + query.get("optionalFields", [])
            payload_shape = "no body" + ("; query {%s}" % ",".join(query_fields) if query["presence"] == "object" else "")
        else:
            payload_shape = "body object schema %s" % body["schema"]
    request["payloadShape"] = payload_shape
    if request.get("sourcePayloadShape") == payload_shape:
        request.pop("sourcePayloadShape")
    request["requestContract"] = contract
    request["checkpointId"] = "%s:%s:%s" % (request.get("flowId", "primary"), request.get("order"), request.get("stepId"))
    request.setdefault("constructor", "LoomClient.%s" % contract["constructor"])
    request.setdefault("sourceConstructors", [])
    return request


def seed_for(affordance_id: str, row: dict, lifecycle: bool) -> dict:
    raw = row.get("seed")
    operation_family = FAMILY_BY_ID.get(affordance_id, "LIFECYCLE")
    refs = source_refs(row)
    if raw:
        source_requirements = raw.get("requirements", [])
        kind = raw.get("kind")
        sid = raw.get("id", affordance_id + ".seed")
    else:
        sid = affordance_id + ".seed"
        kind = {
            "ADD_COLUMNS": "rooted_table_add_columns",
            "KEEP_ROWS": "rooted_table_filter",
            "CALCULATE": "rooted_table_calculate",
            "RESHAPE": "rooted_table_reshape",
            "COMBINE": "two_table_combine",
            "ROW_DEFINITION": "rooted_table_row_definition",
            "TABLE_SHAPE": "rooted_table_table_shape",
            "COLUMN_PRESENTATION": "configured_table_columns",
        }.get(operation_family, "rooted_builder_workspace")
        source_requirements = []
    # Machine-readable fixture preconditions; sourceRequirements retain the
    # original UI-language statements for review without making the generator
    # parse prose.
    lifecycle_preconditions = {
        "builder.demo_entry_route": [("demo.query", "available", True), ("demo.mode", "oneOf", ["builder", "viewer"])],
        "builder.workspace_hydration_and_explorer_selection": [("identity.projectId", "nonEmpty", True)],
        "builder.explorer_create_delete": [("workspace.explorerListLoaded", "equals", True)],
        "builder.first_table_from_root": [("workspace.tableCount", "equals", 0), ("catalog.snapshotToken", "nonEmpty", True), ("catalog.eligibleRootNodeCount", "atLeast", 1)],
        "builder.table_route_and_occurrence_selection": [("workspace.selectedTable", "exists", True), ("catalog.snapshotToken", "nonEmpty", True), ("selectedOccurrence.belongsToDerivedRoute", "equals", True)],
        "builder.add_columns.source_selection": [("workspace.selectedTable.rooted", "equals", True), ("catalog.snapshotToken", "nonEmpty", True), ("workspace.activeConstructionFamily", "equals", "ADD_COLUMNS")],
        "builder.construction_step_editor_routing": [("workspace.selectedTable.rooted", "equals", True), ("draft.identity", "exists", True), ("constructionHistory.selection", "oneOf", ["source", "saved-step"])],
        "builder.root.change_or_replace": [("workspace.selectedTable.rooted", "equals", True), ("draft.identity", "exists", True), ("startingRecordSettings.open", "equals", True)],
        "builder.construction_proposal_apply_cancel_retry": [("capabilities.identityMatchesCurrentStage", "equals", True), ("candidateIntent.mountedEditor", "equals", True)],
        "builder.server_saved_draft_and_reconcile": [("builderState.hydrated", "equals", True), ("draft.identity", "exists", True)],
        "builder.automatic_preview_refresh_and_recovery": [("workspace.selectedTable.rooted", "equals", True), ("preview.selectedOutputVisible", "equals", True), ("catalog.snapshotToken", "nonEmpty", True), ("draft.identity", "exists", True)],
        "builder.restore_previous_draft_undo": [("workspace.tableCount", "equals", 0), ("previousDraftRevisionId", "nonEmpty", True)],
        "builder.delete_explorer_no_fallback": [("capabilities.deleteExplorer", "equals", True), ("workspace.explorerCount", "equals", 1)],
        "builder.row_definition_proposal_apply_cancel": [("workspace.selectedTable.rooted", "equals", True), ("catalog.snapshotToken", "nonEmpty", True), ("draft.identity", "exists", True), ("rowDefinitionSettings.open", "equals", True)],
    }
    preconditions = []
    if affordance_id in lifecycle_preconditions:
        for index, (subject, operator, value) in enumerate(lifecycle_preconditions[affordance_id], 1):
            preconditions.append({"id": f"seed-precondition-{index}", "subject": subject, "operator": operator, "value": value, "evidence": refs})
    else:
        preconditions.extend([
            {"id": "seed-precondition-1", "subject": "workspace.selectedTable", "operator": "exists", "value": True, "evidence": refs},
            {"id": "seed-precondition-2", "subject": "catalog.snapshotToken", "operator": "nonEmpty", "value": True, "evidence": refs},
            {"id": "seed-precondition-3", "subject": "draft.identity", "operator": "exists", "value": True, "evidence": refs},
        ])
        if affordance_id in STAGE_CAPABILITY_IDS:
            preconditions.append({"id": "seed-precondition-stage", "subject": "capabilities.selectedStageIdentity", "operator": "matchesCurrentDraft", "value": True, "evidence": refs})
        if operation_family == "COMBINE":
            preconditions.append({"id": "seed-precondition-second-input", "subject": "catalog.combineInputs", "operator": "hasCompatibleSecondInput", "value": True, "evidence": refs})
    is_initial_table = affordance_id == "builder.first_table_from_root"
    identity_by_id = {
        "builder.demo_entry_route": ["urlQuery.mode", "projectId", "explorerId", "selectionRevisionId?", "featureFocus?"],
        "builder.workspace_hydration_and_explorer_selection": ["projectId", "explorerId?"],
        "builder.explorer_create_delete": ["projectId", "explorerId?"],
        "builder.first_table_from_root": ["projectId", "explorerId", "catalogSnapshotToken", "eligibleRowRootNodeId"],
        "builder.restore_previous_draft_undo": ["projectId", "explorerId", "previousDraftRevisionId"],
        "builder.delete_explorer_no_fallback": ["projectId", "explorerId", "deleteCapability", "explorerCount"],
    }
    identity = identity_by_id.get(affordance_id, ["projectId", "explorerId"])
    if operation_family != "LIFECYCLE" and not is_initial_table:
        identity += ["snapshotToken", "outputId", "expectedDraftVersion", "expectedDraftDigest"]
    table_shape_capability_required = operation_family == "TABLE_SHAPE" or affordance_id == "builder.calculate.table_shape_derived_column"
    if table_shape_capability_required:
        identity.append("tableShapeCapabilitiesResponse")
    stage_capability_required = affordance_id in STAGE_CAPABILITY_IDS
    if stage_capability_required:
        identity.append("selectedStageCapabilityResponse")
    if is_initial_table:
        identity = identity_by_id[affordance_id]
    if table_shape_capability_required:
        capability_kind = "table-shape-api-response"
        capability_status = "required-not-yet-observed"
        capability_source = "actual POST /table-shape-capabilities response bound to catalog/output identity"
        capability_endpoint = "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-capabilities"
    elif affordance_id == "builder.delete_explorer_no_fallback":
        capability_kind = "explorer-list-delete-state"
        capability_status = "required-not-yet-observed"
        capability_source = "actual loaded Explorer record deleteEnabled flag plus current Explorer count"
        capability_endpoint = "/api/v1/projects/{project}/explorers"
    elif "selectedStageCapabilityResponse" in identity:
        capability_kind = "go-compiled-construction-capabilities"
        capability_status = "required-not-yet-observed"
        capability_source = "actual construction-capabilities response from Go-prepared workspace, bound to output/stage/draft identity"
        capability_endpoint = "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-capabilities"
    else:
        capability_kind = "not-applicable"
        capability_status = "not-required-before-action"
        capability_source = None
        capability_endpoint = None
    return {
        "id": sid,
        "kind": kind,
        "requirements": preconditions,
        "sourceRequirements": source_requirements,
        "identityPrerequisites": list(dict.fromkeys(identity)),
        "capabilityFixture": {
            "kind": capability_kind,
            "required": capability_status == "required-not-yet-observed",
            "source": capability_source,
            "endpoint": capability_endpoint,
            "status": capability_status,
        },
        "provenance": {
            "workspace": "required-from-actual-Builder-UI-seed-phase; pending",
            "capability": capability_source + "; pending" if capability_source else "not-required-before-action",
            "sourceEvidence": refs,
        },
    }


def status_for(row: dict) -> str:
    if row.get("id") == "builder.delete_explorer_no_fallback":
        return "visible-disabled"
    availability_text = str(row.get("availability", "")).lower()
    status = row.get("status", row.get("availability", "reachable"))
    if isinstance(status, dict):
        return status.get("status", "reachable")
    status_text = str(status).lower()
    if "visible-but-disabled" in status_text or "visible-disabled" in status_text:
        return "visible-disabled"
    if "reachable_when_" in status_text or "mounted only when" in availability_text:
        return "context-conditional"
    if "capability" in availability_text or "selection-conditional" in availability_text:
        return "capability-conditional"
    if "capability-conditional" in status_text:
        return "capability-conditional"
    if row.get("capability_gates"):
        return "capability-conditional"
    return str(status)


def canonicalize() -> tuple[list[dict], list[dict], list[dict]]:
    raw_entries = load_fragments()
    rows: dict[str, dict] = {}
    aliases = list(ALIASES)
    extra_paths: dict[str, list[dict]] = {}
    mapping_rows = []
    for source in raw_entries:
        fragment, row = source["fragment"], source["row"]
        fid = row["id"]
        if fragment == "additional-mounted.fragment.json" and "canonicalId" in row:
            mapping_rows.append(row)
            continue
        if fid == "builder.add_columns_source_selection":
            alias_target = "builder.add_columns.source_selection"
            extra_paths.setdefault(alias_target, []).append(row)
            continue
        if fid == "builder.related_field_column":
            extra_paths.setdefault("builder.add_columns.related_field", []).append(row)
            continue
        if fid == "builder.add_columns.related_field":
            # This record documents both a RELATED_SOURCE choice flow and an
            # active-record RELATED_FIELD flow. Split those into canonical rows.
            related_source = dict(row)
            related_source["id"] = "builder.related_source_column"
            related_source["operation"] = {"family": "ADD_COLUMNS", "variant": "RELATED_SOURCE"}
            related_source["dimensions"] = [d for d in row.get("dimensions", []) if d.get("id", "").startswith("related_source")]
            related_source["request"] = {**row.get("request", {}), "selectedVariant": "RELATED_SOURCE"}
            extra_paths.setdefault("builder.related_source_column", []).append(related_source)
            row = dict(row)
            row["operation"] = {"family": "ADD_COLUMNS", "variant": "RELATED_FIELD"}
            row["dimensions"] = [d for d in row.get("dimensions", []) if not d.get("id", "").startswith("related_source")]
            row["request"] = {**row.get("request", {}), "selectedVariant": "RELATED_FIELD"}
        if fid not in rows:
            rows[fid] = {"fragment": fragment, "row": row, "sources": [row]}
        else:
            rows[fid]["sources"].append(row)
    for canonical_id, merged_rows in extra_paths.items():
        for row in merged_rows:
            if canonical_id not in rows:
                rows[canonical_id] = {"fragment": "merged", "row": row, "sources": [row]}
            else:
                rows[canonical_id]["sources"].append(row)

    # Include new actions from the supplemental fragment as real affordances.
    supplemental = ROOT / "docs/ui-contract-inventory/additional-mounted.fragment.json"
    if supplemental.exists():
        doc = json.loads(supplemental.read_text())
        mapping_rows.extend(doc.get("surfaceMappings", []))

    # Translate a fragment record into one stable, model-facing affordance.
    out = []
    for fid, bundle in rows.items():
        fragment = bundle["fragment"]
        primary = bundle["row"]
        sources = bundle["sources"]
        refs = list(dict.fromkeys(ref for src in sources for ref in source_refs(src)))
        is_lifecycle = fragment == "lifecycle.fragment.json"
        operation = primary.get("operation", {})
        family = FAMILY_BY_ID.get(fid)
        family = family or (operation.get("family") if isinstance(operation, dict) else None) or primary.get("family") or ("LIFECYCLE" if is_lifecycle else "UI_STATE")
        variants = operation.get("variants", []) if isinstance(operation, dict) else []
        variant = operation.get("variant") if isinstance(operation, dict) else None
        if not variants and variant:
            variants = [variant]
        backend_kinds = OPERATION_KINDS_BY_ID.get(fid, [])
        if fid.startswith("legacy.table_shape."):
            backend_kinds = ["TABLE_SHAPE:" + ("GROUPED_PIVOT" if fid.endswith("grouped_pivot") else "UNPIVOT")]
        path = primary.get("path", primary.get("ui_path", primary.get("entryPath", primary.get("surface"))))
        if isinstance(path, list):
            entry_paths = [{"label": "primary", "path": path}]
        elif path:
            entry_paths = [{"label": "primary", "path": path}]
        else:
            entry_paths = []
        for src in sources[1:]:
            src_path = src.get("path", src.get("ui_path", src.get("entryPath", src.get("surface"))))
            if src_path and src_path != path:
                entry_paths.append({"label": "additional", "path": src_path})
        requests = []
        for src in sources:
            flow = "primary" if src is primary else f"source-{sources.index(src)+1}"
            if fid == "builder.add_columns.source_selection" and src.get("id") == "builder.add_columns_source_selection":
                # The lifecycle fragment also lists the nested child add flow;
                # those are separate canonical affordances, so keep only the
                # source-browse reads under this local selection action.
                requests.extend(req for req in normalize_request(src, flow) if req["kind"] == "read" and "frame-source-options" in req["endpoint"])
                continue
            if fid == "builder.add_columns.related_field" and src.get("id") == "builder.related_field_column":
                # Keep this row as an alternate saved-step path and domain
                # evidence, but the expression source carries its complete
                # request sequence for the same RELATED_FIELD operation.
                continue
            if fid == "builder.related_source_column" and src.get("id") == "builder.related_source_column" and not src.get("request"):
                # The expression source row contributes the ordered choice /
                # proposal / preview / apply sequence; relational source row
                # remains the candidate constructor evidence.
                continue
            requests.extend(normalize_request(src, flow))
        if fid == "builder.population.exclude_unmapped_member":
            sequence = {
                "selectionVariantEndpoint": {
                    "order": 1,
                    "stepId": "selectionVariantEndpoint",
                    "actionId": "create-excluded-selection-variant",
                    "kind": "command",
                    "payloadShape": "{snapshotToken:string,idempotencyKey:string,source:{kind:selectionRevision,selectionRevisionId:string},exclusions:[ResourceRef],requestId:string}",
                    "constructor": "BuilderWorkspace.excludePopulationMember -> LoomClient.createSelection",
                    "condition": "Coverage report identifies this unmapped ResourceRef; create a new immutable selection revision from the currently attached selection.",
                    "evidence": [
                        "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1948-1955",
                        "ui/packages/loom-ui/src/api.ts:1806-1813",
                    ],
                },
                "routeLookupEndpoint": {
                    "order": 2,
                    "stepId": "routeLookupEndpoint",
                    "actionId": "search-population-routes-for-variant",
                    "kind": "read",
                    "payloadShape": "{snapshotToken:string,outputId:string,selectionRevisionId:string,limit:50,cursor?:string,requestId:string}",
                    "constructor": "BuilderWorkspace.excludePopulationMember -> LoomClient.searchPopulationRoutes, retaining the exact previously attached route",
                    "condition": "Repeat pages until the returned route equals the previously attached route or nextCursor is absent; if no exact route is found, show an error and do not attach.",
                    "repeat": {
                        "cardinality": {"min": 1, "max": None},
                        "requestCursorField": "cursor",
                        "responseCursorField": "nextCursor",
                        "stopWhenAny": ["exact prior attached route found", "nextCursor absent"],
                        "applyAllowedWhen": "exact prior attached route found",
                    },
                    "evidence": [
                        "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1961-1978",
                        "ui/packages/loom-ui/src/api.ts:1599-1608",
                    ],
                },
                "applyEndpoint": {
                    "order": 3,
                    "stepId": "applyEndpoint",
                    "actionId": "attach-excluded-selection-variant",
                    "kind": "command",
                    "payloadShape": "{type:SET_TABLE_POPULATION,outputId:string,selectionRevisionId:string,routeChoiceId:string}",
                    "constructor": "BuilderWorkspace.excludePopulationMember -> applyCommands([SET_TABLE_POPULATION])",
                    "condition": "Only after routeLookupEndpoint returns the exact previously attached route for the new selection revision.",
                    "evidence": [
                        "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1980-1983",
                        "ui/packages/loom-ui/src/api.ts:1457-1465",
                    ],
                },
            }
            actual = {request.get("stepId") for request in requests}
            if actual != set(sequence):
                raise SystemExit(
                    "builder.population.exclude_unmapped_member request source shape drifted: expected %s, found %s"
                    % (sorted(sequence), sorted(actual))
                )
            for request in requests:
                request.update(sequence[request["stepId"]])
                request["flowId"] = "primary"
        if fid.startswith("builder.combine."):
            requests.append({
                "order": 1,
                "flowId": "combine-input-catalog",
                "stepId": "load-construction-inputs",
                "kind": "read",
                "method": "POST",
                "endpoint": "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-inputs",
                "actionId": "load-combine-input-catalog",
                "payloadShape": "{snapshotToken:string,expectedDraftVersion:number,expectedDraftDigest:string,cursor?:string,limit:100}",
                "constructor": "BuilderWorkspace.loadCombineCatalogPage -> LoomClient.getConstructionInputs",
                "condition": "Combine editor is open; source catalog identity matches selected draft",
                "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1041-1049", "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1060-1099", "ui/packages/loom-ui/src/api.ts:1679-1684"],
            })
        # Specialized editors share an ordered proposal/preview/apply transport.
        # Keep separate row-definition and construction paths when one UI action
        # can be reached from both surfaces, such as Group and summarize.
        flow_specs = []
        base = "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/"
        if any(kind.startswith("ROW_DEFINITION:") for kind in backend_kinds):
            flow_specs.append(("row-definition", [
                ("read", "load-row-definition-choices", "row-definition-choices?outputId={outputId}&snapshotToken={snapshotToken}", "GET"),
                ("proposal", "propose-row-definition", "row-definition-proposals", "POST"),
                ("command", "apply-row-definition-proposal", "commands", "POST"),
            ]))
        if any(kind.startswith("TABLE_SHAPE:") for kind in backend_kinds):
            flow_specs.append(("table-shape", [
                ("capability", "load-table-shape-capabilities", "table-shape-capabilities", "POST"),
                ("read", "resolve-table-shape", "table-shape-resolutions", "POST"),
                ("proposal", "propose-table-shape", "table-shape-proposals", "POST"),
                ("command", "apply-table-shape-proposal", "commands", "POST"),
            ]))
        construction_kinds = [kind for kind in backend_kinds if not kind.startswith(("ROW_DEFINITION:", "TABLE_SHAPE:"))]
        if construction_kinds:
            flow_specs.append(("construction-proposal", [
                ("proposal", "propose-construction-change", "construction-proposals", "POST"),
                ("preview", "load-construction-proposal-preview", "preview", "POST"),
                ("command", "apply-construction-proposal", "commands", "POST"),
            ]))
        if backend_kinds:
            candidate_constructor = None
            for src in sources:
                candidate = src.get("payload_constructor")
                if isinstance(candidate, dict):
                    candidate_constructor = candidate.get("candidate") or candidate.get("candidateConstructor") or candidate.get("candidate_constructor")
                elif isinstance(candidate, str):
                    candidate_constructor = candidate
                if candidate_constructor:
                    break
            constructor_ref = CONSTRUCTOR_BY_ID.get(fid)
            if not candidate_constructor and constructor_ref:
                candidate_constructor = constructor_ref[0]
                refs.extend(ref for ref in constructor_ref[1] if ref not in refs)
            if not candidate_constructor:
                candidate_constructor = f"typed {','.join(backend_kinds)} UI candidate builder; see source record and source evidence"
            for item in requests:
                endpoint = str(item.get("endpoint", ""))
                if "table-shape-" in endpoint:
                    item["flowId"] = "table-shape"
                elif "/explicit-groups" in endpoint:
                    item["flowId"] = "explicit-group-revision"
                elif "row-definition-" in endpoint:
                    item["flowId"] = "row-definition"
                elif "/construction-inputs" in endpoint:
                    item["flowId"] = "combine-input-catalog"
                else:
                    item["flowId"] = "construction-proposal"
            for flow, steps in flow_specs:
                for order, (kind, step, endpoint_suffix, method) in enumerate(steps, 1):
                    normalized_suffix = endpoint_suffix.split("?", 1)[0]
                    def request_path(item: dict) -> str:
                        value = str(item.get("endpoint", ""))
                        value = value.split("[?", 1)[0].split("?", 1)[0].rstrip(",.;")
                        return value
                    found = any(item.get("flowId") == flow and request_path(item).endswith(normalized_suffix.rstrip("/[]")) for item in requests)
                    if found:
                        continue
                    if flow == "construction-proposal":
                        payload_shapes = {
                            "proposal": "{snapshotToken:string,expectedDraftVersion:number,expectedDraftDigest:string,outputId:string,candidateConstruction:Construction,changedStepId:string,limit:PreviewLimit}",
                            "preview": "{snapshotToken:string,expectedDraftVersion:number,expectedDraftDigest:string,outputId:string,proposalId:string,limit:PreviewLimit}",
                            "command": "{commandId:string,semanticsVersion:constant,snapshotToken:string,expectedDraftVersion:number,expectedDraftDigest?:string,commands:[{type:APPLY_CONSTRUCTION_PROPOSAL,outputId,proposalId}]}",
                        }
                        constructor = f"{candidate_constructor}; constructionLifecycle.proposeCandidateAndWait / APPLY_CONSTRUCTION_PROPOSAL"
                        payload_shape = payload_shapes.get(kind)
                    elif flow == "row-definition":
                        payload_shapes = {
                            "read": "{outputId:string,snapshotToken:string} (query)",
                            "proposal": "{snapshotToken:string,expectedDraftVersion:number,expectedDraftDigest:string,outputId:string,selection:RowDefinitionSelection}",
                            "command": "{commandId,semanticsVersion,snapshotToken,expectedDraftVersion,expectedDraftDigest?,commands:[{type:APPLY_ROW_DEFINITION_PROPOSAL,...}]}",
                        }
                        constructor = f"{candidate_constructor}; row-definition choices/proposal and APPLY_ROW_DEFINITION_PROPOSAL lifecycle"
                        payload_shape = payload_shapes.get(kind)
                    else:
                        payload_shapes = {
                            "capability": "{snapshotToken:string,expectedDraftVersion:number,expectedDraftDigest:string,outputId:string}",
                            "read": "TableShapeResolutionRequest; selected supported choices + catalog identity",
                            "proposal": "TableShapeProposalRequest; reshape and derived resolution IDs",
                            "command": "{commandId,semanticsVersion,snapshotToken,expectedDraftVersion,expectedDraftDigest?,commands:[{type:APPLY_TABLE_SHAPE_PROPOSAL,...}]}",
                        }
                        constructor = f"{candidate_constructor}; TableShapeController.resolveTableShape/proposeTableShape/APPLY_TABLE_SHAPE_PROPOSAL"
                        payload_shape = payload_shapes.get(kind)
                    requests.append({
                        "order": order,
                        "flowId": flow,
                        "stepId": step,
                        "kind": kind,
                        "method": method,
                        "endpoint": base + endpoint_suffix,
                        "actionId": step,
                        "payloadShape": payload_shape,
                        "constructor": constructor,
                        "condition": None,
                        "evidence": refs,
                    })
        # Source fragments may list endpoint fields in object order rather than
        # interaction order. For specialized non-lifecycle flows, derive the
        # order from the UI transition kind.
        by_flow: dict[str, list[dict]] = {}
        for item in requests:
            by_flow.setdefault(str(item.get("flowId")), []).append(item)
        for flow, flow_requests in by_flow.items():
            if fid == "builder.population.exclude_unmapped_member" and flow == "primary":
                continue
            generic_flows = {"primary", "construction-proposal", "row-definition", "table-shape", "explicit-group-revision", "combine-input-catalog"}
            if flow not in generic_flows or any(x.get("kind") == "assessment" for x in flow_requests):
                continue
            kind_priority = {"capability": 0, "read": 1, "inspect": 1, "assessment": 1, "proposal": 2, "preview": 3, "command": 4, "reconcile": 5, "refresh": 6}
            flow_requests.sort(key=lambda item: (kind_priority.get(item.get("kind"), 2), item.get("stepId", "")))
            for index, item in enumerate(flow_requests, 1):
                item["order"] = index
        requests.sort(key=lambda item: (str(item.get("flowId")), item["order"], item["stepId"]))
        requests = [finalize_request_record(item, fid) for item in requests]
        request_flows = []
        flow_map: dict[str, list[dict]] = {}
        for item in requests:
            flow_map.setdefault(str(item.get("flowId", "primary")), []).append(item)
        flow_order: dict[str, int] = {flow: index for index, flow in enumerate(sorted(flow_map), 1)}
        dependencies: dict[str, list[str]] = {}
        dependencies_any: dict[str, list[list[str]]] = {}
        alternative_groups: dict[str, str] = {}
        if fid == "builder.first_table_from_root":
            flow_order.update({"first-table": 1, "automatic-preview": 2})
            dependencies["automatic-preview"] = ["first-table"]
        elif fid == "builder.explorer_create_delete":
            alternative_groups.update({"create": "explorer-lifecycle-choice", "delete": "explorer-lifecycle-choice"})
        elif fid == "builder.root.change_or_replace":
            alternative_groups.update({"connected-rebase": "root-change-strategy", "replace": "root-change-strategy"})
        elif fid.startswith("builder.combine."):
            flow_order.update({"combine-input-catalog": 1, "construction-proposal": 2})
            dependencies["construction-proposal"] = ["combine-input-catalog"]
        elif fid == "builder.row_definition.explicit_groups":
            flow_order.update({"explicit-group-revision": 1, "row-definition": 2})
            dependencies["row-definition"] = ["explicit-group-revision"]
        elif fid == "builder.construction_proposal_apply_cancel_retry":
            alternative_groups.update({"initial-or-retry": "proposal-attempt", "retry": "proposal-attempt"})
            flow_order.update({"initial-or-retry": 1, "retry": 1, "apply": 2})
            dependencies_any["apply"] = [["initial-or-retry", "retry"]]
        elif fid == "builder.automatic_preview_refresh_and_recovery":
            alternative_groups.update({"normal-or-recompile": "preview-recovery-path", "normal": "preview-recovery-path", "retryable-error": "preview-recovery-path", "stale-receipt": "preview-recovery-path", "stale-catalog": "preview-recovery-path"})
        elif fid == "builder.server_saved_draft_and_reconcile":
            alternative_groups.update({"save": "draft-lifecycle-choice", "reconcile": "draft-lifecycle-choice"})
            flow_order.update({"save": 1, "reconcile": 1, "refresh": 2})
            dependencies["refresh"] = ["reconcile"]
        elif fid == "builder.row_definition_proposal_apply_cancel":
            flow_order.update({"primary": 1, "row-shape-change": 2, "apply": 3})
            dependencies["row-shape-change"] = ["primary"]
            dependencies["apply"] = ["row-shape-change"]
        elif fid == "builder.table_route_and_occurrence_selection":
            for item in flow_map.get("primary", []):
                item["order"] = 1 if item["kind"] == "read" else 2
        for flow_id, items in flow_map.items():
            request_flows.append({
                "id": flow_id,
                "sequenceOrder": flow_order.get(flow_id, len(request_flows) + 1),
                "mode": "alternative" if flow_id in alternative_groups else "ordered",
                "alternativeGroup": alternative_groups.get(flow_id),
                "dependsOn": dependencies.get(flow_id, []),
                "dependsOnAnyOf": dependencies_any.get(flow_id, []),
                "steps": [{"checkpointId": item["checkpointId"], "flowId": item["flowId"], "order": item["order"], "stepId": item["stepId"], "kind": item["kind"], "method": item["method"], "endpoint": item["endpoint"], "actionId": item["actionId"], "payloadShape": item["payloadShape"], "sourcePayloadShape": item.get("sourcePayloadShape"), "requestContract": item["requestContract"], "constructor": item.get("constructor"), "sourceConstructors": item.get("sourceConstructors", []), "condition": item.get("condition"), "repeat": item.get("repeat"), "evidence": item.get("evidence", [])} for item in sorted(items, key=lambda x: (x["order"], x["stepId"]))],
            })
        request_flows.sort(key=lambda item: item["sequenceOrder"])
        dimensions = []
        for src in sources:
            raw_dims = src.get("dimensions", src.get("semantic_domains", []))
            for d in raw_dims if isinstance(raw_dims, list) else []:
                nd = normalize_dimension(d, fid)
                if not any(existing["id"] == nd["id"] and existing == nd for existing in dimensions):
                    dimensions.append(nd)
            domain = src.get("domain")
            if isinstance(domain, dict):
                if domain.get("finite_enum_values"):
                    for name, values in domain["finite_enum_values"].items():
                        dimensions.append({"id": name, "kind": "enum", "type": "string", "finiteValues": values, "dependsOn": domain.get("dependencies", []), "cardinality": cardinality(domain), "evidence": domain.get("source", [])})
                for dynamic in domain.get("dynamic_providers", []):
                    dimensions.append({"id": dynamic.get("name", "dynamic"), "kind": "dynamic-provider", "type": dynamic.get("type", "unknown"), "dynamicProvider": dynamic.get("provider"), "dependsOn": domain.get("dependencies", []), "cardinality": cardinality(domain), "evidence": domain.get("source", [])})
        dimensions_by_id = {}
        for d in dimensions:
            dimensions_by_id.setdefault(d["id"], d)
        dimensions = list(dimensions_by_id.values())
        if fid == "builder.reshape.group_field":
            for dimension in dimensions:
                if dimension["id"] == "summary_kind":
                    dimension["cardinality"] = {"min": 1, "max": 1}
            dimensions.extend([
                {
                    "id": "summary_count",
                    "kind": "repeated-array",
                    "type": "GroupAggregate",
                    "dependsOn": ["GROUP capability supported", "selected construction stage"],
                    "cardinality": {"min": 1, "max": None},
                    "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:990", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3336", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3390"],
                },
                {
                    "id": "summary_input_column_id",
                    "kind": "dynamic-reference",
                    "type": "stage-column-id",
                    "dynamicProvider": {"provider": "aggregateInputColumnsFor(stage, operation)", "rules": {"COUNT_ROWS": [], "COUNT_NON_NULL": "scalarColumnsFor(stage)", "COUNT_DISTINCT": "scalarColumnsFor(stage)", "SUM": "numericColumnsFor(stage)", "MEAN": "numericColumnsFor(stage)"}},
                    "dependsOn": ["summary_kind", "selected construction stage"],
                    "cardinality": {"min": 0, "max": None},
                    "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:925-935", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3352-3377"],
                },
                {
                    "id": "summary_output_column_id",
                    "kind": "generated-opaque-id",
                    "type": "OpaqueId",
                    "dynamicProvider": {"provider": "createOpaqueId('group-column')", "freshPerAggregate": True},
                    "dependsOn": ["Add summary"],
                    "cardinality": {"min": 1, "max": None},
                    "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:947-950"],
                },
                {
                    "id": "summary_output_name",
                    "kind": "string",
                    "type": "physical-column-name",
                    "dependsOn": ["summary operation", "selected input column", "existing output names"],
                    "cardinality": {"min": 1, "max": None},
                    "constraints": {"pattern": "^[A-Za-z_][A-Za-z0-9_]*$", "uniqueCaseInsensitiveWithinOutputs": True, "defaultGenerated": True, "maximumLength": None},
                    "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:938-940", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:947-956", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:918-922", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3380-3388"],
                },
                {
                    "id": "summary_output_label",
                    "kind": "string",
                    "type": "display-label",
                    "dependsOn": ["summary operation", "selected input column"],
                    "cardinality": {"min": 1, "max": None},
                    "constraints": {"trimmedNonEmpty": True, "maximumLength": None},
                    "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:947-956", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:918-922", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3499"],
                },
            ])
        constraints = []
        for src in sources:
            for item in src.get("constraints", []) + src.get("invariants", []):
                classification = item.get("classification", item.get("status", item.get("basis", "source-observation")))
                claim = item.get("claim", item.get("statement"))
                if claim:
                    item_refs = item.get("evidence", [])
                    if "$domain.source" in item_refs:
                        item_refs = refs
                    constraints.append({"classification": classification, "claim": claim, "evidence": item_refs})
            prose = src.get("prose", {})
            for gate in prose.get("gates", []):
                constraints.append({"classification": "observed-ui", "claim": gate, "evidence": refs})
            for invariant in prose.get("transition_preview_invariants", []):
                constraints.append({"classification": "observed-ui", "claim": invariant, "evidence": refs})
            for assumption in prose.get("known_assumptions", []):
                constraints.append({"classification": "inferred", "claim": assumption, "evidence": refs})
            for item in src.get("invariants", []) if isinstance(src.get("invariants"), list) else []:
                pass
        status = status_for(primary)
        if fid in STAGE_CAPABILITY_IDS and status == "reachable":
            status = "capability-conditional"
        if status in {"capability-conditional", "context-conditional"}:
            availability = {"status": status, "conditions": primary.get("gates", primary.get("capability_gates", primary.get("reachability_condition", primary.get("availability"))))}
        elif status in {"visible-but-disabled", "visible-disabled"}:
            availability = {"status": "visible-disabled", "conditions": primary.get("visibleButDisabledIntentions", primary.get("disabled_reason", primary.get("prose", {}).get("gates", [])))}
        else:
            availability = {"status": "reachable", "conditions": []}
        if family == "lifecycle" or family == "workspace.entry_route":
            family = "WORKSPACE_LIFECYCLE"
        seed = seed_for(fid, primary, is_lifecycle)
        disabled_states = []
        for src in sources:
            for state in src.get("visibleButDisabledIntentions", []):
                disabled_states.append({
                    "control": state.get("control"),
                    "when": state.get("when"),
                    "effect": state.get("effect"),
                    "evidence": state.get("evidence", []),
                })
        for mapping in mapping_rows:
            if mapping.get("canonicalId") == fid:
                for state in mapping.get("visibleButDisabledIntentions", []):
                    disabled_states.append({
                        "control": state.get("control"),
                        "when": state.get("when"),
                        "effect": state.get("effect"),
                        "evidence": state.get("evidence", []),
                    })
        needs_capability = seed["capabilityFixture"]["required"] or fid == "builder.delete_explorer_no_fallback"
        request_source_facts = []
        for index, src in enumerate(sources):
            constructor = src.get("request", src.get("payload_constructor"))
            if constructor is not None:
                request_source_facts.append({
                    "sourceId": src.get("id"),
                    "candidateOrRequestConstructor": constructor,
                    "evidence": source_refs(src),
                })
        # Capture exact request evidence even if it occurs only as source refs.
        out.append({
            "id": fid,
            "domain": family,
            "surface": primary.get("surface", primary.get("family", family)),
            "availability": availability,
            "operation": {"family": family, "variants": variants, "backendKinds": backend_kinds},
            "entryPaths": entry_paths,
            "requests": requests,
            "requestFlows": request_flows,
            "capabilityProvenance": {
                "status": "required-not-yet-observed" if needs_capability else "not-required-before-action",
                "kind": seed["capabilityFixture"]["kind"],
                "source": seed["capabilityFixture"]["source"],
                "endpoint": seed["capabilityFixture"]["endpoint"],
                "binding": "outputId+stageId+snapshotToken+draft identity" if seed["capabilityFixture"]["kind"] == "go-compiled-construction-capabilities" else None,
                "evidence": refs if needs_capability else [],
            },
            "seed": seed,
            "dimensions": dimensions,
            "constraints": constraints,
            "disabledStates": disabled_states,
            "requestSourceFacts": request_source_facts,
            "evidence": refs,
            "sourceRecords": [{"fragment": fragment if index == 0 else "merged-from-fragments", "sourceId": src.get("id")} for index, src in enumerate(sources)],
            "coverage": {"modelStatus": "pending", "adapterStatus": "pending", "adapterId": None, "runnerActionId": None, "runnerActions": {"status": "pending", "bindings": {}}, "modelStateId": None},
        })
    out.sort(key=lambda x: x["id"])
    # Annotate aliases on canonical rows for the consumers.
    alias_by_target: dict[str, list[str]] = {}
    for alias in aliases:
        alias_by_target.setdefault(alias["canonicalId"], []).append(alias["id"])
    for item in out:
        item["aliases"] = alias_by_target.get(item["id"], [])
    relational = json.loads((ROOT / "docs/ui-contract-inventory/relational.fragment.json").read_text())
    disabled_targets = {
        "group_rows_unavailable": ["builder.row_definition.explicit_groups", "builder.reshape.group_field", "builder.reshape.group_coded"],
        "group_numeric_summaries_without_numeric_inputs": ["builder.reshape.group_field"],
        "related_rows_unavailable": ["builder.row_definition.expanded_source_rows", "builder.reshape.related_expand"],
        "construction_pivot_unpivot_unsupported": ["builder.reshape.pivot_values", "builder.reshape.pivot_coded", "builder.reshape.unpivot"],
        "source_expansion_blocked_for_grouped_rows": ["builder.reshape.expand_scalar_list"],
        "legacy_table_shape_unsupported_choices": ["legacy.table_shape.grouped_pivot", "legacy.table_shape.unpivot"],
        "combine_action_without_root": ["builder.combine.key_join", "builder.combine.append", "builder.combine.membership"],
    }
    disabled_affordances = [{
        "id": item["id"],
        "canonicalIds": disabled_targets.get(item["id"], []),
        "control": item.get("ui_path"),
        "state": item.get("state"),
        "classification": item.get("basis"),
        "evidence": item.get("evidence", []),
    } for item in relational.get("visible_disabled_affordances", [])]
    return out, mapping_rows, disabled_affordances


def main() -> None:
    affordances, surface_mappings, disabled_affordances = canonicalize()
    source_files = set()
    for path in (ROOT / "ui/packages/loom-ui/src/features/ExplorerBuilder").rglob("*.ts*"):
        if "builderContractAdapters" in path.parts:
            continue
        if ".test." not in path.name and ".spec." not in path.name:
            source_files.add(path.relative_to(ROOT).as_posix())
    for name in ["ui/packages/loom-ui/src/types.ts", "ui/packages/loom-ui/src/api.ts", "ui/packages/loom-ui/src/Builder.tsx", "ui/apps/demo/src/main.tsx", "ui/apps/demo/src/demo-route.mjs"]:
        if (ROOT / name).exists():
            source_files.add(name)
    fingerprints = []
    for name in sorted(source_files):
        data = (ROOT / name).read_bytes()
        fingerprints.append({"path": name, "sha256": hashlib.sha256(data).hexdigest()})
    doc = {
        "schemaVersion": 1,
        "status": "source-backed-provisional; model-and-adapter-coverage-pending",
        "sourceCommit": BASE,
        "scope": {
            "included": "Mounted Builder workspace and preview affordances, including capability-conditional and visible-disabled controls, lifecycle, and actual app entry/routing behavior.",
            "excluded": ["publication/release/publish workflows per user scope"],
        },
        "aliases": ALIASES,
        "excludedSurfaces": [{"id": "legacy.construction_operations_panel.intentions", "basis": "unmounted", "reason": "ConstructionOperationsPanel has only its definition and barrel export as production references; BuilderWorkspace routes mounted families to specialized editors.", "evidence": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/index.ts:1", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationsPanel.tsx:268", "ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3637"], "proof": {"search": "rg -n 'ConstructionOperationsPanel' ui/packages/loom-ui/src --glob '!**/*.test.*'", "productionMatches": ["constructionOperations/index.ts export", "ConstructionOperationsPanel.tsx definition"], "mountedCallerFound": False}}],
        "sourceCoverage": {
            "method": "source-fingerprint-review-gate plus mounted operation-discriminant extraction; fingerprint drift requires explicit inventory review and is not a completeness proof; verification-only builderContractAdapters are fingerprinted by the capture artifact, not counted as product UI source",
            "watchedFiles": fingerprints,
            "mountedOperationSources": ["ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx", "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx", "ui/packages/loom-ui/src/types.ts"],
            "discriminantExtractors": [
                {"file": "ui/packages/loom-ui/src/types.ts", "schema": "constructionOperationSchema", "mappingField": "affordances[].operation.backendKinds", "excludes": ["COMBINE"], "combineSubkindSchema": "constructionCombineSchema"},
                {"file": "ui/packages/loom-ui/src/types.ts", "schema": "rowDefinitionSelectionSchema", "mappingField": "affordances[].operation.backendKinds", "prefix": "ROW_DEFINITION:"},
                {"file": "ui/packages/loom-ui/src/types.ts", "schema": "tableShapeProposalIntentSchema", "mappingField": "affordances[].operation.backendKinds", "prefix": "TABLE_SHAPE:"},
                {"file": "ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx", "schema": "allOperationFamilies", "mappingField": "affordances[].operation.family"}
            ],
            "surfaceMappings": surface_mappings,
            "bindings": [{"id": item["id"], "backendKinds": item["operation"]["backendKinds"], "evidence": item["evidence"]} for item in affordances if item["operation"]["backendKinds"]],
        },
        "disabledAffordances": disabled_affordances,
        "affordances": affordances,
    }
    OUT.write_text(json.dumps(doc, indent=2, ensure_ascii=False) + "\n")
    print(f"wrote {OUT.relative_to(ROOT)}: {len(affordances)} affordances, {len(fingerprints)} watched source files, {len(surface_mappings)} mounted surface mappings")


if __name__ == "__main__":
    main()
