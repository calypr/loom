package compiler_test

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	archBackend11BaseFingerprint = "25f4a1abcf9192edb94f53e905cd596f32d96cba67548f68d5e1ec619bdb4dc5"
	archBackend11BaseSQL         = "SELECT r0.\"logical_id\" AS \"c0\", (r0.\"payload\" #> $5::text[]) AS \"c1\" " +
		"FROM \"loom\".\"fhir_resource\" AS r0 " +
		"WHERE r0.\"project\" = $1 AND r0.\"dataset_generation\" = $2 " +
		"AND r0.\"auth_resource_path\" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) " +
		"AND r0.\"resource_type\" = $4 " +
		"ORDER BY r0.\"logical_id\" ASC, r0.\"resource_seq\" ASC"
)

func archBackend11FingerprintFixture() (lower.CompiledRecipeOutput, recipe.RuntimeBindings) {
	bindings := recipe.RuntimeBindings{
		Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"resources"},
	}
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          []string{"id", "name"},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "id", Name: "id", Label: "id", Kind: "string", Cardinality: "one"},
			{ID: "name", Name: "name", Label: "name", Kind: "string", Cardinality: "one"},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		Plan: ir.PhysicalPlan{
			Version: 1,
			Engine:  ir.PhysicalEngineAQL,
			BindVars: map[string]any{
				"root_collection":                  "Patient",
				"project":                          "P1",
				"dataset_generation":               "G1",
				"auth_resource_paths":              []string{"FHIR/Patient"},
				"auth_resource_paths_unrestricted": false,
				"scope_allowed":                    true,
			},
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
				{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
					{Name: "id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
					{Name: "name", Expression: &ir.PhysicalExpression{
						Kind:         ir.PhysicalExtractExpression,
						Cardinality:  ir.PhysicalScalarCardinality,
						NullBehavior: ir.PhysicalPreserveNull,
						Extract: &ir.PhysicalExtract{
							Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
							ResourceType:  "Patient",
							Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}},
							ExecutionMode: ir.PhysicalSelectorDirectScalar,
						},
					}, Presence: &ir.PhysicalProjectionPresence{
						Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						Paths:  [][]string{{"gender"}},
					}},
				}}},
			},
		},
	}
	return output, bindings
}

func TestPostgreSQLPlanFingerprintGoldenMatchesBase648f7c70(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	output, bindings := archBackend11FingerprintFixture()
	query, err := compiler.CompileRecipeOutput(target, output, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileRecipeOutput: %v", err)
	}
	sql, args, err := query.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("PostgreSQLParameters: %v", err)
	}
	if sql != archBackend11BaseSQL {
		t.Fatalf("fixture statement drifted from the base capture input:\n got %q\nwant %q", sql, archBackend11BaseSQL)
	}
	if len(args) != 5 {
		t.Fatalf("argument count drifted from the base capture: %d", len(args))
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "G1"},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "{gender}"},
	}
	for i := range wantArgs {
		if args[i].Type != wantArgs[i].Type {
			t.Fatalf("arg %d type: %q want %q", i, args[i].Type, wantArgs[i].Type)
		}
		if string(marshalProbe(args[i].Value)) != string(marshalProbe(wantArgs[i].Value)) {
			t.Fatalf("arg %d value: %v want %v", i, args[i].Value, wantArgs[i].Value)
		}
	}
	if len(query.Columns) != 2 || query.Columns[0] != "id" || query.Columns[1] != "name" {
		t.Fatalf("declared column contract drifted from the base capture: %v", query.Columns)
	}
	if query.PlanDiagnostics.Fingerprint != archBackend11BaseFingerprint {
		t.Fatalf("moved PostgreSQL fingerprint diverges from the base 648f7c70 capture: got %s want %s",
			query.PlanDiagnostics.Fingerprint, archBackend11BaseFingerprint)
	}
}

func marshalProbe(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte("!marshal-error:" + err.Error())
	}
	return encoded
}
