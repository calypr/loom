package compiler

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompiledCommand(t *testing.T) {
	t.Run("AQLTagCarriesQueryAndBindMapUnchanged", func(t *testing.T) {
		bindVars := map[string]any{"@@collection": "Patient", "limit": 7}
		command, err := NewAQLCommand("FOR d IN @@collection RETURN d", bindVars)
		if err != nil {
			t.Fatalf("NewAQLCommand: %v", err)
		}
		if command.Backend() != CompiledBackendAQL {
			t.Fatalf("backend = %q, want %q", command.Backend(), CompiledBackendAQL)
		}
		query, gotBinds, err := command.AQLParameters()
		if err != nil {
			t.Fatalf("AQLParameters: %v", err)
		}
		if query != "FOR d IN @@collection RETURN d" {
			t.Fatalf("query = %q", query)
		}
		if reflect.ValueOf(gotBinds).Pointer() != reflect.ValueOf(bindVars).Pointer() {
			t.Fatal("AQL dispatch must carry the exact bind map instance unchanged")
		}
		if _, _, err := command.PostgreSQLParameters(); !errors.Is(err, ErrCompiledCommandBackendMismatch) {
			t.Fatalf("AQL command to PostgreSQL dispatch error = %v, want mismatch", err)
		}
	})

	t.Run("PostgreSQLTagCarriesSQLAndOrderedTypedArgs", func(t *testing.T) {
		args := []PostgreSQLParameter{
			{Type: PostgreSQLParameterText, Value: "Patient"},
			{Type: PostgreSQLParameterBigint, Value: int64(7)},
			{Type: PostgreSQLParameterNull, Value: nil},
			{Type: PostgreSQLParameterJSONB, Value: json.RawMessage("null")},
		}
		command, err := NewPostgreSQLCommand("SELECT $1, $2, $3, $4", args)
		if err != nil {
			t.Fatalf("NewPostgreSQLCommand: %v", err)
		}
		if command.Backend() != CompiledBackendPostgreSQL {
			t.Fatalf("backend = %q, want %q", command.Backend(), CompiledBackendPostgreSQL)
		}
		sql, gotArgs, err := command.PostgreSQLParameters()
		if err != nil {
			t.Fatalf("PostgreSQLParameters: %v", err)
		}
		if sql != "SELECT $1, $2, $3, $4" {
			t.Fatalf("sql = %q", sql)
		}
		if !reflect.DeepEqual(gotArgs, args) {
			t.Fatalf("ordered args changed: %#v", gotArgs)
		}
		if gotArgs[0].Value != "Patient" || gotArgs[1].Value != int64(7) ||
			gotArgs[2].Type != PostgreSQLParameterNull || gotArgs[2].Value != nil {
			t.Fatalf("argument order or null/JSON-null distinction lost: %#v", gotArgs)
		}
		// Caller mutation after construction must not reorder the statement.
		args[0].Value = "Observation"
		if _, reread, err := command.PostgreSQLParameters(); err != nil || reread[0].Value != "Patient" {
			t.Fatalf("args must be copied at construction: %#v %v", reread, err)
		}
		// The accessor hands out a fresh copy, unlike AQL's documented shared
		// bind map: mutating the returned slice must not corrupt the
		// command's positional bindings for a later dispatch.
		gotArgs[0].Value = "Corrupted"
		if _, afterMutate, err := command.PostgreSQLParameters(); err != nil || afterMutate[0].Value != "Patient" {
			t.Fatalf("PostgreSQLParameters must return a fresh copy on every read: %#v %v", afterMutate, err)
		}
		// The acceptance guarantee: PostgreSQL SQL is rejected before it can
		// reach the AQL QueryRows(query, bindVars) adapter.
		if _, _, err := command.AQLParameters(); !errors.Is(err, ErrCompiledCommandBackendMismatch) {
			t.Fatalf("PostgreSQL command to AQL dispatch error = %v, want mismatch", err)
		} else if !strings.Contains(err.Error(), "AQL") {
			t.Fatalf("mismatch error must name the refused AQL adapter: %v", err)
		}
	})

	t.Run("ExhaustiveDispatchRejectsMismatchedPairsBeforeExecution", func(t *testing.T) {
		var zero CompiledCommand
		if zero.Backend() != CompiledBackend("") {
			t.Fatalf("zero-value backend = %q", zero.Backend())
		}
		if _, _, err := zero.AQLParameters(); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-value AQL dispatch error = %v, want unregistered backend", err)
		}
		if _, _, err := zero.PostgreSQLParameters(); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-value PostgreSQL dispatch error = %v, want unregistered backend", err)
		}
		aqlCalls, pgCalls := 0, 0
		if err := zero.Dispatch(func(AQLCommand) error { aqlCalls++; return nil }, func(PostgreSQLCommand) error { pgCalls++; return nil }); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-value Dispatch error = %v, want unregistered backend", err)
		}
		if aqlCalls != 0 || pgCalls != 0 {
			t.Fatalf("unregistered backend invoked adapters: aql=%d postgresql=%d", aqlCalls, pgCalls)
		}

		aqlCommand, err := NewAQLCommand("RETURN 1", nil)
		if err != nil {
			t.Fatalf("NewAQLCommand: %v", err)
		}
		if err := aqlCommand.Dispatch(nil, func(PostgreSQLCommand) error { pgCalls++; return nil }); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("AQL command without AQL adapter error = %v, want backend-not-registered", err)
		}
		if pgCalls != 0 {
			t.Fatal("mismatched Dispatch must not run the other backend's adapter")
		}
		if err := aqlCommand.Dispatch(func(AQLCommand) error { aqlCalls++; return nil }, nil); err != nil {
			t.Fatalf("matching Dispatch: %v", err)
		}
		if aqlCalls != 1 {
			t.Fatalf("matching AQL Dispatch adapter calls = %d, want 1", aqlCalls)
		}

		pgCommand, err := NewPostgreSQLCommand("SELECT 1", nil)
		if err != nil {
			t.Fatalf("NewPostgreSQLCommand: %v", err)
		}
		if err := pgCommand.Dispatch(func(AQLCommand) error { aqlCalls++; return nil }, nil); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("PostgreSQL command without PostgreSQL adapter error = %v, want backend-not-registered", err)
		}
		// Missing adapter registration is the fail-closed capability gap, not
		// a routing defect: it must stay distinguishable from Mismatch.
		aqlOnly, err := NewAQLCommand("RETURN 1", nil)
		if err != nil {
			t.Fatalf("NewAQLCommand: %v", err)
		}
		if err := aqlOnly.Dispatch(nil, nil); errors.Is(err, ErrCompiledCommandBackendMismatch) {
			t.Fatalf("missing AQL adapter misclassified as mismatch: %v", err)
		}
		pgOnly, err := NewPostgreSQLCommand("SELECT 1", nil)
		if err != nil {
			t.Fatalf("NewPostgreSQLCommand: %v", err)
		}
		if err := pgOnly.Dispatch(nil, nil); errors.Is(err, ErrCompiledCommandBackendMismatch) {
			t.Fatalf("missing PostgreSQL adapter misclassified as mismatch: %v", err)
		}
		if aqlCalls != 1 {
			t.Fatal("mismatched Dispatch must not run the AQL adapter")
		}
	})

	t.Run("BackendGateRegistersOnlyAQLAndFailsClosedForPostgreSQL", func(t *testing.T) {
		rendered := aql.RenderedPhysicalPlan{Query: "FOR d IN @@collection RETURN d", BindVars: map[string]any{"@@collection": "Patient"}}
		command, err := CompiledCommandForBackend(CompiledBackendAQL, rendered)
		if err != nil {
			t.Fatalf("CompiledCommandForBackend(AQL): %v", err)
		}
		query, bindVars, err := command.AQLParameters()
		if err != nil || query != rendered.Query || !reflect.DeepEqual(bindVars, rendered.BindVars) {
			t.Fatalf("AQL gate changed rendered result: %q vs %q, %v, %v", query, rendered.Query, bindVars, err)
		}
		// Planner-facing fail-closed guarantee: until a PostgreSQL renderer is
		// implemented and registered, a PostgreSQL request yields no command
		// and never falls back to the AQL rendering.
		if fallback, err := CompiledCommandForBackend(CompiledBackendPostgreSQL, rendered); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("PostgreSQL gate error = %v, want not-registered", err)
		} else if fallback.Backend() != CompiledBackend("") {
			t.Fatalf("PostgreSQL request produced a fallback command tagged %q", fallback.Backend())
		}
		for _, backend := range []CompiledBackend{"", "CLICKHOUSE", "aql", "postgresql", "MYSQL"} {
			if _, err := CompiledCommandForBackend(backend, rendered); !errors.Is(err, ErrCompiledCommandBackendNotRegistered) {
				t.Fatalf("CompiledCommandForBackend(%q) error = %v, want not-registered", backend, err)
			}
		}
	})

	t.Run("ConstructorsRejectMalformedTagPayloadPairs", func(t *testing.T) {
		if _, err := NewAQLCommand("   ", nil); err == nil {
			t.Fatal("AQL command with blank query must be rejected")
		}
		if _, err := NewPostgreSQLCommand("", nil); err == nil {
			t.Fatal("PostgreSQL command with blank SQL must be rejected")
		}
		cases := []struct {
			name string
			args []PostgreSQLParameter
		}{
			{"unregistered type", []PostgreSQLParameter{{Type: "money", Value: "1.00"}}},
			{"nil value under typed parameter", []PostgreSQLParameter{{Type: PostgreSQLParameterText, Value: nil}}},
			{"value under SQL null", []PostgreSQLParameter{{Type: PostgreSQLParameterNull, Value: "x"}}},
		}
		for _, testCase := range cases {
			if _, err := NewPostgreSQLCommand("SELECT $1", testCase.args); err == nil {
				t.Fatalf("%s: malformed argument accepted", testCase.name)
			}
		}
	})

	t.Run("RecipePathPublishesOnlyDispatchedAQLCommand", func(t *testing.T) {
		bindings := recipe.RuntimeBindings{Project: "command-project", DatasetGeneration: "command-generation"}
		bundle := recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "command_tag",
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
		queries, err := CompileResolvedRecipePlanWithPolicy(resolved, 7, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("CompileResolvedRecipePlanWithPolicy: %v", err)
		}
		if len(queries) != 1 {
			t.Fatalf("compiled outputs = %d, want 1", len(queries))
		}
		compiled := queries[0]
		if strings.TrimSpace(testAQLText(t, compiled.Command)) == "" || len(testAQLBinds(t, compiled.Command)) == 0 {
			t.Fatalf("recipe path lost rendered command: %#v", compiled)
		}
		// The values the recipe path published are exactly what an
		// AQL-tagged dispatch of the retained plan yields.
		rendered, err := aql.RenderPhysicalPlan(compiled.PhysicalPlan)
		if err != nil {
			t.Fatalf("render retained plan: %v", err)
		}
		command, err := CompiledCommandForBackend(CompiledBackendAQL, rendered)
		if err != nil {
			t.Fatalf("CompiledCommandForBackend(AQL): %v", err)
		}
		query, bindVars, err := command.AQLParameters()
		if err != nil {
			t.Fatalf("AQLParameters: %v", err)
		}
		if query != testAQLText(t, compiled.Command) || !reflect.DeepEqual(bindVars, testAQLBinds(t, compiled.Command)) {
			t.Fatalf("recipe-published command differs from dispatched AQL command: queryMatch=%t bindMatch=%t",
				query == testAQLText(t, compiled.Command), reflect.DeepEqual(bindVars, testAQLBinds(t, compiled.Command)))
		}
		// The identical text, if it ever arrived tagged PostgreSQL, is
		// rejected before the AQL QueryRows(query, bindVars) adapter.
		mislabelled, err := NewPostgreSQLCommand(testAQLText(t, compiled.Command), []PostgreSQLParameter{{Type: PostgreSQLParameterJSONB, Value: json.RawMessage(`{"limit":7}`)}})
		if err != nil {
			t.Fatalf("NewPostgreSQLCommand: %v", err)
		}
		if _, _, err := mislabelled.AQLParameters(); !errors.Is(err, ErrCompiledCommandBackendMismatch) {
			t.Fatalf("PostgreSQL-tagged SQL to AQL adapter error = %v, want mismatch", err)
		}
	})
}

// TestCompiledCommandWithAQLBinds pins the closed per-page bind override
// seam: existing-key-only same-type overrides on a fresh copied map, with
// named fail-closed refusals for every other shape.
func TestCompiledCommandWithAQLBinds(t *testing.T) {
	aqlCommand, err := NewAQLCommand("FOR d IN @@collection FILTER d.p == @p LIMIT @size RETURN d", map[string]any{
		"@@collection": "Patient",
		"p":            "alpha",
		"size":         10,
		"nilbind":      nil,
	})
	if err != nil {
		t.Fatalf("NewAQLCommand: %v", err)
	}
	t.Run("OverrideCopiesWithoutMutatingOriginal", func(t *testing.T) {
		updated, err := aqlCommand.WithAQLBinds(map[string]any{"size": 25, "p": "beta"})
		if err != nil {
			t.Fatalf("WithAQLBinds: %v", err)
		}
		if updated.Backend() != CompiledBackendAQL {
			t.Fatalf("override changed the tag to %q", updated.Backend())
		}
		query, binds, err := updated.AQLParameters()
		if err != nil {
			t.Fatalf("AQLParameters: %v", err)
		}
		if query != "FOR d IN @@collection FILTER d.p == @p LIMIT @size RETURN d" {
			t.Fatalf("override changed the query text: %q", query)
		}
		if binds["size"] != 25 || binds["p"] != "beta" {
			t.Fatalf("overrides not applied: %v", binds)
		}
		if binds["@@collection"] != "Patient" || binds["nilbind"] != nil {
			t.Fatalf("untouched binds lost: %v", binds)
		}
		_, originalBinds, err := aqlCommand.AQLParameters()
		if err != nil {
			t.Fatalf("original AQLParameters: %v", err)
		}
		if originalBinds["size"] != 10 || originalBinds["p"] != "alpha" {
			t.Fatalf("override mutated the original command binds: %v", originalBinds)
		}
		// The returned command must own a fresh map: driving the original's
		// accessor map to a sentinel must not move the override result.
		if _, originalBinds, err := aqlCommand.AQLParameters(); err != nil {
			t.Fatalf("original AQLParameters: %v", err)
		} else {
			originalBinds["size"] = 999
		}
		_, updatedBinds, err := updated.AQLParameters()
		if err != nil {
			t.Fatalf("recheck AQLParameters: %v", err)
		}
		if updatedBinds["size"] != 25 {
			t.Fatalf("returned command does not own a fresh bind map: %v", updatedBinds)
		}
	})
	failures := []struct {
		name      string
		command   CompiledCommand
		overrides map[string]any
		sentinel  error
	}{
		{"UnknownKeyRefused", aqlCommand, map[string]any{"missing": 1}, ErrCompiledCommandBackendMismatch},
		{"TypeMismatchRefused", aqlCommand, map[string]any{"size": "25"}, ErrCompiledCommandBackendMismatch},
		{"NilOverrideRefused", aqlCommand, map[string]any{"size": nil}, ErrCompiledCommandBackendMismatch},
		{"NilCurrentValueRefused", aqlCommand, map[string]any{"nilbind": 1}, ErrCompiledCommandBackendMismatch},
		{"EmptyOverridesRefused", aqlCommand, map[string]any{}, ErrCompiledCommandBackendMismatch},
		{"NilOverridesRefused", aqlCommand, nil, ErrCompiledCommandBackendMismatch},
		{"PostgreSQLTagRefused", mustPostgreSQLCommandForBindOverrides(t), map[string]any{"size": 5}, ErrCompiledCommandBackendMismatch},
		{"ZeroTagRefused", CompiledCommand{}, map[string]any{"size": 5}, ErrCompiledCommandBackendNotRegistered},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.command.WithAQLBinds(tc.overrides); !errors.Is(err, tc.sentinel) {
				t.Fatalf("WithAQLBinds error = %v, want %v", err, tc.sentinel)
			}
		})
	}
}

func mustPostgreSQLCommandForBindOverrides(t *testing.T) CompiledCommand {
	t.Helper()
	command, err := NewPostgreSQLCommand("SELECT id FROM t WHERE p = $1", []PostgreSQLParameter{{Type: PostgreSQLParameterText, Value: "alpha"}})
	if err != nil {
		t.Fatalf("NewPostgreSQLCommand: %v", err)
	}
	return command
}
