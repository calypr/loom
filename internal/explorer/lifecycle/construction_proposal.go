package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

type ConstructionCapabilitiesRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
}

type ConstructionCapabilitiesResponse struct {
	SnapshotToken    string                              `json:"snapshotToken"`
	DraftVersion     int64                               `json:"draftVersion"`
	DraftDigest      string                              `json:"draftDigest"`
	OutputID         string                              `json:"outputId"`
	StageID          string                              `json:"stageId"`
	BaseConstruction authoringv2.Construction            `json:"baseConstruction"`
	Stages           []explorer.ReceiptConstructionStage `json:"stages"`
	SelectedStage    explorer.ReceiptConstructionStage   `json:"selectedStage"`
	SourceInput      ConstructionGroupSourceCapability   `json:"sourceInput"`
	PivotSourceInput ConstructionPivotSourceCapability   `json:"pivotSourceInput"`
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

func (r ConstructionCapabilitiesRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID, "stageId": r.StageID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	return nil
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
	CandidateConstruction authoringv2.Construction
	GroupSources          []ConstructionGroupSourceSelection `json:"groupSources,omitempty"`
	GroupSource           *ConstructionGroupSourceSelection  `json:"groupSource,omitempty"`
	PivotSources          []ConstructionPivotSourceSelection `json:"pivotSources,omitempty"`
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

func (r ConstructionProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ChangedStepID == "" {
		if len(r.RemoveStepIDs) == 0 {
			return fmt.Errorf("changedStepId or at least one removeStepId is required")
		}
	} else if err := requireExactIdentity(r.ChangedStepID, "changedStepId"); err != nil {
		return err
	}
	seenRemoved := make(map[string]struct{}, len(r.RemoveStepIDs))
	for _, stepID := range r.RemoveStepIDs {
		if err := requireExactIdentity(stepID, "removeStepId"); err != nil {
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
		if err := requireExactIdentity(selection.RowChoiceID, "groupSources.rowChoiceId"); err != nil {
			return err
		}
		if err := requireExactIdentity(selection.ColumnID, "groupSources.columnId"); err != nil {
			return err
		}
		if seenGroupChoices[selection.RowChoiceID] || seenGroupColumns[selection.ColumnID] {
			return fmt.Errorf("groupSources contains a duplicate choice or column")
		}
		seenGroupChoices[selection.RowChoiceID], seenGroupColumns[selection.ColumnID] = true, true
	}
	if r.GroupSource != nil {
		if err := requireExactIdentity(r.GroupSource.RowChoiceID, "groupSource.rowChoiceId"); err != nil {
			return err
		}
		if err := requireExactIdentity(r.GroupSource.ColumnID, "groupSource.columnId"); err != nil {
			return err
		}
	}
	if err := validateConstructionPivotSourceSelections(r.PivotSources); err != nil {
		return err
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit must be between 1 and %d", dataframeexecution.MaxPreviewLimit)
	}
	return nil
}

type ConstructionDependencyIssue = authoringv2.ConstructionDependencyIssue
type ConstructionDependencyImpact = authoringv2.ConstructionImpact

type ConstructionProposalResponse struct {
	ProposalID               string                              `json:"proposalId,omitempty"`
	BaseReceiptID            string                              `json:"baseReceiptId,omitempty"`
	OutputID                 string                              `json:"outputId"`
	SnapshotToken            string                              `json:"snapshotToken"`
	DraftVersion             int64                               `json:"draftVersion"`
	DraftDigest              string                              `json:"draftDigest"`
	BaseDocumentDigest       string                              `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                              `json:"candidateWorkspaceDigest"`
	ChangedStepID            string                              `json:"changedStepId"`
	CandidateConstruction    authoringv2.Construction            `json:"candidateConstruction"`
	DependencyImpact         authoringv2.ConstructionImpact      `json:"dependencyImpact"`
	Stages                   []explorer.ReceiptConstructionStage `json:"stages"`
	PreviewStatus            string                              `json:"previewStatus"`
	PreviewDurationMS        int64                               `json:"previewDurationMs"`
	Preview                  any                                 `json:"preview,omitempty"`
}

type constructionBase struct {
	owner           *explorer.Explorer
	explorerID      string
	workspace       authoringv2.Workspace
	document        authoringv2.Document
	construction    authoringv2.Construction
	snapshot        capability.Snapshot
	authorized      AuthorizedCapability
	catalog         authoringv2.CatalogSnapshot
	receipt         *explorer.CompilationReceipt
	stages          []explorer.ReceiptConstructionStage
	baseDocumentSHA string
}

func (s *Service) GetConstructionCapabilities(ctx context.Context, request ConstructionCapabilitiesRequest) (ConstructionCapabilitiesResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionCapabilitiesResponse{}, malformed("construction-capabilities", err.Error(), err)
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, err
	}
	// Choice IDs are ephemeral response authority, not receipt state. Copy the
	// stage slice before decorating the selected descriptor so a later request
	// still validates the original signed compilation receipt.
	stages := append([]explorer.ReceiptConstructionStage(nil), base.stages...)
	if authoringv2.CanPopulateConstructionRows(&base.construction) && len(stages) > 0 {
		last := len(stages) - 1
		stages[last].Capabilities = append(append([]explorer.ReceiptConstructionOperationChoice(nil), stages[last].Capabilities...), explorer.ReceiptConstructionOperationChoice{Kind: "ROW_VALUES", Supported: true})
	}

	if len(stages) == 0 {
		return ConstructionCapabilitiesResponse{}, conflict("construction-capabilities", "STAGE_CAPABILITIES_UNAVAILABLE", "the compiler receipt has no stage descriptors for this output", nil, nil)
	}
	var selected *explorer.ReceiptConstructionStage
	for index := range stages {
		if stages[index].ID == request.StageID {
			selected = &stages[index]
			break
		}
	}
	if selected == nil {
		return ConstructionCapabilitiesResponse{}, conflict("construction-capabilities", "STALE_STAGE_REFERENCE", "the selected stage is not present in the current compiled output", nil, nil)
	}
	choices, err := constructionCodedGroupChoices(base, request.OutputID, *selected)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, fmt.Errorf("compile coded-group choices: %w", err)
	}
	selected.CodedGroupChoices = choices
	sourceInput, err := s.constructionGroupSourceCapability(ctx, base, request.OutputID, selected.ID)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, fmt.Errorf("compile scalar group source choices: %w", err)
	}
	pivotSourceInput, err := s.constructionPivotSourceCapability(ctx, base, request.OutputID, selected.ID)
	if err != nil {
		return ConstructionCapabilitiesResponse{}, fmt.Errorf("compile pivot source choices: %w", err)
	}
	return ConstructionCapabilitiesResponse{
		SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
		OutputID: request.OutputID, StageID: request.StageID, BaseConstruction: base.construction,
		Stages: stages, SelectedStage: *selected, SourceInput: sourceInput, PivotSourceInput: pivotSourceInput,
	}, nil
}

func (s *Service) ProposeConstruction(ctx context.Context, request ConstructionProposalRequest) (ConstructionProposalResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionProposalResponse{}, malformed("construction-proposal", err.Error(), err)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	base, err := s.loadConstructionBase(ctx, request.Project, request.ExplorerID, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.OutputID)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateConstruction, err := s.constructionCandidateWithGroupSource(ctx, base, request)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	var authorizedPivotSourceHelpers map[string]bool
	candidateConstruction, authorizedPivotSourceHelpers, err = s.constructionCandidateWithPivotSources(ctx, base, request, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	var authorizedCodedPivotSteps map[string]bool
	candidateConstruction, authorizedCodedPivotSteps, err = s.resolveConstructionCodedPivotChoices(ctx, base, request.OutputID, candidateConstruction)
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	candidateDocument, impact, err := analyzeConstructionCandidateWithCascade(
		base.document, candidateConstruction, request.ChangedStepID, request.RemoveStepIDs, base.stages,
	)
	if err != nil {
		return ConstructionProposalResponse{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	baseRelatedSources := make(map[string]authoringv2.ConstructionRelatedSource)
	baseRelatedExpands := make(map[string]authoringv2.ConstructionRelatedExpand)
	baseRelatedEligibility := make(map[string]authoringv2.ConstructionRelatedEligibility)
	baseRelatedFields := make(map[string]authoringv2.ConstructionRelatedField)
	baseCodedGroups := make(map[string]authoringv2.ConstructionCodedGroup)
	baseCodedPivots := make(map[string]authoringv2.ConstructionCodedPivot)
	for _, step := range base.construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationRelatedSource && step.Operation.RelatedSource != nil {
			baseRelatedSources[step.ID] = *step.Operation.RelatedSource
		}
		if step.Operation.Kind == authoringv2.ConstructionOperationRelatedExpand && step.Operation.RelatedExpand != nil {
			baseRelatedExpands[step.ID] = *step.Operation.RelatedExpand
		}
		if step.Operation.Kind == authoringv2.ConstructionOperationRelatedEligibility && step.Operation.RelatedEligibility != nil {
			baseRelatedEligibility[step.ID] = *step.Operation.RelatedEligibility
		}
		if step.Operation.Kind == authoringv2.ConstructionOperationRelatedField && step.Operation.RelatedField != nil {
			baseRelatedFields[step.ID] = *step.Operation.RelatedField
		}
		if step.Operation.Kind == authoringv2.ConstructionOperationCodedGroup && step.Operation.CodedGroup != nil {
			baseCodedGroups[step.ID] = *step.Operation.CodedGroup
		}
		if step.Operation.Kind == authoringv2.ConstructionOperationCodedPivot && step.Operation.CodedPivot != nil {
			baseCodedPivots[step.ID] = *step.Operation.CodedPivot
		}
	}
	for _, step := range candidateDocument.Construction.Steps {
		switch step.Operation.Kind {
		case authoringv2.ConstructionOperationRelatedSource:
			if step.Operation.RelatedSource == nil {
				continue
			}
			if prior, exists := baseRelatedSources[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedSource) {
				continue
			}
			if err := reauthorizeConstructionRelatedSource(ctx, base, candidateDocument.RootResourceType, *step.Operation.RelatedSource); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case authoringv2.ConstructionOperationRelatedExpand:
			if step.Operation.RelatedExpand == nil {
				continue
			}
			if prior, exists := baseRelatedExpands[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedExpand) {
				continue
			}
			if err := reauthorizeConstructionRelatedExpand(ctx, base, candidateDocument.RootResourceType, step, *step.Operation.RelatedExpand); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case authoringv2.ConstructionOperationRelatedEligibility:
			if step.Operation.RelatedEligibility == nil {
				continue
			}
			if prior, exists := baseRelatedEligibility[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedEligibility) {
				continue
			}
			if err := reauthorizeConstructionRelatedEligibility(ctx, base, candidateDocument.RootResourceType, step, *step.Operation.RelatedEligibility); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case authoringv2.ConstructionOperationRelatedField:
			if step.Operation.RelatedField == nil {
				continue
			}
			if prior, exists := baseRelatedFields[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.RelatedField) {
				continue
			}
			if authorizedPivotSourceHelpers[step.ID] {
				continue
			}
			if err := reauthorizeConstructionRelatedField(ctx, base, step, *step.Operation.RelatedField); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case authoringv2.ConstructionOperationCodedGroup:
			if step.Operation.CodedGroup == nil {
				continue
			}
			if prior, exists := baseCodedGroups[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.CodedGroup) {
				continue
			}
			if err := reauthorizeConstructionCodedGroup(ctx, base, request.OutputID, step, *step.Operation.CodedGroup); err != nil {
				return ConstructionProposalResponse{}, err
			}
		case authoringv2.ConstructionOperationCodedPivot:
			if step.Operation.CodedPivot == nil {
				continue
			}
			if authorizedCodedPivotSteps[step.ID] {
				continue
			}
			if prior, exists := baseCodedPivots[step.ID]; exists && reflect.DeepEqual(prior, *step.Operation.CodedPivot) {
				continue
			}
			return ConstructionProposalResponse{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "a changed coded Pivot requires current source and category choices", nil)
		}
	}
	candidateWorkspace := base.workspace
	candidateWorkspace.Documents = append([]authoringv2.Document(nil), base.workspace.Documents...)
	documentIndex := constructionDocumentIndex(candidateWorkspace, request.OutputID)
	if documentIndex < 0 {
		return ConstructionProposalResponse{}, conflict("construction-proposal", "OUTPUT_NOT_FOUND", "the proposal output is missing from the candidate workspace", nil, nil)
	}
	candidateWorkspace.Documents[documentIndex] = candidateDocument
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil && impact.HasMissingInputs() {
		candidateDigest, err = constructionWorkspaceCandidateDigest(candidateWorkspace)
	}
	if err != nil {
		return ConstructionProposalResponse{}, fmt.Errorf("digest construction candidate workspace: %w", err)
	}
	baseResponse := ConstructionProposalResponse{
		OutputID: request.OutputID, SnapshotToken: request.SnapshotToken,
		DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
		BaseDocumentDigest: base.baseDocumentSHA, CandidateWorkspaceDigest: candidateDigest,
		ChangedStepID: request.ChangedStepID, CandidateConstruction: *candidateDocument.Construction,
		DependencyImpact: impact, Stages: base.stages,
		PreviewStatus: "NEEDS_REPAIR",
	}
	if impact.HasMissingInputs() {
		return baseResponse, nil
	}
	binding := &explorer.ConstructionProposalBinding{
		DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID, RemoveStepIDs: append([]string(nil), impact.RemovedStepIDs...),
		BaseDocumentDigest: base.baseDocumentSHA, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken, PreviewLimit: request.Limit,
	}
	candidateReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "construction-proposal-candidate", ConstructionProposal: binding,
	})
	if err != nil {
		return ConstructionProposalResponse{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-proposal", candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, base.snapshot, &candidateWorkspace); err != nil {
		return ConstructionProposalResponse{}, err
	}
	if len(candidateReceipt.ConstructionStages[request.OutputID]) == 0 {
		return ConstructionProposalResponse{}, conflict("construction-proposal", "INVALID_COMPILATION_RECEIPT", "the candidate receipt has no stage descriptors for this output", nil, nil)
	}
	baseResponse.ProposalID = candidateReceipt.ID
	baseResponse.BaseReceiptID = base.receipt.ID
	baseResponse.Stages = candidateReceipt.ConstructionStages[request.OutputID]
	baseResponse.PreviewStatus = "PREVIEW_PENDING"
	return baseResponse, nil
}

func analyzeConstructionCandidateWithCascade(
	base authoringv2.Document,
	candidate authoringv2.Construction,
	changedStepID string,
	requestedRemovals []string,
	baseStages []explorer.ReceiptConstructionStage,
) (authoringv2.Document, authoringv2.ConstructionImpact, error) {
	removeStepIDs := append([]string(nil), requestedRemovals...)
	removed := make(map[string]bool, len(removeStepIDs))
	for _, stepID := range removeStepIDs {
		removed[stepID] = true
	}
	invalidatedProviders := append([]string(nil), requestedRemovals...)
	if constructionRelatedExpandTargetChanged(base.Construction, candidate, changedStepID) {
		invalidatedProviders = append(invalidatedProviders, changedStepID)
	}
	seedImpact := authoringv2.ConstructionImpact{RemovedStepIDs: invalidatedProviders}
	seedImpact = constructionImpactForRemovedRelatedExpandAnchors(baseStages, &candidate, seedImpact)
	for _, stepID := range constructionStepsToRemoveForMissingInputs(candidate.Steps, seedImpact.MissingInputs, changedStepID, removed) {
		removed[stepID] = true
		removeStepIDs = append(removeStepIDs, stepID)
	}

	for {
		candidateAttempt := candidate
		candidateAttempt.Steps = make([]authoringv2.ConstructionStep, 0, len(candidate.Steps))
		for _, step := range candidate.Steps {
			if !removed[step.ID] {
				candidateAttempt.Steps = append(candidateAttempt.Steps, step)
			}
		}
		candidateDocument, impact, err := base.AnalyzeConstructionCandidate(candidateAttempt, changedStepID, removeStepIDs)
		if err != nil {
			return authoringv2.Document{}, authoringv2.ConstructionImpact{}, err
		}
		impact = constructionImpactForRemovedRelatedExpandAnchors(baseStages, candidateDocument.Construction, impact)
		if !impact.HasMissingInputs() {
			return candidateDocument, impact, nil
		}

		newRemovals := constructionStepsToRemoveForMissingInputs(candidateDocument.Construction.Steps, impact.MissingInputs, changedStepID, removed)
		if len(newRemovals) == 0 {
			return candidateDocument, impact, nil
		}
		for _, stepID := range newRemovals {
			removed[stepID] = true
		}
		removeStepIDs = append(removeStepIDs, newRemovals...)
	}
}

func constructionRelatedExpandTargetChanged(base *authoringv2.Construction, candidate authoringv2.Construction, changedStepID string) bool {
	if base == nil || changedStepID == "" {
		return false
	}
	var baseStep, candidateStep *authoringv2.ConstructionStep
	for index := range base.Steps {
		if base.Steps[index].ID == changedStepID {
			baseStep = &base.Steps[index]
			break
		}
	}
	for index := range candidate.Steps {
		if candidate.Steps[index].ID == changedStepID {
			candidateStep = &candidate.Steps[index]
			break
		}
	}
	if baseStep == nil || baseStep.Operation.RelatedExpand == nil {
		return false
	}
	if candidateStep == nil || candidateStep.Operation.RelatedExpand == nil {
		return true
	}
	return baseStep.Operation.RelatedExpand.TargetNodeID != candidateStep.Operation.RelatedExpand.TargetNodeID ||
		baseStep.Operation.RelatedExpand.TargetResourceType != candidateStep.Operation.RelatedExpand.TargetResourceType
}

func constructionStepsToRemoveForMissingInputs(
	steps []authoringv2.ConstructionStep,
	missingInputs []authoringv2.ConstructionDependencyIssue,
	protectedStepID string,
	alreadyRemoved map[string]bool,
) []string {
	stepsByID := make(map[string]authoringv2.ConstructionStep, len(steps))
	for _, step := range steps {
		stepsByID[step.ID] = step
	}
	remove := make([]string, 0, len(missingInputs))
	selected := make(map[string]bool, len(missingInputs))
	for _, missing := range missingInputs {
		if missing.StepID == protectedStepID {
			continue
		}
		step, exists := stepsByID[missing.StepID]
		if !exists {
			continue
		}
		stepID := step.ID
		if step.OwnerStepID != "" {
			stepID = step.OwnerStepID
		}
		if stepID == protectedStepID || alreadyRemoved[stepID] || selected[stepID] {
			continue
		}
		if _, exists := stepsByID[stepID]; !exists {
			continue
		}
		selected[stepID] = true
		remove = append(remove, stepID)
	}
	return remove
}

func constructionImpactForRemovedRelatedExpandAnchors(
	baseStages []explorer.ReceiptConstructionStage,
	candidate *authoringv2.Construction,
	impact authoringv2.ConstructionImpact,
) authoringv2.ConstructionImpact {
	if candidate == nil || len(impact.RemovedStepIDs) == 0 {
		return impact
	}
	removed := make(map[string]bool, len(impact.RemovedStepIDs))
	for _, stepID := range impact.RemovedStepIDs {
		removed[stepID] = true
	}
	removedTerminalIdentities := make(map[string]bool)
	for _, stage := range baseStages {
		if removed[stage.ID] && stage.Operation == string(recipe.ConstructionRelatedExpandOp) && stage.RelatedExpand != nil {
			removedTerminalIdentities[stage.RelatedExpand.TerminalIdentityColumn] = true
		}
	}
	if len(removedTerminalIdentities) == 0 {
		return impact
	}
	for _, step := range candidate.Steps {
		anchorColumnID := ""
		switch step.Operation.Kind {
		case authoringv2.ConstructionOperationRelatedField:
			for _, stage := range baseStages {
				if stage.ID == step.ID && stage.ActiveRelatedRecord != nil &&
					removedTerminalIdentities[stage.ActiveRelatedRecord.TerminalIdentityColumn] {
					anchorColumnID = stage.ActiveRelatedRecord.TerminalIdentityColumn
					break
				}
			}
		case authoringv2.ConstructionOperationRelatedExpand:
			if step.Operation.RelatedExpand != nil {
				anchorColumnID = step.Operation.RelatedExpand.AnchorColumnID
			}
		case authoringv2.ConstructionOperationRelatedEligibility:
			if step.Operation.RelatedEligibility != nil {
				anchorColumnID = step.Operation.RelatedEligibility.AnchorColumnID
			}
		}
		if anchorColumnID == "" || !removedTerminalIdentities[anchorColumnID] {
			continue
		}
		issue := authoringv2.ConstructionDependencyIssue{StepID: step.ID, ColumnID: anchorColumnID}
		alreadyReported := false
		for _, existing := range impact.MissingInputs {
			if existing == issue {
				alreadyReported = true
				break
			}
		}
		if !alreadyReported {
			impact.MissingInputs = append(impact.MissingInputs, issue)
		}
	}
	return impact
}

func reauthorizeConstructionRelatedField(
	ctx context.Context,
	base constructionBase,
	step authoringv2.ConstructionStep,
	related authoringv2.ConstructionRelatedField,
) error {
	inputStageID, err := relatedFieldInputStageID(step)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	var inputStage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == inputStageID {
			inputStage = &base.stages[index]
			break
		}
	}
	if inputStage == nil {
		return conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the related field input stage is not in the current compiled output", nil, nil)
	}
	active := inputStage.ActiveRelatedRecord
	if active == nil {
		return unprocessable("construction-proposal", "NO_ACTIVE_RELATED_RECORD", "the related field input stage does not retain an exact terminal resource identity", nil)
	}
	supported := false
	for _, operation := range inputStage.Capabilities {
		if operation.Kind == "RELATED_FIELD" {
			supported = operation.Supported
			break
		}
	}
	if !supported {
		return unprocessable("construction-proposal", "NO_ACTIVE_RELATED_RECORD", "the related field input stage cannot project an exact terminal field", nil)
	}
	if related.Source.Kind != capability.ConstructionChoiceSourceField || related.Source.NodeID != active.TargetNodeID ||
		related.Source.ResourceType != active.TargetResourceType || len(related.Source.RepeatedBoundaries) != 0 {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related field source does not identify a scalar on the active terminal resource", nil)
	}
	choice, err := capability.DecodeConstructionChoiceID(related.ChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related field choice identity is invalid", err)
	}
	if choice.SnapshotToken != base.snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the exact related field for the current authorized snapshot", nil, nil)
	}
	choiceSource, ok := choice.Source.(capability.RelatedFieldChoiceSource)
	if !ok || choiceSource.StageID != inputStageID || choiceSource.CandidateID != related.Source.CandidateID ||
		choiceSource.NodeID != active.TargetNodeID || choiceSource.ResourceType != active.TargetResourceType ||
		choiceSource.Path != related.Source.Path || choiceSource.Cardinality != related.Source.Cardinality ||
		choiceSource.LogicalType != related.Source.LogicalType {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related field source differs from its stage-bound server choice", nil)
	}
	candidate, found := uniqueCapabilityCandidate(base.snapshot, related.Source.CandidateID)
	if !found || candidate.NodeID != active.TargetNodeID || candidate.ResourceType != active.TargetResourceType ||
		candidate.FieldPath != related.Source.Path || (candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") ||
		len(candidate.RepeatedBoundaries) != 0 || !relatedFieldPathExecutable(candidate.FieldPath) ||
		!relatedFieldLogicalTypeExecutable(candidate.LogicalType) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the exact scalar candidate is no longer available on the active terminal resource", nil, nil)
	}
	proved, err := proveConstructionCandidate(ctx, base.authorized, active.TargetResourceType, candidate, nil)
	if err != nil || proved.NodeID != active.TargetNodeID || proved.ResourceType != active.TargetResourceType ||
		proved.FieldPath != related.Source.Path || proved.Cardinality != related.Source.Cardinality || proved.LogicalType != related.Source.LogicalType ||
		len(proved.RepeatedBoundaries) != 0 || !relatedFieldPathExecutable(proved.FieldPath) ||
		!relatedFieldLogicalTypeExecutable(proved.LogicalType) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected scalar field is no longer supported by the compiler", nil, err)
	}
	reissued, err := capability.NewConstructionRelatedFieldChoice(base.snapshot.Token, inputStageID, proved)
	if err != nil || reissued.ChoiceID != related.ChoiceID || reissued.Source.CandidateID != related.Source.CandidateID ||
		reissued.Source.NodeID != related.Source.NodeID || reissued.Source.ResourceType != related.Source.ResourceType ||
		reissued.Source.Path != related.Source.Path || reissued.Source.Cardinality != related.Source.Cardinality {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the related field choice could not be reissued from the current authorized candidate", err)
	}
	return nil
}

func constructionCodedGroupChoices(
	base constructionBase,
	outputID string,
	stage explorer.ReceiptConstructionStage,
) ([]explorer.ReceiptConstructionCodedGroupChoice, error) {
	if stage.ID != recipe.ConstructionSourceProjectionID || stage.RowIdentityColumn != "_key" ||
		!constructionStageSupportsCodedGroup(stage) {
		return nil, nil
	}
	occurrence, err := resolveRowChoiceOccurrence(
		base.snapshot, base.document.RootResourceType, base.document.Route,
		authoringv2.RootOccurrenceID, "",
	)
	if err != nil {
		return nil, fmt.Errorf("the direct root occurrence is not available for coded grouping: %w", err)
	}
	if len(occurrence.Route) != 0 {
		return nil, fmt.Errorf("the direct root occurrence unexpectedly includes a route")
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, err
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(base.document.RootResourceType))
	if err != nil {
		return nil, err
	}
	choices := make([]explorer.ReceiptConstructionCodedGroupChoice, 0, len(paths))
	for _, path := range paths {
		facts, err := index.ResolveRowPath(fhirschema.DefinitionName(base.document.RootResourceType), path)
		if err != nil || facts.FHIRType != "Coding" || facts.Cardinality != fhirschema.RowCardinalityMany ||
			facts.Shape != fhirschema.RowPathArray || facts.Reference {
			continue
		}
		if !constructionCodedGroupPathPopulated(base.snapshot, occurrence, path) {
			continue
		}
		choice, err := capability.NewConstructionCodedGroupChoice(base.snapshot, outputID, stage.ID, occurrence, capability.RowChoiceFacts{
			ResourceType: string(facts.ResourceType), CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
			Cardinality: capability.RowChoiceMany, Shape: capability.RowChoiceArray, Reference: facts.Reference,
			Title: facts.Title, Description: facts.Description,
		})
		if err != nil {
			return nil, err
		}
		choices = append(choices, explorer.ReceiptConstructionCodedGroupChoice{
			ChoiceID: choice.ChoiceID, OccurrenceID: choice.OccurrenceID, ResourceType: choice.ResourceType,
			CodingPath: choice.CodingPath, Label: choice.Label,
		})
	}
	return choices, nil
}

func constructionCodedGroupPathPopulated(
	snapshot capability.Snapshot,
	occurrence capability.RowChoiceOccurrence,
	codingPath string,
) bool {
	wantPath := codingPath + ".code"
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID == occurrence.NodeID && candidate.ResourceType == occurrence.ResourceType &&
			candidate.FieldPath == wantPath && candidate.Observed && candidate.Populated && candidate.ObservedDocumentCount > 0 {
			return true
		}
	}
	return false
}

func reauthorizeConstructionCodedGroup(
	ctx context.Context,
	base constructionBase,
	outputID string,
	step authoringv2.ConstructionStep,
	coded authoringv2.ConstructionCodedGroup,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireExactIdentity(coded.ChoiceID, "codedGroup.choiceId"); err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "choose a current repeated Coding field before proposing this step", err)
	}
	if len(step.Inputs) != 1 || step.Inputs[0].Kind != authoringv2.ConstructionInputSourceProjection ||
		step.Inputs[0].StepID != "" {
		return unprocessable("construction-proposal", "DIRECT_SOURCE_STAGE_REQUIRED", "coded grouping is currently available only on the direct source projection stage", nil)
	}
	var inputStage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == recipe.ConstructionSourceProjectionID {
			inputStage = &base.stages[index]
			break
		}
	}
	if inputStage == nil || inputStage.RowIdentityColumn != "_key" || !constructionStageSupportsCodedGroup(*inputStage) {
		return unprocessable("construction-proposal", "CODED_GROUP_UNAVAILABLE", "the direct source stage does not expose executable repeated Coding fields", nil)
	}
	if coded.Source.OccurrenceID != authoringv2.RootOccurrenceID || coded.Source.ResourceType != base.document.RootResourceType ||
		coded.Source.FHIRType != "Coding" || coded.Source.Cardinality != "MANY" || coded.Source.Shape != "ARRAY" || len(coded.Source.Route) != 0 {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "coded grouping source facts must identify a root repeated Coding path with an empty route", nil)
	}
	occurrence, err := resolveRowChoiceOccurrence(base.snapshot, base.document.RootResourceType, base.document.Route, coded.Source.OccurrenceID, "")
	if err != nil || len(occurrence.Route) != 0 {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the root Coding occurrence is no longer available in the authorized snapshot", nil, err)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fmt.Errorf("load generated FHIR schema for coded grouping: %w", err)
	}
	paths, err := index.RepeatedCodingPaths(fhirschema.DefinitionName(base.document.RootResourceType))
	if err != nil {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "generated FHIR Coding metadata for the selected root is unavailable", nil, err)
	}
	pathAvailable := false
	for _, path := range paths {
		if path == coded.Source.CodingPath {
			pathAvailable = true
			break
		}
	}
	facts, factErr := index.ResolveRowPath(fhirschema.DefinitionName(base.document.RootResourceType), coded.Source.CodingPath)
	if !pathAvailable || factErr != nil || facts.FHIRType != "Coding" || facts.Cardinality != fhirschema.RowCardinalityMany ||
		facts.Shape != fhirschema.RowPathArray || facts.Reference {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected Coding path is no longer an executable repeated Coding field", nil, factErr)
	}
	identity, err := capability.DecodeConstructionChoiceID(coded.ChoiceID)
	if err != nil || identity.SnapshotToken != base.snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload this Coding field from the current authorized snapshot", nil, err)
	}
	choiceSource, ok := identity.Source.(capability.CodedGroupChoiceSource)
	if !ok || identity.Kind != capability.ConstructionChoiceSourceCodedGroup ||
		choiceSource.OutputID != outputID || choiceSource.StageID != recipe.ConstructionSourceProjectionID ||
		choiceSource.OccurrenceID != coded.Source.OccurrenceID || choiceSource.NodeID != occurrence.NodeID ||
		choiceSource.ResourceType != coded.Source.ResourceType || choiceSource.Path != coded.Source.CodingPath ||
		choiceSource.SchemaDigest != base.snapshot.Identity.SchemaDigest {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "coded grouping source differs from its exact stage-bound server choice", nil)
	}
	proved, err := capability.NewConstructionCodedGroupChoice(base.snapshot, outputID, recipe.ConstructionSourceProjectionID, occurrence, capability.RowChoiceFacts{
		ResourceType: string(facts.ResourceType), CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
		Cardinality: capability.RowChoiceMany, Shape: capability.RowChoiceArray, Reference: facts.Reference,
		Title: facts.Title, Description: facts.Description,
	})
	if err != nil || proved.ChoiceID != coded.ChoiceID || proved.CodingPath != coded.Source.CodingPath ||
		proved.OccurrenceID != coded.Source.OccurrenceID || proved.ResourceType != coded.Source.ResourceType {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "the coded grouping choice cannot be reissued from current generated metadata", err)
	}
	return nil
}

func constructionStageSupportsCodedGroup(stage explorer.ReceiptConstructionStage) bool {
	for _, operation := range stage.Capabilities {
		if operation.Kind == string(recipe.ConstructionCodedGroupOp) {
			return operation.Supported
		}
	}
	return false
}

func relatedFieldInputStageID(step authoringv2.ConstructionStep) (string, error) {
	if len(step.Inputs) != 1 {
		return "", fmt.Errorf("related field requires exactly one input stage")
	}
	input := step.Inputs[0]
	switch input.Kind {
	case authoringv2.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID, nil
	case authoringv2.ConstructionInputStepOutput:
		if strings.TrimSpace(input.StepID) == "" || input.StepID != strings.TrimSpace(input.StepID) {
			return "", fmt.Errorf("related field input step ID must be exact")
		}
		return input.StepID, nil
	default:
		return "", fmt.Errorf("related field input must be a source or prior construction stage")
	}
}

func reauthorizeConstructionRelatedExpand(
	ctx context.Context,
	base constructionBase,
	rootResourceType string,
	step authoringv2.ConstructionStep,
	related authoringv2.ConstructionRelatedExpand,
) error {
	inputStageID, err := relatedExpandInputStageID(step)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	var inputStage *explorer.ReceiptConstructionStage
	for index := range base.stages {
		if base.stages[index].ID == inputStageID {
			inputStage = &base.stages[index]
			break
		}
	}
	if inputStage == nil {
		return conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the related expansion input stage is not in the current compiled output", nil, nil)
	}
	if !constructionStageSupportsRelatedExpand(*inputStage) {
		return unprocessable("construction-proposal", "UNSUPPORTED_CONSTRUCTION_OPERATION", "the related expansion input stage does not support this operation", nil)
	}
	anchor, err := resolveRelatedExpandAnchor(base.snapshot, *inputStage, rootResourceType, related.AnchorColumnID)
	if err != nil {
		return unprocessable("construction-proposal", "NO_SOURCE_ROW_ANCHOR", "the related expansion input stage does not retain the selected exact resource anchor", err)
	}
	choice, err := capability.DecodeConstructionChoiceID(related.ChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion route choice identity is invalid", err)
	}
	if choice.SnapshotToken != base.snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the related expansion route for the current authorized snapshot", nil, nil)
	}
	source, ok := choice.Source.(capability.RelatedResourceChoiceSource)
	if !ok {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion must use an exact related-resource route choice", nil)
	}
	if source.StageID != inputStageID || source.AnchorColumnID != related.AnchorColumnID || source.AnchorKind != anchor.Kind ||
		source.AnchorNodeID != anchor.NodeID || source.AnchorResourceType != anchor.ResourceType ||
		source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
		!reflect.DeepEqual(choice.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion anchor, target, input stage, or route differs from its server-issued choice", nil)
	}
	resolvedRoute, err := reauthorizeConstructionRouteFromAnchor(base.snapshot, anchor.NodeID, related.TargetNodeID, related.Route)
	if err != nil || !reflect.DeepEqual(resolvedRoute, related.Route) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected related-resource route is no longer available", nil, err)
	}
	if !constructionRouteHasCompilerProof(ctx, base.authorized, resolvedRoute) {
		return unprocessable("construction-proposal", "UNSUPPORTED_CONSTRUCTION_ROUTE", "the selected related-resource route is no longer supported by the compiler", nil)
	}
	if related.ContributorRule.Predicate == nil {
		return nil
	}
	return reauthorizeRelatedExpandContributor(ctx, base, anchor.ResourceType, related)
}

func reauthorizeConstructionRelatedEligibility(
	ctx context.Context,
	base constructionBase,
	rootResourceType string,
	step authoringv2.ConstructionStep,
	related authoringv2.ConstructionRelatedEligibility,
) error {
	// RELATED_ELIGIBILITY shares RELATED_EXPAND's signed route and contributor
	// choices. Reuse that exact-stage/anchor reauthorization path; its row-grain
	// policy fields are irrelevant because this operation emits no expanded row.
	return reauthorizeConstructionRelatedExpand(ctx, base, rootResourceType, step, authoringv2.ConstructionRelatedExpand{
		AnchorColumnID: related.AnchorColumnID, ChoiceID: related.ChoiceID,
		TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
		Route: related.Route, ContributorRule: related.ContributorRule,
		ContributorSource: related.ContributorSource, ContributorChoiceID: related.ContributorChoiceID,
		EmptyPolicy:           authoringv2.ConstructionExpandEmptyExclude,
		RelatedRecordColumnID: "__related_eligibility_probe",
	})
}

func relatedExpandInputStageID(step authoringv2.ConstructionStep) (string, error) {
	if len(step.Inputs) != 1 {
		return "", fmt.Errorf("related expansion requires exactly one input stage")
	}
	input := step.Inputs[0]
	switch input.Kind {
	case authoringv2.ConstructionInputSourceProjection:
		return recipe.ConstructionSourceProjectionID, nil
	case authoringv2.ConstructionInputStepOutput:
		if strings.TrimSpace(input.StepID) == "" || input.StepID != strings.TrimSpace(input.StepID) {
			return "", fmt.Errorf("related expansion input step ID must be exact")
		}
		return input.StepID, nil
	default:
		return "", fmt.Errorf("related expansion input must be a source or prior construction stage")
	}
}

func reauthorizeRelatedExpandContributor(ctx context.Context, base constructionBase, rootResourceType string, related authoringv2.ConstructionRelatedExpand) error {
	if related.ContributorSource == nil || related.ContributorChoiceID == "" {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor predicate has no exact field choice", nil)
	}
	identity, err := capability.DecodeConstructionChoiceID(related.ContributorChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor choice identity is invalid", err)
	}
	if identity.SnapshotToken != base.snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the related expansion contributor for the current authorized snapshot", nil, nil)
	}
	source, ok := identity.Source.(capability.FieldChoiceSource)
	if !ok {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor must use an exact field choice", nil)
	}
	authored := related.ContributorSource
	expected := capability.FieldChoiceSource{
		Kind: authored.Kind, CandidateID: authored.CandidateID, NodeID: authored.NodeID, ResourceType: authored.ResourceType,
		Path: authored.Path, Cardinality: authored.Cardinality, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), authored.RepeatedBoundaries...),
	}
	if !reflect.DeepEqual(source, expected) || !reflect.DeepEqual(identity.Route, related.Route) ||
		source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor does not match the exact target route and field", nil)
	}
	form := capability.ConstructionChoiceValue
	if capability.IsRepeatedCardinality(source.Cardinality) {
		candidate, found := uniqueCapabilityCandidate(base.snapshot, source.CandidateID)
		if !found {
			return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the exact related expansion contributor is no longer available", nil, nil)
		}
		choice, choiceErr := capability.NewFieldConstructionChoiceForRoute(base.snapshot.Token, identity.Route, candidate)
		if choiceErr != nil || !reflect.DeepEqual(choice.Source, source) {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor does not match its current field choice", choiceErr)
		}
		foundForm := false
		for _, option := range choice.Options {
			if option.Support == capability.ConstructionChoiceSupported {
				form, foundForm = option.Form, true
				break
			}
		}
		if !foundForm {
			return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor has no compiler-proved field form", nil)
		}
	}
	resolved, _, err := resolveFieldConstructionChoice(ctx, base.authorized, base.snapshot, base.catalog, rootResourceType,
		authoringv2.ConstructionChoiceSelection{ChoiceID: related.ContributorChoiceID, Form: form}, identity, source)
	if err != nil {
		return err
	}
	if resolved.LogicalType != authored.LogicalType || !reflect.DeepEqual(resolved.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion contributor type or route differs from its current compiler-proved choice", nil)
	}
	return nil
}

func reauthorizeConstructionRelatedSource(ctx context.Context, base constructionBase, rootResourceType string, related authoringv2.ConstructionRelatedSource) error {
	identity, err := capability.DecodeConstructionChoiceID(related.ChoiceID)
	if err != nil {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source choice identity is invalid", err)
	}
	if identity.SnapshotToken != base.snapshot.Token {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "reload the related source choice for the current authorized snapshot", nil, nil)
	}
	source, ok := identity.Source.(capability.FieldChoiceSource)
	if !ok {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source must use an exact field choice", nil)
	}
	authoredSource := capability.FieldChoiceSource{
		Kind: related.Source.Kind, CandidateID: related.Source.CandidateID, NodeID: related.Source.NodeID,
		ResourceType: related.Source.ResourceType, Path: related.Source.Path, Cardinality: related.Source.Cardinality,
		RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), related.Source.RepeatedBoundaries...),
	}
	if !reflect.DeepEqual(source, authoredSource) || !reflect.DeepEqual(identity.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source route or field does not match its server-issued choice", nil)
	}
	selection := authoringv2.ConstructionChoiceSelection{ChoiceID: related.ChoiceID, Form: related.Form}
	resolved, _, err := resolveFieldConstructionChoice(ctx, base.authorized, base.snapshot, base.catalog, rootResourceType, selection, identity, source)
	if err != nil {
		return err
	}
	if resolved.LogicalType != related.Source.LogicalType || !reflect.DeepEqual(resolved.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related source type or route differs from the current compiler-proved choice", nil)
	}
	return nil
}

func (s *Service) loadConstructionBase(ctx context.Context, project, explorerID, snapshotToken string, draftVersion int64, draftDigest, outputID string) (constructionBase, error) {
	if s.config.Capability.ForCompilation == nil {
		return constructionBase{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "authorized construction compilation is not configured", nil)
	}
	if s.config.Capability.Catalog == nil {
		return constructionBase{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "construction authoring catalog is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, project, snapshotToken)
	if err != nil {
		return constructionBase{}, conflict("construction-capabilities", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(snapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(project) {
		return constructionBase{}, conflict("construction-capabilities", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return constructionBase{}, conflict("construction-capabilities", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	authorized.Snapshot = snapshot.Clone()
	catalogSnapshot := s.config.Capability.Catalog(snapshot, explorerID)
	owner, err := s.store.Get(ctx, project, explorerID)
	if err != nil {
		return constructionBase{}, err
	}
	if owner == nil || owner.ExplorerID != explorerID || projectid.Canonical(owner.Project) != projectid.Canonical(project) {
		return constructionBase{}, notFound("construction-capabilities", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != draftVersion || owner.DraftDigest != draftDigest {
		return constructionBase{}, conflict("construction-capabilities", "DRAFT_CONFLICT", "the Explorer draft changed; reload before requesting construction capabilities", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return constructionBase{}, conflict("construction-capabilities", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be compiled", nil, err)
	}
	actualDigest, err := workspace.Digest()
	if err != nil || actualDigest != owner.DraftDigest {
		return constructionBase{}, conflict("construction-capabilities", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	documentIndex := constructionDocumentIndex(workspace, outputID)
	if documentIndex < 0 {
		return constructionBase{}, unprocessable("construction-capabilities", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	document := workspace.Documents[documentIndex]
	baseDocumentSHA, err := documentDigest(document)
	if err != nil {
		return constructionBase{}, fmt.Errorf("digest construction base document: %w", err)
	}
	upgraded, err := authoringv2.UpgradeDocumentToConstruction(document)
	if err != nil {
		return constructionBase{}, unprocessable("construction-capabilities", "INVALID_CONSTRUCTION_BASE", err.Error(), err)
	}
	if upgraded.Construction == nil {
		return constructionBase{}, conflict("construction-capabilities", "INVALID_CONSTRUCTION_BASE", "the source projection has no construction contract", nil, nil)
	}
	if len(upgraded.Columns) == 0 && len(upgraded.Construction.Steps) == 0 {
		if s.config.ConstructionSourceStage == nil {
			return constructionBase{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "compiler-resolved source-stage discovery is not configured", nil)
		}
		stage, err := s.config.ConstructionSourceStage(ctx, ConstructionSourceStageRequest{
			Project: project, ExplorerID: explorerID, OutputID: outputID, Document: document,
			Authorized: authorized.Clone(), SelectionMembersCollection: s.config.SelectionMembersCollection,
		})
		if err != nil {
			return constructionBase{}, unprocessable("construction-capabilities", "SOURCE_STAGE_DISCOVERY_FAILED", "the compiler could not resolve the empty source projection", err)
		}
		if stage.ID != recipe.ConstructionSourceProjectionID || stage.RowIdentityColumn == "" || len(stage.Columns) != 0 {
			return constructionBase{}, conflict("construction-capabilities", "INVALID_SOURCE_STAGE_DESCRIPTOR", "the compiler returned an invalid zero-column source-stage descriptor", nil, nil)
		}
		return constructionBase{
			owner: owner, explorerID: explorerID, workspace: workspace, document: document,
			construction: *upgraded.Construction, snapshot: snapshot, authorized: authorized.Clone(), catalog: catalogSnapshot,
			stages: []explorer.ReceiptConstructionStage{stage}, baseDocumentSHA: baseDocumentSHA,
		}, nil
	}
	if s.config.CompileReceipt == nil {
		return constructionBase{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "authorized construction compilation is not configured", nil)
	}
	workspace.Documents[documentIndex] = upgraded
	receipt, err := s.compile(ctx, compileRequest{
		Project: project, ExplorerID: explorerID, Workspace: workspace, SnapshotToken: snapshotToken,
		RequestID: "construction-capabilities",
	})
	if err != nil {
		return constructionBase{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-capabilities", receipt, project, explorerID, snapshotToken, snapshot, &workspace); err != nil {
		return constructionBase{}, err
	}
	if err := validateReceiptOutputContract(receipt, outputID); err != nil {
		return constructionBase{}, unprocessable("construction-capabilities", "OUTPUT_NOT_FOUND", "outputId is not in the compiler output contract", nil)
	}
	if len(receipt.ConstructionStages[outputID]) == 0 {
		return constructionBase{}, conflict("construction-capabilities", "STAGE_CAPABILITIES_UNAVAILABLE", "the compiler receipt has no stage descriptors for this output", nil, nil)
	}
	return constructionBase{
		owner: owner, explorerID: explorerID, workspace: workspace, document: document,
		construction: *upgraded.Construction, snapshot: snapshot, authorized: authorized.Clone(), catalog: catalogSnapshot,
		receipt: receipt, stages: receipt.ConstructionStages[outputID], baseDocumentSHA: baseDocumentSHA,
	}, nil
}

func constructionDocumentIndex(workspace authoringv2.Workspace, outputID string) int {
	index := -1
	for candidate := range workspace.Documents {
		if workspace.Documents[candidate].Output.ID != outputID {
			continue
		}
		if index >= 0 {
			return -1
		}
		index = candidate
	}
	return index
}

func constructionWorkspaceCandidateDigest(workspace authoringv2.Workspace) (string, error) {
	encoded, err := json.Marshal(workspace.NormalizePresentationOrders())
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (s *Service) prepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyConstructionProposal {
		return nil, nil, malformed("commands", "APPLY_CONSTRUCTION_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the proposal ID does not match a candidate receipt", nil, nil)
	}
	binding := receipt.ConstructionProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, nil, err
	}
	if currentDigest != binding.DraftDigest {
		return nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the saved draft no longer matches the proposal base", nil, nil)
	}
	baseIndex := constructionDocumentIndex(current, command.OutputID)
	if baseIndex < 0 {
		return nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the proposal output is missing or duplicated in the saved draft", nil, nil)
	}
	baseDocument := current.Documents[baseIndex]
	baseDocumentDigest, err := documentDigest(baseDocument)
	if err != nil {
		return nil, nil, err
	}
	if baseDocumentDigest != binding.BaseDocumentDigest {
		return nil, nil, conflict("commands", "STALE_CONSTRUCTION_PROPOSAL", "the target table changed after the proposal was compiled", nil, nil)
	}
	candidate, err := s.verifyProposalReceipt(ctx, "construction-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, nil)
	if err != nil {
		return nil, nil, err
	}
	candidateIndex := constructionDocumentIndex(candidate, command.OutputID)
	if candidateIndex < 0 || candidateIndex != baseIndex {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt does not contain the requested output in its saved position", nil, nil)
	}
	candidateDocument := candidate.Documents[candidateIndex]
	if candidateDocument.Construction == nil {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt has no staged construction", nil, nil)
	}
	expectedDocument, impact, err := baseDocument.AnalyzeConstructionCandidate(*candidateDocument.Construction, binding.ChangedStepID, binding.RemoveStepIDs)
	if err != nil || impact.HasMissingInputs() {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate construction no longer resolves against the saved base", nil, err)
	}
	if !sameConstructionDocument(expectedDocument, candidateDocument) {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt changes authoring fields outside the construction proposal", nil, nil)
	}
	expectedWorkspace := current
	expectedWorkspace.Documents = append([]authoringv2.Document(nil), current.Documents...)
	expectedWorkspace.Documents[baseIndex] = expectedDocument
	expectedDigest, err := expectedWorkspace.Digest()
	if err != nil || expectedDigest != binding.CandidateWorkspaceDigest || expectedDigest != receipt.IntentDigest {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate workspace digest does not match the exact proposal", nil, err)
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, &expectedWorkspace); err != nil {
		return nil, nil, err
	}
	if err := command.ResolveConstructionProposal(&candidateDocument); err != nil {
		return nil, nil, conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate construction is invalid", nil, err)
	}
	if _, err := s.Preview(ctx, PreviewRequest{
		Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: command.OutputID,
		Limit: binding.PreviewLimit,
		SinkFactory: func(_ *explorer.CompilationReceipt, _ []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		return nil, nil, conflict("commands", "CONSTRUCTION_PREVIEW_FAILED", "the exact candidate preview failed; the accepted draft remains unchanged", nil, err)
	}
	return commands, receipt, nil
}

func sameConstructionDocument(left, right authoringv2.Document) bool {
	left.Construction, right.Construction = nil, nil
	left.TableShape, right.TableShape = nil, nil
	return reflect.DeepEqual(left, right)
}

func checkConstructionProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	if receipt == nil || receipt.ConstructionProposal == nil {
		return conflict("commands", "INVALID_CONSTRUCTION_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied construction workspace: %w", err)
	}
	if digest != receipt.ConstructionProposal.CandidateWorkspaceDigest || digest != receipt.IntentDigest {
		return conflict("commands", "CONSTRUCTION_PROPOSAL_MISMATCH", "the applied construction does not match the candidate receipt workspace", nil, nil)
	}
	return nil
}
