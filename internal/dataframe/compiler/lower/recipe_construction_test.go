package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileConstructionUsesTypedIntermediateStages(t *testing.T) {
	output := constructionTestOutput()
	compiled := compileDerivedTestOutput(t, output)
	if compiled.Plan.StageSequence == nil {
		t.Fatal("compiled plan has no typed stage sequence")
	}
	if got := len(compiled.Stages); got != 5 {
		t.Fatalf("compiled stage descriptors = %d, want source plus four operations", got)
	}
	wantOperations := []ir.PhysicalStageOperationKind{
		ir.PhysicalStagePivotOp, ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageUnpivotOp,
	}
	for index, want := range wantOperations {
		stage := compiled.Plan.StageSequence.Stages[index]
		if stage.Kind != want {
			t.Fatalf("stage %d operation = %q, want %q", index, stage.Kind, want)
		}
		if stage.InputStageID != compiled.Stages[index].ID {
			t.Fatalf("stage %q input = %q, want immediately preceding stage %q", stage.ID, stage.InputStageID, compiled.Stages[index].ID)
		}
	}
	wantNames := []string{"group", "total", "measure", "amount", "__loom_row_id"}
	if got := compiledSchemaNames(compiled.OutputSchema); !equalStringSlices(got, wantNames) {
		t.Fatalf("final construction schema = %#v, want %#v", got, wantNames)
	}
	if compiled.OutputSchema[0].ID != "group_id" || compiled.OutputSchema[1].ID != "total_id" || compiled.OutputSchema[2].ID != "measure_id" || compiled.OutputSchema[3].ID != "amount_id" {
		t.Fatalf("stable column IDs were lost in final schema: %#v", compiled.OutputSchema)
	}
	if compiled.Stages[0].ID != recipe.ConstructionSourceProjectionID || len(compiled.Stages[0].Capabilities) != 7 {
		t.Fatalf("source stage descriptor lacks exact source capabilities: %#v", compiled.Stages[0])
	}
	var relatedSourceCapability *StageOperationCapability
	for _, capability := range compiled.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			break
		}
	}
	if relatedSourceCapability == nil || relatedSourceCapability.Supported || relatedSourceCapability.ReasonCode != "NO_SOURCE_ROW_ANCHOR" {
		t.Fatalf("related source must be unavailable after a reshaped stage loses root identity: %#v", relatedSourceCapability)
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"COLLECT", " + ", "FILTER", "TABLE_UNPIVOT", "__loom_construction_stage_4"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered stage sequence is missing %q: %s", expected, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "LIMIT @") {
		t.Fatalf("lowering inserted a preview limit before the caller requested one: %s", rendered.Query)
	}
}

func TestCompileSourceOnlyConstructionKeepsSourcePlan(t *testing.T) {
	output := constructionTestOutput()
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group"},
			{ID: "category_id", Name: "category"},
			{ID: "amount_id", Name: "amount"},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	if compiled.Plan.StageSequence != nil {
		t.Fatal("source-only construction created a fake physical stage")
	}
	if len(compiled.Stages) != 1 || compiled.Stages[0].ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("source-only descriptors = %#v", compiled.Stages)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"group", "category", "amount", "_key"}; !equalStringSlices(got, want) {
		t.Fatalf("source-only schema = %#v, want %#v", got, want)
	}
}

func TestCompileRelatedSourceAddsAllMatchesAfterSelectedStage(t *testing.T) {
	output := constructionTestOutput()
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
		},
		Steps: []recipe.ConstructionStep{
			{
				ID: "keep_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "group_id", Operator: recipe.FilterExists}},
				Outputs:   []recipe.StageColumn{{ID: "group_id", Name: "group"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"}},
			},
			{
				ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_patients"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: "choice-token", SourceOccurrenceID: "observation-node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "observation-status",
				}},
				Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"}, {ID: "observation-status", Name: "observation_status", Label: "Observation statuses"}},
			},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 {
		t.Fatalf("stage sequence = %#v, want filter then related source", compiled.Plan.StageSequence)
	}
	sequence := compiled.Plan.StageSequence
	if len(sequence.SourceColumns) != 4 || sequence.SourceColumns[len(sequence.SourceColumns)-1].ID == "observation-status" {
		t.Fatalf("initial source projection was rewritten: %#v", sequence.SourceColumns)
	}
	related := sequence.Stages[1]
	if related.Kind != ir.PhysicalStageRelatedSourceOp || related.InputStageID != "keep_patients" || related.RelatedSource == nil ||
		related.RelatedSource.AnchorColumnID != "_key" || related.RelatedSource.OutputColumnID != "observation-status" {
		t.Fatalf("related source stage is not bound to exact prior row identity and schema: %#v", related)
	}
	gotRelatedColumns := make([]string, len(related.OutputColumns))
	for index, column := range related.OutputColumns {
		gotRelatedColumns[index] = column.Name
	}
	if !equalStringSlices(gotRelatedColumns, []string{"group", "category", "amount", "observation_status", "_key"}) {
		t.Fatalf("related stage output schema = %#v", gotRelatedColumns)
	}
	if related.OutputColumns[3].Cardinality != "many" || related.OutputColumns[3].Kind != "string" {
		t.Fatalf("related ALL output type = %#v, want string array", related.OutputColumns[3])
	}
	var relatedProjection *ir.PhysicalProjection
	for index := range related.OutputProjections {
		if related.OutputProjections[index].Name == "observation_status" {
			relatedProjection = &related.OutputProjections[index]
		}
	}
	if relatedProjection == nil || relatedProjection.Expression == nil || relatedProjection.Expression.Subplan == nil {
		t.Fatalf("related output has no correlated route subplan: %#v", relatedProjection)
	}
	var traversal *ir.PhysicalTraversal
	for _, operation := range relatedProjection.Expression.Subplan.Operations {
		if operation.Kind == ir.PhysicalTraversalOp {
			traversal = operation.Traversal
		}
	}
	if traversal == nil || traversal.Direction != ir.PhysicalInbound || traversal.EndpointField != "_to" || traversal.EndpointJoinField != "_from" {
		t.Fatalf("related route did not lower through the schema-derived Patient-to-Observation edge: %#v", traversal)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"IN @@root_collection", "._key == __loom_construction_input_2._key", "DOCUMENT(", "dataset_generation", "auth_resource_path", ".status"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered related stage omits %q: %s", expected, rendered.Query)
		}
	}
	if rendered.BindVars["related_1_hop_1_label"] != "subject_Patient" || rendered.BindVars["related_1_hop_1_target_type"] != "Observation" {
		t.Fatalf("rendered traversal binds do not match selected route: %#v", rendered.BindVars)
	}
}

func TestDescribeConstructionSourceStageForZeroColumnOutput(t *testing.T) {
	output := constructionTestOutput()
	output.Fields = nil
	output.Construction = nil
	compiled := compileDerivedTestOutput(t, output)

	descriptor, err := DescribeConstructionSourceStage(compiled.OutputSchema)
	if err != nil {
		t.Fatalf("describe compiler-resolved source stage: %v", err)
	}
	if descriptor.ID != recipe.ConstructionSourceProjectionID || descriptor.RowIdentityColumn == "" {
		t.Fatalf("source descriptor identity = %#v", descriptor)
	}
	publicColumns := 0
	for _, column := range descriptor.Columns {
		if !column.Internal {
			publicColumns++
		}
	}
	if publicColumns != 0 {
		t.Fatalf("zero-column source descriptor exposed %d public columns: %#v", publicColumns, descriptor.Columns)
	}
	wantUnsupported := map[recipe.ConstructionOperationKind]string{
		recipe.ConstructionPivotOp:   "INSUFFICIENT_SCALAR_COLUMNS",
		recipe.ConstructionDeriveOp:  "NO_NUMERIC_COLUMN",
		recipe.ConstructionFilterOp:  "NO_PUBLIC_COLUMNS",
		recipe.ConstructionUnpivotOp: "NO_COMPATIBLE_UNPIVOT_COLUMNS",
		recipe.ConstructionGroupOp:   "NO_PUBLIC_COLUMNS",
		recipe.ConstructionExpandOp:  "NO_ARRAY_COLUMNS",
	}
	var relatedSourceCapability *StageOperationCapability
	for _, capability := range descriptor.Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			continue
		}
		wantReason, exists := wantUnsupported[capability.Operation]
		if !exists || capability.Supported || capability.ReasonCode != wantReason {
			t.Fatalf("zero-column capability = %#v, want unsupported reason %q", capability, wantReason)
		}
		delete(wantUnsupported, capability.Operation)
	}
	if len(wantUnsupported) != 0 {
		t.Fatalf("source descriptor omitted compiler capabilities %v", wantUnsupported)
	}
	if relatedSourceCapability == nil || !relatedSourceCapability.Supported {
		t.Fatalf("root row identity should make related-source available: %#v", relatedSourceCapability)
	}
	if compiled.Plan.StageSequence != nil {
		t.Fatal("capability description introduced an executable construction stage sequence")
	}
}

func constructionTestOutput() recipe.Output {
	groupLabel, categoryLabel, amountLabel := "Group", "Category", "Amount"
	integerZero, integerOne := int64(0), int64(1)
	stringA, stringB := "a", "b"
	filterValue := int64(5)
	return recipe.Output{
		Name: "construction_test", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "group", ColumnID: "group_id", Label: groupLabel, Expr: recipe.Expression{Select: "gender"}},
			{Name: "category", ColumnID: "category_id", Label: categoryLabel, Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "amount", ColumnID: "amount_id", Label: amountLabel, Expr: recipe.Expression{Select: "multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: groupLabel},
				{ID: "category_id", Name: "category", Label: categoryLabel},
				{ID: "amount_id", Name: "amount", Label: amountLabel},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "pivot_values", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &integerZero}, OutputColumnID: "amount_a_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &integerOne}, OutputColumnID: "amount_b_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}},
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "derive_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_a_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &integerOne}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}, {ID: "total_id", Name: "total"}},
				},
				{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterEq,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &filterValue}},
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "amount_a_id", Name: "amount_a"}, {ID: "amount_b_id", Name: "amount_b"}, {ID: "total_id", Name: "total"}},
				},
				{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "unpivot_values", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "amount_a_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringA}},
							{ColumnID: "amount_b_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringB}},
						}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_id", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group", Label: groupLabel}, {ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "amount_id", Name: "amount"}},
				},
			},
		},
	}
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
