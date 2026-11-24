package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
)

func cloneLifecycleTestConstruction(t *testing.T, construction ws.Construction) ws.Construction {
	return constructionfixtures.CloneConstruction(t, construction)
}

func constructionWithoutStep(construction ws.Construction, stepID string) ws.Construction {
	return constructionfixtures.ConstructionWithoutStep(construction, stepID)
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

func seedRelatedExpandRemovalConstruction(t *testing.T, store *fakeStore) (ws.Workspace, ws.Construction) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
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

func relatedExpandRemovalTestStages(t *testing.T, workspace ws.Workspace, outputID string) []receiptmodel.ReceiptConstructionStage {
	return constructionfixtures.RelatedExpandRemovalStages(t, workspace, outputID)
}

func lifecycleConstructionReceipt(snapshot capability.Snapshot, workspace ws.Workspace) *receiptmodel.CompilationReceipt {
	return constructionfixtures.LifecycleConstructionReceipt(snapshot, workspace)
}

func lifecycleErrorCode(err error) string {
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	return ""
}

func proposalErrorIs(err error, class ports.ErrorClass, code string) bool {
	var lifecycleErr *ports.Error
	return errors.As(err, &lifecycleErr) && lifecycleErr.Class == class && lifecycleErr.Code == code
}

func lifecycleTestStringScalar(value string) ws.TableScalar {
	return ws.TableScalar{Kind: ws.TableScalarString, String: &value}
}

func testCodedCategoryFamily(sourceKind, resourceType, sourcePath, system, version string) semantic.Family {
	return constructionfixtures.CodedCategoryFamily(sourceKind, resourceType, sourcePath, system, version)
}

type libraryRepository struct {
	library      interpretation.InterpretationLibrary
	revisions    map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision
	createSeen   *interpretation.InterpretationRevision
	createErr    error
	mutateCreate bool
}

func (r *libraryRepository) ListInterpretationLibraries(context.Context, string) ([]interpretation.InterpretationLibrary, error) {
	if r.library.ID == "" {
		return nil, nil
	}
	return []interpretation.InterpretationLibrary{r.library}, nil
}

func (r *libraryRepository) GetInterpretationRevision(_ context.Context, project string, id interpretation.InterpretationRevisionID) (*interpretation.InterpretationRevision, error) {
	revision, ok := r.revisions[id]
	if !ok || revision.Project != project {
		return nil, explorer.ErrNotFound
	}
	return &revision, nil
}

func (r *libraryRepository) CreateInterpretationRevision(_ context.Context, revision interpretation.InterpretationRevision, expectedParent *interpretation.InterpretationRevisionID) (*interpretation.InterpretationRevision, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	if existing, ok := r.revisions[revision.ID]; ok {
		return &existing, nil
	}
	if expectedParent == nil {
		if r.library.HeadRevisionID != "" {
			return nil, interpretation.ErrInterpretationParentConflict
		}
	} else if r.library.HeadRevisionID != *expectedParent {
		return nil, interpretation.ErrInterpretationParentConflict
	}
	r.createSeen = &revision
	if r.mutateCreate {
		revision.Explanation = "mutated by adapter"
	}
	r.revisions[revision.ID] = revision
	r.library.HeadRevisionID = revision.ID
	r.library.HeadDigest = revision.ContentDigest
	return &revision, nil
}
