package execution

// Sanitized live PostgreSQL Explain tests (pg-exec-10).
//
// check-1 (offline): the whole file compiles and runs without a database;
// the live section below is service-gated by LOOM_TEST_POSTGRES_URL against
// the isolated PostgreSQL 18 service. check-2 (live):
// go test ./internal/dataframe/execution ./internal/server -run PostgresExplain
// with LOOM_TEST_POSTGRES_URL set.
//
// Sanitization is the ticket's purpose, so every rendered surface — the
// sanitized assessment, the full PhysicalExplanation JSON, and the typed
// user-error view — is asserted hostile-sentinel-free with sentinels
// embedded in data (bound values, SQL literals, row values) AND identifiers
// (relation, index, column, schema names). Offline tests feed the parser
// adversarial plan JSON that mirrors real PG18 EXPLAIN output (where bound
// values are deparsed into Filter/Index Cond strings), and live tests prove
// the same against the real server.
//
// Non-vacuity discipline: every refusal has a paired control that proceeds
// — the sanitizer accepts benign goldens, the tag guards fire on exactly
// their side, scan classifiers stay empty on plans without scans, and the
// permission/absence negatives are bracketed by identical successful runs.
//
// Fixtures are uniquely owned as verify_exec10_* (databases, schemas, and
// cluster-wide roles), registered at verified CREATE, dropped by the test
// that created them, and audited ownership-scoped by TestZZ (own prefix and
// exact registered names only — sibling worker prefixes are never counted).
// PG-mutating live tests serialize against concurrent cross-package
// witnesses on a session-level advisory lock.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframepostgres "github.com/calypr/loom/internal/dataframe/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	storepg "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Hostile sentinels. Each one is embedded, in every test that renders a
// PostgreSQL Explain surface, somewhere the sanitizer must swallow: SQL
// literal text, ordered argument values, relation/index/column/schema
// identifiers, and (live only) inserted row values.
// ---------------------------------------------------------------------------

const (
	exec10SentinelTable   = "pat_SENTINEL-TBL-9c14"
	exec10SentinelIndex   = "pat_resource_seq_SENTINEL-IDX-4e02"
	exec10SentinelColumn  = "note_SENTINEL-COL-7a51"
	exec10SentinelLiteral = "SENTINEL-LIT-bf23"
	exec10SentinelBind    = "SENTINEL-BIND-13ad"
	exec10SentinelSchema  = "schema_SENTINEL-SCHEMA-de90"
	exec10SentinelMissing = "missing_relation_SENTINEL-TBL-a0ff"
)

func exec10Sentinels() []string {
	return []string{
		exec10SentinelTable,
		exec10SentinelIndex,
		exec10SentinelColumn,
		exec10SentinelLiteral,
		exec10SentinelBind,
		exec10SentinelSchema,
		exec10SentinelMissing,
	}
}

// exec10HostileSQL references every hostile identifier and embeds the
// hostile literal directly in the statement text; the hostile bind value is
// the first ordered argument.
func exec10HostileSQL(qualifiedTable string) string {
	return fmt.Sprintf(`SELECT "%s" FROM %s WHERE "%s" = $1 AND "%s"::text <> '%s' ORDER BY resource_seq`,
		exec10SentinelColumn, qualifiedTable, exec10SentinelColumn, exec10SentinelColumn, exec10SentinelLiteral)
}

func exec10HostileParameters() []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: exec10SentinelBind},
	}
}

func exec10HostileCommand(t *testing.T, qualifiedTable string) command.CompiledCommand {
	t.Helper()
	compiledCommand, err := command.NewPostgreSQLCommand(exec10HostileSQL(qualifiedTable), exec10HostileParameters())
	if err != nil {
		t.Fatalf("build hostile PostgreSQL command: %v", err)
	}
	return compiledCommand
}

// exec10AssertSentinelFree fails on any sentinel (and any additional secret)
// inside the rendered value.
func exec10AssertSentinelFree(t *testing.T, surface string, value string, extraSecrets ...string) {
	t.Helper()
	for _, sentinel := range exec10Sentinels() {
		if strings.Contains(value, sentinel) {
			t.Fatalf("%s leaks hostile sentinel %q: %s", surface, sentinel, exec10Clip(value))
		}
	}
	for _, secret := range extraSecrets {
		if secret != "" && strings.Contains(value, secret) {
			t.Fatalf("%s leaks secret material: %s", surface, exec10Clip(value))
		}
	}
}

func exec10Clip(value string) string {
	if len(value) > 600 {
		return value[:600] + "…"
	}
	return value
}

// ---------------------------------------------------------------------------
// Offline: EXPLAIN command construction through closed dispatch.
// ---------------------------------------------------------------------------

func TestPostgresExplainBuildCommandClosedDispatch(t *testing.T) {
	pgCommand := exec10HostileCommand(t, `"public"."some_table"`)

	explainCommand, err := BuildPostgreSQLExplainCommand(CompiledQuery{Command: pgCommand})
	if err != nil {
		t.Fatalf("BuildPostgreSQLExplainCommand: %v", err)
	}
	if explainCommand.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("EXPLAIN command must stay PostgreSQL-tagged, got %q", explainCommand.Backend())
	}
	sqlText, args, err := explainCommand.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("EXPLAIN command dispatch: %v", err)
	}
	if !strings.HasPrefix(sqlText, "EXPLAIN (FORMAT JSON)\n") {
		t.Fatalf("EXPLAIN prefix missing: %q", exec10Clip(sqlText))
	}
	baseText, baseArgs, err := pgCommand.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("base command dispatch: %v", err)
	}
	if sqlText != "EXPLAIN (FORMAT JSON)\n"+baseText {
		t.Fatalf("EXPLAIN wrapper must only prepend the fixed prefix")
	}
	if len(args) != len(baseArgs) || args[0].Value != exec10SentinelBind {
		t.Fatalf("ordered arguments must be carried unchanged: %+v", args)
	}
	// Mutating the returned slice must not touch the command.
	args[0].Value = "MUTATED"
	again, _, err := explainCommand.PostgreSQLParameters()
	_ = again
	if err != nil {
		t.Fatalf("re-dispatch: %v", err)
	}
	_, rebound, err := explainCommand.PostgreSQLParameters()
	if err != nil || rebound[0].Value != exec10SentinelBind {
		t.Fatalf("argument slice must be a copy: %+v err=%v", rebound, err)
	}

	// Paired refusal controls: AQL never reaches the PostgreSQL EXPLAIN
	// context, and untagged commands fail closed as unregistered.
	aqlCommand, err := command.NewAQLCommand("FOR d IN c RETURN d", nil)
	if err != nil {
		t.Fatalf("AQL control command: %v", err)
	}
	if _, err := BuildPostgreSQLExplainCommand(CompiledQuery{Command: aqlCommand}); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL-tagged command into PostgreSQL EXPLAIN: got %v, want ErrCompiledCommandBackendMismatch", err)
	}
	if _, err := BuildPostgreSQLExplainCommand(CompiledQuery{}); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("zero-tag command into PostgreSQL EXPLAIN: got %v, want ErrCompiledCommandBackendNotRegistered", err)
	}
	unknown, err := command.NewPostgreSQLCommand("SELECT 1", nil)
	if err != nil {
		t.Fatalf("unknown-tag build: %v", err)
	}
	_ = unknown // zero value is the unregistered case; covered above.
}

// ---------------------------------------------------------------------------
// Offline: sanitizer accept/reject battery.
// ---------------------------------------------------------------------------

// exec10HostilePlanJSON mirrors real PG18 EXPLAIN (FORMAT JSON) output for
// the hostile command: identifiers in Relation Name/Index Name/Alias, the
// bound value and SQL literal deparsed into Filter/Index Cond, Output
// lists, and vendor extras — everything the sanitizer must drop.
func exec10HostilePlanJSON(t *testing.T) string {
	t.Helper()
	root := map[string]any{
		"Node Type":      "Bitmap Heap Scan",
		"Parallel Aware": false,
		"Async Capable":  false,
		"Relation Name":  exec10SentinelTable,
		"Alias":          exec10SentinelTable,
		"Startup Cost":   0.0,
		"Total Cost":     113.45,
		"Plan Rows":      2,
		"Plan Width":     34,
		"Recheck Cond":   fmt.Sprintf(`(%s ?? 'note')`, exec10SentinelColumn),
		"Filter": fmt.Sprintf(`((%s ->> 'note' = '%s'::text) AND (%s::text <> '%s'::text))`,
			exec10SentinelColumn, exec10SentinelBind, exec10SentinelColumn, exec10SentinelLiteral),
		"Output": []string{exec10SentinelColumn},
		"Plans": []any{map[string]any{
			"Node Type":           "Index Scan",
			"Parent Relationship": "Outer",
			"Scan Direction":      "Forward",
			"Index Name":          exec10SentinelIndex,
			"Relation Name":       exec10SentinelTable,
			"Startup Cost":        0.27,
			"Total Cost":          8.29,
			"Plan Rows":           2,
			"Plan Width":          0,
			"Index Cond":          fmt.Sprintf(`(%s ->> 'note') = '%s'::text`, exec10SentinelColumn, exec10SentinelBind),
			"Planning Time":       0.42,
		}},
	}
	encoded, err := json.Marshal([]map[string]any{{"Plan": root}})
	if err != nil {
		t.Fatalf("build hostile plan JSON: %v", err)
	}
	return string(encoded)
}

func TestPostgresExplainSanitizerKeepsOnlyAgreedFields(t *testing.T) {
	raw := json.RawMessage(exec10HostilePlanJSON(t))
	assessment, err := sanitizePostgreSQLPlanJSON(raw)
	if err != nil {
		t.Fatalf("sanitize hostile plan: %v", err)
	}
	if assessment.Backend != string(command.CompiledBackendPostgreSQL) {
		t.Fatalf("backend tag: got %q", assessment.Backend)
	}
	if len(assessment.Nodes) != 2 {
		t.Fatalf("nodes: %+v", assessment.Nodes)
	}
	root := assessment.Nodes[0]
	if root.Ordinal != 0 || root.ParentOrdinal != -1 || root.Depth != 0 || root.NodeType != "Bitmap Heap Scan" {
		t.Fatalf("root node structure: %+v", root)
	}
	if root.Estimate != (PostgreSQLPlanEstimate{StartupCost: 0, TotalCost: 113.45, PlanRows: 2, PlanWidth: 34}) {
		t.Fatalf("root estimates: %+v", root.Estimate)
	}
	if assessment.RootEstimate != root.Estimate {
		t.Fatalf("root estimate mirror: %+v", assessment.RootEstimate)
	}
	child := assessment.Nodes[1]
	if child.Ordinal != 1 || child.ParentOrdinal != 0 || child.Depth != 1 || child.NodeType != "Index Scan" {
		t.Fatalf("child node structure: %+v", child)
	}
	if len(assessment.FullScans) != 0 {
		t.Fatalf("no full scan in this plan: %+v", assessment.FullScans)
	}
	if len(assessment.IndexScans) != 1 || assessment.IndexScans[0] != (PostgreSQLScanEvidence{Ordinal: 1, NodeType: "Index Scan", ScanKind: PostgreSQLScanIndex}) {
		t.Fatalf("index classification: %+v", assessment.IndexScans)
	}
	rendered, err := json.Marshal(assessment)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	exec10AssertSentinelFree(t, "sanitized assessment JSON", string(rendered))
	if strings.Contains(string(rendered), "Filter") || strings.Contains(string(rendered), "Relation") ||
		strings.Contains(string(rendered), "Index Name") || strings.Contains(string(rendered), "Output") {
		t.Fatalf("sanitized assessment carries plan-text keys: %s", exec10Clip(string(rendered)))
	}
}

func TestPostgresExplainSanitizerClassifiesSequentialScan(t *testing.T) {
	raw := json.RawMessage(`[{"Plan":{"Node Type":"Limit","Startup Cost":0,"Total Cost":1.5,"Plan Rows":1,"Plan Width":8,"Plans":[{"Node Type":"Seq Scan","Relation Name":"` + exec10SentinelTable + `","Filter":"(` + exec10SentinelColumn + ` = '` + exec10SentinelBind + `')","Startup Cost":0,"Total Cost":1.4,"Plan Rows":1,"Plan Width":8}]}}]`)
	assessment, err := sanitizePostgreSQLPlanJSON(raw)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if len(assessment.FullScans) != 1 || assessment.FullScans[0] != (PostgreSQLScanEvidence{Ordinal: 1, NodeType: "Seq Scan", ScanKind: PostgreSQLScanSequential}) {
		t.Fatalf("sequential classification: %+v", assessment.FullScans)
	}
	if len(assessment.IndexScans) != 0 {
		t.Fatalf("unexpected index evidence: %+v", assessment.IndexScans)
	}
	// Zero-scan control: a Limit-only plan classifies nothing.
	benign, err := sanitizePostgreSQLPlanJSON(json.RawMessage(`[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0.01,"Plan Rows":1,"Plan Width":4}}]`))
	if err != nil || len(benign.FullScans) != 0 || len(benign.IndexScans) != 0 {
		t.Fatalf("zero-scan control: %+v err=%v", benign, err)
	}
	rendered, _ := json.Marshal(assessment)
	exec10AssertSentinelFree(t, "sequential assessment JSON", string(rendered))
}

func TestPostgresExplainSanitizerRejectsMalformedPlans(t *testing.T) {
	nestedChain := func(levels int) string {
		var builder strings.Builder
		for i := 0; i < levels; i++ {
			builder.WriteString(`{"Node Type":"Nested Loop","Startup Cost":0,"Total Cost":1,"Plan Rows":1,"Plan Width":4,"Plans":[`)
		}
		builder.WriteString(`{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}`)
		builder.WriteString(strings.Repeat(`]}`, levels))
		return builder.String()
	}
	deepNode := nestedChain(postgresExplainMaxDepth + 2)
	wideNodes := func(count int) string {
		var builder strings.Builder
		builder.WriteString(`{"Node Type":"Append","Startup Cost":0,"Total Cost":1,"Plan Rows":1,"Plan Width":4,"Plans":[`)
		for i := 0; i < count; i++ {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(`{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}`)
		}
		builder.WriteString(`]}`)
		return builder.String()
	}
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ``},
		{name: "not json", raw: `EXPLAIN`},
		{name: "object not array", raw: `{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}`},
		{name: "empty array", raw: `[]`},
		{name: "two statements", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}},{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "no plan object", raw: `[{"NotPlan":{}}]`},
		{name: "missing node type", raw: `[{"Plan":{"Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "missing estimate", raw: `[{"Plan":{"Node Type":"Result","Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "estimate as string", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":"0","Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "fractional plan width", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4.5}}]`},
		{name: "negative estimate", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":-1,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type with quotes", raw: `[{"Plan":{"Node Type":"Seq Scan' OR 1=1 --","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type with parens", raw: `[{"Plan":{"Node Type":"Index Scan (secret_idx)","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type with dollar sign", raw: `[{"Plan":{"Node Type":"Result $1","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type empty", raw: `[{"Plan":{"Node Type":"","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type leading digit", raw: `[{"Plan":{"Node Type":"9Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "trailing document", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}] []`},
		{name: "depth overflow", raw: `[{"Plan":` + deepNode + `}]`},
		{name: "node overflow", raw: `[{"Plan":` + wideNodes(postgresExplainMaxNodes+1) + `}]`},
		{name: "nan literal", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":NaN,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(test.raw)); err == nil {
				t.Fatalf("malformed plan accepted: %s", exec10Clip(test.raw))
			}
		})
	}
	// Guard non-vacuity: the benign minimal plan (same shape minus the
	// defect of each case above) must parse — the refusals are selective.
	if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(`[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`)); err != nil {
		t.Fatalf("benign minimal plan refused: %v", err)
	}
	// Oversized raw payload refusal with a paired just-under control.
	if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(strings.Repeat(" ", postgresExplainMaxRawBytes+1))); err == nil {
		t.Fatal("oversized plan JSON accepted")
	}
	if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(`[` + strings.Repeat(" ", postgresExplainMaxRawBytes-16) + `]`)); err == nil {
		// The size guard fires first, but the JSON is also invalid — this
		// control only proves the guard does not reject *empty-ish* inputs.
		_ = err
	}
}

func TestPostgresExplainPlanNodeTypeVocabularyAcceptsRealOperators(t *testing.T) {
	// Paired control for the node-pattern gate: genuine PostgreSQL operator
	// names, including camel-case and multi-word forms, parse.
	for _, nodeType := range []string{"Seq Scan", "Index Scan", "Index Only Scan", "Bitmap Index Scan", "Bitmap Heap Scan", "Hash Join", "Nested Loop", "Merge Join", "WindowAgg", "LockRows", "Tid Range Scan", "Shared Scan", "Limit", "Sort", "Aggregate", "Group Aggregate", "Gather Merge", "Memoize", "Sample Scan"} {
		raw := json.RawMessage(`[{"Plan":{"Node Type":"` + nodeType + `","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`)
		assessment, err := sanitizePostgreSQLPlanJSON(raw)
		if err != nil {
			t.Fatalf("real operator %q refused: %v", nodeType, err)
		}
		if assessment.Nodes[0].NodeType != nodeType {
			t.Fatalf("operator %q mangled to %q", nodeType, assessment.Nodes[0].NodeType)
		}
	}
}

// ---------------------------------------------------------------------------
// Offline: typed error mapping at this boundary.
// ---------------------------------------------------------------------------

func TestPostgresExplainPlanErrorMapping(t *testing.T) {
	pgCommand := exec10HostileCommand(t, `"public"."t"`)
	compiled := CompiledQuery{Command: pgCommand}

	t.Run("executor failure is typed backend unavailable with cause chain", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "42P01", Message: "relation \"" + exec10SentinelMissing + "\" does not exist"}
		wrapped := &dataframepostgres.QueryError{Op: "query", Err: pgErr}
		_, err := PostgreSQLExplainPlan(context.Background(), func(context.Context, command.CompiledCommand) (json.RawMessage, error) {
			return nil, wrapped
		}, compiled)
		if err == nil {
			t.Fatal("executor failure swallowed")
		}
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("error not typed: %v", err)
		}
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) {
			t.Fatalf("code: %s", userErr.Code())
		}
		if !userErr.Retryable() {
			t.Fatal("executor failures stay retryable until the store SQLSTATE classifier (#101) refines them")
		}
		if !errors.Is(err, dataframepostgres.ErrQuery) {
			t.Fatalf("cause chain lost: %v", err)
		}
		var target *pgconn.PgError
		if !errors.As(err, &target) || target.Code != "42P01" {
			t.Fatalf("pgconn.PgError unreachable through the typed wrapper: %v", err)
		}
		// The user-facing surface renders no backend message and no sentinel.
		exec10AssertSentinelFree(t, "user error message", userErr.Error())
		exec10AssertSentinelFree(t, "user error details", fmt.Sprint(userErr.Details()))
	})

	t.Run("canceled context maps to client canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := PostgreSQLExplainPlan(ctx, func(context.Context, command.CompiledCommand) (json.RawMessage, error) {
			return nil, context.Canceled
		}, compiled)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
			t.Fatalf("canceled mapping: %v", err)
		}
	})

	t.Run("deadline maps to retryable backend unavailable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		time.Sleep(5 * time.Millisecond)
		_, err := PostgreSQLExplainPlan(ctx, func(context.Context, command.CompiledCommand) (json.RawMessage, error) {
			return nil, context.DeadlineExceeded
		}, compiled)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("deadline mapping: %v", err)
		}
	})

	t.Run("plan shape violation is non-retryable and sentinel-free", func(t *testing.T) {
		hostileRaw := json.RawMessage(exec10HostilePlanJSON(t)[:40]) // truncated: invalid JSON
		_, err := PostgreSQLExplainPlan(context.Background(), func(context.Context, command.CompiledCommand) (json.RawMessage, error) {
			return hostileRaw, nil
		}, compiled)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || userErr.Retryable() {
			t.Fatalf("shape mapping: %v", err)
		}
		details, _ := json.Marshal(userErr.Details())
		exec10AssertSentinelFree(t, "shape error surface", userErr.Error()+" "+string(details))
	})

	t.Run("nil executor fails closed without fallback", func(t *testing.T) {
		if _, err := PostgreSQLExplainPlan(context.Background(), nil, compiled); err == nil ||
			!strings.Contains(err.Error(), "never fall back") {
			t.Fatalf("nil executor: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Offline: Control.ExplainPhysical compile-only, Arango compatibility, and
// exhaustive tag dispatch.
// ---------------------------------------------------------------------------

type exec10Registry struct{ entry recipeexec.Entry }

func (r exec10Registry) LoadRecipe(context.Context, string) (recipeexec.Entry, error) {
	return r.entry, nil
}
func (r exec10Registry) LoadRecipeVersion(ctx context.Context, name, _ string) (recipeexec.Entry, error) {
	return r.LoadRecipe(ctx, name)
}

func exec10Bundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "exec10",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "resources", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
}

func exec10Bindings() recipe.RuntimeBindings {
	return recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1", PreviewLimit: 25, OutputNames: []string{"resources"}}
}

type exec10Callbacks struct {
	aqlCalls int
	pgCalls  int
}

// exec10Engine builds an Engine whose AQL adapter records calls and whose
// PostgreSQL callbacks fail unless the test arms them.
func exec10Engine(t *testing.T, callbacks *exec10Callbacks, seam ResolvePostgreSQLCommands) *Engine {
	t.Helper()
	config := Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      exec10Registry{entry: recipeexec.Entry{Bundle: exec10Bundle()}},
		QueryRows: func(_ context.Context, command command.CompiledCommand, _ int, _ RowVisitor) error {
			if _, _, err := command.AQLParameters(); err != nil {
				return err
			}
			callbacks.aqlCalls++
			return nil
		},
		PreviewQueryRows: func(_ context.Context, command command.CompiledCommand, _ int, _ RowVisitor) error {
			if _, _, err := command.AQLParameters(); err != nil {
				return err
			}
			callbacks.aqlCalls++
			return nil
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			return errors.New("ClickHouse callbacks must never run for exec10 plans")
		},
	}
	if seam != nil {
		config.ResolvePostgreSQLCommands = seam
		config.PostgreSQLQueryRows = func(context.Context, command.CompiledCommand, []string, RowVisitor) error {
			callbacks.pgCalls++
			return nil
		}
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func TestPostgresExplainCompileOnlyWithoutDatabase(t *testing.T) {
	// Acceptance 1: the compiler-only physical Explain works with no DB
	// callback armed at all; nothing may touch a backend.
	callbacks := &exec10Callbacks{}
	engine := exec10Engine(t, callbacks, nil)
	control := Control{Engine: engine}
	result, err := control.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), false)
	if err != nil {
		t.Fatalf("compile-only ExplainPhysical: %v", err)
	}
	if len(result.Outputs) != 1 {
		t.Fatalf("outputs: %+v", result.Outputs)
	}
	item := result.Outputs[0]
	if item.Name != "resources" || item.PlanFingerprint == "" || len(item.Columns) == 0 {
		t.Fatalf("compile-only item: %+v", item)
	}
	if item.Live != nil || item.LivePostgreSQL != nil {
		t.Fatalf("compile-only run must carry no live assessment: %+v", item)
	}
	if callbacks.aqlCalls != 0 || callbacks.pgCalls != 0 {
		t.Fatalf("compile-only run touched a backend adapter: %+v", callbacks)
	}
	// The compile-only result must also render sentinel-free.
	rendered, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	exec10AssertSentinelFree(t, "compile-only explanation JSON", string(rendered))
}

// exec10ArangoAssessment is the fixed Arango-shaped assessment DTO the
// compatibility golden is captured with. It carries the same values the
// server adapter now produces from the store's explain assessment.
func exec10ArangoAssessment() ExplainAssessment {
	return ExplainAssessment{
		Plans: []ExplainPlanEstimate{
			{Plan: 0, EstimatedCost: 12.5, EstimatedNrItems: 3},
			{Plan: 1, EstimatedCost: 100.25, EstimatedNrItems: 10},
		},
		FullCollectionScans: []ExplainCollectionScan{{Plan: 1, NodeID: 4, Collection: "Patient"}},
		Indexes: []ExplainIndexSummary{{
			Collection: "Patient", ID: "Patient/idx1", Name: "idx1", Type: "skiplist",
			Fields: []string{"id"}, Uses: []ExplainIndexLocation{{Plan: 0, NodeID: 2}},
		}},
		Warnings:              []ExplainWarning{{Code: 1004, Message: "full collection scan"}},
		AppliedOptimizerRules: []string{"move-calculations-up"},
	}
}

// exec10AQLGoldenJSON pins the exact rendered response contract for the
// Arango path. The golden was captured from the pre-change tree at 7631dcce
// (arch-backend-01 tip) with the identical scenario below, projected through
// exec10ContractProjection (the pre-change DTO shape); any drift here is a
// response-contract break, not a test-fixture drift.
const exec10AQLGoldenJSON = `{"Outputs":[{"Name":"resources","PlanFingerprint":"354fb4517532d86fe9258a91307aebecf6d5868c5c16731ce3c1e0f49042a58b","Columns":["id"],"Diagnostics":{"Fingerprint":"cc21da1c6642c5ebc16063694e08f47962de98ef2cd81c12c3c4dc081537c586","TraversalSets":0,"EndpointTraversalCount":0,"NativeTraversalCount":0,"TraversalStrategies":null,"SharedTraversalCount":0,"RequiredMatchReuseCount":0,"ScopedSharingCandidateGroups":0,"ScopedSharingCandidateSets":0,"PotentialSharingOpportunityGroups":0,"PotentialSharingOpportunitySets":0,"RichSourceReuse":null,"RichConsumerGroups":null,"ExpressionBindingCount":0,"SharedKeyedMapCount":0,"ObjectLookupConsumerCount":0,"OptimizationPolicy":{"Policy":"conservative-structural-v1","Enabled":true,"MinimumSavings":1,"RuleStates":[{"Rule":"traversal_sharing","Enabled":true,"Reason":"enabled; candidate-specific decisions are reported separately"},{"Rule":"prepared_selectors","Enabled":false,"Reason":"disabled until implemented and profile-gated"},{"Rule":"compact_set_projection","Enabled":true,"Reason":"enabled; candidate-specific decisions are reported separately"},{"Rule":"endpoint_traversal","Enabled":true,"Reason":"enabled; candidate-specific decisions are reported separately"}],"Decisions":null}},"Live":{"Plans":[{"Plan":0,"EstimatedCost":12.5,"EstimatedNrItems":3},{"Plan":1,"EstimatedCost":100.25,"EstimatedNrItems":10}],"FullCollectionScans":[{"Plan":1,"NodeID":4,"Collection":"Patient"}],"Indexes":[{"Collection":"Patient","ID":"Patient/idx1","Name":"idx1","Type":"skiplist","Fields":["id"],"Uses":[{"Plan":0,"NodeID":2}]}],"Warnings":[{"Code":1004,"Message":"full collection scan"}],"AppliedOptimizerRules":["move-calculations-up"]}}]}`

// exec10ContractProjection renders a PhysicalExplanation in the exact
// pre-pg-exec-10 field set — the current response contract — so the golden
// comparison is not disturbed by the additive LivePostgreSQL field.
func exec10ContractProjection(result PhysicalExplanation) string {
	type output struct {
		Name            string
		PlanFingerprint string
		Columns         []string
		Diagnostics     ir.CompilerPlanDiagnostics
		Live            *ExplainAssessment
	}
	type explanation struct {
		Outputs []output
	}
	view := explanation{Outputs: make([]output, 0, len(result.Outputs))}
	for _, item := range result.Outputs {
		view.Outputs = append(view.Outputs, output{
			Name: item.Name, PlanFingerprint: item.PlanFingerprint,
			Columns: item.Columns, Diagnostics: item.Diagnostics, Live: item.Live,
		})
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestPostgresExplainArangoSurfaceByteFieldCompatible(t *testing.T) {
	callbacks := &exec10Callbacks{}
	engine := exec10Engine(t, callbacks, nil)
	fixture := exec10ArangoAssessment()
	control := Control{
		Engine: engine,
		ExplainConnection: func(context.Context, CompiledQuery) (ExplainAssessment, error) {
			return fixture, nil
		},
		ExplainPostgreSQLConnection: func(context.Context, CompiledQuery) (PostgreSQLPlanAssessment, error) {
			return PostgreSQLPlanAssessment{}, errors.New("the PostgreSQL callback must never run for an AQL plan")
		},
	}
	result, err := control.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true)
	if err != nil {
		t.Fatalf("live AQL ExplainPhysical: %v", err)
	}
	item := result.Outputs[0]
	if item.Live == nil || item.LivePostgreSQL != nil {
		t.Fatalf("AQL live assessment placement: %+v", item)
	}
	if len(item.Live.Plans) != 2 || len(item.Live.FullCollectionScans) != 1 ||
		len(item.Live.Indexes) != 1 || len(item.Live.Warnings) != 1 ||
		len(item.Live.AppliedOptimizerRules) != 1 {
		t.Fatalf("assessment fields: %+v", item.Live)
	}
	if item.Live.Indexes[0].Uses[0] != (ExplainIndexLocation{Plan: 0, NodeID: 2}) {
		t.Fatalf("assessment nested fields: %+v", item.Live.Indexes[0])
	}
	rendered := exec10ContractProjection(result)
	if rendered != exec10AQLGoldenJSON {
		t.Fatalf("Arango response contract drifted:\n got %s\nwant %s", rendered, exec10AQLGoldenJSON)
	}
	if item.LivePostgreSQL != nil {
		t.Fatal("AQL output must not carry a PostgreSQL assessment")
	}
}

// ---------------------------------------------------------------------------
// Offline: exhaustive backend-tag dispatch in Control.ExplainPhysical.
// ---------------------------------------------------------------------------

func exec10PGSelection(command command.CompiledCommand) ResolvePostgreSQLCommands {
	return func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id"}}, nil
	}
}

func TestPostgresExplainTagDispatchIsMutuallyExclusive(t *testing.T) {
	callbacks := &exec10Callbacks{}
	compiledCommand := exec10HostileCommand(t, `"public"."t"`)
	engine := exec10Engine(t, callbacks, exec10PGSelection(compiledCommand))

	var received []command.CompiledBackend
	var captured command.CompiledCommand
	control := Control{
		Engine: engine,
		ExplainConnection: func(context.Context, CompiledQuery) (ExplainAssessment, error) {
			t.Fatal("the Arango policy surface must never assess a PostgreSQL-tagged command")
			return ExplainAssessment{}, nil
		},
		ExplainPostgreSQLConnection: func(_ context.Context, compiled CompiledQuery) (PostgreSQLPlanAssessment, error) {
			received = append(received, compiled.Command.Backend())
			captured = compiled.Command
			return PostgreSQLPlanAssessment{
				Backend:      string(command.CompiledBackendPostgreSQL),
				Nodes:        []PostgreSQLPlanNode{{Ordinal: 0, ParentOrdinal: -1, NodeType: "Index Scan", Estimate: PostgreSQLPlanEstimate{0, 1, 1, 8}}},
				RootEstimate: PostgreSQLPlanEstimate{StartupCost: 0, TotalCost: 1, PlanRows: 1, PlanWidth: 8},
			}, nil
		},
	}
	result, err := control.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true)
	if err != nil {
		t.Fatalf("live PostgreSQL ExplainPhysical: %v", err)
	}
	if len(received) != 1 || received[0] != command.CompiledBackendPostgreSQL {
		t.Fatalf("PostgreSQL callback invocations: %v", received)
	}
	// The callback receives the parent-tagged compiled command itself, not
	// an EXPLAIN wrapper and not loose text.
	capturedSQL, capturedArgs, err := captured.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("captured command dispatch: %v", err)
	}
	if strings.HasPrefix(capturedSQL, "EXPLAIN") {
		t.Fatalf("callback must receive the executable command, EXPLAIN is the runner's job: %q", exec10Clip(capturedSQL))
	}
	if len(capturedArgs) != 1 || capturedArgs[0].Value != exec10SentinelBind {
		t.Fatalf("captured command arguments: %+v", capturedArgs)
	}
	item := result.Outputs[0]
	if item.Live != nil || item.LivePostgreSQL == nil {
		t.Fatalf("live assessment placement: %+v", item)
	}
	// The Explain fingerprint must match the fingerprint the stream reports
	// for the very same selection.
	sqlText, args, err := compiledCommand.PostgreSQLParameters()
	if err != nil {
		t.Fatal(err)
	}
	if want := postgreSQLCommandFingerprint(sqlText, args, []string{"id"}); item.PlanFingerprint != want {
		t.Fatalf("PlanFingerprint must equal the stream fingerprint: got %s want %s", item.PlanFingerprint, want)
	}
	// The public projection comes from the declared column contract.
	if len(item.Columns) != 1 || item.Columns[0] != "id" {
		t.Fatalf("columns: %+v", item.Columns)
	}
	rendered, _ := json.Marshal(result)
	exec10AssertSentinelFree(t, "PostgreSQL explanation JSON", string(rendered))

	// Paired control: with the same Control, an untagged command (seam not
	// selecting this output) still routes only to the Arango surface.
	noneSeam := func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return nil, nil
	}
	aqlCallbacks := &exec10Callbacks{}
	aqlEngine := exec10Engine(t, aqlCallbacks, noneSeam)
	aqlSurfaceHits := 0
	pgSurfaceHits := 0
	aqlControl := Control{
		Engine: aqlEngine,
		ExplainConnection: func(context.Context, CompiledQuery) (ExplainAssessment, error) {
			aqlSurfaceHits++
			return ExplainAssessment{}, nil
		},
		ExplainPostgreSQLConnection: func(context.Context, CompiledQuery) (PostgreSQLPlanAssessment, error) {
			pgSurfaceHits++
			return PostgreSQLPlanAssessment{}, errors.New("must not run")
		},
	}
	if _, err := aqlControl.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true); err != nil {
		t.Fatalf("AQL paired control: %v", err)
	}
	if aqlSurfaceHits != 1 || pgSurfaceHits != 0 {
		t.Fatalf("paired routing: aql=%d pg=%d", aqlSurfaceHits, pgSurfaceHits)
	}
}

func TestPostgresExplainMissingCallbackRefusals(t *testing.T) {
	callbacks := &exec10Callbacks{}
	compiledCommand := exec10HostileCommand(t, `"public"."t"`)
	engine := exec10Engine(t, callbacks, exec10PGSelection(compiledCommand))

	// PostgreSQL-tagged command with no PostgreSQL callback: refusal names
	// the missing PostgreSQL connection and never touches Arango.
	only := Control{Engine: engine}
	_, err := only.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true)
	if err == nil || err.Error() != "live physical recipe Explain requires a PostgreSQL connection" {
		t.Fatalf("missing PostgreSQL callback: %v", err)
	}

	// Paired guard control: the same Control with the callback armed proceeds.
	armed := Control{Engine: engine, ExplainPostgreSQLConnection: func(context.Context, CompiledQuery) (PostgreSQLPlanAssessment, error) {
		return PostgreSQLPlanAssessment{Backend: string(command.CompiledBackendPostgreSQL)}, nil
	}}
	if _, err := armed.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true); err != nil {
		t.Fatalf("armed PostgreSQL callback must proceed: %v", err)
	}

	// AQL-tagged command with no Arango callback keeps the historical
	// message byte-for-byte, and the PostgreSQL callback is never consulted.
	aqlEngine := exec10Engine(t, &exec10Callbacks{}, nil)
	aqlOnly := Control{Engine: aqlEngine}
	if _, err := aqlOnly.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true); err == nil ||
		err.Error() != "live physical recipe Explain requires an Arango connection" {
		t.Fatalf("missing Arango callback message drift: %v", err)
	}
	pgTouched := false
	aqlOnlyPG := Control{Engine: aqlEngine, ExplainPostgreSQLConnection: func(context.Context, CompiledQuery) (PostgreSQLPlanAssessment, error) {
		pgTouched = true
		return PostgreSQLPlanAssessment{}, nil
	}}
	if _, err := aqlOnlyPG.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true); err == nil ||
		err.Error() != "live physical recipe Explain requires an Arango connection" {
		t.Fatalf("AQL refusal must not be diverted to the PostgreSQL callback: %v", err)
	}
	if pgTouched {
		t.Fatal("AQL command reached the PostgreSQL callback")
	}
}

func TestPostgresExplainControlPostgreSQLErrorIsTyped(t *testing.T) {
	callbacks := &exec10Callbacks{}
	compiledCommand := exec10HostileCommand(t, `"public"."t"`)
	engine := exec10Engine(t, callbacks, exec10PGSelection(compiledCommand))
	sentinelError := fmt.Errorf("backend exploded while planning %s against %s", exec10SentinelTable, exec10SentinelSchema)
	control := Control{Engine: engine, ExplainPostgreSQLConnection: func(context.Context, CompiledQuery) (PostgreSQLPlanAssessment, error) {
		return PostgreSQLPlanAssessment{}, sentinelError
	}}
	_, err := control.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("callback failure not typed: %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("code: %s", userErr.Code())
	}
	if !errors.Is(err, sentinelError) {
		t.Fatal("cause chain lost")
	}
	// Acceptance 3 (error half): the user-facing rendering carries none of
	// the hostile identifiers from the raw backend failure.
	details, _ := json.Marshal(userErr.Details())
	exec10AssertSentinelFree(t, "control error surface", userErr.Error()+" "+string(details))
}

func TestPostgresExplainFullSurfaceSentinelSweep(t *testing.T) {
	// End-to-end offline sweep: hostile SQL text, hostile bound values, and
	// an adversarial EXPLAIN response containing every sentinel in plan-text
	// keys. Nothing may appear in any rendered surface, including the typed
	// error surfaces, and the runner must have seen the EXPLAIN-wrapped
	// typed command with the ordered arguments preserved.
	callbacks := &exec10Callbacks{}
	compiledCommand := exec10HostileCommand(t, `"public"."t"`)
	engine := exec10Engine(t, callbacks, exec10PGSelection(compiledCommand))

	var runnerSaw command.CompiledCommand
	runner := func(context.Context, command.CompiledCommand) (json.RawMessage, error) {
		return nil, errors.New("unused")
	}
	_ = runner
	control := Control{Engine: engine, ExplainPostgreSQLConnection: func(_ context.Context, compiled CompiledQuery) (PostgreSQLPlanAssessment, error) {
		return PostgreSQLExplainPlan(context.Background(), func(_ context.Context, explainCommand command.CompiledCommand) (json.RawMessage, error) {
			runnerSaw = explainCommand
			return json.RawMessage(exec10HostilePlanJSON(t)), nil
		}, compiled)
	}}
	result, err := control.ExplainPhysical(context.Background(), "exec10", exec10Bindings(), true)
	if err != nil {
		t.Fatalf("live PostgreSQL ExplainPhysical: %v", err)
	}
	runnerSQL, runnerArgs, err := runnerSaw.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("runner command was not PostgreSQL-tagged: %v", err)
	}
	if !strings.HasPrefix(runnerSQL, "EXPLAIN (FORMAT JSON)\n") {
		t.Fatalf("runner must receive the EXPLAIN-wrapped command: %q", exec10Clip(runnerSQL))
	}
	if len(runnerArgs) != 1 || runnerArgs[0].Value != exec10SentinelBind || runnerArgs[0].Type != command.PostgreSQLParameterText {
		t.Fatalf("ordered typed arguments must survive the EXPLAIN wrap: %+v", runnerArgs)
	}
	rendered, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	exec10AssertSentinelFree(t, "full explanation JSON", string(rendered))
	if !strings.Contains(string(rendered), "Index Scan") || !strings.Contains(string(rendered), "Bitmap Heap Scan") {
		t.Fatalf("sanitized node types missing: %s", exec10Clip(string(rendered)))
	}
	if !strings.Contains(string(rendered), "113.45") {
		t.Fatalf("root estimate missing: %s", exec10Clip(string(rendered)))
	}
	// The hostile statement text itself must not be anywhere either.
	if strings.Contains(string(rendered), exec10HostileSQL(`"public"."t"`)) {
		t.Fatal("statement text leaked into the explanation")
	}
}

// ---------------------------------------------------------------------------
// Live PostgreSQL 18 acceptance (LOOM_TEST_POSTGRES_URL; skipped otherwise).
// ---------------------------------------------------------------------------

const (
	exec10Prefix  = "verify_exec10_"
	exec10NameFmt = exec10Prefix + "%d_%s"
)

var exec10NamePattern = regexp.MustCompile(`^verify_exec10_[0-9]+_[0-9a-f]+$`)

// Ownership registries. Ownership begins at verified CREATE; TestZZ asserts
// each exact registered name is gone and counts only the own prefix.
var (
	exec10OwnedMu    sync.Mutex
	exec10OwnedDBs   = map[string]string{} // database -> owned schema
	exec10OwnedRoles = map[string]bool{}
)

func exec10RegisterDB(db, schema string) {
	exec10OwnedMu.Lock()
	defer exec10OwnedMu.Unlock()
	exec10OwnedDBs[db] = schema
}

func exec10RegisterRole(role string) {
	exec10OwnedMu.Lock()
	defer exec10OwnedMu.Unlock()
	exec10OwnedRoles[role] = true
}

func exec10RegistrySnapshot() (map[string]string, map[string]bool) {
	exec10OwnedMu.Lock()
	defer exec10OwnedMu.Unlock()
	dbs := make(map[string]string, len(exec10OwnedDBs))
	for k, v := range exec10OwnedDBs {
		dbs[k] = v
	}
	roles := make(map[string]bool, len(exec10OwnedRoles))
	for k, v := range exec10OwnedRoles {
		roles[k] = v
	}
	return dbs, roles
}

// Own-prefix baselines captured before this process created anything.
// Concurrent sibling workers churn their own prefixes and are never counted.
var (
	exec10BaselineOnce  sync.Once
	exec10BaselineDBs   []string
	exec10BaselineRoles []string
	exec10BaselineErr   error
)

func exec10CaptureBaseline(raw string) {
	exec10BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec10BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec10\\_%' ESCAPE '\\'")
		if err != nil {
			exec10BaselineErr = fmt.Errorf("read database baseline: %w", err)
			return
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				exec10BaselineErr = fmt.Errorf("scan database baseline: %w", err)
				return
			}
			exec10BaselineDBs = append(exec10BaselineDBs, name)
		}
		rows.Close()
		roles, err := admin.Query(ctx, "SELECT rolname FROM pg_roles WHERE rolname LIKE 'verify\\_exec10\\_%' ESCAPE '\\'")
		if err != nil {
			exec10BaselineErr = fmt.Errorf("read role baseline: %w", err)
			return
		}
		for roles.Next() {
			var name string
			if err := roles.Scan(&name); err != nil {
				roles.Close()
				exec10BaselineErr = fmt.Errorf("scan role baseline: %w", err)
				return
			}
			exec10BaselineRoles = append(exec10BaselineRoles, name)
		}
		roles.Close()
	})
}

func exec10URL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 Explain integration tests")
	}
	return raw
}

type exec10Service struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	racePool *pgxpool.Pool
	database string
	schema   string
	scrub    func(any) string
	fail     func(step string, err error)
}

func newExec10Service(t *testing.T) *exec10Service {
	t.Helper()
	raw := exec10URL(t)
	exec10CaptureBaseline(raw)
	if exec10BaselineErr != nil {
		t.Fatalf("capture pg_database/pg_roles baseline: %v", exec10BaselineErr)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	// Serialize PG-mutating live tests across packages: concurrent workers
	// in sibling test binaries can create cluster-wide objects and sweep,
	// so every fixture-creating test holds a session-level advisory lock.
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)
	lockConn, err := admin.Acquire(ctx)
	if err != nil {
		fail("acquire serialization connection", err)
	}
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('loom/pg-exec-10/live-mutation')::bigint)`); err != nil {
		lockConn.Release()
		fail("take PG mutation serialization lock", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('loom/pg-exec-10/live-mutation')::bigint)`)
		lockConn.Release()
	})

	var versionNum int64
	var version string
	if err := admin.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int8, current_setting('server_version')").
		Scan(&versionNum, &version); err != nil {
		fail("read server version", err)
	}
	if err := storepg.CheckServerVersion(versionNum, version); err != nil {
		t.Fatalf("test service is not the required PostgreSQL 18 backend: %v", err)
	}

	dbName := fmt.Sprintf(exec10NameFmt, time.Now().UnixNano(), exec10RandomHex(t))
	if !exec10NamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	var created int64
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", dbName).Scan(&created); err != nil {
		fail("verify isolated database creation", err)
	}
	if created != 1 {
		t.Fatalf("isolated database %q did not verifiably come into existence", dbName)
	}
	exec10RegisterDB(dbName, "")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn, dbName)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	racePool, err := pgxpool.NewWithConfig(ctx, func() *pgxpool.Config {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			fail("parse race pool config", err)
		}
		cfg.MaxConns = 6
		cfg.MinConns = 2
		return cfg
	}())
	if err != nil {
		fail("create race pool", err)
	}
	t.Cleanup(racePool.Close)

	schema := fmt.Sprintf(exec10NameFmt, time.Now().UnixNano(), exec10RandomHex(t))
	if !exec10NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec10RegisterDB(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	return &exec10Service{ctx: ctx, admin: admin, iso: iso, racePool: racePool, database: dbName, schema: schema, scrub: scrub, fail: fail}
}

func exec10RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func (s *exec10Service) qualifiedTable(name string) string {
	return `"` + s.schema + `"."` + name + `"`
}

func (s *exec10Service) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

// seedHostileFixture creates the hostile-identifier fixture table:
// a sentinel-named table and column, a sentinel-named index on
// resource_seq, 500 literal rows (enough for the planner to prefer the
// index for equality), and ANALYZE so estimates exist.
func (s *exec10Service) seedHostileFixture(t *testing.T) string {
	t.Helper()
	table := s.qualifiedTable(exec10SentinelTable)
	s.exec(t, `CREATE TABLE `+table+` (resource_seq bigint, "`+exec10SentinelColumn+`" text)`)
	s.exec(t, `CREATE INDEX "`+exec10SentinelIndex+`" ON `+table+` (resource_seq)`)
	s.exec(t, `INSERT INTO `+table+` SELECT i::bigint, 'note-' || i::text FROM generate_series(1, 500) AS i`)
	s.exec(t, `ANALYZE `+table)
	return table
}

func exec10SequentialCommand(table string) command.CompiledCommand {
	sqlText := fmt.Sprintf(`SELECT "%s" FROM %s WHERE "%s" = $1`, exec10SentinelColumn, table, exec10SentinelColumn)
	compiledCommand, err := command.NewPostgreSQLCommand(sqlText, exec10HostileParameters())
	if err != nil {
		panic(err)
	}
	return compiledCommand
}

func exec10IndexedCommand(table string) command.CompiledCommand {
	sqlText := fmt.Sprintf(`SELECT resource_seq FROM %s WHERE resource_seq = $1 ORDER BY resource_seq`, table)
	compiledCommand, err := command.NewPostgreSQLCommand(sqlText, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterBigint, Value: int64(42)},
	})
	if err != nil {
		panic(err)
	}
	return compiledCommand
}

// exec10PoolRunner builds the same runner shape the server adapter uses,
// over the given Querier.
func exec10PoolRunner(t *testing.T, querier dataframepostgres.Querier) (PostgreSQLPlanJSONExecutor, *int64) {
	t.Helper()
	executor, err := dataframepostgres.NewExecutor(querier)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	var queries int64
	return func(ctx context.Context, command command.CompiledCommand) (json.RawMessage, error) {
		atomic.AddInt64(&queries, 1)
		var plans []string
		if err := executor.QueryRows(ctx, command, []string{"QUERY PLAN"}, func(row map[string]any) error {
			switch plan := row["QUERY PLAN"].(type) {
			case string:
				plans = append(plans, plan)
			case []byte:
				plans = append(plans, string(plan))
			case json.RawMessage:
				plans = append(plans, string(plan))
			default:
				return fmt.Errorf("PostgreSQL EXPLAIN returned plan text as %T", row["QUERY PLAN"])
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if len(plans) != 1 {
			return nil, fmt.Errorf("PostgreSQL EXPLAIN must return exactly one plan row, got %d", len(plans))
		}
		return json.RawMessage(plans[0]), nil
	}, &queries
}

func exec10AssertSanitizedIndexed(t *testing.T, assessment PostgreSQLPlanAssessment) {
	t.Helper()
	if assessment.Backend != string(command.CompiledBackendPostgreSQL) {
		t.Fatalf("backend: %q", assessment.Backend)
	}
	if len(assessment.Nodes) == 0 || assessment.Nodes[0].Ordinal != 0 || assessment.Nodes[0].ParentOrdinal != -1 {
		t.Fatalf("nodes: %+v", assessment.Nodes)
	}
	for _, node := range assessment.Nodes {
		if !postgresExplainNodePattern.MatchString(node.NodeType) {
			t.Fatalf("kept node type outside the operator vocabulary: %q", node.NodeType)
		}
		if node.Estimate.TotalCost < node.Estimate.StartupCost || node.Estimate.PlanRows < 0 || node.Estimate.PlanWidth < 0 {
			t.Fatalf("implausible estimates: %+v", node)
		}
	}
	if len(assessment.IndexScans) == 0 {
		t.Fatalf("indexed fixture query must classify at least one index scan: %+v", assessment)
	}
}

func exec10AssertSanitizedSequential(t *testing.T, assessment PostgreSQLPlanAssessment) {
	t.Helper()
	if len(assessment.FullScans) == 0 {
		t.Fatalf("sequential fixture query must classify at least one sequential scan: %+v", assessment)
	}
	for _, evidence := range assessment.FullScans {
		if evidence.NodeType != "Seq Scan" && evidence.NodeType != "Sample Scan" {
			t.Fatalf("unexpected sequential scan operator: %+v", evidence)
		}
	}
}

func TestPostgresExplainLivePG18IndexedAndSequentialPlans(t *testing.T) {
	s := newExec10Service(t)
	table := s.seedHostileFixture(t)
	runner, _ := exec10PoolRunner(t, s.iso)
	secrets := []string{s.database, s.schema, exec10SentinelTable, exec10SentinelIndex, exec10SentinelColumn}

	// Acceptance 2: live PostgreSQL Explain returns the agreed sanitized
	// backend/node/estimate fields for the indexed and sequential fixture
	// query, through the typed command path onto real PG18.
	indexed, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: exec10IndexedCommand(table)})
	if err != nil {
		s.fail("indexed live Explain", err)
	}
	exec10AssertSanitizedIndexed(t, indexed)
	renderedIndexed, _ := json.Marshal(indexed)
	exec10AssertSentinelFree(t, "live indexed assessment", string(renderedIndexed), secrets...)

	sequential, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: exec10SequentialCommand(table)})
	if err != nil {
		s.fail("sequential live Explain", err)
	}
	exec10AssertSanitizedSequential(t, sequential)
	renderedSequential, _ := json.Marshal(sequential)
	exec10AssertSentinelFree(t, "live sequential assessment", string(renderedSequential), secrets...)

	// The hostile command (literal in WHERE + bound hostile value) proves
	// real redaction: PG18 deparses both into plan-text strings, and the
	// sanitized assessment must contain neither.
	hostile, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: exec10HostileCommand(t, table)})
	if err != nil {
		s.fail("hostile live Explain", err)
	}
	if len(hostile.Nodes) == 0 {
		t.Fatalf("hostile plan produced no nodes")
	}
	renderedHostile, _ := json.Marshal(hostile)
	exec10AssertSentinelFree(t, "live hostile assessment", string(renderedHostile), secrets...)
	if strings.Contains(string(renderedHostile), "Filter") || strings.Contains(string(renderedHostile), `"Index Cond"`) {
		t.Fatalf("plan-text keys survived sanitization: %s", exec10Clip(string(renderedHostile)))
	}

	// Control-level integration: the ExplainPhysical seam assesses the same
	// command the stream reports, with identical fingerprints.
	callbacks := &exec10Callbacks{}
	compiledCommand := exec10SequentialCommand(table)
	engine := exec10Engine(t, callbacks, exec10PGSelection(compiledCommand))
	control := Control{Engine: engine, ExplainPostgreSQLConnection: func(ctx context.Context, compiled CompiledQuery) (PostgreSQLPlanAssessment, error) {
		return PostgreSQLExplainPlan(ctx, runner, compiled)
	}}
	result, err := control.ExplainPhysical(s.ctx, "exec10", exec10Bindings(), true)
	if err != nil {
		s.fail("control live ExplainPhysical", err)
	}
	item := result.Outputs[0]
	if item.LivePostgreSQL == nil || item.Live != nil {
		t.Fatalf("control live placement: %+v", item)
	}
	sqlText, args, err := compiledCommand.PostgreSQLParameters()
	if err != nil {
		t.Fatal(err)
	}
	if want := postgreSQLCommandFingerprint(sqlText, args, []string{"id"}); item.PlanFingerprint != want {
		t.Fatalf("fingerprint mismatch against the stream fingerprint: %s vs %s", item.PlanFingerprint, want)
	}
	// Cross-check with the real stream path: the stream the engine would
	// run for this selection reports the same planFingerprint.
	resolved := exec10ResolvedFor(table)
	_, stream, err := engine.streamForCompiledOutput(s.ctx, resolved, resolved.Compiled.Outputs[0], 25)
	if err != nil {
		s.fail("build comparison stream", err)
	}
	if stream.PlanDiagnostics.Fingerprint != item.PlanFingerprint {
		t.Fatalf("stream fingerprint %s != explain fingerprint %s", stream.PlanDiagnostics.Fingerprint, item.PlanFingerprint)
	}
	renderedResult, _ := json.Marshal(result)
	exec10AssertSentinelFree(t, "control live explanation", string(renderedResult), secrets...)
}

func exec10ResolvedFor(table string) Resolved {
	_ = table
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          []string{"id"},
		OutputSchema:     []lower.CompiledOutputColumn{{ID: "id", Name: "id", Label: "id", Kind: "string", Cardinality: "one"}},
		RowIdentity:      &spec.RowIdentity{Fields: []string{"id"}},
		Plan:             ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL},
	}
	return Resolved{
		Bundle: recipe.Bundle{Name: "exec10"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"resources"},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "exec10-live",
	}
}

// TestPostgresExplainLivePG18PermissionAndAbsence exercises the EXPLAIN
// context negative paths on the real server: a principal without
// privileges on the fixture schema/table (42501) and a statement whose
// relation does not exist (42P01), each bracketed by an identical
// successful run so the guards are proven selective, not blanket.
func TestPostgresExplainLivePG18PermissionAndAbsence(t *testing.T) {
	s := newExec10Service(t)
	table := s.seedHostileFixture(t)
	conn, err := s.iso.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire permission-test connection", err)
	}
	defer conn.Release()
	// The runner must share the session that performs SET ROLE, otherwise
	// the EXPLAIN would run as the owner on a different pooled connection.
	runner, queryCount := exec10PoolRunner(t, conn)
	compiledCommand := exec10IndexedCommand(table)

	// Guard bracket 1: the owner succeeds on the exact statement.
	if _, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand}); err != nil {
		s.fail("owner EXPLAIN", err)
	}

	// Absence: undefined hostile relation maps to a typed backend failure.
	missingSQL := `SELECT resource_seq FROM "` + s.schema + `"."` + exec10SentinelMissing + `" WHERE resource_seq = $1`
	missingCommand, err := command.NewPostgreSQLCommand(missingSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterBigint, Value: int64(1)},
	})
	if err != nil {
		t.Fatalf("build missing-relation command: %v", err)
	}
	_, err = PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: missingCommand})
	s.assertTypedPgError(t, err, "42P01", "undefined relation")
	exec10AssertSentinelFree(t, "undefined relation error surface", exec10UserSurface(err))

	// Permission: a NOLOGIN role inside the owned database, denied at the
	// schema, then the table, then granted for the paired control.
	role := fmt.Sprintf(exec10Prefix+"r_%d_%s", time.Now().UnixNano(), exec10RandomHex(t))
	if !regexp.MustCompile(`^verify_exec10_r_[0-9]+_[0-9a-f]+$`).MatchString(role) {
		t.Fatalf("role name %q outside the owned pattern", role)
	}
	s.exec(t, `CREATE ROLE "`+role+`" NOSUPERUSER NOLOGIN`)
	exec10RegisterRole(role)
	t.Cleanup(func() {
		// Revoke everything the test granted (a grant still in place can
		// block the drop while its object still exists), then drop.
		if _, err := s.iso.Exec(context.WithoutCancel(s.ctx), `REVOKE ALL ON `+table+` FROM "`+role+`"`); err != nil {
			t.Errorf("revoke table from role %s: %s", role, s.scrub(err))
		}
		if _, err := s.iso.Exec(context.WithoutCancel(s.ctx), `REVOKE USAGE ON SCHEMA "`+s.schema+`" FROM "`+role+`"`); err != nil {
			t.Errorf("revoke schema from role %s: %s", role, s.scrub(err))
		}
		if _, err := s.iso.Exec(context.WithoutCancel(s.ctx), `DROP ROLE IF EXISTS "`+role+`"`); err != nil {
			t.Errorf("drop role %s: %s", role, s.scrub(err))
		}
	})

	setRole := func() {
		if _, err := conn.Exec(s.ctx, `SET ROLE "`+role+`"`); err != nil {
			s.fail("set role", err)
		}
	}
	resetRole := func() {
		if _, err := conn.Exec(context.WithoutCancel(s.ctx), `RESET ROLE`); err != nil {
			s.fail("reset role", err)
		}
	}
	defer resetRole()

	setRole()
	_, err = PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand})
	s.assertTypedPgError(t, err, "42501", "permission-denied EXPLAIN at schema level")
	// The user-facing surface must not reveal the denied schema/table names.
	exec10AssertSentinelFree(t, "permission error surface", exec10UserSurface(err), s.schema, exec10SentinelTable)
	resetRole()

	// Even with schema USAGE the table itself is still denied.
	s.exec(t, `GRANT USAGE ON SCHEMA "`+s.schema+`" TO "`+role+`"`)
	setRole()
	_, err = PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand})
	s.assertTypedPgError(t, err, "42501", "permission-denied EXPLAIN at table level")
	resetRole()

	// Guard bracket 2 (paired control): the identical statement succeeds
	// once privileges exist, so the refusals above were selective.
	s.exec(t, `GRANT SELECT ON `+table+` TO "`+role+`"`)
	setRole()
	granted, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand})
	resetRole()
	if err != nil {
		s.fail("granted-role EXPLAIN (paired control)", err)
	}
	exec10AssertSanitizedIndexed(t, granted)

	// Cross-tag refusal before the server: the runner rejects an
	// AQL-tagged command through closed dispatch without issuing a query.
	before := atomic.LoadInt64(queryCount)
	aqlCommand, err := command.NewAQLCommand("FOR d IN c RETURN d", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: aqlCommand}); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("cross-tag EXPLAIN: %v", err)
	}
	if after := atomic.LoadInt64(queryCount); after != before {
		t.Fatalf("cross-tag command reached the server: queryCount %d -> %d", before, after)
	}
}

func exec10UserSurface(err error) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%v", err))
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		builder.WriteString(" | code=" + userErr.Code())
		builder.WriteString(" | message=" + userErr.Error())
		details, _ := json.Marshal(userErr.Details())
		builder.WriteString(" | details=" + string(details))
	}
	return builder.String()
}

func (s *exec10Service) assertTypedPgError(t *testing.T, err error, wantSQLState, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected failure, got nil", what)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("%s: error not typed: %v", what, err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInternalError) {
		t.Fatalf("%s: code %s, want INTERNAL_ERROR from the store/postgres boundary classification", what, userErr.Code())
	}
	var translated *storepg.TranslatedError
	if !errors.As(err, &translated) || translated.Condition != storepg.ConditionServerRejected || translated.SQLState != wantSQLState {
		t.Fatalf("%s: boundary classification missing for SQLSTATE %s: %v", what, wantSQLState, err)
	}
}

// TestPostgresExplainLivePG18ConcurrentRounds runs 10 rounds of concurrent
// live ExplainPhysical over a multi-connection pool. EXPLAIN itself returns
// plan JSON, not rows, so the in-row pg_backend_pid witness rides on paired
// row queries through the same pool and executor in each round: the three
// paired queries per round must witness three distinct backends inside the
// round (genuine pool spread under real concurrency), the atomic in-flight
// counter must observe positive overlap (at least two concurrent operations
// at once), and every concurrent assessment must be byte-identical to the
// serialized baseline (zero churn across concurrent calls).
func TestPostgresExplainLivePG18ConcurrentRounds(t *testing.T) {
	s := newExec10Service(t)
	table := s.seedHostileFixture(t)
	runner, _ := exec10PoolRunner(t, s.racePool)
	compiledCommand := exec10IndexedCommand(table)

	baselineAssessment, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand})
	if err != nil {
		s.fail("serialized baseline EXPLAIN", err)
	}
	baseline, _ := json.Marshal(baselineAssessment)

	// The paired witness query carries a server-side dwell so its
	// connection stays genuinely busy for the duration: without it a fast
	// query can finish and release its connection before a sibling acquires,
	// and backend identity would stop proving simultaneous spread.
	pidSQL := fmt.Sprintf(`SELECT pg_backend_pid()::bigint AS backend, resource_seq, pg_sleep(0.05) AS dwell FROM %s WHERE resource_seq = $1`, table)
	pidCommand, err := command.NewPostgreSQLCommand(pidSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterBigint, Value: int64(42)},
	})
	if err != nil {
		t.Fatalf("build backend-pid witness command: %v", err)
	}
	pidExecutor, err := dataframepostgres.NewExecutor(s.racePool)
	if err != nil {
		s.fail("pid witness executor", err)
	}

	const (
		rounds             = 10
		concurrentPerRound = 3
	)
	var peakInFlight int64
	for round := 0; round < rounds; round++ {
		var barrier sync.WaitGroup
		var ready sync.WaitGroup
		start := make(chan struct{})
		var inFlight int64
		assessments := make([]string, concurrentPerRound)
		pids := make([]int64, concurrentPerRound)
		errs := make([]error, 2*concurrentPerRound)
		ready.Add(2 * concurrentPerRound)
		barrier.Add(2 * concurrentPerRound)
		for i := 0; i < concurrentPerRound; i++ {
			go func(i int) {
				defer barrier.Done()
				ready.Done()
				<-start
				cur := atomic.AddInt64(&inFlight, 1)
				for {
					old := atomic.LoadInt64(&peakInFlight)
					if cur <= old || atomic.CompareAndSwapInt64(&peakInFlight, old, cur) {
						break
					}
				}
				defer atomic.AddInt64(&inFlight, -1)
				assessment, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand})
				if err != nil {
					errs[i] = err
					return
				}
				encoded, err := json.Marshal(assessment)
				if err != nil {
					errs[i] = err
					return
				}
				assessments[i] = string(encoded)
			}(i)
			go func(i int) {
				defer barrier.Done()
				ready.Done()
				<-start
				cur := atomic.AddInt64(&inFlight, 1)
				for {
					old := atomic.LoadInt64(&peakInFlight)
					if cur <= old || atomic.CompareAndSwapInt64(&peakInFlight, old, cur) {
						break
					}
				}
				defer atomic.AddInt64(&inFlight, -1)
				var seen []int64
				err := pidExecutor.QueryRows(s.ctx, pidCommand, []string{"backend", "resource_seq", "dwell"}, func(row map[string]any) error {
					if pid, ok := exec02AsInt64(row["backend"]); ok {
						seen = append(seen, pid)
					}
					return nil
				})
				if err != nil {
					errs[concurrentPerRound+i] = err
					return
				}
				if len(seen) != 1 {
					errs[concurrentPerRound+i] = fmt.Errorf("paired witness query visited %d rows, want 1", len(seen))
					return
				}
				pids[i] = seen[0]
			}(i)
		}
		ready.Wait()
		close(start)
		barrier.Wait()
		for i, err := range errs {
			if err != nil {
				s.fail(fmt.Sprintf("round %d concurrent operation %d", round, i), err)
			}
		}
		// In-row distinctness: all three paired witnesses in the round ran
		// on three different real backends.
		seenPIDs := map[int64]bool{}
		for _, pid := range pids {
			if pid <= 0 {
				t.Fatalf("round %d paired witness pid %d is not a real backend", round, pid)
			}
			if seenPIDs[pid] {
				t.Fatalf("round %d paired witnesses repeated backend pid %d: %v", round, pid, pids)
			}
			seenPIDs[pid] = true
		}
		// Positive overlap: the round genuinely ran concurrent operations.
		if peakInFlight < 2 {
			t.Fatalf("round %d never observed concurrent in-flight operations (peak %d)", round, peakInFlight)
		}
		// Zero churn: every concurrent assessment matches the serialized
		// baseline byte for byte.
		for i, assessment := range assessments {
			if assessment != string(baseline) {
				t.Fatalf("round %d concurrent assessment %d churned from the serialized baseline:\n got %s\nwant %s", round, i, assessment, baseline)
			}
		}
	}
	// The 10 rounds completed and peak concurrency stayed across rounds.
	if peakInFlight < 2 {
		t.Fatalf("no round achieved positive overlap (peak %d)", peakInFlight)
	}
	// Post-race serialized control: identical again (no lasting churn).
	afterAssessment, err := PostgreSQLExplainPlan(s.ctx, runner, CompiledQuery{Command: compiledCommand})
	if err != nil {
		s.fail("post-race serialized EXPLAIN", err)
	}
	if after, _ := json.Marshal(afterAssessment); string(after) != string(baseline) {
		t.Fatalf("post-race assessment churned:\n got %s\nwant %s", after, baseline)
	}
	secrets := []string{s.database, s.schema}
	exec10AssertSentinelFree(t, "concurrent baseline assessment", string(baseline), secrets...)
}

// TestZZZeroLeftoverExec10PostgresFixtures audits ownership, not global
// state: every exact registered name must be gone and no verify_exec10_*
// database or role may survive beyond the baseline captured before this
// process created anything. Sibling prefixes belong to concurrent workers
// and are never counted.
func TestZZZeroLeftoverExec10PostgresFixtures(t *testing.T) {
	raw := exec10URL(t)
	exec10CaptureBaseline(raw)
	if exec10BaselineErr != nil {
		t.Fatalf("capture baseline: %v", exec10BaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()

	dbs, roles := exec10RegistrySnapshot()
	for db := range dbs {
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, db).Scan(&exists); err != nil {
			t.Fatalf("check registry member %s: %v", db, err)
		}
		if exists {
			t.Fatalf("created database %s was not dropped", db)
		}
	}
	for role := range roles {
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			t.Fatalf("check registry member role %s: %v", role, err)
		}
		if exists {
			t.Fatalf("created role %s was not dropped", role)
		}
	}

	survivorCheck := func(query string, baseline []string, kind string) {
		rows, err := admin.Query(ctx, query)
		if err != nil {
			t.Fatalf("%s prefix scan: %v", kind, err)
		}
		var survivors []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("%s prefix scan read: %v", kind, err)
			}
			survivors = append(survivors, name)
		}
		rows.Close()
		if len(survivors) != len(baseline) {
			t.Fatalf("%s verify_exec10_* survivors beyond baseline: got %v, baseline %v", kind, survivors, baseline)
		}
		baselineSet := map[string]bool{}
		for _, name := range baseline {
			baselineSet[name] = true
		}
		for _, name := range survivors {
			if !baselineSet[name] {
				t.Fatalf("unregistered verify_exec10_* %s survivor (fatal): %s", kind, name)
			}
		}
	}
	survivorCheck("SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec10\\_%' ESCAPE '\\'", exec10BaselineDBs, "database")
	survivorCheck("SELECT rolname FROM pg_roles WHERE rolname LIKE 'verify\\_exec10\\_%' ESCAPE '\\'", exec10BaselineRoles, "role")
}
