package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) ApplyCommands(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, actor string) (*authoringv2.ApplyCommandsResponse, error) {
	if err := request.Validate(); err != nil {
		if strings.Contains(err.Error(), "UNSUPPORTED_SEMANTICS_VERSION") {
			return nil, conflict("commands", "UNSUPPORTED_SEMANTICS_VERSION", "the authoring semantics version is unsupported; reload the workspace", nil, err)
		}
		return nil, malformed("commands", err.Error(), err)
	}
	constructionChoiceCommand := request.Commands[0].Type == authoringv2.CommandApplyConstructionChoice
	frameSourceCommand := request.Commands[0].Type == authoringv2.CommandSetFrameSource || request.Commands[0].Type == authoringv2.CommandReplaceFrameSource
	rowDefinitionProposalCommand := request.Commands[0].Type == authoringv2.CommandApplyRowDefinitionProposal
	rowChangeReplacementCommand := request.Commands[0].Type == authoringv2.CommandApplyTableRootRebase && request.Commands[0].RowChange != nil && request.Commands[0].RowChange.Mode == authoringv2.RowChangeModeReplace
	var rowDefinitionCandidate *explorer.CompilationReceipt
	tableShapeProposalCommand := request.Commands[0].Type == authoringv2.CommandApplyTableShapeProposal
	var tableShapeCandidate *explorer.CompilationReceipt
	constructionProposalCommand := request.Commands[0].Type == authoringv2.CommandApplyConstructionProposal
	populationMemberProposalCommand := request.Commands[0].Type == authoringv2.CommandApplyPopulationMemberProposal
	restoreDraftRevisionCommand := request.Commands[0].Type == authoringv2.CommandRestoreDraftRevision
	rowValuePolicyCommand := request.Commands[0].Type == authoringv2.CommandUpdateColumnRowValuePolicy
	for _, command := range request.Commands {
		if command.Type == authoringv2.CommandUpdateColumnRowValuePolicy && len(request.Commands) != 1 {
			return nil, malformed("commands", "UPDATE_COLUMN_ROW_VALUE_POLICY must be the only command in its atomic request", nil)
		}
	}
	var constructionCandidate *explorer.CompilationReceipt
	var populationMemberCandidate *explorer.CompilationReceipt
	var rowChangeReplacementCandidate *explorer.CompilationReceipt
	constructionIdentities := make([]capability.ConstructionChoiceIdentity, len(request.Commands))
	var frameSourceIdentity capability.ConstructionChoiceIdentity
	if frameSourceCommand {
		identity, decodeErr := capability.DecodeConstructionChoiceID(request.Commands[0].FrameChoiceID)
		if decodeErr != nil || identity.Kind != capability.ConstructionChoiceSourceSemanticFrame {
			return nil, malformed("commands", "frameChoiceId is invalid", decodeErr)
		}
		if identity.SnapshotToken != request.SnapshotToken {
			return nil, conflict("commands", "STALE_FRAME_SOURCE", "the frame source belongs to a different catalog snapshot", nil, nil)
		}
		frameSourceIdentity = identity
	}
	populationRouteCount := 0
	for _, command := range request.Commands {
		if command.Type == authoringv2.CommandSetTablePopulation {
			populationRouteCount++
		}
	}
	populationRouteCommand := populationRouteCount > 0
	populationRouteIdentities := make([]capability.PopulationRouteChoiceIdentity, len(request.Commands))
	if populationRouteCommand {
		for index, command := range request.Commands {
			if command.Type != authoringv2.CommandSetTablePopulation || command.RouteChoiceID == "" || len(command.EdgeIDs) != 0 {
				return nil, malformed("commands", "population route choices require only SET_TABLE_POPULATION routeChoiceId commands", nil)
			}
			identity, decodeErr := capability.DecodePopulationRouteChoiceID(command.RouteChoiceID)
			if decodeErr != nil {
				return nil, malformed("commands", fmt.Sprintf("commands[%d].routeChoiceId is invalid", index), decodeErr)
			}
			if identity.SnapshotToken != request.SnapshotToken || identity.OutputID != command.OutputID || identity.SelectionRevisionID != command.SelectionRevisionID {
				return nil, conflict("commands", "STALE_POPULATION_ROUTE", "a population route belongs to another table, selection, or snapshot", nil, nil)
			}
			populationRouteIdentities[index] = identity
		}
		if populationRouteCount != len(request.Commands) {
			return nil, malformed("commands", "population route choices must form the entire atomic request", nil)
		}
	}
	if constructionChoiceCommand {
		for index, command := range request.Commands {
			identity, decodeErr := capability.DecodeConstructionChoiceID(command.ConstructionChoice.ChoiceID)
			if decodeErr != nil {
				return nil, malformed("commands", fmt.Sprintf("commands[%d].constructionChoice.choiceId is invalid", index), decodeErr)
			}
			if identity.SnapshotToken != request.SnapshotToken {
				return nil, conflict("commands", "STALE_CONSTRUCTION_CHOICE", "a construction choice belongs to a different catalog snapshot", nil, nil)
			}
			constructionIdentities[index] = identity
		}
	}
	if s.config.Capability.Catalog == nil {
		return nil, unavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	semanticCommand := request.Commands[0].Type == authoringv2.CommandAddSemanticSelections
	var snapshot capability.Snapshot
	var authorized AuthorizedCapability
	var err error
	if semanticCommand || constructionChoiceCommand || populationRouteCommand || constructionProposalCommand || populationMemberProposalCommand || restoreDraftRevisionCommand || frameSourceCommand || rowValuePolicyCommand || rowChangeReplacementCommand {
		if s.config.Capability.ForCompilation == nil {
			return nil, unavailable("commands", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
		}
		authorized, err = s.config.Capability.ForCompilation(ctx, project, request.SnapshotToken)
		snapshot = authorized.Snapshot
		if err == nil {
			if scopeErr := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); scopeErr != nil {
				return nil, conflict("commands", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, scopeErr)
			}
		}
	} else {
		if s.config.Capability.Token == nil {
			return nil, unavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
		}
		snapshot, err = s.config.Capability.Token(ctx, project, request.SnapshotToken)
	}
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || (constructionChoiceCommand && !constructionChoicesMatchSnapshot(constructionIdentities, snapshot.Token)) {
		return nil, conflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(project) || snapshot.Identity.Generation == "" {
		return nil, conflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if snapshot.Token != "" && snapshot.Identity.Generation != "" && snapshot.Identity.AuthorizationScopeDigest != "" {
		if sourceErr := request.ResolveDraftSourceContext(authoringv2.DraftSourceContext{
			SnapshotToken: snapshot.Token, SourceGeneration: snapshot.Identity.Generation,
			AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		}); sourceErr != nil {
			return nil, conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the authorized source context is incomplete", nil, sourceErr)
		}
	} else if restoreDraftRevisionCommand {
		return nil, conflict("commands", "STALE_DRAFT_SOURCE_CONTEXT", "the authorized source context is incomplete", nil, nil)
	}
	catalog := s.config.Capability.Catalog(snapshot, explorerID)
	var prepare func(context.Context, authoringv2.Workspace, []authoringv2.Command) ([]authoringv2.Command, error)
	if semanticCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareSemanticSelections(ctx, project, explorerID, authorized, workspace, catalog, commands)
		}
	} else if constructionChoiceCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareConstructionChoice(ctx, project, explorerID, authorized, constructionIdentities, workspace, catalog, commands)
		}
	} else if populationRouteCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.preparePopulationRouteChoices(ctx, project, authorized, populationRouteIdentities, workspace, commands)
		}
	} else if rowDefinitionProposalCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, prepareErr := s.prepareRowDefinitionProposal(ctx, project, explorerID, request, snapshot, workspace, commands)
			rowDefinitionCandidate = receipt
			return prepared, prepareErr
		}
	} else if tableShapeProposalCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, prepareErr := s.prepareTableShapeProposal(ctx, project, explorerID, request, snapshot, workspace, commands)
			tableShapeCandidate = receipt
			return prepared, prepareErr
		}
	} else if constructionProposalCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, prepareErr := s.prepareConstructionProposal(ctx, project, explorerID, request, snapshot, workspace, commands)
			constructionCandidate = receipt
			return prepared, prepareErr
		}
	} else if populationMemberProposalCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			prepared, receipt, prepareErr := s.preparePopulationMemberRemovalProposal(ctx, project, explorerID, request, authorized, workspace, commands)
			populationMemberCandidate = receipt
			return prepared, prepareErr
		}
	} else if restoreDraftRevisionCommand {
		prepare = func(ctx context.Context, _ authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			return s.prepareDraftRevisionRestore(ctx, project, explorerID, request, snapshot, commands)
		}
	} else if frameSourceCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			if len(commands) != 1 {
				return nil, malformed("commands", "frame source changes must contain exactly one command", nil)
			}
			return s.prepareFrameSourceCommand(ctx, project, explorerID, authorized, workspace, catalog, commands[0], frameSourceIdentity)
		}
	} else if rowChangeReplacementCommand {
		prepare = func(ctx context.Context, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
			if len(commands) != 1 || commands[0].RowChange == nil {
				return nil, malformed("commands", "row replacement must be the only command", nil)
			}
			proposal := *commands[0].RowChange
			if proposal.CatalogSnapshotToken != request.SnapshotToken || proposal.CatalogGeneration != snapshot.Identity.Generation {
				return nil, conflict("commands", "STALE_CATALOG_SNAPSHOT", "the row replacement belongs to a different catalog snapshot", nil, nil)
			}
			catalogDigest, digestErr := catalog.Digest()
			if digestErr != nil || catalogDigest != proposal.CatalogDigest {
				return nil, conflict("commands", "STALE_CATALOG_SNAPSHOT", "the row replacement catalog changed after assessment", nil, digestErr)
			}
			candidate, _, applyErr := authoringv2.ApplyCommands(workspace, catalog, "row-change-replacement-preview", commands)
			if applyErr != nil {
				return nil, unprocessable("commands", "INVALID_ROW_CHANGE", applyErr.Error(), applyErr)
			}
			receiptWorkspace, projectionErr := rowChangeReceiptWorkspace(candidate, proposal.OutputID)
			if projectionErr != nil {
				return nil, unprocessable("commands", "INVALID_ROW_CHANGE", projectionErr.Error(), projectionErr)
			}
			rowChangeReplacementCandidate, err = s.compile(ctx, compileRequest{Project: project, ExplorerID: explorerID, Workspace: receiptWorkspace, SnapshotToken: request.SnapshotToken, RequestID: "row-change-replacement-apply-candidate"})
			if err != nil {
				return nil, err
			}
			if err := verifyPreviewReceiptIntent(rowChangeReplacementCandidate, receiptWorkspace); err != nil {
				return nil, conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the requested row replacement", nil, err)
			}
			if !receiptHasOutput(rowChangeReplacementCandidate.Bundle, proposal.OutputID) || validateReceiptOutputContract(rowChangeReplacementCandidate, proposal.OutputID) != nil {
				return nil, conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the row replacement candidate receipt does not contain the requested output", nil, nil)
			}
			return commands, nil
		}
	}
	response, err := s.store.ApplyWorkspaceCommandsChecked(ctx, project, explorerID, catalog, request, actor, prepare, func(workspace authoringv2.Workspace) error {
		if _, validationErr := s.resolveWorkspacePopulations(ctx, project, workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest); validationErr != nil {
			return validationErr
		}
		if _, validationErr := s.resolveWorkspaceInterpretations(ctx, project, workspace, snapshot); validationErr != nil {
			return validationErr
		}
		if err := s.validateInterpretationCandidateCommands(ctx, project, explorerID, request, workspace, snapshot); err != nil {
			return err
		}
		if rowDefinitionProposalCommand {
			return s.checkRowDefinitionProposalResult(workspace, rowDefinitionCandidate)
		}
		if tableShapeProposalCommand {
			return checkTableShapeProposalResult(workspace, tableShapeCandidate)
		}
		if constructionProposalCommand {
			return checkConstructionProposalResult(workspace, constructionCandidate)
		}
		if populationMemberProposalCommand {
			if err := checkPopulationMemberRemovalProposalResult(workspace, populationMemberCandidate); err != nil {
				return err
			}
			return previewPopulationMemberRemovalProposal(ctx, s, project, explorerID, populationMemberCandidate)
		}
		if rowChangeReplacementCommand {
			return checkRowChangeReplacementResult(workspace, rowChangeReplacementCandidate, request.Commands[0].RowChange.OutputID)
		}
		if rowValuePolicyCommand {
			return s.validateRowValuePolicyCandidate(ctx, project, explorerID, request, workspace, snapshot, authorized, request.Commands[0])
		}
		return nil
	})
	switch {
	case errors.Is(err, explorer.ErrDraftConflict):
		return nil, conflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before editing", nil, err)
	case errors.Is(err, explorer.ErrAuthoringCommandConflict):
		return nil, conflict("commands", "COMMAND_ID_CONFLICT", "commandId was already used for different intent", nil, err)
	case err != nil:
		var lifecycleErr *Error
		if errors.As(err, &lifecycleErr) {
			return nil, lifecycleErr
		}
		return nil, unprocessable("commands", "INVALID_AUTHORING_COMMAND", err.Error(), err)
	default:
		return response, nil
	}
}

func checkRowChangeReplacementResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt, outputID string) error {
	if receipt == nil || !receiptHasOutput(receipt.Bundle, outputID) || validateReceiptOutputContract(receipt, outputID) != nil {
		return conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the applied row replacement does not match its candidate receipt", nil, nil)
	}
	for _, document := range workspace.Documents {
		if document.Output.ID == outputID {
			if document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.RootResourceType != document.Route.ResourceType || len(document.Columns) != 1 || document.Columns[0].Column != "id" {
				return conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the applied row replacement changed the candidate output shape", nil, nil)
			}
			return nil
		}
	}
	return conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the applied row replacement removed its output", nil, nil)
}

func (s *Service) prepareSemanticSelections(ctx context.Context, project, explorerID string, authorized AuthorizedCapability, workspace authoringv2.Workspace, catalogSnapshot authoringv2.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return nil, unavailable("commands", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandAddSemanticSelections {
		return nil, malformed("commands", "ADD_SEMANTIC_SELECTIONS must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	document := findSemanticOutput(workspace, command.OutputID)
	if document == nil || strings.TrimSpace(document.RootResourceType) == "" || document.Route.ResourceType != document.RootResourceType {
		return nil, malformed("commands", "outputId does not identify a valid row-rooted table", nil)
	}
	rowRootAllowed := false
	for _, node := range authorized.Snapshot.Nodes {
		rowRootAllowed = rowRootAllowed || node.RowRootEligible && node.ResourceType == document.RootResourceType
	}
	if !rowRootAllowed {
		return nil, conflict("commands", "STALE_SEMANTIC_CONTEXT", "the selected row root is not available in the current catalog snapshot", nil, nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	references := make([]catalog.SemanticInventoryReference, 0, len(command.SemanticSelections))
	seen := make(map[string]struct{}, len(command.SemanticSelections))
	for _, selection := range command.SemanticSelections {
		identity := selection.BindingID + "\x00" + selection.ConceptID
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		references = append(references, catalog.SemanticInventoryReference{ConceptID: selection.ConceptID, BindingID: selection.BindingID})
	}
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project:                       projectid.Legacy(authorized.Snapshot.Identity.Project),
		DatasetGeneration:             authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References:                    references,
	})
	if err != nil {
		return nil, unavailable("catalog", "CATALOG_UNAVAILABLE", "the selected semantic inventory could not be resolved", err)
	}
	expectedBuildID := catalog.SemanticInventoryBuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete || resolved.Build.BuildID != expectedBuildID {
		return nil, conflict("catalog", "SEMANTIC_INVENTORY_UNAVAILABLE", "the current generation does not have a complete semantic inventory", nil, nil)
	}
	expectedContext, err := semanticInventoryContextToken(authorized.Snapshot, explorerID, document.RootResourceType, expectedBuildID)
	if err != nil {
		return nil, err
	}
	if command.ContextToken != expectedContext {
		return nil, conflict("catalog", "STALE_SEMANTIC_CONTEXT", "reload the selected concepts before applying them", nil, nil)
	}
	byIdentity := make(map[string]catalog.SemanticInventoryEntry, len(resolved.Entries))
	for _, entry := range resolved.Entries {
		identity := entry.BindingID + "\x00" + entry.ConceptID
		if _, exists := byIdentity[identity]; exists {
			return nil, unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned duplicate selection identities", nil)
		}
		byIdentity[identity] = entry
	}
	for index := range command.SemanticSelections {
		selection := &command.SemanticSelections[index]
		identity := selection.BindingID + "\x00" + selection.ConceptID
		entry, found := byIdentity[identity]
		if !found {
			return nil, unprocessable("commands", "INVALID_SEMANTIC_SELECTION", "a selected concept binding is unavailable in the current authorized inventory", nil)
		}
		selection.ResolvedObservation = &entry
	}
	return commands, nil
}

func findSemanticOutput(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func (s *Service) compile(ctx context.Context, request compileRequest) (*explorer.CompilationReceipt, error) {
	if (s.config.Capability.Token == nil && s.config.Capability.ForCompilation == nil) || s.config.CompileReceipt == nil {
		return nil, unavailable("compile", "CAPABILITY_UNAVAILABLE", "Explorer V2 compiler is not configured", nil)
	}
	authorized, snapshot, err := s.resolveCompilationCapability(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return nil, conflict("capability", "STALE_CATALOG_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": request.SnapshotToken}, err)
	}
	workspace := request.Workspace.NormalizePresentationOrders()
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: s.catalog(snapshot, request.ExplorerID)}).Validate(); err != nil {
		return nil, unprocessable("intent", workspaceValidationCode(err), err.Error(), err)
	}
	if authorized.Scope.Mode != "" {
		if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
			return nil, conflict("population", "INVALID_POPULATION_SCOPE", "the effective authorization scope is stale", nil, err)
		}
	}
	resolvedInputs, err := s.resolveWorkspacePopulations(ctx, request.Project, workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest)
	if err != nil {
		return nil, unprocessable("population", "INVALID_POPULATION", err.Error(), err)
	}
	resolvedInterpretations, err := s.resolveWorkspaceInterpretations(ctx, request.Project, workspace, snapshot)
	if err != nil {
		return nil, unprocessable("interpretation", "INVALID_INTERPRETATION", err.Error(), err)
	}
	resolvedInputs.Interpretations = resolvedInterpretations.Interpretations
	receipt, err := s.config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: request.RequestID, Authorized: authorized, ResolvedInputs: resolvedInputs, SelectionMembersCollection: s.config.SelectionMembersCollection, RowDefinitionProposal: cloneRowDefinitionProposalBinding(request.RowDefinitionProposal), TableShapeProposal: cloneTableShapeProposalBinding(request.TableShapeProposal), ConstructionProposal: cloneConstructionProposalBinding(request.ConstructionProposal), PopulationMemberRemovalProposal: clonePopulationMemberRemovalProposalBinding(request.PopulationMemberRemovalProposal)})
	if err != nil {
		var compileErr *explorercompilation.Error
		if errors.As(err, &compileErr) {
			return nil, &Error{Class: ClassUnprocessable, Stage: compileErr.Stage, Code: compilationErrorCode(compileErr.Code), Message: compileErr.Message, Details: compileErr.Details, Cause: err}
		}
		return nil, err
	}
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" {
		return nil, unavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "compiled authoring receipt was not persisted", nil)
	}
	if err := s.validateCompiledReceipt(ctx, request, authorized, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *Service) validateCompiledReceipt(ctx context.Context, request compileRequest, authorized AuthorizedCapability, receipt *explorer.CompilationReceipt) error {
	if receipt == nil {
		return unprocessable("compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt is required", nil)
	}
	snapshot := authorized.Snapshot
	identityChecks := []struct {
		name string
		got  string
		want string
	}{
		{"project", projectid.Canonical(receipt.Project), projectid.Canonical(request.Project)},
		{"explorerId", receipt.ExplorerID, request.ExplorerID},
		{"snapshotToken", receipt.SnapshotToken, request.SnapshotToken},
		{"sourceGeneration", receipt.SourceGeneration, snapshot.Identity.Generation},
		{"authorizationScopeDigest", receipt.AuthorizationScopeDigest, snapshot.Identity.AuthorizationScopeDigest},
		{"capabilitySchemaDigest", receipt.CapabilitySchemaDigest, snapshot.Identity.SchemaDigest},
		{"shapeDigest", receipt.ShapeDigest, snapshot.Identity.ShapeDigest},
	}
	for _, check := range identityChecks {
		if check.got != check.want {
			return failureDetails(ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt identity does not match the request capability", map[string]any{"field": check.name, "expected": check.want, "actual": check.got}, nil)
		}
	}
	if !sameRowDefinitionProposalBinding(request.RowDefinitionProposal, receipt.RowDefinitionProposal) {
		return failureDetails(ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt row-definition proposal binding does not match the request", nil, nil)
	}
	if !sameTableShapeProposalBinding(request.TableShapeProposal, receipt.TableShapeProposal) {
		return failureDetails(ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt table-shape proposal binding does not match the request", nil, nil)
	}
	if !sameConstructionProposalBinding(request.ConstructionProposal, receipt.ConstructionProposal) {
		return failureDetails(ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt construction proposal binding does not match the request", nil, nil)
	}
	if !samePopulationMemberRemovalProposalBinding(request.PopulationMemberRemovalProposal, receipt.PopulationMemberRemovalProposal) {
		return failureDetails(ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt population member proposal binding does not match the request", nil, nil)
	}
	if authorized.Scope.Mode != "" {
		if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
			return unprocessable("compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt scope is not authorized", err)
		}
	}
	if err := receipt.Validate(); err != nil {
		return failureDetails(ClassUnprocessable, "compile", "INVALID_COMPILATION_RECEIPT", "compiled authoring receipt failed integrity validation", nil, err)
	}
	persisted, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, receipt.ID)
	if err != nil {
		return unavailable("compile", "COMPILATION_RECEIPT_NOT_PERSISTED", "compiled authoring receipt was not found after persistence", err)
	}
	if persisted == nil || persisted.ID != receipt.ID {
		return unavailable("compile", "COMPILATION_RECEIPT_NOT_PERSISTED", "compiled authoring receipt was not returned from persistence", nil)
	}
	if err := persisted.Validate(); err != nil {
		return unavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "persisted compilation receipt failed integrity validation", err)
	}
	return nil
}

func sameRowDefinitionProposalBinding(left, right *explorer.RowDefinitionProposalBinding) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneRowDefinitionProposalBinding(binding *explorer.RowDefinitionProposalBinding) *explorer.RowDefinitionProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func sameTableShapeProposalBinding(left, right *explorer.TableShapeProposalBinding) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneTableShapeProposalBinding(binding *explorer.TableShapeProposalBinding) *explorer.TableShapeProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func clonePopulationMemberRemovalProposalBinding(binding *explorer.PopulationMemberRemovalProposalBinding) *explorer.PopulationMemberRemovalProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func samePopulationMemberRemovalProposalBinding(left, right *explorer.PopulationMemberRemovalProposalBinding) bool {
	return reflect.DeepEqual(clonePopulationMemberRemovalProposalBinding(left), clonePopulationMemberRemovalProposalBinding(right))
}

func cloneConstructionProposalBinding(binding *explorer.ConstructionProposalBinding) *explorer.ConstructionProposalBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	cloned.RemoveStepIDs = append([]string(nil), binding.RemoveStepIDs...)
	return &cloned
}

func sameConstructionProposalBinding(left, right *explorer.ConstructionProposalBinding) bool {
	return reflect.DeepEqual(left, right)
}

func (s *Service) Reconcile(ctx context.Context, request ReconcileRequest) (*explorer.CompilationReceipt, error) {
	result, err := s.ReconcileWithWorkspace(ctx, request)
	if err != nil {
		return nil, err
	}
	return result.Receipt, nil
}

// ReconcileWithWorkspace compiles either the complete saved workspace or one
// output and its current-draft dependency closure, while returning the full
// CAS-validated workspace for Builder clients.
func (s *Service) ReconcileWithWorkspace(ctx context.Context, request ReconcileRequest) (ReconcileResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 0 {
		return ReconcileResult{}, malformed("reconcile", "snapshotToken, draftVersion, and draftDigest are required", nil)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
		return ReconcileResult{}, conflict("reconcile", "DRAFT_CONFLICT", "the Explorer draft changed; reload before reconciling", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return ReconcileResult{}, conflict("reconcile", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be reconciled", nil, err)
	}
	if request.OutputID == "" {
		receipt, compileErr := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken})
		if compileErr != nil {
			return ReconcileResult{}, compileErr
		}
		return ReconcileResult{Receipt: receipt, Workspace: workspace}, nil
	}
	if strings.TrimSpace(request.OutputID) != request.OutputID {
		return ReconcileResult{}, malformed("reconcile", "outputId must not contain surrounding whitespace", nil)
	}
	authorized, snapshot, err := s.resolveCompilationCapability(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return ReconcileResult{}, conflict("reconcile", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if authorized.Scope.Mode != "" {
		if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
			return ReconcileResult{}, conflict("reconcile", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
		}
	}
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: s.catalog(snapshot, request.ExplorerID)}).Validate(); err != nil {
		return ReconcileResult{}, unprocessable("reconcile", workspaceValidationCode(err), err.Error(), err)
	}
	scoped, err := workspaceOutputClosure(workspace, request.OutputID)
	if err != nil {
		return ReconcileResult{}, unprocessable("reconcile", "INVALID_OUTPUT_SCOPE", err.Error(), err)
	}
	receipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: scoped, SnapshotToken: request.SnapshotToken})
	if err != nil {
		return ReconcileResult{}, err
	}
	return ReconcileResult{Receipt: receipt, Workspace: workspace}, nil
}

func workspaceOutputClosure(workspace authoringv2.Workspace, outputID string) (authoringv2.Workspace, error) {
	order, err := workspace.OutputDependencyOrder()
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	indexes := make(map[string]int, len(workspace.Documents))
	for index, document := range workspace.Documents {
		indexes[document.Output.ID] = index
	}
	target, found := indexes[outputID]
	if !found {
		return authoringv2.Workspace{}, fmt.Errorf("output %q was not found in the current workspace", outputID)
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
				if input.Kind == authoringv2.ConstructionInputWorkspaceOutput {
					visit(indexes[input.OutputID])
				}
			}
		}
	}
	visit(target)
	projected := workspace
	projected.Documents = make([]authoringv2.Document, 0, len(include))
	for _, index := range order {
		if _, exists := include[index]; exists {
			projected.Documents = append(projected.Documents, workspace.Documents[index])
		}
	}
	outputIDs := make(map[string]struct{}, len(include))
	for _, document := range projected.Documents {
		outputIDs[document.Output.ID] = struct{}{}
	}
	tabs := make([]authoringv2.Tab, 0, len(include))
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
		projected.SharedFilters = make(map[string][]authoringv2.SharedFilterBinding, len(workspace.SharedFilters))
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

func (s *Service) catalog(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	if s.config.Capability.Catalog == nil {
		return authoringv2.CatalogSnapshot{}
	}
	return s.config.Capability.Catalog(snapshot, explorerID)
}

func (s *Service) resolveCompilationCapability(ctx context.Context, project, token string) (AuthorizedCapability, capability.Snapshot, error) {
	if s.config.Capability.ForCompilation != nil {
		authorized, err := s.config.Capability.ForCompilation(ctx, project, token)
		return authorized, authorized.Snapshot, err
	}
	snapshot, err := s.config.Capability.Token(ctx, project, token)
	return AuthorizedCapability{Snapshot: snapshot}, snapshot, err
}
