#!/usr/bin/env python3
"""Read-only inventory of v1 Loom recipe bundles in JSON and JSONL corpora."""

import argparse
import hashlib
import json
from collections import Counter, defaultdict
from pathlib import Path

OUTPUT_FIELDS = (
    "name rootResourceType rootOccurrenceId rowGrain rootColumnNaming traversalColumnNaming fields filters pivots ownerRecords aggregates slices traversals tableReshape expand groupRows identity dynamicColumns extensionColumns catalogProjections columnTransformations derivedColumns construction population collisionPolicy"
).split()

TARGETS = {
    "name": "Output metadata", "rootResourceType": "Root.resourceType",
    "rootOccurrenceId": "Root.occurrenceId", "rowGrain": "Output.rowGrain",
    "rootColumnNaming": "PROJECT naming", "traversalColumnNaming": "PROJECT naming",
    "fields": "PROJECT fields", "filters": "PROJECT filters", "pivots": "PROJECT pivots",
    "ownerRecords": "PROJECT owner records", "aggregates": "PROJECT aggregates",
    "slices": "PROJECT slices", "traversals": "PROJECT routes",
    "tableReshape": "PIVOT or UNPIVOT with archive conversion", "expand": "PROJECT expansion",
    "groupRows": "GROUP_ROWS at AfterStepID", "identity": "PROJECT identity",
    "dynamicColumns": "PROJECT dynamic columns", "extensionColumns": "PROJECT extensions",
    "catalogProjections": "PROJECT catalog projections",
    "columnTransformations": "PROJECT transformations", "derivedColumns": "DERIVE steps",
    "construction": "one Step per construction operation", "population": "Root.population plus Route",
    "collisionPolicy": "unmapped by proposed Output shape",
}


def documents(path):
    if path.is_dir():
        for child in sorted(path.rglob("*")):
            if child.is_file() and child.suffix.lower() in {".json", ".jsonl"}:
                yield from documents(child)
    elif path.suffix.lower() == ".jsonl":
        for i, line in enumerate(path.read_text().splitlines(), 1):
            if line.strip():
                yield f"{path}:{i}", json.loads(line)
    else:
        yield str(path), json.loads(path.read_text())


def walk(value, path="$", parents=()):
    yield path, value, parents
    if isinstance(value, dict):
        for key, child in value.items():
            yield from walk(child, f"{path}.{key}", parents + (key,))
    elif isinstance(value, list):
        for i, child in enumerate(value):
            yield from walk(child, f"{path}[{i}]", parents)


def bundles(document):
    for path, value, _ in walk(document):
        if isinstance(value, dict) and isinstance(value.get("outputs"), list) and isinstance(value.get("recipeSchemaVersion"), int):
            yield path, value


def present(value):
    return value is not None and value is not False and value != "" and value != [] and value != {}


def route_sig(hops):
    rows = []
    for hop in hops:
        if not isinstance(hop, dict):
            rows.append("<non-object-hop>")
        elif "fromNodeId" in hop or "fromResourceType" in hop:
            keys = ("fromNodeId", "toNodeId", "fromResourceType", "toResourceType", "relationship", "storageDirection", "matchMode")
            rows.append(">".join(str(hop.get(k, "")) for k in keys))
        else:
            keys = ("resourceType", "relationship", "storageDirection")
            rows.append(">".join(str(hop.get(k, "")) for k in keys))
    return tuple(rows)


def route_arrays(value, path="$", parents=()):
    if isinstance(value, dict):
        for key, child in value.items():
            p = f"{path}.{key}"
            if key.lower() == "route" and isinstance(child, list):
                yield p, child, parents
            yield from route_arrays(child, p, parents + (key,))
    elif isinstance(value, list):
        for i, child in enumerate(value):
            yield from route_arrays(child, f"{path}[{i}]", parents)


def traversal_nodes(nodes, prefix=()):
    if not isinstance(nodes, list):
        return
    for node in nodes:
        if not isinstance(node, dict):
            continue
        name = str(node.get("name") or "<unnamed>")
        from_expr = json.dumps(node.get("from"), sort_keys=True, separators=(",", ":")) if node.get("from") else ""
        label = ">".join((name, str(node.get("toResourceType", "")), str(node.get("matchMode", "OPTIONAL")), from_expr))
        route = prefix + (label,)
        yield route
        yield from traversal_nodes(node.get("traversals"), route)


def mine(paths):
    present_counts, nonempty_counts, item_counts = Counter(), Counter(), Counter()
    value_counts = defaultdict(Counter)
    ops, routes, fragments, unusual = Counter(), Counter(), Counter(), Counter()
    output_rows, candidates, digests = [], [], defaultdict(list)
    bundle_field_present, bundle_field_nonempty = Counter(), Counter()
    occurrences = fragment_definitions = unknown_ops = malformed = 0
    for input_path in paths:
        for source, doc in documents(input_path):
            found = list(bundles(doc))
            candidates.append({"source": source, "bundle_count": len(found)})
            for bundle_path, bundle in found:
                occurrences += 1
                name = str(bundle.get("name") or "<unnamed>")
                payload = json.dumps(bundle, sort_keys=True, separators=(",", ":")).encode()
                digest = hashlib.sha256(payload).hexdigest()
                label = f"{source}{bundle_path}#{name}@{digest[:12]}"
                digests[digest].append(label)
                for bundle_field in ("recipeSchemaVersion", "name", "translationVersion", "fragments"):
                    if bundle_field in bundle:
                        bundle_field_present[bundle_field] += 1
                    if present(bundle.get(bundle_field)):
                        bundle_field_nonempty[bundle_field] += 1
                library = bundle.get("fragments")
                if isinstance(library, dict) and isinstance(library.get("fragments"), dict):
                    fragment_definitions += len(library["fragments"])
                outputs = bundle["outputs"]
                for oi, output in enumerate(outputs):
                    if not isinstance(output, dict):
                        continue
                    outname = str(output.get("name", f"output[{oi}]"))
                    features = set()
                    for key in OUTPUT_FIELDS:
                        if key in output:
                            present_counts[key] += 1
                            val = output[key]
                            if present(val):
                                nonempty_counts[key] += 1
                                if isinstance(val, (str, int, bool)):
                                    value_counts[key][str(val)] += 1
                            if isinstance(val, list):
                                item_counts[key] += len(val)
                            elif isinstance(val, dict):
                                item_counts[key] += 1
                    construction = output.get("construction")
                    if isinstance(construction, dict):
                        steps = construction.get("steps")
                        if steps is None:
                            steps = []
                        if isinstance(steps, list):
                            for step in steps:
                                op = step.get("operation") if isinstance(step, dict) else None
                                kind = op.get("kind") if isinstance(op, dict) else None
                                if kind:
                                    ops[str(kind)] += 1
                                    features.add("construction:" + str(kind))
                                elif isinstance(op, dict):
                                    unknown_ops += 1
                                if isinstance(op, dict):
                                    for route_path, hops, _ in route_arrays(op):
                                        routes[f"construction.{kind or 'UNKNOWN'}.{route_path}:{route_sig(hops)!r}"] += 1
                        else:
                            malformed += 1
                    for route in traversal_nodes(output.get("traversals")):
                        routes[f"project.traversal.depth={len(route)}:{'/'.join(route)}"] += 1
                        features.add("project:traversals")
                    population = output.get("population")
                    if isinstance(population, dict) and isinstance(population.get("route"), list):
                        routes["population.route:" + repr(route_sig(population["route"]))] += 1
                        features.add("population:route")
                    for path, value, _ in walk(output):
                        if isinstance(value, dict) and isinstance(value.get("call"), str) and value["call"].startswith("fragment:"):
                            frag = value["call"].split(":", 1)[1]
                            fragments[frag] += 1
                            features.add("fragment:reference")
                    for key in ("derivedColumns", "tableReshape", "groupRows", "columnTransformations", "collisionPolicy"):
                        if present(output.get(key)):
                            features.add("output:" + key)
                    for left, right in (("construction", "tableReshape"), ("construction", "derivedColumns"), ("construction", "groupRows"), ("construction", "population"), ("tableReshape", "derivedColumns"), ("traversals", "construction"), ("fragments", "construction")):
                        if present(output.get(left)) and present(output.get(right)):
                            unusual[left + "+" + right] += 1
                    live = [k for k in OUTPUT_FIELDS if present(output.get(k))]
                    if len(live) > 8:
                        unusual[f"output_fields>8:{len(live)}"] += 1
                    if construction and construction.get("steps"):
                        features.add("construction")
                    output_rows.append({"recipe": label, "output": outname, "features": sorted(features)})
    mapping_gaps = sorted({r["recipe"] for r in output_rows if "output:collisionPolicy" in r["features"]})
    return {
        "inputs": [str(p) for p in paths], "bundle_occurrences": occurrences,
        "unique_bundle_digests": len(digests),
        "duplicate_bundles_by_digest": {k: v for k, v in digests.items() if len(v) > 1},
        "candidate_files": candidates,
        "bundle_field_histogram": {
            field: {"present": bundle_field_present[field], "nonempty": bundle_field_nonempty[field]}
            for field in ("recipeSchemaVersion", "name", "translationVersion", "fragments")
        },
        "output_field_histogram": {k: {"present": present_counts[k], "nonempty": nonempty_counts[k], "items_or_objects": item_counts[k]} for k in OUTPUT_FIELDS},
        "output_scalar_values": {k: dict(sorted(v.items())) for k, v in value_counts.items()},
        "construction_kind_histogram": dict(sorted(ops.items())),
        "route_shape_histogram": dict(sorted(routes.items())),
        "fragment_reference_histogram": dict(sorted(fragments.items())),
        "fragment_definitions": fragment_definitions,
        "unusual_combinations": dict(sorted(unusual.items())),
        "unknown_operation_objects": unknown_ops, "malformed_constructions": malformed,
        "recipe_outputs": output_rows, "mapping_gaps": mapping_gaps,
        "mapping_gap_outputs": [r for r in output_rows if "output:collisionPolicy" in r["features"]],
        "mapping": [{"v1_output_field": f, "proposed_v2_target": TARGETS[f]} for f in OUTPUT_FIELDS],
        "bundle_mapping": [
            {"v1_bundle_field": "recipeSchemaVersion", "proposed_v2_target": "wire-version dispatch"},
            {"v1_bundle_field": "name", "proposed_v2_target": "bundle metadata"},
            {"v1_bundle_field": "translationVersion", "proposed_v2_target": "bundle metadata"},
            {"v1_bundle_field": "fragments", "proposed_v2_target": "expand into expressions before v2 translation; retain original bytes for v1 ArchiveDigest"},
        ],
    }


def render(report):
    lines = ["# Compiler recipe corpus inventory", "", "All histogram values are measured by the read-only corpus miner.", "", f"- Bundle occurrences: {report['bundle_occurrences']}", f"- Unique bundle digests: {report['unique_bundle_digests']}", f"- Fragment definitions: {report['fragment_definitions']}", "", "## Output fields", "", "| Field | Present | Nonempty | Array items or objects | Values when low-cardinality | v2 mapping |", "|---|---:|---:|---:|---|---|"]
    for row in report["mapping"]:
        s = report["output_field_histogram"][row["v1_output_field"]]
        values = report.get("output_scalar_values", {}).get(row["v1_output_field"], {})
        value_text = ", ".join(f"{k}={v}" for k, v in values.items()) if 0 < len(values) <= 8 else ""
        lines.append(f"| {row['v1_output_field']} | {s['present']} | {s['nonempty']} | {s['items_or_objects']} | {value_text} | {row['proposed_v2_target']} |")
    lines += ["", "## Bundle envelope mapping", "", "| v1 bundle field | Proposed v2 handling |", "|---|---|"]
    lines += [f"| {row['v1_bundle_field']} | {row['proposed_v2_target']} |" for row in report["bundle_mapping"]]
    for title, key in (("Construction kinds", "construction_kind_histogram"), ("Route shapes", "route_shape_histogram"), ("Fragment references", "fragment_reference_histogram"), ("Unusual combinations", "unusual_combinations")):
        lines += ["", f"## {title}", "", "| Shape | Count |", "|---|---:|"]
        lines += [f"| {shape} | {count} |" for shape, count in report[key].items()]
        if not report[key]:
            lines.append("| none | 0 |")
    lines += ["", "## Mapping gaps", ""]
    lines += [f"- {row['recipe']} output {row['output']}: collisionPolicy has no target field." for row in report["mapping_gap_outputs"]] or ["No corpus output uses the statically unmapped Output fields."]
    lines += ["", "The proposed Output model does not list collisionPolicy. Fragment libraries also need an explicit macro expansion or retention rule.", ""]
    return "\n".join(lines)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("inputs", nargs="+", type=Path)
    p.add_argument("--json-out", type=Path)
    p.add_argument("--markdown-out", type=Path)
    args = p.parse_args()
    report = mine(args.inputs)
    if args.json_out:
        args.json_out.parent.mkdir(parents=True, exist_ok=True)
        args.json_out.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    if args.markdown_out:
        args.markdown_out.parent.mkdir(parents=True, exist_ok=True)
        args.markdown_out.write_text(render(report))
    if not args.json_out and not args.markdown_out:
        print(render(report), end="")


if __name__ == "__main__":
    main()
