#!/usr/bin/env python3
"""Validate the Phase 1 test filename target map against its inventory."""

import argparse
import csv
import json
import sys
from collections import defaultdict
from pathlib import Path


COLUMNS = [
    "current_path",
    "package_clause",
    "lines",
    "test_funcs",
    "primary_prod_files",
    "env",
    "action",
    "target_path",
    "per_func_targets",
    "evidence",
]
ACTIONS = {"rename", "merge", "split", "helper", "relocate", "defer"}
SIZE_CAP = 1500


def read_tsv(path):
    with path.open(newline="", encoding="utf-8") as stream:
        reader = csv.DictReader(stream, delimiter="\t")
        if reader.fieldnames != COLUMNS:
            raise ValueError(f"unexpected TSV columns: {reader.fieldnames!r}")
        return list(reader)


def test_stem(path):
    name = Path(path).name
    return name[:-len("_test.go")] if name.endswith("_test.go") else None


def parse_split_targets(value):
    result = {}
    for item in filter(None, value.split(";")):
        if "=" not in item:
            raise ValueError(f"invalid per_func_targets item {item!r}")
        name, path = item.split("=", 1)
        if name in result:
            raise ValueError(f"duplicate split mapping for {name}")
        result[name] = path
    return result


def validate(root, map_path, inventory_path):
    rows = read_tsv(map_path)
    inventory = json.loads(inventory_path.read_text(encoding="utf-8"))
    inv_by_path = {entry["path"]: entry for entry in inventory}
    errors = []

    if len(inv_by_path) != len(inventory):
        errors.append("inventory contains duplicate paths")

    row_by_path = {}
    for row in rows:
        current = row["current_path"]
        if current in row_by_path:
            errors.append(f"duplicate map row for {current}")
        row_by_path[current] = row
        if current not in inv_by_path:
            errors.append(f"map path missing from inventory: {current}")
        if row["action"] not in ACTIONS:
            errors.append(f"{current}: unknown action {row['action']!r}")

    expected_paths = {
        path for path in inv_by_path
        if path.startswith("internal/") and test_stem(path) is not None and "_" in test_stem(path)
    }
    missing = sorted(expected_paths - row_by_path.keys())
    extra = sorted(row_by_path.keys() - expected_paths)
    if missing:
        errors.append(f"missing inventory rows ({len(missing)}): {', '.join(missing[:8])}")
    if extra:
        errors.append(f"unexpected map rows ({len(extra)}): {', '.join(extra[:8])}")

    targets = defaultdict(list)
    function_assignments = defaultdict(list)
    planned_names = defaultdict(list)
    for current, row in row_by_path.items():
        action = row["action"]
        entry = inv_by_path.get(current)
        if entry is None:
            continue
        try:
            line_count = int(row["lines"])
        except ValueError:
            errors.append(f"{current}: lines must be an integer")
            continue
        if line_count != entry["lines"]:
            errors.append(f"{current}: map line count {line_count} != inventory {entry['lines']}")
        if row["package_clause"] != entry["package_clause"]:
            errors.append(f"{current}: package clause differs from inventory")
        if row["env"] != ";".join(entry["env"]):
            errors.append(f"{current}: environment evidence differs from inventory")
        listed_funcs = [name for name in row["test_funcs"].split(";") if name]
        if sorted(listed_funcs) != sorted(entry["test_funcs"]):
            errors.append(f"{current}: test_funcs differs from inventory")

        target = row["target_path"]
        source_key = (str(Path(current).parent), row["package_clause"])
        if action == "defer":
            if target:
                errors.append(f"{current}: defer target_path must be empty")
            if not row["evidence"].strip():
                errors.append(f"{current}: defer requires a reason in evidence")
            # Deferred functions remain accounted for as an explicit unchanged outcome.
            for name in entry["test_funcs"]:
                function_assignments[(*source_key, name)].append((current, "defer"))
                planned_names[(*source_key, name)].append(current)
            continue

        if not target:
            errors.append(f"{current}: {action} requires target_path")
            continue

        def check_destination(dest, dest_action):
            target_stem = test_stem(dest)
            if target_stem is None:
                errors.append(f"{current}: target must end in _test.go: {dest}")
            elif "_" in target_stem:
                errors.append(f"{current}: target stem contains underscore: {dest}")
            if not dest.startswith("internal/") or ".." in Path(dest).parts:
                errors.append(f"{current}: target is outside internal/: {dest}")
            changes_directory = Path(dest).parent != Path(current).parent
            if changes_directory != (dest_action == "relocate"):
                errors.append(f"{current}: only relocate changes directories: {dest}")
            destination = root / dest
            source = root / current
            if destination.exists() and destination != source and dest_action != "merge":
                errors.append(f"{current}: existing target collision requires action=merge: {dest}")
            existing_entry = inv_by_path.get(dest)
            if existing_entry and existing_entry["package_clause"] != row["package_clause"]:
                errors.append(f"{current}: destination package clause differs at {dest}")

        if action == "split":
            try:
                per_func = parse_split_targets(row["per_func_targets"])
            except ValueError as exc:
                errors.append(f"{current}: {exc}")
                per_func = {}
            helper_lines = entry.get("helper_functions", {})
            mapped_tests = set(per_func) & set(entry["test_funcs"])
            if mapped_tests != set(entry["test_funcs"]):
                errors.append(f"{current}: split mappings must include each test function exactly once")
            unknown_helpers = set(per_func) - set(entry["test_funcs"]) - set(helper_lines)
            if unknown_helpers:
                errors.append(f"{current}: split mappings name unknown declarations: {sorted(unknown_helpers)}")
            mapped_lines = 0
            for name, dest in per_func.items():
                if name in unknown_helpers:
                    continue
                check_destination(dest, action)
                if name in entry["test_funcs"]:
                    function_assignments[(*source_key, name)].append((current, dest))
                    size = entry["functions"][name]["lines"]
                    planned_names[(str(Path(dest).parent), row["package_clause"], name)].append(dest)
                else:
                    size = helper_lines[name]
                    planned_names[(str(Path(dest).parent), row["package_clause"], name)].append(dest)
                mapped_lines += size
                targets[dest].append((current, row["package_clause"], size))
            base_lines = max(0, entry["lines"] - mapped_lines)
            for name in set(helper_lines) - set(per_func):
                planned_names[(str(Path(target).parent), row["package_clause"], name)].append(target)
            if base_lines:
                targets[target].append((current, row["package_clause"], base_lines))
            check_destination(target, action)
        else:
            if row["per_func_targets"]:
                errors.append(f"{current}: per_func_targets is only valid for split")
            for name in entry["test_funcs"]:
                function_assignments[(*source_key, name)].append((current, target))
                planned_names[(str(Path(target).parent), row["package_clause"], name)].append(target)
            for name in entry.get("helper_functions", {}):
                planned_names[(str(Path(target).parent), row["package_clause"], name)].append(target)
            targets[target].append((current, row["package_clause"], entry["lines"]))
            check_destination(target, action)

    for current in expected_paths:
        entry = inv_by_path[current]
        for name in entry["test_funcs"]:
            assigned = function_assignments.get((str(Path(current).parent), entry["package_clause"], name), [])
            if len(assigned) != 1:
                errors.append(f"{current}: {name} appears in {len(assigned)} outcomes")

    for target, members in targets.items():
        clauses = {package for _, package, _ in members}
        if len(clauses) > 1:
            errors.append(f"{target}: target mixes package clauses: {sorted(clauses)}")
        total = sum(lines for _, _, lines in members)
        existing = root / target
        member_sources = {root / source for source, _, _ in members}
        if existing.exists() and existing not in member_sources:
            existing_entry = inv_by_path.get(target)
            existing_lines = existing_entry["lines"] if existing_entry else sum(
                1 for _ in existing.open(encoding="utf-8", errors="replace")
            )
            total += existing_lines
            if existing_entry:
                for name in existing_entry["test_funcs"]:
                    planned_names[(str(Path(target).parent), existing_entry["package_clause"], name)].append(target)
                for name in existing_entry.get("helper_functions", {}):
                    planned_names[(str(Path(target).parent), existing_entry["package_clause"], name)].append(target)
        if total > SIZE_CAP:
            errors.append(f"{target}: target line count {total} exceeds {SIZE_CAP} lines")

    for (directory, package_clause, name), destinations in planned_names.items():
        if len(destinations) > 1:
            errors.append(f"{directory} package {package_clause}: test function {name} is duplicated across {sorted(destinations)}")

    if errors:
        return errors
    print(f"valid: {len(rows)} rows, {len(expected_paths)} in-scope files, {sum(len(inv_by_path[p]['test_funcs']) for p in expected_paths)} test functions")
    return []


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("map", nargs="?", type=Path, default=Path("docs/TEST_FILENAME_MAP.tsv"))
    parser.add_argument("--root", type=Path, default=Path.cwd())
    parser.add_argument("--inventory", type=Path, default=Path(".cache/testnames/inventory.json"))
    args = parser.parse_args()
    try:
        errors = validate(args.root.resolve(), args.map, args.inventory)
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as exc:
        print(f"invalid: {exc}", file=sys.stderr)
        return 2
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        print(f"invalid: {len(errors)} error(s)", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
