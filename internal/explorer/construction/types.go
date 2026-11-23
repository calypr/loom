package construction

import (
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/rowsource"
)

type ConstructionDependencyIssue = ws.ConstructionDependencyIssue
type ConstructionDependencyImpact = ws.ConstructionImpact

type ConstructionFilterSourceCapability = rowsource.ConstructionFilterSourceCapability
type ConstructionFilterSourceSelection = rowsource.ConstructionFilterSourceSelection
type ConstructionDeriveSourceCapability = rowsource.ConstructionDeriveSourceCapability
type ConstructionUnpivotSourceCapability = rowsource.ConstructionUnpivotSourceCapability
type ConstructionExpandSourceCapability = rowsource.ConstructionExpandSourceCapability
type ConstructionDeriveSourceSelection = rowsource.ConstructionDeriveSourceSelection
type ConstructionUnpivotSourceSelection = rowsource.ConstructionUnpivotSourceSelection
type ConstructionExpandSourceSelection = rowsource.ConstructionExpandSourceSelection

const maxConstructionChoiceProposalChoices = 100

type ConstructionSemanticGroupChoice struct {
	ChoiceID     string `json:"choiceId"`
	BindingID    string `json:"bindingId"`
	RuleVersion  string `json:"ruleVersion"`
	Label        string `json:"label"`
	ScopeLabel   string `json:"scopeLabel"`
	ResourceType string `json:"resourceType"`
	SourcePath   string `json:"sourcePath"`
}

type ConstructionCapabilitiesRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	GroupStepID          string
	IncludeGroupChoices  bool
}

type ConstructionCapabilitiesResponse struct {
	SnapshotToken                  string                                    `json:"snapshotToken"`
	DraftVersion                   int64                                     `json:"draftVersion"`
	DraftDigest                    string                                    `json:"draftDigest"`
	OutputID                       string                                    `json:"outputId"`
	StageID                        string                                    `json:"stageId"`
	BaseConstruction               ws.Construction                           `json:"baseConstruction"`
	Stages                         []receiptmodel.ReceiptConstructionStage   `json:"stages"`
	SelectedStage                  receiptmodel.ReceiptConstructionStage     `json:"selectedStage"`
	SourceInput                    ConstructionGroupSourceCapability         `json:"sourceInput"`
	PivotSourceInput               ConstructionPivotSourceCapability         `json:"pivotSourceInput"`
	FilterSourceInput              ConstructionFilterSourceCapability        `json:"filterSourceInput"`
	DeriveSourceInput              ConstructionDeriveSourceCapability        `json:"deriveSourceInput"`
	UnpivotSourceInput             ConstructionUnpivotSourceCapability       `json:"unpivotSourceInput"`
	ExpandSourceInput              ConstructionExpandSourceCapability        `json:"expandSourceInput"`
	GroupFieldChoices              []ConstructionGroupFieldChoice            `json:"groupFieldChoices"`
	GroupFieldChoicesIncomplete    bool                                      `json:"groupFieldChoicesIncomplete,omitempty"`
	GroupRowValueChoices           []ConstructionGroupFieldChoice            `json:"groupRowValueChoices"`
	SemanticGroupChoices           []ConstructionSemanticGroupChoice         `json:"semanticGroupChoices,omitempty"`
	SemanticGroupChoicesIncomplete bool                                      `json:"semanticGroupChoicesIncomplete,omitempty"`
	SourceProjectionAvailability   *ConstructionSourceProjectionAvailability `json:"sourceProjectionAvailability,omitempty"`
	NodeOccurrences                map[string]string                         `json:"nodeOccurrences,omitempty"`
	WorkspaceInputs                []ConstructionWorkspaceInput              `json:"workspaceInputs"`
}

type ConstructionGroupFieldChoice struct {
	Kind               string                                    `json:"kind"`
	InputColumnID      string                                    `json:"inputColumnId,omitempty"`
	FieldSource        *ws.ConstructionGroupFieldSource          `json:"fieldSource,omitempty"`
	SemanticCategory   *ws.ConstructionGroupSemanticCategory     `json:"semanticCategory,omitempty"`
	ValueKind          string                                    `json:"valueKind,omitempty"`
	SourceStageID      string                                    `json:"sourceStageId,omitempty"`
	SourceShapeID      string                                    `json:"sourceShapeId,omitempty"`
	CandidateID        string                                    `json:"candidateId,omitempty"`
	Label              string                                    `json:"label"`
	ScopeLabel         string                                    `json:"scopeLabel,omitempty"`
	ValueType          string                                    `json:"valueType,omitempty"`
	SourceRole         string                                    `json:"sourceRole,omitempty"`
	OccurrenceID       string                                    `json:"occurrenceId,omitempty"`
	ResourceType       string                                    `json:"resourceType,omitempty"`
	OwnerOccurrenceID  string                                    `json:"ownerOccurrenceId,omitempty"`
	OwnerResourceType  string                                    `json:"ownerResourceType,omitempty"`
	CanonicalPath      string                                    `json:"canonicalPath,omitempty"`
	BoundarySelector   string                                    `json:"boundarySelector,omitempty"`
	RelatedOccurrence  *ConstructionGroupRelatedOccurrenceChoice `json:"relatedOccurrence,omitempty"`
	MatchedOutputKeyID string                                    `json:"matchedOutputKeyId,omitempty"`
}

type ConstructionGroupRelatedOccurrenceChoice struct {
	RouteID    string `json:"routeId"`
	RouteLabel string `json:"routeLabel,omitempty"`
}

type ConstructionSourceProjectionAvailability struct {
	Supported  bool   `json:"supported"`
	StageID    string `json:"stageId"`
	ReasonCode string `json:"reasonCode,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ConstructionWorkspaceInput struct {
	OutputID string                             `json:"outputId"`
	Title    string                             `json:"title"`
	Columns  []ConstructionWorkspaceInputColumn `json:"columns"`
}

type ConstructionWorkspaceInputColumn struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Label                  string `json:"label"`
	LogicalType            string `json:"logicalType"`
	Cardinality            string `json:"cardinality"`
	Nullable               bool   `json:"nullable"`
	JoinCompatibilityKey   string `json:"joinCompatibilityKey,omitempty"`
	AppendCompatibilityKey string `json:"appendCompatibilityKey,omitempty"`
}

type ConstructionGroupSourceCapability struct {
	Supported  bool                            `json:"supported"`
	StageID    string                          `json:"stageId"`
	ReasonCode string                          `json:"reasonCode,omitempty"`
	Reason     string                          `json:"reason,omitempty"`
	Choices    []ConstructionGroupSourceChoice `json:"choices"`
}

type ConstructionGroupSourceChoice struct {
	ChoiceID     string `json:"choiceId"`
	OccurrenceID string `json:"occurrenceId"`
	FieldPath    string `json:"fieldPath"`
	Label        string `json:"label"`
	FHIRType     string `json:"fhirType"`
	LogicalType  string `json:"logicalType"`
	ValueType    string `json:"valueType"`
	IsIdentifier bool   `json:"isIdentifier"`
	IsReference  bool   `json:"isReference"`
	IsPopulated  bool   `json:"isPopulated"`
}

type ConstructionPivotSourceCapability struct {
	Supported  bool                            `json:"supported"`
	StageID    string                          `json:"stageId"`
	ReasonCode string                          `json:"reasonCode,omitempty"`
	Reason     string                          `json:"reason,omitempty"`
	Choices    []ConstructionPivotSourceChoice `json:"choices"`
}

type ConstructionPivotSourceChoice struct {
	ChoiceID     string `json:"choiceId"`
	ColumnID     string `json:"columnId,omitempty"`
	OccurrenceID string `json:"occurrenceId"`
	FieldPath    string `json:"fieldPath"`
	Label        string `json:"label"`
	FHIRType     string `json:"fhirType"`
	LogicalType  string `json:"logicalType"`
	ValueType    string `json:"valueType"`
	IsIdentifier bool   `json:"isIdentifier"`
	IsReference  bool   `json:"isReference"`
	IsPopulated  bool   `json:"isPopulated"`
}

type ConstructionProposalRequest struct {
	Project               string
	ExplorerID            string
	SnapshotToken         string
	ExpectedDraftVersion  int64
	ExpectedDraftDigest   string
	OutputID              string
	ChangedStepID         string
	RemoveStepIDs         []string
	CandidateConstruction ws.Construction
	GroupSources          []ConstructionGroupSourceSelection   `json:"groupSources,omitempty"`
	GroupSource           *ConstructionGroupSourceSelection    `json:"groupSource,omitempty"`
	PivotSources          []ConstructionPivotSourceSelection   `json:"pivotSources,omitempty"`
	FilterSources         []ConstructionFilterSourceSelection  `json:"filterSources,omitempty"`
	DeriveSources         []ConstructionDeriveSourceSelection  `json:"deriveSources,omitempty"`
	UnpivotSources        []ConstructionUnpivotSourceSelection `json:"unpivotSources,omitempty"`
	ExpandSources         []ConstructionExpandSourceSelection  `json:"expandSources,omitempty"`
	Limit                 int
}

type ConstructionGroupSourceSelection struct {
	RowChoiceID string `json:"rowChoiceId"`
	ColumnID    string `json:"columnId"`
}

type ConstructionPivotSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

type ConstructionProposalDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	StepID  string `json:"stepId,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type ConstructionProposalResponse struct {
	ProposalID               string                                  `json:"proposalId,omitempty"`
	BaseReceiptID            string                                  `json:"baseReceiptId,omitempty"`
	OutputID                 string                                  `json:"outputId"`
	SnapshotToken            string                                  `json:"snapshotToken"`
	DraftVersion             int64                                   `json:"draftVersion"`
	DraftDigest              string                                  `json:"draftDigest"`
	BaseDocumentDigest       string                                  `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                                  `json:"candidateWorkspaceDigest"`
	ChangedStepID            string                                  `json:"changedStepId"`
	CandidateConstruction    ws.Construction                         `json:"candidateConstruction"`
	DependencyImpact         ws.ConstructionImpact                   `json:"dependencyImpact"`
	Stages                   []receiptmodel.ReceiptConstructionStage `json:"stages"`
	PreviewStatus            string                                  `json:"previewStatus"`
	PreviewDurationMS        int64                                   `json:"previewDurationMs"`
	Preview                  any                                     `json:"preview,omitempty"`
	Diagnostics              []ConstructionProposalDiagnostic        `json:"diagnostics,omitempty"`
}

func (r ConstructionCapabilitiesRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID, "stageId": r.StageID,
	} {
		if err := support.RequireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.GroupStepID != "" {
		if err := support.RequireExactIdentity(r.GroupStepID, "groupStepId"); err != nil {
			return err
		}
	}
	return nil
}

func (r ConstructionProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if err := support.RequireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ChangedStepID == "" {
		if len(r.RemoveStepIDs) == 0 {
			return fmt.Errorf("changedStepId or at least one removeStepId is required")
		}
	} else if err := support.RequireExactIdentity(r.ChangedStepID, "changedStepId"); err != nil {
		return err
	}
	seenRemoved := make(map[string]struct{}, len(r.RemoveStepIDs))
	for _, stepID := range r.RemoveStepIDs {
		if err := support.RequireExactIdentity(stepID, "removeStepId"); err != nil {
			return err
		}
		if _, exists := seenRemoved[stepID]; exists {
			return fmt.Errorf("removeStepIds contains duplicate %q", stepID)
		}
		seenRemoved[stepID] = struct{}{}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.GroupSource != nil && len(r.GroupSources) != 0 {
		return fmt.Errorf("use groupSource or groupSources, not both")
	}
	seenGroupChoices, seenGroupColumns := make(map[string]bool), make(map[string]bool)
	for _, selection := range r.GroupSources {
		if err := support.RequireExactIdentity(selection.RowChoiceID, "groupSources.rowChoiceId"); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(selection.ColumnID, "groupSources.columnId"); err != nil {
			return err
		}
		if seenGroupChoices[selection.RowChoiceID] || seenGroupColumns[selection.ColumnID] {
			return fmt.Errorf("groupSources contains a duplicate choice or column")
		}
		seenGroupChoices[selection.RowChoiceID], seenGroupColumns[selection.ColumnID] = true, true
	}
	if r.GroupSource != nil {
		if err := support.RequireExactIdentity(r.GroupSource.RowChoiceID, "groupSource.rowChoiceId"); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(r.GroupSource.ColumnID, "groupSource.columnId"); err != nil {
			return err
		}
	}
	if err := validatePivotSourceSelections(r.PivotSources); err != nil {
		return err
	}
	if err := rowsource.ValidateFilterSourceSelections(r.FilterSources); err != nil {
		return err
	}
	if err := rowsource.ValidateDeriveSourceSelections(r.DeriveSources); err != nil {
		return err
	}
	if err := rowsource.ValidateUnpivotSourceSelections(r.UnpivotSources); err != nil {
		return err
	}
	if err := rowsource.ValidateExpandSourceSelections(r.ExpandSources); err != nil {
		return err
	}
	for stepIndex, step := range r.CandidateConstruction.Steps {
		for inputIndex, input := range step.Inputs {
			if err := input.Validate(); err != nil {
				return fmt.Errorf("candidateConstruction.steps[%d].inputs[%d]: %w", stepIndex, inputIndex, err)
			}
			if input.Kind == ws.ConstructionInputWorkspaceOutput && step.Operation.Kind != ws.ConstructionOperationCombine {
				return fmt.Errorf("candidateConstruction.steps[%d].inputs[%d]: WORKSPACE_OUTPUT is only supported by COMBINE", stepIndex, inputIndex)
			}
			if input.Kind == ws.ConstructionInputTableRevision && step.Operation.Kind != ws.ConstructionOperationCombine {
				return fmt.Errorf("candidateConstruction.steps[%d].inputs[%d]: TABLE_REVISION is only supported by COMBINE", stepIndex, inputIndex)
			}
		}
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit must be between 1 and %d", dataframeexecution.MaxPreviewLimit)
	}
	return nil
}

type ConstructionChoiceProposalSelection struct {
	RowValuePolicy ws.ConstructionRowValuePolicy     `json:"rowValuePolicy,omitempty"`
	ChoiceID       string                            `json:"choiceId"`
	Form           capability.ConstructionChoiceForm `json:"form"`
	FrameID        string                            `json:"frameId,omitempty"`
	Title          *string                           `json:"title,omitempty"`
}

type ConstructionChoiceProposalRequest struct {
	CommandID            string
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	ConstructionChoices  []ConstructionChoiceProposalSelection
	Limit                int
}

type ConstructionChoiceProposalResponse struct {
	CommandID                string                                `json:"commandId"`
	SnapshotToken            string                                `json:"snapshotToken"`
	DraftVersion             int64                                 `json:"draftVersion"`
	DraftDigest              string                                `json:"draftDigest"`
	OutputID                 string                                `json:"outputId"`
	ConstructionChoices      []ConstructionChoiceProposalSelection `json:"constructionChoices"`
	CandidateWorkspaceDigest string                                `json:"candidateWorkspaceDigest"`
	CandidateColumnIDs       []string                              `json:"candidateColumnIds"`
	PreviewStatus            string                                `json:"previewStatus"`
	PreviewDurationMS        int64                                 `json:"previewDurationMs"`
	Preview                  any                                   `json:"preview,omitempty"`
	PreviewReceiptID         string                                `json:"-"`
}

func (r ConstructionChoiceProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"commandId": r.CommandID, "project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if err := support.RequireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if len(r.ConstructionChoices) == 0 || len(r.ConstructionChoices) > maxConstructionChoiceProposalChoices {
		return fmt.Errorf("constructionChoices must contain between 1 and %d choices", maxConstructionChoiceProposalChoices)
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit must be between 1 and %d", dataframeexecution.MaxPreviewLimit)
	}
	for index, choice := range r.ConstructionChoices {
		if err := support.RequireExactIdentity(choice.ChoiceID, fmt.Sprintf("constructionChoices[%d].choiceId", index)); err != nil {
			return err
		}
		if choice.FrameID != "" {
			if err := support.RequireExactIdentity(choice.FrameID, fmt.Sprintf("constructionChoices[%d].frameId", index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePivotSourceSelections(selections []ConstructionPivotSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := support.RequireExactIdentity(selection.ChoiceID, fmt.Sprintf("pivotSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(selection.ColumnID, fmt.Sprintf("pivotSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("pivotSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}
