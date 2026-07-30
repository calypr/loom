package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func validPhysicalPlan() ir.PhysicalPlan {
	label := ir.PhysicalValue{BindKey: "edge_label"}
	return ir.PhysicalPlan{
		Version:  1,
		Source:   ir.PhysicalSource{RecipeID: "file_manifest", TemplateID: "file-manifest-v1", SemanticNode: "root", ResourceType: "Patient"},
		BindVars: map[string]any{"root_collection": "Patient", "edge_collection": "fhir_edge", "edge_label": "subject_Patient", "target_type": "Specimen"},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, Source: ir.PhysicalSource{SemanticNode: "root", ResourceType: "Patient"}, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalFilterOp, Source: ir.PhysicalSource{SemanticField: "project"}, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &label}}},
			{Kind: ir.PhysicalTraversalOp, Source: ir.PhysicalSource{SemanticNode: "specimen", Relationship: "subject_Patient"}, Traversal: &ir.PhysicalTraversal{SourceVariable: "root", TargetVariable: "specimen", EdgeVariable: "edge", Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection", EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type"}},
			{Kind: ir.PhysicalDerivedLetOp, Source: ir.PhysicalSource{SemanticField: "specimen_count"}, DerivedLet: &ir.PhysicalDerivedLet{Variable: "specimen_count", Operator: "LENGTH", Inputs: []ir.PhysicalValue{{Variable: "specimen"}}}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}, {Name: "specimen_count", Value: ir.PhysicalValue{Variable: "specimen_count"}}}}},
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
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{"traversal before source", func(p *ir.PhysicalPlan) { p.Operations[0], p.Operations[2] = p.Operations[2], p.Operations[0] }, "source variable"},
		{"derived input before definition", func(p *ir.PhysicalPlan) { p.Operations[3].DerivedLet.Inputs[0].Variable = "later" }, "out of scope"},
		{"operation after return", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EXISTS", Left: ir.PhysicalValue{Variable: "root"}}}})
		}, "after RETURN"},
		{"duplicate variable", func(p *ir.PhysicalPlan) { p.Operations[2].Traversal.TargetVariable = "root" }, "already defined"},
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
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{"unsafe map key", func(p *ir.PhysicalPlan) { p.BindVars["bad-key"] = 1 }, "unsafe bind key"},
		{"AQL marker", func(p *ir.PhysicalPlan) { p.Operations[0].RootScan.CollectionBindKey = "@root_collection" }, "unsafe bind key"},
		{"missing key", func(p *ir.PhysicalPlan) { delete(p.BindVars, "target_type") }, "is not defined"},
		{"unsafe path", func(p *ir.PhysicalPlan) { p.Operations[4].Return.Projections[0].Value.Path = []string{"payload.name"} }, "unsafe path segment"},
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
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{"mismatched payload", func(p *ir.PhysicalPlan) { p.Operations[1].Kind = ir.PhysicalDerivedLetOp }, "does not match"},
		{"multiple payloads", func(p *ir.PhysicalPlan) { p.Operations[1].DerivedLet = &ir.PhysicalDerivedLet{} }, "exactly one payload"},
		{"duplicate projection", func(p *ir.PhysicalPlan) { p.Operations[4].Return.Projections[1].Name = "_key" }, "duplicated"},
		{"missing return", func(p *ir.PhysicalPlan) { p.Operations = p.Operations[:4] }, "exactly one RETURN"},
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

func TestPhysicalPlanValidateRichExpressionContract(t *testing.T) {
	patientGender := mustPhysicalSelector(t, "gender")
	attachmentTitle := mustPhysicalSelector(t, "content[].attachment.title")
	root := ir.PhysicalValue{Variable: "root"}
	files := ir.PhysicalValue{Variable: "files"}
	file := ir.PhysicalValue{Variable: "file"}

	fileSubplan := ir.PhysicalSubplan{
		Captures: []string{"root"},
		Operations: []ir.PhysicalOperation{{
			Kind: ir.PhysicalTraversalOp,
			Traversal: &ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: "file", EdgeVariable: "file_edge",
				Direction: ir.PhysicalInbound, EdgeCollectionBindKey: "edge_collection",
				EdgeLabelBindKey: "file_label", TargetTypeBindKey: "file_type",
			},
		}},
		Return: physicalValueExpression(file, ir.PhysicalObjectCardinality),
	}
	existsSubplan := fileSubplan
	existsSubplan.Return = physicalValueExpression(file, ir.PhysicalScalarCardinality)

	plan := ir.PhysicalPlan{
		Version: 2,
		BindVars: map[string]any{
			"root_collection": "Patient", "edge_collection": "fhir_edge",
			"file_label": "subject_Patient", "file_type": "DocumentReference",
			"project": "project-a", "pivot_columns": []string{"BAM", "VCF"}, "slice_limit": 2,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
				Kind: ir.PhysicalAllPredicate,
				Children: []ir.PhysicalPredicateExpression{
					{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}},
					{Kind: ir.PhysicalExistsPredicate, Exists: &existsSubplan},
				},
			}}},
			{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{Variable: "files", Unique: true, Subplan: fileSubplan}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
				Name: "row",
				Expression: &ir.PhysicalExpression{
					Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
					Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
						{Name: "gender", Expression: ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Extract: &ir.PhysicalExtract{Source: root, ResourceType: "Patient", Selector: patientGender}}},
						{Name: "file_count", Expression: ir.PhysicalExpression{Kind: ir.PhysicalAggregateExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Aggregate: &ir.PhysicalAggregate{Source: files, Operation: ir.PhysicalCountAggregate}}},
						{Name: "file_titles", Expression: ir.PhysicalExpression{Kind: ir.PhysicalPivotExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Pivot: &ir.PhysicalPivotMap{Source: files, ResourceType: "DocumentReference", KeySelector: attachmentTitle, ValueSelector: attachmentTitle, ColumnsBindKey: "pivot_columns"}}},
						{Name: "representative_files", Expression: ir.PhysicalExpression{Kind: ir.PhysicalSliceExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Slice: &ir.PhysicalSlice{Source: files, LimitBindKey: "slice_limit", Sort: physicalExpressionPtr(physicalValueExpression(files, ir.PhysicalScalarCardinality)), Projections: []ir.PhysicalExpressionProjection{{Name: "title", Expression: ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Extract: &ir.PhysicalExtract{Source: files, ResourceType: "DocumentReference", Selector: attachmentTitle}}}}}}},
					}},
				},
			}}}},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("rich physical plan should validate: %v", err)
	}
}

func TestPhysicalPlanValidateRichExpressionRejectsUnsafeScopeAndShape(t *testing.T) {
	plan := validPhysicalPlan()
	plan.Version = 2
	plan.Operations[3] = ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{
		Variable: "files",
		Subplan: ir.PhysicalSubplan{
			Captures:   []string{"future"},
			Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "x", Expression: physicalExpressionPtr(physicalValueExpression(ir.PhysicalValue{Variable: "future"}, ir.PhysicalScalarCardinality))}}},
			Return:     physicalValueExpression(ir.PhysicalValue{Variable: "x"}, ir.PhysicalScalarCardinality),
		},
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "capture \"future\" is out of scope") {
		t.Fatalf("Validate() error = %v; want out-of-scope capture", err)
	}

	plan = validPhysicalPlan()
	plan.Version = 2
	plan.Operations[3] = ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
		Variable: "pivot",
		Expression: &ir.PhysicalExpression{Kind: ir.PhysicalPivotExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Pivot: &ir.PhysicalPivotMap{
			Source: ir.PhysicalValue{Variable: "specimen"}, ResourceType: "Specimen",
			KeySelector: mustPhysicalSelector(t, "type.coding[].display"), ValueSelector: mustPhysicalSelector(t, "type.coding[].display"), ColumnsBindKey: "edge_label",
		}},
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "must be a non-empty []string") {
		t.Fatalf("Validate() error = %v; want typed pivot columns", err)
	}
}

func mustPhysicalSelector(t *testing.T, input string) spec.Selector {
	t.Helper()
	selector, err := spec.ParseSelector(input)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", input, err)
	}
	return selector
}

func physicalValueExpression(value ir.PhysicalValue, cardinality ir.PhysicalCardinality) ir.PhysicalExpression {
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: cardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &value}
}

func physicalExpressionPtr(expression ir.PhysicalExpression) *ir.PhysicalExpression {
	return &expression
}
