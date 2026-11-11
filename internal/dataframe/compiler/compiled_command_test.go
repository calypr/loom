package compiler_test

// pg-ir-01 check-4 boundary witness (gap-closure unit pg-ir-01-check4-vacuity-fix).
//
// The ticket contract pins the backend-tagged compiled command at the root
// compiler package: check-4 runs "-run ^TestCompiledCommand$" against
// ./internal/dataframe/compiler. When the executable envelope moved into the
// closed leaf package internal/dataframe/compiler/command (merged
// df1a81b720cdcd4ab881959fa4eb4d6a4b8acda1, then hardened by arch-backend-01
// 7631dcce and arch-backend-11 a11b8efb), the root-side witness was orphaned
// and check-4 became a vacuous "[no tests to run]" green. A vacuous green is
// a blocker, never a pass, so this file restores the named check as a real
// recipe-boundary witness: it compiles through the production
// CompileResolvedRecipePlan/CompileRecipeOutput path (recipe_execution.go)
// and asserts the four acceptance sentences as observable behavior.
//
// Leaf-level validator detail stays in the command package's own matrix
// (command/compiled_command_test.go); what lives here is the boundary
// contract: what the recipe compiler publishes and what execution may or may
// not do with it. No production change belongs in this file's scope: the
// tagged union is the leaf's, and coupling_risk for this ticket says "do not
// invent a parallel command envelope".
//
// Sabotage mapping (each mutant is verified RED against these cells, then
// reverted GREEN; a vacuous test cannot go RED):
//   S1 leaf AQLParameters accepts a PostgreSQL tag      -> PostgreSQLTag... refuses-cross-tag cell
//   S2 target tags the PG statement with dropped args   -> PostgreSQLTag... ordered-arg contract
//   S3 recipe path mislabels its AQL render as PG       -> AQLTag... backend/payload cells
//   S4 Dispatch cross-fires the other adapter           -> ExhaustiveDispatch zero-counter cells

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

const (
	pgIR01Project    = "P1"
	pgIR01Generation = "G1"
)

// pgIR01ResolvedRecipe builds the minimal resolved recipe bundle the check-4
// boundary witnesses compile through the production recipe path.
func pgIR01ResolvedRecipe(t *testing.T) semantic.ResolvedRecipePlan {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: pgIR01Project, DatasetGeneration: pgIR01Generation}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "ir01_check4_boundary",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
		}},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	return resolved
}

func pgIR01AQLQueries(t *testing.T, limit int) []compiler.CompiledQuery {
	t.Helper()
	queries, err := compiler.CompileResolvedRecipePlan(
		compiler.ArangoTarget(), pgIR01ResolvedRecipe(t), limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan(ArangoTarget): %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(queries))
	}
	return queries
}

func pgIR01PostgreSQLQuery(t *testing.T) compiler.CompiledQuery {
	t.Helper()
	output, bindings := archBackend11FingerprintFixture()
	query, err := compiler.CompileRecipeOutput(
		archBackend11PostgreSQLTarget(t), output, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileRecipeOutput(PostgreSQLTarget): %v", err)
	}
	return query
}

// pgIR01PlaceholderScan reports the highest $n placeholder and whether every
// positional placeholder from $1..$max appears exactly once in the statement.
func pgIR01PlaceholderScan(t *testing.T, sqlText string) (int, bool) {
	t.Helper()
	matches := regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(sqlText, -1)
	seen := map[int]int{}
	maxPosition := 0
	for _, match := range matches {
		position := 0
		for _, digit := range match[1] {
			position = position*10 + int(digit-'0')
		}
		seen[position]++
		if position > maxPosition {
			maxPosition = position
		}
	}
	if len(matches) != maxPosition {
		return maxPosition, false
	}
	for position := 1; position <= maxPosition; position++ {
		if seen[position] != 1 {
			return maxPosition, false
		}
	}
	return maxPosition, true
}

func TestCompiledCommand(t *testing.T) {
	t.Run("AQLTagCarriesRenderedPayloadUnchanged", func(t *testing.T) {
		compiled := pgIR01AQLQueries(t, 7)[0]
		if compiled.Command.Backend() != command.CompiledBackendAQL {
			t.Fatalf("recipe path tagged the command %q, want %q", compiled.Command.Backend(), command.CompiledBackendAQL)
		}
		query, binds, err := compiled.Command.AQLParameters()
		if err != nil {
			t.Fatalf("AQLParameters: %v", err)
		}
		if strings.TrimSpace(query) == "" || len(binds) == 0 {
			t.Fatalf("recipe path published an empty AQL payload: %q %v", query, binds)
		}
		// The values the recipe path published are exactly what a fresh
		// render of the retained finalized plan yields: the tag froze the
		// canonical renderer payload, changed by nothing.
		rendered, err := aql.RenderPhysicalPlan(compiled.PhysicalPlan)
		if err != nil {
			t.Fatalf("render retained plan: %v", err)
		}
		if query != rendered.Query {
			t.Fatalf("published AQL drifted from the canonical render:\npublished: %q\ncanonical: %q", query, rendered.Query)
		}
		if !reflect.DeepEqual(binds, rendered.BindVars) {
			t.Fatalf("published binds drifted from the canonical render: %v vs %v", binds, rendered.BindVars)
		}
		// The documented AQL contract carries the named bind map instance
		// unchanged to the adapter: repeated dispatch reads observe the same
		// map, not a per-read rebuild.
		_, bindsAgain, err := compiled.Command.AQLParameters()
		if err != nil {
			t.Fatalf("repeated AQLParameters: %v", err)
		}
		if reflect.ValueOf(binds).Pointer() != reflect.ValueOf(bindsAgain).Pointer() {
			t.Fatal("AQL dispatch must carry the exact bind map instance unchanged")
		}
		// Determinism: a second compile of the same request publishes the
		// identical executable payload.
		second := pgIR01AQLQueries(t, 7)[0]
		query2, binds2, err := second.Command.AQLParameters()
		if err != nil {
			t.Fatalf("second compile AQLParameters: %v", err)
		}
		if query2 != query || !reflect.DeepEqual(binds2, binds) {
			t.Fatalf("recipe compilation is not deterministic:\n%q vs %q\n%v vs %v", query2, query, binds2, binds)
		}
	})

	t.Run("PostgreSQLTagCarriesSQLAndOrderedTypedArgs", func(t *testing.T) {
		compiled := pgIR01PostgreSQLQuery(t)
		if compiled.Command.Backend() != command.CompiledBackendPostgreSQL {
			t.Fatalf("selected PostgreSQL target produced %q", compiled.Command.Backend())
		}
		sqlText, args, err := compiled.Command.PostgreSQLParameters()
		if err != nil {
			t.Fatalf("PostgreSQLParameters: %v", err)
		}
		// The frozen statement contract for the canonical fixture plan. The
		// SQL text and plan fingerprint are the same witnesses the
		// fingerprint golden pins, so this cell cannot silently re-anchor.
		if sqlText != archBackend11BaseSQL {
			t.Fatalf("statement SQL drifted from the frozen contract:\ngot:  %q\nwant: %q", sqlText, archBackend11BaseSQL)
		}
		if compiled.PlanDiagnostics.Fingerprint != archBackend11BaseFingerprint {
			t.Fatalf("plan fingerprint drifted: %q want %q", compiled.PlanDiagnostics.Fingerprint, archBackend11BaseFingerprint)
		}
		wantTypes := []command.PostgreSQLParameterType{
			command.PostgreSQLParameterText, command.PostgreSQLParameterText,
			command.PostgreSQLParameterJSONB, command.PostgreSQLParameterText, command.PostgreSQLParameterText,
		}
		if len(args) != len(wantTypes) {
			t.Fatalf("ordered argument count = %d, want %d: %#v", len(args), len(wantTypes), args)
		}
		wantValues := []any{"P1", "G1", json.RawMessage(`["FHIR/Patient"]`), "Patient", "{gender}"}
		for index := range args {
			if args[index].Type != wantTypes[index] {
				t.Fatalf("argument %d type = %q, want %q", index, args[index].Type, wantTypes[index])
			}
			if args[index].Value == nil {
				t.Fatalf("argument %d carries a nil value under the non-null type %q", index, args[index].Type)
			}
			if !reflect.DeepEqual(args[index].Value, wantValues[index]) {
				t.Fatalf("argument %d value = %#v, want %#v", index, args[index].Value, wantValues[index])
			}
		}
		// Every positional placeholder is present exactly once and the
		// placeholder count matches the ordered argument count: SQL text and
		// arguments are one contract, not two.
		maxPosition, complete := pgIR01PlaceholderScan(t, sqlText)
		if !complete || maxPosition != len(args) {
			t.Fatalf("placeholders $1..$%d (complete=%t) do not match %d ordered arguments", maxPosition, complete, len(args))
		}
		// Copy-on-read: mutating what the accessor handed out must not
		// reorder the statement's positional bindings for a later dispatch.
		args[0].Value = "Corrupted"
		_, reread, err := compiled.Command.PostgreSQLParameters()
		if err != nil || !reflect.DeepEqual(reread[0].Value, "P1") {
			t.Fatalf("PostgreSQLParameters must return a fresh copy on every read: %#v %v", reread, err)
		}
		// The acceptance guarantee at the recipe boundary: this PostgreSQL
		// command is refused before it can reach the AQL
		// QueryRows(query, map) adapter, and the refusal hands back nothing
		// an adapter could execute.
		aqlQuery, aqlBinds, err := compiled.Command.AQLParameters()
		if !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("PostgreSQL command to AQL adapter error = %v, want mismatch", err)
		}
		if aqlQuery != "" || aqlBinds != nil {
			t.Fatalf("refused AQL dispatch must return zero values, got %q %v", aqlQuery, aqlBinds)
		}
		if !strings.Contains(err.Error(), "AQL") {
			t.Fatalf("mismatch error must name the refused AQL adapter: %v", err)
		}
		// Determinism: a second compile publishes the identical SQL, ordered
		// arguments, and fingerprint.
		second := pgIR01PostgreSQLQuery(t)
		sql2, args2, err := second.Command.PostgreSQLParameters()
		if err != nil {
			t.Fatalf("second compile PostgreSQLParameters: %v", err)
		}
		if sql2 != sqlText || !reflect.DeepEqual(args2, reread) || second.PlanDiagnostics.Fingerprint != compiled.PlanDiagnostics.Fingerprint {
			t.Fatalf("PostgreSQL compilation is not deterministic:\n%q vs %q\n%#v vs %#v", sql2, sqlText, args2, reread)
		}
	})

	t.Run("ExhaustiveDispatchRejectsMismatchBeforeExecution", func(t *testing.T) {
		aqlCommand := pgIR01AQLQueries(t, 7)[0].Command
		pgCommand := pgIR01PostgreSQLQuery(t).Command
		aqlCalls, pgCalls := 0, 0
		aqlTrap := func(command.AQLCommand) error { aqlCalls++; return nil }
		pgTrap := func(command.PostgreSQLCommand) error { pgCalls++; return nil }

		// A PostgreSQL command dispatched without its own adapter is the
		// fail-closed capability gap; the AQL adapter must stay untouched.
		if err := pgCommand.Dispatch(aqlTrap, nil); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("PostgreSQL command without PostgreSQL adapter error = %v, want backend-not-registered", err)
		}
		if aqlCalls != 0 || pgCalls != 0 {
			t.Fatalf("mismatched Dispatch executed adapters: aql=%d postgresql=%d", aqlCalls, pgCalls)
		}
		// Mirror: an AQL command without its own adapter never fires the
		// PostgreSQL adapter.
		if err := aqlCommand.Dispatch(nil, pgTrap); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("AQL command without AQL adapter error = %v, want backend-not-registered", err)
		}
		if aqlCalls != 0 || pgCalls != 0 {
			t.Fatalf("mismatched Dispatch executed adapters: aql=%d postgresql=%d", aqlCalls, pgCalls)
		}
		// A zero-tag command dispatches nowhere and executes nothing.
		var zero command.CompiledCommand
		if _, _, err := zero.AQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-tag AQL dispatch error = %v, want backend-not-registered", err)
		}
		if _, _, err := zero.PostgreSQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-tag PostgreSQL dispatch error = %v, want backend-not-registered", err)
		}
		if err := zero.Dispatch(aqlTrap, pgTrap); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-tag Dispatch error = %v, want backend-not-registered", err)
		}
		if aqlCalls != 0 || pgCalls != 0 {
			t.Fatalf("zero-tag Dispatch executed adapters: aql=%d postgresql=%d", aqlCalls, pgCalls)
		}
		// Non-vacuity: when the tag legitimately matches, its own adapter
		// fires exactly once and the other backend's adapter still fires
		// zero times (the cross-fire witness).
		if err := aqlCommand.Dispatch(aqlTrap, pgTrap); err != nil {
			t.Fatalf("matching AQL Dispatch: %v", err)
		}
		if aqlCalls != 1 || pgCalls != 0 {
			t.Fatalf("AQL Dispatch adapter calls aql=%d postgresql=%d, want 1 and 0", aqlCalls, pgCalls)
		}
		if err := pgCommand.Dispatch(aqlTrap, pgTrap); err != nil {
			t.Fatalf("matching PostgreSQL Dispatch: %v", err)
		}
		if aqlCalls != 1 || pgCalls != 1 {
			t.Fatalf("PostgreSQL Dispatch adapter calls aql=%d postgresql=%d, want 1 and 1", aqlCalls, pgCalls)
		}
	})

	t.Run("AQLCallPathRetainsExactBehavior", func(t *testing.T) {
		compiled := pgIR01AQLQueries(t, 7)[0]
		// The AQL call path keeps its exact metadata contract: physical plan
		// mode, the generic recipe profile, and the caller-visible limit.
		if compiled.PlanMode != "physical" || compiled.PlanProfile != "generic_fhir_graph_recipe" {
			t.Fatalf("AQL plan metadata changed: mode=%q profile=%q", compiled.PlanMode, compiled.PlanProfile)
		}
		if compiled.Limit != 7 {
			t.Fatalf("AQL limit not preserved: %d, want 7", compiled.Limit)
		}
		if len(compiled.Columns) == 0 || len(compiled.PublicColumns) == 0 {
			t.Fatalf("AQL output lost its column contract: columns=%v public=%v", compiled.Columns, compiled.PublicColumns)
		}
		query, binds, err := compiled.Command.AQLParameters()
		if err != nil {
			t.Fatalf("AQLParameters: %v", err)
		}
		// The query is canonical AQL: root traversal with named binds; the
		// scope binds are carried, not inlined.
		if !strings.Contains(query, "FOR root IN") || !strings.Contains(query, "@project") {
			t.Fatalf("AQL path drifted from canonical AQL rendering: %q", query)
		}
		if binds["project"] != pgIR01Project || binds["dataset_generation"] != pgIR01Generation {
			t.Fatalf("AQL scope binds lost: %v", binds)
		}
		hasCollectionBind := false
		for key := range binds {
			if strings.HasPrefix(key, "@") {
				hasCollectionBind = true
			}
		}
		if !hasCollectionBind {
			t.Fatalf("AQL collection bind missing: %v", binds)
		}
		// The paired PostgreSQL boundary never borrows the AQL accelerators
		// or a named bind map: those are AQL-target-only privileges of the
		// recipe path.
		pg := pgIR01PostgreSQLQuery(t)
		if pg.PreviewCoveringIndex != nil || pg.PreviewGroupScan != nil || pg.PreviewGroupProjection != nil {
			t.Fatalf("PostgreSQL output carries AQL-only preview accelerators: index=%v scan=%v projection=%v",
				pg.PreviewCoveringIndex, pg.PreviewGroupScan, pg.PreviewGroupProjection)
		}
		if _, binds, err := pg.Command.AQLParameters(); err == nil || binds != nil {
			t.Fatalf("PostgreSQL command must never expose a named bind map: %v %v", binds, err)
		}
	})
}
