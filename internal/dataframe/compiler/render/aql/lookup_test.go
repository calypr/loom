package aql_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	aql "github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestRenderPhysicalLookupExpression(t *testing.T) {
	value := func(path ...string) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "dynamic_item", Path: path}}
	}
	lookup := ir.PhysicalExpression{
		Kind:         ir.PhysicalLookupExpression,
		Cardinality:  ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Lookup: &ir.PhysicalLookup{
			Source:       ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"items"}}},
			ItemVariable: "dynamic_item",
			ItemKey:      value("url"),
			ItemValue:    value("value"),
			MatchBindKey: "dynamic_key",
		},
	}
	plan, err := lower.BuildGenericPhysicalPlan(semantic.SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root:              semantic.SemanticNode{Alias: "root", ResourceType: "Patient"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.BindVars["dynamic_key"] = "x"
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalReturnOp {
			plan.Operations[index].Return.Projections = []ir.PhysicalProjection{{Name: "lookup", Expression: &lookup}}
		}
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FIRST(FOR dynamic_item IN") || !strings.Contains(rendered.Query, "dynamic_item.url == @dynamic_key") || !strings.Contains(rendered.Query, "RETURN dynamic_item.value") {
		t.Fatalf("unexpected lookup AQL: %s", rendered.Query)
	}
	if rendered.BindVars["dynamic_key"] != "x" {
		t.Fatalf("lookup bind changed: %#v", rendered.BindVars)
	}
}
