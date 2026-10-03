package aql_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	aql "github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestRenderPhysicalPlanExcludeUnnestUsesCanonicalCorrelatedLoop(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestExclude, "")
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"LET __loom_physical_unnest_source_0 = (root.payload.identifier == null ? [] : FLATTEN(root.payload.identifier))",
		"FOR __loom_physical_unnest_index_0 IN (LENGTH(__loom_physical_unnest_source_0) == 0 ? [] : RANGE(0, LENGTH(__loom_physical_unnest_source_0) - 1))",
		"LET item = __loom_physical_unnest_index_0 == null ? null : __loom_physical_unnest_source_0[__loom_physical_unnest_index_0]",
		"LET has_item = __loom_physical_unnest_index_0 != null",
		"RETURN { [@__loom_physical_projection_0_name]: item }",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("inner unnest query missing %q:\n%s", want, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "[null] : RANGE") {
		t.Fatalf("INNER unnest emitted an OUTER sentinel:\n%s", rendered.Query)
	}
}

func TestRenderPhysicalPlanPreserveParentUnnestAndOrdinality(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestPreserveParent, "item_index")
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"FOR __loom_physical_unnest_index_0 IN (LENGTH(__loom_physical_unnest_source_0) == 0 ? [null] : RANGE(0, LENGTH(__loom_physical_unnest_source_0) - 1))",
		"LET item = __loom_physical_unnest_index_0 == null ? null : __loom_physical_unnest_source_0[__loom_physical_unnest_index_0]",
		"LET has_item = __loom_physical_unnest_index_0 != null",
		"LET item_index = __loom_physical_unnest_index_0",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("outer unnest query missing %q:\n%s", want, rendered.Query)
		}
	}
}

func TestRenderPhysicalPlanErrorUnnestUsesAssertAtOwnerBoundary(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestError, "item_index")
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	if !strings.Contains(rendered.Query, "FILTER ASSERT(LENGTH(__loom_physical_unnest_source_0) > 0") || !strings.Contains(rendered.Query, "root._key") || !strings.Contains(rendered.Query, "CONSTRUCTION_EXPANSION_EMPTY: row expansion occurrence") {
		t.Fatalf("ERROR unnest did not assert a nonempty source with owner evidence:\n%s", rendered.Query)
	}
	if got := rendered.BindVars["unnest_error_occurrence_0"]; got != "root-occurrence" {
		t.Fatalf("ERROR occurrence bind = %#v", got)
	}
}

func TestRenderPhysicalPlanRejectsUnnestAfterRootWindow(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestExclude, "")
	last := len(plan.Operations) - 1
	window := ir.PhysicalOperation{
		Kind: ir.PhysicalSortOp,
		Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"_key"}}}},
	}
	unnest := plan.Operations[last-1]
	plan.Operations = append(append(append([]ir.PhysicalOperation{}, plan.Operations[:last-1]...), window, unnest), plan.Operations[last])
	if _, err := aql.RenderPhysicalPlan(plan); err == nil || !strings.Contains(err.Error(), "unnest") {
		t.Fatalf("RenderPhysicalPlan() error = %v, want unnest ordering failure", err)
	}
}

func genericUnnestPlan(t *testing.T, policy ir.PhysicalUnnestEmptyPolicy, ordinality string) ir.PhysicalPlan {
	t.Helper()
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(
		semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}},
		semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}},
		ir.DefaultPhysicalOptimizationPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	last := len(plan.Operations) - 1
	plan.Operations[last].Return.Projections = []ir.PhysicalProjection{{
		Name:  "item",
		Value: ir.PhysicalValue{Variable: "item"},
	}}
	unnest := ir.PhysicalOperation{
		Kind: ir.PhysicalUnnestOp,
		Unnest: &ir.PhysicalUnnest{
			Owner:           ir.PhysicalUnnestOwner{OccurrenceID: "root-occurrence", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
			OutputVariable:  "item",
			HasItemVariable: "has_item",
			Ordinality:      ordinality,
			Expression: ir.PhysicalExpression{
				Kind:         ir.PhysicalValueExpression,
				Cardinality:  ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "root", Path: []string{"payload", "identifier"}},
			},
			EmptyPolicy: policy,
		},
	}
	plan.Operations = append(append(append([]ir.PhysicalOperation{}, plan.Operations[:last]...), unnest), plan.Operations[last])
	return plan
}
