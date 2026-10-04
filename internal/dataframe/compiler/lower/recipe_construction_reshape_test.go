package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionExpandThenGroupUsesTypedIntermediateColumns(t *testing.T) {
	output := recipe.Output{
		Name: "construction_group_expand", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
						ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
						OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
					}},
					Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
				},
				{
					ID: "group_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_tags"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_tag_values",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
						Aggregates: []recipe.ConstructionGroupAggregate{
							{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
							{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
							{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "grouped_tag_id", Name: "tag"}, {ID: "rows_id", Name: "rows"},
						{ID: "status_count_id", Name: "status_count"}, {ID: "status_distinct_id", Name: "status_distinct"},
					},
				},
			},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 2 {
		t.Fatalf("construction stages = %#v, want EXPAND followed by GROUP", sequence)
	}
	if sequence.Stages[0].Kind != ir.PhysicalStageExpandOp || sequence.Stages[1].Kind != ir.PhysicalStageGroupOp {
		t.Fatalf("stage kinds = %q, %q, want EXPAND then GROUP", sequence.Stages[0].Kind, sequence.Stages[1].Kind)
	}
	if sequence.Stages[1].InputStageID != sequence.Stages[0].ID {
		t.Fatalf("group input stage = %q, want prior expand stage %q", sequence.Stages[1].InputStageID, sequence.Stages[0].ID)
	}
	if sequence.Stages[0].Expand.EmptyPolicy != ir.PhysicalUnnestPreserveParent || sequence.Stages[0].Expand.OrdinalColumn != "ordinal" {
		t.Fatalf("expand contract = %#v, want preserve-parent with public ordinal", sequence.Stages[0].Expand)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"tag", "rows", "status_count", "status_distinct", "__loom_row_id"}; !equalStringSlices(got, want) {
		t.Fatalf("final schema = %#v, want %#v", got, want)
	}
	if compiled.OutputSchema[1].Kind != string(expression.KindInteger) || compiled.OutputSchema[2].Kind != string(expression.KindInteger) || compiled.OutputSchema[3].Kind != string(expression.KindInteger) {
		t.Fatalf("group count output types = %#v, want integers", compiled.OutputSchema[1:4])
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"CONSTRUCTION_EXPAND_ARRAY_TYPE_MISMATCH", "CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH", "RANGE(0,", "[\"ordinal\",", "COLLECT ",
		"CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH", "SORTED_UNIQUE(", "__loom_construction_stage_1",
	} {
		if !strings.Contains(rendered.Query, expected) {
			t.Errorf("AQL stage sequence is missing %q:\n%s", expected, rendered.Query)
		}
	}
	if _, ok := rendered.BindVars[sequence.Stages[0].Expand.ConstructionIDBindKey]; !ok {
		t.Fatal("expand construction identity was not bound")
	}
}

func TestConstructionPivotTracksMissingAfterDirectNestedSourceProjection(t *testing.T) {
	categoryValue := "BodyStructure/2f9db8e2-82b9-4f75-8c33-a5327192dfc8"
	output := recipe.Output{
		Name: "specimen_missing_category", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{
			{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "body_site_reference", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.collection.bodySite.reference.reference"}},
			{Name: "subject_reference", ColumnID: "value_id", Expr: recipe.Expression{Select: "root.subject.reference"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "specimen_id", Name: "specimen_id"},
				{ID: "category_id", Name: "body_site_reference"},
				{ID: "value_id", Name: "subject_reference"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "specimen_pivot", GroupKeyIDs: []string{"specimen_id"},
					CategoryColumnID: "category_id", ValueColumnID: "value_id",
					Categories: []recipe.ConstructionPivotCategory{{
						Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &categoryValue}, OutputColumnID: "body_site_output",
					}},
					DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id"}, {ID: "body_site_output", Name: "body_site"}},
			}},
		},
	}

	compiled := compileDerivedTestOutput(t, output)
	var sourceCategory ir.PhysicalProjection
	for _, operation := range compiled.Plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "body_site_reference" {
				sourceCategory = projection
			}
		}
	}
	if sourceCategory.Presence == nil {
		t.Fatalf("direct nested source projection lost its presence proof: %#v", sourceCategory)
	}
	stage := compiled.Plan.StageSequence.Stages[0]
	var pivotCategory ir.PhysicalProjection
	for _, projection := range stage.GroupedPivot.InputProjections {
		if projection.Name == "body_site_reference" {
			pivotCategory = projection
		}
	}
	if pivotCategory.Name == "" || pivotCategory.Presence != nil {
		t.Fatalf("construction stage should expose the materialized column without source presence metadata: %#v", pivotCategory)
	}

	missing := output
	construction := *output.Construction
	construction.Steps = append([]recipe.ConstructionStep(nil), output.Construction.Steps...)
	step := construction.Steps[0]
	pivot := *step.Operation.Pivot
	pivot.Categories = append([]recipe.ConstructionPivotCategory(nil), step.Operation.Pivot.Categories...)
	pivot.Categories[0].Key = recipe.TableScalar{Kind: recipe.TableScalarMissing}
	step.Operation.Pivot = &pivot
	construction.Steps[0] = step
	missing.Construction = &construction
	missingBundle, err := compileDerivedTestBundle(t, missing)
	if err != nil {
		t.Fatalf("compile construction Pivot with an exact missing category: %v", err)
	}
	missingStage := missingBundle.Plan.StageSequence.Stages[0]
	if missingStage.GroupedPivot == nil || !missingStage.GroupedPivot.CategoryPresenceFromInput ||
		missingStage.GroupedPivot.CategoryPresenceColumn == "" {
		t.Fatalf("missing category has no materialized source-presence companion: %#v", missingStage.GroupedPivot)
	}
	var companionProjection bool
	for _, projection := range missingStage.GroupedPivot.InputProjections {
		if projection.Name == missingStage.GroupedPivot.CategoryPresenceColumn && projection.Hidden {
			companionProjection = true
		}
	}
	if !companionProjection {
		t.Fatalf("missing category companion %q is not a hidden Pivot input projection: %#v", missingStage.GroupedPivot.CategoryPresenceColumn, missingStage.GroupedPivot.InputProjections)
	}

	null := output
	nullConstruction := *output.Construction
	nullConstruction.Steps = append([]recipe.ConstructionStep(nil), output.Construction.Steps...)
	nullStep := nullConstruction.Steps[0]
	nullPivot := *nullStep.Operation.Pivot
	nullPivot.Categories = append([]recipe.ConstructionPivotCategory(nil), nullStep.Operation.Pivot.Categories...)
	nullPivot.Categories[0].Key = recipe.TableScalar{Kind: recipe.TableScalarNull}
	nullStep.Operation.Pivot = &nullPivot
	nullConstruction.Steps[0] = nullStep
	null.Construction = &nullConstruction
	nullCompiled, err := compileDerivedTestBundle(t, null)
	if err != nil {
		t.Fatalf("compile construction Pivot with an exact NULL category: %v", err)
	}
	nullStage := nullCompiled.Plan.StageSequence.Stages[0]
	if nullStage.GroupedPivot == nil || !nullStage.GroupedPivot.CategoryPresenceFromInput || nullStage.GroupedPivot.CategoryPresenceColumn == "" {
		t.Fatalf("NULL-only category has no materialized source-presence companion: %#v", nullStage.GroupedPivot)
	}
	nullRendered, err := aql.RenderPhysicalPlan(nullCompiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nullRendered.Query, " == true AND ") || !strings.Contains(nullRendered.Query, " == null)") {
		t.Fatalf("NULL category match does not require source presence:\n%s", nullRendered.Query)
	}
}

func TestConstructionPivotTracksPresenceForExactRelatedField(t *testing.T) {
	output := constructionTestOutput()
	construction := relatedFieldTestConstruction([]string{"RELATED_EXPAND", "RELATED_FIELD"})
	construction.Steps = append(construction.Steps, recipe.ConstructionStep{
		ID: "pivot_related_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "related_step_2"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "pivot_missing_related_status", GroupKeyIDs: []string{"group_id"},
			CategoryColumnID: "observation_status", ValueColumnID: "amount_id",
			Categories:      []recipe.ConstructionPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_status_id"}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group"}, {ID: "missing_status_id", Name: "missing_status"}},
	})
	output.Construction = construction

	compiled, err := compileDerivedTestBundle(t, output)
	if err != nil {
		t.Fatalf("compile Pivot over an exact related-field MISSING category: %v", err)
	}
	fieldStage := compiled.Plan.StageSequence.Stages[1]
	var presenceOnly bool
	for _, projection := range fieldStage.OutputProjections {
		if projection.Hidden && projection.Expression != nil && projection.Expression.RelatedField != nil && projection.Expression.RelatedField.PresenceOnly {
			presenceOnly = true
		}
	}
	if !presenceOnly {
		t.Fatalf("related-field stage has no exact scoped presence projection: %#v", fieldStage.OutputProjections)
	}
	pivot := compiled.Plan.StageSequence.Stages[2].GroupedPivot
	if pivot == nil || !pivot.CategoryPresenceFromInput || pivot.CategoryPresenceColumn == "" {
		t.Fatalf("related-field Pivot lost its materialized presence input: %#v", pivot)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FIRST(APPEND(") || !strings.Contains(rendered.Query, "HAS(related_field_document.payload") {
		t.Fatalf("related-field presence query does not distinguish missing from null:\n%s", rendered.Query)
	}

	nullConstruction := *construction
	nullConstruction.Steps = append([]recipe.ConstructionStep(nil), construction.Steps...)
	nullStepIndex := len(nullConstruction.Steps) - 1
	nullStep := nullConstruction.Steps[nullStepIndex]
	nullPivot := *nullStep.Operation.Pivot
	nullPivot.Categories = append([]recipe.ConstructionPivotCategory(nil), nullStep.Operation.Pivot.Categories...)
	nullPivot.Categories[0].Key = recipe.TableScalar{Kind: recipe.TableScalarNull}
	nullStep.Operation.Pivot = &nullPivot
	nullConstruction.Steps[nullStepIndex] = nullStep
	output.Construction = &nullConstruction
	nullCompiled, err := compileDerivedTestBundle(t, output)
	if err != nil {
		t.Fatalf("compile NULL-only Pivot over an exact related-field category: %v", err)
	}
	nullPivotStage := nullCompiled.Plan.StageSequence.Stages[2].GroupedPivot
	if nullPivotStage == nil || !nullPivotStage.CategoryPresenceFromInput || nullPivotStage.CategoryPresenceColumn == "" {
		t.Fatalf("related-field NULL category lost its materialized presence input: %#v", nullPivotStage)
	}
	nullRendered, err := aql.RenderPhysicalPlan(nullCompiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nullRendered.Query, " == true AND ") || !strings.Contains(nullRendered.Query, " == null)") {
		t.Fatalf("related-field NULL category match does not require target presence:\n%s", nullRendered.Query)
	}
}

func TestConstructionExpandExcludeUsesRequiredItemAndOrdinal(t *testing.T) {
	output := recipe.Output{
		Name: "construction_expand_exclude", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
					OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionExclude,
				}},
				Outputs: []recipe.StageColumn{{ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
			}},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Expand.EmptyPolicy != ir.PhysicalUnnestExclude {
		t.Fatalf("empty policy = %q, want EXCLUDE", stage.Expand.EmptyPolicy)
	}
	for _, column := range stage.OutputColumns {
		if column.Name == "tag" || column.Name == "ordinal" {
			if column.Cardinality != string(expression.RequiredOne) {
				t.Errorf("%s cardinality = %q, want required_one", column.Name, column.Cardinality)
			}
		}
	}
	if _, err := aql.RenderPhysicalPlan(compiled.Plan); err != nil {
		t.Fatal(err)
	}
}

func TestConstructionExpandErrorUsesStableAssertionCode(t *testing.T) {
	output := recipe.Output{
		Name: "construction_expand_error", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
					OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionError,
				}},
				Outputs: []recipe.StageColumn{{ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
			}},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "CONSTRUCTION_EXPANSION_EMPTY: construction ") {
		t.Fatalf("ERROR empty policy omitted its stable assertion code:\n%s", rendered.Query)
	}
}

func TestConstructionGroupAllowsZeroKeysAndExplicitCountSemantics(t *testing.T) {
	output := recipe.Output{
		Name: "construction_summary", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "amount_id", Name: "amount"}},
			Steps: []recipe.ConstructionStep{{
				ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "whole_table_summary",
					Aggregates: []recipe.ConstructionGroupAggregate{
						{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
						{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						{Operation: recipe.ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
						{Operation: recipe.ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
					},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "rows_id", Name: "rows"}, {ID: "status_count_id", Name: "status_count"},
					{ID: "status_distinct_id", Name: "status_distinct"}, {ID: "amount_sum_id", Name: "amount_sum"},
					{ID: "amount_mean_id", Name: "amount_mean"},
				},
			}},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || len(stage.Group.Keys) != 0 {
		t.Fatalf("group stage = %#v, want zero-key table summary", stage)
	}
	if stage.Group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup {
		t.Fatalf("zero-key summary missing-key policy = %q, want GROUP", stage.Group.MissingKeyPolicy)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"rows", "status_count", "status_distinct", "amount_sum", "amount_mean", "__loom_row_id"}; !equalStringSlices(got, want) {
		t.Fatalf("summary schema = %#v, want %#v", got, want)
	}
	if compiled.OutputSchema[3].Kind != string(expression.KindDecimal) && compiled.OutputSchema[3].Kind != string(expression.KindInteger) {
		t.Fatalf("sum output type = %q, want numeric", compiled.OutputSchema[3].Kind)
	}
	if compiled.OutputSchema[4].Kind != string(expression.KindDecimal) {
		t.Fatalf("mean output type = %q, want decimal", compiled.OutputSchema[4].Kind)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "== 0 ? [null] :") {
		t.Fatalf("zero-key summary does not synthesize an empty group:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, " = null INTO ") {
		t.Fatalf("zero-key summary does not COLLECT into one constant group:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "COUNT") && !strings.Contains(rendered.Query, "LENGTH(") {
		t.Fatalf("summary AQL has no explicit row count:\n%s", rendered.Query)
	}
}

func TestConstructionGroupMissingKeyPoliciesLowerToTypedIRAndAQL(t *testing.T) {
	tests := []struct {
		name       string
		policy     recipe.ConstructionGroupMissingKeyPolicy
		wantPolicy ir.PhysicalStageGroupMissingKeyPolicy
		wantFilter bool
		wantError  bool
	}{
		{name: "legacy default", wantPolicy: ir.PhysicalStageGroupMissingKeyGroup},
		{name: "group", policy: recipe.ConstructionGroupMissingKeyGroup, wantPolicy: ir.PhysicalStageGroupMissingKeyGroup},
		{name: "exclude", policy: recipe.ConstructionGroupMissingKeyExclude, wantPolicy: ir.PhysicalStageGroupMissingKeyExclude, wantFilter: true},
		{name: "error", policy: recipe.ConstructionGroupMissingKeyError, wantPolicy: ir.PhysicalStageGroupMissingKeyError, wantFilter: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := recipe.Output{
				Name: "group_missing_key_policy", RootResourceType: "Observation", RowGrain: "observation",
				Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
				Construction: &recipe.Construction{
					Version:       1,
					SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
					Steps: []recipe.ConstructionStep{{
						ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
						Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
							ConstructionID: "group_status", MissingKeyPolicy: test.policy,
							Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "group_status_id"}},
							Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
						}},
						Outputs: []recipe.StageColumn{{ID: "group_status_id", Name: "status", Type: "string"}, {ID: "row_count_id", Name: "rows", Type: "integer"}},
					}},
				},
			}
			compiled := compileDerivedTestOutput(t, output)
			stage := compiled.Plan.StageSequence.Stages[0]
			if stage.Group.MissingKeyPolicy != test.wantPolicy {
				t.Fatalf("lowered missing-key policy = %q, want %q", stage.Group.MissingKeyPolicy, test.wantPolicy)
			}
			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatalf("render physical plan: %v", err)
			}
			if got := strings.Contains(rendered.Query, "CONSTRUCTION_GROUP_MISSING_KEY"); got != test.wantError {
				t.Fatalf("query has missing-key error assertion = %t, want %t:\n%s", got, test.wantError, rendered.Query)
			}
			hasMissingFilter := false
			for _, line := range strings.Split(rendered.Query, "\n") {
				if strings.Contains(line, "FILTER ") && strings.Contains(line, " != null") {
					hasMissingFilter = true
					break
				}
			}
			if hasMissingFilter != test.wantFilter {
				t.Fatalf("query has missing-key filter = %t, want %t:\n%s", hasMissingFilter, test.wantFilter, rendered.Query)
			}
		})
	}
}
