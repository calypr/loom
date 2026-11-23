#!/usr/bin/env python3
"""Check Loom's internal Go filename convention."""

import csv
from pathlib import Path
import subprocess
import sys


# These are the filename suffixes recognized by Go's internal/syslist.
GOOS = set(
    ("aix android darwin dragonfly freebsd hurd illumos ios js linux "
     "nacl netbsd openbsd plan9 solaris wasip1 windows zos").split()
)
GOARCH = set(
    ("386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 "
     "mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 "
     "s390 s390x sparc sparc64 wasm").split()
)
BUILD_SUFFIXES = GOOS | GOARCH


def stem_without_build_suffix(name):
    stem = name.removesuffix(".go").removesuffix("_test").removesuffix(".gen")
    words = stem.split("_")
    if len(words) > 2 and any(words[:-2]) and words[-2] in GOOS and words[-1] in GOARCH:
        return "_".join(words[:-2])
    if len(words) > 1 and any(words[:-1]) and words[-1] in BUILD_SUFFIXES:
        return "_".join(words[:-1])
    return stem


def main():
    with Path("scripts/filenames/testallowlist.tsv").open(newline="") as manifest:
        reader = csv.DictReader(manifest, delimiter="\t")
        if reader.fieldnames != ["path", "reason"]:
            raise ValueError("filename allowlist requires path and reason columns")
        records = list(reader)
    allowlist = {row["path"]: row["reason"] for row in records}
    invalid = any(
        not path.endswith("_test.go") or not (reason and reason.strip())
        for path, reason in allowlist.items()
    )
    if len(allowlist) != len(records) or invalid:
        raise ValueError("filename allowlist requires unique paths and a reason for each path")
    files = subprocess.check_output(["rg", "--files", "--null", "internal"]).decode().split("\x00")
    violations = {
        path for path in files
        if path.endswith(".go")
        and "testdata" not in Path(path).parts
        and "_" in stem_without_build_suffix(Path(path).name)
    }
    stale = allowlist.keys() - violations
    if stale:
        print("Stale filename allowlist entries", file=sys.stderr)
        print(*sorted(stale), sep="\n", file=sys.stderr)
    unexpected = violations - allowlist.keys()
    if unexpected:
        print("Internal Go filenames contain underscores before recognized suffixes", file=sys.stderr)
        print(*sorted(unexpected), sep="\n", file=sys.stderr)
    return bool(stale or unexpected)


if __name__ == "__main__":
    raise SystemExit(main())
