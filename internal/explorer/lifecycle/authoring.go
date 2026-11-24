package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

func (s *Service) ApplyCommands(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, actor string) (*authoringv2.ApplyCommandsResponse, error) {
	response, err := s.applyCommandsWithConstructionPreview(ctx, project, explorerID, request, actor, nil)
	if err != nil {
		return nil, err
	}
	return response.Commands, nil
}

func (s *Service) ApplyBuilderCommands(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, actor string, sinkFactory authoringcore.PreviewSinkFactory) (*ApplyCommandsWithConstructionPreviewResponse, error) {
	if len(request.Commands) != 1 || request.Commands[0].Type != authoringv2.CommandApplyConstructionProposal {
		sinkFactory = nil
	}
	return s.applyCommandsWithConstructionPreview(ctx, project, explorerID, request, actor, sinkFactory)
}

func (s *Service) ApplyCommandsWithConstructionPreview(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, actor string, sinkFactory authoringcore.PreviewSinkFactory) (*ApplyCommandsWithConstructionPreviewResponse, error) {
	if len(request.Commands) != 1 || request.Commands[0].Type != authoringv2.CommandApplyConstructionProposal {
		return nil, support.Malformed("commands", "APPLY_CONSTRUCTION_PROPOSAL must be the only command in its atomic request", nil)
	}
	return s.applyCommandsWithConstructionPreview(ctx, project, explorerID, request, actor, sinkFactory)
}

func (s *Service) applyCommandsWithConstructionPreview(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, actor string, previewSinkFactory authoringcore.PreviewSinkFactory) (*ApplyCommandsWithConstructionPreviewResponse, error) {
	if err := request.Validate(); err != nil {
		if strings.Contains(err.Error(), "UNSUPPORTED_SEMANTICS_VERSION") {
			return nil, support.Conflict("commands", "UNSUPPORTED_SEMANTICS_VERSION", "the authoring semantics version is unsupported; reload the workspace", nil, err)
		}
		return nil, support.Malformed("commands", err.Error(), err)
	}
	intents, err := parseAuthoringCommandIntents(request)
	if err != nil {
		return nil, err
	}
	var rowDefinitionCandidate *receiptmodel.CompilationReceipt
	var constructionCandidate *receiptmodel.CompilationReceipt
	var acceptedConstruction *construction.AcceptedConstruction
	var populationMemberCandidate *receiptmodel.CompilationReceipt
	var rowChangeReplacementCandidate *receiptmodel.CompilationReceipt
	if s.config.Capability.Catalog == nil {
		return nil, support.Unavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	var snapshot capability.Snapshot
	var authorized ports.AuthorizedCapability
	if intents.requiresAuthorizedScope() {
		if s.config.Capability.ForCompilation == nil {
			return nil, support.Unavailable("commands", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
		}
		authorized, err = s.config.Capability.ForCompilation(ctx, project, request.SnapshotToken)
		snapshot = authorized.Snapshot
		if err == nil {
			if scopeErr := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); scopeErr != nil {
				return nil, support.Conflict("commands", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, scopeErr)
			}
		}
	} else {
		if s.config.Capability.Token == nil {
			return nil, support.Unavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
		}
		snapshot, err = s.config.Capability.Token(ctx, project, request.SnapshotToken)
	}
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || (intents.constructionChoiceCommand && !constructionChoicesMatchSnapshot(intents.constructionIdentities, snapshot.Token)) {
		return nil, support.Conflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(project) || snapshot.Identity.Generation == "" {
		return nil, support.Conflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if snapshot.Token != "" && snapshot.Identity.Generation != "" && snapshot.Identity.AuthorizationScopeDigest != "" {
		if sourceErr := request.ResolveDraftSourceContext(authoringv2.DraftSourceContext{
			SnapshotToken: snapshot.Token, SourceGeneration: snapshot.Identity.Generation,
			AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		}); sourceErr != nil {
			return nil, support.Conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the authorized source context is incomplete", nil, sourceErr)
		}
	} else if intents.restoreDraftRevisionCommand {
		return nil, support.Conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the authorized source context is incomplete", nil, nil)
	}
	catalog := s.config.Capability.Catalog(snapshot, explorerID)
	if explorerID == DefaultDataframeID {
		if err := s.seedDefaultDataframeForApply(ctx, project, explorerID, snapshot, catalog, request, actor); err != nil {
			return nil, err
		}
	}
	var prepare func(context.Context, ws.Workspace, []authoringv2.Command) ([]authoringv2.Command, error)
	if intents.columnPolicyProposalCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareColumnPolicyProposal(ctx, project, explorerID, request, authorized, workspace, commands)
		}
	} else if intents.projectedFieldRemovalCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareProjectedFieldRemoval(ctx, project, explorerID, request, authorized, workspace, catalog, commands)
		}
	} else if intents.importedSourceCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			if commands[0].Type == authoringv2.CommandApplyImportedRelationshipProposal {
				return s.prepareImportedRelationshipProposal(ctx, project, explorerID, request, authorized, workspace, commands)
			}
			return s.prepareImportedSource(ctx, project, explorerID, authorized, workspace, catalog, commands)
		}
	} else if intents.semanticCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareSemanticSelections(ctx, project, explorerID, authorized, workspace, catalog, commands)
		}
	} else if intents.constructionChoiceCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareConstructionChoice(ctx, project, explorerID, authorized, intents.constructionIdentities, workspace, catalog, commands)
		}
	} else if intents.populationRouteCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.preparePopulationRouteChoices(ctx, project, authorized, intents.populationRouteIdentities, workspace, commands)
		}
	} else if intents.rowDefinitionProposalCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, prepareErr := s.rowDefService.PrepareRowDefinitionProposal(ctx, project, explorerID, request, snapshot, workspace, commands)
			rowDefinitionCandidate = receipt
			return prepared, prepareErr
		}
	} else if intents.constructionProposalCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, accepted, prepareErr := s.prepareConstructionProposal(ctx, project, explorerID, request, snapshot, workspace, commands, previewSinkFactory)
			constructionCandidate = receipt
			acceptedConstruction = accepted
			return prepared, prepareErr
		}
	} else if intents.populationMemberProposalCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, prepareErr := s.preparePopulationMemberRemovalProposal(ctx, project, explorerID, request, authorized, workspace, commands)
			populationMemberCandidate = receipt
			return prepared, prepareErr
		}
	} else if intents.restoreDraftRevisionCommand {
		prepare = func(ctx context.Context, _ ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareDraftRevisionRestore(ctx, project, explorerID, request, snapshot, commands)
		}
	} else if intents.frameSourceCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			if len(commands) != 1 {
				return nil, support.Malformed("commands", "frame source changes must contain exactly one command", nil)
			}
			return s.prepareFrameSourceCommand(ctx, project, explorerID, authorized, workspace, catalog, commands[0], intents.frameSourceIdentity)
		}
	} else if intents.rowChangeReplacementCommand {
		prepare = func(ctx context.Context, workspace ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			if len(commands) != 1 || commands[0].RowChange() == nil {
				return nil, support.Malformed("commands", "row replacement must be the only command", nil)
			}
			proposal := *commands[0].RowChange()
			if proposal.CatalogSnapshotToken != request.SnapshotToken || proposal.CatalogGeneration != snapshot.Identity.Generation {
				return nil, support.Conflict("commands", "STALE_CATALOG_SNAPSHOT", "the row replacement belongs to a different catalog snapshot", nil, nil)
			}
			catalogDigest, digestErr := catalog.Digest()
			if digestErr != nil || catalogDigest != proposal.CatalogDigest {
				return nil, support.Conflict("commands", "STALE_CATALOG_SNAPSHOT", "the row replacement catalog changed after assessment", nil, digestErr)
			}
			candidate, _, applyErr := authoringv2.ApplyCommands(workspace, catalog, "row-change-replacement-preview", commands)
			if applyErr != nil {
				return nil, support.Unprocessable("commands", "INVALID_ROW_CHANGE", applyErr.Error(), applyErr)
			}
			receiptWorkspace, projectionErr := rowChangeReceiptWorkspace(candidate, proposal.OutputID)
			if projectionErr != nil {
				return nil, support.Unprocessable("commands", "INVALID_ROW_CHANGE", projectionErr.Error(), projectionErr)
			}
			rowChangeReplacementCandidate, err = s.core.Compile(ctx, authoringcore.CompileRequest{Project: project, ExplorerID: explorerID, Workspace: receiptWorkspace, SnapshotToken: request.SnapshotToken, RequestID: "row-change-replacement-apply-candidate"})
			if err != nil {
				return nil, err
			}
			if err := verifyPreviewReceiptIntent(rowChangeReplacementCandidate, receiptWorkspace); err != nil {
				return nil, support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the requested row replacement", nil, err)
			}
			if !support.ReceiptHasOutput(rowChangeReplacementCandidate.Bundle, proposal.OutputID) || support.ValidateReceiptOutputContract(rowChangeReplacementCandidate, proposal.OutputID) != nil {
				return nil, support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the row replacement candidate receipt does not contain the requested output", nil, nil)
			}
			return commands, nil
		}
	}
	response, err := s.store.ApplyWorkspaceCommandsChecked(ctx, project, explorerID, catalog, request, actor, prepare, func(workspace ws.Workspace) error {
		if _, validationErr := s.resolveWorkspacePopulations(ctx, project, workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest); validationErr != nil {
			return validationErr
		}
		if _, validationErr := s.core.ResolveWorkspaceInterpretations(ctx, project, workspace, snapshot); validationErr != nil {
			return validationErr
		}
		if err := s.validateInterpretationCandidateCommands(ctx, project, explorerID, request, workspace, snapshot); err != nil {
			return err
		}
		if intents.rowDefinitionProposalCommand {
			return checkRowDefinitionProposalResult(workspace, rowDefinitionCandidate)
		}
		if intents.constructionProposalCommand {
			return checkConstructionProposalResult(workspace, constructionCandidate)
		}
		if intents.populationMemberProposalCommand {
			if err := checkPopulationMemberRemovalProposalResult(workspace, populationMemberCandidate); err != nil {
				return err
			}
			return previewPopulationMemberRemovalProposal(ctx, s, project, explorerID, populationMemberCandidate)
		}
		if intents.rowChangeReplacementCommand {
			return checkRowChangeReplacementResult(workspace, rowChangeReplacementCandidate, request.Commands[0].RowChange().OutputID)
		}
		if intents.rowValuePolicyCommand {
			return s.validateRowValuePolicyCandidate(ctx, project, explorerID, request, workspace, snapshot, authorized, request.Commands[0])
		}
		return nil
	})
	switch {
	case errors.Is(err, explorer.ErrDraftConflict):
		return nil, support.Conflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before editing", nil, err)
	case errors.Is(err, explorer.ErrAuthoringCommandConflict):
		return nil, support.Conflict("commands", "COMMAND_ID_CONFLICT", "commandId was already used for different intent", nil, err)
	case err != nil:
		var lifecycleErr *ports.Error
		if errors.As(err, &lifecycleErr) {
			return nil, lifecycleErr
		}
		return nil, support.Unprocessable("commands", "INVALID_AUTHORING_COMMAND", err.Error(), err)
	default:
		return &ApplyCommandsWithConstructionPreviewResponse{Commands: response, Accepted: acceptedConstruction}, nil
	}
}

func (s *Service) seedDefaultDataframeForApply(ctx context.Context, project, explorerID string, snapshot capability.Snapshot, catalog ws.CatalogSnapshot, request authoringv2.ApplyCommandsRequest, actor string) error {
	_, err := s.store.Get(ctx, project, explorerID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, explorer.ErrNotFound) {
		return err
	}
	if request.ExpectedDraftVersion != 1 || strings.TrimSpace(request.ExpectedDraftDigest) == "" {
		return support.Conflict("commands", "DRAFT_CONFLICT", "the default dataframe draft changed; reload before applying commands", nil, explorer.ErrDraftConflict)
	}
	virtual, err := s.core.VirtualDefaultDataframeOwner(ctx, project, snapshot, catalog)
	if err != nil {
		return err
	}
	if request.ExpectedDraftDigest != virtual.DraftDigest {
		return support.Conflict("commands", "DRAFT_CONFLICT", "the default dataframe changed; reload before applying commands", nil, explorer.ErrDraftConflict)
	}
	workspace, err := ws.DecodeWorkspace(virtual.DraftConfig)
	if err != nil {
		return support.Conflict("commands", "DEFAULT_DATAFRAME_INVALID", "the imported default dataframe workspace is invalid", nil, err)
	}
	created, createErr := s.store.CreateSeededDataframe(ctx, project, explorerID, virtual.Title, workspace, actor, authoringv2.DraftSourceContext{SnapshotToken: snapshot.Token, SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest})
	if createErr == nil {
		if created.DraftVersion != 1 || created.DraftDigest != virtual.DraftDigest {
			return support.Conflict("commands", "DEFAULT_DATAFRAME_SEED_CONFLICT", "the default dataframe seed differs from its imported workspace", nil, explorer.ErrDraftConflict)
		}
		return nil
	}
	if !errors.Is(createErr, explorer.ErrDraftConflict) {
		return createErr
	}

	created, err = s.store.Get(ctx, project, explorerID)
	if err != nil {
		return err
	}
	if created.DraftVersion != 1 || created.DraftDigest != virtual.DraftDigest || !bytes.Equal(created.DraftConfig, virtual.DraftConfig) {
		return support.Conflict("commands", "DRAFT_CONFLICT", "the default dataframe draft changed; reload before applying commands", nil, explorer.ErrDraftConflict)
	}
	return nil
}

func checkRowChangeReplacementResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt, outputID string) error {
	if receipt == nil || !support.ReceiptHasOutput(receipt.Bundle, outputID) || support.ValidateReceiptOutputContract(receipt, outputID) != nil {
		return support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the applied row replacement does not match its candidate receipt", nil, nil)
	}
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID {
			if document.Route.OccurrenceID != ws.RootOccurrenceID || document.RootResourceType != document.Route.ResourceType || len(document.Columns) != 1 || document.Columns[0].Column != "id" {
				return support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the applied row replacement changed the candidate output shape", nil, nil)
			}
			return nil
		}
	}
	return support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the applied row replacement removed its output", nil, nil)
}

func (s *Service) prepareSemanticSelections(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, workspace ws.Workspace, catalogSnapshot ws.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return nil, support.Unavailable("commands", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandAddSemanticSelections {
		return nil, support.Malformed("commands", "ADD_SEMANTIC_SELECTIONS must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	document := findSemanticOutput(workspace, command.OutputID)
	if document == nil || strings.TrimSpace(document.RootResourceType) == "" || document.Route.ResourceType != document.RootResourceType {
		return nil, support.Malformed("commands", "outputId does not identify a valid row-rooted table", nil)
	}
	rowRootAllowed := false
	for _, node := range authorized.Snapshot.Nodes {
		rowRootAllowed = rowRootAllowed || node.RowRootEligible && node.ResourceType == document.RootResourceType
	}
	if !rowRootAllowed {
		return nil, support.Conflict("commands", "STALE_SEMANTIC_CONTEXT", "the selected row root is not available in the current catalog snapshot", nil, nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	references := make([]semantic.Reference, 0, len(command.SemanticSelections()))
	seen := make(map[string]struct{}, len(command.SemanticSelections()))
	for _, selection := range command.SemanticSelections() {
		identity := selection.BindingID + "\x00" + selection.ConceptID
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		references = append(references, semantic.Reference{ConceptID: selection.ConceptID, BindingID: selection.BindingID})
	}
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project:                       projectid.Legacy(authorized.Snapshot.Identity.Project),
		DatasetGeneration:             authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References:                    references,
	})
	if err != nil {
		return nil, support.Unavailable("catalog", "CATALOG_UNAVAILABLE", "the selected semantic inventory could not be resolved", err)
	}
	expectedBuildID := semantic.BuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete || resolved.Build.BuildID != expectedBuildID {
		return nil, support.Conflict("catalog", "SEMANTIC_INVENTORY_UNAVAILABLE", "the current generation does not have a complete semantic inventory", nil, nil)
	}
	expectedContext, err := semanticbrowse.SemanticInventoryContextToken(authorized.Snapshot, explorerID, document.RootResourceType, expectedBuildID)
	if err != nil {
		return nil, err
	}
	if command.ContextToken() != expectedContext {
		return nil, support.Conflict("catalog", "STALE_SEMANTIC_CONTEXT", "reload the selected concepts before applying them", nil, nil)
	}
	byIdentity := make(map[string]semantic.Entry, len(resolved.Entries))
	for _, entry := range resolved.Entries {
		identity := entry.BindingID + "\x00" + entry.ConceptID
		if _, exists := byIdentity[identity]; exists {
			return nil, support.Unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned duplicate selection identities", nil)
		}
		byIdentity[identity] = entry
	}
	semanticPayload, ok := command.Payload.(*authoringv2.SemanticPayload)
	if !ok {
		return nil, support.Unprocessable("commands", "INVALID_SEMANTIC_SELECTION", "semantic selections require a semantic command payload", nil)
	}
	for index := range semanticPayload.SemanticSelections {
		selection := &semanticPayload.SemanticSelections[index]
		identity := selection.BindingID + "\x00" + selection.ConceptID
		entry, found := byIdentity[identity]
		if !found {
			return nil, support.Unprocessable("commands", "INVALID_SEMANTIC_SELECTION", "a selected concept binding is unavailable in the current authorized inventory", nil)
		}
		selection.ResolvedObservation = &entry
	}
	return commands, nil
}

func findSemanticOutput(workspace ws.Workspace, outputID string) *ws.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func (s *Service) Reconcile(ctx context.Context, request ReconcileRequest) (*receiptmodel.CompilationReceipt, error) {
	result, err := s.ReconcileWithWorkspace(ctx, request)
	if err != nil {
		return nil, err
	}
	return result.Receipt, nil
}

func (s *Service) ReconcileWithWorkspace(ctx context.Context, request ReconcileRequest) (ReconcileResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 0 {
		return ReconcileResult{}, support.Malformed("reconcile", "snapshotToken, draftVersion, and draftDigest are required", nil)
	}
	owner, err := s.core.GetOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
		return ReconcileResult{}, support.Conflict("reconcile", "DRAFT_CONFLICT", "the Explorer draft changed; reload before reconciling", nil, explorer.ErrDraftConflict)
	}
	workspace, err := ws.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return ReconcileResult{}, support.Conflict("reconcile", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be reconciled", nil, err)
	}
	if request.OutputID == "" {
		receipt, compileErr := s.core.Compile(ctx, authoringcore.CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken})
		if compileErr != nil {
			return ReconcileResult{}, compileErr
		}
		return ReconcileResult{Receipt: receipt, Workspace: workspace}, nil
	}
	if strings.TrimSpace(request.OutputID) != request.OutputID {
		return ReconcileResult{}, support.Malformed("reconcile", "outputId must not contain surrounding whitespace", nil)
	}
	authorized, snapshot, err := s.core.ResolveCompilationCapability(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return ReconcileResult{}, support.Conflict("reconcile", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if authorized.Scope.Mode != "" {
		if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
			return ReconcileResult{}, support.Conflict("reconcile", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
		}
	}
	if err := (ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &workspace, Catalog: s.core.Catalog(snapshot, request.ExplorerID)}).Validate(); err != nil {
		return ReconcileResult{}, support.Unprocessable("reconcile", authoringcore.WorkspaceValidationCode(err), err.Error(), err)
	}
	scoped, err := workspaceOutputClosure(workspace, request.OutputID)
	if err != nil {
		return ReconcileResult{}, support.Unprocessable("reconcile", "INVALID_OUTPUT_SCOPE", err.Error(), err)
	}
	receipt, err := s.core.CompileAuthorized(ctx, authoringcore.CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: scoped, SnapshotToken: request.SnapshotToken}, authorized)
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{Receipt: receipt, Workspace: workspace}, nil
}

func workspaceOutputClosure(workspace ws.Workspace, outputID string) (ws.Workspace, error) {
	order, err := workspace.OutputDependencyOrder()
	if err != nil {
		return ws.Workspace{}, err
	}
	indexes := make(map[string]int, len(workspace.Documents))
	for index, document := range workspace.Documents {
		indexes[document.Output.ID] = index
	}
	target, found := indexes[outputID]
	if !found {
		return ws.Workspace{}, fmt.Errorf("output %q was not found in the current workspace", outputID)
	}
	include := map[int]struct{}{}
	var visit func(int)
	visit = func(index int) {
		if _, exists := include[index]; exists {
			return
		}
		include[index] = struct{}{}
		document := workspace.Documents[index]
		if document.Construction == nil {
			return
		}
		for _, step := range document.Construction.Steps {
			for _, input := range step.Inputs {
				if input.Kind == ws.ConstructionInputWorkspaceOutput {
					visit(indexes[input.OutputID])
				}
			}
		}
	}
	visit(target)
	projected := workspace
	projected.Documents = make([]ws.Document, 0, len(include))
	for _, index := range order {
		if _, exists := include[index]; exists {
			projected.Documents = append(projected.Documents, workspace.Documents[index])
		}
	}
	outputIDs := make(map[string]struct{}, len(include))
	for _, document := range projected.Documents {
		outputIDs[document.Output.ID] = struct{}{}
	}
	tabs := make([]ws.Tab, 0, len(include))
	for _, tab := range workspace.Tabs {
		if _, exists := outputIDs[tab.OutputID]; exists {
			tab.Order = len(tabs)
			tabs = append(tabs, tab)
		}
	}
	projected.Tabs = tabs
	if workspace.SharedFilters == nil {
		projected.SharedFilters = nil
	} else {
		projected.SharedFilters = make(map[string][]ws.SharedFilterBinding, len(workspace.SharedFilters))
		for name, bindings := range workspace.SharedFilters {
			for _, binding := range bindings {
				if _, exists := outputIDs[binding.OutputID]; exists {
					projected.SharedFilters[name] = append(projected.SharedFilters[name], binding)
				}
			}
			if len(projected.SharedFilters[name]) == 0 {
				delete(projected.SharedFilters, name)
			}
		}
	}
	return projected, nil
}

func (s *Service) prepareDraftRevisionRestore(
	ctx context.Context,
	project string,
	explorerID string,
	request authoringv2.ApplyCommandsRequest,
	snapshot capability.Snapshot,
	commands []authoringv2.Command,
) ([]authoringv2.Command, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandRestoreDraftRevision {
		return nil, support.Malformed("commands", "RESTORE_DRAFT_REVISION must be the only command", nil)
	}
	command := &commands[0]
	owner, err := s.core.GetOwner(ctx, project, explorerID)
	if err != nil {
		return nil, support.PersistenceFailure("commands", "the Explorer draft could not be loaded before restoring", err)
	}
	if owner == nil || owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return nil, support.Conflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before restoring", nil, explorer.ErrDraftConflict)
	}
	if owner.PreviousDraftRevisionID == "" || command.DraftRevisionID() != owner.PreviousDraftRevisionID {
		return nil, support.Conflict("commands", "STALE_DRAFT_REVISION", "the requested draft revision is no longer the current Undo target", nil, nil)
	}
	revision, err := s.store.GetDraftRevision(ctx, project, explorerID, command.DraftRevisionID())
	if errors.Is(err, explorer.ErrNotFound) {
		return nil, support.Conflict("commands", "STALE_DRAFT_REVISION", "the requested draft revision is unavailable", nil, err)
	}
	if err != nil {
		return nil, support.PersistenceFailure("commands", "the prior draft revision could not be read", err)
	}
	if revision == nil || revision.ID != command.DraftRevisionID() || projectid.Canonical(revision.Project) != projectid.Canonical(project) ||
		revision.ExplorerID != explorerID || revision.DraftVersion != owner.DraftVersion-1 {
		return nil, support.Conflict("commands", "STALE_DRAFT_REVISION", "the requested draft revision does not belong to this Explorer draft", nil, nil)
	}
	currentSource := request.ResolvedDraftSourceContext()
	if currentSource == nil || currentSource.SnapshotToken != snapshot.Token || currentSource.SourceGeneration != snapshot.Identity.Generation ||
		currentSource.AuthorizationScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		return nil, support.Conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the authorized source context changed; reload before restoring", nil, nil)
	}

	var restored ws.Workspace
	if len(revision.DraftConfig) != 0 {
		if revision.SnapshotToken != currentSource.SnapshotToken || revision.SourceGeneration != currentSource.SourceGeneration ||
			revision.AuthorizationScopeDigest != currentSource.AuthorizationScopeDigest {
			return nil, support.Conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the prior draft was created from another authorized source context", nil, nil)
		}
		var expectedDigest string
		restored, expectedDigest, err = s.store.DraftRevisionWorkspace(ctx, *revision)
		if err != nil {
			return nil, support.Conflict("commands", "DRAFT_REVISION_INVALID", "the prior draft revision is invalid", nil, err)
		}
		digest, digestErr := restored.Digest()
		if digestErr != nil || revision.DraftDigest == "" || digest != expectedDigest {
			return nil, support.Conflict("commands", "DRAFT_REVISION_INVALID", "the prior draft revision failed its digest check", nil, digestErr)
		}
	} else {

		if owner.ActiveRevisionID != "" {
			active, activeErr := s.store.ActiveRevision(ctx, project, explorerID)
			if activeErr != nil {
				return nil, support.PersistenceFailure("commands", "the prior workspace could not be loaded", activeErr)
			}
			if active.SourceGeneration != currentSource.SourceGeneration {
				return nil, support.Conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the active source generation changed since the prior workspace", nil, nil)
			}
			restored, err = s.store.RevisionWorkspace(ctx, *active)
			if err != nil {
				return nil, support.Conflict("commands", "DRAFT_REVISION_INVALID", "the prior workspace is invalid", nil, err)
			}
		} else {
			restored = ws.NewWorkspace(revision.Title)
		}
	}
	if err := command.ResolveDraftRevision(restored); err != nil {
		return nil, support.Malformed("commands", err.Error(), err)
	}
	return commands, nil
}
