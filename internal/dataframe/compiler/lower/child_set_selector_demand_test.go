package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestProjectPhysicalChildSetRequiresProofBeforeFirstOnlyDemand(t *testing.T) {
	selector := spec.Selector{Steps: []spec.SelectorStep{{Field: "content", Iterate: true}, {Field: "attachment"}, {Field: "title"}}}
	extract := func(cardinality ir.PhysicalCardinality, selected spec.Selector) ir.PhysicalExpression {
		return ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "children"}, ResourceType: "DocumentReference", Selector: selected},
		}
	}

	tests := []struct {
		name       string
		expression ir.PhysicalExpression
		want       ir.PhysicalSelectorValueDemand
	}{
		{name: "first", expression: extract(ir.PhysicalScalarCardinality, selector), want: ir.PhysicalSelectorFirstValue},
		{name: "array", expression: extract(ir.PhysicalArrayCardinality, selector), want: ir.PhysicalSelectorAllValues},
		{name: "aggregate", expression: ir.PhysicalExpression{
			Kind: ir.PhysicalAggregateExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Aggregate: &ir.PhysicalAggregate{Source: ir.PhysicalValue{Variable: "children"}, Operation: ir.PhysicalCountDistinctAggregate, Value: expressionPtr(extract(ir.PhysicalArrayCardinality, selector))},
		}, want: ir.PhysicalSelectorAllValues},
		{name: "filtered", expression: extract(ir.PhysicalScalarCardinality, spec.Selector{
			Steps: selector.Steps, Filter: &fhirschema.ContainsFilter{Field: "url", Needle: "proof"},
		}), want: ir.PhysicalSelectorAllValues},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set := ir.PhysicalSet{Variable: "children"}
			projectPhysicalChildSet(&set, "DocumentReference", []ir.PhysicalProjection{{Name: "value", Expression: &test.expression}})
			if set.Projection == nil || len(set.Projection.Fields) != 1 {
				t.Fatalf("selector projection = %#v, want one field", set.Projection)
			}
			if got := set.Projection.Fields[0].Demand; got != test.want {
				t.Fatalf("selector demand = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("opaque rich consumer", func(t *testing.T) {
		set := ir.PhysicalSet{Variable: "children"}
		expression := ir.PhysicalExpression{
			Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Call: &ir.PhysicalCall{Name: "coalesce", Args: []ir.PhysicalExpression{extract(ir.PhysicalScalarCardinality, selector)}},
		}
		projectPhysicalChildSet(&set, "DocumentReference", []ir.PhysicalProjection{
			{Name: "first", Expression: expressionPtr(extract(ir.PhysicalScalarCardinality, selector))},
			{Name: "derived", Expression: &expression},
		})
		if set.Projection != nil {
			t.Fatalf("opaque rich consumer was compacted without a rewrite proof: %#v", set.Projection)
		}
	})
}

func expressionPtr(value ir.PhysicalExpression) *ir.PhysicalExpression { return &value }
