import csv
import subprocess
import sys
import unittest
from pathlib import Path

from scripts import package_audit


ROOT = Path(__file__).resolve().parents[2]


class PackageAuditTest(unittest.TestCase):
    def test_generator_covers_each_go_package_once(self) -> None:
        subprocess.run(
            [sys.executable, "scripts/package_audit.py"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        with (ROOT / "docs" / "PACKAGE_AUDIT.csv").open(encoding="utf-8", newline="") as handle:
            rows = list(csv.DictReader(handle))
        locations = [row["location"] for row in rows]
        expected_locations = sorted(
            {
                package_audit.rel(path.parent)
                for path in ROOT.rglob("*.go")
                if package_audit.included(path.relative_to(ROOT))
            }
        )
        self.assertEqual(len(locations), len(set(locations)))
        self.assertEqual(expected_locations, sorted(locations))
        self.assertTrue(all(row["responsibility"].endswith(".") for row in rows))
        self.assertTrue(all(row["audit_status"] for row in rows))
        resolver = next(row for row in rows if row["location"] == "internal/api/graphql/graph/resolver")
        self.assertEqual("transport adapter", resolver["kind"])
        self.assertNotEqual("—", resolver["generated_files"])
        load = next(row for row in rows if row["location"] == "internal/api/bulk/load")
        self.assertEqual("completed/keep", load["audit_status"])

    def test_completed_decisions_match_live_or_removed_packages(self) -> None:
        locations = {
            package_audit.rel(path.parent)
            for path in ROOT.rglob("*.go")
            if package_audit.included(path.relative_to(ROOT))
        }
        with (ROOT / "docs" / "PACKAGE_AUDIT_DECISIONS.csv").open(
            encoding="utf-8", newline=""
        ) as handle:
            decisions = list(csv.DictReader(handle))

        self.assertEqual(len(package_audit.COMPLETED_DECISIONS), len(decisions))
        self.assertEqual(package_audit.DECISION_FIELDS, list(decisions[0]))
        for decision in decisions:
            if decision["disposition"] == "keep":
                self.assertIn(decision["source"], locations)
                self.assertIn(decision["destination"], locations)
                self.assertEqual(decision["source"], decision["destination"])
            else:
                self.assertNotIn(decision["source"], locations)
                self.assertIn(decision["destination"], locations)
            self.assertEqual("complete", decision["status"])

    def test_regeneration_is_deterministic(self) -> None:
        paths = [
            ROOT / "docs" / "PACKAGE_AUDIT.csv",
            ROOT / "docs" / "PACKAGE_AUDIT_DECISIONS.csv",
            ROOT / "docs" / "PACKAGE_AUDIT.md",
        ]

        subprocess.run(
            [sys.executable, "scripts/package_audit.py"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        first = [path.read_bytes() for path in paths]
        subprocess.run(
            [sys.executable, "scripts/package_audit.py"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        second = [path.read_bytes() for path in paths]

        self.assertEqual(first, second)

    def test_verifier_selects_package_and_direct_importers(self) -> None:
        result = subprocess.run(
            [
                sys.executable,
                "scripts/package_audit_verify.py",
                "internal/api/graphql/graph",
                "--dry-run",
            ],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        self.assertIn("internal/server", result.stdout)
        self.assertIn(
            "go test ./internal/api/graphql/graph ./internal/server -count=1",
            result.stdout,
        )

    def test_verifier_full_mode_runs_both_repository_checks(self) -> None:
        result = subprocess.run(
            [sys.executable, "scripts/package_audit_verify.py", "--full", "--dry-run"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        self.assertIn("-m unittest scripts.tests.test_package_audit", result.stdout)
        self.assertIn("$ make test", result.stdout)


if __name__ == "__main__":
    unittest.main()
