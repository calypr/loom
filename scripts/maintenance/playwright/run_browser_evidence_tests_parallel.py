#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
TEST_PATH = Path(__file__).with_name("tests") / "test_builder_browser_evidence.py"
TEST_MODULE = "test_builder_browser_evidence"


def load_test_module():
    spec = importlib.util.spec_from_file_location(TEST_MODULE, TEST_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {TEST_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[TEST_MODULE] = module
    spec.loader.exec_module(module)
    return module


def test_names(module) -> list[str]:
    suite = unittest.defaultTestLoader.loadTestsFromModule(module)
    names: list[str] = []

    def collect(item):
        if isinstance(item, unittest.TestSuite):
            for child in item:
                collect(child)
        else:
            names.append(item._testMethodName)

    collect(suite)
    return sorted(names)


def run_worker(names: list[str]) -> int:
    module = load_test_module()
    case_type = module.BuilderBrowserEvidenceNegativeControlsTest
    suite = unittest.TestSuite(case_type(name) for name in names)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    return 0 if result.wasSuccessful() else 1


def run_parallel(names: list[str], workers: int) -> int:
    module = load_test_module()
    temporary = tempfile.TemporaryDirectory(prefix="loom-browser-evidence-shared-")
    baseline = Path(temporary.name) / "baseline"
    try:
        module.copy_fixture_inputs(baseline)
        environment = os.environ.copy()
        environment["LOOM_BROWSER_EVIDENCE_SHARED_BASELINE"] = str(baseline)
        shards = [names[index::workers] for index in range(workers)]
        processes = []
        for shard in shards:
            if not shard:
                continue
            processes.append(subprocess.Popen(
                [sys.executable, __file__, "--worker", json.dumps(shard)],
                cwd=REPOSITORY_ROOT,
                env=environment,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            ))
        failed = []
        for process in processes:
            output, _ = process.communicate()
            print(output, end="")
            if process.returncode != 0:
                failed.append(process.returncode)
        return 0 if not failed else 1
    finally:
        temporary.cleanup()


def main() -> int:
    parser = argparse.ArgumentParser(description="Run retained browser-evidence controls in parallel.")
    parser.add_argument("--worker", help=argparse.SUPPRESS)
    parser.add_argument("--workers", type=int, default=None, help="Worker count (default: up to 4)")
    arguments = parser.parse_args()
    if arguments.worker:
        return run_worker(json.loads(arguments.worker))
    module = load_test_module()
    names = test_names(module)
    workers = arguments.workers or int(os.environ.get("LOOM_BROWSER_EVIDENCE_WORKERS", min(4, os.cpu_count() or 1)))
    workers = max(1, min(workers, len(names)))
    started = time.perf_counter()
    status = run_parallel(names, workers)
    print(f"{len(names)} tests in {time.perf_counter() - started:.1f}s with {workers} workers")
    return status


if __name__ == "__main__":
    raise SystemExit(main())
