package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestRenderPhysicalPlanGenericNavigation(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias:        "root",
		ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias:        "specimen",
			ResourceType: "Specimen",
			EdgeLabel:    "subject_Patient",
		}},
	}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}

	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"FOR root IN @@root_collection",
		"  FILTER root.project == @project",
		"  LET root_scope_allowed = @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths",
		"  FILTER root_scope_allowed == @scope_allowed",
		"  LET __loom_physical_set_1 = (",
		"    FOR edge_1 IN @@traversal_1_edge_collection",
		"      FILTER edge_1._to == root._id",
		"      LET node_1 = DOCUMENT(edge_1._from)",
		"      FILTER edge_1.label == @traversal_1_label",
		"      FILTER edge_1.from_type == @traversal_1_target_type",
		"      FILTER node_1.resourceType == @traversal_1_target_type",
		"      FILTER edge_1.project == @project",
		"      FILTER node_1.project == @project",
		"      LET traversal_1_scope_allowed = @auth_resource_paths_unrestricted == true OR (edge_1.auth_resource_path IN @auth_resource_paths AND node_1.auth_resource_path IN @auth_resource_paths)",
		"      FILTER traversal_1_scope_allowed == @scope_allowed",
		"      RETURN node_1",
		"RETURN { [@__loom_physical_projection_0_name]: root._key }",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered query missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["@root_collection"]; got != "Patient" {
		t.Fatalf("root collection bind = %#v", got)
	}
	if got := rendered.BindVars["@traversal_1_edge_collection"]; got != "fhir_edge" {
		t.Fatalf("edge collection bind = %#v", got)
	}
	if _, present := rendered.BindVars["root_collection"]; present {
		t.Fatalf("runtime binds retained unprefixed root collection: %#v", rendered.BindVars)
	}
	if _, present := rendered.BindVars["traversal_1_edge_collection"]; present {
		t.Fatalf("runtime binds retained unprefixed edge collection: %#v", rendered.BindVars)
	}
	if got := rendered.BindVars["project"]; got != "project-1" {
		t.Fatalf("normal bind was not retained: %#v", got)
	}
	if got := rendered.BindVars["__loom_physical_projection_0_name"]; got != "_key" {
		t.Fatalf("projection name was not bound: %#v", got)
	}
}

func TestRenderPhysicalSetEndpointLookupUsesTargetCollectionForScalarType(t *testing.T) {
	buildPlan := func(includeSecondSet bool) ir.PhysicalPlan {
		t.Helper()
		policy := ir.PhysicalOptimizationPolicy{Enabled: true, MinimumSavings: 1}.
			WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, true).
			WithRule(ir.PhysicalOptimizationRuleTraversalSharing, false)
		children := []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Fields: []semantic.SemanticField{testSemanticField("id", mustPhysicalSelector(t, "id"), "")},
		}}
		if includeSecondSet {
			children = append(children, semantic.SemanticNode{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Fields: []semantic.SemanticField{testSemanticField("id", mustPhysicalSelector(t, "id"), "")},
			})
		}
		plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
			Alias: "root", ResourceType: "Patient", Children: children,
		}}, semantic.ExecutionContext{Project: "project-1", DatasetGeneration: "generation-1", AuthResourcePaths: []string{"/programs/p1"}}, policy)
		if err != nil {
			t.Fatalf("BuildGenericPhysicalPlanWithPolicy() error = %v", err)
		}
		return plan
	}

	t.Run("scalar target type uses collection index lookup", func(t *testing.T) {
		plan := buildPlan(true)
		sets := make([]*ir.PhysicalSet, 0, 2)
		for index := range plan.Operations {
			if plan.Operations[index].Kind == ir.PhysicalSetOp && plan.Operations[index].Set != nil {
				sets = append(sets, plan.Operations[index].Set)
			}
		}
		if len(sets) != 2 || sets[0].SourceSetVariable != "" || sets[1].SourceSetVariable != "" {
			t.Fatalf("expected two direct PhysicalSetOps, got %#v", sets)
		}
		for _, set := range sets {
			traversal := set.Subplan.Operations[0].Traversal
			if traversal == nil || traversal.Strategy != ir.PhysicalTraversalEndpointLookup {
				t.Fatalf("set traversal = %#v, want endpoint lookup", traversal)
			}
		}

		rendered, err := aql.RenderPhysicalPlan(plan)
		if err != nil {
			t.Fatalf("RenderPhysicalPlan() error = %v", err)
		}
		for _, want := range []string{
			"LET child_set_1 = UNIQUE((",
			"FILTER child_set_1_edge.label == @child_set_1_label",
			"FILTER child_set_1_edge.from_type == @child_set_1_target_type",
			"FOR child_set_1_node IN @@__loom_physical_target_collection",
			"FILTER child_set_1_node._id == child_set_1_edge._from",
			"FILTER child_set_1_node.resourceType == @child_set_1_target_type",
			"FILTER child_set_1_edge.project == @project",
			"FILTER child_set_1_node.project == @project",
			"FILTER child_set_1_edge.dataset_generation == @dataset_generation",
			"FILTER child_set_1_node.dataset_generation == @dataset_generation",
			"FILTER child_set_1_scope_allowed == @scope_allowed",
			"FOR child_set_2_node IN @@__loom_physical_target_collection_1",
			"FILTER child_set_2_node._id == child_set_2_edge._from",
		} {
			if !strings.Contains(rendered.Query, want) {
				t.Fatalf("rendered set query missing %q:\n%s", want, rendered.Query)
			}
		}
		if strings.Contains(rendered.Query, "DOCUMENT(child_set_1_edge._from)") {
			t.Fatalf("scalar set lookup still uses DOCUMENT():\n%s", rendered.Query)
		}
		if got := rendered.BindVars["@__loom_physical_target_collection"]; got != "Specimen" {
			t.Fatalf("target collection bind = %#v, want Specimen", got)
		}
		if got := rendered.BindVars["@__loom_physical_target_collection_1"]; got != "Condition" {
			t.Fatalf("second target collection bind = %#v, want Condition", got)
		}
	})

	t.Run("list target types keep dynamic document lookup", func(t *testing.T) {
		plan := buildPlan(false)
		for _, operation := range plan.Operations {
			if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
				traversal := operation.Set.Subplan.Operations[0].Traversal
				plan.BindVars[traversal.TargetTypeBindKey] = []string{"Specimen"}
				break
			}
		}
		rendered, err := aql.RenderPhysicalPlan(plan)
		if err != nil {
			t.Fatalf("RenderPhysicalPlan() error = %v", err)
		}
		if !strings.Contains(rendered.Query, "LET child_set_1_node = DOCUMENT(child_set_1_edge._from)") {
			t.Fatalf("list-valued set lookup did not retain DOCUMENT():\n%s", rendered.Query)
		}
		if strings.Contains(rendered.Query, "@@__loom_physical_target_collection") {
			t.Fatalf("list-valued target type used a scalar collection bind:\n%s", rendered.Query)
		}
	})
}

func TestRenderPhysicalPlanTraversalSetsPreserveRootRowGrain(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Children: []semantic.SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
			}},
		}},
	}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}

	setOne := strings.Index(rendered.Query, "\n  LET __loom_physical_set_1 = (")
	firstTraversal := strings.Index(rendered.Query, "\n    FOR edge_1 IN @@traversal_1_edge_collection")
	setTwo := strings.Index(rendered.Query, "\n  LET __loom_physical_set_2 = (")
	parentLoop := strings.Index(rendered.Query, "\n    FOR __loom_physical_parent_2 IN __loom_physical_set_1")
	secondTraversal := strings.Index(rendered.Query, "\n      FOR node_2, edge_2 IN 1..1 INBOUND __loom_physical_parent_2 @@traversal_2_edge_collection")
	secondEndpoint := strings.Index(rendered.Query, "\n      FOR edge_2 IN @@traversal_2_edge_collection")
	outerReturn := strings.LastIndex(rendered.Query, "\nRETURN { [@__loom_physical_projection_0_name]: root._key }")
	secondTraversalEnd := secondTraversal
	if secondTraversalEnd < 0 {
		secondTraversalEnd = secondEndpoint
	}
	if setOne < 0 || firstTraversal < setOne || setTwo < firstTraversal || parentLoop < setTwo || secondTraversalEnd < parentLoop || outerReturn < secondTraversalEnd {
		t.Fatalf("nested traversal sets did not preserve outer root shape:\n%s", rendered.Query)
	}
	if strings.Contains(rendered.Query, "\nFOR node_1") || strings.Contains(rendered.Query, "\nFOR node_2") {
		t.Fatalf("a traversal escaped its LET subquery and can multiply root rows:\n%s", rendered.Query)
	}
	if strings.Count(rendered.Query, "RETURN { [@__loom_physical_projection_0_name]: root._key }") != 1 {
		t.Fatalf("expected exactly one outer root RETURN:\n%s", rendered.Query)
	}
}

func TestRenderPhysicalPlanIsDeterministicAndCopiesBindVars(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if first.Query != second.Query || !reflect.DeepEqual(first.BindVars, second.BindVars) {
		t.Fatalf("renders are not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if strings.Contains(first.Query, "LET __loom_physical_set_") {
		t.Fatalf("root-only navigation should not require a child traversal set:\n%s", first.Query)
	}

	first.BindVars["project"] = "changed"
	first.BindVars["auth_resource_paths"].([]string)[0] = "/changed"
	if plan.BindVars["project"] != "project-1" {
		t.Fatalf("runtime bind map mutated plan: %#v", plan.BindVars)
	}
	if got := plan.BindVars["auth_resource_paths"].([]string)[0]; got != "/programs/p1" {
		t.Fatalf("runtime bind slice mutated plan: %#v", plan.BindVars)
	}
}

func TestRenderPhysicalPlanNestedObjectExpression(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	gender := mustPhysicalSelector(t, "gender")
	value := func(value ir.PhysicalValue) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &value}
	}
	genderExpression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient", Selector: gender},
	}
	inner := ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
			{Name: "z_key", Expression: value(ir.PhysicalValue{Variable: "root", Path: []string{"_key"}})},
			{Name: "a_gender", Expression: genderExpression},
		}},
	}
	outer := ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
			{Name: "scalar", Expression: genderExpression},
			{Name: "nested", Expression: inner},
		}},
	}
	returnOp := plan.Operations[len(plan.Operations)-1].Return
	returnOp.Projections = []ir.PhysicalProjection{{Name: "row", Expression: &outer}}

	first, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	second, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("second RenderPhysicalPlan() error = %v", err)
	}
	if first.Query != second.Query {
		t.Fatalf("nested object rendering is not deterministic:\nfirst=%s\nsecond=%s", first.Query, second.Query)
	}
	for _, want := range []string{
		"RETURN { [@__loom_physical_projection_0_name]: {",
	} {
		if !strings.Contains(first.Query, want) {
			t.Fatalf("nested object query missing %q:\n%s", want, first.Query)
		}
	}
	if got := strings.Count(first.Query, "[@__loom_physical_object_field_"); got != 4 {
		t.Fatalf("nested object did not render four bind-backed field names, got %d:\n%s", got, first.Query)
	}
	fieldNames := map[string]bool{}
	for key, value := range first.BindVars {
		if strings.HasPrefix(key, "__loom_physical_object_field_") {
			if name, ok := value.(string); ok {
				fieldNames[name] = true
			}
		}
	}
	for _, name := range []string{"nested", "scalar", "a_gender", "z_key"} {
		if !fieldNames[name] {
			t.Fatalf("object field %q was not bind-backed: %#v", name, first.BindVars)
		}
	}
	foundNestedField := false
	for key, value := range first.BindVars {
		if strings.HasPrefix(key, "__loom_physical_object_field_") && value == "a_gender" {
			foundNestedField = true
			break
		}
	}
	if !foundNestedField {
		t.Fatalf("nested object field name was not bind-backed: %#v", first.BindVars)
	}
}

func TestRenderPhysicalPlanObjectExpressionOmitsNullFields(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	gender := mustPhysicalSelector(t, "gender")
	optional := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalOmitNulls,
		Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient", Selector: gender},
	}
	object := ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{{Name: "optional_gender", Expression: optional}}},
	}
	returnOp := plan.Operations[len(plan.Operations)-1].Return
	returnOp.Projections = []ir.PhysicalProjection{{Name: "row", Expression: &object}}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"MERGE(",
		"__loom_object_omit: true",
		"FILTER __loom_object_field.__loom_object_omit == false OR __loom_object_field.__loom_object_value != null",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("null-omitting object query missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["__loom_physical_object_field_0_name"]; got != "optional_gender" {
		t.Fatalf("object field bind = %#v", got)
	}
}

func TestPhysicalPlanValidateRejectsRecursiveObjectExpression(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	object := &ir.PhysicalObject{}
	cycle := ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Object: object}
	object.Fields = []ir.PhysicalExpressionProjection{{Name: "self", Expression: cycle}}
	plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{{Name: "row", Expression: &cycle}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "recursive cycle") {
		t.Fatalf("Validate() error = %v; want recursive object cycle rejection", err)
	}
}

func TestRenderPhysicalPlanRejectsUnsupportedOrAmbiguousOperations(t *testing.T) {
	newPlan := func(t *testing.T) ir.PhysicalPlan {
		t.Helper()
		plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1"})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	tests := []struct {
		name   string
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{
			name: "invalid physical plan",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.Version = 0
			},
			want: "validate physical plan",
		},
		{
			name: "unsupported filter operator",
			mutate: func(plan *ir.PhysicalPlan) {
				returnIndex := len(plan.Operations) - 1
				unsupported := ir.PhysicalOperation{
					Kind: ir.PhysicalFilterOp,
					Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
						Operator: "NOT_EQUALS",
						Left:     ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
						Right:    &ir.PhysicalValue{BindKey: "project"},
					}},
				}
				plan.Operations = append(plan.Operations[:returnIndex], append([]ir.PhysicalOperation{unsupported}, plan.Operations[returnIndex:]...)...)
			},
			want: "unsupported physical filter operator",
		},
		{
			name: "unsupported derived operator",
			mutate: func(plan *ir.PhysicalPlan) {
				returnIndex := len(plan.Operations) - 1
				unsupported := ir.PhysicalOperation{
					Kind:       ir.PhysicalDerivedLetOp,
					DerivedLet: &ir.PhysicalDerivedLet{Variable: "unsupported_value", Operator: "LENGTH", Inputs: []ir.PhysicalValue{{Variable: "root"}}},
				}
				plan.Operations = append(plan.Operations[:returnIndex], append([]ir.PhysicalOperation{unsupported}, plan.Operations[returnIndex:]...)...)
			},
			want: "unsupported physical derived LET operator",
		},
		{
			name: "collection key used as scalar bind",
			mutate: func(plan *ir.PhysicalPlan) {
				value := ir.PhysicalValue{BindKey: "root_collection"}
				plan.Operations[1].Filter.Predicate.Right = &value
			},
			want: "both a collection and scalar bind",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := newPlan(t)
			test.mutate(&plan)
			_, err := aql.RenderPhysicalPlan(plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RenderPhysicalPlan() error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func TestRenderPhysicalPlanRejectsMissingGenericScope(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1"})
	if err != nil {
		t.Fatal(err)
	}
	plan.Operations = append(plan.Operations[:1], plan.Operations[2:]...)

	_, err = aql.RenderPhysicalPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "generic physical plan scope") {
		t.Fatalf("RenderPhysicalPlan() error = %v, want scope validation failure", err)
	}
}

func TestRenderPhysicalPlanRejectsMisboundGenericEdgeTypeDiscriminator(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
		}},
	}}, semantic.ExecutionContext{Project: "project-1"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Operations {
		if plan.Operations[index].Traversal != nil {
			plan.Operations[index].Traversal.EdgeTargetTypeField = "to_type"
			break
		}
	}
	_, err = aql.RenderPhysicalPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "must constrain edge.from_type") {
		t.Fatalf("RenderPhysicalPlan() error = %v, want inbound edge discriminator rejection", err)
	}
}

func TestRenderPhysicalPlanKeepsCollectionAndProjectionValuesOutOfAQL(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "project-1"})
	if err != nil {
		t.Fatal(err)
	}
	maliciousCollection := "Patient; RETURN {injected: true}"
	maliciousProjection := "x]: true } RETURN {injected: true} //"
	plan.BindVars["root_collection"] = maliciousCollection
	for index := range plan.Operations {
		if plan.Operations[index].Return != nil {
			plan.Operations[index].Return.Projections[0].Name = maliciousProjection
		}
	}

	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, maliciousCollection) || strings.Contains(rendered.Query, maliciousProjection) {
		t.Fatalf("data value was interpolated into AQL:\n%s", rendered.Query)
	}
	if got := rendered.BindVars["@root_collection"]; got != maliciousCollection {
		t.Fatalf("collection value was not carried as a collection bind: %#v", got)
	}
	if got := rendered.BindVars["__loom_physical_projection_0_name"]; got != maliciousProjection {
		t.Fatalf("projection name was not carried as a scalar bind: %#v", got)
	}
}
