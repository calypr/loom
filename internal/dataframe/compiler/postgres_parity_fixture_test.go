package compiler_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// TestPostgresParityFixture is the pg-ir-02 cross-backend parity guard. It
// pins the boundary the two renderers must never cross:
//
//   - The Arango/AQL path is unchanged: the canonical recipe pipeline still
//     renders through the AQL renderer and dispatches only as an AQL-tagged
//     command.
//   - The PostgreSQL path renders only its closed supported surface as
//     parameterized SQL with no AQL bind syntax, and rejects everything else
//     with a typed ErrUnsupportedPostgresPlan. It never emits AQL text and
//     never converts AQL to SQL.
//   - The planner-facing gate from pg-ir-01 stays fail-closed: a PostgreSQL
//     request through CompiledCommandForBackend yields no command until a
//     PostgreSQL command adapter is registered by a later ticket.
func TestPostgresParityFixture(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "parity-project", DatasetGeneration: "parity-generation"}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "parity_fixture",
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
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlanWithPolicy: %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(queries))
	}
	compiled := queries[0]

	t.Run("AQLPathIsUnchanged", func(t *testing.T) {
		rendered, err := aql.RenderPhysicalPlan(compiled.PhysicalPlan)
		if err != nil {
			t.Fatalf("AQL render of the retained plan: %v", err)
		}
		if rendered.Query != testAQLText(t, compiled.Command) {
			t.Fatalf("AQL renderer output drifted from the compiled query")
		}
		if !strings.Contains(rendered.Query, "@") {
			t.Fatalf("expected the AQL path to keep its named bind syntax")
		}
		command, err := compiler.CompiledCommandForBackend(compiler.CompiledBackendAQL, rendered)
		if err != nil {
			t.Fatalf("AQL command registration: %v", err)
		}
		if _, _, err := command.PostgreSQLParameters(); !errors.Is(err, compiler.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("AQL command to the PostgreSQL executor error = %v, want mismatch", err)
		}
	})

	t.Run("PostgreSQLGateStaysFailClosed", func(t *testing.T) {
		// Planner capability discovery must keep reporting PostgreSQL as
		// unsupported until a command adapter registers; no AQL fallback.
		rendered, err := aql.RenderPhysicalPlan(compiled.PhysicalPlan)
		if err != nil {
			t.Fatalf("AQL render: %v", err)
		}
		if _, err := compiler.CompiledCommandForBackend(compiler.CompiledBackendPostgreSQL, rendered); !errors.Is(err, compiler.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("PostgreSQL request error = %v, want not-registered", err)
		}
	})

	t.Run("PostgreSQLRendererNeverFallsBackToAQL", func(t *testing.T) {
		storage, err := ir.NewPostgresStorageBinding("pgir02_it_parity")
		if err != nil {
			t.Fatalf("NewPostgresStorageBinding: %v", err)
		}
		pgRendered, pgErr := postgresrender.RenderPhysicalPlan(compiled.PhysicalPlan, storage)
		if pgErr == nil {
			// Inside the supported surface: the output must be a clean
			// parameterized statement that cannot be the AQL text.
			if strings.Contains(pgRendered.SQL, "@") {
				t.Fatalf("PostgreSQL SQL carries AQL bind syntax: %s", pgRendered.SQL)
			}
			if pgRendered.SQL == testAQLText(t, compiled.Command) {
				t.Fatalf("PostgreSQL renderer returned the AQL query text")
			}
			return
		}
		// Outside the supported surface: fail closed with the typed
		// capability error. A generic error would mean no contract.
		if !errors.Is(pgErr, postgresrender.ErrUnsupportedPostgresPlan) {
			t.Fatalf("PostgreSQL render error = %v, want ErrUnsupportedPostgresPlan", pgErr)
		}
		if strings.Contains(pgErr.Error(), testAQLText(t, compiled.Command)) {
			t.Fatalf("PostgreSQL failure path leaked AQL text as a result")
		}
	})

	t.Run("PostgreSQLRenderedSpineDispatchesOnlyToPostgreSQL", func(t *testing.T) {
		storage, err := ir.NewPostgresStorageBinding("pgir02_it_parity")
		if err != nil {
			t.Fatalf("NewPostgresStorageBinding: %v", err)
		}
		spine := ir.PhysicalPlan{
			Version: 1,
			Engine:  ir.PhysicalEngineAQL,
			BindVars: map[string]any{
				"root_collection":                  "Patient",
				"project":                          "parity-project",
				"dataset_generation":               "parity-generation",
				"auth_resource_paths":              []string{"FHIR/Patient"},
				"auth_resource_paths_unrestricted": false,
				"scope_allowed":                    true,
			},
			Operations: []ir.PhysicalOperation{
				{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
				{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
					{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
				}}},
			},
		}
		first, err := postgresrender.RenderPhysicalPlan(spine, storage)
		if err != nil {
			t.Fatalf("PostgreSQL spine render: %v", err)
		}
		second, err := postgresrender.RenderPhysicalPlan(spine, storage)
		if err != nil || first.SQL != second.SQL || !reflect.DeepEqual(first.Args, second.Args) {
			t.Fatalf("parity spine render is not deterministic")
		}
		if strings.Contains(first.SQL, "@") {
			t.Fatalf("PostgreSQL SQL carries AQL bind syntax: %s", first.SQL)
		}
		command, err := compiler.NewPostgreSQLCommand(first.SQL, first.Args)
		if err != nil {
			t.Fatalf("PostgreSQL command construction from rendered plan: %v", err)
		}
		if _, _, err := command.AQLParameters(); !errors.Is(err, compiler.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("PostgreSQL command to the AQL adapter error = %v, want mismatch", err)
		}
		sql, args, err := command.PostgreSQLParameters()
		if err != nil || sql != first.SQL || !reflect.DeepEqual(args, first.Args) {
			t.Fatalf("PostgreSQL dispatch round trip lost the rendered result")
		}
	})
}
