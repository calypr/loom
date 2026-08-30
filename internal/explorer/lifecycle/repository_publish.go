package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) PublishRepository(ctx context.Context, request RepositoryPublishRequest) (RepositoryPublishResult, error) {
	if err := request.Workspace.ValidateForPublication(); err != nil {
		return RepositoryPublishResult{}, unprocessable("repository_publish", "INVALID_WORKSPACE", err.Error(), err)
	}
	if s.config.Capability.Current == nil || s.config.Capability.ForCompilation == nil || s.config.CompileReceipt == nil {
		return RepositoryPublishResult{}, unavailable("repository_publish", "PUBLICATION_UNAVAILABLE", "repository V2 compilation is not configured", nil)
	}
	snapshot, err := s.config.Capability.Current(ctx, request.Project, "default", request.Generation)
	if err != nil || !snapshot.Usable() || snapshot.Identity.Generation != request.Generation {
		return RepositoryPublishResult{}, conflict("repository_publish", "RECEIPT_STALE", fmt.Sprintf("resolve repository V2 capability: %v", err), nil, err)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, snapshot.Token)
	if err != nil {
		return RepositoryPublishResult{}, forbidden("repository_publish", fmt.Sprintf("authorize repository V2 compilation: %v", err), err)
	}
	receipt, err := s.config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: "default", Workspace: request.Workspace, SnapshotToken: snapshot.Token, Authorized: authorized})
	if err != nil || receipt == nil {
		if err == nil {
			err = errors.New("compiler returned no receipt")
		}
		return RepositoryPublishResult{}, unprocessable("repository_publish", "COMPILATION_FAILED", fmt.Sprintf("compile repository V2 workspace: %v", err), err)
	}
	if receipt.SourceGeneration != request.Generation {
		return RepositoryPublishResult{}, conflict("repository_publish", "RECEIPT_STALE", "compiled receipt generation does not match deployment generation", nil, nil)
	}
	if s.config.Capability.ForExecution != nil {
		authorized, err = s.config.Capability.ForExecution(ctx, request.Project, receipt.SnapshotToken)
		if err != nil {
			return RepositoryPublishResult{}, forbidden("repository_publish", fmt.Sprintf("authorize repository V2 materialization: %v", err), err)
		}
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(request.Project), DatasetGeneration: request.Generation}
	applyAuthorizedScope(&bindings, authorized, true)
	var execution Execution
	if s.config.MaterializeReceipt == nil {
		err = errors.New("repository V2 materialization is not configured")
	} else {
		execution, err = s.config.MaterializeReceipt(ctx, receipt, bindings)
	}
	if err != nil {
		return RepositoryPublishResult{}, unprocessable("repository_publish", "MATERIALIZATION_FAILED", fmt.Sprintf("materialize repository V2 workspace: %v", err), err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return RepositoryPublishResult{}, unprocessable("repository_publish", "MATERIALIZATION_FAILED", err.Error(), err)
	}
	materialized := materializations(receipt.Bundle, execution)
	datasetMetadata := datasetMetadataFromExecution(receipt.Bundle, request.Generation, receipt.ResolvedSchemaDigest, execution)
	publication := explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: request.Generation, ExecutionID: execution.ID, UpdatedAt: s.now()}
	owner, revision, err := s.store.UpsertRepositoryV2(ctx, *receipt, request.Commit, request.Actor, materialized, datasetMetadata, publication)
	if err != nil {
		return RepositoryPublishResult{}, internal("repository_publish", "PERSISTENCE_FAILED", fmt.Sprintf("persist Explorer lifecycle V2: %v", err), err)
	}
	if owner.ManagementMode != explorer.ManagementRepository {
		return RepositoryPublishResult{}, conflict("repository_publish", "INVALID_MANAGEMENT_MODE", "default Explorer has invalid management mode", nil, nil)
	}
	if s.config.ActivateRelease == nil {
		return RepositoryPublishResult{}, unavailable("repository_publish", "PUBLICATION_UNAVAILABLE", "release activation is not configured", nil)
	}
	if err := s.config.ActivateRelease(ctx, projectid.Legacy(request.Project), request.Generation, selectorsForBundle(receipt.Bundle)); err != nil {
		_, _ = s.store.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
		return RepositoryPublishResult{}, conflict("repository_publish", "RELEASE_ACTIVATION_FAILED", fmt.Sprintf("activate published ExplorerConfigV2: %v", err), nil, err)
	}
	if err := s.store.ActivateRepositoryGeneration(ctx, request.Project, request.Generation, revision.ID); err != nil {
		return RepositoryPublishResult{}, conflict("repository_publish", "RELEASE_ACTIVATION_FAILED", fmt.Sprintf("activate ExplorerConfigV2: %v", err), nil, err)
	}
	publication.State, publication.RevisionID, publication.UpdatedAt = string(explorer.RevisionActive), revision.ID, s.now()
	return RepositoryPublishResult{Receipt: receipt, Owner: owner, Revision: revision, Execution: execution, Materializations: materialized, Dataset: datasetMetadata, Publication: publication}, nil
}
