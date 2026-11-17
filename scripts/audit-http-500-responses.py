#!/usr/bin/env python3
"""Audit returned OpenAPI 500 bodies using the Go AST and mapper provenance."""
from __future__ import annotations
import argparse
import json
import pathlib
import re
import subprocess
import sys

MAPPERS = {"authoringErrorForOpenAPI": "failure", "explorerErrorResponse": "failure", "explorerLegacyResponse": "legacy", "mapServiceError": "body"}

def openapi_500_operations(path: pathlib.Path) -> list[str]:
    """Read operation IDs and their response keys from the repository YAML layout.

    Reject missing IDs and duplicate operations rather than substituting a count.
    The Go analyzer independently checks each named operation's actual returns.
    """
    operations = []
    operation = None
    responses_indent = None
    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if indent <= 4:
            operation = None
            responses_indent = None
        match = re.match(r"operationId:\s*([A-Za-z_][A-Za-z_0-9]*)\s*$", stripped)
        if match:
            operation = match.group(1)
        if stripped == "responses:":
            responses_indent = indent
        elif responses_indent is not None and indent <= responses_indent:
            responses_indent = None
        if responses_indent is not None and indent == responses_indent + 2 and re.match(r"['\"]?500['\"]?\s*:", stripped):
            if not operation:
                raise ValueError("500 response has no operationId")
            if operation in operations:
                raise ValueError(f"duplicate 500 response for {operation}")
            operations.append(operation)
    return operations

def audit_server(root: pathlib.Path, openapi: pathlib.Path) -> dict:
    operations = openapi_500_operations(openapi)
    helper = pathlib.Path(__file__).with_name("audit-http-500.go")
    result = subprocess.run(["go", "run", str(helper), "--root", str(root), "--operations", ",".join(operations)], check=True, text=True, capture_output=True)
    analyzed = json.loads(result.stdout)
    cases = analyzed["cases"] or []
    problems = analyzed["problems"] or []
    covered = {row["function"][:1].lower() + row["function"][1:] for row in cases}
    return {"schemaVersion": 1, "scope": "returned OpenAPI 500 responses and mapper provenance (Go AST)", "mapperContract": MAPPERS,
            "spec500OperationCount": len(operations), "count": len(cases), "covered500OperationCount": len(covered),
            "mappers": {name: sum(1 for row in cases if row["mapper"] == name) for name in MAPPERS},
            "problems": problems, "cases": cases}

def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default="internal/server")
    parser.add_argument("--openapi", default="openapi/openapi.yaml")
    parser.add_argument("--output")
    args = parser.parse_args()
    try:
        report = audit_server(pathlib.Path(args.root), pathlib.Path(args.openapi))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"OpenAPI 500 audit failed: {error}", file=sys.stderr)
        return 1
    payload = json.dumps(report, indent=2) + "\n"
    if args.output:
        pathlib.Path(args.output).write_text(payload, encoding="utf-8")
    else:
        sys.stdout.write(payload)
    if report["count"] == 0:
        print("no OpenAPI 500 responses found", file=sys.stderr)
        return 1
    for problem in report["problems"]:
        print(problem, file=sys.stderr)
    return 1 if report["problems"] else 0

if __name__ == "__main__":
    raise SystemExit(main())
