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
	Limit                 int
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
	stages := base.stages
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
	return ConstructionCapabilitiesResponse{
		SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
		OutputID: request.OutputID, StageID: request.StageID, BaseConstruction: base.construction,
		Stages: stages, SelectedStage: *selected,
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
	candidateDocument, impact, err := base.document.AnalyzeConstructionCandidate(request.CandidateConstruction, request.ChangedStepID, request.RemoveStepIDs)
	if err != nil {
		return ConstructionProposalResponse{}, unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CANDIDATE", err.Error(), err)
	}
	baseRelatedSources := make(map[string]authoringv2.ConstructionRelatedSource)
	baseRelatedExpands := make(map[string]authoringv2.ConstructionRelatedExpand)
	for _, step := range base.construction.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationRelatedSource && step.Operation.RelatedSource != nil {
			baseRelatedSources[step.ID] = *step.Operation.RelatedSource
		}
		if step.Operation.Kind == authoringv2.ConstructionOperationRelatedExpand && step.Operation.RelatedExpand != nil {
			baseRelatedExpands[step.ID] = *step.Operation.RelatedExpand
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
		ChangedStepID: request.ChangedStepID, RemoveStepIDs: append([]string(nil), request.RemoveStepIDs...),
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
	stageSupportsRelatedExpand := false
	for _, operation := range inputStage.Capabilities {
		if operation.Kind == "RELATED_EXPAND" {
			stageSupportsRelatedExpand = operation.Supported
			break
		}
	}
	if !stageSupportsRelatedExpand {
		return unprocessable("construction-proposal", "NO_SOURCE_ROW_ANCHOR", "the related expansion input stage does not retain the root resource key", nil)
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
	if source.StageID != inputStageID || source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
		!reflect.DeepEqual(choice.Route, related.Route) {
		return unprocessable("construction-proposal", "INVALID_CONSTRUCTION_CHOICE", "related expansion target, input stage, or route differs from its server-issued choice", nil)
	}
	resolvedRoute, err := reauthorizeConstructionRoute(base.snapshot, rootResourceType, related.TargetNodeID, related.Route)
	if err != nil || !reflect.DeepEqual(resolvedRoute, related.Route) {
		return conflict("construction-proposal", "STALE_CONSTRUCTION_CHOICE", "the selected related-resource route is no longer available", nil, err)
	}
	if !constructionRouteHasCompilerProof(ctx, base.authorized, resolvedRoute) {
		return unprocessable("construction-proposal", "UNSUPPORTED_CONSTRUCTION_ROUTE", "the selected related-resource route is no longer supported by the compiler", nil)
	}
	if related.ContributorRule.Predicate == nil {
		return nil
	}
	return reauthorizeRelatedExpandContributor(ctx, base, rootResourceType, related)
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
	resolved, _, err := resolveFieldConstructionChoice(ctx, base.authorized, base.snapshot, base.catalog, rootResourceType,
		authoringv2.ConstructionChoiceSelection{ChoiceID: related.ContributorChoiceID, Form: capability.ConstructionChoiceValue}, identity, source)
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
			owner: owner, workspace: workspace, document: document,
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
		owner: owner, workspace: workspace, document: document,
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
	if err := command.ResolveConstructionProposal(candidateDocument.Construction); err != nil {
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
