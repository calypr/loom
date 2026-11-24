package construction

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
)

func testConstructionStageDescriptors(workspace ws.Workspace, outputID string) []receiptmodel.ReceiptConstructionStage {
	index := constructionDocumentIndex(workspace, outputID)
	if index < 0 {
		return nil
	}
	document := workspace.Documents[index]
	columns := make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, receiptmodel.ReceiptConstructionStageColumn{
			ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType, Cardinality: expression.OptionalOne,
		})
	}
	allChoices := func() []receiptmodel.ReceiptConstructionOperationChoice {
		return []receiptmodel.ReceiptConstructionOperationChoice{
			{Kind: "PIVOT", Supported: true}, {Kind: "DERIVE", Supported: true},
			{Kind: "FILTER", Supported: true}, {Kind: "UNPIVOT", Supported: true},
			{Kind: "RELATED_EXPAND", Supported: true},
		}
	}
	rootAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{
		AnchorColumnID: "_key", Kind: "root", ResourceType: document.RootResourceType,
		Label: "Original " + document.RootResourceType,
	}
	stages := []receiptmodel.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: allChoices(),
		RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor},
	}}
	var activeAnchor *receiptmodel.ReceiptConstructionActiveRelatedRecord
	if document.Construction == nil {
		return stages
	}
	for _, step := range document.Construction.Steps {
		stageColumns := make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(step.Outputs))
		for _, column := range step.Outputs {
			stageColumns = append(stageColumns, receiptmodel.ReceiptConstructionStageColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type, Cardinality: expression.OptionalOne,
			})
		}
		inputStageID := recipe.ConstructionSourceProjectionID
		if len(stages) > 1 {
			inputStageID = stages[len(stages)-1].ID
		}
		stage := receiptmodel.ReceiptConstructionStage{
			ID: step.ID, InputStageID: inputStageID, Operation: string(step.Operation.Kind),
			Columns: stageColumns, Capabilities: allChoices(),
		}
		if related := step.Operation.RelatedExpand; related != nil {
			route := make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route))
			for _, hop := range related.Route {
				route = append(route, recipe.ConstructionRelatedRouteStep{
					EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
					FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
					Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
				})
			}
			stage.RelatedExpand = &receiptmodel.ReceiptConstructionRelatedExpand{
				AnchorColumnID: related.AnchorColumnID, AnchorColumn: related.AnchorColumnID,
				AnchorKind: "root", AnchorNodeID: related.Route[0].FromNodeID,
				AnchorResourceType:     related.Route[0].FromResourceType,
				RelatedRecordColumnID:  related.RelatedRecordColumnID,
				ParentIdentityColumnID: "__test_parent_identity", ParentIdentityColumn: "__test_parent_identity",
				TerminalIdentityColumn: "__test_terminal_identity", TargetNodeID: related.TargetNodeID,
				TargetResourceType: related.TargetResourceType, Route: route,
			}
			activeAnchor = &receiptmodel.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
				TerminalIdentityColumn: "__test_terminal_identity",
			}
		} else if step.Operation.Kind != ws.ConstructionOperationFilter &&
			step.Operation.Kind != ws.ConstructionOperationDerive &&
			step.Operation.Kind != ws.ConstructionOperationRelatedSource &&
			step.Operation.Kind != ws.ConstructionOperationRelatedField {
			activeAnchor = nil
		}
		stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor}
		if activeAnchor != nil {
			stage.ActiveRelatedRecord = activeAnchor
			stage.RelatedExpandAnchors = append(stage.RelatedExpandAnchors, receiptmodel.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: activeAnchor.TerminalIdentityColumn, Kind: "activeRelatedRecord",
				NodeID: activeAnchor.TargetNodeID, ResourceType: activeAnchor.TargetResourceType,
				Label: "Current related " + activeAnchor.TargetResourceType,
			})
		}
		stages = append(stages, stage)
	}
	return stages
}

func TestConstructionRemovalCascadeRemovesOwnerOfDependentRelatedField(t *testing.T) {
	workspace, construction := seedRelatedExpandRemovalConstruction(t)
	document := workspace.Documents[0]
	construction.Steps = construction.Steps[:1]
	expansion := construction.Steps[0]
	statusColumnID := "observation_status"
	statusSource := ws.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-status", NodeID: "observation-node",
		ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
	}
	relatedFieldOutputs := append(append([]ws.StageColumn(nil), expansion.Outputs...), ws.StageColumn{
		ID: statusColumnID, Name: statusColumnID, Label: "Observation status", Type: "string", Nullable: true,
	})
	relatedField := ws.ConstructionStep{
		ID: "related_status_input", OwnerStepID: "group_status",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: expansion.ID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
			ChoiceID: "saved-status-choice", Source: statusSource, OutputColumnID: statusColumnID,
		}},
		Outputs: relatedFieldOutputs,
	}
	patientIDColumnID := document.Columns[0].ColumnID
	groupOutputs := []ws.StageColumn{
		{ID: patientIDColumnID, Name: document.Columns[0].Column, Label: document.Columns[0].Label, Type: document.Columns[0].LogicalType},
		{ID: "status_group", Name: "status_group", Label: "Observation status", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"},
	}
	group := ws.ConstructionStep{
		ID: "group_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: relatedField.ID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group_status", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys: []ws.ConstructionGroupKey{
				{InputColumnID: patientIDColumnID, OutputColumnID: patientIDColumnID},
				{InputColumnID: statusColumnID, OutputColumnID: "status_group"},
			},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: groupOutputs,
	}
	filter := ws.ConstructionStep{
		ID: "keep_root_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: group.ID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: patientIDColumnID, Operator: ws.ConstructionFilterExists,
		}},
		Outputs: append([]ws.StageColumn(nil), groupOutputs...),
	}
	construction.Steps = append(construction.Steps, relatedField, group, filter)
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate owned related-field construction: %v", err)
	}
	baseStages := relatedExpandRemovalTestStages(t, workspace, "patients")
	removedDocument, impact, err := analyzeConstructionCandidateWithCascade(document, constructionWithoutStep(construction, expansion.ID), "", []string{expansion.ID}, baseStages)
	if err != nil {
		t.Fatalf("cascade owner of removed related-field source: %v", err)
	}
	wantRemoved := []string{expansion.ID, relatedField.ID, group.ID}
	if !equalStringSlices(impact.RemovedStepIDs, wantRemoved) {
		t.Fatalf("removed steps = %#v, want expansion, its owned field input, and owner %#v", impact.RemovedStepIDs, wantRemoved)
	}
	if !equalStringSlices(constructionStepIDs(removedDocument.Construction.Steps), []string{filter.ID}) || impact.HasMissingInputs() {
		t.Fatalf("cascade did not preserve independent filter after removing owner: steps=%#v impact=%#v", constructionStepIDs(removedDocument.Construction.Steps), impact)
	}
}

func TestConstructionRelatedExpandTargetEditCascadesDependentsButPolicyEditDoesNot(t *testing.T) {
	workspace, construction := seedRelatedExpandRemovalConstruction(t)
	document := workspace.Documents[0]
	baseStages := relatedExpandRemovalTestStages(t, workspace, "patients")
	firstStepID := construction.Steps[0].ID

	t.Run("target resource change removes dependent expansions before schema rebuild", func(t *testing.T) {
		candidate := cloneLifecycleTestConstruction(t, construction)
		first := &candidate.Steps[0]
		first.Operation.RelatedExpand.TargetNodeID = "condition"
		first.Operation.RelatedExpand.TargetResourceType = "Condition"
		first.Operation.RelatedExpand.Route[0].ToNodeID = "condition"
		first.Operation.RelatedExpand.Route[0].ToResourceType = "Condition"
		for outputIndex := range first.Outputs {
			if first.Outputs[outputIndex].ID == first.Operation.RelatedExpand.RelatedRecordColumnID {
				first.Outputs[outputIndex].Name = "related_conditions"
				first.Outputs[outputIndex].Label = "related_conditions"
			}
		}
		updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, firstStepID, nil, baseStages)
		if err != nil {
			t.Fatalf("analyze target edit with dependent cascade: %v", err)
		}
		if !equalStringSlices(impact.RemovedStepIDs, []string{"expand_conditions", "expand_medications"}) {
			t.Fatalf("target edit removed steps = %#v, want dependent expansion suffix", impact.RemovedStepIDs)
		}
		if !equalStringSlices(constructionStepIDs(updated.Construction.Steps), []string{firstStepID, "keep_root_filter"}) || impact.HasMissingInputs() {
			t.Fatalf("target edit candidate = %#v, impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
		}
	})

	t.Run("same target with changed route and empty policy keeps valid downstream operations", func(t *testing.T) {
		candidate := cloneLifecycleTestConstruction(t, construction)
		candidate.Steps[0].Operation.RelatedExpand.EmptyPolicy = ws.ConstructionExpandEmptyError
		candidate.Steps[0].Operation.RelatedExpand.Route[0].EdgeID = "edited-observations-edge"
		candidate.Steps[0].Operation.RelatedExpand.Route[0].Relationship = "edited_observations"
		updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, firstStepID, nil, baseStages)
		if err != nil {
			t.Fatalf("analyze policy edit: %v", err)
		}
		if len(impact.RemovedStepIDs) != 0 || !equalStringSlices(constructionStepIDs(updated.Construction.Steps), constructionStepIDs(construction.Steps)) {
			t.Fatalf("policy edit removed valid dependents: steps=%#v impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
		}
	})

	t.Run("middle target resource change removes only its dependent suffix", func(t *testing.T) {
		candidate := cloneLifecycleTestConstruction(t, construction)
		middle := &candidate.Steps[1]
		middle.Operation.RelatedExpand.TargetNodeID = "medication"
		middle.Operation.RelatedExpand.TargetResourceType = "Medication"
		middle.Operation.RelatedExpand.Route[0].EdgeID = "medications"
		middle.Operation.RelatedExpand.Route[0].ToNodeID = "medication"
		middle.Operation.RelatedExpand.Route[0].ToResourceType = "Medication"
		middle.Operation.RelatedExpand.Route[0].Relationship = "medications"
		for outputIndex := range middle.Outputs {
			if middle.Outputs[outputIndex].ID == middle.Operation.RelatedExpand.RelatedRecordColumnID {
				middle.Outputs[outputIndex].Name = "related_medications"
				middle.Outputs[outputIndex].Label = "related_medications"
			}
		}
		updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, middle.ID, nil, baseStages)
		if err != nil {
			t.Fatalf("analyze middle target edit with dependent cascade: %v", err)
		}
		if !equalStringSlices(impact.RemovedStepIDs, []string{"expand_medications"}) ||
			!equalStringSlices(constructionStepIDs(updated.Construction.Steps), []string{"expand_observations", middle.ID, "keep_root_filter"}) || impact.HasMissingInputs() {
			t.Fatalf("middle target edit result = steps=%#v impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
		}
	})
}

func TestConstructionEditCascadeKeepsChangedStepWhenItsOwnInputIsMissing(t *testing.T) {
	workspace, construction := seedGroupKeyEditConstruction(t)
	document := workspace.Documents[0]
	candidate := cloneLifecycleTestConstruction(t, construction)
	candidate.Steps[0].Operation.Group.Keys[0].InputColumnID = "missing_source_column"
	updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, "group-records", nil, nil)
	if err != nil {
		t.Fatalf("analyze invalid GROUP edit: %v", err)
	}
	if len(impact.RemovedStepIDs) != 0 || !equalStringSlices(constructionStepIDs(updated.Construction.Steps), constructionStepIDs(construction.Steps)) {
		t.Fatalf("edit cascade removed the changed step or its descendants: steps=%#v impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
	}
	if len(impact.MissingInputs) != 1 || impact.MissingInputs[0] != (ws.ConstructionDependencyIssue{StepID: "group-records", ColumnID: "missing_source_column"}) {
		t.Fatalf("missing changed-step input = %#v, want one NEEDS_REPAIR issue", impact.MissingInputs)
	}
}

func cloneLifecycleTestConstruction(t *testing.T, construction ws.Construction) ws.Construction {
	t.Helper()
	raw, err := json.Marshal(construction)
	if err != nil {
		t.Fatal(err)
	}
	var clone ws.Construction
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func constructionWithoutStep(construction ws.Construction, stepID string) ws.Construction {
	candidate := construction
	candidate.Steps = make([]ws.ConstructionStep, 0, len(construction.Steps)-1)
	for _, step := range construction.Steps {
		if step.ID != stepID {
			candidate.Steps = append(candidate.Steps, step)
		}
	}
	return candidate
}

func constructionStepIDs(steps []ws.ConstructionStep) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID)
	}
	return ids
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

func seedRelatedExpandRemovalConstruction(t *testing.T) (ws.Workspace, ws.Construction) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	resources := []string{"Patient", "Observation", "Condition", "Medication"}
	nodes := []string{"patient", "observation", "condition", "medication"}
	relationships := []string{"observations", "conditions", "medications"}
	outputs := make([]ws.StageColumn, 0, len(document.Columns)+len(relationships))
	for _, column := range document.Columns {
		outputs = append(outputs, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	steps := make([]ws.ConstructionStep, 0, len(relationships))
	for index, relationship := range relationships {
		stepID := "expand_" + relationship
		anchorColumnID := "_key"
		input := ws.ConstructionInputRef{Kind: ws.ConstructionInputSourceProjection}
		if index > 0 {
			anchorColumnID = "test_terminal_identity_" + steps[index-1].ID
			input = ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: steps[index-1].ID}
		}
		relatedRecordColumnID := "related_" + relationship
		route := []capability.ConstructionRouteStep{{
			EdgeID: relationship, FromNodeID: nodes[index], ToNodeID: nodes[index+1],
			FromResourceType: resources[index], ToResourceType: resources[index+1],
			Relationship: relationship, StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}}
		stepOutputs := append(append([]ws.StageColumn(nil), outputs...), ws.StageColumn{
			ID: relatedRecordColumnID, Name: relatedRecordColumnID, Label: relatedRecordColumnID, Type: "string", Nullable: false,
		})
		steps = append(steps, ws.ConstructionStep{
			ID: stepID, Inputs: []ws.ConstructionInputRef{input},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: &ws.ConstructionRelatedExpand{
				AnchorColumnID: anchorColumnID, ChoiceID: "choice_" + stepID,
				TargetNodeID: nodes[index+1], TargetResourceType: resources[index+1], Route: route,
				ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
				EmptyPolicy:     ws.ConstructionExpandEmptyExclude, RelatedRecordColumnID: relatedRecordColumnID,
			}},
			Outputs: stepOutputs,
		})
		outputs = stepOutputs
	}
	filterColumnID := document.Columns[0].ColumnID
	steps = append(steps, ws.ConstructionStep{
		ID: "keep_root_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: steps[len(steps)-1].ID}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: filterColumnID, Operator: ws.ConstructionFilterExists},
		},
		Outputs: append([]ws.StageColumn(nil), outputs...),
	})
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: steps}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate related expansion chain: %v", err)
	}
	return workspace, *document.Construction
}

func relatedExpandRemovalTestStages(t *testing.T, workspace ws.Workspace, outputID string) []receiptmodel.ReceiptConstructionStage {
	t.Helper()
	stages := testConstructionStageDescriptors(workspace, outputID)
	index := constructionDocumentIndex(workspace, outputID)
	if index < 0 || workspace.Documents[index].Construction == nil {
		t.Fatal("related expansion test workspace has no construction")
	}
	rootAnchor := stages[0].RelatedExpandAnchors[0]
	var active *receiptmodel.ReceiptConstructionActiveRelatedRecord
	for stepIndex, step := range workspace.Documents[index].Construction.Steps {
		related := step.Operation.RelatedExpand
		if related == nil {
			if step.Operation.Kind != ws.ConstructionOperationFilter &&
				step.Operation.Kind != ws.ConstructionOperationDerive &&
				step.Operation.Kind != ws.ConstructionOperationRelatedSource &&
				step.Operation.Kind != ws.ConstructionOperationRelatedField {
				active = nil
			}
			if active != nil {
				stage := &stages[stepIndex+1]
				stage.ActiveRelatedRecord = active
				stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{
					rootAnchor,
					{AnchorColumnID: active.TerminalIdentityColumn, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
				}
			}
			continue
		}
		stage := &stages[stepIndex+1]
		anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, related.Route[0].FromResourceType
		if related.AnchorColumnID != "_key" {
			if active == nil || active.TerminalIdentityColumn != related.AnchorColumnID {
				t.Fatalf("step %q anchor %q does not match its prior active identity", step.ID, related.AnchorColumnID)
			}
			anchorKind, anchorNodeID, anchorResourceType = "activeRelatedRecord", active.TargetNodeID, active.TargetResourceType
		}
		terminalColumnID := "test_terminal_identity_" + step.ID
		stage.RelatedExpand.AnchorColumnID = related.AnchorColumnID
		stage.RelatedExpand.AnchorColumn = related.AnchorColumnID
		stage.RelatedExpand.AnchorKind = anchorKind
		stage.RelatedExpand.AnchorNodeID = anchorNodeID
		stage.RelatedExpand.AnchorResourceType = anchorResourceType
		stage.RelatedExpand.TerminalIdentityColumn = terminalColumnID
		active = &receiptmodel.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			TerminalIdentityColumn: terminalColumnID,
		}
		stage.ActiveRelatedRecord = active
		stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{
			rootAnchor,
			{AnchorColumnID: terminalColumnID, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
		}
	}
	return stages
}

func seedGroupKeyEditConstruction(t *testing.T) (ws.Workspace, ws.Construction) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Columns) == 0 {
		t.Fatal("group-key test document has no source columns")
	}
	inputID := document.Columns[0].ColumnID
	groupOutputs := []ws.StageColumn{
		{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"},
	}
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID: "group-records", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group-records", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
				Keys:       []ws.ConstructionGroupKey{{InputColumnID: inputID, OutputColumnID: "group_id"}},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: groupOutputs,
		},
		{
			ID: "dependent-id-filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group-records"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "group_id", Operator: ws.ConstructionFilterExists,
			}},
			Outputs: append([]ws.StageColumn(nil), groupOutputs...),
		},
		{
			ID: "independent-count-filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "dependent-id-filter"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "row_count", Operator: ws.ConstructionFilterExists,
			}},
			Outputs: append([]ws.StageColumn(nil), groupOutputs...),
		},
	}}
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate GROUP key dependency fixture: %v", err)
	}
	return workspace, construction
}

func TestReauthorizeRelatedExpandContributorRejectsTamperedRepeatedBoundaries(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "construction-route-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation"}}
	snapshot.Edges = []capability.Edge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND"}}
	status := capability.Candidate{ID: "observation-status", NodeID: "observation", ResourceType: "Observation", FieldPath: "status", Label: "Observation status", LogicalType: "code", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}
	repeated := capability.Candidate{
		ID: "observation-category-code", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "category[].coding[].code", Label: "Observation category code", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray}, SuggestedValues: []string{"d", "t"}, SuggestionsComplete: true,
	}
	snapshot.Candidates = []capability.Candidate{status, repeated}
	route := []capability.ConstructionRouteStep{{EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}}
	rootChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, status)
	if err != nil {
		t.Fatal(err)
	}
	contributorChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, repeated)
	if err != nil {
		t.Fatal(err)
	}
	contributorValue := "d"
	related := ws.ConstructionRelatedExpand{
		ChoiceID: rootChoice.ChoiceID, TargetNodeID: "observation", TargetResourceType: "Observation", Route: route,
		ContributorChoiceID: contributorChoice.ChoiceID,
		ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches, Predicate: &ws.ContributorPredicate{
			CandidateID: repeated.ID, Operator: ws.ContributorEquals, Quantifier: ws.ContributorAny,
			Value: &ws.ContributorValue{Kind: ws.ContributorString, String: &contributorValue},
		}},
	}
	related.ContributorSource = &ws.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: repeated.ID, NodeID: repeated.NodeID,
		ResourceType: repeated.ResourceType, Path: repeated.FieldPath, Cardinality: repeated.Cardinality,
		LogicalType: repeated.LogicalType, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), repeated.RepeatedBoundaries...),
	}
	catalog := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation"}},
		Edges: []ws.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"}},
		Candidates: []ws.CatalogCandidate{
			{ID: status.ID, NodeID: status.NodeID, FieldPath: status.FieldPath, Label: status.Label, LogicalType: status.LogicalType, Cardinality: status.Cardinality, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &rootChoice},
			{ID: repeated.ID, NodeID: repeated.NodeID, FieldPath: repeated.FieldPath, Label: repeated.Label, LogicalType: repeated.LogicalType, Cardinality: repeated.Cardinality, ProjectionModes: []string{"ARRAY"}, DefaultProjectionMode: "ARRAY", ConstructionChoice: &contributorChoice},
		},
	}
	base := Base{Snapshot: snapshot, Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, Catalog: catalog, Document: ws.Document{RootResourceType: "Patient", Rows: ws.RecordsRowDefinition()}}
	if err := reauthorizeRelatedExpandContributor(context.Background(), base, "Patient", related); err != nil {
		t.Fatalf("repeated contributor signed choice reauthorization: %v", err)
	}
	tampered := *related.ContributorSource
	tampered.RepeatedBoundaries = append([]capability.RepeatedBoundary(nil), related.ContributorSource.RepeatedBoundaries...)
	tampered.RepeatedBoundaries[1].MaxItems++
	related.ContributorSource = &tampered
	if err := reauthorizeRelatedExpandContributor(context.Background(), base, "Patient", related); err == nil || lifecycleErrorCode(err) != "INVALID_CONSTRUCTION_CHOICE" {
		t.Fatalf("tampered repeated boundaries were not rejected as an invalid signed choice: %v", err)
	}
}
