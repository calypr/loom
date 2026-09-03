package lifecycle

import (
	"context"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) Publish(ctx context.Context, request PublishRequest) (PublishResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" {
		return PublishResult{}, malformed("publish", "receiptId is required", nil)
	}
	if s.config.MaterializeReceipt == nil || s.config.PrepareRelease == nil || s.config.ValidateReleaseGeneration == nil || (s.config.Capability.Token == nil && s.config.Capability.ForExecution == nil) {
		return PublishResult{}, unavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured", nil)
	}
	receipt, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return PublishResult{}, err
	}
	if err := s.validateReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return PublishResult{}, err
	}
	authorized, snapshot, err := s.resolveExecutionCapability(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil || strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return PublishResult{}, conflict("publish", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if s.config.CompileReceipt != nil {
		if err := validateReceiptCapability(receipt, snapshot); err != nil {
			return PublishResult{}, conflict("publish", "RECEIPT_STALE", err.Error(), nil, err)
		}
	}
	if len(receipt.EmittedColumns) == 0 || len(receipt.CompiledConfig) == 0 {
		return PublishResult{}, unprocessable("publish", "NO_SELECTED_COLUMNS", "select at least one output column before publishing", nil)
	}
	if s.config.CompileReceipt != nil {
		workspace, decodeErr := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if decodeErr != nil {
			return PublishResult{}, conflict("publish", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt contains invalid authoring intent", nil, decodeErr)
		}
		if err := workspace.ValidateForPublication(); err != nil {
			return PublishResult{}, unprocessable("publish", "NO_SELECTED_COLUMNS", "select at least one visible output column for every visible table before publishing", err)
		}
	}
	if err := s.config.ValidateReleaseGeneration(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {
		return PublishResult{}, conflict("publish", "RECEIPT_STALE", "the receipt generation is no longer active", map[string]any{"generation": receipt.SourceGeneration}, err)
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration}
	applyAuthorizedScope(&bindings, authorized, true)
	execution, err := s.config.MaterializeReceipt(ctx, receipt, bindings)
	if err != nil {
		if resourceErr := materializationResourceError("materialize", err); resourceErr != nil {
			return PublishResult{}, resourceErr
		}
		if userErr, ok := dataframeerrors.AsUserError(err); ok {
			switch userErr.Code() {
			case string(dataframeerrors.CodePublicationInProgress):
				details := userErr.Details()
				if details == nil {
					details = make(map[string]any)
				}
				details["retryable"] = userErr.Retryable()
				return PublishResult{}, conflict("materialize", userErr.Code(), "Explorer publication is already in progress; retry after it completes", details, err)
			}
		}
		return PublishResult{}, unavailable("materialize", "MATERIALIZATION_FAILED", "Explorer materialization failed; the active revision was retained", err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return PublishResult{}, unavailable("materialize", "MATERIALIZATION_FAILED", "materialization did not produce queryable outputs", err)
	}
	release, expectedReleaseRevision, err := s.config.PrepareRelease(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle))
	if err != nil {
		return PublishResult{}, unavailable("activation", "MATERIALIZATION_ACTIVATION_FAILED", "dataset release preparation failed; the prior Explorer revision was retained", err)
	}
	now := s.now()
	revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
	revisionValue := explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: receipt.PublicOutputContract, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: materializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, Status: explorer.RevisionReady, CreatedBy: request.Actor, CreatedAt: now, ReadyAt: &now}
	revision, err := s.store.PublishAuthoring(ctx, *receipt, revisionValue, release, expectedReleaseRevision)
	if err != nil {
		return PublishResult{}, err
	}
	if s.config.PersistPublishedWorkspace != nil {
		if err := s.config.PersistPublishedWorkspace(ctx, receipt.Project, receipt.ExplorerID, receipt.NormalizedBundle); err != nil {
			return PublishResult{}, unavailable("local_writeback", "LOCAL_WORKSPACE_WRITEBACK_FAILED", "Explorer published, but the local CONFIG workspace could not be updated", err)
		}
	}
	return PublishResult{Receipt: receipt, Revision: revision, Execution: execution}, nil
}

func materializationResourceError(stage string, err error) error {
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		return nil
	}
	var message string
	switch userErr.Code() {
	case string(dataframeerrors.CodeQueryMemoryLimitExceeded):
		message = "Explorer materialization exceeded the ArangoDB query memory limit. Reduce the Explorer query complexity or ask an operator to increase ArangoDB's query-memory-limit, then publish again. The active revision was retained."
	case string(dataframeerrors.CodeQueryResourceLimitExceeded):
		message = "ArangoDB rejected Explorer materialization because the query exceeded a configured resource limit. Check the ArangoDB server log for the exact limit, then adjust the query or server configuration before publishing again. The active revision was retained."
	case string(dataframeerrors.CodeQueryBackendOutOfMemory):
		message = "ArangoDB ran out of memory while materializing the Explorer. Reduce the Explorer query complexity or ask an operator to increase the memory available to ArangoDB, then publish again. The active revision was retained."
	default:
		return nil
	}
	return failureDetails(ClassUnavailable, stage, userErr.Code(), message, userErr.Details(), err)
}

func (s *Service) resolveExecutionCapability(ctx context.Context, project, token string) (AuthorizedCapability, capability.Snapshot, error) {
	if s.config.Capability.ForExecution != nil {
		authorized, err := s.config.Capability.ForExecution(ctx, project, token)
		return authorized, authorized.Snapshot, err
	}
	snapshot, err := s.config.Capability.Token(ctx, project, token)
	return AuthorizedCapability{Snapshot: snapshot}, snapshot, err
}
