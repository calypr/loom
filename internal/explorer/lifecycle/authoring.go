package lifecycle

import (
	"context"
	"errors"
	"fmt"
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
	rowDefinitionProposalCommand := request.Commands[0].Type == authoringv2.CommandApplyRowDefinitionProposal
	var rowDefinitionCandidate *explorer.CompilationReceipt
	tableShapeProposalCommand := request.Commands[0].Type == authoringv2.CommandApplyTableShapeProposal
	var tableShapeCandidate *explorer.CompilationReceipt
	constructionIdentities := make([]capability.ConstructionChoiceIdentity, len(request.Commands))
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
	if semanticCommand || constructionChoiceCommand || populationRouteCommand {
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
	receipt, err := s.config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: request.RequestID, Authorized: authorized, ResolvedInputs: resolvedInputs, SelectionMembersCollection: s.config.SelectionMembersCollection, RowDefinitionProposal: cloneRowDefinitionProposalBinding(request.RowDefinitionProposal), TableShapeProposal: cloneTableShapeProposalBinding(request.TableShapeProposal)})
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

func (s *Service) Reconcile(ctx context.Context, request ReconcileRequest) (*explorer.CompilationReceipt, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 0 {
		return nil, malformed("reconcile", "snapshotToken, draftVersion, and draftDigest are required", nil)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return nil, err
	}
	if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
		return nil, conflict("reconcile", "DRAFT_CONFLICT", "the Explorer draft changed; reload before reconciling", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return nil, conflict("reconcile", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be reconciled", nil, err)
	}
	return s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken})
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
