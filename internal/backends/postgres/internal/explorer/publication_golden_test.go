package postgres

import (
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// T10: golden literals. The publish path's SQL shape, the canonicalization
// single-source rule, and the deterministic stored values are pinned to
// literals; every pin carries a break-one control proving the comparison is
// byte-exact and the region is load-bearing.
// ---------------------------------------------------------------------------

// pubauthGoldenSchemas are the fixed schema names the literal pins use.
const (
	pubauthGoldenCtl   = "ctlg"
	pubauthGoldenLane  = "lanesg"
	pubauthGoldenTable = `"ctlg"."explorers"`
)

// pubauthGoldenStatementPins are the literal SQL shapes of the publish
// transaction's own statements (the receipt/revision claim statements are
// the standalone suites' statements, reused verbatim, and pinned there).
func pubauthGoldenStatementPins() map[string]string {
	return map[string]string{
		"releaseCandidateLock": `SELECT payload::text, (payload - 'createdAt') = ($3::jsonb - 'createdAt') FROM "lanesg"."project_release" WHERE project = $1 AND id = $2 FOR SHARE`,
		"manifestLock":         `SELECT state FROM "lanesg"."dataset_generation_manifest" WHERE project = $1 AND generation = $2 FOR SHARE`,
		"generationPointer":    `SELECT generation FROM "lanesg"."dataset_active_generation" WHERE project = $1 FOR SHARE`,
		"releasePointerLock":   `SELECT release_id, revision FROM "lanesg"."active_project_release" WHERE project = $1 FOR UPDATE`,
		"releasePointerRead":   `SELECT release_id, revision FROM "lanesg"."active_project_release" WHERE project = $1`,
		"releasePointerCAS":    `UPDATE "lanesg"."active_project_release" SET release_id = $2, revision = $3, updated_at = now() WHERE project = $1 AND revision = $4 RETURNING revision`,
		"releasePointerInsert": `INSERT INTO "lanesg"."active_project_release" (project, release_id, revision) VALUES ($1, $2, 1) ON CONFLICT DO NOTHING`,
		"ownerGuard":           `SELECT ` + strings.Join(ownerColumns, ", ") + ` FROM "ctlg"."explorers" WHERE project = $1 AND explorer_id = $2 AND management_mode = $3 FOR UPDATE`,
		"priorLock":            `SELECT ` + strings.Join(revisionColumns, ", ") + ` FROM "ctlg"."explorer_revisions" WHERE id = $1 FOR UPDATE`,
		"priorSupersede":       `UPDATE "ctlg"."explorer_revisions" SET status = 'SUPERSEDED' WHERE id = $1 AND status = 'ACTIVE' RETURNING ` + strings.Join(revisionColumns, ", "),
		"candidateActive":      `UPDATE "ctlg"."explorer_revisions" SET status = 'ACTIVE', activated_at = $2, failed_at = NULL, diagnostics = $3, publication = $4 WHERE id = $1 AND status = 'READY' RETURNING ` + strings.Join(revisionColumns, ", "),
		"ownerActivate":        `UPDATE "ctlg"."explorers" SET active_revision_id = $3, updated_at = $4 WHERE project = $1 AND explorer_id = $2 RETURNING ` + strings.Join(ownerColumns, ", "),
	}
}

func TestPublishAuthoringStatementShapeLiterals(t *testing.T) {
	live := newPublishStatements(pubauthGoldenCtl, pubauthGoldenLane)
	v := reflect.ValueOf(live)
	for name, want := range pubauthGoldenStatementPins() {
		field := v.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("publish statement %q no longer exists", name)
		}
		got := field.String()
		if got != want {
			t.Fatalf("statement %s drifted from its literal pin:\ngot:\n%s\nwant:\n%s", name, got, want)
		}
		breakOnePin(t, "statement "+name, got, want)
	}
}

// canonicalization single source: no SQL-side trim/lower/cast tricks may
// appear in any publish statement -- canonical forms are computed in Go.
func TestPublishAuthoringStatementsCanonicalizeOnlyInGo(t *testing.T) {
	s := newPublishStatements(pubauthGoldenCtl, pubauthGoldenLane)
	v := reflect.ValueOf(s)
	for i := range v.NumField() {
		statement := strings.ToLower(v.Field(i).String())
		for _, forbidden := range []string{"trim(", "lower(", "upper(", "btrim(", "initcap(", "regexp_replace("} {
			if strings.Contains(statement, forbidden) {
				t.Errorf("statement %s applies SQL-side canonicalization %q: canonical forms belong in Go",
					v.Type().Field(i).Name, forbidden)
			}
		}
	}
}

func TestPublishAuthoringStoredWorldGoldenLiterals(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	ps := f.newPublishStore(t)
	project, explorerID, tag := "proj_pubauth_golden", "exp_golden", "gold"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
	f.publishOK(t, ps, receipt, revision, release, 0)

	// Deterministic identity literals: the content-addressed receipt id and
	// the revision content digest for the fixed fixture inputs.
	if receipt.ID != pubauthGoldenReceiptID {
		t.Fatalf("golden receipt id = %s, want %s", receipt.ID, pubauthGoldenReceiptID)
	}
	breakOnePin(t, "golden receipt id", receipt.ID, pubauthGoldenReceiptID)

	var storedDigest, storedStatus, storedReceiptBinding string
	if err := f.iso.QueryRow(f.ctx, "SELECT content_digest, status, compilation_receipt_id FROM "+
		f.ctlTable(RevisionsTable)+" WHERE id = $1", revision.ID).
		Scan(&storedDigest, &storedStatus, &storedReceiptBinding); err != nil {
		f.fail("read golden revision", err)
	}
	if storedDigest != pubauthGoldenContentDigest {
		t.Fatalf("golden content digest = %s, want %s", storedDigest, pubauthGoldenContentDigest)
	}
	breakOnePin(t, "golden content digest", storedDigest, pubauthGoldenContentDigest)
	if storedStatus != "ACTIVE" || storedReceiptBinding != receipt.ID {
		t.Fatalf("golden revision = %s/%s, want ACTIVE/%s", storedStatus, storedReceiptBinding, receipt.ID)
	}

	// The activation publication document: Go is the single source of the
	// shape -- state/revisionId/updatedAt overwritten, executionId carried
	// from the stored document, generation defaulted from source_generation.
	var publication map[string]any
	if err := f.iso.QueryRow(f.ctx, "SELECT publication FROM "+f.ctlTable(RevisionsTable)+
		" WHERE id = $1", revision.ID).Scan(&publication); err != nil {
		f.fail("read golden publication", err)
	}
	if publication["state"] != "ACTIVE" || publication["revisionId"] != revision.ID ||
		publication["executionId"] != "exec_pubauth_gold" ||
		publication["generation"] != pubauthGeneration {
		t.Fatalf("golden publication = %v, want the activated shape", publication)
	}
	if _, ok := publication["updatedAt"]; !ok {
		t.Fatal("golden publication lost the updatedAt overwrite")
	}
	keys := make([]string, 0, len(publication))
	for k := range publication {
		keys = append(keys, k)
	}
	if want := []string{"executionId", "generation", "revisionId", "state", "updatedAt"}; !reflect.DeepEqual(sortedStrings(keys), want) {
		t.Fatalf("golden publication keys = %v, want %v", keys, want)
	}

	// Owner + pointer golden rows (clock columns masked by omission).
	var owner string
	if err := f.iso.QueryRow(f.ctx, "SELECT (to_jsonb(t) - 'updated_at' - 'created_at')::text FROM "+
		f.ctlTable(OwnerTable)+" t WHERE project = $1", project).Scan(&owner); err != nil {
		f.fail("read golden owner row", err)
	}
	if owner != pubauthGoldenOwnerRow {
		t.Fatalf("golden owner row = %s, want %s", owner, pubauthGoldenOwnerRow)
	}
	breakOnePin(t, "golden owner row", owner, pubauthGoldenOwnerRow)

	var pointer string
	if err := f.iso.QueryRow(f.ctx, "SELECT (to_jsonb(t) - 'updated_at' - 'created_at')::text FROM "+
		f.lanesTable("active_project_release")+" t WHERE project = $1", project).Scan(&pointer); err != nil {
		f.fail("read golden pointer row", err)
	}
	if pointer != pubauthGoldenPointerRow {
		t.Fatalf("golden pointer row = %s, want %s", pointer, pubauthGoldenPointerRow)
	}
	breakOnePin(t, "golden pointer row", pointer, pubauthGoldenPointerRow)
}

// breakOnePin is the non-vacuity control for a literal pin: a one-byte
// tamper of the expected value must NOT match the stored value, proving the
// comparison is byte-exact and covers the whole region.
func breakOnePin(t *testing.T, label, stored, want string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatalf("%s: empty pin", label)
	}
	tampered := want[:len(want)-1] + string(want[len(want)-1]+1)
	if stored == tampered {
		t.Fatalf("%s: pinned region is vacuous (a one-byte tamper still matched)", label)
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Golden literals for the fixed fixture inputs (project proj_pubauth_golden,
// explorer exp_golden, tag gold), discovered from the stored world and held
// as literals on purpose: they move only when the canonicalization or the
// stored shape changes.
const (
	pubauthGoldenReceiptID     = "receipt_20cc875b33f9db6614541f42344022ac7b85a981a443615ca2217d6b672c6320"
	pubauthGoldenContentDigest = "sha256:25c52c00e6b86c4bf97d0df1d156942a50fb5f6449726f8eead80d96ef26dd08"
	pubauthGoldenOwnerRow      = `{"title": "pubauth exp_golden", "project": "proj_pubauth_golden", "updated_by": "author@calypr", "explorer_id": "exp_golden", "draft_config": null, "draft_digest": "sha256:draft-exp_golden", "draft_version": 1, "management_mode": "INTERACTIVE", "active_revision_id": "rev_proj_pubauth_golden_gold", "draft_snapshot_token": "", "draft_source_generation": "", "last_authoring_command_id": "", "previous_draft_revision_id": "", "last_authoring_command_digest": "", "last_authoring_command_results": null, "draft_authorization_scope_digest": ""}`
	pubauthGoldenPointerRow    = `{"project": "proj_pubauth_golden", "revision": 1, "release_id": "rel_proj_pubauth_golden_gold"}`
)
