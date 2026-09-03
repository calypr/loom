#!/usr/bin/env python3
"""Generate Loom's repository-wide Go package audit lookup tables."""

from __future__ import annotations

import csv
import re
from collections import defaultdict
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/calypr/loom"
CSV_PATH = ROOT / "docs" / "PACKAGE_AUDIT.csv"
DECISIONS_CSV_PATH = ROOT / "docs" / "PACKAGE_AUDIT_DECISIONS.csv"
MARKDOWN_PATH = ROOT / "docs" / "PACKAGE_AUDIT.md"

EXCLUDED_PARTS = {
    ".git",
    ".codex",
    ".agents",
    "graphify-out",
    "META_SMALL",
    "data.broken.20260706-full-load",
}

# These sentences are deliberately curated. The generator refuses to emit a row
# without one so a directory listing cannot masquerade as a semantic inventory.
RESPONSIBILITIES = {
    ".": "Start the default Loom server process through the internal server composition root.",
    "cmd/arango-fhir-proto": "Provide operator commands for FHIR loading, catalog diagnostics, generation repair, and activation.",
    "cmd/arango-fhir-server": "Start the explicit Loom HTTP server binary.",
    "cmd/dataframe-profile": "Run and compare developer-facing dataframe profiling workloads.",
    "cmd/dataframe-query": "Execute a checked-in GraphQL dataframe request and report response timing and diagnostics.",
    "cmd/explorer-config-v2-convert": "Convert legacy Explorer configuration input into the V2 authoring representation offline.",
    "cmd/generate": "Generate FHIR models, schema metadata, validation, extraction, and GraphQL artifacts from source schemas.",
    "cmd/gqlgenfix": "Post-process gqlgen output to enforce Loom-specific generated-code invariants.",
    "cmd/loom-acceptance": "Run the locked real-data acceptance scenario and emit machine-readable verification artifacts.",
    "conformance/compiler": "Load and exercise the canonical dataframe compiler oracle corpus for conformance and performance checks.",
    "generated/fhir": "Expose generated FHIR resource models and their extraction, validation, and GraphQL helpers.",
    "generated/fhirschema": "Expose generated raw FHIR schema metadata tables.",
    "generated/graphql/graph/executor": "Provide gqlgen-generated GraphQL executable schema and execution plumbing.",
    "generated/graphql/graph/model": "Provide gqlgen-generated GraphQL transport model types.",
    "generated/loomapi": "Provide OpenAPI-generated HTTP request, response, and strict-server contracts.",
    "internal/api/bulk/load": "Adapt multipart HTTP generation uploads and activation requests to ingest and dataset services.",
    "internal/api/graphql/graph": "Own GraphQL HTTP handler construction, error presentation, and generated executor binding.",
    "internal/api/graphql/graph/dataframe": "Adapt published ClickHouse dataframe discovery, row, aggregate, and export operations to GraphQL.",
    "internal/api/graphql/graph/query": "Resolve and authorize Arango-backed FHIR dataframe and graph query requests for GraphQL.",
    "internal/api/graphql/graph/resolver": "Implement gqlgen field resolvers and map GraphQL values to query and lifecycle services.",
    "internal/api/http": "Own shared Fiber server setup, middleware, request IDs, errors, health, and readiness behavior.",
    "internal/acceptance": "Fetch and cache the locked FHIR fixture, drive the real-data acceptance scenario, and verify its outputs and performance evidence.",
    "internal/authscope": "Represent request principals and resolve authorized project, generation, and resource scopes.",
    "internal/catalog": "Own persistence-neutral observed FHIR field, pivot, relationship, and capability facts.",
    "internal/catalog/arango": "Persist and retrieve catalog facts in ArangoDB.",
    "internal/dataframe/compiler": "Orchestrate semantic planning, physical lowering, optimization, rendering, and compiled-query assembly.",
    "internal/dataframe/compiler/capability": "Probe which dataframe language capabilities the compiler can prove and support.",
    "internal/dataframe/compiler/ir": "Define typed physical operations, values, scope proofs, policies, and validation.",
    "internal/dataframe/compiler/lower": "Lower logical FHIR dataframe plans into storage-aware physical IR.",
    "internal/dataframe/compiler/optimize": "Apply explainable semantics-preserving rewrites to validated physical IR.",
    "internal/dataframe/compiler/render/aql": "Render validated physical IR as deterministic parameterized AQL.",
    "internal/dataframe/errors": "Define and normalize stable structured dataframe errors for transport presentation.",
    "internal/dataframe/execution": "Prepare, authorize, execute, explain, and post-process compiled dataframe requests.",
    "internal/dataframe/expression": "Define and validate the typed backend-neutral expression AST used by recipes.",
    "internal/dataframe/publication": "Own backend-neutral dataframe publication contracts, bundles, and publication orchestration.",
    "internal/dataframe/publication/arango": "Persist dataframe publication registry and bundle metadata in ArangoDB.",
    "internal/dataframe/publication/clickhouse": "Materialize and inspect published dataframe bundles in ClickHouse.",
    "internal/dataframe/published": "Read, page, aggregate, and export authorized published ClickHouse dataframes.",
    "internal/dataframe/recipe": "Define, canonicalize, expand, encode, and validate persistence-neutral recipe documents.",
    "internal/dataframe/recipe/exec": "Define durable recipe-version storage contracts and immutable registration behavior.",
    "internal/dataframe/recipe/exec/arango": "Persist recipe versions and execution-use records in ArangoDB.",
    "internal/dataframe/recipe/schema": "Resolve catalog-backed recipe declarations into concrete validated schemas.",
    "internal/dataframe/semantic": "Convert validated dataframe requests and recipes into backend-neutral logical FHIR plans.",
    "internal/dataframe/spec": "Define the backend-independent dataframe request, filter, grain, relationship, and selector language.",
    "internal/dataset": "Define immutable dataset generation manifests, releases, active pointers, and lifecycle validation.",
    "internal/dataset/arango": "Persist immutable dataset generation lifecycle state in ArangoDB.",
    "internal/explorer": "Define Explorer aggregate identities, configs, revisions, receipts, output contracts, stores, and core service operations.",
    "internal/explorer/arango": "Persist Explorer drafts, revisions, receipts, publications, capabilities, and repository state in ArangoDB.",
    "internal/explorer/authoringv2": "Define, validate, canonicalize, and mutate the V2 Explorer Builder authoring workspace.",
    "internal/explorer/capability": "Build the immutable compiler-backed capability contract exposed to Explorer Builder.",
    "internal/explorer/compilation": "Translate validated Explorer authoring intent into native recipe and compilation artifacts.",
    "internal/explorer/lifecycle": "Coordinate transport-neutral Explorer authoring, preview, publication, query, and policy operations.",
    "internal/fhir/schema": "Add handwritten selector, traversal, pivot, and compiler semantics to generated FHIR metadata.",
    "internal/ingest": "Preflight and load FHIR NDJSON into generation-scoped Arango vertices, edges, and catalog facts.",
    "internal/projectid": "Define canonical and legacy-compatible public project identity normalization.",
    "internal/server": "Compose storage, domain services, generated OpenAPI handlers, GraphQL adapters, and HTTP routes.",
    "internal/store/arango": "Provide the low-level typed ArangoDB client, query streaming, profiling, and Explain assessment boundary.",
    "internal/store/clickhouse": "Provide the low-level typed ClickHouse connection and database-management boundary.",
}

# Completed decisions are deliberately curated rather than inferred from the
# current source tree. Removed-package records preserve the evidence needed to
# understand why a source disappeared; keep records pin the reviewed boundary
# to a live package.
COMPLETED_DECISIONS = (
    {
        "source": "internal/api/graphql",
        "disposition": "merged",
        "destination": "internal/api/graphql/graph",
        "rationale": "one production importer and error presentation is transport policy owned by graph",
        "contracts": "public error codes/messages/redaction/request IDs/cause/path/location",
        "verification": "focused graph+server tests; full repository verification; extension-map isolation regression",
        "status": "complete",
    },
    {
        "source": "internal/api/bulk/load",
        "disposition": "keep",
        "destination": "internal/api/bulk/load",
        "rationale": "owns multipart staging, authorization, ingest error translation, generation reuse, and release activation apart from generated OpenAPI response mapping",
        "contracts": "multipart fields; JSON result fields; auth scope; stable error codes; activation safety",
        "verification": "focused load+server tests; full repository verification",
        "status": "complete",
    },
    {
        "source": "internal/api/graphql/graph",
        "disposition": "keep",
        "destination": "internal/api/graphql/graph",
        "rationale": "owns gqlgen handler construction, GraphQL error presentation, and response-status adaptation apart from server composition and generated execution",
        "contracts": "GraphQL error shape; request IDs; partial-data HTTP 200; failure status lifting; playground and sandbox handlers",
        "verification": "focused graph+server tests; full repository verification; extension-map isolation regression",
        "status": "complete",
    },
)

DECISION_FIELDS = [
    "source",
    "disposition",
    "destination",
    "rationale",
    "contracts",
    "verification",
    "status",
]

PACKAGE_RE = re.compile(r"(?m)^\s*package\s+([A-Za-z_]\w*)")
IMPORT_BLOCK_RE = re.compile(r"(?ms)^\s*import\s*\((.*?)^\s*\)")
IMPORT_SINGLE_RE = re.compile(r'(?m)^\s*import\s+(?:[._A-Za-z]\w*\s+)?"([^"]+)"')
QUOTED_IMPORT_RE = re.compile(r'(?:^|\s)(?:[._A-Za-z]\w*\s+)?"([^"]+)"')
EXPORTED_RE = re.compile(
    r"(?m)^\s*(?:func\s+(?:\([^\n)]*\)\s*)?|type\s+)([A-Z][A-Za-z0-9_]*)"
)


def rel(path: Path) -> str:
    value = path.relative_to(ROOT).as_posix()
    return "." if value == "." else value


def included(path: Path) -> bool:
    return not any(part in EXCLUDED_PARTS or part.startswith("data.broken.") for part in path.parts)


def imports(text: str) -> set[str]:
    result = set(IMPORT_SINGLE_RE.findall(text))
    for block in IMPORT_BLOCK_RE.findall(text):
        result.update(QUOTED_IMPORT_RE.findall(block))
    return result


def is_generated(text: str) -> bool:
    return bool(re.search(r"(?m)^// Code generated\b", "\n".join(text.splitlines()[:20])))


def package_kind(location: str, generated_files: list[str], production_file_count: int) -> str:
    if location == "." or location.startswith("cmd/"):
        return "executable"
    if location.startswith("generated/") or (
        production_file_count > 0 and len(generated_files) == production_file_count
    ):
        return "generated"
    if location.startswith("conformance/"):
        return "conformance"
    if location.startswith("internal/api/"):
        return "transport adapter"
    if location.endswith("/arango") or location.endswith("/clickhouse") or location.startswith("internal/store/"):
        return "storage adapter"
    return "domain/library"


def display(values: list[str], limit: int | None = None) -> str:
    if not values:
        return "—"
    if limit is not None and len(values) > limit:
        return "; ".join(values[:limit]) + f"; … (+{len(values) - limit})"
    return "; ".join(values)


def markdown_cell(value: object) -> str:
    return str(value).replace("|", "\\|").replace("\n", " ")


def import_label(import_path: str) -> str:
    prefix = MODULE + "/"
    return import_path[len(prefix) :] if import_path.startswith(prefix) else import_path


def decision_evidence(row: dict[str, object]) -> str:
    kind = str(row["kind"])
    location = str(row["location"])
    importers = list(row["production_importers_raw"])
    prod_files = list(row["production_files_raw"])
    evidence: list[str] = []
    if kind == "generated":
        evidence.append("generated boundary; change its source/generator, not emitted code")
    elif kind == "executable":
        evidence.append("executable boundary; compare orchestration with sibling commands before combining")
    elif kind == "conformance":
        evidence.append("non-runtime conformance boundary")
    elif not importers:
        evidence.append("no in-repo production importer; verify runtime/tooling reachability")
    elif len(importers) == 1:
        evidence.append(f"one production importer ({importers[0]}); inspect ownership/inline potential")
    if len(prod_files) >= 12 and kind not in {"generated", "conformance"}:
        evidence.append(f"broad surface ({len(prod_files)} production files); inspect cohesion")
    if kind == "storage adapter":
        evidence.append("storage adapter; preserve interface and persisted-data contracts")
    if row["generated_files_raw"] and kind != "generated":
        evidence.append(
            f"mixed handwritten/generated package ({len(row['generated_files_raw'])} generated file(s)); preserve regeneration boundary"
        )
    if location in {"internal/dataframe/errors", "internal/projectid"}:
        evidence.append("small shared semantic contract; compare direct callers before inlining")
    if row["runtime_signals"] != "—":
        evidence.append("runtime/build signals present; static importer counts are insufficient for deletion")
    return "; ".join(evidence) if evidence else "no automatic consolidation signal; perform semantic review"


def validate_completed_decisions(known_locations: set[str]) -> None:
    """Ensure each completed decision agrees with the live package inventory."""
    seen_sources: set[str] = set()
    for decision in COMPLETED_DECISIONS:
        source = str(decision["source"])
        disposition = str(decision["disposition"])
        destination = str(decision["destination"])
        if source in seen_sources:
            raise SystemExit(f"duplicate completed decision source: {source}")
        seen_sources.add(source)
        if disposition == "keep":
            if source not in known_locations:
                raise SystemExit(f"kept decision source missing: {source}")
            if destination not in known_locations:
                raise SystemExit(f"kept decision destination missing: {destination}")
            if source != destination:
                raise SystemExit(
                    f"kept decision source/destination differ: {source} != {destination}"
                )
            continue
        if source in known_locations:
            raise SystemExit(f"completed decision source still exists: {source}")
        if destination not in known_locations:
            raise SystemExit(f"completed decision destination missing: {destination}")


def main() -> None:
    go_files = sorted(path for path in ROOT.rglob("*.go") if included(path.relative_to(ROOT)))
    grouped: dict[Path, list[Path]] = defaultdict(list)
    for path in go_files:
        grouped[path.parent].append(path)

    locations = sorted(rel(directory) for directory in grouped)
    missing = sorted(set(locations) - set(RESPONSIBILITIES))
    stale = sorted(set(RESPONSIBILITIES) - set(locations))
    if missing or stale:
        raise SystemExit(f"responsibility map mismatch: missing={missing}, stale={stale}")

    rows: list[dict[str, object]] = []
    imports_by_location: dict[str, set[str]] = {}
    test_imports_by_location: dict[str, set[str]] = {}

    for directory, paths in grouped.items():
        location = rel(directory)
        production = [path for path in paths if not path.name.endswith("_test.go")]
        tests = [path for path in paths if path.name.endswith("_test.go")]
        texts = {path: path.read_text(encoding="utf-8") for path in paths}
        package_source = production[0] if production else paths[0]
        package_match = PACKAGE_RE.search(texts[package_source])
        package_name = package_match.group(1) if package_match else "unknown"
        generated = [path.name for path in production if is_generated(texts[path])]

        prod_imports = set().union(*(imports(texts[path]) for path in production)) if production else set()
        test_imports = set().union(*(imports(texts[path]) for path in tests)) if tests else set()
        imports_by_location[location] = {
            import_label(value) for value in prod_imports if value == MODULE or value.startswith(MODULE + "/")
        }
        test_imports_by_location[location] = {
            import_label(value) for value in test_imports if value == MODULE or value.startswith(MODULE + "/")
        }

        all_prod_text = "\n".join(texts[path] for path in production)
        exported = sorted(set(EXPORTED_RE.findall(all_prod_text)))
        external = sorted(
            value
            for value in prod_imports
            if "." in value.split("/")[0] and not value.startswith(MODULE)
        )
        runtime_signals: list[str] = []
        if re.search(r"(?m)^\s*func\s+init\s*\(", all_prod_text):
            runtime_signals.append("init")
        if "//go:embed" in all_prod_text:
            runtime_signals.append("go:embed")
        if "//go:generate" in all_prod_text:
            runtime_signals.append("go:generate")
        if re.search(r"(?m)^//go:build", all_prod_text):
            runtime_signals.append("build tags")
        interface_count = len(re.findall(r"(?m)^\s*type\s+[A-Z]\w*\s+interface\s*{", all_prod_text))
        if interface_count:
            runtime_signals.append(f"{interface_count} exported interface(s)")

        compatibility_signals: list[str] = []
        for label, pattern in (
            ("legacy", r"(?i)\blegacy\b"),
            ("deprecated", r"(?i)\bdeprecated\b"),
            ("migration", r"(?i)\bmigrat(?:e|es|ed|ing|ion|ions)\b"),
            ("compatibility", r"(?i)\bcompatib(?:ility|le)\b"),
        ):
            count = len(re.findall(pattern, all_prod_text))
            if count:
                compatibility_signals.append(f"{label}={count}")

        if location == ".":
            other_files = ["go.mod"]
        else:
            other_files = sorted(
                path.name
                for path in directory.iterdir()
                if path.is_file()
                and path.suffix in {".graphql", ".json", ".md", ".tmpl", ".txt", ".yaml", ".yml"}
                and not path.name.startswith(".")
            )
        prod_loc = sum(len(texts[path].splitlines()) for path in production)
        test_loc = sum(len(texts[path].splitlines()) for path in tests)
        import_path = MODULE if location == "." else f"{MODULE}/{location}"
        row: dict[str, object] = {
            "location": location,
            "import_path": import_path,
            "package_name": package_name,
            "kind": package_kind(location, generated, len(production)),
            "responsibility": RESPONSIBILITIES[location],
            "production_files_raw": sorted(path.name for path in production),
            "test_files_raw": sorted(path.name for path in tests),
            "other_files_raw": other_files,
            "generated_files_raw": sorted(generated),
            "production_loc": prod_loc,
            "test_loc": test_loc,
            "exported_symbols_raw": exported,
            "external_dependencies_raw": external,
            "runtime_signals": display(runtime_signals),
            "compatibility_signals": display(compatibility_signals),
            "production_importers_raw": [],
            "test_importers_raw": [],
        }
        rows.append(row)

    rows.sort(key=lambda row: (row["location"] != ".", row["location"]))

    known_locations = set(locations)
    validate_completed_decisions(known_locations)
    decisions_by_source = {
        str(decision["source"]): decision for decision in COMPLETED_DECISIONS
    }

    for row in rows:
        location = str(row["location"])
        row["internal_dependencies_raw"] = sorted(imports_by_location[location] & known_locations)
        row["test_dependencies_raw"] = sorted(test_imports_by_location[location] & known_locations)
        row["production_importers_raw"] = sorted(
            source for source, dependencies in imports_by_location.items() if location in dependencies
        )
        row["test_importers_raw"] = sorted(
            source
            for source, dependencies in test_imports_by_location.items()
            if location in dependencies and source not in row["production_importers_raw"]
        )
        row["decision_evidence"] = decision_evidence(row)
        row["test_scope"] = "go test ." if location == "." else f"go test ./{location}"
        decision = decisions_by_source.get(location)
        row["audit_status"] = (
            f"completed/{decision['disposition']}"
            if decision is not None
            else "inventory complete; semantic disposition pending"
        )

    fieldnames = [
        "location",
        "import_path",
        "package_name",
        "kind",
        "responsibility",
        "production_files",
        "test_files",
        "other_files",
        "generated_files",
        "production_loc",
        "test_loc",
        "exported_symbol_count",
        "exported_symbols",
        "internal_dependencies",
        "test_dependencies",
        "production_importers",
        "test_only_importers",
        "external_dependencies",
        "runtime_signals",
        "compatibility_signals",
        "decision_evidence",
        "test_scope",
        "audit_status",
    ]
    csv_rows = []
    for row in rows:
        csv_rows.append(
            {
                "location": row["location"],
                "import_path": row["import_path"],
                "package_name": row["package_name"],
                "kind": row["kind"],
                "responsibility": row["responsibility"],
                "production_files": display(row["production_files_raw"]),
                "test_files": display(row["test_files_raw"]),
                "other_files": display(row["other_files_raw"]),
                "generated_files": display(row["generated_files_raw"]),
                "production_loc": row["production_loc"],
                "test_loc": row["test_loc"],
                "exported_symbol_count": len(row["exported_symbols_raw"]),
                "exported_symbols": display(row["exported_symbols_raw"]),
                "internal_dependencies": display(row["internal_dependencies_raw"]),
                "test_dependencies": display(row["test_dependencies_raw"]),
                "production_importers": display(row["production_importers_raw"]),
                "test_only_importers": display(row["test_importers_raw"]),
                "external_dependencies": display(row["external_dependencies_raw"]),
                "runtime_signals": row["runtime_signals"],
                "compatibility_signals": row["compatibility_signals"],
                "decision_evidence": row["decision_evidence"],
                "test_scope": row["test_scope"],
                "audit_status": row["audit_status"],
            }
        )

    CSV_PATH.parent.mkdir(parents=True, exist_ok=True)
    with CSV_PATH.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(csv_rows)

    with DECISIONS_CSV_PATH.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=DECISION_FIELDS)
        writer.writeheader()
        writer.writerows(COMPLETED_DECISIONS)

    kind_counts: dict[str, int] = defaultdict(int)
    for row in rows:
        kind_counts[str(row["kind"])] += 1
    no_importers = [
        str(row["location"])
        for row in rows
        if row["kind"] not in {"executable", "generated", "conformance"}
        and not row["production_importers_raw"]
    ]
    one_importer = [
        str(row["location"])
        for row in rows
        if row["kind"] not in {"executable", "generated", "conformance"}
        and len(row["production_importers_raw"]) == 1
    ]

    lines = [
        "# Package audit lookup table",
        "",
        "This is the repository-wide evidence index for package-boundary decisions. It is generated from the current Go source tree by `scripts/package_audit.py`; package responsibilities are curated in that script, while files, imports, importers, line counts, exported symbols, and signals are measured. The sortable source of record is [`PACKAGE_AUDIT.csv`](PACKAGE_AUDIT.csv).",
        "",
        "A row marked as a consolidation candidate is **not** deletion authorization. Before moving or deleting code, read the package and its tests, repeat call-site searches, check interface/runtime registration, confirm compatibility constraints, and run the listed focused test plus affected importer tests.",
        "",
        "## Snapshot",
        "",
        f"- Packages: {len(rows)}",
        f"- Production Go files: {sum(len(row['production_files_raw']) for row in rows)}",
        f"- Test Go files: {sum(len(row['test_files_raw']) for row in rows)}",
        f"- Production Go lines: {sum(int(row['production_loc']) for row in rows):,}",
        f"- Test Go lines: {sum(int(row['test_loc']) for row in rows):,}",
        "- Package kinds: " + ", ".join(f"{kind}={count}" for kind, count in sorted(kind_counts.items())),
        f"- Non-entrypoint handwritten packages with no production importer: {display(no_importers)}",
        f"- Non-entrypoint handwritten packages with one production importer: {display(one_importer)}",
        "",
        "## How to use the table",
        "",
        "- Start with **Responsibility**, **Depends on**, and **Imported by** to judge whether the boundary has a distinct job and correct dependency direction.",
        "- Use **Contents** and **API** to estimate the change surface; the API list is a top-level function/type estimate, not a compatibility guarantee.",
        "- Treat **Decision evidence** as a review queue. It reports structural facts and prompts, not a final keep/merge/delete verdict.",
        "- Treat generated, persisted, migration, compatibility, `init`, build-tag, embedded-data, and interface signals as deletion blockers until verified.",
        "- The full CSV also records test-only dependencies/importers, external dependencies, generated files, compatibility marker counts, and exact focused test commands.",
        "",
        "## Regression-safety workflow",
        "",
        "Every package decision uses a before/after verification bracket. Use `regression-guard:verify-change` as the controller when available. The table identifies the initial change surface; pstack's `blast-radius` skill checks the contracts that static imports miss.",
        "",
        "1. Invoke `regression-guard:verify-change`, then run `pstack:blast-radius` for the proposed package move, merge, or deletion. Name the behavior that must remain true and require an executable proof.",
        "2. Before editing, run `python3 scripts/package_audit_verify.py <location>`. This tests the package and all direct production and test-only importers recorded in the CSV.",
        "3. Make one package-boundary change. Do not batch independent package decisions.",
        "4. Run the same focused command after the change. For a move, merge, or deletion, also run `python3 scripts/package_audit_verify.py --full`.",
        "5. Regenerate this table with `python3 scripts/package_audit.py`, inspect the changed dependency/importer rows, and record the decision and proof before starting the next package.",
        "",
        "A green test run proves only the exercised contracts. Runtime registration, generated-code ownership, persisted data, wire formats, external consumers, and deployment behavior still require the specific proof identified by the blast-radius review.",
        "",
        "## Completed decisions",
        "",
        "These records preserve verified package-boundary decisions for both live packages and packages removed from the current inventory. The sortable source of record is [`PACKAGE_AUDIT_DECISIONS.csv`](PACKAGE_AUDIT_DECISIONS.csv).",
        "",
        "| Source | Disposition | Destination | Rationale | Contracts preserved | Verification | Status |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for decision in COMPLETED_DECISIONS:
        lines.append(
            "| "
            + " | ".join(
                markdown_cell(decision[field])
                for field in DECISION_FIELDS
            )
            + " |"
        )

    lines.extend(
        [
            "",
            "## Complete package table",
            "",
            "| Location | Kind | Responsibility | Contents | LOC (prod/test) | API | Depends on | Imported by (production) | Decision evidence |",
            "| --- | --- | --- | --- | ---: | --- | --- | --- | --- |",
        ]
    )
    for row in rows:
        contents = (
            f"P: {display(row['production_files_raw'])}; "
            f"T: {display(row['test_files_raw'])}; "
            f"Other: {display(row['other_files_raw'])}"
        )
        api = f"{len(row['exported_symbols_raw'])}: {display(row['exported_symbols_raw'], 12)}"
        cells = [
            f"`{row['location']}`",
            row["kind"],
            row["responsibility"],
            contents,
            f"{row['production_loc']:,}/{row['test_loc']:,}",
            api,
            display(row["internal_dependencies_raw"]),
            display(row["production_importers_raw"]),
            row["decision_evidence"],
        ]
        lines.append("| " + " | ".join(markdown_cell(cell) for cell in cells) + " |")

    lines.extend(
        [
            "",
            "## Regeneration and validation",
            "",
            "```bash",
            "python3 scripts/package_audit.py",
            "python3 -m unittest scripts.tests.test_package_audit",
            "```",
            "",
            "Regenerate after adding, removing, or moving a Go package. A generator failure means the curated responsibility map and the source tree disagree; resolve that mismatch rather than emitting an unclassified row.",
        ]
    )
    MARKDOWN_PATH.write_text("\n".join(lines) + "\n", encoding="utf-8")

    print(f"wrote {len(rows)} packages to {CSV_PATH.relative_to(ROOT)} and {MARKDOWN_PATH.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
