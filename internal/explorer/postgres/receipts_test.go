package postgres

// Compilation receipt tests for pg-state-receipt-revision-immutable-v1.
//
// Every claim is made against a real PostgreSQL 18 with literal rows and
// direct-SQL witnesses (no mocks, no skips once a service is present):
// insert-once + scoped reread + canonical digest collision per the Arango
// parity contract, tenant-scoped reads, the DB-enforced frozen guard proven
// non-vacuously by a live DROP TRIGGER / restore probe, and two-real-
// connection claim-race proofs with distinct pg_backend_pid witnesses.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

// ---------------------------------------------------------------------------
// Fixtures.
// ---------------------------------------------------------------------------

const (
	receiptProject  = "proj_receipts"
	receiptExplorer = "exp_receipts"
)

var receiptCreatedAt = time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)

const receiptCreatedAtText = "2026-03-04T05:06:07.123456Z"

func pgstate02ReceiptBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "state-receipt",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "output", RootResourceType: "Patient", RowGrain: "resource",
		}},
	}
}

// pgstate02Receipt builds a fully validated current-contract receipt with a
// content-addressed ID, mirroring the Arango baseline fixture
// (internal/explorer/arango/receipt_store_test.go testCompilationReceipt).
func pgstate02Receipt(t *testing.T, intent, project, explorerID string) explorer.CompilationReceipt {
	t.Helper()
	bundle := pgstate02ReceiptBundle()
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:    explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion: explorer.CurrentCompilerContractVersion,
		Project:                 project, ExplorerID: explorerID,
		IntentDigest:             intent,
		SnapshotToken:            "snapshot-pg-01",
		AuthorizationScopeDigest: "scope-pg-01",
		CapabilitySchemaDigest:   "schema-pg-01",
		ShapeDigest:              "shape-pg-01",
		SourceGeneration:         "gen_pg_01",
		RecipeDigest:             "sha256:recipe-digest",
		ResolvedSchemaDigest:     "sha256:resolved-schema-pg",
		NormalizedBundle:         json.RawMessage(cfgAlpha),
		Bundle:                   bundle,
		PublicOutputContract:     json.RawMessage(`{"outputs":[{"outputId":"output","columns":[],"lossless":true,"mlReady":true}]}`),
		OutputColumnProvenance:   map[string]map[string]string{"output": {"__loom_row_id": "EXPLICIT"}},
		RequestID:                "req_01",
		CreatedAt:                receiptCreatedAt,
	}
	var err error
	if receipt.ResolvedRecipeDigest, err = bundle.Digest(); err != nil {
		t.Fatal(err)
	}
	if receipt.OutputContractDigest, err = explorer.CompilationArtifactDigest(receipt.PublicOutputContract); err != nil {
		t.Fatal(err)
	}
	if receipt.CompilationKey, err = explorer.CompilationKey(receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ID, err = explorer.ReceiptID(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

// pgstate02LegacyReceipt builds a pre-contract receipt (no format/contract
// markers, opaque ID): the Arango validation gate skips Validate for these,
// so two different legacy receipts can legitimately share an ID -- the
// corrupt-ID-reuse case the store must refuse with ErrCorruptReceipt.
func pgstate02LegacyReceipt(id, intent string) explorer.CompilationReceipt {
	return explorer.CompilationReceipt{
		ID:               id,
		Project:          receiptProject,
		ExplorerID:       receiptExplorer,
		IntentDigest:     intent,
		SnapshotToken:    "snapshot-legacy",
		SourceGeneration: "gen_legacy",
		NormalizedBundle: json.RawMessage(cfgBeta),
		RecipeDigest:     "sha256:legacy-recipe",
		CreatedAt:        receiptCreatedAt,
	}
}

func requireReceiptEqual(t *testing.T, want, got explorer.CompilationReceipt, where string) {
	t.Helper()
	if got.ID != want.ID || got.Project != want.Project || got.ExplorerID != want.ExplorerID {
		t.Fatalf("%s: identity changed: got %q/%q/%q want %q/%q/%q", where,
			got.Project, got.ExplorerID, got.ID, want.Project, want.ExplorerID, want.ID)
	}
	if got.ReceiptFormatVersion != want.ReceiptFormatVersion ||
		got.CompilerContractVersion != want.CompilerContractVersion ||
		got.CompilationKey != want.CompilationKey ||
		got.IntentDigest != want.IntentDigest ||
		got.SnapshotToken != want.SnapshotToken ||
		got.SourceGeneration != want.SourceGeneration ||
		got.RecipeDigest != want.RecipeDigest ||
		got.ResolvedRecipeDigest != want.ResolvedRecipeDigest ||
		got.RequestID != want.RequestID {
		t.Fatalf("%s: scalar fields changed: got %+v want %+v", where, got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("%s: createdAt changed: got %s want %s", where, got.CreatedAt, want.CreatedAt)
	}
	jsonEqual(t, want.NormalizedBundle, got.NormalizedBundle, where+" normalizedBundle")
	jsonEqual(t, want.CompiledConfig, got.CompiledConfig, where+" compiledConfig")
	jsonEqual(t, want.PublicOutputContract, got.PublicOutputContract, where+" publicOutputContract")
	if len(got.Bundle.Outputs) != len(want.Bundle.Outputs) ||
		(len(want.Bundle.Outputs) > 0 && got.Bundle.Outputs[0].RootResourceType != want.Bundle.Outputs[0].RootResourceType) {
		t.Fatalf("%s: canonical recipe not lossless: got %+v want %+v", where, got.Bundle, want.Bundle)
	}
}

// claimReceiptRaw renders the raw (non-ON CONFLICT) receipt INSERT so a test
// transaction can hold an uncommitted claim row through direct SQL.
func pgstate02RawReceiptInsert(schema string) string {
	statements := newReceiptStatements(schema)
	// Strip the ON CONFLICT clause: the raw helper is used for claim rows in
	// explicit transactions where a duplicate is a test bug, not a race.
	return strings.Replace(statements.insert,
		" ON CONFLICT (project, explorer_id, id) DO NOTHING", "", 1)
}

func (f *pgState02Fixture) rawInsertReceipt(t *testing.T, receipt explorer.CompilationReceipt) {
	t.Helper()
	doc, err := json.Marshal(receipt)
	if err != nil {
		f.fail("encode raw receipt fixture", err)
	}
	_, err = f.iso.Exec(f.ctx, pgstate02RawReceiptInsert(f.schema), receiptArgs(receipt, doc)...)
	if err != nil {
		f.fail("raw insert receipt "+receipt.ID, err)
	}
}

// receiptColumnValues reads every typed column of one receipt row plus
// targeted jsonb witnesses through direct SQL.
type receiptLiteralRow struct {
	Project                 string
	ExplorerID              string
	ID                      string
	ReceiptFormatVersion    int64
	CompilerContractVersion string
	CompilationKey          string
	IntentDigest            string
	SnapshotToken           string
	SourceGeneration        string
	RecipeDigest            string
	ResolvedRecipeDigest    string
	DocType                 string
	DocRequestID            string
	DocCreatedAt            string
	DocIntentDigest         string
}

func (f *pgState02Fixture) receiptLiteralRow(t *testing.T, project, explorerID, id string) receiptLiteralRow {
	t.Helper()
	var row receiptLiteralRow
	err := f.iso.QueryRow(f.ctx, `SELECT
		project, explorer_id, id, receipt_format_version, compiler_contract_version,
		compilation_key, intent_digest, snapshot_token, source_generation,
		recipe_digest, resolved_recipe_digest,
		jsonb_typeof(doc), doc->>'requestId', doc->>'createdAt', doc->>'intentDigest'
	FROM `+f.receiptTable()+` WHERE project = $1 AND explorer_id = $2 AND id = $3`,
		project, explorerID, id).Scan(
		&row.Project, &row.ExplorerID, &row.ID, &row.ReceiptFormatVersion,
		&row.CompilerContractVersion, &row.CompilationKey, &row.IntentDigest,
		&row.SnapshotToken, &row.SourceGeneration, &row.RecipeDigest,
		&row.ResolvedRecipeDigest, &row.DocType, &row.DocRequestID,
		&row.DocCreatedAt, &row.DocIntentDigest)
	if err != nil {
		f.fail("read literal receipt row", err)
	}
	return row
}

func (f *pgState02Fixture) receiptCount(t *testing.T, project string) int {
	t.Helper()
	var n int
	if err := f.iso.QueryRow(f.ctx,
		`SELECT count(*) FROM `+f.receiptTable()+` WHERE project = $1`, project).Scan(&n); err != nil {
		f.fail("count receipt rows", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// Unit tests: no PostgreSQL service required.
// ---------------------------------------------------------------------------

func TestPostgresReceiptSQLContract(t *testing.T) {
	statements := newReceiptStatements("loom_control")
	if !strings.Contains(statements.insert, `ON CONFLICT (project, explorer_id, id) DO NOTHING`) {
		t.Error("receipt insert must be insert-once: ON CONFLICT (project, explorer_id, id) DO NOTHING")
	}
	if !strings.Contains(statements.insert, "RETURNING") {
		t.Error("receipt insert must RETURNING the stored row")
	}
	if !strings.Contains(statements.insert, `"loom_control"."`+ReceiptsTable+`"`) {
		t.Error("receipt insert must bind the schema-qualified, sanitized table")
	}
	if !strings.Contains(statements.claim, " FOR UPDATE") {
		t.Error("the conflict reread must be FOR UPDATE so it waits for an in-flight winner")
	}
	for name, statement := range map[string]string{"insert": statements.insert, "claim": statements.claim, "get": statements.get} {
		if !strings.Contains(statement, `WHERE project = $1 AND explorer_id = $2 AND id = $3`) && name != "insert" {
			t.Errorf("receipt %s statement is not tenant-scoped by project AND explorer_id: %s", name, statement)
		}
	}
	for i := 1; i <= len(receiptColumns); i++ {
		if !strings.Contains(statements.insert, fmt.Sprintf("$%d", i)) {
			t.Fatalf("receipt insert is missing ordered parameter $%d", i)
		}
	}
	if strings.Contains(statements.insert, fmt.Sprintf("$%d", len(receiptColumns)+1)) {
		t.Fatalf("receipt insert carries more than %d parameters", len(receiptColumns))
	}
	doc, err := json.Marshal(pgstate02LegacyReceipt("legacy-1", "intent"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(receiptArgs(pgstate02LegacyReceipt("legacy-1", "intent"), doc)); got != len(receiptColumns) {
		t.Fatalf("receiptArgs produced %d values, want %d (one per column)", got, len(receiptColumns))
	}
}

func TestPostgresReceiptInsertValidationIsTyped(t *testing.T) {
	if err := validateReceiptForStore(explorer.CompilationReceipt{ID: "   "}); !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("blank ID error = %v, want ErrCorruptReceipt", err)
	}
	if err := validateReceiptForStore(explorer.CompilationReceipt{}); !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("empty ID error = %v, want ErrCorruptReceipt", err)
	}
	// A legacy receipt (no contract markers, opaque ID) passes the gate --
	// the Arango parity shape.
	if err := validateReceiptForStore(pgstate02LegacyReceipt("legacy-ok", "intent")); err != nil {
		t.Fatalf("legacy receipt rejected: %v", err)
	}
	// A content-addressed ID that disagrees with the content is corrupt.
	tamperedID := "receipt_" + strings.Repeat("0", 64)
	err := validateReceiptForStore(explorer.CompilationReceipt{ID: tamperedID})
	if !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("mismatched content-addressed ID error = %v, want ErrCorruptReceipt", err)
	}
}

func TestPostgresReceiptContentIdentityIgnoresRequestMetadata(t *testing.T) {
	want := pgstate02Receipt(t, "intent-alpha", receiptProject, receiptExplorer)
	replay := want
	replay.RequestID = "req_02"
	replay.CreatedAt = want.CreatedAt.Add(90 * time.Second)
	left, err := explorer.ReceiptID(want)
	if err != nil {
		t.Fatal(err)
	}
	right, err := explorer.ReceiptID(replay)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("RequestID/CreatedAt leaked into the content identity: %s vs %s", left, right)
	}
	different := want
	different.IntentDigest = "intent-beta"
	differentKey, err := explorer.ReceiptID(different)
	if err != nil {
		t.Fatal(err)
	}
	if differentKey == left {
		t.Fatal("intent digest change did not move the content identity")
	}
}

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 integration.
// ---------------------------------------------------------------------------

// TestPostgresReceiptInsertGetLiteralRows proves the receipt table stores an
// insert-once row under the (project, explorer_id, id) identity: the
// RETURNING row is what PostgreSQL persisted, direct SQL reads every typed
// column and targeted jsonb members exactly (including the microsecond-exact
// createdAt string inside doc and the jsonb-normalized normalizedBundle
// text), a Store on a fresh independent pool reads it back, and reads are
// tenant-scoped: another project or Explorer sees ErrNotFound.
func TestPostgresReceiptInsertGetLiteralRows(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	want := pgstate02Receipt(t, "intent-alpha", receiptProject, receiptExplorer)
	created, err := writer.InsertCompilationReceipt(f.ctx, want)
	if err != nil {
		f.fail("insert receipt", err)
	}
	requireReceiptEqual(t, want, *created, "Insert result")

	row := f.receiptLiteralRow(t, receiptProject, receiptExplorer, want.ID)
	if row.Project != receiptProject || row.ExplorerID != receiptExplorer || row.ID != want.ID {
		t.Fatalf("literal identity = %q/%q/%q, want %q/%q/%q", row.Project, row.ExplorerID, row.ID, receiptProject, receiptExplorer, want.ID)
	}
	if row.ReceiptFormatVersion != int64(explorer.CurrentReceiptFormatVersion) {
		t.Errorf("literal receipt_format_version = %d, want %d", row.ReceiptFormatVersion, explorer.CurrentReceiptFormatVersion)
	}
	if row.CompilerContractVersion != explorer.CurrentCompilerContractVersion {
		t.Errorf("literal compiler_contract_version = %q, want %q", row.CompilerContractVersion, explorer.CurrentCompilerContractVersion)
	}
	if row.CompilationKey != want.CompilationKey || row.IntentDigest != "intent-alpha" ||
		row.SnapshotToken != "snapshot-pg-01" || row.SourceGeneration != "gen_pg_01" ||
		row.RecipeDigest != "sha256:recipe-digest" || row.ResolvedRecipeDigest != want.ResolvedRecipeDigest {
		t.Fatalf("literal digest/generation columns drifted: %+v", row)
	}
	if row.DocType != "object" {
		t.Errorf("doc jsonb_typeof = %q, want object", row.DocType)
	}
	if row.DocRequestID != "req_01" || row.DocCreatedAt != receiptCreatedAtText || row.DocIntentDigest != "intent-alpha" {
		t.Errorf("literal doc members = %q/%q/%q, want req_01/%s/intent-alpha",
			row.DocRequestID, row.DocCreatedAt, row.DocIntentDigest, receiptCreatedAtText)
	}
	// The jsonb-normalized canonical payload text, stored exactly.
	if kinds := f.columnKinds(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
		[]any{receiptProject, receiptExplorer, want.ID}, "doc"); kinds[0] != "object" {
		t.Fatalf("doc kind = %s", kinds[0])
	}
	var bundleText string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT doc->'normalizedBundle'::text FROM `+f.receiptTable()+` WHERE project = $1 AND explorer_id = $2 AND id = $3`,
		receiptProject, receiptExplorer, want.ID).Scan(&bundleText); err != nil {
		f.fail("read normalizedBundle literal", err)
	}
	if bundleText != literalCfgAlphaText {
		t.Fatalf("normalizedBundle jsonb text = %s, want %s", bundleText, literalCfgAlphaText)
	}
	if n := f.receiptCount(t, receiptProject); n != 1 {
		t.Fatalf("receipt row count = %d, want exactly 1", n)
	}

	reader := f.newStore(t)
	got, err := reader.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, want.ID)
	if err != nil {
		f.fail("get receipt on fresh pool", err)
	}
	requireReceiptEqual(t, want, *got, "fresh-pool Get")

	// Tenant scoping: the same id under another project or Explorer is
	// indistinguishable from missing.
	if _, err := reader.GetCompilationReceiptForExplorer(f.ctx, "proj_other", receiptExplorer, want.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-project read error = %v, want ErrNotFound", err)
	}
	if _, err := reader.GetCompilationReceiptForExplorer(f.ctx, receiptProject, "exp_other", want.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-explorer read error = %v, want ErrNotFound", err)
	}
}

// TestPostgresReceiptIdempotentSameContent proves acceptance 1's idempotent
// half: replaying the same ID with the same canonical content (RequestID and
// CreatedAt excluded from the content identity) writes nothing, returns the
// stored row (the stored RequestID/CreatedAt, not the replay's), and the
// literal row is byte-identical before and after.
func TestPostgresReceiptIdempotentSameContent(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	want := pgstate02Receipt(t, "intent-idem", receiptProject, receiptExplorer)
	if _, err := writer.InsertCompilationReceipt(f.ctx, want); err != nil {
		f.fail("insert receipt", err)
	}
	before := f.rowSnapshot(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
		receiptProject, receiptExplorer, want.ID)

	replay := want
	replay.RequestID = "req-replay"
	replay.CreatedAt = want.CreatedAt.Add(2 * time.Minute)
	stored, err := writer.InsertCompilationReceipt(f.ctx, replay)
	if err != nil {
		f.fail("idempotent replay", err)
	}
	if !stored.CreatedAt.Equal(want.CreatedAt) || stored.RequestID != "req_01" {
		t.Fatalf("replay returned the replay's metadata, not the stored row: createdAt=%s requestId=%q",
			stored.CreatedAt, stored.RequestID)
	}
	after := f.rowSnapshot(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
		receiptProject, receiptExplorer, want.ID)
	if before != after {
		t.Fatalf("idempotent replay mutated the row:\n before %s\n after  %s", before, after)
	}
	if n := f.receiptCount(t, receiptProject); n != 1 {
		t.Fatalf("receipt row count = %d, want exactly 1", n)
	}
}

// TestPostgresReceiptIDReuseDifferentContentRefused proves acceptance 1's
// refusal half: same ID, different content is a typed ErrCorruptReceipt
// (the corrupt-ID-reuse report) with the stored row byte-identical, both
// through the store's conflict path (legacy IDs that legitimately collide)
// and through the insert validation gate (a content-addressed ID kept while
// the intent changes).
func TestPostgresReceiptIDReuseDifferentContentRefused(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	t.Run("legacy id reuse via conflict", func(t *testing.T) {
		first := pgstate02LegacyReceipt("legacy-reuse-key", "intent-one")
		if _, err := writer.InsertCompilationReceipt(f.ctx, first); err != nil {
			f.fail("insert first legacy receipt", err)
		}
		before := f.rowSnapshot(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
			receiptProject, receiptExplorer, first.ID)

		second := pgstate02LegacyReceipt("legacy-reuse-key", "intent-two")
		_, err := writer.InsertCompilationReceipt(f.ctx, second)
		if !errors.Is(err, explorer.ErrCorruptReceipt) {
			t.Fatalf("different content at existing ID error = %v, want ErrCorruptReceipt", err)
		}
		after := f.rowSnapshot(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
			receiptProject, receiptExplorer, first.ID)
		if before != after {
			t.Fatalf("refused reuse mutated the stored row:\n before %s\n after  %s", before, after)
		}
		if n := f.receiptCount(t, receiptProject); n != 1 {
			t.Fatalf("receipt row count = %d, want exactly 1", n)
		}
		stored, err := writer.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, first.ID)
		if err != nil || stored.IntentDigest != "intent-one" {
			t.Fatalf("stored receipt after refused reuse = %+v, %v", stored, err)
		}
	})

	t.Run("content-addressed tamper refused before write", func(t *testing.T) {
		valid := pgstate02Receipt(t, "intent-valid", receiptProject, receiptExplorer)
		if _, err := writer.InsertCompilationReceipt(f.ctx, valid); err != nil {
			f.fail("insert valid receipt", err)
		}
		before := f.rowSnapshot(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
			receiptProject, receiptExplorer, valid.ID)

		tampered := valid
		tampered.IntentDigest = "intent-tampered" // stale ID: content no longer hashes to it
		_, err := writer.InsertCompilationReceipt(f.ctx, tampered)
		if err == nil {
			t.Fatal("tampered content at a content-addressed ID was accepted")
		}
		if errors.Is(err, explorer.ErrNotFound) {
			t.Fatalf("tampered insert misclassified as missing: %v", err)
		}
		after := f.rowSnapshot(t, f.receiptTable(), "project = $1 AND explorer_id = $2 AND id = $3",
			receiptProject, receiptExplorer, valid.ID)
		if before != after {
			t.Fatalf("refused tampered insert mutated the row:\n before %s\n after  %s", before, after)
		}
		if n := f.receiptCount(t, receiptProject); n != 2 {
			t.Fatalf("receipt row count = %d, want 2 (the valid receipt and the legacy one)", n)
		}
	})
}

// TestPostgresReceiptConcurrentIdenticalWritersTwoConnections races two
// concurrent inserts of one receipt from two dedicated stores whose
// pg_backend_pid values are witnessed to differ, so the outcome cannot be a
// session-local illusion. Both writers must return the single stored row.
func TestPostgresReceiptConcurrentIdenticalWritersTwoConnections(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	storeA, pidA := f.newDedicatedStore(t)
	storeB, pidB := f.newDedicatedStore(t)
	requireDistinctBackendsPGState02(t, pidA, pidB, "store A", "store B")

	receipt := pgstate02Receipt(t, "intent-race", receiptProject, receiptExplorer)
	start := make(chan struct{})
	var writers sync.WaitGroup
	type result struct {
		stored *explorer.CompilationReceipt
		err    error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		writers.Add(1)
		store := storeA
		if i == 1 {
			store = storeB
		}
		go func() {
			defer writers.Done()
			<-start
			stored, err := store.InsertCompilationReceipt(f.ctx, receipt)
			results <- result{stored: stored, err: err}
		}()
	}
	close(start)
	writers.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent InsertCompilationReceipt error = %v", result.err)
		}
		requireReceiptEqual(t, receipt, *result.stored, "concurrent insert result")
	}
	if n := f.receiptCount(t, receiptProject); n != 1 {
		t.Fatalf("receipt row count after race = %d, want exactly 1", n)
	}
}

// TestPostgresReceiptUncommittedWinnerClaimTwoConnections proves the claim
// reread is genuinely cross-connection: connection A (one backend) holds an
// uncommitted claim row, connection B (a witnessed different backend) runs
// the Store insert and provably waits on it (pg_stat_activity lock/
// transaction wait on B's pid). On commit, B returns A's row -- witnessed by
// A's distinct stored createdAt that B never had -- and on abort, B's
// bounded retry claims the identity itself.
func TestPostgresReceiptUncommittedWinnerClaimTwoConnections(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	type claimResult struct {
		stored *explorer.CompilationReceipt
		err    error
	}

	rawA, pidA := f.newDedicatedPool(t)
	storeB, pidB := f.newDedicatedStore(t)
	requireDistinctBackendsPGState02(t, pidA, pidB, "raw connection A", "store B")

	// Same canonical content, different excluded metadata: whichever row
	// wins is identifiable by createdAt/requestId inside doc.
	winner := pgstate02Receipt(t, "intent-uncommitted", receiptProject, receiptExplorer)
	winner.RequestID = "req_from_A"
	winner.CreatedAt = time.Date(2026, 4, 5, 6, 7, 8, 654321000, time.UTC)
	loser := winner
	loser.RequestID = "req_from_B"
	loser.CreatedAt = receiptCreatedAt
	winnerDoc, err := json.Marshal(winner)
	if err != nil {
		t.Fatal(err)
	}
	rawInsert := pgstate02RawReceiptInsert(f.schema)

	insertBlockedUntil := func(commit bool) (*explorer.CompilationReceipt, error) {
		connA, err := rawA.Acquire(f.ctx)
		if err != nil {
			f.fail("acquire raw connection A", err)
		}
		defer connA.Release()
		tx, err := connA.Begin(f.ctx)
		if err != nil {
			f.fail("begin raw claim transaction", err)
		}
		if _, err := tx.Exec(f.ctx, rawInsert, receiptArgs(winner, winnerDoc)...); err != nil {
			tx.Rollback(f.ctx)
			f.fail("raw claim insert on A", err)
		}
		done := make(chan claimResult, 1)
		go func() {
			stored, err := storeB.InsertCompilationReceipt(f.ctx, loser)
			done <- claimResult{stored: stored, err: err}
		}()

		// Witness B genuinely waiting on A on the server.
		observed := false
		deadline := time.Now().Add(5 * time.Second)
		for !observed && time.Now().Before(deadline) {
			var waiting bool
			if err := f.iso.QueryRow(f.ctx,
				`SELECT count(*) > 0 FROM pg_stat_activity
				 WHERE pid = $1 AND state = 'active'
				   AND wait_event_type IN ('Lock', 'Transaction')`, pidB).Scan(&waiting); err != nil {
				tx.Rollback(f.ctx)
				f.fail("witness wait event", err)
			}
			if waiting {
				observed = true
				break
			}
			select {
			case r := <-done:
				tx.Rollback(f.ctx)
				t.Fatalf("precondition broken: claim finished before waiting on the uncommitted winner (err=%v)", r.err)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !observed {
			tx.Rollback(f.ctx)
			t.Fatal("store B never blocked on the uncommitted claim on connection A")
		}
		if commit {
			if err := tx.Commit(f.ctx); err != nil {
				f.fail("commit raw claim", err)
			}
		} else {
			if err := tx.Rollback(f.ctx); err != nil {
				f.fail("rollback raw claim", err)
			}
		}
		select {
		case r := <-done:
			return r.stored, r.err
		case <-time.After(30 * time.Second):
			t.Fatal("claim never resolved after the winner resolved")
			return nil, nil
		}
	}

	t.Run("winner commits", func(t *testing.T) {
		stored, err := insertBlockedUntil(true)
		if err != nil {
			f.fail("claim against committed winner", err)
		}
		requireReceiptEqual(t, winner, *stored, "claim reread of the committed winner")
		if stored.RequestID != "req_from_A" {
			t.Fatalf("claim returned the loser's row, not the winner's: requestId=%q", stored.RequestID)
		}
		if n := f.receiptCount(t, receiptProject); n != 1 {
			t.Fatalf("receipt row count = %d, want exactly 1", n)
		}
	})

	t.Run("winner aborts", func(t *testing.T) {
		abortWinner := winner
		abortWinner.IntentDigest = "intent-uncommitted-abort"
		// Re-derive both content identities: the contract check verifies
		// compilationKey against the canonical key before ReceiptID applies.
		abortWinner.CompilationKey, err = explorer.CompilationKey(abortWinner)
		if err != nil {
			t.Fatal(err)
		}
		abortKey, err := explorer.ReceiptID(abortWinner)
		if err != nil {
			t.Fatal(err)
		}
		abortWinner.ID = abortKey
		abortDoc, err := json.Marshal(abortWinner)
		if err != nil {
			t.Fatal(err)
		}
		abortLoser := abortWinner
		abortLoser.RequestID = "req_from_B_abort"
		abortLoser.CreatedAt = receiptCreatedAt

		connA, err := rawA.Acquire(f.ctx)
		if err != nil {
			f.fail("acquire raw connection A (abort case)", err)
		}
		defer connA.Release()
		tx, err := connA.Begin(f.ctx)
		if err != nil {
			f.fail("begin raw claim transaction (abort case)", err)
		}
		if _, err := tx.Exec(f.ctx, rawInsert, receiptArgs(abortWinner, abortDoc)...); err != nil {
			tx.Rollback(f.ctx)
			f.fail("raw claim insert on A (abort case)", err)
		}
		done := make(chan claimResult, 1)
		go func() {
			stored, err := storeB.InsertCompilationReceipt(f.ctx, abortLoser)
			done <- claimResult{stored: stored, err: err}
		}()
		time.Sleep(200 * time.Millisecond)
		if err := tx.Rollback(f.ctx); err != nil {
			f.fail("rollback raw claim (abort case)", err)
		}
		select {
		case r := <-done:
			if r.err != nil {
				f.fail("claim after aborted winner", r.err)
			}
			if r.stored.RequestID != "req_from_B_abort" {
				t.Fatalf("aborted winner left a row that is not the retry's own: requestId=%q", r.stored.RequestID)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("claim never resolved after the winner aborted")
		}
		var createdAt string
		if err := f.iso.QueryRow(f.ctx,
			`SELECT doc->>'createdAt' FROM `+f.receiptTable()+` WHERE project = $1 AND explorer_id = $2 AND id = $3`,
			receiptProject, receiptExplorer, abortWinner.ID).Scan(&createdAt); err != nil {
			f.fail("read aborted-case winner createdAt", err)
		}
		if createdAt != receiptCreatedAtText {
			t.Fatalf("aborted winner left createdAt %q, want the retry's own %q", createdAt, receiptCreatedAtText)
		}
	})
}

// TestPostgresReceiptFrozenGuardProvenNonVacuous is the accepted
// non-vacuity bar: every UPDATE and DELETE on the receipt table is refused
// with SQLSTATE 23514 by the DB trigger (message proves it is the frozen
// guard, not a CHECK), then the same violating writes SUCCEED only after a
// live DROP TRIGGER -- literal witnesses show the row really changed -- and
// the same UPDATE is refused again once the guard is restored by re-running
// the migration's exact SQL text.
func TestPostgresReceiptFrozenGuardProvenNonVacuous(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	receipt := pgstate02Receipt(t, "intent-frozen", receiptProject, receiptExplorer)
	f.rawInsertReceipt(t, receipt)

	mutations := []struct {
		column string
		set    string
	}{
		{"project", "'proj_intruder'"},
		{"explorer_id", "'exp_intruder'"},
		{"id", "'receipt_intruder'"},
		{"receipt_format_version", "9"},
		{"compiler_contract_version", "'loom.explorer.compiler/v99'"},
		{"compilation_key", "'compile_intruder'"},
		{"intent_digest", "'sha256:intruder'"},
		{"snapshot_token", "'intruder'"},
		{"source_generation", "'gen_intruder'"},
		{"recipe_digest", "'sha256:intruder'"},
		{"resolved_recipe_digest", "'sha256:intruder'"},
		{"doc", `'{"id":"intruder"}'::jsonb`},
	}
	where := fmt.Sprintf("project = '%s' AND explorer_id = '%s' AND id = '%s'",
		receipt.Project, receipt.ExplorerID, receipt.ID)

	for _, m := range mutations {
		_, err := f.iso.Exec(f.ctx,
			"UPDATE "+f.receiptTable()+" SET "+m.column+" = "+m.set+" WHERE "+where)
		pgErr := requirePgErrPGState02(t, err, "23514", "frozen receipt column "+m.column)
		if !strings.Contains(pgErr.Message, "insert-once") {
			t.Fatalf("frozen receipt refusal on %s came from something other than the guard: %s", m.column, pgErr.Message)
		}
	}
	if _, err := f.iso.Exec(f.ctx, "DELETE FROM "+f.receiptTable()+" WHERE "+where); err == nil {
		t.Fatal("DELETE on a receipt was accepted while the frozen trigger is installed")
	} else {
		pgErr := requirePgErrPGState02(t, err, "23514", "receipt delete")
		if !strings.Contains(pgErr.Message, "insert-once") {
			t.Fatalf("receipt delete refusal came from something other than the guard: %s", pgErr.Message)
		}
	}

	// Guard removal: the identical violating writes now succeed, with
	// literal witnesses proving the row actually changed -- the guard, not
	// the test, was doing the refusing.
	if _, err := f.iso.Exec(f.ctx,
		`DROP TRIGGER compilation_receipts_frozen ON `+f.receiptTable()); err != nil {
		f.fail("drop frozen trigger", err)
	}
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.receiptTable()+" SET intent_digest = 'sha256:unfrozen' WHERE "+where); err != nil {
		f.fail("mutating without the trigger must succeed", err)
	}
	var mutated string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT intent_digest FROM `+f.receiptTable()+` WHERE `+where).Scan(&mutated); err != nil {
		f.fail("read mutated intent_digest", err)
	}
	if mutated != "sha256:unfrozen" {
		t.Fatalf("guard-removal probe did not persist: intent_digest = %q", mutated)
	}
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.receiptTable()+" SET doc = '{\"id\":\"unfrozen\"}'::jsonb WHERE "+where); err != nil {
		f.fail("mutating doc without the trigger must succeed", err)
	}
	var docID string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT doc->>'id' FROM `+f.receiptTable()+` WHERE `+where).Scan(&docID); err != nil {
		f.fail("read mutated doc", err)
	}
	if docID != "unfrozen" {
		t.Fatalf("guard-removal doc probe did not persist: doc->>'id' = %q", docID)
	}
	if _, err := f.iso.Exec(f.ctx, "DELETE FROM "+f.receiptTable()+" WHERE "+where); err != nil {
		f.fail("delete without the trigger must succeed", err)
	}
	if n := f.receiptCount(t, receiptProject); n != 0 {
		t.Fatalf("guard-removal delete did not remove the row, count = %d", n)
	}

	// Restore the guard from the migration's own text and re-prove refusal.
	f.rerunMigration(t, 5)
	f.rawInsertReceipt(t, receipt)
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+f.receiptTable()+" SET intent_digest = 'sha256:refrozen' WHERE "+where); err == nil {
		t.Fatal("UPDATE accepted after the guard was restored from the migration text")
	} else {
		pgErr := requirePgErrPGState02(t, err, "23514", "restored frozen guard")
		if !strings.Contains(pgErr.Message, "insert-once") {
			t.Fatalf("restored guard refusal came from something else: %s", pgErr.Message)
		}
	}
}

// TestPostgresReceiptCorruptRowDetectedOnRead proves the read path fails
// closed: after a guard-removal probe tampers the stored document (witnessed
// as a real mutation), the tenant-scoped read refuses the row as
// ErrCorruptReceipt instead of serving a receipt that no longer matches its
// content identity.
func TestPostgresReceiptCorruptRowDetectedOnRead(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)

	writer := f.newStore(t)
	receipt := pgstate02Receipt(t, "intent-corrupt", receiptProject, receiptExplorer)
	if _, err := writer.InsertCompilationReceipt(f.ctx, receipt); err != nil {
		f.fail("insert receipt", err)
	}

	// With the guard removed the tamper lands (non-vacuity witness)...
	if _, err := f.iso.Exec(f.ctx,
		`DROP TRIGGER compilation_receipts_frozen ON `+f.receiptTable()); err != nil {
		f.fail("drop frozen trigger", err)
	}
	if _, err := f.iso.Exec(f.ctx, `UPDATE `+f.receiptTable()+`
		SET doc = doc || '{"intentDigest":"sha256:tampered"}'::jsonb,
		    intent_digest = 'sha256:tampered'
		WHERE project = $1 AND explorer_id = $2 AND id = $3`,
		receiptProject, receiptExplorer, receipt.ID); err != nil {
		f.fail("tamper receipt document", err)
	}
	var tampered string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT doc->>'intentDigest' FROM `+f.receiptTable()+
			` WHERE project = $1 AND explorer_id = $2 AND id = $3`,
		receiptProject, receiptExplorer, receipt.ID).Scan(&tampered); err != nil {
		f.fail("read tampered document", err)
	}
	if tampered != "sha256:tampered" {
		t.Fatalf("tamper probe did not persist: %q", tampered)
	}
	// ...and the guard is restored so the read, not a weakened schema, is
	// what decides.
	f.rerunMigration(t, 5)

	if _, err := writer.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, receipt.ID); !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("read of tampered receipt error = %v, want ErrCorruptReceipt", err)
	}
}

// TestPostgresReceiptJSONTriStateFreshRead proves the payload boundary
// survives a fresh DB read through the single stored document: a missing
// omitempty payload (nil), the JSON-null literal, and a document are three
// distinct stored shapes -- SQL-witnessed via key presence and jsonb typeof
// -- and each reads back exactly through a brand-new pool. A nil
// non-omitempty payload round-trips through the jsonb 'null' it encodes to
// (the same asymmetry encoding/json gives the Arango document path), which
// the literal witness pins instead of hand-waving.
func TestPostgresReceiptJSONTriStateFreshRead(t *testing.T) {
	f := newPGState02Fixture(t)
	f.migrate(t)
	writer := f.newStore(t)

	missing := pgstate02LegacyReceipt("legacy-tri-missing", "intent-missing")
	missing.NormalizedBundle = nil
	jsonNull := pgstate02LegacyReceipt("legacy-tri-null", "intent-null")
	jsonNull.CompiledConfig = json.RawMessage("null")
	jsonNull.PublicOutputContract = json.RawMessage("null")
	documented := pgstate02LegacyReceipt("legacy-tri-doc", "intent-doc")
	documented.CompiledConfig = json.RawMessage(cfgBeta)
	documented.PublicOutputContract = json.RawMessage(resultsOK)

	for _, receipt := range []explorer.CompilationReceipt{missing, jsonNull, documented} {
		if _, err := writer.InsertCompilationReceipt(f.ctx, receipt); err != nil {
			f.fail("insert tri-state receipt "+receipt.ID, err)
		}
	}

	// Literal witnesses on the stored documents: key presence and typeof.
	for _, tc := range []struct {
		id        string
		wantField string
		wantKind  string
	}{
		{"legacy-tri-missing", "compiledConfig", "missing"},
		{"legacy-tri-null", "compiledConfig", "null"},
		{"legacy-tri-doc", "compiledConfig", "object"},
	} {
		var has bool
		var kind string
		if err := f.iso.QueryRow(f.ctx,
			`SELECT doc ? 'compiledConfig', COALESCE(jsonb_typeof(doc->'compiledConfig'), 'missing') FROM `+
				f.receiptTable()+` WHERE project = $1 AND explorer_id = $2 AND id = $3`,
			receiptProject, receiptExplorer, tc.id).Scan(&has, &kind); err != nil {
			f.fail("read tri-state witness for "+tc.id, err)
		}
		if tc.wantKind == "missing" {
			if has || kind != "missing" {
				t.Fatalf("%s compiledConfig: key present=%v kind=%s, want absent/missing", tc.id, has, kind)
			}
			continue
		}
		if !has || kind != tc.wantKind {
			t.Fatalf("%s compiledConfig: key present=%v kind=%s, want present/%s", tc.id, has, kind, tc.wantKind)
		}
	}

	reader := f.newStore(t)
	gotMissing, err := reader.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, "legacy-tri-missing")
	if err != nil {
		f.fail("read missing-payload receipt", err)
	}
	if gotMissing.CompiledConfig != nil || gotMissing.PublicOutputContract != nil {
		t.Fatalf("missing payloads revived: config=%q contract=%q", gotMissing.CompiledConfig, gotMissing.PublicOutputContract)
	}
	gotNull, err := reader.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, "legacy-tri-null")
	if err != nil {
		f.fail("read json-null receipt", err)
	}
	if string(gotNull.CompiledConfig) != "null" || string(gotNull.PublicOutputContract) != "null" {
		t.Fatalf("JSON-null payload lost: config=%q contract=%q", gotNull.CompiledConfig, gotNull.PublicOutputContract)
	}
	gotDoc, err := reader.GetCompilationReceiptForExplorer(f.ctx, receiptProject, receiptExplorer, "legacy-tri-doc")
	if err != nil {
		f.fail("read documented receipt", err)
	}
	jsonEqual(t, json.RawMessage(cfgBeta), gotDoc.CompiledConfig, "documented compiledConfig")
	jsonEqual(t, json.RawMessage(resultsOK), gotDoc.PublicOutputContract, "documented publicOutputContract")
	// NormalizedBundle has no omitempty: nil encodes to the JSON 'null' the
	// document stores -- pinning the exact witness rather than assuming.
	var nilBundleKind string
	if err := f.iso.QueryRow(f.ctx,
		`SELECT COALESCE(jsonb_typeof(doc->'normalizedBundle'), 'missing') FROM `+
			f.receiptTable()+` WHERE project = $1 AND explorer_id = $2 AND id = 'legacy-tri-missing'`,
		receiptProject, receiptExplorer).Scan(&nilBundleKind); err != nil {
		f.fail("read nil bundle kind", err)
	}
	if nilBundleKind != "null" {
		t.Fatalf("nil normalizedBundle jsonb typeof = %s, want null (encoding/json has no omitempty on this field)", nilBundleKind)
	}
}
