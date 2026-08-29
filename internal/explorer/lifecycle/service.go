package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

// Service is the Explorer application layer. It coordinates persistence and
// deployment adapters while leaving all wire representation to transports.
type Service struct {
	store  Store
	config Config
	now    func() time.Time
}

func New(store Store, config Config) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("Explorer lifecycle store is required")
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	config.Now = now
	return &Service{store: store, config: config, now: now}, nil
}

func (s *Service) List(ctx context.Context, project string) (ListResult, error) {
	project = projectid.Canonical(project)
	values, err := s.store.List(ctx, project)
	if err != nil {
		return ListResult{}, internal("list", "EXPLORER_READ_FAILED", err.Error(), err)
	}
	result := ListResult{Project: project, Summaries: make([]explorer.ExplorerSummaryV1, 0, len(values))}
	for _, value := range values {
		result.Summaries = append(result.Summaries, explorer.ExplorerSummaryV1{
			Project: project, ExplorerID: value.ExplorerID, Title: value.Title,
			Management: value.ManagementMode, ActiveRevisionID: value.ActiveRevisionID,
			UpdatedAt: value.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, project, id string) (explorer.ExplorerStateV1, error) {
	state, err := s.store.LoadExplorerState(ctx, projectid.Canonical(project), strings.TrimSpace(id))
	if errors.Is(err, explorer.ErrNotFound) {
		return explorer.ExplorerStateV1{}, notFound("get", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return explorer.ExplorerStateV1{}, internal("get", "EXPLORER_READ_FAILED", err.Error(), err)
	}
	return state, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (explorer.ExplorerSummaryV1, error) {
	project := projectid.Canonical(request.Project)
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return explorer.ExplorerSummaryV1{}, malformed("create", "name is required", nil)
	}
	id := explorer.StableExplorerID(name)
	if id == "default" {
		return explorer.ExplorerSummaryV1{}, conflict("create", "EXPLORER_EXISTS", "the repository default already exists", nil, nil)
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = name
	}
	value, err := s.store.CreateInteractiveFrom(ctx, project, id, title, strings.TrimSpace(request.SourceExplorerID), request.Actor)
	if errors.Is(err, explorer.ErrDraftConflict) {
		return explorer.ExplorerSummaryV1{}, conflict("create", "EXPLORER_EXISTS", "an Explorer with this name already exists", nil, err)
	}
	if err != nil {
		return explorer.ExplorerSummaryV1{}, unprocessable("create", "INVALID_EXPLORER", err.Error(), err)
	}
	return explorer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, UpdatedAt: value.UpdatedAt}, nil
}

func (s *Service) Capability(ctx context.Context, project, explorerID, generation string) (capability.Snapshot, authoringv2.CatalogSnapshot, error) {
	if s.config.Capability.Current == nil || s.config.Capability.Catalog == nil {
		return capability.Snapshot{}, authoringv2.CatalogSnapshot{}, unavailable("capability", "CAPABILITY_UNAVAILABLE", "Explorer capability compiler is not configured", nil)
	}
	snapshot, err := s.config.Capability.Current(ctx, project, explorerID, generation)
	if err != nil || !snapshot.Usable() {
		if err == nil {
			err = capability.ErrSnapshotUnavailable
		}
		return capability.Snapshot{}, authoringv2.CatalogSnapshot{}, unavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error(), err)
	}
	return snapshot, s.config.Capability.Catalog(snapshot, explorerID), nil
}

func (s *Service) Suggestions(ctx context.Context, request SuggestionsRequest) (SuggestionsResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.NodeID) == "" {
		return SuggestionsResult{}, malformed("suggestions", "snapshotToken and nodeId are required", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return SuggestionsResult{}, unavailable("suggestions", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return SuggestionsResult{}, conflict("suggestions", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale", nil, err)
	}
	catalog := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	query := strings.ToLower(strings.TrimSpace(request.Query))
	result := SuggestionsResult{SnapshotToken: request.SnapshotToken, NodeID: request.NodeID, Candidates: make([]authoringv2.CatalogCandidate, 0)}
	for _, candidate := range catalog.Candidates {
		if candidate.NodeID != request.NodeID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(candidate.Label), query) && !strings.Contains(strings.ToLower(candidate.ID), query) {
			continue
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, nil
}

func (s *Service) Builder(ctx context.Context, request BuilderRequest) (authoringv2.BuilderState, error) {
	_, catalog, err := s.Capability(ctx, request.Project, request.ExplorerID, "")
	if err != nil {
		return authoringv2.BuilderState{}, err
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) {
			return authoringv2.BuilderState{}, notFound("builder", "EXPLORER_NOT_FOUND", "Explorer not found", err)
		}
		return authoringv2.BuilderState{}, err
	}
	state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, LifecycleState: authoringv2.LifecycleNew, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Catalog: catalog}
	var activeWorkspace *authoringv2.Workspace
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.store.ActiveRevision(ctx, request.Project, request.ExplorerID)
		if activeErr != nil {
			return authoringv2.BuilderState{}, conflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision cannot be loaded as V2 authoring state", map[string]any{"revisionId": owner.ActiveRevisionID}, activeErr)
		}
		workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle)
		if decodeErr != nil {
			return authoringv2.BuilderState{}, conflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision has no valid V2 workspace", map[string]any{"revisionId": active.ID}, decodeErr)
		}
		activeWorkspace = &workspace
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return authoringv2.BuilderState{}, conflict("builder", "DRAFT_STATE_INVALID", "the saved Explorer draft is not a valid V2 workspace", map[string]any{"draftVersion": owner.DraftVersion}, decodeErr)
		}
		state.Workspace = &workspace
		state.LifecycleState = authoringv2.LifecycleReady
	} else {
		state.Workspace = activeWorkspace
		if activeWorkspace != nil {
			state.LifecycleState = authoringv2.LifecycleReady
		}
	}
	if err := state.Validate(); err != nil {
		return authoringv2.BuilderState{}, unavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error(), err)
	}
	return state, nil
}

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

func (s *Service) Compile(ctx context.Context, request CompileRequest) (*explorer.CompilationReceipt, error) {
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
	return s.Compile(ctx, CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken})
}

func (s *Service) Preview(ctx context.Context, request PreviewRequest) (PreviewResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" {
		return PreviewResult{}, malformed("preview", "receiptId and outputId are required", nil)
	}
	if request.Limit == 0 {
		request.Limit = engine.DefaultPreviewLimit
	}
	if request.Limit > engine.MaxPreviewLimit {
		return PreviewResult{}, unprocessable("preview", "INVALID_PREVIEW_LIMIT", "limit must be between 1 and 1000", nil)
	}
	if s.config.Capability.ForExecution == nil {
		return PreviewResult{}, unavailable("preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured", nil)
	}
	if s.config.Preview == nil && s.config.PreviewReceipt == nil {
		return PreviewResult{}, unavailable("preview", "PREVIEW_UNAVAILABLE", "Explorer preview is not configured", nil)
	}
	receipt, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return PreviewResult{}, err
	}
	if err := s.validateReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return PreviewResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return PreviewResult{}, conflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if s.config.PreviewReceipt != nil {
		if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
			return PreviewResult{}, conflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
		}
	}
	if !receiptHasOutput(receipt.Bundle, request.OutputID) || (s.config.PreviewReceipt != nil && validateReceiptOutputContract(receipt, request.OutputID) != nil) {
		return PreviewResult{}, unprocessable("preview", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration, PreviewLimit: request.Limit, OutputNames: []string{request.OutputID}}
	applyAuthorizedScope(&bindings, authorized, false)
	columns := emittedColumnsForOutput(receipt, request.OutputID)
	result := PreviewResult{Receipt: receipt, Columns: columns}
	if s.config.PreviewReceipt != nil {
		if request.SinkFactory == nil {
			return PreviewResult{}, unavailable("preview", "PREVIEW_UNAVAILABLE", "native preview sink is not configured", nil)
		}
		sink, err := request.SinkFactory(receipt, columns)
		if err != nil {
			return PreviewResult{}, err
		}
		result.Summary, err = s.config.PreviewReceipt(ctx, receipt, bindings, sink)
		if err != nil {
			return PreviewResult{}, err
		}
		return result, nil
	}
	result.Rows, err = s.config.Preview(ctx, receipt.Bundle, bindings)
	if err != nil {
		return PreviewResult{}, err
	}
	return result, nil
}

func (s *Service) Publish(ctx context.Context, request PublishRequest) (PublishResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" {
		return PublishResult{}, malformed("publish", "receiptId is required", nil)
	}
	if s.config.Materialize == nil && s.config.MaterializeReceipt == nil {
		return PublishResult{}, unavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured", nil)
	}
	if s.config.ActivateRelease == nil || s.config.ValidateReleaseGeneration == nil || (s.config.Capability.Token == nil && s.config.Capability.ForExecution == nil) {
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
	var execution Execution
	if s.config.MaterializeReceipt != nil {
		execution, err = s.config.MaterializeReceipt(ctx, receipt, bindings)
	} else {
		execution, err = s.config.Materialize(ctx, receipt.Bundle, bindings)
	}
	if err != nil {
		return PublishResult{}, unavailable("materialize", "MATERIALIZATION_FAILED", "Explorer materialization failed; the active revision was retained", err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return PublishResult{}, unavailable("materialize", "MATERIALIZATION_FAILED", "materialization did not produce queryable outputs", err)
	}
	if err := s.config.ActivateRelease(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle)); err != nil {
		return PublishResult{}, unavailable("activation", "MATERIALIZATION_ACTIVATION_FAILED", "dataset release activation failed; the prior Explorer revision was retained", err)
	}
	now := s.now()
	revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
	revision, err := s.store.PublishAuthoring(ctx, *receipt, explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, ConfigDigest: receipt.IntentDigest, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: receipt.PublicOutputContract, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: materializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, Status: explorer.RevisionReady, CreatedBy: request.Actor, CreatedAt: now, ReadyAt: &now})
	if err != nil {
		return PublishResult{}, err
	}
	return PublishResult{Receipt: receipt, Revision: revision, Execution: execution}, nil
}

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
	if s.config.MaterializeReceipt != nil {
		execution, err = s.config.MaterializeReceipt(ctx, receipt, bindings)
	} else if s.config.Materialize != nil {
		execution, err = s.config.Materialize(ctx, receipt.Bundle, bindings)
	} else {
		err = errors.New("repository V2 materialization is not configured")
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
	if _, err := s.store.SaveRepositoryConfig(ctx, explorer.RepositoryConfig{Project: request.Project, Config: append([]byte(nil), receipt.CompiledConfig...), Workspace: append([]byte(nil), receipt.NormalizedBundle...), ConfigDigest: receipt.IntentDigest, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: append([]byte(nil), receipt.PublicOutputContract...), ActiveRevisionID: revision.ID, DraftVersion: owner.DraftVersion, SourceGeneration: request.Generation, SourceCommit: request.Commit, ExecutionID: execution.ID, Materializations: materialized, Dataset: datasetMetadata, Publication: publication}); err != nil {
		return RepositoryPublishResult{}, internal("repository_publish", "PERSISTENCE_FAILED", fmt.Sprintf("persist ExplorerConfigV2: %v", err), err)
	}
	return RepositoryPublishResult{Receipt: receipt, Owner: owner, Revision: revision, Execution: execution, Materializations: materialized, Dataset: datasetMetadata, Publication: publication}, nil
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

func (s *Service) resolveExecutionCapability(ctx context.Context, project, token string) (AuthorizedCapability, capability.Snapshot, error) {
	if s.config.Capability.ForExecution != nil {
		authorized, err := s.config.Capability.ForExecution(ctx, project, token)
		return authorized, authorized.Snapshot, err
	}
	snapshot, err := s.config.Capability.Token(ctx, project, token)
	return AuthorizedCapability{Snapshot: snapshot}, snapshot, err
}
