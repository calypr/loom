#!/usr/bin/env python3
"""Run the regression checks selected by the package audit.

Focused mode tests a package and every direct in-repository importer recorded in
the generated audit CSV. Full mode runs the repository maintenance check and the
entire Go test suite, and is required after a package move, merge, or deletion.
"""

from __future__ import annotations

import argparse
import csv
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
AUDIT_CSV = ROOT / "docs" / "PACKAGE_AUDIT.csv"
EMPTY = "—"


def values(cell: str) -> list[str]:
    if not cell or cell == EMPTY:
        return []
    return [value.strip() for value in cell.split(";") if value.strip()]


def test_target(location: str) -> str:
    return "." if location == "." else f"./{location}"


def load_package(location: str) -> dict[str, str]:
    with AUDIT_CSV.open(encoding="utf-8", newline="") as handle:
        for row in csv.DictReader(handle):
            if row["location"] == location:
                return row
    raise ValueError(
        f"package {location!r} is absent from {AUDIT_CSV.relative_to(ROOT)}; "
        "regenerate the audit or use --full after an intentional deletion"
    )


def focused_locations(row: dict[str, str]) -> list[str]:
    locations = {
        row["location"],
        *values(row["production_importers"]),
        *values(row["test_only_importers"]),
    }
    return sorted(locations, key=lambda location: (location != ".", location))


def focused_command(row: dict[str, str]) -> list[str]:
    targets = [test_target(location) for location in focused_locations(row)]
    return ["go", "test", *targets, "-count=1"]


def run(command: list[str], dry_run: bool) -> None:
    print("$ " + " ".join(command), flush=True)
    if not dry_run:
        subprocess.run(command, cwd=ROOT, check=True)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run focused or repository-wide checks for a package audit decision."
    )
    parser.add_argument(
        "location",
        nargs="?",
        help="package location from docs/PACKAGE_AUDIT.csv; required unless --full is used",
    )
    parser.add_argument(
        "--full",
        action="store_true",
        help="run the audit consistency test and complete Go suite",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="print the selected commands without executing them",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    if args.full:
        run(
            [sys.executable, "-m", "unittest", "scripts.tests.test_package_audit"],
            args.dry_run,
        )
        run(["make", "test"], args.dry_run)
        return

    if not args.location:
        raise SystemExit("a package location is required unless --full is used")

    try:
        row = load_package(args.location)
    except ValueError as error:
        raise SystemExit(str(error)) from error

    impacted = focused_locations(row)
    print("Impacted package tests: " + ", ".join(impacted), flush=True)
    run(focused_command(row), args.dry_run)


if __name__ == "__main__":
    main()
