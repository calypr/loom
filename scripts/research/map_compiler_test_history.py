#!/usr/bin/env python3
"""Map compiler production moves to root tests changed in the same commit."""

import argparse
import csv
import re
import subprocess
from collections import defaultdict
from pathlib import Path

ROOT = "internal/dataframe/compiler"


def git(*args):
    return subprocess.check_output(["git", *args], text=True)


def changed_paths(commit):
    output = git("show", "--format=", "--find-renames=40%", "--name-status", commit, "--", ROOT)
    changes = []
    for line in output.splitlines():
        fields = line.split("\t")
        if len(fields) == 2 and fields[0][0] in "AMDT":
            changes.append((fields[0], "", fields[1]))
        elif len(fields) == 3 and fields[0].startswith("R"):
            changes.append((fields[0], fields[1], fields[2]))
    return changes


def root_production(path):
    relative = path.removeprefix(ROOT + "/")
    return "/" not in relative and relative.endswith(".go") and not relative.endswith("_test.go")


def root_test(path):
    relative = path.removeprefix(ROOT + "/")
    return "/" not in relative and relative.endswith("_test.go")


def package_for(path):
    relative = path.removeprefix(ROOT + "/")
    if "/" not in relative:
        return ""
    return str(Path(relative).parent)


def file_at(commit, path):
    result = subprocess.run(["git", "show", f"{commit}:{path}"], text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    return result.stdout if result.returncode == 0 else ""


def imported_compiler_packages(source):
    return set(re.findall(r'"github\.com/calypr/loom/internal/dataframe/compiler/([^"]+)', source))


def test_content_after(commit, old, new):
    origin = old if old and root_test(old) else new
    if not origin or not root_test(origin):
        return "", ""
    candidates = [new, old]
    for path in candidates:
        source = file_at(commit, path)
        if source:
            return origin, source
    return "", ""


def production_moves():
    output = git("log", "--all", "--find-renames=40%", "--name-status",
                 "--format=COMMIT:%H", "--", ROOT)
    candidates = []
    commit = ""
    for line in output.splitlines():
        if line.startswith("COMMIT:"):
            commit = line.removeprefix("COMMIT:")
            continue
        fields = line.split("\t")
        if len(fields) == 3 and fields[0].startswith("R"):
            status, old, new = fields
            if root_production(old) and package_for(new):
                candidates.append((commit, status, old, new))

    moves = []
    for destination in sorted({item[3] for item in candidates}):
        history = git("log", "--all", "--follow", "-M", "--name-status", "--format=COMMIT:%H", "--", destination)
        followed_commit = ""
        for line in history.splitlines():
            if line.startswith("COMMIT:"):
                followed_commit = line.removeprefix("COMMIT:")
                continue
            fields = line.split("\t")
            if len(fields) == 3 and fields[0].startswith("R"):
                status, old, new = fields
                if root_production(old) and new == destination:
                    moves.append((followed_commit, status, old, new))
    return sorted(set(moves))


def correlate(moves):
    commits = sorted({move[0] for move in moves})
    rows = []
    ambiguous_tests = []
    for commit in commits:
        commit_moves = [move for move in moves if move[0] == commit]
        changes = changed_paths(commit)
        root_tests = []
        for status, old, new in changes:
            path, source = test_content_after(commit, old, new)
            if path:
                root_tests.append((path, imported_compiler_packages(source)))
        for _, _, old, new in commit_moves:
            owner = package_for(new)
            matching = sorted(path for path, imports in root_tests if owner in imports)
            if matching:
                rows.extend((commit, old, new, path, owner, "paired") for path in matching)
            else:
                rows.append((commit, old, new, "", owner, "no same-commit root test imports owner"))
        for path, imports in root_tests:
            expected = {package_for(new) for _, _, _, new in commit_moves}
            relevant = imports & expected
            if len(relevant) > 1:
                ambiguous_tests.append((commit, path, ",".join(sorted(imports & expected))))
    return rows, ambiguous_tests


def current_test_inventory(mapped_rows):
    owners_by_history_name = defaultdict(set)
    for row in mapped_rows:
        if row[3]:
            owners_by_history_name[row[3]].add(row[4])

    current_tests = [path for path in git("ls-files", f"{ROOT}/*_test.go").splitlines()
                     if root_test(path)]
    inventory = []
    for current in current_tests:
        history = git("log", "--all", "--follow", "-M", "--name-status",
                      "--format=COMMIT:%H", "--", current)
        names = {current}
        for line in history.splitlines():
            fields = line.split("\t")
            if len(fields) == 3 and fields[0].startswith("R"):
                _, old, new = fields
                if root_test(old):
                    names.add(old)
                if root_test(new):
                    names.add(new)
        owners = set().union(*(owners_by_history_name[name] for name in names))
        if len(owners) == 1:
            classification = "history maps to one owner"
        elif owners:
            classification = "multiple historical owners"
        else:
            classification = "no known owner from move commits"
        inventory.append((current, ",".join(sorted(owners)), ";".join(sorted(names)), classification))
    return inventory


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("/tmp/compiler-test-history.tsv"))
    parser.add_argument("--current-output", type=Path, default=Path("/tmp/compiler-current-test-ownership.tsv"))
    args = parser.parse_args()
    moves = production_moves()
    rows, ambiguous = correlate(moves)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", newline="") as stream:
        writer = csv.writer(stream, delimiter="\t", lineterminator="\n")
        writer.writerow(("commit", "production_source", "production_destination", "root_test", "owner_package", "classification"))
        writer.writerows(rows)
    current = current_test_inventory(rows)
    args.current_output.parent.mkdir(parents=True, exist_ok=True)
    with args.current_output.open("w", newline="") as stream:
        writer = csv.writer(stream, delimiter="\t", lineterminator="\n")
        writer.writerow(("current_test", "historical_owner_candidates", "historical_names", "classification"))
        writer.writerows(current)
    print(f"production moves classified: {len(moves)}")
    print(f"same-commit test correlations: {sum(bool(row[3]) for row in rows)}")
    print(f"distinct test files mapped: {len({row[3] for row in rows if row[3]})}")
    print(f"ambiguous root test changes: {len(ambiguous)}")
    print(f"current root test files: {len(current)}")
    print(f"current files without one historical owner: {sum(row[1] == '' or ',' in row[1] for row in current)}")
    for commit, path, packages in ambiguous:
        print(f"ambiguous\t{commit}\t{path}\t{packages}")
    print(f"map: {args.output}")
    print(f"current inventory: {args.current_output}")


if __name__ == "__main__":
    main()
