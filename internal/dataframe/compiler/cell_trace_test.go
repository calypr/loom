package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileCellTraceUsesFinalValueAndStableIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	compiled, err := CompileCellTraceOutputWithPolicy(output, "patient_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"root.payload.id", "@cell_trace_offset", "@cell_trace_limit", "[@project, root._key]"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("trace query missing %q:\n%s", want, compiled.Query)
		}
	}
	for _, field := range []string{ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField, ir.PhysicalCellTraceStatusField, ir.PhysicalCellTraceHasMoreField} {
		if !containsBindValue(compiled.BindVars, field) {
			t.Fatalf("trace field %q missing from binds %#v", field, compiled.BindVars)
		}
	}
	if compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("trace identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
}

func TestCompileCellTraceExplainsOptionalTraversalBeforeFirstReduction(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
			Fields: []recipe.Field{{Name: "condition_id", Expr: recipe.Expression{Select: "id"}, ValueMode: recipe.ValueModeFirst}},
		}},
	})
	compiled, err := CompileCellTraceOutputWithPolicy(output, "condition_id", 5, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "TO_ARRAY(", "resourceType:", "resourceId:", "LENGTH(__loom_physical_trace_status_candidates) > 1 ? \"AMBIGUOUS\""} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("trace query missing pre-reduction contributor evidence %q:\n%s", want, compiled.Query)
		}
	}
	if compiled.ContributionOffset != 5 || compiled.ContributionLimit != 10 {
		t.Fatalf("trace page = %d/%d", compiled.ContributionOffset, compiled.ContributionLimit)
	}
}

func TestCompileCellTraceExplainsRelatedAggregateContributors(t *testing.T) {
	value := recipe.Expression{Select: "valueQuantity.value"}
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			Aggregates: []recipe.Aggregate{{Name: "maximum_value", Operation: recipe.AggregateMax, Expr: &value}},
		}},
	})
	compiled, err := CompileCellTraceOutputWithPolicy(output, "maximum_value", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FOR __loom_physical_trace_contributor", "IN child_set_1", "resourceType:", "resourceId:", ".payload.valueQuantity.value"} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("trace query missing related aggregate contributor evidence %q:\n%s", want, compiled.Query)
		}
	}
}

func TestCompileCellTraceRejectsUnknownOrHiddenColumn(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	if _, err := CompileCellTraceOutputWithPolicy(output, "missing", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("unknown trace column was accepted")
	}
	if _, err := CompileCellTraceOutputWithPolicy(output, "__loom_row_id", 0, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("internal row identity was accepted as a trace column")
	}
}
