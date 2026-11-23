package lower

import (
	"fmt"
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
	if compiled.Stages[0].ID != recipe.ConstructionSourceProjectionID || len(compiled.Stages[0].Capabilities) != 12 {
		t.Fatalf("source stage descriptor lacks exact source capabilities: %#v", compiled.Stages[0])
	}
	var sourceCodedPivotCapability *StageOperationCapability
	var sourceRelatedExpandCapability *StageOperationCapability
	var sourceRelatedEligibilityCapability *StageOperationCapability
	var sourceRelatedFieldCapability *StageOperationCapability
	for _, capability := range compiled.Stages[0].Capabilities {
		if capability.Operation == recipe.ConstructionCodedPivotOp {
			sourceCodedPivotCapability = &capability
		}
		if capability.Operation == recipe.ConstructionRelatedExpandOp {
			sourceRelatedExpandCapability = &capability
		}
		if capability.Operation == recipe.ConstructionRelatedEligibilityOp {
			sourceRelatedEligibilityCapability = &capability
		}
		if capability.Operation == recipe.ConstructionRelatedFieldOp {
			sourceRelatedFieldCapability = &capability
		}
	}
	if sourceCodedPivotCapability == nil || !sourceCodedPivotCapability.Supported {
		t.Fatalf("source stage should support a coded Pivot with an authorized family: %#v", sourceCodedPivotCapability)
	}
	if sourceRelatedExpandCapability == nil || !sourceRelatedExpandCapability.Supported {
		t.Fatalf("source stage should retain its root key for related expansion: %#v", sourceRelatedExpandCapability)
	}
	if len(compiled.Stages[0].RelatedExpandAnchors) != 1 || compiled.Stages[0].RelatedExpandAnchors[0].Kind != "root" ||
		compiled.Stages[0].RelatedExpandAnchors[0].AnchorColumnID != "_key" {
		t.Fatalf("direct-root stage should expose only its scalar root anchor: %#v", compiled.Stages[0].RelatedExpandAnchors)
	}
	if sourceRelatedEligibilityCapability == nil || !sourceRelatedEligibilityCapability.Supported {
		t.Fatalf("source stage should retain its root key for related eligibility: %#v", sourceRelatedEligibilityCapability)
	}
	if sourceRelatedFieldCapability == nil || sourceRelatedFieldCapability.Supported || sourceRelatedFieldCapability.ReasonCode != "NO_ACTIVE_RELATED_RECORD" {
		t.Fatalf("source stage must not offer exact related fields without a terminal record: %#v", sourceRelatedFieldCapability)
	}
	var relatedSourceCapability *StageOperationCapability
	for _, capability := range compiled.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			break
		}
	}
	if relatedSourceCapability == nil || !relatedSourceCapability.Supported {
		t.Fatalf("Pivot stage should advertise compiler-proven root contributors for a later related source: %#v", relatedSourceCapability)
	}
	if compiled.Stages[1].Columns != nil {
		for _, column := range compiled.Stages[1].Columns {
			if column.Name == rootContributorSetColumn {
				t.Fatalf("unused future related source caused hidden contributor IDs to be retained: %#v", compiled.Stages[1].Columns)
			}
		}
	}
	var reshapedRelatedExpandCapability *StageOperationCapability
	var reshapedRelatedEligibilityCapability *StageOperationCapability
	for _, capability := range compiled.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedExpandOp {
			reshapedRelatedExpandCapability = &capability
		}
		if capability.Operation == recipe.ConstructionRelatedEligibilityOp {
			reshapedRelatedEligibilityCapability = &capability
		}
	}
	if reshapedRelatedExpandCapability == nil || !reshapedRelatedExpandCapability.Supported {
		t.Fatalf("Pivot stage should offer related expansion from compiler-proven root contributors: %#v", reshapedRelatedExpandCapability)
	}
	if reshapedRelatedEligibilityCapability == nil || !reshapedRelatedEligibilityCapability.Supported {
		t.Fatalf("Pivot stage should offer related eligibility from compiler-proven root contributors: %#v", reshapedRelatedEligibilityCapability)
	}
	if len(compiled.Stages[1].RelatedExpandAnchors) != 1 ||
		compiled.Stages[1].RelatedExpandAnchors[0].AnchorColumnID != rootContributorSetColumn ||
		compiled.Stages[1].RelatedExpandAnchors[0].Kind != "rootContributors" ||
		compiled.Stages[1].RelatedExpandAnchors[0].ResourceType != "Patient" {
		t.Fatalf("Pivot stage did not expose its compiler-proven root contributors: %#v", compiled.Stages[1].RelatedExpandAnchors)
	}

	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"COLLECT", " + ", "FILTER", "TABLE_UNPIVOT", "__loom_construction_stage_4"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered stage sequence is missing %q: %s", expected, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, rootContributorSetColumn) {
		t.Fatalf("unmodified Pivot pipeline materialized unused root contributor arrays:\n%s", rendered.Query)
	}
	if strings.Contains(rendered.Query, "LIMIT @") {
		t.Fatalf("lowering inserted a preview limit before the caller requested one: %s", rendered.Query)
	}
}

func TestConstructionUnpivotPreservesInternalSourceIdentity(t *testing.T) {
	output := constructionTestOutput()
	keyGroup, keyAmount := "category", "amount"
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
			ConstructionID: "unpivot_source_values",
			Inputs: []recipe.ConstructionUnpivotInput{
				{ColumnID: "category_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &keyGroup}},
				{ColumnID: "amount_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &keyAmount}},
			},
			KeyOutputColumnID: "measure_id", ValueOutputColumnID: "measure_value_id", NullRowPolicy: recipe.UnpivotNullDrop,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "measure_id", Name: "measure", Label: "Measure"},
			{ID: "measure_value_id", Name: "measure_value", Label: "Measure value"},
		},
	}}

	compiled := compileDerivedTestOutput(t, output)
	if len(compiled.Plan.StageSequence.Stages) != 1 {
		t.Fatalf("construction stages = %d, want one unpivot stage", len(compiled.Plan.StageSequence.Stages))
	}
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Unpivot == nil {
		t.Fatal("construction stage has no unpivot payload")
	}
	if !hasConstructionUnpivotOutput(stage.Unpivot.PreservedOutputs, "_key", "_key") {
		t.Fatalf("unpivot preserved outputs do not retain hidden source identity: %#v", stage.Unpivot.PreservedOutputs)
	}
	if !hasCompiledColumn(compiled.OutputSchema, "_key", true) {
		t.Fatalf("final construction schema dropped hidden source identity: %#v", compiled.OutputSchema)
	}
	var relatedSourceCapability *StageOperationCapability
	for _, capability := range compiled.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			break
		}
	}
	if relatedSourceCapability == nil || !relatedSourceCapability.Supported {
		t.Fatalf("UNPIVOT should expose its physically retained typed root key: %#v", relatedSourceCapability)
	}
}

func hasConstructionUnpivotOutput(outputs []ir.PhysicalUnpivotOutput, input, output string) bool {
	for _, candidate := range outputs {
		if candidate.InputColumn == input && candidate.OutputColumn == output {
			return true
		}
	}
	return false
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
	status := "registered"
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
					ContributorPolicy: "ALL_MATCHES",
					Predicate:         &recipe.ConstructionRelatedPredicate{CandidateID: "observation-status", Operator: recipe.FilterEquals, Value: &recipe.FilterValue{Kind: recipe.FilterString, String: &status}},
					Form:              "ALL", OutputColumnID: "observation-status",
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
	var contributorFilter *ir.PhysicalFilter
	for _, operation := range relatedProjection.Expression.Subplan.Operations {
		if operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Filter.Expression != nil {
			contributorFilter = operation.Filter
		}
	}
	if contributorFilter == nil || contributorFilter.Expression.Comparison == nil || contributorFilter.Expression.Comparison.Operator != string(recipe.FilterEquals) ||
		contributorFilter.Expression.Comparison.LeftExpression == nil || contributorFilter.Expression.Comparison.LeftExpression.Extract == nil ||
		contributorFilter.Expression.Comparison.LeftExpression.Extract.Selector.CanonicalPath() != "status" {
		t.Fatalf("related source predicate did not target the selected scalar field: %#v", contributorFilter)
	}
	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"IN @@root_collection", "._key == __loom_construction_input_2._key", "DOCUMENT(", "dataset_generation", "auth_resource_path", ".status", "== @related_1_contributor_value"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered related stage omits %q: %s", expected, rendered.Query)
		}
	}
	if rendered.BindVars["related_1_hop_1_label"] != "subject_Patient" || rendered.BindVars["related_1_hop_1_target_type"] != "Observation" {
		t.Fatalf("rendered traversal binds do not match selected route: %#v", rendered.BindVars)
	}
	if rendered.BindVars["@related_1_hop_1_edge_collection"] != "fhir_edge" {
		t.Fatalf("rendered route collection bind is missing or unprefixed: %#v", rendered.BindVars)
	}
	if rendered.BindVars["related_1_contributor_value"] != status {
		t.Fatalf("related contributor predicate bind = %#v, want %q", rendered.BindVars["related_1_contributor_value"], status)
	}
}

func TestCompileRelatedSourceOnePolicyReducesDistinctPresentValuesToNullableScalar(t *testing.T) {
	output := constructionTestOutput()
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "_key", ChoiceID: "choice-token", SourceOccurrenceID: "observation-node",
				Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
				Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
				ContributorPolicy: "ALL_MATCHES", Form: "ALL", RowValuePolicy: recipe.ConstructionRowValueOne, OutputColumnID: "observation-status",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
				{ID: "observation-status", Name: "observation_status", Label: "Observation status"},
			},
		}},
	}
	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.RelatedSource == nil || stage.RelatedSource.Form != "ALL" || stage.RelatedSource.RowValuePolicy != "ONE" || stage.RelatedSource.ContributorPolicy != "ALL_MATCHES" {
		t.Fatalf("operation-local reduction changed related-resource form or contributor policy: %#v", stage.RelatedSource)
	}
	column := stage.OutputColumns[3]
	if column.Kind != "string" || column.Cardinality != "optional_one" || !column.Nullable {
		t.Fatalf("ONE related output = %#v, want nullable scalar string", column)
	}
	var projected *ir.PhysicalProjection
	for index := range stage.OutputProjections {
		if stage.OutputProjections[index].Name == "observation_status" {
			projected = &stage.OutputProjections[index]
		}
	}
	if projected == nil || projected.Expression == nil || projected.Expression.Call == nil ||
		projected.Expression.Call.Name != "distinct_present_one" || projected.Expression.Call.TargetKind != "STRING" ||
		len(projected.Expression.Call.Args) != 1 || projected.Expression.Call.Args[0].Subplan == nil ||
		projected.Expression.Call.Args[0].Subplan.DistinctBy == nil {
		t.Fatalf("ONE expression did not retain the exact distinct related-source subplan: %#v", projected)
	}
	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES",
		"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH",
		"FILTER __loom_physical_construction_related_source_row_value_item != null",
		"LENGTH(__loom_physical_construction_related_source_row_value_values) == 0 ? null : FIRST(__loom_physical_construction_related_source_row_value_values)",
	} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("ONE related query omits %q: %s", expected, rendered.Query)
		}
	}
	foundType := false
	for key, value := range rendered.BindVars {
		if strings.Contains(key, "related_source_row_value_type") && value == "string" {
			foundType = true
		}
	}
	if !foundType {
		t.Fatalf("ONE related query has no typed scalar guard bind: %#v", rendered.BindVars)
	}
}

func TestCompileRelatedFieldCarriesExactTerminalIdentityThroughRowPreservingStages(t *testing.T) {
	for _, test := range []struct {
		name       string
		operations []string
	}{
		{name: "expand then repeated fields", operations: []string{"RELATED_EXPAND", "RELATED_FIELD", "RELATED_FIELD"}},
		{name: "expand then filter then field", operations: []string{"RELATED_EXPAND", "FILTER", "RELATED_FIELD"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := constructionTestOutput()
			output.Construction = relatedFieldTestConstruction(test.operations)
			compiled := compileDerivedTestOutput(t, output)
			sequence := compiled.Plan.StageSequence
			if sequence == nil || len(sequence.Stages) != len(test.operations) {
				t.Fatalf("stage sequence = %#v, want %d operations", sequence, len(test.operations))
			}
			terminalColumn := sequence.Stages[0].RelatedExpand.TerminalIdentityColumn
			activeIdentity := sequence.Stages[0].RowIdentityColumn
			for index, operation := range test.operations {
				stage := sequence.Stages[index]
				if string(stage.Kind) != operation {
					t.Fatalf("stage %d kind = %q, want %q", index, stage.Kind, operation)
				}
				if index == 0 {
					continue
				}
				if stage.RowIdentityColumn != activeIdentity {
					t.Fatalf("row-preserving stage %q changed exact row identity from %q to %q", stage.ID, activeIdentity, stage.RowIdentityColumn)
				}
				if _, found := physicalStageColumnsForTest(stage.InputColumns)[terminalColumn]; !found {
					t.Fatalf("stage %q input lost physical terminal identity %q: %#v", stage.ID, terminalColumn, stage.InputColumns)
				}
				if anchor, found := physicalStageColumnsForTest(stage.OutputColumns)[terminalColumn]; !found || !anchor.Internal || anchor.Identity {
					t.Fatalf("stage %q output lost hidden terminal identity %q: %#v", stage.ID, terminalColumn, anchor)
				}
				if !projectionPassesThroughHiddenColumn(stage.OutputProjections, terminalColumn) {
					t.Fatalf("stage %q does not physically project terminal identity %q: %#v", stage.ID, terminalColumn, stage.OutputProjections)
				}
				descriptor := compiled.Stages[index+1]
				if descriptor.ActiveRelatedRecord == nil || descriptor.ActiveRelatedRecord.TerminalIdentityColumn != terminalColumn ||
					descriptor.ActiveRelatedRecord.TargetNodeID != "observation-node" || descriptor.ActiveRelatedRecord.TargetResourceType != "Observation" {
					t.Fatalf("stage %q descriptor lost exact active terminal record: %#v", stage.ID, descriptor.ActiveRelatedRecord)
				}
				hasRoot, hasActive := false, false
				for _, anchor := range descriptor.RelatedExpandAnchors {
					hasRoot = hasRoot || (anchor.AnchorColumnID == "_key" && anchor.Kind == "root" && anchor.ResourceType == "Patient")
					hasActive = hasActive || (anchor.AnchorColumnID == terminalColumn && anchor.Kind == "activeRelatedRecord" && anchor.NodeID == "observation-node")
				}
				if !hasRoot || !hasActive {
					t.Fatalf("row-preserving stage %q lost a compiler-proven expansion anchor: %#v", stage.ID, descriptor.RelatedExpandAnchors)
				}
			}
			rendered, err := aql.Render(compiled.Plan, aql.Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{
				"related_field_document.project == @project",
				"related_field_document.dataset_generation == @dataset_generation",
				"related_field_document.resourceType == @",
				"related_field_document.auth_resource_path IN @auth_resource_paths",
				".payload[@",
			} {
				if !strings.Contains(rendered.Query, expected) {
					t.Fatalf("rendered query lacks scoped exact-field lookup %q: %s", expected, rendered.Query)
				}
			}
		})
	}
}

func TestFilterDoesNotDuplicateRootAnchorWhenItIsTheRowIdentity(t *testing.T) {
	output := constructionTestOutput()
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "filter_source", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "group_id", Operator: recipe.FilterExists,
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: "Group"},
				{ID: "category_id", Name: "category", Label: "Category"},
				{ID: "amount_id", Name: "amount", Label: "Amount"},
			},
		}},
	}
	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	keyColumns, keyProjections := 0, 0
	for _, column := range stage.OutputColumns {
		if column.Name == "_key" {
			keyColumns++
			if !column.Identity {
				t.Fatalf("source _key ceased to be the row identity through FILTER: %#v", column)
			}
		}
	}
	for _, projection := range stage.OutputProjections {
		if projection.Name == "_key" {
			keyProjections++
		}
	}
	if keyColumns != 1 || keyProjections != 1 {
		t.Fatalf("FILTER must carry the row-identity root key once: columns=%d projections=%d", keyColumns, keyProjections)
	}
}

func TestRelatedSourceCapabilityAfterRelatedExpandUsesRetainedRootAnchor(t *testing.T) {
	output := constructionTestOutput()
	construction := relatedFieldTestConstruction([]string{"RELATED_EXPAND", "FILTER"})
	filterStep := construction.Steps[len(construction.Steps)-1]
	outputs := append([]recipe.StageColumn(nil), filterStep.Outputs...)
	outputs = append(outputs, recipe.StageColumn{ID: "medication-status", Name: "medication_status", Type: "string"})
	construction.Steps = append(construction.Steps, recipe.ConstructionStep{
		ID:     "add_medication_status",
		Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: filterStep.ID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
			AnchorColumnID: constructionRowID, ChoiceID: "medication-status-choice", SourceOccurrenceID: "medication-node",
			Source: recipe.ConstructionRelatedFieldSource{
				CandidateID: "medication-status", NodeID: "medication-node", ResourceType: "MedicationAdministration",
				Path: "MedicationAdministration.status", Cardinality: "optional_one", LogicalType: "string",
			},
			Route: []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "patient-medication", FromNodeID: "patient-node", ToNodeID: "medication-node",
				FromResourceType: "Patient", ToResourceType: "MedicationAdministration", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "medication-status",
		}},
		Outputs: outputs,
	})
	output.Construction = construction

	compiled := compileDerivedTestOutput(t, output)
	if len(compiled.Stages) != 4 {
		t.Fatalf("compiled stage count = %d, want source, RelatedExpand, FILTER, RelatedSource", len(compiled.Stages))
	}
	for _, stageIndex := range []int{1, 2} {
		var relatedSourceCapability *StageOperationCapability
		for _, capability := range compiled.Stages[stageIndex].Capabilities {
			if capability.Operation == recipe.ConstructionRelatedSourceOp {
				relatedSourceCapability = &capability
				break
			}
		}
		if relatedSourceCapability == nil || !relatedSourceCapability.Supported {
			t.Fatalf("stage %q should expose related source through its retained typed root anchor: %#v", compiled.Stages[stageIndex].ID, relatedSourceCapability)
		}
	}
	filter := compiled.Plan.StageSequence.Stages[1]
	activeIdentity := compiled.Plan.StageSequence.Stages[0].RelatedExpand.TerminalIdentityColumn
	if filter.RowIdentityColumn != constructionRowID {
		t.Fatalf("FILTER row identity = %q, want exact generated RelatedExpand identity %q", filter.RowIdentityColumn, constructionRowID)
	}
	rootKey, found := physicalStageColumnsForTest(filter.OutputColumns)["_key"]
	if !found || !rootKey.Internal || rootKey.Identity || rootKey.RootContributorResourceType != "Patient" || rootKey.Cardinality != "required_one" {
		t.Fatalf("FILTER root contributor = %#v, want the retained typed Patient key", rootKey)
	}
	terminal, found := physicalStageColumnsForTest(filter.OutputColumns)[activeIdentity]
	if !found || !terminal.Internal || !terminal.Nullable || terminal.RelatedRecordAnchor == nil || terminal.RelatedRecordAnchor.ResourceType != "Observation" {
		t.Fatalf("FILTER active terminal identity = %#v, want nullable exact Observation identity", terminal)
	}
	relatedSource := compiled.Plan.StageSequence.Stages[2].RelatedSource
	if relatedSource == nil || relatedSource.AnchorColumnID != constructionRowID || relatedSource.RootContributorColumn != "_key" || relatedSource.RootResourceType != "Patient" {
		t.Fatalf("RELATED_SOURCE anchors = %#v, want current row identity plus typed Patient root contributor", relatedSource)
	}
	if _, err := aql.Render(compiled.Plan, aql.Options{}); err != nil {
		t.Fatalf("render RelatedExpand→FILTER→RELATED_SOURCE plan: %v", err)
	}
}

func TestRelatedSourceAfterExpandCarriesExactRootKey(t *testing.T) {
	const (
		patientIDColumn = "patient-id"
		givenValuesID   = "given-values"
		givenItemID     = "given-item"
		statusID        = "medication-status"
	)
	columns := []recipe.StageColumn{
		{ID: patientIDColumn, Name: "patient_id", Type: "string"},
		{ID: givenValuesID, Name: "given_values", Type: "string"},
	}
	expandedColumns := []recipe.StageColumn{
		columns[0], {ID: givenItemID, Name: "given", Type: "string"},
	}
	output := recipe.Output{
		Name: "patient_expanded_related_source", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: patientIDColumn, Expr: recipe.Expression{Select: "root.id"}},
			{Name: "given_values", ColumnID: givenValuesID, Expr: recipe.Expression{Select: "root.name[].given[]"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{
			{
				ID: "expand_given_values", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "expand_given_values", InputColumnID: givenValuesID, OutputColumnID: givenItemID,
					EmptyPolicy: recipe.ExpansionPreserveParent,
				}}, Outputs: expandedColumns,
			},
			{
				ID: "add_medication_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_given_values"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: constructionRowID, ChoiceID: "medication-status-choice", SourceOccurrenceID: "medication-node",
					Source: recipe.ConstructionRelatedFieldSource{
						CandidateID: "medication-status", NodeID: "medication-node", ResourceType: "MedicationAdministration",
						Path: "MedicationAdministration.status", Cardinality: "optional_one", LogicalType: "string",
					},
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-medication", FromNodeID: "patient-node", ToNodeID: "medication-node",
						FromResourceType: "Patient", ToResourceType: "MedicationAdministration", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}},
					ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: statusID,
				}}, Outputs: append(append([]recipe.StageColumn(nil), expandedColumns...), recipe.StageColumn{
					ID: statusID, Name: "medication_status", Type: "string",
				}),
			},
		}},
	}
	compiled := compileDerivedTestOutput(t, output)
	if len(compiled.Stages) != 3 {
		t.Fatalf("compiled stage count = %d, want source, EXPAND, RELATED_SOURCE", len(compiled.Stages))
	}
	var relatedSourceCapability *StageOperationCapability
	for _, capability := range compiled.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			break
		}
	}
	if relatedSourceCapability == nil || !relatedSourceCapability.Supported {
		t.Fatalf("EXPAND should retain a compiler-owned scalar root key for a later RELATED_SOURCE: %#v", relatedSourceCapability)
	}
	expand := compiled.Plan.StageSequence.Stages[0]
	rootKey, found := physicalStageColumnsForTest(expand.OutputColumns)["_key"]
	if !found || !rootKey.Internal || rootKey.Identity || rootKey.RootContributorResourceType != "Patient" || rootKey.Cardinality != "required_one" {
		t.Fatalf("EXPAND root contributor = %#v, want hidden required scalar Patient key", rootKey)
	}
	if projectionCount(expand.OutputProjections, "_key") != 1 {
		t.Fatalf("EXPAND should project the root key exactly once: %#v", expand.OutputProjections)
	}
	relatedSource := compiled.Plan.StageSequence.Stages[1].RelatedSource
	if relatedSource == nil || relatedSource.AnchorColumnID != constructionRowID || relatedSource.RootContributorColumn != "_key" {
		t.Fatalf("RELATED_SOURCE anchors after EXPAND = %#v, want current generated identity plus typed root key", relatedSource)
	}
	if _, err := aql.Render(compiled.Plan, aql.Options{}); err != nil {
		t.Fatalf("render EXPAND→RELATED_SOURCE plan: %v", err)
	}
}

func projectionCount(projections []ir.PhysicalProjection, name string) int {
	count := 0
	for _, projection := range projections {
		if projection.Name == name {
			count++
		}
	}
	return count
}

func TestGroupRootContributorSetStaysLazyUntilRelatedSourceIsAdded(t *testing.T) {
	sourceColumns := []recipe.StageColumn{
		{ID: "active-id", Name: "active", Type: "boolean"},
	}
	groupStep := recipe.ConstructionStep{
		ID: "group_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_by_active",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "active-id", OutputColumnID: "group-active-id"}},
			Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row-count-id"}},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "group-active-id", Name: "active", Type: "boolean"},
			{ID: "row-count-id", Name: "row_count", Type: "integer"},
		},
	}
	output := recipe.Output{
		Name: "grouped_patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields:       []recipe.Field{{Name: "active", ColumnID: "active-id", Expr: recipe.Expression{Select: "root.active"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{groupStep}},
	}
	base := compileDerivedTestOutput(t, output)
	group := base.Plan.StageSequence.Stages[0]
	if group.Group == nil || group.Group.RootContributorOutputColumn != "" {
		t.Fatalf("unmodified Group should not materialize an unused contributor set: %#v", group.Group)
	}
	var sourceCapability *StageOperationCapability
	for _, capability := range base.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			sourceCapability = &capability
			break
		}
	}
	if sourceCapability == nil || !sourceCapability.Supported {
		t.Fatalf("Group should advertise a future related source backed by its lazy compiler proof: %#v", sourceCapability)
	}
	baseQuery, err := aql.Render(base.Plan, aql.Options{})
	if err != nil {
		t.Fatalf("render unmodified Group plan: %v", err)
	}
	if strings.Contains(baseQuery.Query, rootContributorSetColumn) {
		t.Fatalf("unmodified Group query materialized unused contributor arrays:\n%s", baseQuery.Query)
	}

	candidate := output
	candidate.Construction = &recipe.Construction{
		Version: 1, SourceColumns: append([]recipe.StageColumn(nil), sourceColumns...),
		Steps: append([]recipe.ConstructionStep(nil), output.Construction.Steps...),
	}
	candidateSteps := append([]recipe.StageColumn(nil), groupStep.Outputs...)
	candidateSteps = append(candidateSteps, recipe.StageColumn{ID: "medication-status", Name: "medication_status", Type: "string"})
	candidate.Construction.Steps = append(candidate.Construction.Steps, recipe.ConstructionStep{
		ID: "add_medication_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: groupStep.ID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
			AnchorColumnID: constructionRowID, ChoiceID: "medication-status-choice", SourceOccurrenceID: "medication-node",
			Source: recipe.ConstructionRelatedFieldSource{
				CandidateID: "medication-status", NodeID: "medication-node", ResourceType: "MedicationAdministration",
				Path: "MedicationAdministration.status", Cardinality: "optional_one", LogicalType: "string",
			},
			Route: []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "patient-medication", FromNodeID: "patient-node", ToNodeID: "medication-node",
				FromResourceType: "Patient", ToResourceType: "MedicationAdministration", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "medication-status",
		}}, Outputs: candidateSteps,
	})
	extended := compileDerivedTestOutput(t, candidate)
	retainedGroup := extended.Plan.StageSequence.Stages[0].Group
	if retainedGroup == nil || retainedGroup.RootContributorOutputColumn != rootContributorSetColumn {
		t.Fatalf("Group did not materialize contributors for the appended source operation: %#v", retainedGroup)
	}
	relatedSource := extended.Plan.StageSequence.Stages[1].RelatedSource
	if relatedSource == nil || relatedSource.AnchorColumnID != constructionRowID || relatedSource.RootContributorColumn != rootContributorSetColumn {
		t.Fatalf("appended RELATED_SOURCE did not use exact row identity and compiler-owned contributor set: %#v", relatedSource)
	}
}

func relatedFieldTestConstruction(operations []string) *recipe.Construction {
	sourceColumns := []recipe.StageColumn{
		{ID: "group_id", Name: "group", Label: "Group"},
		{ID: "category_id", Name: "category", Label: "Category"},
		{ID: "amount_id", Name: "amount", Label: "Amount"},
	}
	steps := make([]recipe.ConstructionStep, 0, len(operations))
	priorID := ""
	columns := append([]recipe.StageColumn(nil), sourceColumns...)
	for index, operation := range operations {
		step := recipe.ConstructionStep{ID: fmt.Sprintf("related_step_%d", index+1)}
		if index == 0 {
			step.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}
		} else {
			step.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: priorID}}
		}
		switch operation {
		case "RELATED_EXPAND":
			step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "related-route-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent,
				RelatedRecordColumnID: "observation_id",
			}}
			columns = append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{
				ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true,
			})
		case "FILTER":
			step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "group_id", Operator: recipe.FilterExists,
			}}
		case "RELATED_FIELD":
			fieldID, fieldName, candidateID, path := "observation_status", "observation_status", "observation-status", "status"
			if len(steps) > 0 && steps[len(steps)-1].Operation.RelatedField != nil {
				fieldID, fieldName, candidateID, path = "observation_effective", "observation_effective", "observation-effective", "effectiveDateTime"
			}
			step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedFieldOp, RelatedField: &recipe.ConstructionRelatedField{
				ChoiceID: "related-field-choice-" + candidateID,
				Source: recipe.ConstructionRelatedFieldSource{
					CandidateID: candidateID, NodeID: "observation-node", ResourceType: "Observation", Path: path,
					Cardinality: "optional_one", LogicalType: "string",
				},
				OutputColumnID: fieldID,
			}}
			columns = append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{
				ID: fieldID, Name: fieldName, Label: fieldName, Type: "string", Nullable: true,
			})
		}
		step.Outputs = append([]recipe.StageColumn(nil), columns...)
		steps = append(steps, step)
		priorID = step.ID
	}
	return &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: steps}
}

func projectionPassesThroughHiddenColumn(projections []ir.PhysicalProjection, name string) bool {
	for _, projection := range projections {
		if projection.Name == name && projection.Hidden && len(projection.Value.Path) > 0 && projection.Value.Path[0] == name {
			return true
		}
	}
	return false
}

func physicalStageColumnsForTest(columns []ir.PhysicalStageColumn) map[string]ir.PhysicalStageColumn {
	indexed := make(map[string]ir.PhysicalStageColumn, len(columns))
	for _, column := range columns {
		indexed[column.ID] = column
	}
	return indexed
}

func TestDescribeConstructionSourceStageForZeroColumnOutput(t *testing.T) {
	output := constructionTestOutput()
	output.Fields = nil
	output.Construction = nil
	compiled := compileDerivedTestOutput(t, output)

	descriptor, err := DescribeConstructionSourceStage(compiled.OutputSchema, output.RootResourceType, &compiled.Plan)
	if err != nil {
		t.Fatalf("describe compiler-resolved source stage: %v", err)
	}
	if descriptor.ID != recipe.ConstructionSourceProjectionID || descriptor.RowIdentityColumn == "" ||
		!compiledStageCapabilitySupported(descriptor.Capabilities, recipe.ConstructionCodedPivotOp) {
		t.Fatalf("source descriptor identity = %#v", descriptor)
	}
	expandedPlan := compiled.Plan
	expandedPlan.Operations = append([]ir.PhysicalOperation(nil), compiled.Plan.Operations...)
	if len(expandedPlan.Operations) == 0 || expandedPlan.Operations[len(expandedPlan.Operations)-1].Kind != ir.PhysicalReturnOp {
		t.Fatalf("source plan has no terminal RETURN: %#v", expandedPlan.Operations)
	}
	returnIndex := len(expandedPlan.Operations) - 1
	expandedPlan.Operations = append(expandedPlan.Operations[:returnIndex], append(
		[]ir.PhysicalOperation{{Kind: ir.PhysicalUnnestOp}}, expandedPlan.Operations[returnIndex:]...)...)
	expanded, err := DescribeConstructionSourceStage(compiled.OutputSchema, output.RootResourceType, &expandedPlan)
	if err != nil {
		t.Fatalf("describe expanded source stage: %v", err)
	}
	if compiledStageCapabilitySupported(expanded.Capabilities, recipe.ConstructionCodedPivotOp) {
		t.Fatalf("expanded source stage advertises direct-root coded operations: %#v", expanded)
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
		recipe.ConstructionPivotOp:        "INSUFFICIENT_SCALAR_COLUMNS",
		recipe.ConstructionDeriveOp:       "NO_NUMERIC_COLUMN",
		recipe.ConstructionFilterOp:       "NO_PUBLIC_COLUMNS",
		recipe.ConstructionUnpivotOp:      "NO_COMPATIBLE_UNPIVOT_COLUMNS",
		recipe.ConstructionGroupOp:        "NO_PUBLIC_COLUMNS",
		recipe.ConstructionCodedGroupOp:   "NO_PUBLIC_SOURCE_COLUMNS",
		recipe.ConstructionExpandOp:       "NO_ARRAY_COLUMNS",
		recipe.ConstructionRelatedFieldOp: "NO_ACTIVE_RELATED_RECORD",
	}
	var relatedSourceCapability *StageOperationCapability
	var relatedExpandCapability *StageOperationCapability
	var relatedEligibilityCapability *StageOperationCapability
	var relatedFieldCapability *StageOperationCapability
	for _, capability := range descriptor.Capabilities {
		if capability.Operation == recipe.ConstructionCodedPivotOp {
			if !capability.Supported || capability.ReasonCode != "" {
				t.Fatalf("zero-column direct root stage should support coded Pivot: %#v", capability)
			}
			continue
		}
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			continue
		}
		if capability.Operation == recipe.ConstructionRelatedExpandOp {
			relatedExpandCapability = &capability
			continue
		}
		if capability.Operation == recipe.ConstructionRelatedEligibilityOp {
			relatedEligibilityCapability = &capability
			continue
		}
		if capability.Operation == recipe.ConstructionRelatedFieldOp {
			relatedFieldCapability = &capability
			wantReason, exists := wantUnsupported[capability.Operation]
			if !exists || capability.Supported || capability.ReasonCode != wantReason {
				t.Fatalf("zero-column capability = %#v, want unsupported reason %q", capability, wantReason)
			}
			delete(wantUnsupported, capability.Operation)
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
	if relatedExpandCapability == nil || !relatedExpandCapability.Supported {
		t.Fatalf("zero-column source retains its root _key and should advertise related expansion: %#v", relatedExpandCapability)
	}
	if relatedEligibilityCapability == nil || !relatedEligibilityCapability.Supported {
		t.Fatalf("zero-column source retains its root _key and should advertise related eligibility: %#v", relatedEligibilityCapability)
	}
	if relatedFieldCapability == nil || relatedFieldCapability.Supported || relatedFieldCapability.ReasonCode != "NO_ACTIVE_RELATED_RECORD" {
		t.Fatalf("zero-column source must not offer exact related fields without a terminal record: %#v", relatedFieldCapability)
	}
	if compiled.Plan.StageSequence != nil {
		t.Fatal("capability description introduced an executable construction stage sequence")
	}
}

func compiledStageCapabilitySupported(capabilities []StageOperationCapability, operation recipe.ConstructionOperationKind) bool {
	for _, capability := range capabilities {
		if capability.Operation == operation {
			return capability.Supported
		}
	}
	return false
}

func TestCompileRelatedSourceMarksNonObservationListsWithCompilerSemanticPath(t *testing.T) {
	output := constructionTestOutput()
	output.Fields = []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "id"}}}
	output.Construction = &recipe.Construction{
		Version:       1,
		SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
		Steps: []recipe.ConstructionStep{{
			ID: "add_medication_route", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "_key", ChoiceID: "medication-route-choice", SourceOccurrenceID: "medication-route",
				Source: recipe.ConstructionRelatedFieldSource{
					CandidateID: "medication-route-field", NodeID: "medication-route", ResourceType: "MedicationAdministration",
					Path: "MedicationAdministration.status", Cardinality: "optional_one", LogicalType: "string",
				},
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-medication-administration", FromNodeID: "patient", ToNodeID: "medication-route",
					FromResourceType: "Patient", ToResourceType: "MedicationAdministration", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "medication-route-id",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "patient_id", Name: "patient_id"},
				{ID: "medication-route-id", Name: "medication_route"},
			},
		}},
	}

	compiled := compileDerivedTestOutput(t, output)
	for _, column := range compiled.OutputSchema {
		if column.Name != "medication_route" {
			continue
		}
		if column.SemanticPath != "related_source:medication-route.status" || column.Cardinality != "many" {
			t.Fatalf("compiled related source metadata = %#v", column)
		}
		return
	}
	t.Fatal("compiled schema omitted the MedicationAdministration related-source list")
}

func intPointer(value int) *int { return &value }

func TestRelatedSourceRebindsRootAnchorAfterSourceRowExpansion(t *testing.T) {
	output := constructionTestOutput()
	output.RootOccurrenceID = "patient-root"
	output.RowGrain = "expanded"
	output.Expand = &recipe.Expansion{
		OwnerOccurrenceID: "patient-root", From: recipe.Expression{Select: "root.identifier[]"},
		As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent,
	}
	output.Identity = &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}}
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group"},
			{ID: "category_id", Name: "category"},
			{ID: "amount_id", Name: "amount"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "related_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "_key", ChoiceID: "patient-observation-choice", SourceOccurrenceID: "observation-node",
				Source: recipe.ConstructionRelatedFieldSource{
					CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
					Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
				},
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "observation-status",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group"},
				{ID: "category_id", Name: "category"},
				{ID: "amount_id", Name: "amount"},
				{ID: "observation-status", Name: "observation_status"},
			},
		}},
	}

	compiled := compileDerivedTestOutput(t, output)
	if got := compiled.Plan.StageSequence.SourceRowIdentity; got != "__loom_expansion_identity" {
		t.Fatalf("source row identity = %q, want expanded-row identity", got)
	}
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Kind != ir.PhysicalStageRelatedSourceOp || stage.RowIdentityColumn != "__loom_expansion_identity" {
		t.Fatalf("RELATED_SOURCE output identity = %q, want one row per source expansion", stage.RowIdentityColumn)
	}
	if stage.RelatedSource == nil || stage.RelatedSource.AnchorColumnID != "__loom_expansion_identity" ||
		stage.RelatedSource.RootContributorColumn != "_key" || stage.RelatedSource.RootResourceType != "Patient" {
		t.Fatalf("RELATED_SOURCE lost its expanded-row identity or Patient root correlation: %#v", stage.RelatedSource)
	}
	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatalf("render expanded RELATED_SOURCE plan: %v", err)
	}
	if !strings.Contains(rendered.Query, "__loom_expansion_identity") || !strings.Contains(rendered.Query, "_key") {
		t.Fatalf("rendered query lost expanded row identity or root contributor correlation:\n%s", rendered.Query)
	}
}

func TestCompileRelatedExpandChainFromIdentityOnlySource(t *testing.T) {
	output := constructionTestOutput()
	output.Fields = nil
	_, activeObservationIdentity := relatedExpandIdentityColumnNames("expand_observations")
	output.Construction = &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{
		{
			ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation_id",
			}},
			Outputs: []recipe.StageColumn{{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true}},
		},
		{
			ID: "expand_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: activeObservationIdentity, ChoiceID: "observation-specimen", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node",
					FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen",
					StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "specimen_id",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
				{ID: "specimen_id", Name: "specimen_id", Label: "Specimen ID", Type: "string", Nullable: true},
			},
		},
	}}

	compiled, err := compileDerivedTestBundle(t, output)
	if err != nil {
		t.Fatalf("compile identity-only RELATED_EXPAND chain: %v", err)
	}
	sequence := compiled.Plan.StageSequence
	if sequence == nil || sequence.SourceRowIdentity != "_key" || len(sequence.Stages) != 2 {
		t.Fatalf("identity-only source sequence = %#v, want root _key and two expansion stages", sequence)
	}
	if len(publicCompiledSchema(compiled.Stages[0].Columns)) != 0 {
		t.Fatalf("source projection should have no visible columns: %#v", compiled.Stages[0].Columns)
	}
	rootKey, found := physicalStageColumnsForTest(sequence.Stages[0].InputColumns)["_key"]
	if !found || !rootKey.Internal || !rootKey.Identity || rootKey.Kind != "string" || rootKey.Cardinality != "required_one" {
		t.Fatalf("first expansion did not receive the exact hidden root identity: %#v", sequence.Stages[0].InputColumns)
	}
	first, second := sequence.Stages[0], sequence.Stages[1]
	if first.RelatedExpand == nil || first.RelatedExpand.AnchorKind != "root" || first.RelatedExpand.AnchorColumnID != "_key" ||
		first.RelatedExpand.ParentIdentityColumn != "_key" || first.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		t.Fatalf("first expansion lost its root identity or empty policy: %#v", first.RelatedExpand)
	}
	if second.RelatedExpand == nil || second.RelatedExpand.AnchorKind != "activeRelatedRecord" ||
		second.RelatedExpand.AnchorColumnID != first.RelatedExpand.TerminalIdentityColumn ||
		second.RelatedExpand.AnchorNodeID != "observation-node" || second.RelatedExpand.AnchorResourceType != "Observation" ||
		second.RelatedExpand.ParentIdentityColumn != first.RowIdentityColumn ||
		second.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		t.Fatalf("onward expansion lost its active record identity or parent policy: %#v", second.RelatedExpand)
	}
	if got := publicCompiledSchema(compiled.OutputSchema); len(got) != 2 || got[0].ID != "observation_id" || got[1].ID != "specimen_id" {
		t.Fatalf("identity-only chain public output = %#v, want only its two related IDs", got)
	}
	if !hasCompiledColumn(compiled.OutputSchema, "_key", true) {
		t.Fatalf("related expansion chain dropped the original hidden root key: %#v", compiled.OutputSchema)
	}
	if compiled.Plan.BindVars["project"] != "project" || compiled.Plan.BindVars["dataset_generation"] != "generation" {
		t.Fatalf("related expansion changed project or generation bindings: %#v", compiled.Plan.BindVars)
	}
	for _, stage := range []ir.PhysicalConstructionStage{first, second} {
		projectScope, generationScope, authScope := false, false, false
		for _, operation := range stage.RelatedExpand.RelatedRecords.Operations {
			if operation.Filter != nil && operation.Filter.Predicate.Right != nil {
				predicate := operation.Filter.Predicate
				projectScope = projectScope || len(predicate.Left.Path) == 1 && predicate.Left.Path[0] == "project" && predicate.Right.BindKey == "project"
				generationScope = generationScope || len(predicate.Left.Path) == 1 && predicate.Left.Path[0] == "dataset_generation" && predicate.Right.BindKey == "dataset_generation"
			}
			if operation.DerivedLet != nil && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
				authScope = true
			}
		}
		if !projectScope || !generationScope || !authScope {
			t.Fatalf("%s route lost project, generation, or authorization scope: project=%t generation=%t auth=%t", stage.ID, projectScope, generationScope, authScope)
		}
	}

	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []ir.PhysicalConstructionStage{first, second} {
		if !strings.Contains(rendered.Query, "LENGTH("+stage.RelatedExpand.RelatedRecordsVariable+") == 0 ? [null]") {
			t.Errorf("rendered %s expansion does not preserve empty parents: %s", stage.ID, rendered.Query)
		}
	}
}

func hasCompiledColumn(columns []CompiledOutputColumn, name string, internal bool) bool {
	for _, column := range columns {
		if column.Name == name && column.Internal == internal {
			return true
		}
	}
	return false
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
