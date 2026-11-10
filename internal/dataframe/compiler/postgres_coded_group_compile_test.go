package compiler_test

// pg-sql-19 compile-level witness (declared deviation S3): the CODED_GROUP
// slice must survive the full target-bound compile, not just the renderer.
// A compiled PostgreSQL query is one contract: tagged command, declared
// column contract, public projection, and fingerprint all originate from
// the same lowered plan (postgres_target.go). This file pins them for the
// registered coded-group slice and keeps the AQL target as the control —
// the shared lowering must not drift for AQL while PostgreSQL registers
// the stage.

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func codedGroup19CompiledOutput(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy) (lower.CompiledRecipeOutput, recipe.RuntimeBindings) {
	t.Helper()
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	output := recipe.Output{
		Name: "coded_group_rows", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "resource_id", Name: "resource_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
					ConstructionID: "group_codes",
					Source: recipe.ConstructionCodedGroupSource{
						OccurrenceID: "base", ResourceType: "Observation", CodingPath: "component[].code.coding[]",
						FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
					},
					MissingKeyPolicy:     policy,
					SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
					CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
				}}, Outputs: columns,
			}},
		},
	}
	bindings := recipe.RuntimeBindings{Project: "P19", DatasetGeneration: "G19", AuthResourcePaths: []string{"FHIR/Observation"}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return compiled.Outputs[0], bindings
}

func TestPostgreSQLCodedGroupCompileContract(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	output, bindings := codedGroup19CompiledOutput(t, recipe.ConstructionGroupMissingKeyGroup)
	query, err := compiler.CompileRecipeOutput(target, output, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileRecipeOutput (PostgreSQL): %v", err)
	}

	// One contract, one tag: the command is PostgreSQL-tagged with no AQL
	// fallback.
	if query.Command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("command backend = %q, want PostgreSQL", string(query.Command.Backend()))
	}
	if _, _, err := query.Command.AQLParameters(); err == nil {
		t.Fatalf("a PostgreSQL-tagged command must not expose AQL parameters")
	}
	sql, args, err := query.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("PostgreSQLParameters: %v", err)
	}
	for _, want := range []string{"WITH cg_src AS (", "cg_candidates", "cg_tuples", "cg_grouped", `COLLATE "C" ASC`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("compiled statement is missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "@") {
		t.Fatalf("compiled statement carries AQL bind syntax:\n%s", sql)
	}
	if len(args) == 0 {
		t.Fatalf("compiled statement declares no bound parameters")
	}
	texts := 0
	for index, parameter := range args {
		switch parameter.Type {
		case command.PostgreSQLParameterText:
			texts++
		case command.PostgreSQLParameterJSONB:
			// the auth-path array binds as jsonb, as in every scoped spine
		default:
			t.Fatalf("arg %d has unregistered type %q", index, string(parameter.Type))
		}
	}
	if texts < 8 {
		t.Fatalf("text-bound path/key parameters = %d, want at least 8", texts)
	}

	// Declared column contract vs public projection: the hidden stable
	// identity ships in the statement but never in the public row.
	wantColumns := []string{"code_system", "code_version", "code", "source_records", "__loom_row_id"}
	if strings.Join(query.Columns, ",") != strings.Join(wantColumns, ",") {
		t.Fatalf("declared columns = %v, want %v", query.Columns, wantColumns)
	}
	wantPublic := []string{"code_system", "code_version", "code", "source_records"}
	if strings.Join(query.PublicColumns, ",") != strings.Join(wantPublic, ",") {
		t.Fatalf("public columns = %v, want %v", query.PublicColumns, wantPublic)
	}

	// Fingerprint: deterministic for identical inputs, sensitive to the
	// rendered surface (the policy changes the statement, so the digest
	// must move).
	repeat, err := compiler.CompileRecipeOutput(target, output, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	if repeat.PlanDiagnostics.Fingerprint != query.PlanDiagnostics.Fingerprint {
		t.Fatalf("fingerprint is not deterministic: %s vs %s", query.PlanDiagnostics.Fingerprint, repeat.PlanDiagnostics.Fingerprint)
	}
	excludeOutput, excludeBindings := codedGroup19CompiledOutput(t, recipe.ConstructionGroupMissingKeyExclude)
	exclude, err := compiler.CompileRecipeOutput(target, excludeOutput, excludeBindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileRecipeOutput (EXCLUDE): %v", err)
	}
	if exclude.PlanDiagnostics.Fingerprint == query.PlanDiagnostics.Fingerprint {
		t.Fatalf("fingerprint must move when the rendered surface moves")
	}
}

// TestPostgreSQLCodedGroupAQLControlFreezesSharedLowering is the AQL
// control: registering the slice on the PostgreSQL renderer must not drift
// the AQL path built on the same lowered plan. The pinned literals come
// from render/aql/construction_coded_group.go (identity TO_STRING member
// list and the guard tag).
func TestPostgreSQLCodedGroupAQLControlFreezesSharedLowering(t *testing.T) {
	output, bindings := codedGroup19CompiledOutput(t, recipe.ConstructionGroupMissingKeyError)
	query, err := compiler.CompileRecipeOutput(compiler.ArangoTarget(), output, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileRecipeOutput (AQL control): %v", err)
	}
	if query.Command.Backend() != command.CompiledBackendAQL {
		t.Fatalf("control command backend = %q, want AQL", string(query.Command.Backend()))
	}
	aql, _, err := query.Command.AQLParameters()
	if err != nil {
		t.Fatalf("AQLParameters: %v", err)
	}
	for _, want := range []string{"CONSTRUCTION_CODED_GROUP_MISSING_KEY", `"system"`, `"version"`, `"code"`} {
		if !strings.Contains(aql, want) {
			t.Fatalf("AQL control drifted, missing %q:\n%s", want, aql)
		}
	}
	for _, forbidden := range []string{"WITH cg_src", "jsonb_array_elements", "COLLATE"} {
		if strings.Contains(aql, forbidden) {
			t.Fatalf("AQL control leaked PostgreSQL renderer text (%q):\n%s", forbidden, aql)
		}
	}
}
