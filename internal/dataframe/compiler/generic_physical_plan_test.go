package compiler

import (
	"strings"
	"testing"
)

func TestBuildGenericPhysicalPlanNavigationSkeleton(t *testing.T) {
	semantic := SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []SemanticNode{{Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen"}},
			}},
		},
	}
	plan, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("built plan does not validate: %v", err)
	}
	if plan.BindVars["root_collection"] != "Patient" || plan.BindVars["traversal_1_edge_collection"] != "fhir_edge" || plan.BindVars["traversal_1_label"] != "subject_Patient" || plan.BindVars["traversal_1_target_type"] != "Specimen" {
		t.Fatalf("unexpected bound physical values: %#v", plan.BindVars)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("unexpected auth mode: %#v", plan.BindVars)
	}

	traversals := 0
	authPredicates := 0
	for _, operation := range plan.Operations {
		if operation.Traversal != nil {
			traversals++
			if operation.Traversal.Direction != PhysicalInbound {
				t.Fatalf("generic traversal direction = %q", operation.Traversal.Direction)
			}
			if operation.Traversal.EdgeTargetTypeField != "from_type" {
				t.Fatalf("generic inbound traversal edge discriminator = %q", operation.Traversal.EdgeTargetTypeField)
			}
			if operation.Source.SemanticNode == "" || operation.Source.Relationship == "" {
				t.Fatalf("traversal lost provenance: %#v", operation.Source)
			}
		}
		if operation.DerivedLet != nil && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
			authPredicates++
		}
	}
	if traversals != 2 || authPredicates != 3 {
		t.Fatalf("traversals=%d auth predicates=%d operations=%#v", traversals, authPredicates, plan.Operations)
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != PhysicalReturnOp || len(last.Return.Projections) != 1 || last.Return.Projections[0].Name != "_key" {
		t.Fatalf("unexpected terminal return: %#v", last)
	}
}

func TestBuildGenericPhysicalPlanEmptyAuthScopeIsExplicit(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{Version: 1, Project: "p", Root: SemanticNode{Alias: "root", ResourceType: "Observation"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != true {
		t.Fatalf("empty auth paths were not represented as unrestricted: %#v", plan.BindVars)
	}
}

func TestBuildAndRenderGenericPhysicalPlanOptionalChildFieldsAndFilters(t *testing.T) {
	wantID := "file-1"
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{
			Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
			Fields:  []SemanticField{{Name: "file_id", FieldRef: "DocumentReference.id", Selector: Selector{Steps: []SelectorStep{{Field: "id"}}}, ValueMode: "FIRST"}},
			Filters: []TypedFilter{{FieldRef: "DocumentReference.id", Selector: "id", FieldKind: FilterString, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &wantID}}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sets int
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalSetOp {
			sets++
		}
	}
	if sets != 1 {
		t.Fatalf("optional child was not lowered to one physical set: %#v", plan.Operations)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "LET child_set_1 = UNIQUE") || !strings.Contains(rendered.Query, "@child_set_1_filter_1_value") || !strings.Contains(rendered.Query, "__loom_projection_0") {
		t.Fatalf("optional child fields/filter were not rendered:\n%s", rendered.Query)
	}
	if rendered.BindVars["child_set_1_filter_1_value"] != wantID {
		t.Fatalf("child filter bind = %#v", rendered.BindVars["child_set_1_filter_1_value"])
	}
}

func TestBuildAndRenderGenericPhysicalPlanNestedOptionalFieldsAndAggregates(t *testing.T) {
	id := "file-1"
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []SemanticNode{{
					Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
					Fields:     []SemanticField{{Name: "id", Selector: Selector{Steps: []SelectorStep{{Field: "id"}}}, ValueMode: "FIRST"}},
					Filters:    []TypedFilter{{FieldRef: "DocumentReference.id", Selector: "id", FieldKind: FilterString, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &id}}}},
					Aggregates: []SemanticAggregate{{Name: "count", Operation: "COUNT"}},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("nested physical plan does not validate: %v", err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LET child_set_1 = UNIQUE((",
		"@child_set_2_filter_1_value", "LENGTH(child_set_2)", "FOR __loom_prepared_value IN child_set_2",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("nested physical query missing %q:\n%s", want, rendered.Query)
		}
	}
	if !strings.Contains(rendered.Query, "FOR child_set_2_node, child_set_2_edge IN 1..1 INBOUND __loom_physical_parent_set_2 @@child_set_2_edge_collection") && !strings.Contains(rendered.Query, "FOR child_set_2_edge IN @@child_set_2_edge_collection") {
		t.Fatalf("nested physical query lost both native and endpoint traversal forms:\n%s", rendered.Query)
	}
	foundNestedName := false
	for key, value := range rendered.BindVars {
		if strings.HasPrefix(key, "__loom_physical_projection_") && value == "specimen__file__id" {
			foundNestedName = true
		}
	}
	if !foundNestedName {
		t.Fatalf("nested field projection did not retain path-qualified name: %#v\n%s", rendered.BindVars, rendered.Query)
	}
}

func TestBuildAndRenderGenericPhysicalPlanAggregates(t *testing.T) {
	gender := Selector{Steps: []SelectorStep{{Field: "gender"}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Aggregates: []SemanticAggregate{
				{Name: "patient_count", Operation: "COUNT"},
				{Name: "genders", Operation: "DISTINCT_VALUES", Selector: &gender},
			},
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Aggregates: []SemanticAggregate{{Name: "count", Operation: "COUNT"}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LENGTH([root])", "SORTED_UNIQUE(FLATTEN(", "LENGTH(child_set_1)", "[@__loom_physical_projection_1_name]"} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("aggregate query missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["__loom_physical_projection_2_name"]; got != "genders" {
		t.Fatalf("projection bind = %#v", got)
	}
}

func TestBuildAndRenderGenericPhysicalPlanRepresentativeSlices(t *testing.T) {
	gender := Selector{Steps: []SelectorStep{{Field: "gender"}}}
	title := Selector{Steps: []SelectorStep{{Field: "content", Iterate: true}, {Field: "attachment"}, {Field: "title"}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Slices:   []SemanticSlice{{Name: "representative", Limit: 2, Predicate: &gender, PredicateEquals: "female", Fields: []SemanticField{{Name: "gender", Selector: gender, ValueMode: "FIRST"}}}},
			Children: []SemanticNode{{Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient", Slices: []SemanticSlice{{Name: "representative_files", Limit: 1, Fields: []SemanticField{{Name: "title", Selector: title, ValueMode: "FIRST"}}}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SORT __loom_physical_slice_item._key ASC", "LIMIT @slice_root_representative_limit", "LIMIT @slice_child_set_1_representative_files_limit", "@slice_root_representative_predicate_equals", "FOR __loom_physical_slice_item_1 IN child_set_1"} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("slice query missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["slice_root_representative_limit"]; got != 2 {
		t.Fatalf("root slice limit bind = %#v", got)
	}
	if got := rendered.BindVars["slice_child_set_1_representative_files_limit"]; got != 1 {
		t.Fatalf("child slice limit bind = %#v", got)
	}
}

func TestBuildAndRenderGenericPhysicalPlanAggregatePredicates(t *testing.T) {
	status := Selector{Steps: []SelectorStep{{Field: "id"}}}
	gender := Selector{Steps: []SelectorStep{{Field: "gender"}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Aggregates: []SemanticAggregate{{Name: "female_count", Operation: "COUNT", Predicate: &gender, PredicateEquals: "female"}},
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Aggregates: []SemanticAggregate{{Name: "available_count", Operation: "COUNT_DISTINCT", Selector: &status, Predicate: &status, PredicateEquals: "available"}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(rendered.Query, "FOR __loom_physical_aggregate_item") < 2 {
		t.Fatalf("aggregate predicates did not render per-item filters:\n%s", rendered.Query)
	}
	if rendered.BindVars["aggregate_root_female_count_predicate_equals"] != "female" || rendered.BindVars["aggregate_child_set_1_available_count_predicate_equals"] != "available" {
		t.Fatalf("predicate binds missing: %#v", rendered.BindVars)
	}
}

func TestBuildGenericPhysicalPlanPreparesSelectorsAcrossRichConsumers(t *testing.T) {
	status := Selector{Steps: []SelectorStep{{Field: "id"}}}
	policy := DefaultPhysicalOptimizationPolicy().WithRule(PhysicalOptimizationRuleCompactProjection, false).WithRule(PhysicalOptimizationRulePreparedSelectors, true)
	plan, err := BuildGenericPhysicalPlanWithPolicy(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Aggregates: []SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &status}},
				Pivots:     []SemanticPivot{{Name: "status_values", ColumnSelector: status, ValueSelector: status, Columns: []string{"active", "resolved"}}},
				Slices:     []SemanticSlice{{Name: "representative", Limit: 1, Fields: []SemanticField{{Name: "status", Selector: status}}}},
			}},
		},
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("prepared physical plan does not validate: %v", err)
	}
	var child *PhysicalSet
	for index := range plan.Operations {
		if plan.Operations[index].Kind == PhysicalSetOp && plan.Operations[index].Set != nil {
			child = plan.Operations[index].Set
			break
		}
	}
	if child == nil || child.Prepared == nil {
		t.Fatalf("expected rich child set to have a prepared selector projection: %#v", child)
	}
	if len(child.Prepared.Fields) != 1 || child.Prepared.Fields[0].Selector.CanonicalPath() != "id" {
		t.Fatalf("prepared selector fields = %#v, want one stable id field", child.Prepared.Fields)
	}
	preparedSet := child.Prepared.Variable
	var preparedRefs int
	var check func(PhysicalExpression)
	check = func(expression PhysicalExpression) {
		switch expression.Kind {
		case PhysicalAggregateExpression:
			if expression.Aggregate != nil && expression.Aggregate.Value != nil && expression.Aggregate.Value.Extract != nil && expression.Aggregate.Value.Extract.Prepared != nil {
				preparedRefs++
			}
		case PhysicalPivotExpression:
			if expression.Pivot != nil && expression.Pivot.PreparedKey != nil && expression.Pivot.PreparedValue != nil {
				preparedRefs += 2
			}
		case PhysicalSliceExpression:
			if expression.Slice != nil {
				for _, projection := range expression.Slice.Projections {
					if projection.Expression.Extract != nil && projection.Expression.Extract.Prepared != nil {
						preparedRefs++
					}
				}
			}
		}
	}
	for _, operation := range plan.Operations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil {
				check(*projection.Expression)
			}
		}
	}
	if preparedRefs != 2 {
		t.Fatalf("prepared rich consumer references = %d, want 2 (one shared pivot key/value pair)", preparedRefs)
	}
	renderedA, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	renderedB, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if renderedA.Query != renderedB.Query {
		t.Fatalf("prepared rendering is nondeterministic:\nA:\n%s\nB:\n%s", renderedA.Query, renderedB.Query)
	}
	if !strings.Contains(renderedA.Query, "LET "+preparedSet+" = (") {
		t.Fatalf("prepared selector set was not rendered:\n%s", renderedA.Query)
	}
}

func TestPreparedSelectorsPreserveFallbackExtraction(t *testing.T) {
	primary := Selector{Steps: []SelectorStep{{Field: "id"}}}
	fallback := Selector{Steps: []SelectorStep{{Field: "code"}}}
	policy := DefaultPhysicalOptimizationPolicy().WithRule(PhysicalOptimizationRulePreparedSelectors, true)
	plan, err := BuildGenericPhysicalPlanWithPolicy(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Fields:     []SemanticField{{Name: "status_with_fallback", Selector: primary, Fallbacks: []Selector{fallback}}},
				Aggregates: []SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &primary}},
			}},
		},
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	var child *PhysicalSet
	for index := range plan.Operations {
		if plan.Operations[index].Kind == PhysicalSetOp && plan.Operations[index].Set != nil {
			child = plan.Operations[index].Set
			break
		}
	}
	if child == nil || child.Prepared == nil {
		t.Fatalf("expected primary selector to be prepared: %#v", child)
	}
	for _, operation := range plan.Operations {
		if operation.Kind != PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "condition__status_with_fallback" && projection.Expression != nil && projection.Expression.Extract != nil {
				if projection.Expression.Extract.Prepared != nil {
					t.Fatalf("fallback field incorrectly used prepared primary value: %#v", projection.Expression.Extract.Prepared)
				}
				if len(projection.Expression.Extract.Fallbacks) != 1 {
					t.Fatalf("fallback field lost fallback selectors: %#v", projection.Expression.Extract.Fallbacks)
				}
			}
		}
	}
}

func TestBuildAndRenderGenericPhysicalPlanPivots(t *testing.T) {
	key := Selector{Steps: []SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "display"}}}
	value := Selector{Steps: []SelectorStep{{Field: "text"}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{Alias: "root", ResourceType: "Condition", Pivots: []SemanticPivot{{Name: "lab_values", ColumnSelector: key, ValueSelector: value, Columns: []string{"female", "male"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FIRST(", "COLLECT __pivot_key = __pair.key", "__loom_pivot_root_lab_values[@pivot_root_lab_values_columns_female"} {
		if !strings.Contains(rendered.Query, want) && want != "lab_values" {
			t.Fatalf("pivot query missing %q:\n%s", want, rendered.Query)
		}
	}
	for _, key := range []string{"pivot_root_lab_values_columns", "pivot_root_lab_values_columns_female", "pivot_root_lab_values_columns_male"} {
		if got := rendered.BindVars[key]; got == nil {
			t.Fatalf("pivot columns bind %q missing: %#v", key, rendered.BindVars)
		}
	}
	if rendered.BindVars["__loom_physical_projection_1_name"] != "lab_values__female" || rendered.BindVars["__loom_physical_projection_2_name"] != "lab_values__male" {
		t.Fatalf("flattened pivot projection names missing: %#v", rendered.BindVars)
	}
}

func TestChildPivotRetainsPayloadForDeferredEvaluation(t *testing.T) {
	code := Selector{Steps: []SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "display"}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "p",
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Pivots: []SemanticPivot{{Name: "code_values", ColumnSelector: code, ValueSelector: code, Columns: []string{"active"}}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var childSet *PhysicalSet
	for _, operation := range plan.Operations {
		if operation.Kind == PhysicalSetOp && operation.Set != nil && operation.Set.Variable == "child_set_1" {
			childSet = operation.Set
			break
		}
	}
	if childSet == nil {
		t.Fatalf("pivot child set was not lowered: %#v", plan.Operations)
	}
	if childSet.Projection != nil {
		t.Fatalf("pivot child set must retain payload, got selector-only projection: %#v", childSet.Projection)
	}
	if childSet.Output == nil || !containsPhysicalSetOutputField(childSet.Output.Fields, PhysicalSetPayloadField) {
		t.Fatalf("pivot child set must retain payload output: %#v", childSet.Output)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "payload: child_set_1_node.payload") {
		t.Fatalf("pivot child payload was omitted from rendered set:\n%s", rendered.Query)
	}
}

func containsPhysicalSetOutputField(fields []PhysicalSetOutputField, want PhysicalSetOutputField) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func TestRenderGenericPivotFlattensRepeatedItemSource(t *testing.T) {
	codeText := Selector{Steps: []SelectorStep{{Field: "code"}, {Field: "text"}}}
	valueString := Selector{Steps: []SelectorStep{{Field: "valueString"}}}
	componentItems := Selector{Steps: []SelectorStep{{Field: "component", Iterate: true}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1, Project: "p", Root: SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []SemanticPivot{{
			Name: "component_values", ColumnSelector: codeText, ValueSelector: valueString,
			ItemSource: componentItems, ItemResourceType: "ObservationComponent", Columns: []string{"Component"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FOR __loom_physical_pivot_item_value IN FLATTEN(") {
		t.Fatalf("repeated pivot item source was not flattened:\n%s", rendered.Query)
	}
}

func TestBuildGenericPhysicalPlanRejectsUnsupportedSemanticFeatures(t *testing.T) {
	tests := []struct {
		name string
		root SemanticNode
		want string
	}{
		{"unknown root", SemanticNode{Alias: "root", ResourceType: "Unknown"}, "not represented"},
		{"child filter with incomplete type metadata", SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient", Filters: []TypedFilter{{FieldRef: "status"}}}}}, "unknown filter field kind"},
		{"unknown traversal", SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{Alias: "medication", ResourceType: "Medication", EdgeLabel: "missing"}}}, "not represented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildGenericPhysicalPlan(SemanticPlan{Version: 1, Project: "p", Root: test.root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want substring %q", err, test.want)
			}
		})
	}
}
