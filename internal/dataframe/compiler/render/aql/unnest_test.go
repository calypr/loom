package aql_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	aql "github.com/calypr/loom/internal/dataframe/compiler/render/aql"
)

func TestRenderPhysicalPlanInnerUnnestUsesCanonicalCorrelatedLoop(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestInner, "")
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"LET __loom_physical_unnest_source_0 = (root.payload.identifier == null ? [] : FLATTEN(root.payload.identifier))",
		"FOR item IN __loom_physical_unnest_source_0",
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

func TestRenderPhysicalPlanOuterUnnestPreservesEmptyParentAndOrdinality(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestOuter, "item_index")
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"FOR __loom_physical_unnest_index_0 IN (LENGTH(__loom_physical_unnest_source_0) == 0 ? [null] : RANGE(0, LENGTH(__loom_physical_unnest_source_0) - 1))",
		"LET item = __loom_physical_unnest_index_0 == null ? null : __loom_physical_unnest_source_0[__loom_physical_unnest_index_0]",
		"LET item_index = __loom_physical_unnest_index_0",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("outer unnest query missing %q:\n%s", want, rendered.Query)
		}
	}
}

func TestRenderPhysicalPlanRejectsUnnestAfterRootWindow(t *testing.T) {
	plan := genericUnnestPlan(t, ir.PhysicalUnnestInner, "")
	last := len(plan.Operations) - 1
	window := ir.PhysicalOperation{
		Kind: ir.PhysicalSortOp,
		Sort: &ir.PhysicalSort{Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
	}
	unnest := plan.Operations[last-1]
	plan.Operations = append(append(append([]ir.PhysicalOperation{}, plan.Operations[:last-1]...), window, unnest), plan.Operations[last])
	if _, err := aql.RenderPhysicalPlan(plan); err == nil || !strings.Contains(err.Error(), "unnest") {
		t.Fatalf("RenderPhysicalPlan() error = %v, want unnest ordering failure", err)
	}
}

func genericUnnestPlan(t *testing.T, mode ir.PhysicalUnnestJoinMode, ordinality string) compiler.PhysicalPlan {
	t.Helper()
	plan, err := compiler.BuildGenericPhysicalPlan(compiler.SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root:              compiler.SemanticNode{Alias: "root", ResourceType: "Patient"},
	})
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
			InputVariable:  "root",
			OutputVariable: "item",
			Ordinality:     ordinality,
			Expression: ir.PhysicalExpression{
				Kind:         ir.PhysicalValueExpression,
				Cardinality:  ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "root", Path: []string{"payload", "identifier"}},
			},
			JoinMode: mode,
		},
	}
	plan.Operations = append(append(append([]ir.PhysicalOperation{}, plan.Operations[:last]...), unnest), plan.Operations[last])
	return plan
}
