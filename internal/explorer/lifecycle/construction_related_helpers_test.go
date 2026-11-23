package lifecycle

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func cloneLifecycleTestConstruction(t *testing.T, construction authoringv2.Construction) authoringv2.Construction {
	t.Helper()
	raw, err := json.Marshal(construction)
	if err != nil {
		t.Fatal(err)
	}
	var clone authoringv2.Construction
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func constructionWithoutStep(construction authoringv2.Construction, stepID string) authoringv2.Construction {
	candidate := construction
	candidate.Steps = make([]authoringv2.ConstructionStep, 0, len(construction.Steps)-1)
	for _, step := range construction.Steps {
		if step.ID != stepID {
			candidate.Steps = append(candidate.Steps, step)
		}
	}
	return candidate
}

func constructionStepIDs(steps []authoringv2.ConstructionStep) []string {
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

func seedRelatedExpandRemovalConstruction(t *testing.T, store *fakeStore) (authoringv2.Workspace, authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	resources := []string{"Patient", "Observation", "Condition", "Medication"}
	nodes := []string{"patient", "observation", "condition", "medication"}
	relationships := []string{"observations", "conditions", "medications"}
	outputs := make([]authoringv2.StageColumn, 0, len(document.Columns)+len(relationships))
	for _, column := range document.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(relationships))
	for index, relationship := range relationships {
		stepID := "expand_" + relationship
		anchorColumnID := "_key"
		input := authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
		if index > 0 {
			anchorColumnID = "test_terminal_identity_" + steps[index-1].ID
			input = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: steps[index-1].ID}
		}
		relatedRecordColumnID := "related_" + relationship
		route := []capability.ConstructionRouteStep{{
			EdgeID: relationship, FromNodeID: nodes[index], ToNodeID: nodes[index+1],
			FromResourceType: resources[index], ToResourceType: resources[index+1],
			Relationship: relationship, StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}}
		stepOutputs := append(append([]authoringv2.StageColumn(nil), outputs...), authoringv2.StageColumn{
			ID: relatedRecordColumnID, Name: relatedRecordColumnID, Label: relatedRecordColumnID, Type: "string", Nullable: false,
		})
		steps = append(steps, authoringv2.ConstructionStep{
			ID: stepID, Inputs: []authoringv2.ConstructionInputRef{input},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{
				AnchorColumnID: anchorColumnID, ChoiceID: "choice_" + stepID,
				TargetNodeID: nodes[index+1], TargetResourceType: resources[index+1], Route: route,
				ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
				EmptyPolicy:     authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: relatedRecordColumnID,
			}},
			Outputs: stepOutputs,
		})
		outputs = stepOutputs
	}
	filterColumnID := document.Columns[0].ColumnID
	steps = append(steps, authoringv2.ConstructionStep{
		ID: "keep_root_filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: steps[len(steps)-1].ID}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: filterColumnID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: append([]authoringv2.StageColumn(nil), outputs...),
	})
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: steps}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate related expansion chain: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = draft
	store.created.DraftDigest = digest
	return workspace, *document.Construction
}

func relatedExpandRemovalTestStages(t *testing.T, workspace authoringv2.Workspace, outputID string) []explorer.ReceiptConstructionStage {
	t.Helper()
	stages := testConstructionStageDescriptors(workspace, outputID)
	index := constructionDocumentIndex(workspace, outputID)
	if index < 0 || workspace.Documents[index].Construction == nil {
		t.Fatal("related expansion test workspace has no construction")
	}
	rootAnchor := stages[0].RelatedExpandAnchors[0]
	var active *explorer.ReceiptConstructionActiveRelatedRecord
	for stepIndex, step := range workspace.Documents[index].Construction.Steps {
		related := step.Operation.RelatedExpand
		if related == nil {
			if step.Operation.Kind != authoringv2.ConstructionOperationFilter &&
				step.Operation.Kind != authoringv2.ConstructionOperationDerive &&
				step.Operation.Kind != authoringv2.ConstructionOperationRelatedSource &&
				step.Operation.Kind != authoringv2.ConstructionOperationRelatedField {
				active = nil
			}
			if active != nil {
				stage := &stages[stepIndex+1]
				stage.ActiveRelatedRecord = active
				stage.RelatedExpandAnchors = []explorer.ReceiptConstructionRelatedExpandAnchor{
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
		active = &explorer.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			TerminalIdentityColumn: terminalColumnID,
		}
		stage.ActiveRelatedRecord = active
		stage.RelatedExpandAnchors = []explorer.ReceiptConstructionRelatedExpandAnchor{
			rootAnchor,
			{AnchorColumnID: terminalColumnID, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
		}
	}
	return stages
}
