package execution

// Shared PostgreSQL-target fixtures for engine-level tests: a scalar
// root-scan physical plan inside the renderer's registered root-scan slice
// plus the storage binding it renders against.

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const verifyArch01PGSchema = "ab11_engine"

func verifyArch01PostgreSQLTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(verifyArch01PGSchema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func verifyArch01PostgreSQLResolved(outputName string, columns []string) Resolved {
	binds := map[string]any{
		"root_collection":                  "Patient",
		"project":                          "P1",
		"dataset_generation":               "G1",
		"auth_resource_paths":              []string{"FHIR/Patient"},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
	}
	projections := make([]ir.PhysicalProjection, 0, len(columns))
	for _, column := range columns {
		projections = append(projections, ir.PhysicalProjection{
			Name:  column,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		})
	}
	plan := ir.PhysicalPlan{
		Version:  1,
		Engine:   ir.PhysicalEngineAQL,
		BindVars: binds,
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
			{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "root_auth_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: []ir.PhysicalValue{
				{Variable: "root", Path: []string{"auth_resource_path"}},
				{BindKey: "auth_resource_paths"},
				{BindKey: "auth_resource_paths_unrestricted"},
			}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root_auth_allowed"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
	schema := make([]lower.CompiledOutputColumn, 0, len(columns))
	for _, column := range columns {
		schema = append(schema, lower.CompiledOutputColumn{ID: column, Name: column, Label: column, Kind: "string", Cardinality: "one"})
	}
	output := lower.CompiledRecipeOutput{
		Name:             outputName,
		RootResourceType: "Patient",
		Columns:          append([]string(nil), columns...),
		OutputSchema:     schema,
		RowIdentity:      &spec.RowIdentity{Fields: append([]string(nil), columns...)},
		Plan:             plan,
	}
	return Resolved{
		Bundle: recipe.Bundle{Name: "verify_arch01"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "P1", DatasetGeneration: "G1", OutputNames: []string{outputName},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "verify-arch01",
	}
}
