package dataframe

import (
	"strings"
	"testing"
)

func validPhysicalPlan() PhysicalPlan {
	label := PhysicalValue{BindKey: "edge_label"}
	return PhysicalPlan{
		Version:  1,
		Source:   PhysicalSource{RecipeID: "file_manifest", TemplateID: "file-manifest-v1", SemanticNode: "root", ResourceType: "Patient"},
		BindVars: map[string]any{"root_collection": "Patient", "edge_collection": "fhir_edge", "edge_label": "subject_Patient", "target_type": "Specimen"},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, Source: PhysicalSource{SemanticNode: "root", ResourceType: "Patient"}, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: PhysicalFilterOp, Source: PhysicalSource{SemanticField: "project"}, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &label}}},
			{Kind: PhysicalTraversalOp, Source: PhysicalSource{SemanticNode: "specimen", Relationship: "subject_Patient"}, Traversal: &PhysicalTraversal{SourceVariable: "root", TargetVariable: "specimen", EdgeVariable: "edge", Direction: PhysicalOutbound, EdgeCollectionBindKey: "edge_collection", EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type"}},
			{Kind: PhysicalDerivedLetOp, Source: PhysicalSource{SemanticField: "specimen_count"}, DerivedLet: &PhysicalDerivedLet{Variable: "specimen_count", Operator: "LENGTH", Inputs: []PhysicalValue{{Variable: "specimen"}}}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "_key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}, {Name: "specimen_count", Value: PhysicalValue{Variable: "specimen_count"}}}}},
		},
	}
}

func TestPhysicalPlanValidateGenericOperationGraph(t *testing.T) {
	plan := validPhysicalPlan()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if plan.Operations[1].Source.SemanticField != "project" || plan.Operations[2].Source.Relationship != "subject_Patient" {
		t.Fatalf("semantic provenance was not retained: %#v", plan.Operations)
	}
}

func TestPhysicalPlanValidateRejectsOutOfScopeAndOrdering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalPlan)
		want   string
	}{
		{"traversal before source", func(p *PhysicalPlan) { p.Operations[0], p.Operations[2] = p.Operations[2], p.Operations[0] }, "source variable"},
		{"derived input before definition", func(p *PhysicalPlan) { p.Operations[3].DerivedLet.Inputs[0].Variable = "later" }, "out of scope"},
		{"operation after return", func(p *PhysicalPlan) {
			p.Operations = append(p.Operations, PhysicalOperation{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EXISTS", Left: PhysicalValue{Variable: "root"}}}})
		}, "after RETURN"},
		{"duplicate variable", func(p *PhysicalPlan) { p.Operations[2].Traversal.TargetVariable = "root" }, "already defined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validPhysicalPlan()
			test.mutate(&plan)
			if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func TestPhysicalPlanValidateRejectsUnsafeOrMissingBindKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalPlan)
		want   string
	}{
		{"unsafe map key", func(p *PhysicalPlan) { p.BindVars["bad-key"] = 1 }, "unsafe bind key"},
		{"AQL marker", func(p *PhysicalPlan) { p.Operations[0].RootScan.CollectionBindKey = "@root_collection" }, "unsafe bind key"},
		{"missing key", func(p *PhysicalPlan) { delete(p.BindVars, "target_type") }, "is not defined"},
		{"unsafe path", func(p *PhysicalPlan) { p.Operations[4].Return.Projections[0].Value.Path = []string{"payload.name"} }, "unsafe path segment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validPhysicalPlan()
			test.mutate(&plan)
			if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func TestPhysicalPlanValidateTaggedOperationAndReturnShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalPlan)
		want   string
	}{
		{"mismatched payload", func(p *PhysicalPlan) { p.Operations[1].Kind = PhysicalDerivedLetOp }, "does not match"},
		{"multiple payloads", func(p *PhysicalPlan) { p.Operations[1].DerivedLet = &PhysicalDerivedLet{} }, "exactly one payload"},
		{"duplicate projection", func(p *PhysicalPlan) { p.Operations[4].Return.Projections[1].Name = "_key" }, "duplicated"},
		{"missing return", func(p *PhysicalPlan) { p.Operations = p.Operations[:4] }, "exactly one RETURN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validPhysicalPlan()
			test.mutate(&plan)
			if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v; want substring %q", err, test.want)
			}
		})
	}
}
