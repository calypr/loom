package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) PublishRepository(ctx context.Context, request RepositoryPublishRequest) (RepositoryPublishResult, error) {
	if err := request.Workspace.ValidateForPublication(); err != nil {
		code := workspaceValidationCode(err)
		return RepositoryPublishResult{}, unprocessable("repository_publish", code, err.Error(), err)
	}
	if s.config.Capability.Current == nil || s.config.Capability.ForCompilation == nil || s.config.CompileReceipt == nil {
		return RepositoryPublishResult{}, unavailable("repository_publish", "PUBLICATION_UNAVAILABLE", "repository V2 compilation is not configured", nil)
	}
	snapshot, err := s.config.Capability.Current(ctx, request.Project, "default", request.Generation)
	if err != nil {
		return RepositoryPublishResult{}, capabilityResolutionFailure("repository_publish", "repository V2 capability could not be resolved", err)
	}
	if !snapshot.Usable() || snapshot.Identity.Generation != request.Generation {
		return RepositoryPublishResult{}, conflict("repository_publish", "RECEIPT_STALE", "repository V2 capability is stale or unusable", nil, nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, snapshot.Token)
	if err != nil {
		return RepositoryPublishResult{}, capabilityResolutionFailure("repository_publish", "repository V2 compilation could not be authorized", err)
	}
	receipt, err := s.config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: "default", Workspace: request.Workspace, SnapshotToken: snapshot.Token, Authorized: authorized})
	if err != nil || receipt == nil {
		if err == nil {
			return RepositoryPublishResult{}, unavailable("repository_publish", "COMPILATION_FAILED", "the compiler returned no receipt", nil)
		}
		var typed *Error
		if errors.As(err, &typed) {
			return RepositoryPublishResult{}, typed
		}
		return RepositoryPublishResult{}, unprocessable("repository_publish", "COMPILATION_FAILED", fmt.Sprintf("compile repository V2 workspace: %v", err), err)
	}
	if receipt.SourceGeneration != request.Generation {
		return RepositoryPublishResult{}, conflict("repository_publish", "RECEIPT_STALE", "compiled receipt generation does not match deployment generation", nil, nil)
	}
	if s.config.Capability.ForExecution != nil {
		authorized, err = s.config.Capability.ForExecution(ctx, request.Project, receipt.SnapshotToken)
		if err != nil {
			return RepositoryPublishResult{}, capabilityResolutionFailure("repository_publish", "repository V2 materialization could not be authorized", err)
		}
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project), DatasetGeneration: request.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection}
	applyAuthorizedScope(&bindings, authorized, true)
	if s.config.MaterializeReceipt == nil {
		return RepositoryPublishResult{}, unavailable("repository_publish", "MATERIALIZATION_UNAVAILABLE", "repository V2 materialization is not configured", nil)
	}
	execution, err := s.config.MaterializeReceipt(ctx, receipt, bindings)
	if err != nil {
		var typed *Error
		if errors.As(err, &typed) {
			return RepositoryPublishResult{}, typed
		}
		if classifiedErr := classifyMaterializationError("repository_publish", err); classifiedErr != nil {
			return RepositoryPublishResult{}, classifiedErr
		}
		return RepositoryPublishResult{}, unavailable("repository_publish", "MATERIALIZATION_FAILED", "repository V2 materialization is unavailable", err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return RepositoryPublishResult{}, unprocessable("repository_publish", "MATERIALIZATION_FAILED", err.Error(), err)
	}
	if err := verifyActivationQuality(receipt, execution); err != nil {
		return RepositoryPublishResult{}, unprocessable("repository_publish", "QUALITY_EVIDENCE_INVALID", err.Error(), err)
	}
	materialized := materializations(receipt.Bundle, execution)
	datasetMetadata := datasetMetadataFromExecution(receipt.Bundle, request.Generation, receipt.ResolvedSchemaDigest, execution)
	publication := explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: request.Generation, ExecutionID: execution.ID, UpdatedAt: s.now()}
	owner, revision, err := s.store.UpsertRepositoryV2(ctx, *receipt, request.Commit, request.Actor, materialized, datasetMetadata, execution.QualityReports, publication)
	if err != nil {
		return RepositoryPublishResult{}, unavailable("repository_publish", "PERSISTENCE_UNAVAILABLE", "the published repository revision could not be saved", err)
	}
	if owner.ManagementMode != explorer.ManagementRepository {
		return RepositoryPublishResult{}, conflict("repository_publish", "INVALID_MANAGEMENT_MODE", "default Explorer has invalid management mode", nil, nil)
	}
	if s.config.ActivateRelease == nil {
		return RepositoryPublishResult{}, unavailable("repository_publish", "PUBLICATION_UNAVAILABLE", "release activation is not configured", nil)
	}
	if err := s.config.ActivateRelease(ctx, projectid.Legacy(request.Project), request.Generation, selectorsForBundle(receipt.Bundle)); err != nil {
		if errors.Is(err, dataset.ErrReleaseActivationConflict) {
			// Deterministic lost compare-and-swap race, the same contract the
			// authoring publish path types as PUBLICATION_ACTIVATION_CONFLICT:
			// fixed conflict text, never the wrapped transport text, and no
			// retry hint for a race the client cannot win by hammering.
			_, _ = s.store.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "PUBLICATION_ACTIVATION_CONFLICT", Message: err.Error(), Retryable: false}})
			return RepositoryPublishResult{}, conflict("repository_publish", "PUBLICATION_ACTIVATION_CONFLICT", "another publication won activation; the prior Explorer revision was retained", nil, err)
		}
		var requirementsErr *dataset.ReleaseRequirementsError
		if errors.As(err, &requirementsErr) {
			// Deterministic release-contract miss: a 409 with an explicit
			// non-retryable diagnostic, not a fault the client should retry.
			_, _ = s.store.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: false}})
			return RepositoryPublishResult{}, conflict("repository_publish", "RELEASE_ACTIVATION_FAILED", fmt.Sprintf("activate published ExplorerConfigV2: %v", err), map[string]any{"retryable": false}, err)
		}
		_, _ = s.store.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
		return RepositoryPublishResult{}, unavailable("repository_publish", "RELEASE_ACTIVATION_FAILED", fmt.Sprintf("activate published ExplorerConfigV2: %v", err), err)
	}
	if err := s.store.ActivateRepositoryGeneration(ctx, request.Project, request.Generation, revision.ID); err != nil {
		// The visibility switch follows the sibling CAS contract above: the
		// typed lost-race sentinel keeps a deterministic fixed-text conflict
		// with no retry bait, and any other failure is a transport fault that
		// must never surface as a conflict or echo wrapped store text. The
		// sentinel marks losing the composite activation guard (read miss at
		// activation.go, and its zero-replaced/zero-updated guards, all of
		// which mean a concurrent Explorer update won the switch).
		if errors.Is(err, explorer.ErrDraftConflict) {
			_, _ = s.store.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "GENERATION_ACTIVATION_FAILED", Message: err.Error(), Retryable: false}})
			return RepositoryPublishResult{}, conflict("repository_publish", "GENERATION_ACTIVATION_FAILED", "generation activation lost a concurrent Explorer update; the prior Explorer revision was retained", nil, err)
		}
		_, _ = s.store.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "GENERATION_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
		return RepositoryPublishResult{}, unavailable("repository_publish", "GENERATION_ACTIVATION_FAILED", "activate ExplorerConfigV2 failed; the prior Explorer revision was retained", err)
	}
	publication.State, publication.RevisionID, publication.UpdatedAt = string(explorer.RevisionActive), revision.ID, s.now()
	return RepositoryPublishResult{Receipt: receipt, Owner: owner, Revision: revision, Execution: execution, Materializations: materialized, Dataset: datasetMetadata, Publication: publication}, nil
}
