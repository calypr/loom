package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) UpsertRepositoryV2(ctx context.Context, receipt receiptmodel.CompilationReceipt, sourceCommit, actor string, materializations []viewer.Materialization, data viewer.DatasetMetadata, qualityReports []dataframepublication.QualityReport, publicationMetadata viewer.PublicationMetadata) (*explorer.Explorer, *explorer.Revision, error) {
	if err := receipt.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid repository compilation receipt: %w", err)
	}
	if strings.TrimSpace(receipt.ExplorerID) != "default" {
		return nil, nil, fmt.Errorf("repository compilation receipt must target default")
	}
	if len(receipt.NormalizedBundle) == 0 || len(receipt.CompiledConfig) == 0 || len(receipt.PublicOutputContract) == 0 {
		return nil, nil, fmt.Errorf("repository compilation receipt is missing durable V2 artifacts")
	}
	if s.config.ExplorerStore == nil {
		return nil, nil, fmt.Errorf("repository Explorer store is required")
	}
	if s.config.RevisionWriter == nil {
		return nil, nil, fmt.Errorf("repository revision writer is required")
	}
	project := projectid.Canonical(receipt.Project)
	storageProject := projectid.Legacy(project)
	workspace := append(json.RawMessage(nil), receipt.NormalizedBundle...)
	compiledConfig := append(json.RawMessage(nil), receipt.CompiledConfig...)
	title := "Default"
	if decoded, err := ws.DecodeWorkspace(workspace); err == nil && len(decoded.Tabs) > 0 {
		title = decoded.Tabs[0].Title
	}
	if _, err := s.store.StoreCompilationReceipt(ctx, receipt); err != nil {
		return nil, nil, err
	}
	owner, err := s.config.ExplorerStore.Get(ctx, storageProject, "default")
	if errors.Is(err, explorer.ErrNotFound) {
		owner, err = s.config.ExplorerStore.Create(ctx, explorer.Explorer{Project: storageProject, ExplorerID: "default", Title: title, ManagementMode: viewer.ManagementRepository, DraftConfig: workspace, DraftVersion: 1, DraftDigest: receipt.IntentDigest, UpdatedBy: actor, UpdatedAt: s.now()})
	} else if err == nil {
		owner.Title, owner.DraftConfig, owner.DraftDigest, owner.UpdatedBy = title, workspace, receipt.IntentDigest, actor
		owner, err = s.config.ExplorerStore.SaveDraft(ctx, *owner, owner.DraftVersion)
	}
	if err != nil {
		return nil, nil, err
	}
	revisionID := RepositoryRevisionID(project, sourceCommit, receipt.IntentDigest, receipt.SourceGeneration, receipt.ID)
	now := s.now()
	revision := explorer.NewRevision(explorer.Revision{
		ID: revisionID, Project: storageProject, ExplorerID: "default", Config: compiledConfig,
		AuthoringBundle: workspace, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID,
		PublicOutputContract: append(json.RawMessage(nil), receipt.PublicOutputContract...), Recipe: receipt.Bundle,
		RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest,
		SourceGeneration: receipt.SourceGeneration, Dataset: data,
		QualityReports: dataframepublication.CloneQualityReports(qualityReports), Publication: publicationMetadata,
		Materializations: append([]viewer.Materialization(nil), materializations...),
		EmittedColumns:   append([]viewer.EmittedColumn(nil), receipt.EmittedColumns...),
		CreatedBy:        actor, CreatedAt: now, ReadyAt: &now,
	})
	storedRevision, err := s.config.RevisionWriter.InsertRevision(ctx, revision)
	if err != nil {
		return nil, nil, err
	}
	return owner, storedRevision, nil
}

func RepositoryRevisionID(project, sourceCommit, definitionDigest, sourceGeneration string, receiptIdentity ...string) string {
	artifact := ""
	if len(receiptIdentity) > 0 {
		artifact = receiptIdentity[0]
	}
	sum := sha256.Sum256([]byte(project + "\x00default\x00" + sourceCommit + "\x00" + definitionDigest + "\x00" + sourceGeneration + "\x00" + artifact))
	return "repository_" + hex.EncodeToString(sum[:])
}

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
	owner, revision, err := s.UpsertRepositoryV2(ctx, *receipt, request.Commit, request.Actor, materialized, datasetMetadata, execution.QualityReports, publication)
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
