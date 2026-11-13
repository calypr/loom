import contextlib
import hashlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

import recompute_reviewed_ledger as rc

LEDGER = "verification/publication-bundle/reviewed-sources.json"
WIDGET = "internal/widget/widget.go"
PROBE = "verification/publication-bundle/Probe.tla"
BLOB = "data/blob.bin"


def sh(cwd, *argv):
    p = subprocess.run(argv, cwd=cwd, capture_output=True)
    if p.returncode != 0:
        raise AssertionError((argv, p.stderr.decode("utf-8", "replace")))
    return p.stdout.decode().strip()


def md5b(data):
    return hashlib.md5(data).hexdigest()


def sha256b(data):
    return hashlib.sha256(data).hexdigest()


class Fixture(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.repo = os.path.join(self._tmp.name, "repo")
        os.makedirs(self.repo)
        sh(self.repo, "git", "init", "-q", ".")
        sh(self.repo, "git", "config", "user.email", "fixture@example.invalid")
        sh(self.repo, "git", "config", "user.name", "Fixture")
        self._orig = (rc.REPO, dict(rc.RELOCATIONS), dict(rc.RETIREMENTS))
        rc.REPO = self.repo
        rc.RELOCATIONS.clear()
        rc.RETIREMENTS.clear()
        self.addCleanup(self._restore)
        self.c1 = b"package widget\n// v1\n"
        self.c2 = b"package widget\n// v2\n"
        self.probe = b"---- MODULE Probe ----\n"
        self.b1 = b"alpha-blob\n"
        self.b2 = b"beta-blob\n"
        self.write(WIDGET, self.c1)
        self.write(PROBE, self.probe)
        self.write(BLOB, self.b1)
        self.commit1 = self.commit("c1")
        self.write(WIDGET, self.c2)
        self.write(BLOB, self.b2)
        self.commit2 = self.commit("c2")

    def _restore(self):
        rc.REPO, reloc, retir = self._orig
        rc.RELOCATIONS.clear()
        rc.RELOCATIONS.update(reloc)
        rc.RETIREMENTS.clear()
        rc.RETIREMENTS.update(retir)

    def write(self, rel, data):
        path = os.path.join(self.repo, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(data)

    def commit(self, msg):
        sh(self.repo, "git", "add", "-A")
        sh(self.repo, "git", "commit", "-qm", msg)
        return sh(self.repo, "git", "rev-parse", "HEAD")

    def put_ledger(self, doc):
        with open(os.path.join(self.repo, LEDGER), "w") as f:
            f.write(json.dumps(doc, indent=2, ensure_ascii=False) + "\n")

    def read_ledger(self):
        with open(os.path.join(self.repo, LEDGER)) as f:
            return f.read()

    def doc(self):
        return json.loads(self.read_ledger())

    def run_main(self, *argv):
        buf = io.StringIO()
        old = sys.argv
        sys.argv = ["recompute_reviewed_ledger.py", "--ledger", LEDGER, *argv]
        try:
            with contextlib.redirect_stdout(buf):
                code = rc.main()
        finally:
            sys.argv = old
        return code, buf.getvalue()

    def scan_rows(self, doc):
        return rc.Checker(LEDGER, "HEAD").scan(doc)

    def test_apply_repins_mismatch_with_receipt_and_canonical_form(self):
        self.put_ledger({"files": {WIDGET: {"md5": "0" * 32,
                                            "sha256": sha256b(self.c2)}}})
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        self.assertIn("FIX re-pinned", out)
        row = self.doc()["files"][WIDGET]
        self.assertEqual(row["md5"], md5b(self.c2))
        self.assertEqual(row["sha256"], sha256b(self.c2))
        blk = row["pubbundleRepin"]
        self.assertEqual(blk["ticket"], "FIXTURE")
        self.assertEqual(blk["fields"]["md5"],
                         {"pre": "0" * 32, "post": md5b(self.c2)})
        self.assertEqual(blk.get("movedAt"), self.commit2[:8])
        text = self.read_ledger()
        self.assertEqual(text,
                         json.dumps(json.loads(text), indent=2,
                                    ensure_ascii=False) + "\n")

    def test_shape_anomaly_and_prose_value_gate(self):
        doc = {"files": {WIDGET: {"md5": sha256b(self.c2)},
                         PROBE: {"sha256": "value recorded by hand, not hex"}}}
        self.put_ledger(doc)
        classes = [r["class"] for r in self.scan_rows(doc)]
        self.assertIn("SHAPE-ANOMALY", classes)
        self.assertIn("PROSE-VALUE", classes)
        code, out = self.run_main()
        self.assertEqual(code, 1)
        self.assertIn("GATED-FAIL", out)

    def test_repoint_merge_when_both_keys_exist(self):
        rc.RELOCATIONS["legacy/widget.go"] = WIDGET
        doc = {"files": {"legacy/widget.go": {"role": "old lane receipt"},
                         WIDGET: {"md5": md5b(self.c2)}}}
        self.put_ledger(doc)
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        files = self.doc()["files"]
        self.assertNotIn("legacy/widget.go", files)
        self.assertIn("repoint merge note", files[WIDGET]["role"])
        self.assertIn("legacy/widget.go", files[WIDGET]["role"])
        self.assertIn("merged repointed", out)

    def test_repoint_moves_untracked_row_with_receipt(self):
        rc.RELOCATIONS["legacy/widget.go"] = WIDGET
        doc = {"files": {"legacy/widget.go": {"md5": md5b(self.c1)}}}
        self.put_ledger(doc)
        code, _ = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        files = self.doc()["files"]
        self.assertNotIn("legacy/widget.go", files)
        self.assertEqual(files[WIDGET]["pubbundleRepin"]["repointFrom"],
                         "legacy/widget.go")

    def test_prose_string_row_upgraded_with_note(self):
        prose = "audit r9 observed md5 " + "a" * 32 + " on disk"
        self.put_ledger({"files": {WIDGET: prose}})
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        row = self.doc()["files"][WIDGET]
        self.assertEqual(row["md5"], md5b(self.c2))
        self.assertEqual(row["note"], prose)
        blk = row["pubbundleRepin"]
        self.assertEqual(blk["fields"]["md5"]["pre"], ["a" * 32])
        self.assertEqual(blk["fields"]["md5"]["post"], md5b(self.c2))
        self.assertIn("upgraded prose row", out)

    def test_model_hex_tla_field_gates_and_repins(self):
        doc = {"model": {"probe": {"file": PROBE, "tla": "f" * 32}}}
        self.put_ledger(doc)
        tla_rows = [r for r in self.scan_rows(doc) if r["field"] == "tla"]
        self.assertEqual([r["class"] for r in tla_rows], ["MISMATCH"])
        code, _ = self.run_main()
        self.assertEqual(code, 1)
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        sec = self.doc()["model"]["probe"]
        self.assertEqual(sec["tla"], md5b(self.probe))
        self.assertIn("tla", sec["pubbundleRepin"]["fields"])

    def test_artifact_head_gates_without_evidence(self):
        doc = {"confirmed": [{"id": "X", "sourceSnapshot": self.commit2,
                              "artifacts": [{"path": BLOB,
                                             "sha256": sha256b(self.b1)}]}]}
        self.put_ledger(doc)
        code, out = self.run_main()
        self.assertEqual(code, 1)
        self.assertIn("GATED-FAIL", out)
        self.assertIn("MISMATCH", out)

    def test_artifact_snapshot_and_evidence_stay_report_only(self):
        old_claim = sha256b(self.b1)
        doc = {"confirmed": [
            {"id": "S", "sourceSnapshot": self.commit1,
             "artifacts": [{"path": BLOB, "sha256": old_claim}]},
            {"id": "V", "sourceSnapshot": self.commit2,
             "artifacts": [{"path": BLOB, "sha256": old_claim,
                            "sha256Verification":
                                {"method": "file-history-blob",
                                 "commit": self.commit1,
                                 "queriedPath": BLOB}}]},
            {"id": "H", "artifacts":
                 [{"path": BLOB, "sha256": sha256b(self.b2)}]}]}
        self.put_ledger(doc)
        code, _ = self.run_main()
        self.assertEqual(code, 0)
        heads = [r for r in self.scan_rows(doc) if r["field"] == "sha256@head"]
        self.assertEqual({r["class"] for r in heads},
                         {"SNAPSHOT-DRIFT", "MATCH"})

    def test_apply_noop_on_converged_artifact_verifications(self):
        doc = {"confirmed": [
            {"id": "S", "sourceSnapshot": self.commit1,
             "artifacts": [
                 {"path": BLOB, "sha256": sha256b(self.b2),
                  "sha256Verification": {"method": "head-blob",
                                         "commit": self.commit2[:8],
                                         "queriedPath": BLOB}},
                 {"path": BLOB, "sha256": sha256b(self.b1),
                  "sha256Verification": {"method": "file-history-blob",
                                         "commit": self.commit1,
                                         "matchingCommits":
                                             [self.commit1[:8]],
                                         "queriedPath": BLOB}}]}]}
        self.put_ledger(doc)
        before = self.read_ledger()
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        self.assertEqual(self.read_ledger(), before)
        self.assertNotIn("verified via", out)
        self.assertIn("no rewrite", out)
        code, _ = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        self.assertEqual(self.read_ledger(), before)

    def test_stale_artifact_verification_is_rewritten(self):
        doc = {"confirmed": [
            {"id": "X", "sourceSnapshot": self.commit2,
             "artifacts": [{"path": BLOB, "sha256": sha256b(self.b2),
                            "sha256Verification":
                                {"method": "file-history-blob",
                                 "commit": self.commit1,
                                 "queriedPath": BLOB}}]}]}
        self.put_ledger(doc)
        before = self.read_ledger()
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        self.assertNotEqual(self.read_ledger(), before)
        self.assertIn("claim verified via", out)
        ver = (self.doc()["confirmed"][0]["artifacts"][0]
               ["sha256Verification"])
        self.assertEqual(ver["method"], "sourceSnapshot-blob")
        self.assertEqual(ver["commit"], self.commit2)

    def test_superseded_quote_reported_then_stamped_idempotent(self):
        quote = ("r9 SELF-RUN: live md5 == git show "
                 f"{self.commit1[:8]}:{WIDGET} == on-disk")
        live = (f"live md5 == git show {self.commit1[:8]}:{PROBE} == on-disk")
        doc = {"files": {
            WIDGET: {"md5": md5b(self.c2), "rederived": quote},
            PROBE: {"md5": md5b(self.probe), "rederived": live}}}
        self.put_ledger(doc)
        rows = self.scan_rows(doc)
        sup = [r for r in rows if r["class"] == "SUPERSEDED-PROSE"]
        self.assertEqual([r["key"] for r in sup], [WIDGET])
        code, _ = self.run_main()
        self.assertEqual(code, 0)
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        self.assertIn("stamped superseded quote", out)
        stamp = (self.doc()["files"][WIDGET]["pubbundleRepin"]
                 ["rederivedSuperseded"]["rederived"])
        self.assertEqual(stamp["quotes"][0]["commit"], self.commit1[:8])
        self.assertEqual(stamp["pinnedClaims"]["md5"], md5b(self.c2))
        rows = self.scan_rows(self.doc())
        self.assertEqual([r for r in rows
                          if r["class"] == "SUPERSEDED-PROSE"], [])
        code, out = self.run_main("--apply", "--ticket", "FIXTURE")
        self.assertEqual(code, 0)
        self.assertNotIn("stamped superseded", out)


if __name__ == "__main__":
    unittest.main()
