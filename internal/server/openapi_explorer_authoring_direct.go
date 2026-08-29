package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

// directAuthoringJSON converts the transport-neutral authoring values to the
// generated OpenAPI values. The generated types intentionally remain the
// boundary representation; the authoring package does not import transport
// code.
func directAuthoringJSON[T any](value any) (T, error) {
	var result T
	raw, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (h *explorerHTTPHandlers) authoringReadDirect(ctx context.Context, project string) error {
	if h == nil || h.authorizeRead == nil {
		return explorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		return &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func (h *explorerHTTPHandlers) authoringWriteDirect(ctx context.Context, project string) error {
	if h == nil || h.authorizer == nil {
		return explorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizer.AuthorizeWrite(ctx, principal, project, ""); err != nil {
		return &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func (h *explorerHTTPHandlers) getAuthoringCapabilityDirect(ctx context.Context, project string) (loomapi.AuthoringCapability, error) {
	var result loomapi.AuthoringCapability
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	return loomapi.AuthoringCapability{
		ApiVersion:    loomapi.LoomCalyprOrgexplorerAuthoringv2,
		Kind:          loomapi.ExplorerAuthoringCapabilities,
		Operations:    []loomapi.AuthoringCapabilityOperations{loomapi.Builder, loomapi.Suggestions, loomapi.Preview, loomapi.Publish, loomapi.Commands, loomapi.Reconcile},
		PreviewLimits: []int{10, 25, 50, 100},
		Features:      loomapi.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true},
	}, nil
}

func (h *explorerHTTPHandlers) searchAuthoringSuggestionsDirect(ctx context.Context, project, explorerID string, body *loomapi.SearchExplorerCandidatesJSONRequestBody) (loomapi.CandidateSearchResponse, error) {
	var result loomapi.CandidateSearchResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("suggestions", errors.New("request body is required"))
	}
	request := *body
	if strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.NodeId) == "" {
		return result, malformedRouteError("suggestions", errors.New("snapshotToken and nodeId are required"))
	}
	config := h.lifecycleConfig
	if config.CapabilityToken == nil {
		return result, explorerUnavailable("suggestions", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured")
	}
	snapshot, err := config.CapabilityToken(ctx, project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, explorerConflict("suggestions", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale", nil)
	}
	query := ""
	if request.Query != nil {
		query = strings.ToLower(strings.TrimSpace(*request.Query))
	}
	internal := authoringV2Catalog(snapshot, explorerID)
	candidates := make([]loomapi.CatalogCandidate, 0)
	for _, candidate := range internal.Candidates {
		if candidate.NodeID != request.NodeId || (query != "" && !strings.Contains(strings.ToLower(candidate.Label), query) && !strings.Contains(strings.ToLower(candidate.ID), query)) {
			continue
		}
		wire, convertErr := directAuthoringJSON[loomapi.CatalogCandidate](candidate)
		if convertErr != nil {
			return result, convertErr
		}
		candidates = append(candidates, wire)
	}
	return loomapi.CandidateSearchResponse{
		ApiVersion:    loomapi.LoomCalyprOrgexplorerAuthoringv2,
		Kind:          loomapi.ExplorerBuilderCandidateSuggestions,
		SnapshotToken: request.SnapshotToken,
		NodeId:        request.NodeId,
		Candidates:    candidates,
		Diagnostics:   []loomapi.Diagnostic{},
	}, nil
}

func (h *explorerHTTPHandlers) getAuthoringBuilderDirect(ctx context.Context, project, explorerID string) (loomapi.BuilderState, error) {
	var result loomapi.BuilderState
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if h == nil || h.explorers == nil {
		return result, explorerUnavailable("builder", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	config := h.lifecycleConfig
	if config.Capability == nil {
		return result, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", "Explorer capability compiler is not configured")
	}
	snapshot, err := config.Capability(ctx, project, explorerID, "")
	if err != nil || !snapshot.Usable() {
		if err == nil {
			err = capability.ErrSnapshotUnavailable
		}
		return result, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error())
	}
	wire, err := directAuthoringJSON[loomapi.Catalog](authoringV2Catalog(snapshot, explorerID))
	if err != nil {
		return result, err
	}
	owner, err := h.explorers.Get(ctx, project, explorerID)
	if err != nil {
		return result, err
	}
	state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, LifecycleState: authoringv2.LifecycleNew, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Catalog: authoringV2Catalog(snapshot, explorerID)}
	var activeWorkspace *authoringv2.Workspace
	if owner.ActiveRevisionID != "" {
		active, activeErr := h.explorers.ActiveRevision(ctx, project, explorerID)
		if activeErr != nil {
			return result, explorerConflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision cannot be loaded as V2 authoring state", map[string]any{"revisionId": owner.ActiveRevisionID})
		}
		workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle)
		if decodeErr != nil {
			return result, explorerConflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision has no valid V2 workspace", map[string]any{"revisionId": active.ID})
		}
		activeWorkspace = &workspace
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return result, explorerConflict("builder", "DRAFT_STATE_INVALID", "the saved Explorer draft is not a valid V2 workspace", map[string]any{"draftVersion": owner.DraftVersion})
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
		return result, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error())
	}
	// Keep the conversion explicit; the internal state has the same wire
	// representation but must not leak its transport-only hidden fields.
	_ = wire
	return directAuthoringJSON[loomapi.BuilderState](state)
}

func (h *explorerHTTPHandlers) applyAuthoringCommandsDirect(ctx context.Context, project, explorerID string, body *loomapi.ApplyExplorerBuilderCommandsJSONRequestBody) (loomapi.ApplyCommandsResponse, error) {
	var result loomapi.ApplyCommandsResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("commands", errors.New("request body is required"))
	}
	request, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](*body)
	if err != nil {
		return result, malformedRouteError("commands", err)
	}
	if err := request.Validate(); err != nil {
		return result, malformedRouteError("commands", err)
	}
	config := h.lifecycleConfig
	if config.CapabilityToken == nil {
		return result, explorerUnavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured")
	}
	snapshot, snapshotErr := config.CapabilityToken(ctx, project, request.SnapshotToken)
	if snapshotErr != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, explorerConflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil)
	}
	if h.explorers == nil {
		return result, explorerUnavailable("commands", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	response, applyErr := h.explorers.ApplyWorkspaceCommands(ctx, project, explorerID, authoringV2Catalog(snapshot, explorerID), request, subjectFromContext(ctx))
	switch {
	case errors.Is(applyErr, explorer.ErrDraftConflict):
		return result, explorerConflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before editing", nil)
	case errors.Is(applyErr, explorer.ErrAuthoringCommandConflict):
		return result, explorerConflict("commands", "COMMAND_ID_CONFLICT", "commandId was already used for different intent", nil)
	case applyErr != nil:
		return result, authoringSemanticRoute("commands", "$.commands", "INVALID_AUTHORING_COMMAND", applyErr.Error(), nil)
	default:
		return directAuthoringJSON[loomapi.ApplyCommandsResponse](response)
	}
}

func (h *explorerHTTPHandlers) compileAuthoringWorkspaceDirect(ctx context.Context, project, explorerID string, workspace authoringv2.Workspace, snapshotToken string) (loomapi.CompileResponse, error) {
	var result loomapi.CompileResponse
	config := h.lifecycleConfig
	if (config.CapabilityToken == nil && config.AuthorizedCapabilityCompile == nil) || config.CompileReceipt == nil {
		return result, explorerUnavailable("compile", "CAPABILITY_UNAVAILABLE", "Explorer V2 compiler is not configured")
	}
	var err error
	var snapshot capability.Snapshot
	var authorized AuthorizedCapability
	if config.AuthorizedCapabilityCompile != nil {
		authorized, err = config.AuthorizedCapabilityCompile(ctx, project, snapshotToken)
		snapshot = authorized.Snapshot
	} else {
		snapshot, err = config.CapabilityToken(ctx, project, snapshotToken)
	}
	if err != nil {
		return result, explorerConflict("capability", "STALE_CATALOG_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": snapshotToken})
	}
	state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: authoringV2Catalog(snapshot, explorerID)}
	if err := state.Validate(); err != nil {
		return result, authoringSemanticRoute("intent", "$", workspaceValidationCode(err), err.Error(), nil)
	}
	stored, err := config.CompileReceipt(ctx, ExplorerV2ReceiptCompileRequest{Project: project, ExplorerID: explorerID, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: requestIDFromContext(ctx), Authorized: authorized})
	if err != nil {
		var compileErr *explorercompilation.Error
		if errors.As(err, &compileErr) {
			return result, authoringSemanticRoute(compileErr.Stage, compileErr.Path, compilationErrorCode(compileErr.Code), compileErr.Message, compileErr.Details)
		}
		return result, err
	}
	if stored == nil || strings.TrimSpace(stored.ID) == "" {
		return result, explorerUnavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "compiled authoring receipt was not persisted")
	}
	return v2ReceiptResponse(stored, workspace), nil
}

func (h *explorerHTTPHandlers) compileAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.CompileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
	var result loomapi.CompileResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("intent", errors.New("request body is required"))
	}
	return h.compileAuthoringWorkspaceDirect(ctx, project, explorerID, body.Workspace, body.SnapshotToken)
}

func (h *explorerHTTPHandlers) reconcileAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.ReconcileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
	var result loomapi.CompileResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("reconcile", errors.New("request body is required"))
	}
	if strings.TrimSpace(body.SnapshotToken) == "" || body.DraftVersion < 0 {
		return result, malformedRouteError("reconcile", errors.New("snapshotToken, draftVersion, and draftDigest are required"))
	}
	if h == nil || h.explorers == nil {
		return result, explorerUnavailable("reconcile", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	owner, err := h.explorers.Get(ctx, project, explorerID)
	if err != nil {
		return result, err
	}
	if owner.DraftVersion != body.DraftVersion || owner.DraftDigest != body.DraftDigest {
		return result, explorerConflict("reconcile", "DRAFT_CONFLICT", "the Explorer draft changed; reload before reconciling", nil)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return result, explorerConflict("reconcile", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be reconciled", nil)
	}
	return h.compileAuthoringWorkspaceDirect(ctx, project, explorerID, workspace, body.SnapshotToken)
}

func (h *explorerHTTPHandlers) previewAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody) (loomapi.PreviewResponse, error) {
	var result loomapi.PreviewResponse
	started := time.Now()
	previewCtx, cancel := context.WithTimeout(ctx, explorerPreviewTimeout)
	defer cancel()
	responseBytes, responseRows := 0, 0
	logReceiptID, logOutputID := "", ""
	lookupDuration, authorizationDuration := time.Duration(0), time.Duration(0)
	previewSummary := engine.PreviewSummary{}
	outcome := "failure"
	config := h.lifecycleConfig
	defer func() {
		if config.Logger != nil {
			config.Logger.Info("Explorer receipt preview", "operation", "receipt_preview", "project", project, "explorer_id", explorerID, "receipt_id", logReceiptID, "output_id", logOutputID, "outcome", outcome, "duration_ms", time.Since(started).Milliseconds(), "lookup_ms", lookupDuration.Milliseconds(), "authorization_ms", authorizationDuration.Milliseconds(), "lowering_ms", previewSummary.LoweringDuration.Milliseconds(), "query_ms", previewSummary.QueryDuration.Milliseconds(), "plan_mode", previewSummary.PlanMode, "plan_profile", previewSummary.PlanProfile, "plan_fingerprint", previewSummary.PlanFingerprint, "traversal_count", previewSummary.TraversalCount, "rows", responseRows, "bytes", responseBytes)
		}
	}()
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil || strings.TrimSpace(body.ReceiptId) == "" || strings.TrimSpace(body.OutputId) == "" {
		return result, malformedRouteError("preview", errors.New("receiptId and outputId are required"))
	}
	logReceiptID, logOutputID = body.ReceiptId, body.OutputId
	limit := engine.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	if limit < 1 || limit > engine.MaxPreviewLimit {
		return result, authoringSemanticRoute("preview", "$.limit", "INVALID_PREVIEW_LIMIT", "limit must be between 1 and 1000", nil)
	}
	nativeReceiptPreview := config.PreviewReceipt != nil
	if config.AuthorizedCapabilityExecution == nil {
		return result, explorerUnavailable("preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured")
	}
	if config.Preview == nil && config.PreviewReceipt == nil {
		return result, explorerUnavailable("preview", "PREVIEW_UNAVAILABLE", "Explorer preview is not configured")
	}
	lookupStarted := time.Now()
	receipt, err := lookupV2Receipt(previewCtx, h.explorers, config, project, explorerID, body.ReceiptId)
	lookupDuration = time.Since(lookupStarted)
	if err != nil {
		return result, receiptRouteError("preview", err)
	}
	if err := validateV2ReceiptRouteValues(receipt, config, project, explorerID); err != nil {
		return result, err
	}
	authorizationStarted := time.Now()
	authorized, err := config.AuthorizedCapabilityExecution(previewCtx, receipt.Project, receipt.SnapshotToken)
	authorizationDuration = time.Since(authorizationStarted)
	snapshot := authorized.Snapshot
	if err != nil || snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return result, explorerConflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil)
	}
	if config.PreviewReceipt != nil {
		if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
			return result, explorerConflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil)
		}
	}
	if nativeReceiptPreview {
		if !receiptHasOutput(receipt.Bundle, body.OutputId) || validateReceiptOutputContract(receipt, body.OutputId) != nil {
			return result, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
		}
	} else if !receiptHasOutput(receipt.Bundle, body.OutputId) {
		return result, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration, PreviewLimit: limit, OutputNames: []string{body.OutputId}}
	applyAuthorizedScope(&bindings, authorized, false)
	columns := make([]explorer.EmittedColumn, 0)
	for _, column := range receipt.EmittedColumns {
		if column.OutputID == body.OutputId {
			columns = append(columns, column)
		}
	}
	var encoded []byte
	if config.PreviewReceipt != nil {
		encoder, encoderErr := newPreviewResponseEncoder(receipt, body.OutputId, columns, maxExplorerPreviewResponseBytes)
		if encoderErr != nil {
			return result, previewRouteError(encoderErr)
		}
		previewSummary, err = config.PreviewReceipt(previewCtx, receipt, bindings, encoder.Visit)
		responseRows = previewSummary.RowCount
		if err == nil {
			encoded, err = encoder.Finish()
		}
	} else {
		var rows map[string][]map[string]any
		rows, err = config.Preview(previewCtx, receipt.Bundle, bindings)
		if err == nil {
			responseRows = len(rows[body.OutputId])
			encoded, err = encodeExplorerPreviewResponse(receipt, body.OutputId, columns, rows[body.OutputId], maxExplorerPreviewResponseBytes)
		}
	}
	if err != nil {
		return result, previewRouteError(err)
	}
	responseBytes = len(encoded)
	if err := json.Unmarshal(encoded, &result); err != nil {
		return result, err
	}
	outcome = "success"
	return result, nil
}

func (h *explorerHTTPHandlers) publishAuthoringDirect(ctx context.Context, project, explorerID string, body *loomapi.PublishExplorerJSONRequestBody) (loomapi.PublishResponse, error) {
	var result loomapi.PublishResponse
	if err := h.authoringWriteDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil || strings.TrimSpace(body.ReceiptId) == "" {
		return result, malformedRouteError("publish", errors.New("receiptId is required"))
	}
	config := h.lifecycleConfig
	if (config.Materialize == nil && config.MaterializeReceipt == nil) || config.ActivateRelease == nil || config.ValidateReleaseGeneration == nil || (config.CapabilityToken == nil && config.AuthorizedCapabilityExecution == nil) {
		return result, explorerUnavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured")
	}
	receipt, err := lookupV2Receipt(ctx, h.explorers, config, project, explorerID, body.ReceiptId)
	if err != nil {
		return result, receiptRouteError("publish", err)
	}
	if err := validateV2ReceiptRouteValues(receipt, config, project, explorerID); err != nil {
		return result, err
	}
	var snapshot capability.Snapshot
	var authorized AuthorizedCapability
	if config.AuthorizedCapabilityExecution != nil {
		authorized, err = config.AuthorizedCapabilityExecution(ctx, receipt.Project, receipt.SnapshotToken)
		snapshot = authorized.Snapshot
	} else {
		snapshot, err = config.CapabilityToken(ctx, receipt.Project, receipt.SnapshotToken)
	}
	if err != nil || strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return result, explorerConflict("publish", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil)
	}
	if config.CompileReceipt != nil {
		if err := validateReceiptCapability(receipt, snapshot); err != nil {
			return result, explorerConflict("publish", "RECEIPT_STALE", err.Error(), nil)
		}
	}
	if len(receipt.EmittedColumns) == 0 || len(receipt.CompiledConfig) == 0 {
		return result, authoringSemanticRoute("publish", "$.receiptId", "NO_SELECTED_COLUMNS", "select at least one output column before publishing", nil)
	}
	if config.CompileReceipt != nil {
		workspace, decodeErr := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if decodeErr != nil {
			return result, explorerConflict("publish", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt contains invalid authoring intent", nil)
		}
		if err := workspace.ValidateForPublication(); err != nil {
			return result, authoringSemanticRoute("publish", "$.receiptId", "NO_SELECTED_COLUMNS", "select at least one visible output column for every visible table before publishing", nil)
		}
	}
	if err := config.ValidateReleaseGeneration(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {
		return result, explorerConflict("publish", "RECEIPT_STALE", "the receipt generation is no longer active", map[string]any{"generation": receipt.SourceGeneration})
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration}
	applyAuthorizedScope(&bindings, authorized, true)
	var execution graphresolver.RecipeExecution
	if config.MaterializeReceipt != nil {
		execution, err = config.MaterializeReceipt(ctx, receipt, bindings)
	} else {
		execution, err = config.Materialize(ctx, receipt.Bundle, bindings)
	}
	if err != nil {
		var mismatch *receiptContractMismatch
		if errors.As(err, &mismatch) {
			return result, &explorer.AuthoringError{Status: http.StatusConflict, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "publish", Code: "RECEIPT_RECOMPILE_REQUIRED", Message: "receipt deterministic lowering no longer matches the stored artifact", Details: receiptMismatchDetails(receipt.ID, err)}, Cause: err}
		}
		return result, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "materialize", Code: "MATERIALIZATION_FAILED", Message: "Explorer materialization failed; the active revision was retained"}, Cause: err}
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return result, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "materialize", Code: "MATERIALIZATION_FAILED", Message: "materialization did not produce queryable outputs"}, Cause: err}
	}
	if err := config.ActivateRelease(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle)); err != nil {
		return result, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "activation", Code: "MATERIALIZATION_ACTIVATION_FAILED", Message: "dataset release activation failed; the prior Explorer revision was retained"}, Cause: err}
	}
	now := time.Now().UTC()
	revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
	revision, err := h.explorers.PublishAuthoring(ctx, *receipt, explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, ConfigDigest: receipt.IntentDigest, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: receipt.PublicOutputContract, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: explorerMaterializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, Status: explorer.RevisionReady, CreatedBy: subjectFromContext(ctx), CreatedAt: now, ReadyAt: &now})
	if err != nil {
		return result, err
	}
	outputs := make([]loomapi.PublicationOutput, 0, len(revision.Materializations))
	for _, materialization := range revision.Materializations {
		outputs = append(outputs, loomapi.PublicationOutput{OutputId: firstNonEmpty(materialization.OutputID, materialization.Output), State: "READY", MaterializationId: materialization.MaterializationID})
	}
	return loomapi.PublishResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderPublication, ReceiptId: receipt.ID, RevisionId: revision.ID, State: string(revision.Status), Outputs: outputs, Diagnostics: []loomapi.Diagnostic{}}, nil
}

// authoringErrorForOpenAPI preserves the generated ErrorResponse contract and
// keeps internal causes out of the response while retaining request IDs and
// structured diagnostics.
func authoringErrorForOpenAPI(ctx context.Context, operation string, err error) (int, loomapi.ErrorResponse) {
	requestID := requestIDFromContext(ctx)
	status := 500
	code, message, stage, path := "INTERNAL_ERROR", "internal server error", "internal", ""
	var details map[string]any
	var cause error = err
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) {
		status = authoringErr.Status
		code, message, stage, path = authoringErr.Diagnostic.Code, authoringErr.Diagnostic.Message, authoringErr.Diagnostic.Stage, authoringErr.Diagnostic.JSONPath
		details = authoringErr.Diagnostic.Details
		if authoringErr.Cause != nil {
			cause = authoringErr.Cause
		}
	} else if errors.Is(err, explorer.ErrNotFound) {
		status, code, message = 404, "NOT_FOUND", "Explorer resource not found"
	}
	if stage == "" {
		stage = operation
	}
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	if message == "" {
		message = "internal server error"
	}
	if cause != nil && status >= 500 {
		slog.Error("Explorer authoring request failed", "request_id", requestID, "operation", operation, "status", status, "stage", stage, "code", code, "json_path", path, "cause", cause, "cause_type", fmt.Sprintf("%T", cause))
	}
	severity := "error"
	diagnostic := loomapi.Diagnostic{Severity: severity, Stage: stage, Code: code, Message: message}
	if path != "" {
		diagnostic.JsonPath = &path
	}
	if requestID != "" {
		diagnostic.RequestId = &requestID
	}
	if details != nil {
		converted := map[string]interface{}(details)
		diagnostic.Details = &converted
	}
	diagnostics := []loomapi.Diagnostic{diagnostic}
	body := loomapi.ErrorBody{Code: code, Message: message, Diagnostic: &diagnostic}
	if requestID != "" {
		body.RequestId = &requestID
	}
	if details != nil {
		body.AdditionalProperties = map[string]interface{}{"details": details}
	}
	return status, loomapi.ErrorResponse{Error: body, Diagnostics: &diagnostics}
}
