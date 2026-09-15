#!/usr/bin/env python3
"""Validate the architecture execution tables without modifying them."""

import argparse
import csv
import fnmatch
import json
from pathlib import Path
import re
import subprocess
import sys


ISSUE_FIELDS = {
    "issue_id", "wp_id", "title", "kind", "priority", "confidence",
    "evidence_state", "status", "source_refs", "problem", "change",
    "acceptance", "verification_command", "observed_verification",
    "compatibility", "migration", "review_verdict", "base_sha", "target_files",
}
WP_FIELDS = {
    "wp_id", "title", "objective", "depends_on", "wave", "owner", "status",
    "branch", "worker_paths", "integration_paths", "unit_gate", "live_gate",
    "performance_gate", "migration_gate", "rollback", "base_sha",
}


def entries(value):
    return [item.strip() for item in value.split(";") if item.strip()]


def read_rows(path, required):
    with path.open(newline="", encoding="utf-8") as stream:
        reader = csv.DictReader(stream)
        missing = required - set(reader.fieldnames or [])
        if missing:
            raise ValueError(f"{path.name}: missing columns {sorted(missing)}")
        rows = list(reader)
    if not rows:
        raise ValueError(f"{path.name}: no rows")
    for number, row in enumerate(rows, 2):
        if None in row or any(value is None for value in row.values()):
            raise ValueError(f"{path.name}:{number}: wrong column count")
    return rows


def validate(plan_dir, root):
    issues = read_rows(plan_dir / "ISSUES.csv", ISSUE_FIELDS)
    packages = read_rows(plan_dir / "WORK_PACKAGES.csv", WP_FIELDS)
    baseline = json.loads((plan_dir / "BASELINE.json").read_text())
    baseline_sha = baseline["source_baseline_sha"]
    errors = []
    if not re.fullmatch(r"[0-9a-f]{40}", baseline_sha):
        errors.append("BASELINE.json: source baseline must be a full commit SHA")
    else:
        subprocess.run(["git", "cat-file", "-e", f"{baseline_sha}^{{commit}}"], cwd=root, check=True)
    by_id = {row["wp_id"]: row for row in packages}
    if len(by_id) != len(packages):
        errors.append("duplicate work-package ID")
    if len({row["issue_id"] for row in issues}) != len(issues):
        errors.append("duplicate issue ID")

    for table, rows, required, id_field in [
        ("ISSUES", issues, ISSUE_FIELDS, "issue_id"),
        ("WORK_PACKAGES", packages, WP_FIELDS - {"depends_on", "integration_paths"}, "wp_id"),
    ]:
        for row in rows:
            identity = row[id_field]
            for key in required:
                if not row[key].strip():
                    errors.append(f"{table} {identity}: empty {key}")
            if row["base_sha"] != baseline_sha:
                errors.append(f"{table} {identity}: baseline SHA mismatch")
            if row["status"] not in {"planned", "blocked", "in_progress", "review", "done", "rejected"}:
                errors.append(f"{table} {identity}: unknown execution status")
            if row["status"] == "done":
                if not re.fullmatch(r"[0-9a-f]{40}", row.get("implementation_sha", "")) or not row.get("verification_evidence", "").strip():
                    errors.append(f"{table} {identity}: done requires implementation SHA and verification evidence")

    dependencies = {key: entries(row["depends_on"]) for key, row in by_id.items()}
    visiting, visited = set(), set()

    def visit(key):
        if key in visiting:
            errors.append(f"dependency cycle at {key}")
            return
        if key in visited:
            return
        visiting.add(key)
        for dependency in dependencies[key]:
            if dependency not in by_id:
                errors.append(f"{key}: unknown dependency {dependency}")
            else:
                visit(dependency)
        visiting.remove(key)
        visited.add(key)

    for key in by_id:
        visit(key)

    waves = {}
    for key, row in by_id.items():
        try:
            waves[key] = int(row["wave"])
            if waves[key] < 1:
                raise ValueError
        except ValueError:
            errors.append(f"{key}: wave must be a positive integer")
        for pattern in entries(row["worker_paths"]) + entries(row["integration_paths"]):
            if pattern.startswith("/") or ".." in Path(pattern).parts:
                errors.append(f"{key}: unsafe file scope {pattern}")
    for key, deps in dependencies.items():
        for dependency in deps:
            if key in waves and dependency in waves and waves[dependency] >= waves[key]:
                errors.append(f"{key}: dependency {dependency} is not in an earlier wave")

    line_counts = {}
    for row in issues:
        identity = row["issue_id"]
        if row["wp_id"] not in by_id:
            errors.append(f"{identity}: unknown work package {row['wp_id']}")
        if row["review_verdict"] not in {"accept", "narrow", "needs-reproduction"}:
            errors.append(f"{identity}: issue lacks an accepted or explicitly gated review")
        if row["review_verdict"] == "needs-reproduction" and row["status"] not in {"blocked", "rejected"}:
            errors.append(f"{identity}: unproven candidate must remain blocked")
        if row["priority"] not in {"P1", "P2", "P3"}:
            errors.append(f"{identity}: unknown priority")
        for reference in entries(row["source_refs"]):
            match = re.fullmatch(r"([^:]+):(\d+)(?:-(\d+))?", reference)
            if not match:
                errors.append(f"{identity}: malformed source reference {reference}")
                continue
            relative, line, end = match.groups()
            path = (root / relative).resolve()
            if not path.is_relative_to(root.resolve()):
                errors.append(f"{identity}: source is outside repository {relative}")
                continue
            if path not in line_counts:
                result = subprocess.run(["git", "show", f"{baseline_sha}:{relative}"], cwd=root, text=True, capture_output=True)
                if result.returncode:
                    errors.append(f"{identity}: source absent from baseline {relative}")
                    continue
                line_counts[path] = len(result.stdout.splitlines())
            if not 1 <= int(line) <= int(end or line) <= line_counts[path]:
                errors.append(f"{identity}: source line out of range {reference}")

    tracked = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    reserved = [pattern for row in packages for pattern in entries(row["integration_paths"])]
    claimed = {
        key: {path for path in tracked if path
              and any(fnmatch.fnmatchcase(path, pattern) for pattern in entries(row["worker_paths"]))
              and not any(fnmatch.fnmatchcase(path, pattern) for pattern in reserved)}
        for key, row in by_id.items()
    }
    for row in issues:
        if row["wp_id"] not in by_id:
            continue
        allowed = entries(by_id[row["wp_id"]]["worker_paths"]) + reserved
        for pattern in entries(row["target_files"]):
            if pattern.startswith("/") or ".." in Path(pattern).parts:
                errors.append(f"{row['issue_id']}: unsafe target scope {pattern}")
                continue
            for path in tracked:
                if path and fnmatch.fnmatchcase(path, pattern) and not any(fnmatch.fnmatchcase(path, scope) for scope in allowed):
                    errors.append(f"{row['issue_id']}: target outside assigned ownership {path}")
    for index, left in enumerate(packages):
        for right in packages[index + 1:]:
            a, b = left["wp_id"], right["wp_id"]
            if a in waves and b in waves and waves[a] == waves[b]:
                overlap = claimed[a] & claimed[b]
                if overlap:
                    errors.append(f"parallel ownership conflict {a}/{b}: {sorted(overlap)[:5]}")
    for key in by_id:
        if not any(row["wp_id"] == key for row in issues):
            errors.append(f"{key}: no assigned issues")
        if by_id[key]["status"] == "done" and any(row["wp_id"] == key and row["status"] not in {"done", "rejected"} for row in issues):
            errors.append(f"{key}: done work package has unfinished issues")

    if errors:
        raise ValueError("\n".join(errors))
    blocked = sum(row["status"] == "blocked" for row in issues)
    return f"PLAN_VALID: {len(issues)} issues, {len(packages)} work packages, {len(set(waves.values()))} waves, {blocked} blocked; references, dependencies, baseline and existing-file parallel ownership checked"


def main():
    root = Path(__file__).resolve().parent.parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan-dir", type=Path, default=root / "docs/architecture")
    args = parser.parse_args()
    try:
        print(validate(args.plan_dir, root))
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        print(f"PLAN_INVALID: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
