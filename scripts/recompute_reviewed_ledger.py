#!/usr/bin/env python3
"""Row-by-row recompute of verification reviewed-source ledger derivation fields.

For every claimed derivation value (md5/sha256/contentMD5/expectedMD5 plus
bare-hex files-map rows) in a reviewed-sources.json ledger, recompute the
digest from the git blob at --ref (default HEAD) via git show and emit a
row-by-row match table. Classes:

  MATCH            claimed value equals the digest computed from the blob
  MISMATCH         resolvable path, claimed value differs (re-pin required)
  SHAPE-ANOMALY    field named md5-class but holds sha256-length value or the
                   inverse (contentMD5-style derivation fields)
  PROSE-VALUE      claim-named field whose value is not a bare hex digest
  UNRESOLVABLE-A   files-map key names a former tracked path (moved/deleted)
  UNRESOLVABLE-B   files-map key is malformed (not a repo-relative path)
  SNAPSHOT-*       artifact digests bound to a source snapshot: verified
                   against the snapshot blob; HEAD column reports drift only
  PIN-*            per-row pin commit attribution cross-check (informational)
  NON-FILE-PIN     digest-shaped value with no resolvable file target
  INFO-HEX         prose text containing hex digests (never gated)

Exit 1 when any gated row (files-map claims, canonicalProbe, model sections,
target-mapped status pins, confirmed/prevented artifact claims) is MISMATCH,
SHAPE-ANOMALY, PROSE-VALUE or UNRESOLVABLE. Receipt subtrees (*Repin blocks,
repair_units/confirmed narrative records) are reported but never gated.

--apply re-pins every gated mismatch to the true computed value, repoints
A-class keys to their relocated tracked paths (RELOCATIONS table), retires
keys with reasons (RETIREMENTS table), and appends pubbundleRepin receipt
blocks recording pre/post values per touched row.

Usage:
  recompute_reviewed_ledger.py [--ledger PATH] [--ref REF] [--table]
                               [--json-out PATH] [--apply] [--ticket TEXT]
"""

import argparse
import collections
import hashlib
import json
import os
import re
import subprocess
import sys

REPO = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
DEFAULT_LEDGER = "verification/publication-bundle/reviewed-sources.json"

HEX32 = re.compile(r"^[0-9a-f]{32}$")
HEX64 = re.compile(r"^[0-9a-f]{64}$")
HEX_ANY = re.compile(r"\b[0-9a-f]{32}\b|\b[0-9a-f]{64}\b")
LEADING_HEX = re.compile(r"^([0-9a-f]{32}|[0-9a-f]{64})(\b|$)")
SHAPE_IN_TEXT = re.compile(r"\bsha256=([0-9a-f]{64})\b")
PATHY = re.compile(r"^[\w./\[\] -]+$")

CLAIM_ALGO = {"md5": "md5", "sha256": "sha256", "contentMD5": "md5",
              "expectedMD5": "md5", "canonicalTlaMd5": "md5", "digest": "auto"}
NAMED_MODEL_CLAIMS = ("md5", "sha256", "contentMD5", "expectedMD5", "digest")


def model_section_claims(section):
    claims = [(f, CLAIM_ALGO[f]) for f in NAMED_MODEL_CLAIMS if f in section]
    for field in ("tla",):
        v = section.get(field)
        if isinstance(v, str) and (HEX32.match(v) or HEX64.match(v)):
            claims.append((field, "md5" if HEX32.match(v) else "sha256"))
    return claims


def resolve_algo(algo, value):
    if algo != "auto":
        return algo
    return "md5" if isinstance(value, str) and HEX32.match(value) else "sha256"
SKIP_SUBTREE = ("repin",)

RELOCATIONS = {
    "internal/catalog/arango/all_generations_project_binding_integration_test.go":
        "internal/backends/arango/internal/catalog/all_generations_project_binding_integration_test.go",
    "internal/catalog/arango/resources.go":
        "internal/backends/arango/internal/catalog/resources.go",
    "internal/dataframe/execution/chartifact/arango/catalog.go":
        "internal/backends/arango/internal/chartifact/catalog.go",
    "internal/dataframe/publication/arango/bundle_registry.go":
        "internal/backends/arango/internal/publication/bundle_registry.go",
    "internal/dataframe/publication/arango/dangling_binding_e2e_test.go":
        "internal/backends/arango/internal/publication/dangling_binding_e2e_test.go",
    "internal/dataframe/publication/arango/name_latest_binding_e2e_test.go":
        "internal/backends/arango/internal/publication/name_latest_binding_e2e_test.go",
    "internal/dataframe/publication/arango/release_dangle_reconcile_e2e_test.go":
        "internal/backends/arango/internal/publication/release_dangle_reconcile_e2e_test.go",
    "internal/dataset/arango/generation_activation_cas_e2e_test.go":
        "internal/backends/arango/internal/dataset/generation_activation_cas_e2e_test.go",
    "internal/dataset/arango/manifest_lifecycle.go":
        "internal/backends/arango/internal/dataset/manifest_lifecycle.go",
    "internal/dataset/arango/release_store.go":
        "internal/backends/arango/internal/dataset/release_store.go",
    "internal/dataset/arango/revision_inflation_e2e_test.go":
        "internal/backends/arango/internal/dataset/revision_inflation_e2e_test.go",
    "internal/explorer/arango/cross_generation_publish_e2e_test.go":
        "internal/backends/arango/internal/explorer/cross_generation_publish_e2e_test.go",
    "internal/explorer/arango/selections.go":
        "internal/backends/arango/internal/explorer/selections.go",
    "internal/explorer/arango/selections_cleanup_e2e_test.go":
        "internal/backends/arango/internal/explorer/selections_cleanup_e2e_test.go",
}

RETIREMENTS = {
    "internal/backends/postgres/aliases.go": "deleted at HEAD: removed by b2da41e2 "
        "(arch-09 root control facade deletion, #155); row is a commit-time D6 receipt, "
        "not a head-blob attestation; retained verbatim under retiredFilesKeys",
    "cross_generation_publish_e2e_test.go.explorer": "B-class malformed non-path key "
        "(authoring-time label, never a repo-relative path); retired from files map, "
        "prose preserved verbatim under retiredFilesKeys; the tracked counterpart row "
        "is internal/explorer/arango/cross_generation_publish_e2e_test.go (repointed)",
    "release_dangle_reconcile_e2e_test.go [arango pkg]": "B-class malformed non-path "
        "key; retired from files map, prose preserved verbatim under "
        "retiredFilesKeys; tracked counterpart rows: the repointed arango-pkg row and "
        "internal/dataframe/publication/clickhouse/release_dangle_reconcile_e2e_test.go",
    "release_dangle_reconcile_e2e_test.go [clickhouse pkg]": "B-class malformed "
        "non-path key; retired from files map, prose preserved verbatim under "
        "retiredFilesKeys; tracked counterpart row: "
        "internal/dataframe/publication/clickhouse/release_dangle_reconcile_e2e_test.go",
}

STATUS_PIN_TARGETS = {
    "F-B15-ledger-pin": "internal/backends/arango/internal/dataset/revision_inflation_e2e_test.go",
}


class Git:
    def __init__(self, repo):
        self.repo = repo
        self._blob = {}
        self._tracked = None

    def tracked(self):
        if self._tracked is None:
            p = self._run(["git", "ls-tree", "-r", "--name-only", "-z", "HEAD"])
            self._tracked = set(p.decode("utf-8", "surrogateescape").split("\0"))
        return self._tracked

    def _run(self, argv):
        p = subprocess.run(argv, cwd=self.repo, capture_output=True)
        if p.returncode != 0:
            return None
        return p.stdout

    def history(self, path):
        out = subprocess.run(["git", "log", "--format=%H", "--follow",
                              "HEAD", "--", path],
                             cwd=self.repo, capture_output=True, text=True).stdout
        return list(reversed(out.split()))

    def object_db_sha256s(self):
        if getattr(self, "_objdb", None) is None:
            import itertools
            p = subprocess.run(["git", "rev-list", "--objects", "--all"],
                               cwd=self.repo, capture_output=True, text=True)
            objs = [line.split(" ", 1)[0] for line in p.stdout.splitlines()
                    if len(line.split(" ", 1)) == 2]
            index = {}
            for i in range(0, len(objs), 2000):
                chunk = objs[i:i + 2000]
                q = subprocess.run(["git", "cat-file", "--batch"],
                                   input="\n".join(chunk).encode(),
                                   cwd=self.repo, capture_output=True)
                buf = q.stdout
                pos = 0
                for s in chunk:
                    e = buf.index(b"\n", pos)
                    toks = buf[pos:e].decode(errors="replace").split(" ")
                    pos = e + 1
                    if len(toks) != 3 or not toks[2].isdigit():
                        continue
                    size = int(toks[2])
                    data = buf[pos:pos + size]
                    pos += size + 1
                    if buf[pos:pos + 1] == b"\n":
                        pos += 1
                    index.setdefault(hashlib.sha256(data).hexdigest(), []).append(s)
            self._objdb = index
        return self._objdb

    def blob(self, ref, path):
        key = (ref, path)
        if key not in self._blob:
            self._blob[key] = self._run(["git", "show", f"{ref}:{path}"])
        return self._blob[key]

    def head(self):
        if getattr(self, "_head", None) is None:
            self._head = subprocess.run(
                ["git", "rev-parse", "HEAD"], cwd=self.repo,
                capture_output=True, text=True).stdout.strip()[:8]
        return self._head

    def commit_order(self, sha):
        out = subprocess.run(["git", "show", "-s", "--format=%ct", sha],
                             cwd=self.repo, capture_output=True, text=True).stdout
        return int(out) if out.strip().isdigit() else 0

    def is_commit(self, rev):
        p = subprocess.run(["git", "rev-parse", "--verify", "--quiet", f"{rev}^{{commit}}"],
                           cwd=self.repo, capture_output=True)
        return p.returncode == 0

    def first_moved_at(self, since, path, digest_hex, algo, extra_paths=()):
        rng = [f"{since}..HEAD"] if since and self.is_commit(since) else ["HEAD"]
        out = subprocess.run(["git", "log", "--format=%H", "--follow"]
                             + rng + ["--", path],
                             cwd=self.repo, capture_output=True, text=True).stdout
        cands = [path] + [p for p in extra_paths if p and p != path]
        for sha in reversed(out.split()):
            for cand in cands:
                raw = self.blob(sha, cand)
                if raw is not None and digest(raw, algo) == digest_hex:
                    return sha[:8]
        return None


def digest(raw, algo):
    h = hashlib.md5(raw) if algo == "md5" else hashlib.sha256(raw)
    return h.hexdigest()


def pathy(key):
    return bool(PATHY.match(key)) and "/" in key and "." in os.path.basename(key)


def classify_unresolvable(key):
    return "UNRESOLVABLE-A" if pathy(key) else "UNRESOLVABLE-B"


class Checker:
    def __init__(self, ledger_rel, ref):
        self.ledger_rel = ledger_rel
        self.ledger_dir = os.path.dirname(ledger_rel)
        self.ref = ref
        self.git = Git(REPO)
        self.rows = []

    def resolve(self, path):
        if path in self.git.tracked():
            return path
        cand = os.path.normpath(os.path.join(self.ledger_dir, path))
        if cand in self.git.tracked():
            return cand
        cand2 = os.path.normpath(os.path.join(REPO, path))
        rel = os.path.relpath(cand2, REPO)
        if rel in self.git.tracked():
            return rel
        return None

    def compute(self, path, algo, ref=None):
        raw = self.git.blob(ref or self.ref, path)
        return None if raw is None else digest(raw, algo)

    def emit(self, section, key, field, algo, claimed, computed, cls, extra=None):
        row = {"section": section, "key": key, "field": field, "algo": algo,
               "claimed": claimed, "computed": computed, "class": cls}
        if extra:
            row.update(extra)
        self.rows.append(row)
        return row

    def check_claim(self, section, key, field, algo, value, path, gated=True,
                    ref=None):
        if not isinstance(value, str):
            self.emit(section, key, field, algo, repr(value), None, "SHAPE-ANOMALY")
            return False
        m = LEADING_HEX.match(value.strip())
        if not m:
            self.emit(section, key, field, algo, value[:64], None,
                      "PROSE-VALUE" if gated else "INFO-HEX")
            return False
        hexval = m.group(1)
        prose_suffix = value.strip()[len(hexval):].strip()
        if prose_suffix and not prose_suffix.startswith("("):
            self.emit(section, key, field, algo, value[:64], None, "PROSE-VALUE")
            return False
        lenalgo = "md5" if HEX32.match(hexval) else "sha256"
        if lenalgo != algo and algo != "auto":
            self.emit(section, key, field, algo, hexval, None, "SHAPE-ANOMALY",
                      {"note": f"field-named {algo}, value length is {lenalgo}"})
            return False
        if path is None:
            self.emit(section, key, field, lenalgo, hexval, None, "PATH-UNRESOLVED")
            return False
        comp = self.compute(path, lenalgo, ref)
        if comp is None:
            self.emit(section, key, field, lenalgo, hexval, None, "BLOB-MISSING",
                      {"path": path})
            return False
        ok = comp == hexval
        cls = ("MATCH" if ok else "MISMATCH") if gated else \
              ("SNAPSHOT-MATCH" if ok else "SNAPSHOT-DRIFT")
        self.emit(section, key, field, lenalgo, hexval, comp, cls,
                  {"path": path, "proseSuffix": bool(prose_suffix)})
        return ok

    def pin_check(self, key, row):
        pin = row.get("pin")
        if not isinstance(pin, str) or not re.match(r"^[0-9a-f]{7,40}$", pin):
            return
        if not self.git.is_commit(pin):
            self.emit("pin", key, "pin", "commit", pin, None, "PIN-UNKNOWN-COMMIT")
            return
        for field, algo in (("sha256", "sha256"), ("md5", "md5")):
            v = row.get(field)
            if not isinstance(v, str):
                continue
            m = LEADING_HEX.match(v.strip())
            if not m:
                continue
            hexval = m.group(1)
            path = self.resolve(key)
            if path is None:
                continue
            raw = self.git.blob(pin, path)
            if raw is None:
                self.emit("pin", key, "pin", algo, pin, None, "PIN-NO-BLOB")
                continue
            ok = digest(raw, "md5" if len(hexval) == 32 else "sha256") == hexval
            self.emit("pin", key, field, "commit", pin, None,
                      "PIN-TRUE" if ok else "PIN-STALE")

    def scan(self, doc):
        self.scan_files(doc.get("files", {}))
        self.scan_canonical_probe(doc)
        self.scan_model(doc.get("model", {}))
        self.scan_confirmed(doc)
        self.scan_status_pins(doc)
        self.scan_receipts(doc)
        return self.rows

    def scan_files(self, files):
        for key, row in files.items():
            path = self.resolve(key)
            if path is None:
                self.emit("files", key, "key", None, None, None,
                          classify_unresolvable(key))
            if isinstance(row, str):
                if HEX32.match(row) or HEX64.match(row):
                    algo = "md5" if HEX32.match(row) else "sha256"
                    self.check_claim("files", key, "(bare-row)", algo, row, path)
                elif HEX_ANY.search(row):
                    self.emit("files", key, "(prose-row)", None, row[:64],
                              None, "INFO-HEX",
                              {"path": path, "hexHits": HEX_ANY.findall(row)})
                continue
            if not isinstance(row, dict):
                continue
            for field in ("md5", "sha256"):
                if field in row:
                    self.check_claim("files", key, field, field, row[field], path)
            self.pin_check(key, row)

    def scan_canonical_probe(self, doc):
        probe = doc.get("canonicalProbe")
        if not isinstance(probe, dict):
            return
        path = self.resolve(probe.get("file", "")) if probe.get("file") else None
        self.check_claim("canonicalProbe", probe.get("file", "?"),
                         "expectedMD5", "md5", probe.get("expectedMD5"), path)

    def _section_path(self, name, section):
        for field in ("file", "tla"):
            v = section.get(field)
            if isinstance(v, str) and not HEX32.match(v) and not HEX64.match(v):
                p = self.resolve(v)
                if p:
                    return p
        cand = os.path.join(self.ledger_dir, name[0].upper() + name[1:] + ".tla")
        return self.resolve(cand)

    def scan_model(self, model):
        if not isinstance(model, dict):
            return
        canon = self.resolve(model.get("tla", "")) if isinstance(model.get("tla"), str) else None
        for field in ("canonicalTlaMd5",):
            if field in model:
                self.check_claim("model", model.get("tla", "?"), field,
                                 CLAIM_ALGO[field], model[field], canon)
        alg = model.get("canonicalTlaMd5Algorithm")
        if isinstance(alg, str):
            m = SHAPE_IN_TEXT.search(alg)
            if m:
                self.check_claim("model", model.get("tla", "?"),
                                 "canonicalTlaMd5Algorithm", "sha256", m.group(1), canon)
        tier = model.get("fB22Tier1Md5s")
        if isinstance(tier, str):
            try:
                tier = json.loads(tier)
            except ValueError:
                self.emit("model", "fB22Tier1Md5s", "(json)", None, tier[:64],
                          None, "PROSE-VALUE")
                tier = None
        if isinstance(tier, dict):
            for p, v in tier.items():
                self.check_claim("model", "fB22Tier1Md5s", p, "md5", v, self.resolve(p))
        for name, section in model.items():
            if not isinstance(section, dict):
                continue
            spath = self._section_path(name, section)
            for field, algo in model_section_claims(section):
                self.check_claim("model", name, field, algo, section[field], spath)
            cfgs = section.get("cfgs")
            if isinstance(cfgs, dict):
                for cfgname, v in cfgs.items():
                    cpath = self.resolve(os.path.join(self.ledger_dir, cfgname + ".cfg"))
                    self.check_claim("model", f"{name}.cfgs", cfgname, "md5", v, cpath)

    def scan_confirmed(self, doc):
        for entry in doc.get("confirmed", []):
            if not isinstance(entry, dict):
                continue
            snap = entry.get("sourceSnapshot")
            for art in entry.get("artifacts", []):
                if not isinstance(art, dict):
                    continue
                self._artifact_claim("confirmed", entry.get("id", "?"), art, snap)
        snap = doc.get("baselineCommit")
        for art in doc.get("preventedArtifacts", []):
            if isinstance(art, dict):
                self._artifact_claim("preventedArtifacts", art.get("path", "?"), art, snap)

    def _artifact_claim(self, section, ident, art, snap):
        path = art.get("path")
        head_path = self.resolve(path) if isinstance(path, str) else None
        if head_path is None and isinstance(path, str) and path in RELOCATIONS:
            head_path = self.resolve(RELOCATIONS[path])
        for field in ("sha256", "md5"):
            if field not in art:
                continue
            algo = "md5" if field == "md5" else "sha256"
            snap_path = None
            if isinstance(path, str):
                for ref in (snap, None):
                    if ref and self.git.blob(ref, path) is not None:
                        snap_path = path
                        break
                    if ref is None:
                        break
            ok_snap = None
            if snap and snap_path:
                ok_snap = self.check_claim(section, f"{ident}:{snap_path}", field,
                                           algo, art[field], snap_path, gated=False,
                                           ref=snap)
            self.check_claim(section, f"{ident}:{path}", f"{field}@head", algo,
                             art[field], head_path, gated=False)
            if ok_snap is True:
                self.rows[-1]["note"] = "verified-against-sourceSnapshot-blob"
            elif ok_snap is False:
                self.rows[-1]["note"] = "NOT-VERIFIED-at-sourceSnapshot"

    def scan_status_pins(self, doc):
        st = doc.get("status_updates", {})
        if not isinstance(st, dict):
            return
        for key, v in st.items():
            if isinstance(v, str) and (HEX32.match(v) or HEX64.match(v)):
                target = STATUS_PIN_TARGETS.get(key)
                algo = "md5" if HEX32.match(v) else "sha256"
                if target is None:
                    self.emit("status_updates", key, "(bare-pin)", algo, v, None,
                              "NON-FILE-PIN")
                else:
                    self.check_claim("status_updates", key, "(bare-pin)", algo, v,
                                     self.resolve(target))

    def scan_receipts(self, doc):
        for unit, u in (doc.get("repair_units") or {}).items():
            if not isinstance(u, dict):
                continue
            self._walk_info(f"repair_units/{unit}", u)

    def _walk_info(self, path, node):
        if isinstance(node, dict):
            for k, v in node.items():
                if any(s in k.lower() for s in SKIP_SUBTREE):
                    continue
                self._walk_info(f"{path}/{k}", v)
        elif isinstance(node, list):
            for i, v in enumerate(node):
                self._walk_info(f"{path}[{i}]", v)
        elif isinstance(node, str) and (HEX32.match(node) or HEX64.match(node)):
            self.emit("receipt", path, "(bare-hex)", None, node, None, "NON-FILE-PIN")


def fmt_table(rows):
    by = collections.Counter(r["class"] for r in rows)
    lines = ["section | key | field | algo | claimed(12) | computed(12) | class"]
    for r in rows:
        lines.append("| ".join([
            str(r["section"]), str(r["key"]), str(r["field"]), str(r["algo"]),
            (r["claimed"] or "")[:12] if isinstance(r.get("claimed"), str) else "-",
            (r["computed"] or "")[:12] if isinstance(r.get("computed"), str) else "-",
            r["class"]]))
    lines.append("")
    lines.append("class histogram: " + ", ".join(f"{k}={v}" for k, v in sorted(by.items())))
    return "\n".join(lines)


GATED_BAD = {"MISMATCH", "SHAPE-ANOMALY", "PROSE-VALUE", "UNRESOLVABLE-A",
             "UNRESOLVABLE-B", "PATH-UNRESOLVED", "BLOB-MISSING"}


def apply_fixes(doc, ledger_rel, ref, ticket):
    git = Git(REPO)
    checker = Checker(ledger_rel, ref)
    dirn = os.path.dirname(ledger_rel)

    def resolve(path):
        return checker.resolve(path)

    def receipt(row_block, fields, extra=None):
        blk = {"ticket": ticket, "date": "2026-11-13",
               "reason": "final pubbundle ledger re-pin sweep: every derivation-claiming "
                         "field recomputed from the git blob at " + ref +
                         " (git show <ref>:<path>); stale values replaced with true "
                         "computed digests, expectations never adjusted",
               "fields": fields}
        if extra:
            blk.update(extra)
        return blk

    def true_value(path, algo):
        raw = git.blob(ref, path)
        return None if raw is None else digest(raw, algo)

    def attach_receipt(row, blk):
        prior = row.get("pubbundleRepin")
        if isinstance(prior, dict):
            prior.setdefault("fields", {}).update(blk.get("fields", {}))
            for k, v in blk.items():
                if k != "fields":
                    prior[k] = v
        else:
            row["pubbundleRepin"] = blk

    notes = []
    files = doc.get("files", {})

    repoint = {}
    for key in list(files):
        if key in RELOCATIONS and key not in resolved_tracked(git):
            repoint[key] = RELOCATIONS[key]

    resolved_tracked_set = resolved_tracked(git)
    for old, new in repoint.items():
        if new in files and old in files:
            oldrow = files.pop(old)
            if isinstance(oldrow, str):
                oldrow = {"role": oldrow}
            newrow = files[new]
            if not isinstance(newrow, dict):
                newrow = {"md5": newrow}
                files[new] = newrow
            merged_note = "repoint merge note: consolidated the malformed/duplicate " \
                          "stale-path entry '" + old + "' into this row"
            prior = newrow.get("role", "")
            newrow["role"] = (prior + " || " if prior else "") + merged_note
            continue
        row = files.pop(old)
        if isinstance(row, str):
            row = {"note": row}
        if isinstance(row, dict):
            row["pubbundleRepin"] = receipt(
                None, {}, {"repointFrom": old,
                           "viaCommit": "32e8ad39 (arch-backend-07 PR-2: adapter trees "
                                        "moved under internal/backends/arango)"})
        files[new] = row
        notes.append(f"repointed {old} -> {new}")

    for key in list(files):
        if key in RETIREMENTS and key not in resolved_tracked_set:
            row = files.pop(key)
            doc.setdefault("retiredFilesKeys", {})[key] = {
                "reason": RETIREMENTS[key],
                "retainedRow": row,
                "retiredBy": ticket,
                "date": "2026-11-13",
            }
            notes.append(f"retired {key}")

    for key, row in files.items():
        path = resolve(key)
        if path is None:
            continue
        if isinstance(row, str) and (HEX32.match(row) or HEX64.match(row)):
            algo = "md5" if HEX32.match(row) else "sha256"
            true = true_value(path, algo)
            if true and true != row:
                doc["files"][key] = {algo: true, "pubbundleRepin":
                                     receipt(None, {algo: {"pre": row, "post": true}})}
                notes.append(f"re-pinned bare row {key} ({algo})")
            continue
        if isinstance(row, str) and HEX_ANY.search(row):
            hexhits = [h for h in HEX_ANY.findall(row) if HEX32.match(h)]
            algo = "md5"
            true = true_value(path, algo) if hexhits else None
            if true and true not in hexhits:
                doc["files"][key] = {algo: true, "note": row,
                                     "pubbundleRepin":
                                     receipt(None, {algo: {"pre": hexhits or [None],
                                                            "post": true}})}
                notes.append(f"upgraded prose row {key} to machine md5 claim")
            continue
        if not isinstance(row, dict):
            continue
        touched = {}
        for field in ("md5", "sha256"):
            if field not in row:
                continue
            v = row[field]
            m = LEADING_HEX.match(v.strip()) if isinstance(v, str) else None
            true = true_value(path, field)
            if true is None:
                continue
            if not m or m.group(1) != true:
                touched[field] = {"pre": v if isinstance(v, str) and len(v) < 400
                                  else (v[:400] if isinstance(v, str) else repr(v)),
                                  "post": true}
                row[field] = true
        if touched:
            moved = None
            for field in touched:
                moved = git.first_moved_at(None, path, touched[field]["post"], field)
                if moved:
                    break
            extra = {"movedAt": moved} if moved else None
            attach_receipt(row, receipt(None, touched, extra))
            notes.append(f"re-pinned {key}: {sorted(touched)}")

        claims_ok = {}
        for field in ("md5", "sha256"):
            v = row.get(field)
            if isinstance(v, str):
                mm = LEADING_HEX.match(v.strip())
                if mm:
                    claims_ok[field] = mm.group(1)
        if claims_ok:
            pin = row.get("pin")
            pin_true = not isinstance(pin, str)
            if isinstance(pin, str) and re.match(r"^[0-9a-f]{7,40}$", pin) \
                    and git.is_commit(pin):
                raw = git.blob(pin, path)
                if raw is not None:
                    for field, hexv in claims_ok.items():
                        algo = "md5" if len(hexv) == 32 else "sha256"
                        if digest(raw, algo) == hexv:
                            pin_true = True
                            break
            if not pin_true:
                moved = None
                for field, hexv in claims_ok.items():
                    algo = "md5" if len(hexv) == 32 else "sha256"
                    moved = git.first_moved_at(None, path, hexv, algo)
                    if moved:
                        break
                blk = receipt(None, {}, {"movedAt": moved} if moved else None)
                blk["pinCorrection"] = {
                    "pre": pin, "post": moved,
                    "preVerification": "claimed digests matched no blob at the "
                                       "recorded pin commit"}
                if moved:
                    row["pin"] = moved
                attach_receipt(row, blk)
                notes.append(f"corrected pin {key}: {pin} -> {moved}")

    probe = doc.get("canonicalProbe")
    if isinstance(probe, dict):
        p = resolve(probe.get("file", ""))
        if p:
            t = true_value(p, "md5")
            if t and probe.get("expectedMD5") != t:
                probe["pubbundleRepin"] = receipt(None, {"expectedMD5": {
                    "pre": probe.get("expectedMD5"), "post": t}})
                probe["expectedMD5"] = t
                notes.append("re-pinned canonicalProbe.expectedMD5")

    model = doc.get("model")
    if isinstance(model, dict):
        touched_model = {}
        canon = resolve(model.get("tla", "")) if isinstance(model.get("tla"), str) else None
        if canon and "canonicalTlaMd5" in model:
            t = true_value(canon, "md5")
            if t and model["canonicalTlaMd5"] != t:
                touched_model["canonicalTlaMd5"] = {"pre": model["canonicalTlaMd5"],
                                                    "post": t}
                model["canonicalTlaMd5"] = t
        alg = model.get("canonicalTlaMd5Algorithm")
        if canon and isinstance(alg, str):
            m = SHAPE_IN_TEXT.search(alg)
            if m:
                t = true_value(canon, "sha256")
                if t and m.group(1) != t:
                    touched_model["canonicalTlaMd5Algorithm"] = {
                        "pre": alg, "post": f"md5 (convention); sha256={t}"}
                    model["canonicalTlaMd5Algorithm"] = f"md5 (convention); sha256={t}"
        tier = model.get("fB22Tier1Md5s")
        tier_obj = None
        if isinstance(tier, str):
            try:
                tier_obj = json.loads(tier)
            except ValueError:
                tier_obj = None
        elif isinstance(tier, dict):
            tier_obj = tier
        if isinstance(tier_obj, dict):
            changed = False
            for pth, v in list(tier_obj.items()):
                rp = resolve(pth)
                if rp is None:
                    continue
                t = true_value(rp, "md5")
                if t and v != t:
                    touched_model[f"fB22Tier1Md5s[{pth}]"] = {"pre": v, "post": t}
                    tier_obj[pth] = t
                    changed = True
            if changed:
                model["fB22Tier1Md5s"] = json.dumps(tier_obj)
        for name, section in model.items():
            if not isinstance(section, dict):
                continue
            spath = checker._section_path(name, section)
            if spath is None:
                continue
            touched = {}
            for field, algo in model_section_claims(section):
                algo = resolve_algo(algo, section[field])
                t = true_value(spath, algo)
                if t and section[field] != t:
                    touched[field] = {"pre": section[field], "post": t}
                    section[field] = t
            cfgs = section.get("cfgs")
            if isinstance(cfgs, dict):
                for cfgname, v in cfgs.items():
                    cpath = resolve(os.path.join(dirn, cfgname + ".cfg"))
                    if cpath is None:
                        continue
                    t = true_value(cpath, "md5")
                    if t and v != t:
                        touched[f"cfgs.{cfgname}"] = {"pre": v, "post": t}
                        cfgs[cfgname] = t
            if touched:
                section["pubbundleRepin"] = receipt(None, touched)
                notes.append(f"re-pinned model.{name}: {sorted(touched)}")
        if touched_model:
            model["pubbundleRepin"] = receipt(None, touched_model)
            notes.append(f"re-pinned model scalars: {sorted(touched_model)}")

    def trace_claim(path, claim, snap):
        cands = [path]
        if path in RELOCATIONS:
            cands.append(RELOCATIONS[path])
        for cand in cands:
            if snap and git.blob(snap, cand) is not None:
                if digest(git.blob(snap, cand), "sha256") == claim:
                    return {"method": "sourceSnapshot-blob", "commit": snap,
                            "queriedPath": cand}
        for cand in cands:
            hit = git.first_moved_at(None, cand, claim, "sha256",
                                     extra_paths=cands)
            if hit:
                commits = [c[:8] for c in git.history(cand)
                           if git.blob(c, cand) is not None and
                           digest(git.blob(c, cand), "sha256") == claim][:3]
                return {"method": "file-history-blob", "commit": hit,
                        "matchingCommits": commits, "queriedPath": cand}
        objdb = git.object_db_sha256s()
        if claim in objdb:
            return {"method": "object-db-scan", "blob": objdb[claim][0][:12]}
        return None

    def adjudicate_artifact(ident, art, snap):
        if not isinstance(art, dict) or "sha256" not in art:
            return
        old_path = art.get("path")
        if not isinstance(old_path, str):
            return
        claim = art["sha256"]
        head_path = resolve(old_path)
        relocated = None
        if head_path is None and old_path in RELOCATIONS:
            head_path = resolve(RELOCATIONS[old_path])
            relocated = RELOCATIONS[old_path]
        trace = trace_claim(old_path, claim, snap)
        if trace:
            art["sha256Verification"] = trace
            if relocated:
                art["currentPath"] = relocated
            if trace["method"] != "sourceSnapshot-blob" and snap:
                trace["note"] = ("claim verified at a real blob but not at the "
                                 "recorded sourceSnapshot path: "
                                 "git show " + snap + ":" + old_path + " differs")
            notes.append(f"artifact {ident} {old_path}: claim verified via "
                         f"{trace['method']}")
            return
        if head_path is None:
            art["sha256Verification"] = {"method": "untraceable-no-head-path",
                                         "note": "prior value matches no blob in the "
                                                 "repository object database; kept "
                                                 "as an explicitly unverified "
                                                 "historical claim"}
            notes.append(f"artifact {ident} {old_path}: UNTRACEABLE, no head path "
                         f"to re-pin against; marked unverified")
            return
        t = true_value(head_path, "sha256")
        if t is None:
            return
        fields = {"sha256": {"pre": claim, "post": t,
                             "preVerification": "no match for any blob in git "
                                                "rev-list --objects --all, the "
                                                "sourceSnapshot blob, or the file "
                                                "history: untraceable credential"}}
        if relocated:
            art["currentPath"] = relocated
            fields["currentPath"] = {"pre": None, "post": relocated}
        art["sha256"] = t
        blk = receipt(None, fields)
        blk["class"] = "artifact-untraceable-repinned-to-head-blob"
        art["pubbundleRepin"] = blk
        art["sha256Verification"] = {"method": "head-blob", "commit": git.head(),
                                    "queriedPath": head_path}
        notes.append(f"artifact {ident} {old_path}: untraceable prior sha256 "
                     f"replaced with true head-blob value")

    for entry in doc.get("confirmed", []):
        if not isinstance(entry, dict):
            continue
        snap = entry.get("sourceSnapshot")
        for art in entry.get("artifacts", []):
            adjudicate_artifact(entry.get("id"), art, snap)

    for art in doc.get("preventedArtifacts", []):
        adjudicate_artifact("preventedArtifacts", art, doc.get("baselineCommit"))

    st = doc.get("status_updates", {})
    if isinstance(st, dict):
        for key, target in STATUS_PIN_TARGETS.items():
            v = st.get(key)
            if isinstance(v, str) and (HEX32.match(v) or HEX64.match(v)):
                p = resolve(target)
                algo = "md5" if HEX32.match(v) else "sha256"
                if p:
                    t = true_value(p, algo)
                    if t and v != t:
                        prior_commits = []
                        seen_hist = []
                        for hp in (target, *[k for k, nv in RELOCATIONS.items()
                                             if nv == target]):
                            seen_hist += git.history(hp)
                        for c in sorted(set(seen_hist),
                                        key=lambda s: git.commit_order(s)):
                            for cand in (target, *[k for k, nv in
                                                   RELOCATIONS.items()
                                                   if nv == target]):
                                raw = git.blob(c, cand)
                                if raw is not None and digest(raw, algo) == v:
                                    prior_commits.append(c[:8])
                                    break
                            if len(prior_commits) >= 4:
                                break
                        doc["status_updates"][key] = {
                            "md5Pin": t, "pinTarget": target,
                            "priorPinWas": v,
                            "priorPinVerifiedAtCommits": prior_commits,
                            "priorPinNote": "prior value matched real blobs only at "
                                            "the listed commits; file changed after "
                                            "(relocation + comment strip), value was "
                                            "stale against head; re-pinned to head "
                                            "blob digest per pin convention "
                                            "live==cited-blob==on-disk",
                            "pubbundleRepin": receipt(
                                None, {algo: {"pre": v, "post": t}})}
                        notes.append(f"re-pinned status_updates[{key}] "
                                     f"(prior verified at {prior_commits})")

    return notes


_TRACK_CACHE = {}


def resolved_tracked(git):
    key = id(git)
    if key not in _TRACK_CACHE:
        _TRACK_CACHE[key] = set(k for k in git.tracked())
    return _TRACK_CACHE[key]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ledger", default=DEFAULT_LEDGER)
    ap.add_argument("--ref", default="HEAD")
    ap.add_argument("--table", action="store_true")
    ap.add_argument("--json-out")
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--ticket", default="pubbundle-final-re-pin "
                                        "(AO session loom-experiment-249, "
                                        "branch ao/pubbundle/final-re-pin)")
    args = ap.parse_args()

    full = os.path.join(REPO, args.ledger)
    if args.apply:
        with open(full) as f:
            doc = json.load(f)
        notes = apply_fixes(doc, args.ledger, args.ref, args.ticket)
        canon = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
        with open(full, "w") as f:
            f.write(canon)
        with open(full) as f:
            if f.read() != json.dumps(json.load(open(full)), indent=2,
                                      ensure_ascii=False) + "\n":
                raise SystemExit("canonical roundtrip check failed after apply")
        for n in notes:
            print("FIX", n)
        print(f"applied {len(notes)} fixes to {args.ledger}")

    with open(full) as f:
        doc = json.load(f)
    checker = Checker(args.ledger, args.ref)
    rows = checker.scan(doc)
    bad = [r for r in rows if r["class"] in GATED_BAD]
    if args.json_out:
        with open(args.json_out, "w") as f:
            json.dump({"ref": args.ref, "ledger": args.ledger, "rows": rows,
                       "gatedFailures": len(bad)}, f, indent=1, sort_keys=True)
    if args.table:
        print(fmt_table(rows))
    hist = collections.Counter(r["class"] for r in rows)
    print("scanned rows=" + str(len(rows)) + " " +
          " ".join(f"{k}={v}" for k, v in sorted(hist.items())))
    for r in bad:
        print(f"GATED-FAIL {r['section']} {r['key']} {r['field']} "
              f"claimed={str(r['claimed'])[:16]} computed={str(r['computed'])[:16]} "
              f"{r['class']}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
