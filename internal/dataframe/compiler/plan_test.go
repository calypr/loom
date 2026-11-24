package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
)

func TestBuildGenericPhysicalPlanNavigationSkeleton(t *testing.T) {
	output := recipeplan.OutputPlan{
		Root: recipeplan.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []recipeplan.SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []recipeplan.SemanticNode{{Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen"}},
			}},
		},
	}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(output, recipeplan.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}}, ir.DefaultPhysicalOptimizationPolicy())
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Observation"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != true {
		t.Fatalf("empty auth paths were not represented as unrestricted: %#v", plan.BindVars)
	}
}

func TestBuildAndRenderGenericPhysicalPlanOptionalChildFieldsAndFilters(t *testing.T) {
	wantID := "file-1"
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{
		Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
		Fields:  []recipeplan.SemanticField{{Name: "file_id", FieldRef: "DocumentReference.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionFirst}},
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{
		Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
		Fields: []recipeplan.SemanticField{
			{Name: "file_id", FieldRef: "DocumentReference.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionFirst},
			{Name: "description", FieldRef: "DocumentReference.description", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "description"})}, Projection: spec.ProjectionFirst},
			{Name: "titles", FieldRef: "DocumentReference.content[].attachment.title", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "content[].attachment.title"})}, Projection: spec.ProjectionArray},
			{Name: "distinct_titles", FieldRef: "DocumentReference.content[].attachment.title", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "content[].attachment.title"})}, Projection: spec.ProjectionDistinctArray},
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Fields: []recipeplan.SemanticField{{
				Name: "specimen_id", FieldRef: "Specimen.id",
				Expr:       recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})},
				Projection: spec.ProjectionFirst,
			}},
			Children: []recipeplan.SemanticNode{{
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Children: []recipeplan.SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
				Fields:     []recipeplan.SemanticField{{Name: "id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionFirst}},
				Filters:    []spec.TypedFilter{{FieldRef: "DocumentReference.id", Selector: "id", FieldKind: spec.FilterString, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &id}}}},
				Aggregates: []recipeplan.SemanticAggregate{{Name: "count", Operation: "COUNT"}},
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Aggregates: []recipeplan.SemanticAggregate{
			{Name: "patient_count", Operation: "COUNT"},
			{Name: "gender_value_count", Operation: "COUNT", Selector: &gender},
			{Name: "genders", Operation: "DISTINCT_VALUES", Selector: &gender},
			{Name: "one_gender", Operation: "REQUIRE_ONE", Selector: &gender},
			{Name: "all_genders", Operation: "COLLECT", Selector: &gender},
		},
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{{Name: "count", Operation: "COUNT"}},
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
	for _, want := range []string{"LENGTH([root])", "FILTER __loom_physical_aggregate_count_value != null", "SORTED_UNIQUE(FLATTEN(", "ASSERT(LENGTH(", "RELATIONSHIP_CARDINALITY_VIOLATION", "LENGTH(child_set_1)", "[@__loom_physical_projection_1_name]"} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("aggregate query missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["__loom_physical_projection_3_name"]; got != "genders" {
		t.Fatalf("projection bind = %#v", got)
	}
}

func TestBuildAndRenderOrderedContributorWindowAggregate(t *testing.T) {
	value, err := spec.ParseSelector("valueQuantity.value")
	if err != nil {
		t.Fatal(err)
	}
	timestamp, err := spec.ParseSelector("effectiveDateTime")
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := spec.ParseSelector("meta.lastUpdated")
	if err != nil {
		t.Fatal(err)
	}
	system, err := spec.ParseSelector("valueQuantity.system")
	if err != nil {
		t.Fatal(err)
	}
	code, err := spec.ParseSelector("valueQuantity.code")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := unit.ResolveApprovedUnitPolicy("to-centimeters", "1")
	if err != nil {
		t.Fatal(err)
	}
	dimension, rules, err := unit.ResolveApprovedUnitRules(policy.Rules, policy.Target)
	if err != nil {
		t.Fatal(err)
	}
	window := &recipeplan.SemanticContributorWindow{
		Timestamp: timestamp, Anchor: anchor, AnchorResource: "Patient",
		LowerOffset: -86400, UpperOffset: 0, LowerInclusive: true, UpperInclusive: true,
		Precision: "INSTANT",
	}
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "observation", ResourceType: "Observation", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{
				{
					Name: "latest_height", Operation: "FIRST_ORDERED", Selector: &value,
					UnitSystemSelector: &system, UnitCodeSelector: &code,
					UnitNormalization: &unit.UnitNormalization{Target: policy.Target, Dimension: dimension, Rules: rules},
					ContributorWindow: window,
					Ordering:          &recipeplan.SemanticTemporalOrdering{Timestamp: timestamp, Direction: "DESC", TiePolicy: "REQUIRE_UNIQUE"},
				},
				{
					Name: "sum_height", Operation: "SUM", Selector: &value,
					UnitSystemSelector: &system, UnitCodeSelector: &code,
					UnitNormalization: &unit.UnitNormalization{Target: policy.Target, Dimension: dimension, Rules: rules},
					ContributorWindow: window,
				},
			},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TEMPORAL_ANCHOR_INVALID", "TEMPORAL_PRECISION_UNSUPPORTED", "TEMPORAL_TIE_AMBIGUOUS", "SORT DATE_TIMESTAMP(__loom_temporal_timestamp) DESC", "__loom_physical_contributor_window_anchor = root.payload.meta.lastUpdated", "DATE_ADD(__loom_physical_contributor_window_anchor", "UNIT_IDENTITY_UNKNOWN", ".scale +"} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("ordered temporal query missing %q:\n%s", want, rendered.Query)
		}
	}
	windowFilter := strings.Index(rendered.Query, "LET __loom_physical_contributor_window_eligible =")
	unitIdentityCheck := strings.Index(rendered.Query, "UNIT_IDENTITY_UNKNOWN")
	if windowFilter < 0 || unitIdentityCheck <= windowFilter {
		t.Fatalf("contributor window must filter resources before unit normalization; filter=%d conversion=%d:\n%s", windowFilter, unitIdentityCheck, rendered.Query)
	}
	if got := strings.Count(rendered.Query, "LET __loom_physical_contributor_window_eligible"); got != 2 {
		t.Fatalf("ordered and numeric reducers must each consume a contributor window, got %d filters:\n%s", got, rendered.Query)
	}
	if !strings.Contains(rendered.Query, "FOR __loom_physical_aggregate_value_item IN FIRST(FOR __loom_physical_contributor_window_scope") {
		t.Fatalf("numeric value extraction did not iterate the eligible window set:\n%s", rendered.Query)
	}
	if got := strings.Count(rendered.Query, "LET __loom_physical_temporal_candidates ="); got != 1 {
		t.Fatalf("temporal candidates evaluated %d times, want once:\n%s", got, rendered.Query)
	}
	for _, check := range []struct {
		name   string
		filter string
		assert string
	}{
		{
			name:   "window timestamp",
			filter: "FILTER __loom_physical_contributor_window_timestamp != null",
			assert: "FILTER ASSERT(REGEX_TEST(TO_STRING(__loom_physical_contributor_window_timestamp)",
		},
		{
			name:   "ordering timestamp",
			filter: "FILTER __loom_temporal_timestamp != null",
			assert: "FILTER ASSERT(REGEX_TEST(TO_STRING(__loom_temporal_timestamp)",
		},
	} {
		nullFilter := strings.Index(rendered.Query, check.filter)
		precisionAssertion := strings.Index(rendered.Query, check.assert)
		if nullFilter < 0 || precisionAssertion <= nullFilter {
			t.Fatalf("%s must exclude null timestamps before validating non-null precision; filter=%d assert=%d:\n%s", check.name, nullFilter, precisionAssertion, rendered.Query)
		}
	}
	foundLower, foundUpper := false, false
	for _, value := range rendered.BindVars {
		foundLower = foundLower || value == int64(-86400)
		foundUpper = foundUpper || value == int64(0)
	}
	if !foundLower || !foundUpper {
		t.Fatalf("temporal offsets missing from binds: %#v", rendered.BindVars)
	}
}

func TestBuildAndRenderGenericPhysicalPlanRepresentativeSlices(t *testing.T) {
	gender := spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}}
	title := spec.Selector{Steps: []spec.SelectorStep{{Field: "content", Iterate: true}, {Field: "attachment"}, {Field: "title"}}}
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Slices: []recipeplan.SemanticSlice{{Name: "representative", Limit: 2, Predicate: &gender, PredicateEquals: "female", Fields: []recipeplan.SemanticField{testSemanticField("gender", gender, spec.ProjectionFirst)}}},
		Children: []recipeplan.SemanticNode{{
			Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Patient",
			Slices: []recipeplan.SemanticSlice{{Name: "representative_files", Limit: 1, Fields: []recipeplan.SemanticField{testSemanticField("title", title, spec.ProjectionFirst)}}},
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Aggregates: []recipeplan.SemanticAggregate{{Name: "female_count", Operation: "COUNT", Predicate: &spec.TypedFilter{FieldRef: "Patient.gender", Selector: "gender", FieldKind: spec.FilterString, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: stringPtr("female")}}}}},
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{{Name: "available_count", Operation: "COUNT_DISTINCT", Selector: &status, Predicate: &spec.TypedFilter{FieldRef: "Specimen.status", Selector: "status", FieldKind: spec.FilterString, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: stringPtr("available")}}}}},
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
	if rendered.BindVars["aggregate_root_female_count_predicate_value"] != "female" || rendered.BindVars["aggregate_child_set_1_available_count_predicate_value"] != "available" {
		t.Fatalf("predicate binds missing: %#v", rendered.BindVars)
	}
}

func TestBuildAndRenderGenericPhysicalPlanContainsAll(t *testing.T) {
	typeCode := spec.Selector{Steps: []spec.SelectorStep{{Field: "type"}, {Field: "coding", Iterate: true}, {Field: "code"}}}
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{{Name: "paired", Operation: "CONTAINS_ALL", Selector: &typeCode, RequiredValues: []string{"Tumor", "Normal"}}},
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
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &status}},
			Pivots:     []recipeplan.SemanticPivot{{Name: "status_values", ColumnSelector: status, ValueSelector: status, Columns: []string{"active", "resolved"}}},
			Slices:     []recipeplan.SemanticSlice{{Name: "representative", Limit: 1, Fields: []recipeplan.SemanticField{testSemanticField("status", status, "")}}},
		}},
	},
	}, recipeplan.ExecutionContext{Project: "p"}, policy)
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
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Fields:     []recipeplan.SemanticField{testSemanticFieldWithFallback("status_with_fallback", primary, fallback)},
			Aggregates: []recipeplan.SemanticAggregate{{Name: "status_count", Operation: "COUNT_DISTINCT", Selector: &primary}},
		}},
	},
	}, recipeplan.ExecutionContext{Project: "p"}, policy)
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Condition", Pivots: []recipeplan.SemanticPivot{{Name: "lab_values", ColumnSelector: key, ValueSelector: value, Columns: []string{"female", "male"}}}}})
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{
		Root: recipeplan.SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []recipeplan.SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Pivots: []recipeplan.SemanticPivot{{Name: "code_values", ColumnSelector: code, ValueSelector: code, Columns: []string{"active"}}},
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
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []recipeplan.SemanticPivot{{
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
		root recipeplan.SemanticNode
		want string
	}{
		{"unknown root", recipeplan.SemanticNode{Alias: "root", ResourceType: "Unknown"}, "not represented"},
		{"child filter with incomplete type metadata", recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient", Filters: []spec.TypedFilter{{FieldRef: "status"}}}}}, "unknown filter field kind"},
		{"unknown traversal", recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{Alias: "medication", ResourceType: "Medication", EdgeLabel: "missing"}}}, "not represented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: test.root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want substring %q", err, test.want)
			}
		})
	}
}

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
			Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "x", Expression: physicalValueExpression(ir.PhysicalValue{Variable: "future"}, ir.PhysicalScalarCardinality)}}},
			Return:     physicalValueExpression(ir.PhysicalValue{Variable: "x"}, ir.PhysicalScalarCardinality),
		},
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "capture \"future\" is out of scope") {
		t.Fatalf("Validate() error = %v; want out-of-scope capture", err)
	}

	plan = validPhysicalPlan()
	plan.Version = 2
	plan.Operations[3] = ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
		Variable: "pivot",
		Expression: ir.PhysicalExpression{Kind: ir.PhysicalPivotExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Pivot: &ir.PhysicalPivotMap{
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
