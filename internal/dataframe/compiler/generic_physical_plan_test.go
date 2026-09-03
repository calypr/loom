package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestBuildGenericPhysicalPlanNavigationSkeleton(t *testing.T) {
	output := semantic.OutputPlan{
		Root: semantic.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []semantic.SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []semantic.SemanticNode{{Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen"}},
			}},
		},
	}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(output, semantic.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}}, ir.DefaultPhysicalOptimizationPolicy())
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
			if operation.Traversal.Direction != ir.PhysicalInbound {
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
	if last.Kind != ir.PhysicalReturnOp || len(last.Return.Projections) != 1 || last.Return.Projections[0].Name != "_key" {
		t.Fatalf("unexpected terminal return: %#v", last)
	}
}

func TestBuildGenericPhysicalPlanEmptyAuthScopeIsExplicit(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Observation"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != true {
		t.Fatalf("empty auth paths were not represented as unrestricted: %#v", plan.BindVars)
	}
}

func TestBuildAndRenderGenericPhysicalPlanOptionalChildFieldsAndFilters(t *testing.T) {
	wantID := "file-1"
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{
		Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
		Fields:  []semantic.SemanticField{{Name: "file_id", FieldRef: "DocumentReference.id", Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionFirst}},
		Filters: []spec.TypedFilter{{FieldRef: "DocumentReference.id", Selector: "id", FieldKind: spec.FilterString, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &wantID}}}},
	}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sets int
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp {
			sets++
		}
	}
	if sets != 1 {
		t.Fatalf("optional child was not lowered to one physical set: %#v", plan.Operations)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
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

func TestBuildAndRenderGenericPhysicalPlanReducesDirectChildFieldsOnce(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{
		Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
		Fields: []semantic.SemanticField{
			{Name: "file_id", FieldRef: "DocumentReference.id", Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionFirst},
			{Name: "description", FieldRef: "DocumentReference.description", Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "description"})}, Projection: spec.ProjectionFirst},
			{Name: "titles", FieldRef: "DocumentReference.content[].attachment.title", Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "content[].attachment.title"})}, Projection: spec.ProjectionArray},
			{Name: "distinct_titles", FieldRef: "DocumentReference.content[].attachment.title", Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "content[].attachment.title"})}, Projection: spec.ProjectionDistinctArray},
		},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "LET child_set_1_reduced = {") {
		t.Fatalf("direct child fields were not reduced beside their set:\n%s", rendered.Query)
	}
	if strings.Contains(rendered.Query, "FOR __loom_prepared_value IN child_set_1") {
		t.Fatalf("final child fields still rescan the relationship set:\n%s", rendered.Query)
	}
	if strings.Count(rendered.Query, "child_set_1_reduced.__loom_reduced_") != 4 {
		t.Fatalf("final child fields do not read the reduction object exactly four times:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "SORTED_UNIQUE(FLATTEN(child_set_1[*].__loom_projection_") {
		t.Fatalf("distinct child array was not reduced with sorted uniqueness:\n%s", rendered.Query)
	}
}

func TestCompileRecipeOutputPreservesMixedChildDependencyOrder(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Fields: []semantic.SemanticField{{
				Name: "specimen_id", FieldRef: "Specimen.id",
				Expr:       semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})},
				Projection: spec.ProjectionFirst,
			}},
			Children: []semantic.SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
			}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := CompileRecipeOutputWithPolicy(lower.CompiledRecipeOutput{
		Name:             "patients",
		RootResourceType: "Patient",
		RowGrain:         "patient",
		Plan:             plan,
	}, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}

	parentSet := strings.Index(compiled.Query, "LET child_set_1 =")
	nestedTraversal := strings.Index(compiled.Query, "FOR __loom_physical_parent_1 IN child_set_1")
	if parentSet < 0 || nestedTraversal < 0 || parentSet >= nestedTraversal {
		t.Fatalf("mixed child dependencies rendered out of order:\n%s", compiled.Query)
	}
}

func TestBuildAndRenderGenericPhysicalPlanNestedOptionalFieldsAndAggregates(t *testing.T) {
	id := "file-1"
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Children: []semantic.SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
				Fields:     []semantic.SemanticField{{Name: "id", Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionFirst}},
				Filters:    []spec.TypedFilter{{FieldRef: "DocumentReference.id", Selector: "id", FieldKind: spec.FilterString, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &id}}}},
				Aggregates: []semantic.SemanticAggregate{{Name: "count", Operation: "COUNT"}},
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
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LET child_set_1 = UNIQUE((",
		"@child_set_2_filter_1_value", "LENGTH(child_set_2)", "LET child_set_2_reduced = {",
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
	gender := spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Aggregates: []semantic.SemanticAggregate{
			{Name: "patient_count", Operation: "COUNT"},
			{Name: "genders", Operation: "DISTINCT_VALUES", Selector: &gender},
		},
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Aggregates: []semantic.SemanticAggregate{{Name: "count", Operation: "COUNT"}},
		}},
	},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
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
	gender := spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}}
	title := spec.Selector{Steps: []spec.SelectorStep{{Field: "content", Iterate: true}, {Field: "attachment"}, {Field: "title"}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Slices: []semantic.SemanticSlice{{Name: "representative", Limit: 2, Predicate: &gender, PredicateEquals: "female", Fields: []semantic.SemanticField{testSemanticField("gender", gender, spec.ProjectionFirst)}}},
		Children: []semantic.SemanticNode{{
			Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
			Slices: []semantic.SemanticSlice{{Name: "representative_files", Limit: 1, Fields: []semantic.SemanticField{testSemanticField("title", title, spec.ProjectionFirst)}}},
		}},
	},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
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
	status := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	gender := spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Aggregates: []semantic.SemanticAggregate{{Name: "female_count", Operation: "COUNT", Predicate: &gender, PredicateEquals: "female"}},
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Aggregates: []semantic.SemanticAggregate{{Name: "available_count", Operation: "COUNT_DISTINCT", Selector: &status, Predicate: &status, PredicateEquals: "available"}},
		}},
	},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
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

func TestBuildAndRenderGenericPhysicalPlanContainsAll(t *testing.T) {
	typeCode := spec.Selector{Steps: []spec.SelectorStep{{Field: "type"}, {Field: "coding", Iterate: true}, {Field: "code"}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Aggregates: []semantic.SemanticAggregate{{Name: "paired", Operation: "CONTAINS_ALL", Selector: &typeCode, RequiredValues: []string{"Tumor", "Normal"}}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FILTER POSITION(FLATTEN(") || strings.Contains(rendered.Query, "POSITION(FLATTEN(") && strings.Contains(rendered.Query, ") != -1") {
		t.Fatalf("CONTAINS_ALL does not use boolean POSITION semantics:\n%s", rendered.Query)
	}
	found := false
	for key, value := range rendered.BindVars {
		values, ok := value.([]string)
		if strings.Contains(key, "required_values") && ok && len(values) == 2 && values[0] == "Tumor" && values[1] == "Normal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("required values were not bind-backed: %#v", rendered.BindVars)
	}
}

func TestBuildGenericPhysicalPlanPreparesSelectorsAcrossRichConsumers(t *testing.T) {
	status := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleCompactProjection, false).WithRule(ir.PhysicalOptimizationRulePreparedSelectors, true)
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Aggregates: []semantic.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &status}},
			Pivots:     []semantic.SemanticPivot{{Name: "status_values", ColumnSelector: status, ValueSelector: status, Columns: []string{"active", "resolved"}}},
			Slices:     []semantic.SemanticSlice{{Name: "representative", Limit: 1, Fields: []semantic.SemanticField{testSemanticField("status", status, "")}}},
		}},
	},
	}, semantic.ExecutionContext{Project: "p"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("prepared physical plan does not validate: %v", err)
	}
	var child *ir.PhysicalSet
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalSetOp && plan.Operations[index].Set != nil {
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
	var check func(ir.PhysicalExpression)
	check = func(expression ir.PhysicalExpression) {
		switch expression.Kind {
		case ir.PhysicalAggregateExpression:
			if expression.Aggregate != nil && expression.Aggregate.Value != nil && expression.Aggregate.Value.Extract != nil && expression.Aggregate.Value.Extract.Prepared != nil {
				preparedRefs++
			}
		case ir.PhysicalPivotExpression:
			if expression.Pivot != nil && expression.Pivot.PreparedKey != nil && expression.Pivot.PreparedValue != nil {
				preparedRefs += 2
			}
		case ir.PhysicalSliceExpression:
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
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
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
	renderedA, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	renderedB, err := aql.RenderPhysicalPlan(plan)
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
	primary := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	fallback := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}}}
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRulePreparedSelectors, true)
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Fields:     []semantic.SemanticField{testSemanticFieldWithFallback("status_with_fallback", primary, fallback)},
			Aggregates: []semantic.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &primary}},
		}},
	},
	}, semantic.ExecutionContext{Project: "p"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	var child *ir.PhysicalSet
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalSetOp && plan.Operations[index].Set != nil {
			child = plan.Operations[index].Set
			break
		}
	}
	if child == nil || child.Prepared == nil {
		t.Fatalf("expected primary selector to be prepared: %#v", child)
	}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
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
	key := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "display"}}}
	value := spec.Selector{Steps: []spec.SelectorStep{{Field: "text"}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Condition", Pivots: []semantic.SemanticPivot{{Name: "lab_values", ColumnSelector: key, ValueSelector: value, Columns: []string{"female", "male"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
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
	code := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "display"}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{
		Root: semantic.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []semantic.SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Pivots: []semantic.SemanticPivot{{Name: "code_values", ColumnSelector: code, ValueSelector: code, Columns: []string{"active"}}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var childSet *ir.PhysicalSet
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Set.Variable == "child_set_1" {
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
	if childSet.Output == nil || !containsPhysicalSetOutputField(childSet.Output.Fields, ir.PhysicalSetPayloadField) {
		t.Fatalf("pivot child set must retain payload output: %#v", childSet.Output)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "payload: child_set_1_node.payload") {
		t.Fatalf("pivot child payload was omitted from rendered set:\n%s", rendered.Query)
	}
}

func containsPhysicalSetOutputField(fields []ir.PhysicalSetOutputField, want ir.PhysicalSetOutputField) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func TestRenderGenericPivotFlattensRepeatedItemSource(t *testing.T) {
	codeText := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}, {Field: "text"}}}
	valueString := spec.Selector{Steps: []spec.SelectorStep{{Field: "valueString"}}}
	componentItems := spec.Selector{Steps: []spec.SelectorStep{{Field: "component", Iterate: true}}}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
		Name: "component_values", ColumnSelector: codeText, ValueSelector: valueString,
		ItemSource: componentItems, ItemResourceType: "ObservationComponent", Columns: []string{"Component"},
	}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
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
		root semantic.SemanticNode
		want string
	}{
		{"unknown root", semantic.SemanticNode{Alias: "root", ResourceType: "Unknown"}, "not represented"},
		{"child filter with incomplete type metadata", semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient", Filters: []spec.TypedFilter{{FieldRef: "status"}}}}}, "unknown filter field kind"},
		{"unknown traversal", semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{Alias: "medication", ResourceType: "Medication", EdgeLabel: "missing"}}}, "not represented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: test.root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want substring %q", err, test.want)
			}
		})
	}
}
