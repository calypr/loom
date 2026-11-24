package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
)

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

type lifecycleConstructionColumn struct {
	name, label, logical string
	nullable             bool
	construction         string
}

func lifecycleConstructionReceipt(snapshot capability.Snapshot, workspace ws.Workspace) *receiptmodel.CompilationReceipt {
	receipt := nativeReceipt(snapshot)
	receipt.IntentDigest, _ = workspace.Digest()
	receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
	columns := []lifecycleConstructionColumn{
		{name: "patient_id", label: "Patient ID", logical: "string"},
		{name: "category", label: "Category", logical: "string", nullable: true},
		{name: "value", label: "Value", logical: "decimal", nullable: true},
		{name: "value_alt", label: "Alternate value", logical: "decimal"},
		{name: "category_number", label: "Numeric category", logical: "integer"},
		{name: "category_boolean", label: "Boolean category", logical: "boolean"},
		{name: "text_value", label: "Text value", logical: "string"},
		{name: "overflow", label: "Overflow category", logical: "string"},
	}
	public := receiptmodel.PublicOutputContract{OutputID: "patients", Columns: make([]receiptmodel.PublicOutputColumn, 0, len(columns))}
	receipt.EmittedColumns = make([]viewer.EmittedColumn, 0, len(columns))
	provenance := make(map[string]string, len(columns))
	for index, column := range columns {
		constructionID := column.construction
		public.Columns = append(public.Columns, receiptmodel.PublicOutputColumn{
			Column: column.name, ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		receipt.EmittedColumns = append(receipt.EmittedColumns, viewer.EmittedColumn{
			EmissionID: fmt.Sprintf("em_%02d", index), OutputID: "patients", PublicColumn: column.name,
			ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		provenance[column.name] = "EXPLICIT"
	}
	contract, _ := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{public}})
	receipt.PublicOutputContract = contract
	receipt.OutputContractDigest, _ = receiptmodel.CompilationArtifactDigest(contract)
	receipt.OutputFingerprints = map[string]string{"patients": "fingerprint-" + receipt.IntentDigest}
	receipt.OutputColumnProvenance = map[string]map[string]string{"patients": provenance}
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(*receipt)
	receipt.ID, _ = receiptmodel.ReceiptID(*receipt)
	return receipt
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
	reference := semantic.FamilyReference{
		BindingID: "binding-" + resourceType, SourceKind: sourceKind, ResourceType: resourceType,
		SourcePath: sourcePath, System: system, Version: version,
	}
	reference.FamilyID = semantic.FamilyID(reference)
	return semantic.Family{
		FamilyReference: reference,
		Source:          semantic.Source{Type: resourceType, Path: sourcePath},
		KeySelector:     "code.coding[]",
		Value: semantic.Value{
			Selector: "code.coding[].display", Type: "string",
			FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", SchemaVersion: semantic.ObservationSchemaVersion,
		RuleVersion:  strconv.Itoa(semantic.SemanticObservationRuleVersion),
		Completeness: semantic.CompletenessComplete, Status: "SUPPORTED", Population: 2,
		Examples: []string{"CARBOXYAMIDOTRIAZOLE", "another compound"},
	}
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
