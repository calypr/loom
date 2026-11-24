package publication

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) Publish(ctx context.Context, request PublishRequest) (PublishResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" {
		return PublishResult{}, support.Malformed("publish", "receiptId is required", nil)
	}
	if s.config.MaterializeReceipt == nil || s.config.PrepareRelease == nil || s.config.ValidateReleaseGeneration == nil || (s.config.Capability.Token == nil && s.config.Capability.ForExecution == nil) {
		return PublishResult{}, support.Unavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured", nil)
	}
	receipt, err := s.core.LookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return PublishResult{}, err
	}
	if err := s.core.ValidateExecutableReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return PublishResult{}, err
	}
	authorized, snapshot, err := s.resolveExecutionCapability(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil {
		return PublishResult{}, authoringcore.CapabilityResolutionFailure("publish", "the receipt's capability snapshot is no longer authorized or retained", err)
	}
	if strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return PublishResult{}, support.Conflict("publish", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, nil)
	}
	if s.config.CompileReceipt != nil {
		if err := authoringcore.ValidateReceiptCapability(receipt, snapshot); err != nil {
			return PublishResult{}, support.Conflict("publish", "RECEIPT_STALE", err.Error(), nil, err)
		}
	}
	if len(receipt.EmittedColumns) == 0 || len(receipt.CompiledConfig) == 0 {
		return PublishResult{}, support.Unprocessable("publish", "NO_SELECTED_COLUMNS", "select at least one output column before publishing", nil)
	}
	if s.config.CompileReceipt != nil {
		workspace, decodeErr := ws.DecodeWorkspace(receipt.NormalizedBundle)
		if decodeErr != nil {
			return PublishResult{}, support.Conflict("publish", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt contains invalid authoring intent", nil, decodeErr)
		}
		if err := workspace.ValidateForPublication(); err != nil {
			code := support.WorkspaceValidationCode(err)
			message := "select at least one visible output column for every visible table before publishing"
			if code == "UNACKNOWLEDGED_RELATED_FIRST" {
				message = "acknowledge the related-resource FIRST selection before publishing"
			}
			return PublishResult{}, support.Unprocessable("publish", code, message, err)
		}
	}
	if err := s.config.ValidateReleaseGeneration(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {
		if errors.Is(err, dataset.ErrReleaseRequirementsUnmet) {
			return PublishResult{}, support.Conflict("publish", "RECEIPT_STALE", "the receipt generation is no longer active", map[string]any{"generation": receipt.SourceGeneration}, err)
		}
		return PublishResult{}, support.Unavailable("publish", "RELEASE_GENERATION_UNAVAILABLE", "the receipt generation could not be verified", err)
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project), DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection}
	support.ApplyAuthorizedScope(&bindings, authorized, true)
	execution, err := s.config.MaterializeReceipt(ctx, receipt, bindings)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return PublishResult{}, typed
		}
		if classifiedErr := support.ClassifyMaterializationError("materialize", err); classifiedErr != nil {
			return PublishResult{}, classifiedErr
		}
		if userErr, ok := dataframeerrors.AsUserError(err); ok {
			switch userErr.Code() {
			case string(dataframeerrors.CodePublicationInProgress):
				details := userErr.Details()
				if details == nil {
					details = make(map[string]any)
				}
				details["retryable"] = userErr.Retryable()
				return PublishResult{}, support.Conflict("materialize", userErr.Code(), "Explorer publication is already in progress; retry after it completes", details, err)
			}
		}
		return PublishResult{}, support.Unavailable("materialize", "MATERIALIZATION_FAILED", "Explorer materialization failed; the active revision was retained", err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return PublishResult{}, support.Unavailable("materialize", "MATERIALIZATION_FAILED", "materialization did not produce queryable outputs", err)
	}
	if err := verifyActivationQuality(receipt, execution); err != nil {
		return PublishResult{}, support.Unavailable("quality", "QUALITY_EVIDENCE_INVALID", "publication quality evidence is incomplete or does not match the materialized output; the prior Explorer revision was retained", err)
	}
	release, expectedReleaseRevision, err := s.config.PrepareRelease(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle))
	if err != nil {
		var requirementsErr *dataset.ReleaseRequirementsError
		if errors.As(err, &requirementsErr) {
			return PublishResult{}, support.FailureDetails(ports.ClassUnprocessable, "activation", "RELEASE_REQUIREMENTS_UNMET", "release contract verification is unsatisfied; the prior Explorer revision was retained", map[string]any{"retryable": false}, err)
		}
		return PublishResult{}, support.Unavailable("activation", "MATERIALIZATION_ACTIVATION_FAILED", "dataset release preparation failed; the prior Explorer revision was retained", err)
	}
	now := s.now()
	revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
	revisionValue := explorer.NewRevision(explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: receipt.PublicOutputContract, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: materializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), QualityReports: publication.CloneQualityReports(execution.QualityReports), Publication: viewer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, CreatedBy: request.Actor, CreatedAt: now, ReadyAt: &now})
	revision, err := s.store.PublishAuthoring(ctx, *receipt, revisionValue, release, expectedReleaseRevision)
	if err != nil {
		if errors.Is(err, dataset.ErrReleaseActivationConflict) {
			return PublishResult{}, support.Conflict("activation", "PUBLICATION_ACTIVATION_CONFLICT", "another publication won activation; the prior Explorer revision was retained", nil, err)
		}
		return PublishResult{}, support.Unavailable("publish", "PERSISTENCE_UNAVAILABLE", "the published Explorer revision could not be saved", err)
	}
	if s.config.PersistPublishedWorkspace != nil {
		if err := s.config.PersistPublishedWorkspace(ctx, receipt.Project, receipt.ExplorerID, receipt.NormalizedBundle); err != nil {
			return PublishResult{}, support.Unavailable("local_writeback", "LOCAL_WORKSPACE_WRITEBACK_FAILED", "Explorer published, but the local CONFIG workspace could not be updated", err)
		}
	}
	return PublishResult{Receipt: receipt, Revision: revision, Execution: execution}, nil
}

func (s *Service) resolveExecutionCapability(ctx context.Context, project, token string) (ports.AuthorizedCapability, capability.Snapshot, error) {
	if s.config.Capability.ForExecution != nil {
		authorized, err := s.config.Capability.ForExecution(ctx, project, token)
		return authorized, authorized.Snapshot, err
	}
	snapshot, err := s.config.Capability.Token(ctx, project, token)
	return ports.AuthorizedCapability{Snapshot: snapshot}, snapshot, err
}
