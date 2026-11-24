package publication

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
)

func classifyRepositoryCompilationError(err error) error {
	var typed *ports.Error
	if errors.As(err, &typed) {
		return typed
	}
	var compileErr *explorercompilation.Error
	if errors.As(err, &compileErr) {
		message := compileErr.Message
		if message == "" {
			message = "the repository V2 workspace is invalid"
		}
		return support.FailureDetails(ports.ClassUnprocessable, "repository_publish", authoringcore.CompilationErrorCode(compileErr.Code), message, compileErr.Details, err)
	}
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) {
		class := ports.ClassUnprocessable
		switch authoringErr.Status {
		case 401, 403:
			class = ports.ClassForbidden
		case 404:
			class = ports.ClassNotFound
		case 409:
			class = ports.ClassConflict
		case 429:
			class = ports.ClassUnavailable
		default:
			if authoringErr.Status >= 500 {
				class = ports.ClassUnavailable
			}
		}
		code := authoringErr.Diagnostic.Code
		if code == "" {
			code = "COMPILATION_FAILED"
		}
		return support.FailureDetails(class, "repository_publish", code, authoringErr.Diagnostic.Message, authoringErr.Diagnostic.Details, err)
	}
	if errors.Is(err, capability.ErrStaleSnapshot) {
		return support.Conflict("repository_publish", "STALE_CATALOG_SNAPSHOT", "repository V2 compilation used a stale capability snapshot", nil, err)
	}
	if errors.Is(err, capability.ErrSnapshotUnavailable) {
		return support.Unavailable("repository_publish", "CAPABILITY_UNAVAILABLE", "repository V2 compilation capability is unavailable", err)
	}
	if classified := support.ClassifyMaterializationError("repository_publish", err); classified != nil {
		return classified
	}
	return support.Unavailable("repository_publish", "COMPILATION_UNAVAILABLE", "the repository V2 workspace could not be compiled", err)
}

func (s *Service) PublishRepository(ctx context.Context, request RepositoryPublishRequest) (RepositoryPublishResult, error) {
	if err := request.Workspace.ValidateForPublication(); err != nil {
		code := support.WorkspaceValidationCode(err)
		return RepositoryPublishResult{}, support.Unprocessable("repository_publish", code, err.Error(), err)
	}
	if s.config.Capability.Current == nil || s.config.Capability.ForCompilation == nil || s.config.CompileReceipt == nil {
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "PUBLICATION_UNAVAILABLE", "repository V2 compilation is not configured", nil)
	}
	snapshot, err := s.config.Capability.Current(ctx, request.Project, "default", request.Generation)
	if err != nil {
		return RepositoryPublishResult{}, authoringcore.CapabilityResolutionFailure("repository_publish", "repository V2 capability could not be resolved", err)
	}
	if !snapshot.Usable() || snapshot.Identity.Generation != request.Generation {
		return RepositoryPublishResult{}, support.Conflict("repository_publish", "RECEIPT_STALE", "repository V2 capability is stale or unusable", nil, nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, snapshot.Token)
	if err != nil {
		return RepositoryPublishResult{}, authoringcore.CapabilityResolutionFailure("repository_publish", "repository V2 compilation could not be authorized", err)
	}
	receipt, err := s.config.CompileReceipt(ctx, ports.CompileReceiptRequest{Project: request.Project, ExplorerID: "default", Workspace: request.Workspace, SnapshotToken: snapshot.Token, Authorized: authorized})
	if err != nil || receipt == nil {
		if err == nil {
			return RepositoryPublishResult{}, support.Unavailable("repository_publish", "COMPILATION_FAILED", "the compiler returned no receipt", nil)
		}
		return RepositoryPublishResult{}, classifyRepositoryCompilationError(err)
	}
	if receipt.SourceGeneration != request.Generation {
		return RepositoryPublishResult{}, support.Conflict("repository_publish", "RECEIPT_STALE", "compiled receipt generation does not match deployment generation", nil, nil)
	}
	if s.config.Capability.ForExecution != nil {
		authorized, err = s.config.Capability.ForExecution(ctx, request.Project, receipt.SnapshotToken)
		if err != nil {
			return RepositoryPublishResult{}, authoringcore.CapabilityResolutionFailure("repository_publish", "repository V2 materialization could not be authorized", err)
		}
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project), DatasetGeneration: request.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection}
	support.ApplyAuthorizedScope(&bindings, authorized, true)
	if s.config.MaterializeReceipt == nil {
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "MATERIALIZATION_UNAVAILABLE", "repository V2 materialization is not configured", nil)
	}
	execution, err := s.config.MaterializeReceipt(ctx, receipt, bindings)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return RepositoryPublishResult{}, typed
		}
		if classifiedErr := support.ClassifyMaterializationError("repository_publish", err); classifiedErr != nil {
			return RepositoryPublishResult{}, classifiedErr
		}
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "MATERIALIZATION_FAILED", "repository V2 materialization is unavailable", err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return RepositoryPublishResult{}, support.Unprocessable("repository_publish", "MATERIALIZATION_FAILED", err.Error(), err)
	}
	if err := verifyActivationQuality(receipt, execution); err != nil {
		return RepositoryPublishResult{}, support.Unprocessable("repository_publish", "QUALITY_EVIDENCE_INVALID", err.Error(), err)
	}
	materialized := materializations(receipt.Bundle, execution)
	datasetMetadata := datasetMetadataFromExecution(receipt.Bundle, request.Generation, receipt.ResolvedSchemaDigest, execution)
	publication := viewer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: request.Generation, ExecutionID: execution.ID, UpdatedAt: s.now()}
	owner, revision, err := s.store.UpsertRepositoryV2(ctx, *receipt, request.Commit, request.Actor, materialized, datasetMetadata, execution.QualityReports, publication)
	if err != nil {
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "PERSISTENCE_UNAVAILABLE", "the published repository revision could not be saved", err)
	}
	if owner.ManagementMode != viewer.ManagementRepository {
		return RepositoryPublishResult{}, support.Conflict("repository_publish", "INVALID_MANAGEMENT_MODE", "default Explorer has invalid management mode", nil, nil)
	}
	if s.config.ActivateRelease == nil {
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "PUBLICATION_UNAVAILABLE", "release activation is not configured", nil)
	}
	if err := s.config.ActivateRelease(ctx, projectid.Legacy(request.Project), request.Generation, selectorsForBundle(receipt.Bundle)); err != nil {
		if errors.Is(err, dataset.ErrReleaseActivationConflict) {

			_, _ = s.store.FailRevision(ctx, revision.ID, []viewer.Diagnostic{{Severity: "ERROR", Code: "PUBLICATION_ACTIVATION_CONFLICT", Message: err.Error(), Retryable: false}})
			return RepositoryPublishResult{}, support.Conflict("repository_publish", "PUBLICATION_ACTIVATION_CONFLICT", "another publication won activation; the prior Explorer revision was retained", nil, err)
		}
		var requirementsErr *dataset.ReleaseRequirementsError
		if errors.As(err, &requirementsErr) {

			_, _ = s.store.FailRevision(ctx, revision.ID, []viewer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: false}})
			return RepositoryPublishResult{}, support.Conflict("repository_publish", "RELEASE_ACTIVATION_FAILED", fmt.Sprintf("activate published ExplorerConfigV2: %v", err), map[string]any{"retryable": false}, err)
		}
		_, _ = s.store.FailRevision(ctx, revision.ID, []viewer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "RELEASE_ACTIVATION_FAILED", "activate published ExplorerConfigV2 failed; the prior Explorer revision was retained", err)
	}
	if err := s.store.ActivateRepositoryGeneration(ctx, request.Project, request.Generation, revision.ID); err != nil {

		if errors.Is(err, explorer.ErrDraftConflict) {
			_, _ = s.store.FailRevision(ctx, revision.ID, []viewer.Diagnostic{{Severity: "ERROR", Code: "GENERATION_ACTIVATION_FAILED", Message: err.Error(), Retryable: false}})
			return RepositoryPublishResult{}, support.Conflict("repository_publish", "GENERATION_ACTIVATION_FAILED", "generation activation lost a concurrent Explorer update; the prior Explorer revision was retained", nil, err)
		}
		_, _ = s.store.FailRevision(ctx, revision.ID, []viewer.Diagnostic{{Severity: "ERROR", Code: "GENERATION_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
		return RepositoryPublishResult{}, support.Unavailable("repository_publish", "GENERATION_ACTIVATION_FAILED", "activate ExplorerConfigV2 failed; the prior Explorer revision was retained", err)
	}
	publication.State, publication.RevisionID, publication.UpdatedAt = string(explorer.RevisionActive), revision.ID, s.now()
	return RepositoryPublishResult{Receipt: receipt, Owner: owner, Revision: revision, Execution: execution, Materializations: materialized, Dataset: datasetMetadata, Publication: publication}, nil
}
