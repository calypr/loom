package lifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func (s *Service) ApplyCommands(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, actor string) (*authoringv2.ApplyCommandsResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, malformed("commands", err.Error(), err)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return nil, unavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	snapshot, err := s.config.Capability.Token(ctx, project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return nil, conflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	response, err := s.store.ApplyWorkspaceCommands(ctx, project, explorerID, s.config.Capability.Catalog(snapshot, explorerID), request, actor)
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
	receipt, err := s.config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: request.RequestID, Authorized: authorized})
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
	return receipt, nil
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
