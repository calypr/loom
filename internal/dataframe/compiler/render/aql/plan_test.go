package aql_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
)

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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
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

func TestBuildAndRenderGenericPhysicalPlanPivots(t *testing.T) {
	key := spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}, {Field: "coding", Iterate: true}, {Field: "display"}}}
	value := spec.Selector{Steps: []spec.SelectorStep{{Field: "text"}}}
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Condition", Pivots: []recipeplan.SemanticPivot{{Name: "lab_values", ColumnSelector: key, ValueSelector: value, Columns: []string{"female", "male"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.Render(plan, aql.Options{})
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
	rendered, err := aql.Render(plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FOR __loom_physical_pivot_item_value IN FLATTEN(") {
		t.Fatalf("repeated pivot item source was not flattened:\n%s", rendered.Query)
	}
}
