package lifecycle

import (
	"context"
	"errors"
	"strings"

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
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return nil, unavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	snapshot, err := s.config.Capability.Token(ctx, project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return nil, conflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	catalog := s.config.Capability.Catalog(snapshot, explorerID)
	response, err := s.store.ApplyWorkspaceCommandsChecked(ctx, project, explorerID, catalog, request, actor, func(workspace authoringv2.Workspace) error {
		_, validationErr := s.resolveWorkspacePopulations(ctx, project, workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest)
		return validationErr
	})
	switch {
	case errors.Is(err, explorer.ErrDraftConflict):
		return nil, conflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before editing", nil, err)
	case errors.Is(err, explorer.ErrAuthoringCommandConflict):
		return nil, conflict("commands", "COMMAND_ID_CONFLICT", "commandId was already used for different intent", nil, err)
	case err != nil:
		return nil, unprocessable("commands", "INVALID_AUTHORING_COMMAND", err.Error(), err)
	default:
		return response, nil
	}
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
	receipt, err := s.config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: request.RequestID, Authorized: authorized, ResolvedInputs: resolvedInputs, SelectionMembersCollection: s.config.SelectionMembersCollection})
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
