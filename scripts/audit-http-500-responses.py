#!/usr/bin/env python3
"""Audit every generated OpenAPI 500 response case.

Each hand-written adapter must return an error body produced by one of the
shared descriptive mappers. This script verifies that structural contract and
records the mapper used for every case.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import sys

MAPPERS = {
    "authoringErrorForOpenAPI": "failure",
    "explorerLegacyResponse": "legacy",
    "mapServiceError": "body",
    "serveGraphQL": "value",
}
FUNC_RE = re.compile(r"^func\s+\([^)]*\)\s+(?P<name>\w+)\s*\(")
CASE_RE = re.compile(r"^\s*case\s+http\.StatusInternalServerError\s*:")
BODY_RE = re.compile(r"\b(?P<var>failure|legacy|body|value)\b")


def enclosing_function(lines: list[str], case_index: int) -> tuple[str, int]:
    for index in range(case_index, -1, -1):
        match = FUNC_RE.match(lines[index])
        if match:
            return match.group("name"), index
    return "", -1


def mapper_for(lines: list[str], start: int, end: int) -> str:
    window = "\n".join(lines[start:end])
    found = [name for name in MAPPERS if re.search(rf"\b{name}\s*\(", window)]
    return found[0] if len(found) == 1 else ",".join(found)


def case_returns_case_body(lines: list[str], case_index: int) -> str:
    # Generated adapters keep the return on the case line or the next line.
    window = "\n".join(lines[case_index : case_index + 3])
    match = BODY_RE.search(window)
    return match.group("var") if match else ""


def openapi_500_operations(path: pathlib.Path) -> int:
    if not path.exists():
        return 0
    return sum(
        1
        for line in path.read_text(encoding="utf-8").splitlines()
        if re.match(r"^\s+['\"]?500['\"]?\s*:", line)
    )


def audit_server(root: pathlib.Path, openapi: pathlib.Path) -> dict:
    rows: list[dict] = []
    problems: list[str] = []
    spec_count = openapi_500_operations(openapi)
    for path in sorted(root.glob("openapi_*.go")):
        if path.name.endswith("_test.go"):
            continue
        lines = path.read_text(encoding="utf-8").splitlines()
        for index, line in enumerate(lines):
            if not CASE_RE.match(line):
                continue
            function, start = enclosing_function(lines, index)
            mapper = mapper_for(lines, start, index)
            body_var = case_returns_case_body(lines, index)
            expected = MAPPERS.get(mapper)
            if expected is None:
                problems.append(
                    f"{path.name}:{index + 1} {function}: 500 case has no recognized mapper"
                )
            elif body_var != expected:
                problems.append(
                    f"{path.name}:{index + 1} {function}: mapper {mapper} should return "
                    f"{expected}, got {body_var or 'no mapped body'}"
                )
            rows.append(
                {
                    "file": path.name,
                    "line": index + 1,
                    "function": function,
                    "mapper": mapper,
                    "bodyVariable": body_var,
                }
            )
    if spec_count and spec_count != len(rows):
        problems.append(
            f"OpenAPI spec declares {spec_count} operations with a 500 response, "
            f"but adapters contain {len(rows)} 500 cases"
        )
    return {
        "schemaVersion": 1,
        "scope": "internal/server/openapi_*.go case http.StatusInternalServerError",
        "mapperContract": MAPPERS,
        "spec500OperationCount": spec_count,
        "count": len(rows),
        "mappers": {
            name: sum(1 for row in rows if row["mapper"] == name)
            for name in MAPPERS
        },
        "problems": problems,
        "cases": rows,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default="internal/server")
    parser.add_argument("--openapi", default="openapi/openapi.yaml")
    parser.add_argument("--output")
    args = parser.parse_args()

    report = audit_server(pathlib.Path(args.root), pathlib.Path(args.openapi))
    payload = json.dumps(report, indent=2) + "\n"
    if args.output:
        pathlib.Path(args.output).write_text(payload, encoding="utf-8")
    else:
        sys.stdout.write(payload)

    if report["count"] == 0:
        print("no OpenAPI 500 cases found", file=sys.stderr)
        return 1
    for problem in report["problems"]:
        print(problem, file=sys.stderr)
    return 1 if report["problems"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
