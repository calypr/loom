package compiler

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

func TestPhysicalPlanValidateRichExpressionContract(t *testing.T) {
	patientGender := mustPhysicalSelector(t, "gender")
	attachmentTitle := mustPhysicalSelector(t, "content[].attachment.title")
	root := PhysicalValue{Variable: "root"}
	files := PhysicalValue{Variable: "files"}
	file := PhysicalValue{Variable: "file"}

	fileSubplan := PhysicalSubplan{
		Captures: []string{"root"},
		Operations: []PhysicalOperation{{
			Kind: PhysicalTraversalOp,
			Traversal: &PhysicalTraversal{
				SourceVariable: "root", TargetVariable: "file", EdgeVariable: "file_edge",
				Direction: PhysicalInbound, EdgeCollectionBindKey: "edge_collection",
				EdgeLabelBindKey: "file_label", TargetTypeBindKey: "file_type",
			},
		}},
		Return: physicalValueExpression(file, PhysicalObjectCardinality),
	}
	existsSubplan := fileSubplan
	existsSubplan.Return = physicalValueExpression(file, PhysicalScalarCardinality)

	plan := PhysicalPlan{
		Version: 2,
		BindVars: map[string]any{
			"root_collection": "Patient", "edge_collection": "fhir_edge",
			"file_label": "subject_Patient", "file_type": "DocumentReference",
			"project": "project-a", "pivot_columns": []string{"BAM", "VCF"}, "slice_limit": 2,
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Expression: &PhysicalPredicateExpression{
				Kind: PhysicalAllPredicate,
				Children: []PhysicalPredicateExpression{
					{Kind: PhysicalComparisonPredicate, Comparison: &PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &PhysicalValue{BindKey: "project"}}},
					{Kind: PhysicalExistsPredicate, Exists: &existsSubplan},
				},
			}}},
			{Kind: PhysicalSetOp, Set: &PhysicalSet{Variable: "files", Unique: true, Subplan: fileSubplan}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{
				Name: "row",
				Expression: &PhysicalExpression{
					Kind: PhysicalObjectExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull,
					Object: &PhysicalObject{Fields: []PhysicalExpressionProjection{
						{Name: "gender", Expression: PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Extract: &PhysicalExtract{Source: root, ResourceType: "Patient", Selector: patientGender}}},
						{Name: "file_count", Expression: PhysicalExpression{Kind: PhysicalAggregateExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalEmptyOnNull, Aggregate: &PhysicalAggregate{Source: files, Operation: PhysicalCountAggregate}}},
						{Name: "file_titles", Expression: PhysicalExpression{Kind: PhysicalPivotExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalEmptyOnNull, Pivot: &PhysicalPivotMap{Source: files, ResourceType: "DocumentReference", KeySelector: attachmentTitle, ValueSelector: attachmentTitle, ColumnsBindKey: "pivot_columns"}}},
						{Name: "representative_files", Expression: PhysicalExpression{Kind: PhysicalSliceExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull, Slice: &PhysicalSlice{Source: files, LimitBindKey: "slice_limit", Sort: physicalExpressionPtr(physicalValueExpression(files, PhysicalScalarCardinality)), Projections: []PhysicalExpressionProjection{{Name: "title", Expression: PhysicalExpression{Kind: PhysicalExtractExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Extract: &PhysicalExtract{Source: files, ResourceType: "DocumentReference", Selector: attachmentTitle}}}}}}},
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
	plan.Operations[3] = PhysicalOperation{Kind: PhysicalSetOp, Set: &PhysicalSet{
		Variable: "files",
		Subplan: PhysicalSubplan{
			Captures:   []string{"future"},
			Operations: []PhysicalOperation{{Kind: PhysicalDerivedLetOp, DerivedLet: &PhysicalDerivedLet{Variable: "x", Expression: physicalExpressionPtr(physicalValueExpression(PhysicalValue{Variable: "future"}, PhysicalScalarCardinality))}}},
			Return:     physicalValueExpression(PhysicalValue{Variable: "x"}, PhysicalScalarCardinality),
		},
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "capture \"future\" is out of scope") {
		t.Fatalf("Validate() error = %v; want out-of-scope capture", err)
	}

	plan = validPhysicalPlan()
	plan.Version = 2
	plan.Operations[3] = PhysicalOperation{Kind: PhysicalDerivedLetOp, DerivedLet: &PhysicalDerivedLet{
		Variable: "pivot",
		Expression: &PhysicalExpression{Kind: PhysicalPivotExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalEmptyOnNull, Pivot: &PhysicalPivotMap{
			Source: PhysicalValue{Variable: "specimen"}, ResourceType: "Specimen",
			KeySelector: mustPhysicalSelector(t, "type.coding[].display"), ValueSelector: mustPhysicalSelector(t, "type.coding[].display"), ColumnsBindKey: "edge_label",
		}},
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "must be a non-empty []string") {
		t.Fatalf("Validate() error = %v; want typed pivot columns", err)
	}
}

func mustPhysicalSelector(t *testing.T, input string) Selector {
	t.Helper()
	selector, err := ParseSelector(input)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", input, err)
	}
	return selector
}

func physicalValueExpression(value PhysicalValue, cardinality PhysicalCardinality) PhysicalExpression {
	return PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: cardinality, NullBehavior: PhysicalPreserveNull, Value: &value}
}

func physicalExpressionPtr(expression PhysicalExpression) *PhysicalExpression { return &expression }
