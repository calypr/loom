package execution

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// TestCompiledQueryFingerprintByteGolden pins the exact digest bytes the
// cache-key and EXPLAIN policy consume, proving the typed-command migration
// kept the hash input shape byte-identical: the AQL digest covers query text
// and bind names only, and untagged or PostgreSQL-tagged commands contribute
// the empty AQL shape (sha256 of an empty payload). A single flipped byte of
// query text must move the AQL digest; the PostgreSQL/zero digests must not
// move at all.
func TestCompiledQueryFingerprintByteGolden(t *testing.T) {
	const (
		aqlText = "FOR d IN @@collection FILTER d.p == @p SORT d.id LIMIT @__loom_page_size RETURN d"
		// sha256 of the documented JSON payload shape; recompute only when
		// the documented encoding changes deliberately, never to chase a
		// migration drift.
		wantAQL = "47d2ce3477240c9eead395870c755b5bfbc99eae8e6d6d96480e2c6909657954"
		// sha256 of the empty string: the pinned empty AQL shape.
		wantEmpty = "6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d"
	)
	aqlCommand, err := command.NewAQLCommand(aqlText, map[string]any{
		"@collection":       "patients",
		"p":                 "abc",
		"__loom_page_size":  25,
		"__loom_page_after": "",
		"__loom_page_keys":  []string{},
	})
	if err != nil {
		t.Fatalf("build AQL command: %v", err)
	}
	plan := ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL}
	got := CompiledQueryFingerprint(compiler.CompiledQuery{PhysicalPlan: plan, Command: aqlCommand})
	if got != wantAQL {
		t.Fatalf("AQL fingerprint drifted from the byte golden: got %s want %s", got, wantAQL)
	}
	flipped, err := command.NewAQLCommand(aqlText[:len(aqlText)-1]+"e", map[string]any{
		"@collection":       "patients",
		"p":                 "abc",
		"__loom_page_size":  25,
		"__loom_page_after": "",
		"__loom_page_keys":  []string{},
	})
	if err != nil {
		t.Fatalf("build flipped command: %v", err)
	}
	if breakOne := CompiledQueryFingerprint(compiler.CompiledQuery{PhysicalPlan: plan, Command: flipped}); breakOne == wantAQL {
		t.Fatalf("single-byte query flip did not change the digest: %s", breakOne)
	}
	pgCommand, err := command.NewPostgreSQLCommand("SELECT id FROM t WHERE p = $1",
		[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "abc"}})
	if err != nil {
		t.Fatalf("build PostgreSQL command: %v", err)
	}
	if got := CompiledQueryFingerprint(compiler.CompiledQuery{PhysicalPlan: plan, Command: pgCommand}); got != wantEmpty {
		t.Fatalf("PostgreSQL-tagged command must contribute the pinned empty AQL shape: got %s want %s", got, wantEmpty)
	}
	if got := CompiledQueryFingerprint(compiler.CompiledQuery{PhysicalPlan: plan}); got != wantEmpty {
		t.Fatalf("untagged command must contribute the pinned empty AQL shape: got %s want %s", got, wantEmpty)
	}
}

func TestCompiledQueryFingerprintExcludesBindValues(t *testing.T) {
	query := "FOR root IN Patient FILTER root.id == @id RETURN root"
	left := compiler.CompiledQuery{Command: mustAQLCommand(query, map[string]any{"id": "one"}), Columns: []string{"id"}}
	right := left
	// Commands are immutable; the value-varied and key-varied plans rebuild
	// the tagged command rather than mutating shared bind state.
	right.Command = mustAQLCommand(query, map[string]any{"id": "two"})
	if got, want := CompiledQueryFingerprint(left), CompiledQueryFingerprint(right); got != want {
		t.Fatalf("fingerprint includes bind value: left=%q right=%q", got, want)
	}
	right.Command = mustAQLCommand(query, map[string]any{"id": "two", "other": "value"})
	if CompiledQueryFingerprint(left) == CompiledQueryFingerprint(right) {
		t.Fatal("fingerprint did not include bind-key shape")
	}
}

func TestCompiledQueryFingerprintKeepsPostgreSQLShapeIndependentOfText(t *testing.T) {
	first, err := command.NewPostgreSQLCommand("SELECT 1 AS id", []command.PostgreSQLParameter{{Type: command.PostgreSQLParameterBigint, Value: int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := command.NewPostgreSQLCommand("SELECT 2 AS id WHERE x = $1", nil)
	if err != nil {
		t.Fatal(err)
	}
	left := compiler.CompiledQuery{Command: first, Columns: []string{"id"}}
	right := compiler.CompiledQuery{Command: second, Columns: []string{"id"}}
	if got, want := CompiledQueryFingerprint(left), CompiledQueryFingerprint(right); got != want {
		t.Fatalf("PostgreSQL plan fingerprint must keep the pre-boundary empty AQL shape: left=%q right=%q", got, want)
	}
	// Output metadata still participates for PostgreSQL plans.
	right.Columns = []string{"id", "name"}
	if CompiledQueryFingerprint(left) == CompiledQueryFingerprint(right) {
		t.Fatal("fingerprint did not include output column shape for a PostgreSQL plan")
	}
}
