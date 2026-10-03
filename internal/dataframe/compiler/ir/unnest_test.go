package ir

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func unnestValueExpression(variable string) PhysicalExpression {
	return PhysicalExpression{
		Kind:         PhysicalValueExpression,
		Cardinality:  PhysicalArrayCardinality,
		NullBehavior: PhysicalEmptyOnNull,
		Value:        &PhysicalValue{Variable: variable, Path: []string{"payload"}},
	}
}

func unnestPlan(policy PhysicalUnnestEmptyPolicy) PhysicalPlan {
	return PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"collection": "Patient",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "collection"}},
			{Kind: PhysicalUnnestOp, Unnest: &PhysicalUnnest{
				Owner:          PhysicalUnnestOwner{OccurrenceID: "root-occurrence", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
				OutputVariable: "item", Ordinality: "item_index", HasItemVariable: "has_item",
				Expression: unnestValueExpression("root"), EmptyPolicy: policy,
			}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{
				Name: "item", Expression: &PhysicalExpression{
					Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality,
					NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "item"},
				},
			}}}},
		},
	}
}

func TestPhysicalUnnestValidatesAllEmptyPolicies(t *testing.T) {
	for _, policy := range []PhysicalUnnestEmptyPolicy{PhysicalUnnestError, PhysicalUnnestExclude, PhysicalUnnestPreserveParent} {
		plan := unnestPlan(policy)
		if err := plan.Validate(); err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
	}
}

func TestPhysicalUnnestValidatesTypedDepthTwoOwnerRoute(t *testing.T) {
	plan := depthTwoUnnestPlan(t)
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	unnest := plan.Operations[1].Unnest
	if len(unnest.Owner.Route) != 2 || unnest.Owner.OwnerVariable != "node_2" || unnest.Expression.Extract.Source.Variable != "node_2" {
		t.Fatalf("depth-two owner route is not reflected by expansion: %#v", unnest)
	}
}

func TestPhysicalUnnestRejectsStaleRouteOccurrenceAndSourceOwner(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*PhysicalUnnest)
		want string
	}{
		{name: "stale terminal occurrence", edit: func(unnest *PhysicalUnnest) { unnest.Owner.OccurrenceID = "other" }, want: "does not match route terminal"},
		{name: "source uses root", edit: func(unnest *PhysicalUnnest) { unnest.Expression.Extract.Source.Variable = "root" }, want: "exact owner occurrence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := depthTwoUnnestPlan(t)
			test.edit(plan.Operations[1].Unnest)
			if err := plan.Validate(); err == nil || !contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func depthTwoUnnestPlan(t *testing.T) PhysicalPlan {
	t.Helper()
	plan := unnestPlan(PhysicalUnnestExclude)
	plan.BindVars["edge_collection_1"] = "fhir_edge"
	plan.BindVars["edge_collection_2"] = "fhir_edge"
	plan.BindVars["edge_label_1"] = "subject"
	plan.BindVars["edge_label_2"] = "guardian"
	plan.BindVars["target_type_1"] = "Patient"
	plan.BindVars["target_type_2"] = "Patient"
	selector, err := spec.ParseSelector("identifier[]")
	if err != nil {
		t.Fatal(err)
	}
	unnest := plan.Operations[1].Unnest
	unnest.Owner = PhysicalUnnestOwner{
		OccurrenceID: "guardian-occurrence", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "node_2",
		Route: []PhysicalUnnestRouteStep{
			{OccurrenceID: "subject-occurrence", Traversal: PhysicalTraversal{SourceVariable: "root", TargetVariable: "node_1", EdgeVariable: "edge_1", Direction: PhysicalInbound, EdgeCollectionBindKey: "edge_collection_1", EdgeLabelBindKey: "edge_label_1", TargetTypeBindKey: "target_type_1", EdgeTargetTypeField: "from_type"}},
			{OccurrenceID: "guardian-occurrence", Traversal: PhysicalTraversal{SourceVariable: "node_1", TargetVariable: "node_2", EdgeVariable: "edge_2", Direction: PhysicalInbound, EdgeCollectionBindKey: "edge_collection_2", EdgeLabelBindKey: "edge_label_2", TargetTypeBindKey: "target_type_2", EdgeTargetTypeField: "from_type"}},
		},
	}
	unnest.Expression = PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Extract: &PhysicalExtract{Source: PhysicalValue{Variable: "node_2", Path: []string{"payload"}}, ResourceType: "Patient", Selector: selector, ExecutionMode: PhysicalSelectorGeneric}}
	return plan
}

func TestPhysicalUnnestRejectsInvalidScopeAndCardinality(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PhysicalPlan)
		want string
	}{
		{"owner out of scope", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.Owner.RootVariable = "future" }, "out of scope"},
		{"scalar source", func(plan *PhysicalPlan) {
			plan.Operations[1].Unnest.Expression.Cardinality = PhysicalScalarCardinality
		}, "array-valued"},
		{"shadowed output", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.OutputVariable = "root" }, "already defined"},
		{"unsafe ordinality", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.Ordinality = "item.index" }, "unsafe"},
		{"unknown empty policy", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.EmptyPolicy = "CROSS" }, "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := unnestPlan(PhysicalUnnestExclude)
			test.edit(&plan)
			if err := plan.Validate(); err == nil || !contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestPhysicalUnnestCanBeNestedInSubplan(t *testing.T) {
	plan := unnestPlan(PhysicalUnnestExclude)
	plan.Operations[1] = PhysicalOperation{Kind: PhysicalSetOp, Set: &PhysicalSet{
		Variable: "items",
		Subplan: PhysicalSubplan{
			Captures: []string{"root"},
			Operations: []PhysicalOperation{{Kind: PhysicalUnnestOp, Unnest: &PhysicalUnnest{
				Owner:          PhysicalUnnestOwner{ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
				OutputVariable: "item", HasItemVariable: "has_item", Expression: unnestValueExpression("root"), EmptyPolicy: PhysicalUnnestExclude,
			}}},
			Return: PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalObjectCardinality,
				NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "item"}},
		},
	}}
	plan.Operations[2].Return.Projections[0].Expression.Value.Variable = "items"
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestClonePhysicalUnnestClonesExpression(t *testing.T) {
	plan := unnestPlan(PhysicalUnnestPreserveParent)
	copy := ClonePhysicalPlan(plan)
	copy.Operations[1].Unnest.Expression.Value.Path[0] = "changed"
	if got := plan.Operations[1].Unnest.Expression.Value.Path[0]; got != "payload" {
		t.Fatalf("clone mutated original unnest expression path: %q", got)
	}
}

func TestClonePhysicalUnnestClonesTypedFallbackBinding(t *testing.T) {
	plan := depthTwoUnnestPlan(t)
	fallback, err := spec.ParseSelector("identifier.value")
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	fallback.Steps[0].Index = &index
	plan.Operations[1].Unnest.Expression.Extract.Fallbacks = []PhysicalSelectorFallback{{
		Source: PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient", Selector: fallback,
	}}
	copy := ClonePhysicalPlan(plan)
	cloned := &copy.Operations[1].Unnest.Expression.Extract.Fallbacks[0]
	cloned.Source.Path[0] = "changed"
	cloned.Selector.Steps[0].Field = "changed"
	*cloned.Selector.Steps[0].Index = 3
	original := plan.Operations[1].Unnest.Expression.Extract.Fallbacks[0]
	if original.Source.Path[0] != "payload" || original.Selector.Steps[0].Field != "identifier" || *original.Selector.Steps[0].Index != 0 {
		t.Fatalf("clone mutations changed the original typed fallback: %#v", original)
	}
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
