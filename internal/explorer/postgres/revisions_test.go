package postgres

// Immutable Explorer revision tests for
// pg-state-receipt-revision-immutable-v1.
//
// Literal rows and direct-SQL witnesses against a real PostgreSQL 18, no
// mocks: insert-once with canonical content-digest collision (idempotent
// replay / typed ErrImmutableRevision), tenant-scoped reads, lifecycle CAS
// with expected-status tokens (stale-token and double-transition refusals
// with byte-identical row witnesses), the DB-enforced frozen-column and
// monotone-state-machine guards proven non-vacuously by live DROP-TRIGGER /
// restore probes, and two-real-connection race proofs with distinct
// pg_backend_pid witnesses. TestZZNoLeftoverPGState02Resources runs last.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// ---------------------------------------------------------------------------
// Fixtures.
// ---------------------------------------------------------------------------

const (
	revisionProject  = "proj_revisions"
	revisionExplorer = "exp_revisions"
)

var revisionCreatedAt = time.Date(2026, 2, 3, 4, 5, 6, 654321000, time.UTC)

const revisionCreatedAtText = "2026-02-03 04:05:06.654321"

func pgstate02RevisionBundle(name string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "output", RootResourceType: "Patient", RowGrain: "resource",
		}},
	}
}

// pgstate02Revision builds a READY revision with every frozen field
// populated: typed digests, the three json.RawMessage tri-state payloads,
// materialization mappings, emitted columns, and dataset metadata.
func pgstate02Revision(id, name string) explorer.Revision {
	return explorer.Revision{
		ID:                   id,
		Project:              revisionProject,
		ExplorerID:           revisionExplorer,
		Config:               json.RawMessage(cfgAlpha),
		AuthoringBundle:      json.RawMessage(cfgBeta),
		IntentDigest:         "sha256:intent-" + id,
		CompilationReceiptID: "receipt_" + id,
		PublicOutputContract: json.RawMessage(resultsOK),
		Recipe:               pgstate02RevisionBundle(name),
		RecipeDigest:         "sha256:recipe-" + id,
		ResolvedSchemaDigest: "sha256:schema-" + id,
		SourceGeneration:     "gen_pg_01",
		Materializations: []explorer.Materialization{{
			OutputID: "output", Output: "output", MaterializationID: "mat_1",
			Fingerprint: "fp-1",
			Columns: []publication.PhysicalColumn{{
				Name: "day", ClickHouse: "Date", LogicalType: "date",
			}},
		}},
		EmittedColumns: []explorer.EmittedColumn{{
			EmissionID: "emit_1", OutputID: "output", PublicColumn: "day",
			LogicalType: "date", Shape: "scalar", Filterable: true,
		}},
		Dataset: explorer.DatasetMetadata{
			Generation:   "gen_pg_01",
			SchemaDigest: "sha256:dataset",
			Outputs:      []explorer.DatasetOutput{{Name: "output", State: "QUERYABLE", Queryable: true}},
		},
		Status:    explorer.RevisionReady,
		CreatedBy: "author@calypr",
		CreatedAt: revisionCreatedAt,
	}
}

func pgstate02Diagnostic(code string) []explorer.Diagnostic {
	return []explorer.Diagnostic{{Severity: "ERROR", Code: code, Message: "witnessed failure"}}
}

func requireRevisionEqual(t *testing.T, want, got explorer.Revision, where string) {
	t.Helper()
	if got.ID != want.ID || got.Project != want.Project || got.ExplorerID != want.ExplorerID {
		t.Fatalf("%s: identity changed: got %q/%q/%q want %q/%q/%q", where,
			got.Project, got.ExplorerID, got.ID, want.Project, want.ExplorerID, want.ID)
	}
	if got.IntentDigest != want.IntentDigest || got.CompilationReceiptID != want.CompilationReceiptID ||
		got.RecipeDigest != want.RecipeDigest || got.ResolvedSchemaDigest != want.ResolvedSchemaDigest ||
		got.SourceGeneration != want.SourceGeneration || got.Status != want.Status ||
		got.CreatedBy != want.CreatedBy {
		t.Fatalf("%s: frozen scalars/status changed: got %+v want %+v", where, got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("%s: createdAt changed: got %s want %s", where, got.CreatedAt, want.CreatedAt)
	}
	jsonEqual(t, want.Config, got.Config, where+" config")
	jsonEqual(t, want.AuthoringBundle, got.AuthoringBundle, where+" authoringBundle")
	jsonEqual(t, want.PublicOutputContract, got.PublicOutputContract, where+" publicOutputContract")
	if got.Recipe.Name != want.Recipe.Name || len(got.Recipe.Outputs) != len(want.Recipe.Outputs) {
		t.Fatalf("%s: canonical recipe not lossless: got %+v want %+v", where, got.Recipe, want.Recipe)
	}
	if len(want.Materializations) != len(got.Materializations) {
		t.Fatalf("%s: materialization count changed: got %d want %d", where, len(got.Materializations), len(want.Materializations))
	}
	if len(want.Materializations) > 0 {
		w, g := want.Materializations[0], got.Materializations[0]
		if g.OutputID != w.OutputID || g.MaterializationID != w.MaterializationID ||
			g.Fingerprint != w.Fingerprint || len(g.Columns) != len(w.Columns) ||
			(len(w.Columns) > 0 && g.Columns[0].Name != w.Columns[0].Name) {
			t.Fatalf("%s: materialization mapping not lossless: got %+v want %+v", where, g, w)
		}
	}
	if len(want.EmittedColumns) != len(got.EmittedColumns) ||
		(len(want.EmittedColumns) > 0 && got.EmittedColumns[0].PublicColumn != want.EmittedColumns[0].PublicColumn) {
		t.Fatalf("%s: emitted columns changed: got %+v want %+v", where, got.EmittedColumns, want.EmittedColumns)
	}
	if got.Dataset.Generation != want.Dataset.Generation ||
		got.Dataset.SchemaDigest != want.Dataset.SchemaDigest ||
		len(got.Dataset.Outputs) != len(want.Dataset.Outputs) {
		t.Fatalf("%s: dataset metadata changed: got %+v want %+v", where, got.Dataset, want.Dataset)
	}
	if (want.Diagnostics == nil) != (got.Diagnostics == nil) ||
		len(want.Diagnostics) != len(got.Diagnostics) ||
		(len(want.Diagnostics) > 0 && got.Diagnostics[0].Code != want.Diagnostics[0].Code) {
		t.Fatalf("%s: diagnostics changed: got %+v want %+v", where, got.Diagnostics, want.Diagnostics)
	}
}

// revisionArgsFor encodes a revision for direct-SQL statements exactly the
// way the Store does, so raw probes exercise identical shapes.
func pgstate02RevisionArgs(revision explorer.Revision) ([]any, string, error) {
	digest, err := revisionContentDigest(revision)
	if err != nil {
		return nil, "", err
	}
	payloads, err := encodeRevisionPayloads(revision)
	if err != nil {
		return nil, "", err
	}
	diagnostics, err := marshalOrNil(revision.Diagnostics)
	if err != nil {
		return nil, "", err
	}
	return revisionArgs(revision, payloads, digest, diagnostics), digest, nil
}

func (f *pgState02Fixture) rawInsertRevision(t *testing.T, revision explorer.Revision) string {
	t.Helper()
	args, digest, err := pgstate02RevisionArgs(revision)
	if err != nil {
		f.fail("encode raw revision fixture", err)
	}
	statements := newRevisionStatements(f.schema)
	insert := strings.Replace(statements.insert,
		" ON CONFLICT (id) DO NOTHING", "", 1)
	if _, err := f.iso.Exec(f.ctx, insert, args...); err != nil {
		f.fail("raw insert revision "+revision.ID, err)
	}
	return digest
}

type revisionLiteralRow struct {
	ID                   string
	Project              string
	ExplorerID           string
	ConfigKind           string
	AuthoringKind        string
	ContractKind         string
	MaterializationsKind string
	EmittedKind          string
	DatasetKind          string
	QualityKind          string
	DiagnosticsKind      string
	PublicationKind      string
	ContentDigest        string
	Status               string
	CreatedBy            string
	CreatedAtText        string
	FailedAtText         string
	ActivatedAtText      string
	ConfigText           string
}

func (f *pgState02Fixture) revisionLiteralRow(t *testing.T, id string) revisionLiteralRow {
	t.Helper()
	var row revisionLiteralRow
	err := f.iso.QueryRow(f.ctx, `SELECT
		id, project, explorer_id,
		COALESCE(jsonb_typeof(config), 'missing'),
		COALESCE(jsonb_typeof(authoring_bundle), 'missing'),
		COALESCE(jsonb_typeof(public_output_contract), 'missing'),
		COALESCE(jsonb_typeof(materializations), 'missing'),
		COALESCE(jsonb_typeof(emitted_columns), 'missing'),
		jsonb_typeof(dataset),
		COALESCE(jsonb_typeof(quality_reports), 'missing'),
		COALESCE(jsonb_typeof(diagnostics), 'missing'),
		jsonb_typeof(publication),
		content_digest, status, created_by,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		COALESCE(to_char(failed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), ''),
		COALESCE(to_char(activated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), ''),
		COALESCE(config::text, '')
	FROM `+f.revisionTable()+` WHERE id = $1`, id).Scan(
		&row.ID, &row.Project, &row.ExplorerID,
		&row.ConfigKind, &row.AuthoringKind, &row.ContractKind,
		&row.MaterializationsKind, &row.EmittedKind, &row.DatasetKind,
		&row.QualityKind, &row.DiagnosticsKind, &row.PublicationKind,
		&row.ContentDigest, &row.Status, &row.CreatedBy,
		&row.CreatedAtText, &row.FailedAtText, &row.ActivatedAtText,
		&row.ConfigText)
	if err != nil {
		f.fail("read literal revision row "+id, err)
	}
	return row
}

func (f *pgState02Fixture) revisionCount(t *testing.T, project string) int {
	t.Helper()
	var n int
	if err := f.iso.QueryRow(f.ctx,
		`SELECT count(*) FROM `+f.revisionTable()+` WHERE project = $1`, project).Scan(&n); err != nil {
		f.fail("count revision rows", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// Unit tests: no PostgreSQL service required.
// ---------------------------------------------------------------------------

func TestPostgresRevisionSQLContract(t *testing.T) {
	statements := newRevisionStatements("loom_control")
	if !strings.Contains(statements.insert, "ON CONFLICT (id) DO NOTHING") {
		t.Error("revision insert must be insert-once: ON CONFLICT (id) DO NOTHING")
	}
	if !strings.Contains(statements.insert, "RETURNING") {
		t.Error("revision insert must RETURNING the stored row")
	}
	if !strings.Contains(statements.insert, `"loom_control"."`+RevisionsTable+`"`) {
		t.Error("revision insert must bind the schema-qualified, sanitized table")
	}
	if !strings.Contains(statements.claim, "WHERE id = $1 FOR UPDATE") {
		t.Error("the revision conflict reread must be FOR UPDATE by id")
	}
	if !strings.Contains(statements.getScoped, "WHERE project = $1 AND explorer_id = $2 AND id = $3") {
		t.Error("the scoped revision read must be by project AND explorer_id AND id")
	}
	if !strings.Contains(statements.failAny, "WHERE id = $3 AND status IN ('READY', 'ACTIVE')") {
		t.Error("FailRevision must be a CAS admitting only the two failure-eligible states")
	}
	if !strings.Contains(statements.failToken, "WHERE id = $3 AND status = $4 AND status IN ('READY', 'ACTIVE')") {
		t.Error("FailRevisionFrom must carry the expected-status token in its WHERE clause")
	}
	if !strings.Contains(statements.failAny, "UPDATE") {
		t.Error("FailRevision must be an UPDATE")
	}
	for i := 1; i <= len(revisionColumns); i++ {
		if !strings.Contains(statements.insert, fmt.Sprintf("$%d", i)) {
			t.Fatalf("revision insert is missing ordered parameter $%d", i)
		}
	}
	if strings.Contains(statements.insert, fmt.Sprintf("$%d", len(revisionColumns)+1)) {
		t.Fatalf("revision insert carries more than %d parameters", len(revisionColumns))
	}
	args, digest, err := pgstate02RevisionArgs(pgstate02Revision("rev_contract", "contract"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(args); got != len(revisionColumns) {
		t.Fatalf("revisionArgs produced %d values, want %d (one per column)", got, len(revisionColumns))
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("content digest %q is not sha256:<64 hex>", digest)
	}
}

func TestPostgresRevisionContentDigestBoundaries(t *testing.T) {
	base := pgstate02Revision("rev_digest", "digest")
	digestOf := func(r explorer.Revision) string {
		_, digest, err := pgstate02RevisionArgs(r)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}

	// Lifecycle/mutable fields are excluded from the frozen identity.
	lifecycle := base
	lifecycle.Status = explorer.RevisionActive
	lifecycle.Diagnostics = pgstate02Diagnostic("LATE")
	activated := revisionCreatedAt.Add(time.Minute)
	lifecycle.ActivatedAt = &activated
	if digestOf(lifecycle) != digestOf(base) {
		t.Fatal("mutable lifecycle fields leaked into the frozen content digest")
	}

	// Every frozen dimension moves the digest.
	for name, mutate := range map[string]func(*explorer.Revision){
		"config":              func(r *explorer.Revision) { r.Config = json.RawMessage(cfgBeta) },
		"authoringBundle":     func(r *explorer.Revision) { r.AuthoringBundle = json.RawMessage(cfgAlpha) },
		"publicOutput":        func(r *explorer.Revision) { r.PublicOutputContract = json.RawMessage(cfgBeta) },
		"recipe":              func(r *explorer.Revision) { r.Recipe = pgstate02RevisionBundle("other") },
		"recipeDigest":        func(r *explorer.Revision) { r.RecipeDigest = "sha256:other" },
		"resolvedSchema":      func(r *explorer.Revision) { r.ResolvedSchemaDigest = "sha256:other" },
		"sourceGeneration":    func(r *explorer.Revision) { r.SourceGeneration = "gen_other" },
		"materializationMap":  func(r *explorer.Revision) { r.Materializations[0].MaterializationID = "mat_2" },
		"materializationDrop": func(r *explorer.Revision) { r.Materializations = nil },
		"materializationEmpt": func(r *explorer.Revision) { r.Materializations = []explorer.Materialization{} },
		"emittedColumns":      func(r *explorer.Revision) { r.EmittedColumns = nil },
		"dataset":             func(r *explorer.Revision) { r.Dataset.Generation = "gen_other" },
		"qualityReports":      func(r *explorer.Revision) { r.QualityReports = []publication.QualityReport{{ID: "q1"}} },
		"intent":              func(r *explorer.Revision) { r.IntentDigest = "sha256:other" },
		"receipt":             func(r *explorer.Revision) { r.CompilationReceiptID = "receipt_other" },
		"project":             func(r *explorer.Revision) { r.Project = "proj_intruder" },
		"explorer":            func(r *explorer.Revision) { r.ExplorerID = "exp_intruder" },
		"rawKeyOrder": func(r *explorer.Revision) {
			r.Config = json.RawMessage(`{"limit": 25, "beta": true, "note": null, "title": "Alpha", "filters": [{"op": "eq", "value": "2026-01-01", "column": "day"}]}`)
		},
	} {
		mutated := pgstate02Revision("rev_digest", "digest")
		mutate(&mutated)
		if digestOf(mutated) == digestOf(base) {
			t.Fatalf("frozen dimension %s did not move the content digest", name)
		}
	}
}

// TestPostgresRevisionTriStateArangoParityReplay drives both digest parity
// boundaries through the full store path on live PostgreSQL:
//   - an explicit JSON-null raw payload stored in the row refuses a
//     missing-payload replay with ErrImmutableRevision, witnessed
//     unchanged by direct SQL (the class the Arango adapter refuses,
//     where reflect.DeepEqual separates a decoded nil from null bytes);
//   - the byte-exact null replay, a nil-vs-empty quality-report replay
//     (publication.CloneQualityReports collapses them), and an
//     empty-RawMessage replay of a stored missing payload all return the
//     first stored row idempotently, matching Arango.
func TestPostgresRevisionTriStateArangoParityReplay(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	nullish := pgstate02Revision("rev_tri_null", "tri")
	nullish.Config = json.RawMessage("null")
	nullish.QualityReports = nil
	if _, err := writer.InsertRevision(f.ctx, nullish); err != nil {
		f.fail("insert explicit-null revision", err)
	}
	before := f.rowSnapshot(t, f.revisionTable(), "id = $1", nullish.ID)

	missingReplay := nullish
	missingReplay.Config = nil
	if _, err := writer.InsertRevision(f.ctx, missingReplay); !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("missing-payload replay against stored JSON-null error = %v, want ErrImmutableRevision (Arango parity)", err)
	}
	if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", nullish.ID); after != before {
		t.Fatalf("refused tri-state replay mutated the stored row:\n before %s\n after  %s", before, after)
	}

	exact := nullish
	stored, err := writer.InsertRevision(f.ctx, exact)
	if err != nil {
		f.fail("byte-exact explicit-null replay was refused", err)
	}
	requireRevisionEqual(t, nullish, *stored, "explicit-null idempotent replay")

	emptyQR := nullish
	emptyQR.QualityReports = []publication.QualityReport{}
	if _, err := writer.InsertRevision(f.ctx, emptyQR); err != nil {
		t.Fatalf("nil-vs-empty quality-report replay was refused: %v (Arango replays this idempotently via CloneQualityReports)", err)
	}

	missingRow := pgstate02Revision("rev_tri_missing", "tri")
	missingRow.Config = nil
	if _, err := writer.InsertRevision(f.ctx, missingRow); err != nil {
		f.fail("insert missing-payload revision", err)
	}
	emptyRaw := missingRow
	emptyRaw.Config = json.RawMessage{}
	if _, err := writer.InsertRevision(f.ctx, emptyRaw); err != nil {
		t.Fatalf("empty RawMessage replay against a stored missing payload was refused: %v (omitempty collapses both classes in Arango)", err)
	}
}

func TestPostgresRevisionFailTokenAndIDValidation(t *testing.T) {
	store, err := New(&pgstore.Client{}, "loom_control")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailRevisionFrom(context.Background(), "rev", explorer.RevisionStatus("BOGUS"), nil); !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("unknown expected status error = %v, want ErrInvalidRevision", err)
	}
	if _, err := store.InsertRevision(context.Background(), explorer.Revision{ID: "  "}); !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("blank revision ID error = %v, want ErrInvalidRevision", err)
	}
}

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration.
// ---------------------------------------------------------------------------

// TestPostgresRevisionInsertGetLiteralRows proves the revision table stores
// one insert-once row: RETURNING equals the input, direct SQL witnesses
// every typed column, the jsonb typeof kinds, the microsecond-exact
// created_at, the jsonb-normalized config text, and the content_digest that
// equals the Go-computed canonical digest of the frozen column set. Fresh
// pools read back through GetRevision and the tenant-scoped
// GetRevisionForExplorer; other scopes see ErrNotFound.
func TestPostgresRevisionInsertGetLiteralRows(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	want := pgstate02Revision("rev_literal", "literal")
	created, err := writer.InsertRevision(f.ctx, want)
	if err != nil {
		f.fail("insert revision", err)
	}
	requireRevisionEqual(t, want, *created, "Insert result")

	_, digest, err := pgstate02RevisionArgs(want)
	if err != nil {
		t.Fatal(err)
	}
	row := f.revisionLiteralRow(t, want.ID)
	if row.Project != revisionProject || row.ExplorerID != revisionExplorer || row.ID != want.ID {
		t.Fatalf("literal identity = %q/%q/%q", row.Project, row.ExplorerID, row.ID)
	}
	if row.ContentDigest != digest {
		t.Fatalf("literal content_digest = %q, want the Go-computed canonical digest %q", row.ContentDigest, digest)
	}
	if row.Status != "READY" || row.CreatedBy != "author@calypr" {
		t.Fatalf("literal status/created_by = %q/%q", row.Status, row.CreatedBy)
	}
	if row.CreatedAtText != revisionCreatedAtText {
		t.Fatalf("literal created_at = %q, want %q", row.CreatedAtText, revisionCreatedAtText)
	}
	if row.FailedAtText != "" || row.ActivatedAtText != "" {
		t.Fatalf("lifecycle timestamps present at insert: failed=%q activated=%q", row.FailedAtText, row.ActivatedAtText)
	}
	if row.ConfigText != literalCfgAlphaText {
		t.Fatalf("literal config jsonb text = %s, want %s", row.ConfigText, literalCfgAlphaText)
	}
	for name, kind := range map[string]string{
		"config": "object", "authoring_bundle": "object", "public_output_contract": "object",
		"materializations": "array", "emitted_columns": "array", "dataset": "object",
		"quality_reports": "missing", "diagnostics": "missing", "publication": "object",
	} {
		var got string
		switch name {
		case "config":
			got = row.ConfigKind
		case "authoring_bundle":
			got = row.AuthoringKind
		case "public_output_contract":
			got = row.ContractKind
		case "materializations":
			got = row.MaterializationsKind
		case "emitted_columns":
			got = row.EmittedKind
		case "dataset":
			got = row.DatasetKind
		case "quality_reports":
			got = row.QualityKind
		case "diagnostics":
			got = row.DiagnosticsKind
		case "publication":
			got = row.PublicationKind
		}
		if got != kind {
			t.Fatalf("literal jsonb kind %s = %q, want %q", name, got, kind)
		}
	}
	if n := f.revisionCount(t, revisionProject); n != 1 {
		t.Fatalf("revision row count = %d, want exactly 1", n)
	}

	reader := f.newStore(t)
	got, err := reader.GetRevision(f.ctx, want.ID)
	if err != nil {
		f.fail("GetRevision on fresh pool", err)
	}
	requireRevisionEqual(t, want, *got, "fresh-pool GetRevision")

	scoped, err := reader.GetRevisionForExplorer(f.ctx, revisionProject, revisionExplorer, want.ID)
	if err != nil {
		f.fail("GetRevisionForExplorer on fresh pool", err)
	}
	requireRevisionEqual(t, want, *scoped, "fresh-pool GetRevisionForExplorer")

	if _, err := reader.GetRevisionForExplorer(f.ctx, "proj_other", revisionExplorer, want.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-project scoped read error = %v, want ErrNotFound", err)
	}
	if _, err := reader.GetRevisionForExplorer(f.ctx, revisionProject, "exp_other", want.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-explorer scoped read error = %v, want ErrNotFound", err)
	}
}

// TestPostgresRevisionIdempotentSameContent: same ID + same frozen content
// (mutable lifecycle fields differ) replays idempotently -- the stored row
// is returned unchanged, byte-identical by full-row witness.
func TestPostgresRevisionIdempotentSameContent(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	want := pgstate02Revision("rev_idem", "idem")
	if _, err := writer.InsertRevision(f.ctx, want); err != nil {
		f.fail("insert revision", err)
	}
	before := f.rowSnapshot(t, f.revisionTable(), "id = $1", want.ID)

	replay := want
	replay.Status = explorer.RevisionActive
	replay.Diagnostics = pgstate02Diagnostic("REPLAY")
	replay.ActivatedAt = &revisionCreatedAt
	stored, err := writer.InsertRevision(f.ctx, replay)
	if err != nil {
		f.fail("idempotent replay", err)
	}
	if stored.Status != explorer.RevisionReady || stored.Diagnostics != nil || stored.ActivatedAt != nil {
		t.Fatalf("replay returned the replay's lifecycle fields, not the stored row: %+v", stored)
	}
	after := f.rowSnapshot(t, f.revisionTable(), "id = $1", want.ID)
	if before != after {
		t.Fatalf("idempotent replay mutated the row:\n before %s\n after  %s", before, after)
	}
	if n := f.revisionCount(t, revisionProject); n != 1 {
		t.Fatalf("revision row count = %d, want exactly 1", n)
	}
}

// TestPostgresRevisionSameIDDifferentContentRefused: same ID + different
// frozen content is the typed ErrImmutableRevision refusal with the stored
// row byte-identical; the same frozen content under a different ID is a
// second legitimate row (the Arango adapter's per-key insert-once parity).
func TestPostgresRevisionSameIDDifferentContentRefused(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	first := pgstate02Revision("rev_collision", "collision")
	if _, err := writer.InsertRevision(f.ctx, first); err != nil {
		f.fail("insert first revision", err)
	}
	before := f.rowSnapshot(t, f.revisionTable(), "id = $1", first.ID)

	second := pgstate02Revision("rev_collision", "collision-other-recipe")
	_, err := writer.InsertRevision(f.ctx, second)
	if !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("different content at existing ID error = %v, want ErrImmutableRevision", err)
	}
	after := f.rowSnapshot(t, f.revisionTable(), "id = $1", first.ID)
	if before != after {
		t.Fatalf("refused reuse mutated the stored row:\n before %s\n after  %s", before, after)
	}

	// The same recipe content under a different ID is a second legitimate
	// row: identity is the row PK, and each row's content_digest covers its
	// own identity-bearing frozen set (Arango adapter parity: per-key
	// insert-once, not global content addressing).
	sibling := first
	sibling.ID = "rev_collision_sibling"
	stored, err := writer.InsertRevision(f.ctx, sibling)
	if err != nil {
		t.Fatalf("identical frozen content (different ID) was refused: %v", err)
	}
	siblingArgs, siblingDigest, err := pgstate02RevisionArgs(sibling)
	if err != nil {
		t.Fatal(err)
	}
	_ = siblingArgs
	var storedDigest string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT content_digest FROM `+f.revisionTable()+` WHERE id = $1`, sibling.ID).
		Scan(&storedDigest); err != nil {
		f.fail("read sibling content_digest", err)
	}
	if storedDigest != siblingDigest {
		t.Fatalf("sibling content_digest = %q, want the Go-computed %q", storedDigest, siblingDigest)
	}
	if _, digestOfFirst, _ := pgstate02RevisionArgs(first); digestOfFirst == siblingDigest {
		t.Fatal("distinct row identities must carry distinct digests: frozenRevision covers the ID")
	}
	if stored.Recipe.Name != "collision" {
		t.Fatalf("sibling stored with wrong recipe: %+v", stored.Recipe)
	}
	if n := f.revisionCount(t, revisionProject); n != 2 {
		t.Fatalf("revision row count = %d, want 2", n)
	}
}

// TestPostgresRevisionLifecycleAndDoubleTransition proves the CAS lifecycle:
// FailRevision is the only path to FAILED and admits just READY/ACTIVE; a
// second fail (the double transition) and every stale expected-status token
// are typed refusals whose refused rows stay byte-identical by full-row
// witness; a missing revision is ErrNotFound.
func TestPostgresRevisionLifecycleAndDoubleTransition(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	ready := pgstate02Revision("rev_fail_ready", "fail-ready")
	if _, err := writer.InsertRevision(f.ctx, ready); err != nil {
		f.fail("insert ready revision", err)
	}
	frozenBefore := f.frozenSnapshot(t, ready.ID)
	rowBefore := f.rowSnapshot(t, f.revisionTable(), "id = $1", ready.ID)

	failed, err := writer.FailRevision(f.ctx, ready.ID, pgstate02Diagnostic("COMPILE_FAILED"))
	if err != nil {
		f.fail("fail READY revision", err)
	}
	if failed.Status != explorer.RevisionFailed || failed.FailedAt == nil ||
		len(failed.Diagnostics) != 1 || failed.Diagnostics[0].Code != "COMPILE_FAILED" {
		t.Fatalf("fail result = %+v", failed)
	}
	if frozenAfter := f.frozenSnapshot(t, ready.ID); frozenAfter != frozenBefore {
		t.Fatalf("legal READY->FAILED transition disturbed frozen content:\n before %s\n after  %s", frozenBefore, frozenAfter)
	}
	row := f.revisionLiteralRow(t, ready.ID)
	if row.Status != "FAILED" || row.FailedAtText == "" || row.DiagnosticsKind != "array" {
		t.Fatalf("literal failed row = %+v", row)
	}

	// Double transition: the same revision cannot fail twice -- typed
	// refusal, whole row byte-identical (the second diagnostics never land).
	blocked, err := writer.FailRevision(f.ctx, ready.ID, pgstate02Diagnostic("SECOND_FAIL"))
	if !errors.Is(err, ErrStaleRevisionTransition) {
		t.Fatalf("double FailRevision error = %v (result %+v), want ErrStaleRevisionTransition", err, blocked)
	}
	postFail := f.rowSnapshot(t, f.revisionTable(), "id = $1", ready.ID)
	if postFail == rowBefore {
		t.Fatal("the first FailRevision never changed the row; the double-transition witness would be vacuous")
	}
	if again, err := writer.FailRevision(f.ctx, ready.ID, pgstate02Diagnostic("THIRD_FAIL")); !errors.Is(err, ErrStaleRevisionTransition) || again != nil {
		t.Fatalf("second double-transition attempt = %+v, %v", again, err)
	}
	if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", ready.ID); after != postFail {
		t.Fatalf("refused double transition mutated the row:\n want %s\n got  %s", postFail, after)
	}

	// Stale tokens: a token that disagrees with the committed status is
	// refused without writing, including tokens on the failed row.
	if _, err := writer.FailRevisionFrom(f.ctx, ready.ID, explorer.RevisionReady, pgstate02Diagnostic("STALE")); !errors.Is(err, ErrStaleRevisionTransition) {
		t.Fatalf("stale READY token on a FAILED revision error = %v, want ErrStaleRevisionTransition", err)
	}
	if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", ready.ID); after != postFail {
		t.Fatalf("stale token write landed:\n want %s\n got  %s", postFail, after)
	}
	if _, err := writer.FailRevisionFrom(f.ctx, ready.ID, explorer.RevisionFailed, pgstate02Diagnostic("RESURRECT")); !errors.Is(err, ErrStaleRevisionTransition) {
		t.Fatalf("FAILED->FAILED token error = %v, want ErrStaleRevisionTransition", err)
	}
	if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", ready.ID); after != postFail {
		t.Fatalf("resurrect token write landed:\n want %s\n got  %s", postFail, after)
	}
	if _, err := writer.FailRevision(f.ctx, "rev_never_created", nil); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("missing revision fail error = %v, want ErrNotFound", err)
	}

	// ACTIVE and SUPERSEDED paths: an ACTIVE revision can be superseded by
	// direct legal SQL, after which it can no longer be failed; an ACTIVE
	// revision fails fine on the first attempt.
	active := pgstate02Revision("rev_fail_active", "fail-active")
	active.Status = explorer.RevisionActive
	activated := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	active.ActivatedAt = &activated
	if _, err := writer.InsertRevision(f.ctx, active); err != nil {
		f.fail("insert ACTIVE revision", err)
	}
	failedActive, err := writer.FailRevision(f.ctx, active.ID, pgstate02Diagnostic("RUNTIME_FAILED"))
	if err != nil || failedActive.Status != explorer.RevisionFailed {
		t.Fatalf("fail ACTIVE revision = %+v, %v", failedActive, err)
	}

	superseded := pgstate02Revision("rev_superseded", "superseded")
	superseded.Status = explorer.RevisionActive
	if _, err := writer.InsertRevision(f.ctx, superseded); err != nil {
		f.fail("insert revision to supersede", err)
	}
	if _, err := f.iso.Exec(f.ctx,
		`UPDATE `+f.revisionTable()+` SET status = 'SUPERSEDED' WHERE id = $1 AND status = 'ACTIVE'`,
		superseded.ID); err != nil {
		f.fail("legal ACTIVE->SUPERSEDED transition", err)
	}
	if _, err := writer.FailRevision(f.ctx, superseded.ID, pgstate02Diagnostic("TOO_LATE")); !errors.Is(err, ErrStaleRevisionTransition) {
		t.Fatalf("failing a SUPERSEDED revision error = %v, want ErrStaleRevisionTransition", err)
	}
}

// TestPostgresRevisionStaleTokenRaceTwoConnections is the two-real-
// connection race proof for the lifecycle CAS: connection B (witnessed
// different backend) commits an activation; the losing writer on connection
// A, still holding the stale READY token, is refused by its own CAS guard --
// not by the trigger -- and every witness (row bytes, literal status,
// failed_at NULL) proves it wrote nothing on its backend.
func TestPostgresRevisionStaleTokenRaceTwoConnections(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	storeA, pidA := f.newDedicatedStore(t)
	rawB, pidB := f.newDedicatedPool(t)
	requireDistinctBackends(t, pidA, pidB, "stale writer (store A)", "activator (raw B)")

	revision := pgstate02Revision("rev_race_token", "race")
	if _, err := storeA.InsertRevision(f.ctx, revision); err != nil {
		f.fail("insert revision from backend A", err)
	}

	// The activator commits on backend B.
	activatedAt := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	tag, err := rawB.Exec(f.ctx,
		`UPDATE `+f.revisionTable()+`
		    SET status = 'ACTIVE', activated_at = $2,
		        publication = '{"state":"PUBLISHED","generation":"gen_pg_01"}'::jsonb
		  WHERE id = $1 AND status = 'READY'`, revision.ID, activatedAt)
	if err != nil {
		f.fail("activation on backend B", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("activation affected %d rows, want 1", tag.RowsAffected())
	}
	activatedWitness := f.rowSnapshot(t, f.revisionTable(), "id = $1", revision.ID)

	// The stale writer on backend A still believes READY: typed refusal.
	if _, err := storeA.FailRevisionFrom(f.ctx, revision.ID, explorer.RevisionReady,
		pgstate02Diagnostic("STALE_AFTER_ACTIVATION")); !errors.Is(err, ErrStaleRevisionTransition) {
		t.Fatalf("stale-token FailRevisionFrom after a cross-backend activation error = %v, want ErrStaleRevisionTransition", err)
	}
	if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", revision.ID); after != activatedWitness {
		t.Fatalf("the stale writer on backend A mutated the row the activator committed:\n want %s\n got  %s", activatedWitness, after)
	}
	row := f.revisionLiteralRow(t, revision.ID)
	if row.Status != "ACTIVE" || row.ActivatedAtText != "2026-05-02 12:00:00.000000" || row.FailedAtText != "" {
		t.Fatalf("literal row after refused stale write = %+v", row)
	}

	// The unconditioned FailRevision CAS still admits the now-ACTIVE row
	// (READY->FAILED has passed, ACTIVE->FAILED is legal): the CAS is a
	// state machine, not a one-shot latch.
	if failed, err := storeA.FailRevision(f.ctx, revision.ID, pgstate02Diagnostic("POST_RACE")); err != nil || failed.Status != explorer.RevisionFailed {
		t.Fatalf("post-race legal fail = %+v, %v", failed, err)
	}
}

// TestPostgresRevisionConcurrentFailWritersTwoConnections races two
// backends attempting to fail one READY revision: exactly one wins the CAS
// transition, the other is refused with the stale-token type, and the
// committed diagnostics are the single winner's -- never a merge of both.
func TestPostgresRevisionConcurrentFailWritersTwoConnections(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	storeA, pidA := f.newDedicatedStore(t)
	storeB, pidB := f.newDedicatedStore(t)
	requireDistinctBackends(t, pidA, pidB, "failer A", "failer B")

	revision := pgstate02Revision("rev_race_fail", "race-fail")
	if _, err := storeA.InsertRevision(f.ctx, revision); err != nil {
		f.fail("insert revision", err)
	}

	start := make(chan struct{})
	type failResult struct {
		backend string
		status  explorer.RevisionStatus
		err     error
	}
	var writers sync.WaitGroup
	results := make(chan failResult, 2)
	for i := 0; i < 2; i++ {
		writers.Add(1)
		store, backend := storeA, "A"
		code := "FAILER_A"
		if i == 1 {
			store, backend, code = storeB, "B", "FAILER_B"
		}
		go func() {
			defer writers.Done()
			<-start
			_, err := store.FailRevision(f.ctx, revision.ID, pgstate02Diagnostic(code))
			status := explorer.RevisionStatus("")
			if err == nil {
				status = explorer.RevisionFailed
			}
			results <- failResult{backend: backend, status: status, err: err}
		}()
	}
	close(start)
	writers.Wait()
	close(results)

	wins, refusals := 0, 0
	for result := range results {
		if result.err == nil {
			wins++
			continue
		}
		if errors.Is(result.err, ErrStaleRevisionTransition) {
			refusals++
			continue
		}
		t.Fatalf("unexpected concurrent fail outcome on backend %s: %v", result.backend, result.err)
	}
	if wins != 1 || refusals != 1 {
		t.Fatalf("concurrent fail results: %d winners, %d stale refusals, want 1 and 1", wins, refusals)
	}
	codes := f.columnKinds(t, f.revisionTable(), "id = $1", []any{revision.ID}, "diagnostics")
	if codes[0] != "array" {
		t.Fatalf("diagnostics kind = %s", codes[0])
	}
	var diagnosticsText string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT diagnostics::text FROM `+f.revisionTable()+` WHERE id = $1`, revision.ID).
		Scan(&diagnosticsText); err != nil {
		f.fail("read committed diagnostics", err)
	}
	if strings.Contains(diagnosticsText, "FAILER_A") == strings.Contains(diagnosticsText, "FAILER_B") {
		t.Fatalf("diagnostics are not exactly one winner's: %s", diagnosticsText)
	}
}

// TestPostgresRevisionTransitionMonotonicityProbe pins the DB-enforced
// state machine directly: each legal transition commits in one statement
// with its timestamp witness; every illegal transition -- including the
// double transition FAILED->FAILED with fresh diagnostics, every
// resurrection, un-activation, and same-status mutation -- is refused by
// PostgreSQL with 23514 from the guard (message-proven), leaving the row
// byte-identical.
func TestPostgresRevisionTransitionMonotonicityProbe(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	type probe struct {
		name    string
		setup   explorer.Revision
		set     string
		want    bool // legal transition?
		wantMsg string
	}
	at := func(r explorer.Revision, status explorer.RevisionStatus, withActivated bool) explorer.Revision {
		r.Status = status
		if withActivated {
			stamp := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
			r.ActivatedAt = &stamp
		}
		return r
	}

	probes := []probe{
		{"ready->active", at(pgstate02Revision("tr_ready_active", "a"), explorer.RevisionReady, false),
			`status = 'ACTIVE', activated_at = '2026-05-04T00:00:00Z'`, true, ""},
		{"ready->failed", at(pgstate02Revision("tr_ready_failed", "a"), explorer.RevisionReady, false),
			`status = 'FAILED', failed_at = now()`, true, ""},
		{"active->superseded", at(pgstate02Revision("tr_active_super", "a"), explorer.RevisionActive, true),
			`status = 'SUPERSEDED'`, true, ""},
		{"active->failed", at(pgstate02Revision("tr_active_failed", "a"), explorer.RevisionActive, true),
			`status = 'FAILED', failed_at = now()`, true, ""},
		{"failed->ready resurrection", at(pgstate02Revision("tr_failed_ready", "a"), explorer.RevisionFailed, false),
			`status = 'READY'`, false, "status transition"},
		{"failed->failed double transition", at(pgstate02Revision("tr_failed_failed", "a"), explorer.RevisionFailed, false),
			`status = 'FAILED', diagnostics = '[{"code":"AGAIN","message":"again","severity":"ERROR"}]'::jsonb`,
			false, "status transition"},
		{"failed->active resurrection", at(pgstate02Revision("tr_failed_active", "a"), explorer.RevisionFailed, false),
			`status = 'ACTIVE'`, false, "status transition"},
		{"superseded->active resurrection", at(pgstate02Revision("tr_super_active", "a"), explorer.RevisionSuperseded, true),
			`status = 'ACTIVE'`, false, "status transition"},
		{"superseded->ready resurrection", at(pgstate02Revision("tr_super_ready", "a"), explorer.RevisionSuperseded, true),
			`status = 'READY'`, false, "status transition"},
		{"ready->superseded skip", at(pgstate02Revision("tr_ready_super", "a"), explorer.RevisionReady, false),
			`status = 'SUPERSEDED'`, false, "status transition"},
		{"active->ready un-activation", at(pgstate02Revision("tr_active_ready", "a"), explorer.RevisionActive, true),
			`status = 'READY'`, false, "status transition"},
		{"ready->ready same-status mutation", at(pgstate02Revision("tr_ready_ready", "a"), explorer.RevisionReady, false),
			`status = 'READY', diagnostics = '[{"code":"NOPE","message":"nope","severity":"ERROR"}]'::jsonb`,
			false, "status transition"},
	}
	for _, p := range probes {
		f.rawInsertRevision(t, p.setup)
		before := f.rowSnapshot(t, f.revisionTable(), "id = $1", p.setup.ID)
		_, err := f.iso.Exec(f.ctx,
			"UPDATE "+f.revisionTable()+" SET "+p.set+" WHERE id = $1", p.setup.ID)
		if p.want {
			if err != nil {
				f.fail("legal transition "+p.name, err)
			}
			if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", p.setup.ID); after == before {
				t.Fatalf("legal transition %s changed nothing", p.name)
			}
			continue
		}
		pgErr := requirePgErrPGState02(t, err, "23514", "illegal transition "+p.name)
		if !strings.Contains(pgErr.Message, p.wantMsg) {
			t.Fatalf("illegal transition %s refused by something other than the state machine: %s", p.name, pgErr.Message)
		}
		if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", p.setup.ID); after != before {
			t.Fatalf("refused illegal transition %s mutated the row:\n want %s\n got  %s", p.name, before, after)
		}
	}

	// The one legal timestamp witness: ready->active stored activated_at exactly.
	row := f.revisionLiteralRow(t, "tr_ready_active")
	if row.Status != "ACTIVE" || row.ActivatedAtText != "2026-05-04 00:00:00.000000" {
		t.Fatalf("literal activated_at witness = %+v", row)
	}

	// A true no-op UPDATE (identical values) is harmless and stays legal.
	noop := pgstate02Revision("tr_noop", "a")
	f.rawInsertRevision(t, noop)
	noopBefore := f.rowSnapshot(t, f.revisionTable(), "id = $1", noop.ID)
	tag, err := f.iso.Exec(f.ctx,
		`UPDATE `+f.revisionTable()+` SET status = 'READY' WHERE id = $1 AND status = 'READY'`, noop.ID)
	if err != nil {
		f.fail("no-op UPDATE", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("no-op UPDATE affected %d rows, want 1 (the guard allows identical rows)", tag.RowsAffected())
	}
	if after := f.rowSnapshot(t, f.revisionTable(), "id = $1", noop.ID); after != noopBefore {
		t.Fatalf("no-op UPDATE changed the row:\n want %s\n got  %s", noopBefore, after)
	}
}

// TestPostgresRevisionFrozenGuardProvenNonVacuous is the accepted
// non-vacuity bar for the frozen columns: every direct UPDATE of each
// frozen column is refused with 23514 from the frozen guard (message-
// proven), and the same statement -- including a legal transition smuggled
// together with a frozen mutation -- SUCCEEDS only while the trigger is
// live-dropped, with literal witnesses proving the row really changed,
// before the guard is restored from the migration's exact text.
func TestPostgresRevisionFrozenGuardProvenNonVacuous(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	validDigest := "sha256:" + strings.Repeat("a", 64)
	revision := pgstate02Revision("rev_frozen_probe", "frozen")
	f.rawInsertRevision(t, revision)

	mutations := []struct {
		column string
		set    string
	}{
		{"id", "'rev_intruder'"},
		{"project", "'proj_intruder'"},
		{"explorer_id", "'exp_intruder'"},
		{"config", `'{"mutated": true}'::jsonb`},
		{"authoring_bundle", `'{"mutated": true}'::jsonb`},
		{"public_output_contract", `'{"mutated": true}'::jsonb`},
		{"intent_digest", "'sha256:intruder'"},
		{"compilation_receipt_id", "'receipt_intruder'"},
		{"recipe", `'{"name":"intruder"}'::jsonb`},
		{"recipe_digest", "'sha256:intruder'"},
		{"resolved_schema_digest", "'sha256:intruder'"},
		{"source_generation", "'gen_intruder'"},
		{"materializations", `'[]'::jsonb`},
		{"emitted_columns", `'[]'::jsonb`},
		{"dataset", `'{"generation":"gen_intruder","outputs":[]}'::jsonb`},
		{"quality_reports", `'[{"id":"q_intruder"}]'::jsonb`},
		{"content_digest", "'" + validDigest + "'"},
		{"created_by", "'intruder@calypr'"},
		{"created_at", "'2030-01-01T00:00:00Z'"},
	}
	for _, m := range mutations {
		_, err := f.iso.Exec(f.ctx,
			"UPDATE "+f.revisionTable()+" SET "+m.column+" = "+m.set+" WHERE id = $1", revision.ID)
		pgErr := requirePgErrPGState02(t, err, "23514", "frozen revision column "+m.column)
		if !strings.Contains(pgErr.Message, "frozen content changed") {
			t.Fatalf("frozen refusal on %s came from something other than the guard: %s", m.column, pgErr.Message)
		}
	}
	// A legal transition smuggled together with a frozen mutation is
	// refused by the frozen guard, not the state machine.
	_, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.revisionTable()+" SET status = 'FAILED', recipe_digest = 'sha256:smuggled' WHERE id = $1",
		revision.ID)
	pgErr := requirePgErrPGState02(t, err, "23514", "smuggled frozen mutation during legal transition")
	if !strings.Contains(pgErr.Message, "frozen content changed") {
		t.Fatalf("smuggled mutation refused by the wrong guard: %s", pgErr.Message)
	}

	// Guard removal: the identical violating writes now succeed, with
	// literal witnesses proving the row actually changed.
	if _, err := f.iso.Exec(f.ctx,
		`DROP TRIGGER explorer_revisions_guard ON `+f.revisionTable()); err != nil {
		f.fail("drop revision guard", err)
	}
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.revisionTable()+" SET recipe_digest = 'sha256:unfrozen' WHERE id = $1", revision.ID); err != nil {
		f.fail("frozen mutation without the trigger must succeed", err)
	}
	var mutated string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT recipe_digest FROM `+f.revisionTable()+` WHERE id = $1`, revision.ID).Scan(&mutated); err != nil {
		f.fail("read mutated recipe_digest", err)
	}
	if mutated != "sha256:unfrozen" {
		t.Fatalf("guard-removal probe did not persist: recipe_digest = %q", mutated)
	}
	// The resurrection that the state machine refuses also lands unfrozen.
	resurrect := pgstate02Revision("rev_frozen_resurrect", "frozen")
	resurrect.Status = explorer.RevisionFailed
	failedAt := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	resurrect.FailedAt = &failedAt
	f.rawInsertRevision(t, resurrect)
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.revisionTable()+" SET status = 'READY' WHERE id = $1", resurrect.ID); err != nil {
		f.fail("resurrection without the trigger must succeed", err)
	}
	if row := f.revisionLiteralRow(t, resurrect.ID); row.Status != "READY" {
		t.Fatalf("guard-removal resurrection did not persist: status = %q", row.Status)
	}

	// Restore the guard from the migration's own text: identical writes are
	// refused again.
	f.rerunMigration(t, 6)
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.revisionTable()+" SET recipe_digest = 'sha256:refrozen' WHERE id = $1", revision.ID); err == nil {
		t.Fatal("frozen mutation accepted after the guard was restored from the migration text")
	} else {
		pgErr := requirePgErrPGState02(t, err, "23514", "restored frozen guard")
		if !strings.Contains(pgErr.Message, "frozen content changed") {
			t.Fatalf("restored guard refusal came from something else: %s", pgErr.Message)
		}
	}
	// A freshly inserted terminal row cannot be resurrected once the state
	// machine is restored (the earlier resurrect row is now READY, so
	// FAILED->READY must be probed on a clean FAILED row).
	restoredProbe := pgstate02Revision("rev_frozen_restored", "frozen")
	restoredProbe.Status = explorer.RevisionFailed
	f.rawInsertRevision(t, restoredProbe)
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.revisionTable()+" SET status = 'READY' WHERE id = $1", restoredProbe.ID); err == nil {
		t.Fatal("resurrection accepted after the guard was restored")
	} else {
		pgErr := requirePgErrPGState02(t, err, "23514", "restored state machine")
		if !strings.Contains(pgErr.Message, "status transition") {
			t.Fatalf("restored state-machine refusal came from something else: %s", pgErr.Message)
		}
	}
}

// TestPostgresRevisionSchemaConstraints proves the CHECK/NOT NULL guards are
// enforced by PostgreSQL itself on raw INSERTs, and the rejected statements
// persist nothing.
func TestPostgresRevisionSchemaConstraints(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	rawInsert := "INSERT INTO " + f.revisionTable() + " (" +
		strings.Join(revisionColumns, ", ") + ") VALUES (" +
		orderedPlaceholders(len(revisionColumns)) + ")"

	variants := []struct {
		name     string
		sqlstate string
		mutate   func([]any) []any
	}{
		{"unknown status", "23514", func(a []any) []any { a[17] = "SOMETHING_ELSE"; return a }},
		{"digest shape", "23514", func(a []any) []any { a[16] = "not-a-digest"; return a }},
		{"recipe scalar", "23514", func(a []any) []any { a[8] = []byte("5"); return a }},
		{"materializations map", "23514", func(a []any) []any { a[12] = []byte("{}"); return a }},
		{"quality scalar", "23514", func(a []any) []any { a[15] = []byte("42"); return a }},
		{"diagnostics object", "23514", func(a []any) []any { a[18] = []byte("{}"); return a }},
		{"dataset not null", "23502", func(a []any) []any { a[14] = nil; return a }},
		{"publication not null", "23502", func(a []any) []any { a[19] = nil; return a }},
	}
	for _, variant := range variants {
		args, _, err := pgstate02RevisionArgs(pgstate02Revision("rev_constraint_"+strings.ReplaceAll(variant.name, " ", "_"), "c"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.iso.Exec(f.ctx, rawInsert, variant.mutate(args)...)
		requirePgErrPGState02(t, err, variant.sqlstate, "constraint variant: "+variant.name)
	}

	// Non-JSON text in the tri-state jsonb boundary is refused at the wire.
	args, _, err := pgstate02RevisionArgs(pgstate02Revision("rev_constraint_notjson", "c"))
	if err != nil {
		t.Fatal(err)
	}
	args[3] = "this is not json"
	_, err = f.iso.Exec(f.ctx, rawInsert, args...)
	requirePgErrPGState02(t, err, "22P02", "non-JSON config payload")

	if n := f.revisionCount(t, revisionProject); n != 0 {
		t.Fatalf("rejected raw inserts left %d rows, want 0", n)
	}
}

// TestPostgresRevisionJSONTriStateFreshRead proves missing versus JSON-null
// versus document for the three json.RawMessage payloads, and missing
// versus empty for the slice payloads, survive a fresh DB read: each stored
// shape is witnessed literally (SQL NULL 'missing', the jsonb value 'null',
// the array '[]', or the object), and each reads back to the exact Go
// state through a brand-new pool.
func TestPostgresRevisionJSONTriStateFreshRead(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	missing := pgstate02Revision("rev_tri_missing", "tri")
	missing.Config = nil
	missing.AuthoringBundle = nil
	missing.PublicOutputContract = nil
	missing.Materializations = nil
	missing.EmittedColumns = nil

	jsonNull := pgstate02Revision("rev_tri_null", "tri")
	jsonNull.Config = json.RawMessage("null")
	jsonNull.AuthoringBundle = json.RawMessage("null")
	jsonNull.PublicOutputContract = json.RawMessage("null")

	empty := pgstate02Revision("rev_tri_empty", "tri")
	empty.Materializations = []explorer.Materialization{}
	empty.EmittedColumns = []explorer.EmittedColumn{}
	empty.QualityReports = []publication.QualityReport{}
	empty.Diagnostics = []explorer.Diagnostic{}

	for _, revision := range []explorer.Revision{missing, jsonNull, empty} {
		if _, err := writer.InsertRevision(f.ctx, revision); err != nil {
			f.fail("insert tri-state revision "+revision.ID, err)
		}
	}

	// Literal storage-shape witnesses.
	for _, tc := range []struct {
		id     string
		config string
		mats   string
		diag   string
	}{
		{"rev_tri_missing", "missing", "missing", "missing"},
		{"rev_tri_null", "null", "array", "missing"},
		{"rev_tri_empty", "object", "array", "array"},
	} {
		kinds := f.columnKinds(t, f.revisionTable(), "id = $1", []any{tc.id},
			"config", "materializations", "diagnostics")
		if kinds[0] != tc.config || kinds[1] != tc.mats || kinds[2] != tc.diag {
			t.Fatalf("%s stored kinds = %v, want [%s %s %s]", tc.id, kinds, tc.config, tc.mats, tc.diag)
		}
	}
	var emptyMatsText, emptyDiagText string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT materializations::text, diagnostics::text FROM `+f.revisionTable()+` WHERE id = 'rev_tri_empty'`).
		Scan(&emptyMatsText, &emptyDiagText); err != nil {
		f.fail("read empty-array literals", err)
	}
	if emptyMatsText != "[]" || emptyDiagText != "[]" {
		t.Fatalf("empty slices stored as %q/%q, want [] / []", emptyMatsText, emptyDiagText)
	}

	reader := f.newStore(t)
	gotMissing, err := reader.GetRevision(f.ctx, "rev_tri_missing")
	if err != nil {
		f.fail("read missing-payload revision", err)
	}
	if gotMissing.Config != nil || gotMissing.AuthoringBundle != nil || gotMissing.PublicOutputContract != nil {
		t.Fatalf("missing payloads revived: config=%q authoring=%q contract=%q",
			gotMissing.Config, gotMissing.AuthoringBundle, gotMissing.PublicOutputContract)
	}
	if gotMissing.Materializations != nil || gotMissing.EmittedColumns != nil {
		t.Fatalf("missing slices revived: %+v / %+v", gotMissing.Materializations, gotMissing.EmittedColumns)
	}
	gotNull, err := reader.GetRevision(f.ctx, "rev_tri_null")
	if err != nil {
		f.fail("read json-null revision", err)
	}
	if string(gotNull.Config) != "null" || string(gotNull.AuthoringBundle) != "null" ||
		string(gotNull.PublicOutputContract) != "null" {
		t.Fatalf("JSON-null payloads lost: config=%q authoring=%q contract=%q",
			gotNull.Config, gotNull.AuthoringBundle, gotNull.PublicOutputContract)
	}
	gotEmpty, err := reader.GetRevision(f.ctx, "rev_tri_empty")
	if err != nil {
		f.fail("read empty-payload revision", err)
	}
	if gotEmpty.Materializations == nil || len(gotEmpty.Materializations) != 0 ||
		gotEmpty.EmittedColumns == nil || len(gotEmpty.EmittedColumns) != 0 ||
		gotEmpty.Diagnostics == nil || len(gotEmpty.Diagnostics) != 0 ||
		gotEmpty.QualityReports == nil || len(gotEmpty.QualityReports) != 0 {
		t.Fatalf("empty slices collapsed to nil: %+v", gotEmpty)
	}
}

// TestPostgresExplorerStateMigrationsRerunConverges proves the extended
// history (ledger + owner + receipts + revisions) reruns as a no-op that
// preserves all state and keeps the ledger pinned to the four embedded
// checksums.
func TestPostgresExplorerStateMigrationsRerunConverges(t *testing.T) {
	f := newPGState02Fixture(t)
	first := f.migrate(t)
	if len(first.Applied) != 4 || first.LatestVersion != 6 {
		t.Fatalf("first migration result = %+v, want 4 ledger rows ending at version 6", first)
	}

	writer := f.newStore(t)
	owner := fullOwner("proj_state", "exp_state")
	if _, err := writer.Create(f.ctx, owner); err != nil {
		f.fail("create owner", err)
	}
	receipt := pgstate02Receipt(t, "intent-rerun", receiptProject, receiptExplorer)
	if _, err := writer.InsertCompilationReceipt(f.ctx, receipt); err != nil {
		f.fail("insert receipt", err)
	}
	revision := pgstate02Revision("rev_rerun", "rerun")
	if _, err := writer.InsertRevision(f.ctx, revision); err != nil {
		f.fail("insert revision", err)
	}

	second := f.migrate(t)
	if len(second.Applied) != 4 || second.LatestVersion != 6 {
		t.Fatalf("rerun result = %+v, want the identical 4-row ledger ending at version 6 (all recorded, nothing re-run)", second)
	}

	rows, err := f.iso.Query(f.ctx, `SELECT version, name, checksum FROM `+
		sanitizeTableRef(f.schema, "schema_migrations")+` ORDER BY version`)
	if err != nil {
		f.fail("read ledger", err)
	}
	type ledgerRow struct {
		version  int64
		name     string
		checksum string
	}
	var ledger []ledgerRow
	for rows.Next() {
		var rec ledgerRow
		if err := rows.Scan(&rec.version, &rec.name, &rec.checksum); err != nil {
			rows.Close()
			f.fail("scan ledger", err)
		}
		ledger = append(ledger, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		f.fail("read ledger", err)
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger) != len(migrations) {
		t.Fatalf("ledger holds %d rows, want %d", len(ledger), len(migrations))
	}
	for i, want := range migrations {
		if ledger[i] != (ledgerRow{want.Version, want.Name, want.Checksum}) {
			t.Fatalf("ledger[%d] = %+v, want %+v", i, ledger[i], want)
		}
	}

	// State survived every rerun, through brand-new pools.
	reader := f.newStore(t)
	if _, err := reader.Get(f.ctx, "proj_state", "exp_state"); err != nil {
		f.fail("owner after migrate reruns", err)
	}
	if _, err := reader.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, receipt.ID); err != nil {
		f.fail("receipt after migrate reruns", err)
	}
	if _, err := reader.GetRevision(f.ctx, revision.ID); err != nil {
		f.fail("revision after migrate reruns", err)
	}
}

// TestZZNoLeftoverPGState02Resources runs last in this package's file order
// and audits that every fixture name in the pgstate02_it_* namespace this
// ticket owns is gone. The audit is scoped to this prefix -- never a global
// scan -- so sibling packages' and sibling workers' concurrent fixtures in
// other namespaces cannot make this fail spuriously.
func TestZZNoLeftoverPGState02Resources(t *testing.T) {
	raw := pgstate02TestPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	var dbLeftovers []string
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'pgstate02\_it\_%' ESCAPE '\' ORDER BY datname`)
	if err != nil {
		t.Fatalf("audit isolated databases: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("audit isolated databases: %v", err)
		}
		dbLeftovers = append(dbLeftovers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("audit isolated databases: %v", err)
	}
	if len(dbLeftovers) != 0 {
		t.Errorf("leftover isolated databases owned by this ticket: %v", dbLeftovers)
	}

	var schemaLeftovers []string
	rows, err = admin.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'pgstate02\_it\_%' ESCAPE '\' ORDER BY nspname`)
	if err != nil {
		t.Fatalf("audit owned schemas: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("audit owned schemas: %v", err)
		}
		schemaLeftovers = append(schemaLeftovers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("audit owned schemas: %v", err)
	}
	if len(schemaLeftovers) != 0 {
		t.Errorf("leftover owned schemas owned by this ticket: %v", schemaLeftovers)
	}
}
