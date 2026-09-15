import csv
import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("architecture_plan", ROOT / "scripts/validate_architecture_plan.py")
PLAN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PLAN)


class PlanValidationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="loom-plan-test-")
        self.addCleanup(self.temporary.cleanup)
        self.plan_dir = Path(self.temporary.name)
        for name in ("ISSUES.csv", "WORK_PACKAGES.csv", "BASELINE.json"):
            shutil.copyfile(ROOT / "docs/architecture" / name, self.plan_dir / name)

    def mutate(self, name, edit):
        path = self.plan_dir / name
        with path.open(newline="") as stream:
            reader = csv.DictReader(stream)
            fields, rows = reader.fieldnames, list(reader)
        edit(rows)
        with path.open("w", newline="") as stream:
            writer = csv.DictWriter(stream, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)

    def test_current_plan_passes(self):
        self.assertIn("PLAN_VALID:", PLAN.validate(self.plan_dir, ROOT))

    def test_duplicate_issue_is_rejected(self):
        self.mutate("ISSUES.csv", lambda rows: rows.append(dict(rows[0])))
        with self.assertRaisesRegex(ValueError, "duplicate issue ID"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_dependency_cycle_is_rejected(self):
        self.mutate("WORK_PACKAGES.csv", lambda rows: rows[0].update(depends_on=rows[0]["wp_id"]))
        with self.assertRaisesRegex(ValueError, "dependency cycle"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_parallel_file_collision_is_rejected(self):
        def collide(rows):
            for row in rows[:2]:
                row.update(wave="1", worker_paths="internal/explorer/service.go")
        self.mutate("WORK_PACKAGES.csv", collide)
        with self.assertRaisesRegex(ValueError, "parallel ownership conflict"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_unproven_candidate_cannot_be_marked_planned(self):
        self.mutate("ISSUES.csv", lambda rows: rows[0].update(review_verdict="needs-reproduction", status="planned"))
        with self.assertRaisesRegex(ValueError, "unproven candidate must remain blocked"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_invalid_source_line_is_rejected(self):
        self.mutate("ISSUES.csv", lambda rows: rows[0].update(source_refs="internal/server/server.go:999999"))
        with self.assertRaisesRegex(ValueError, "source line out of range"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_baseline_mismatch_is_rejected(self):
        self.mutate("ISSUES.csv", lambda rows: rows[0].update(base_sha="0" * 40))
        with self.assertRaisesRegex(ValueError, "baseline SHA mismatch"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_done_requires_recorded_execution_evidence(self):
        self.mutate("ISSUES.csv", lambda rows: rows[0].update(status="done"))
        with self.assertRaisesRegex(ValueError, "done requires implementation SHA"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_issue_target_must_belong_to_assigned_owner(self):
        self.mutate("ISSUES.csv", lambda rows: rows[0].update(target_files="internal/catalog/cache.go"))
        with self.assertRaisesRegex(ValueError, "target outside assigned ownership"):
            PLAN.validate(self.plan_dir, ROOT)

    def test_package_cannot_finish_with_open_issues(self):
        self.mutate("WORK_PACKAGES.csv", lambda rows: rows[0].update(status="done", implementation_sha="1" * 40, verification_evidence="example evidence"))
        with self.assertRaisesRegex(ValueError, "done work package has unfinished issues"):
            PLAN.validate(self.plan_dir, ROOT)


if __name__ == "__main__":
    unittest.main()
