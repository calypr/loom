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
	if compiled.Stages[0].ID != recipe.ConstructionSourceProjectionID || len(compiled.Stages[0].Capabilities) != 9 {
		t.Fatalf("source stage descriptor lacks exact source capabilities: %#v", compiled.Stages[0])
	}
	var sourceRelatedExpandCapability *StageOperationCapability
	var sourceRelatedFieldCapability *StageOperationCapability
	for _, capability := range compiled.Stages[0].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedExpandOp {
			sourceRelatedExpandCapability = &capability
		}
		if capability.Operation == recipe.ConstructionRelatedFieldOp {
			sourceRelatedFieldCapability = &capability
		}
	}
	if sourceRelatedExpandCapability == nil || !sourceRelatedExpandCapability.Supported {
		t.Fatalf("source stage should retain its root key for related expansion: %#v", sourceRelatedExpandCapability)
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
	if relatedSourceCapability == nil || relatedSourceCapability.Supported || relatedSourceCapability.ReasonCode != "NO_SOURCE_ROW_ANCHOR" {
		t.Fatalf("related source must be unavailable after a reshaped stage loses root identity: %#v", relatedSourceCapability)
	}
	var reshapedRelatedExpandCapability *StageOperationCapability
	for _, capability := range compiled.Stages[1].Capabilities {
		if capability.Operation == recipe.ConstructionRelatedExpandOp {
			reshapedRelatedExpandCapability = &capability
		}
	}
	if reshapedRelatedExpandCapability == nil || reshapedRelatedExpandCapability.Supported || reshapedRelatedExpandCapability.ReasonCode != "NO_SOURCE_ROW_ANCHOR" {
		t.Fatalf("reshaped stage must not advertise related expansion without root key: %#v", reshapedRelatedExpandCapability)
	}
	if len(compiled.Stages[1].RelatedExpandAnchors) != 0 {
		t.Fatalf("reshape must not fabricate related-resource anchors: %#v", compiled.Stages[1].RelatedExpandAnchors)
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
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
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
			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
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

	descriptor, err := DescribeConstructionSourceStage(compiled.OutputSchema, output.RootResourceType)
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
		recipe.ConstructionPivotOp:        "INSUFFICIENT_SCALAR_COLUMNS",
		recipe.ConstructionDeriveOp:       "NO_NUMERIC_COLUMN",
		recipe.ConstructionFilterOp:       "NO_PUBLIC_COLUMNS",
		recipe.ConstructionUnpivotOp:      "NO_COMPATIBLE_UNPIVOT_COLUMNS",
		recipe.ConstructionGroupOp:        "NO_PUBLIC_COLUMNS",
		recipe.ConstructionExpandOp:       "NO_ARRAY_COLUMNS",
		recipe.ConstructionRelatedFieldOp: "NO_ACTIVE_RELATED_RECORD",
	}
	var relatedSourceCapability *StageOperationCapability
	var relatedExpandCapability *StageOperationCapability
	var relatedFieldCapability *StageOperationCapability
	for _, capability := range descriptor.Capabilities {
		if capability.Operation == recipe.ConstructionRelatedSourceOp {
			relatedSourceCapability = &capability
			continue
		}
		if capability.Operation == recipe.ConstructionRelatedExpandOp {
			relatedExpandCapability = &capability
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
	if relatedFieldCapability == nil || relatedFieldCapability.Supported || relatedFieldCapability.ReasonCode != "NO_ACTIVE_RELATED_RECORD" {
		t.Fatalf("zero-column source must not offer exact related fields without a terminal record: %#v", relatedFieldCapability)
	}
	if compiled.Plan.StageSequence != nil {
		t.Fatal("capability description introduced an executable construction stage sequence")
	}
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

func TestCompileRelatedExpandSupportsScalarContributorPredicate(t *testing.T) {
	output := constructionTestOutput()
	specimenID := "specimen-123"
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "patient-observations", TargetNodeID: "observation-node", TargetResourceType: "Observation",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES",
				ContributorPredicate: &recipe.ConstructionRelatedPredicate{
					CandidateID: "observation-id", Operator: recipe.FilterEquals,
					Value: &recipe.FilterValue{Kind: recipe.FilterString, String: &specimenID},
				},
				ContributorSource: &recipe.ConstructionRelatedFieldSource{
					CandidateID: "observation-id", NodeID: "observation-node", ResourceType: "Observation",
					Path: "Observation.id", Cardinality: "required_one", LogicalType: "string",
				},
				ContributorChoiceID: "observation-id-choice",
				EmptyPolicy:         recipe.ExpansionExclude, RelatedRecordColumnID: "observation_id",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: "Group"},
				{ID: "category_id", Name: "category", Label: "Category"},
				{ID: "amount_id", Name: "amount", Label: "Amount"},
				{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string"},
			},
		}},
	}

	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".id", "== @related_expand_contributor_value"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered related expansion omitted %q: %s", expected, rendered.Query)
		}
	}
	if rendered.BindVars["related_expand_contributor_value"] != specimenID {
		t.Fatalf("related expansion contributor bind = %#v, want %q", rendered.BindVars["related_expand_contributor_value"], specimenID)
	}
}

func TestCompileRelatedExpandUsesDistinctTerminalIdentityAndExplicitEmptyPolicy(t *testing.T) {
	for _, policy := range []recipe.ExpansionEmptyPolicy{
		recipe.ExpansionError,
		recipe.ExpansionExclude,
		recipe.ExpansionPreserveParent,
	} {
		t.Run(string(policy), func(t *testing.T) {
			output := constructionTestOutput()
			output.Construction = &recipe.Construction{
				Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"},
					{ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"},
				},
				Steps: []recipe.ConstructionStep{{
					ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}},
						ContributorPolicy: "ALL_MATCHES", EmptyPolicy: policy, RelatedRecordColumnID: "observation_id",
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_id", Name: "group", Label: "Group"},
						{ID: "category_id", Name: "category", Label: "Category"},
						{ID: "amount_id", Name: "amount", Label: "Amount"},
						{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: policy == recipe.ExpansionPreserveParent},
					},
				}},
			}

			compiled := compileDerivedTestOutput(t, output)
			if len(compiled.Plan.StageSequence.Stages) != 1 {
				t.Fatalf("physical related expansion stage count = %d", len(compiled.Plan.StageSequence.Stages))
			}
			stage := compiled.Plan.StageSequence.Stages[0]
			if stage.Kind != ir.PhysicalStageRelatedExpandOp || stage.RelatedExpand == nil || !stage.RelatedExpand.RelatedRecords.Unique {
				t.Fatalf("related expansion lowering did not deduplicate terminal resources: %#v", stage)
			}
			parentIdentityColumn := stage.RelatedExpand.ParentIdentityColumnID
			terminalIdentityColumn := stage.RelatedExpand.TerminalIdentityColumn
			if stage.RelatedExpand.Route[0].EdgeID != "patient-observation" || stage.RelatedExpand.TargetResourceType != "Observation" {
				t.Fatalf("compiled related route metadata = %#v", stage.RelatedExpand)
			}
			if !hasCompiledColumn(compiled.OutputSchema, "_key", true) ||
				!hasCompiledColumn(compiled.OutputSchema, parentIdentityColumn, true) ||
				!hasCompiledColumn(compiled.OutputSchema, terminalIdentityColumn, true) {
				t.Fatalf("related expansion lost separate root, parent, or terminal identity: %#v", compiled.OutputSchema)
			}
			if compiled.Stages[1].RelatedExpand == nil || compiled.Stages[1].RelatedExpand.TargetNodeID != "observation-node" ||
				compiled.Stages[1].RelatedExpand.ParentIdentityColumn != "_key" || compiled.Stages[1].RelatedExpand.TerminalIdentityColumn != terminalIdentityColumn {
				t.Fatalf("related expansion stage descriptor lacks downstream identity contract: %#v", compiled.Stages[1])
			}

			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			foundTraversal := false
			for _, operation := range stage.RelatedExpand.RelatedRecords.Operations {
				if operation.Traversal == nil {
					continue
				}
				foundTraversal = true
				if rendered.BindVars["@"+operation.Traversal.EdgeCollectionBindKey] != "fhir_edge" {
					t.Fatalf("related expansion edge collection bind = %#v", rendered.BindVars["@"+operation.Traversal.EdgeCollectionBindKey])
				}
			}
			if !foundTraversal {
				t.Fatal("related expansion did not retain its graph traversal")
			}
			recordsVariable := stage.RelatedExpand.RelatedRecordsVariable
			for _, expected := range []string{"SORTED_UNIQUE", "terminal_id", "resource_id", parentIdentityColumn, terminalIdentityColumn, `TO_STRING([["input"`} {
				if !strings.Contains(rendered.Query, expected) {
					t.Fatalf("rendered related expansion omitted %q: %s", expected, rendered.Query)
				}
			}
			if !strings.Contains(rendered.Query, ".id") || strings.Contains(rendered.Query, "related_key") {
				t.Fatalf("public related ID must be the required FHIR document id, not Arango _key: %s", rendered.Query)
			}
			switch policy {
			case recipe.ExpansionError:
				if !strings.Contains(rendered.Query, "FILTER ASSERT(LENGTH("+recordsVariable+") > 0") {
					t.Fatalf("ERROR policy did not reject a missing related resource: %s", rendered.Query)
				}
			case recipe.ExpansionExclude:
				if !strings.Contains(rendered.Query, "LENGTH("+recordsVariable+") == 0 ? []") {
					t.Fatalf("EXCLUDE policy did not remove empty parents: %s", rendered.Query)
				}
			case recipe.ExpansionPreserveParent:
				if !strings.Contains(rendered.Query, "LENGTH("+recordsVariable+") == 0 ? [null]") {
					t.Fatalf("PRESERVE_PARENT policy did not emit an empty related row: %s", rendered.Query)
				}
			}
		})
	}
}

func TestOnwardRelatedExpandLooksUpActiveIdentityAndPreservesNullParentRows(t *testing.T) {
	output := constructionTestOutput()
	_, observationIdentity := relatedExpandIdentityColumnNames("expand_observations")
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{
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
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"}, {ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"}, {ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
				},
			},
			{
				ID: "expand_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: observationIdentity, ChoiceID: "observation-specimen", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node",
						FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen",
						StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
					}},
					ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "specimen_id",
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"}, {ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"},
					{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
					{ID: "specimen_id", Name: "specimen_id", Label: "Specimen ID", Type: "string", Nullable: true},
				},
			},
		},
	}

	compiled := compileDerivedTestOutput(t, output)
	if len(compiled.Plan.StageSequence.Stages) != 2 {
		t.Fatalf("onward related expansion stages = %d, want 2", len(compiled.Plan.StageSequence.Stages))
	}
	first, second := compiled.Plan.StageSequence.Stages[0], compiled.Plan.StageSequence.Stages[1]
	if second.RelatedExpand == nil || second.RelatedExpand.AnchorKind != "activeRelatedRecord" ||
		second.RelatedExpand.AnchorNodeID != "observation-node" || second.RelatedExpand.AnchorResourceType != "Observation" ||
		second.RelatedExpand.AnchorColumnID != observationIdentity {
		t.Fatalf("onward expansion did not retain its exact active source identity: %#v", second.RelatedExpand)
	}
	if first.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent || second.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		t.Fatalf("both expansions must preserve parents with missing related records: first=%q second=%q", first.RelatedExpand.EmptyPolicy, second.RelatedExpand.EmptyPolicy)
	}
	if !hasCompiledColumn(compiled.OutputSchema, "_key", true) {
		t.Fatalf("onward expansion dropped the original root key needed for sibling routes: %#v", compiled.OutputSchema)
	}
	var activeLookup *ir.PhysicalDocumentLookup
	for _, operation := range second.RelatedExpand.RelatedRecords.Operations {
		if operation.Kind == ir.PhysicalDocumentLookupOp {
			activeLookup = operation.DocumentLookup
			break
		}
	}
	if activeLookup == nil || activeLookup.ExactID.Path[0] != observationIdentity || compiled.Plan.BindVars[activeLookup.CollectionBindKey] != "Observation" {
		t.Fatalf("onward expansion must point-lookup the selected Observation identity: lookup=%#v binds=%#v", activeLookup, compiled.Plan.BindVars)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	parseIndex := strings.Index(query, "PARSE_IDENTIFIER(")
	if parseIndex < 0 {
		t.Fatalf("active source lookup did not parse the exact FHIR id: %s", query)
	}
	guardIndex := strings.LastIndex(query[:parseIndex], "FILTER ")
	if guardIndex < 0 || !strings.Contains(query[guardIndex:parseIndex], " != null") {
		t.Fatalf("null-preserved parent identity must be filtered before PARSE_IDENTIFIER: %s", query)
	}
	for _, expected := range []string{
		activeLookup.Variable + ".project == @project",
		activeLookup.Variable + ".dataset_generation == @dataset_generation",
		activeLookup.Variable + ".auth_resource_path IN @auth_resource_paths",
		"LENGTH(" + second.RelatedExpand.RelatedRecordsVariable + ") == 0 ? [null]",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("onward expansion query omitted %q: %s", expected, query)
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
