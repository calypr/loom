package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	explorerv2api "github.com/calypr/loom/generated/loomapi"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

type explorerAuthoringHandlers struct {
	getCapability           fiber.Handler
	searchSuggestions       fiber.Handler
	getCandidateSuggestions fiber.Handler
	getBuilder              fiber.Handler
	saveDraft               fiber.Handler
	exportWorkspace         fiber.Handler
	applyCommands           fiber.Handler
	compileBuilder          fiber.Handler
	compile                 fiber.Handler
	reconcile               fiber.Handler
	preview                 fiber.Handler
	publish                 fiber.Handler
}

// newExplorerAuthoringHandlers is the hard-cut traversal Builder contract.
// Generated OpenAPI code owns all method/path registration.
func newExplorerAuthoringHandlers(authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) *explorerAuthoringHandlers {
	handlers := &explorerAuthoringHandlers{}
	if authorizer == nil || authorizeRead == nil || explorers == nil {
		return handlers
	}

	readCapability := func(c fiber.Ctx) (capability.Snapshot, authoringv2.CatalogSnapshot, error) {
		if capabilities.Capability == nil {
			return capability.Snapshot{}, authoringv2.CatalogSnapshot{}, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", "Explorer capability compiler is not configured")
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		snapshot, err := capabilities.Capability(c.Context(), project, id, "")
		if err != nil || !snapshot.Usable() {
			if err == nil {
				err = capability.ErrSnapshotUnavailable
			}
			return capability.Snapshot{}, authoringv2.CatalogSnapshot{}, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error())
		}
		return snapshot, authoringV2Catalog(snapshot, id), nil
	}

	handlers.getCapability = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		return c.JSON(explorerv2api.AuthoringCapability{
			ApiVersion:    explorerv2api.LoomCalyprOrgexplorerAuthoringv2,
			Kind:          explorerv2api.ExplorerAuthoringCapabilities,
			Operations:    []explorerv2api.AuthoringCapabilityOperations{explorerv2api.Builder, explorerv2api.Compile, explorerv2api.Draft, explorerv2api.Export, explorerv2api.Suggestions, explorerv2api.Preview, explorerv2api.Publish, explorerv2api.Commands, explorerv2api.Reconcile},
			PreviewLimits: []int{10, 25, 50, 100},
			Features:      explorerv2api.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true},
		})
	}

	handlers.searchSuggestions = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		var request explorerv2api.CandidateSearchRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.NodeId) == "" {
			if err == nil {
				err = fmt.Errorf("snapshotToken and nodeId are required")
			}
			return authoringHTTPError(c, malformedRouteError("suggestions", err))
		}
		if capabilities.CapabilityToken == nil {
			return authoringHTTPError(c, explorerUnavailable("suggestions", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured"))
		}
		snapshot, err := capabilities.CapabilityToken(c.Context(), explorerProjectParam(c), request.SnapshotToken)
		if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
			return authoringHTTPError(c, explorerConflict("suggestions", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale", nil))
		}
		query := ""
		if request.Query != nil {
			query = strings.ToLower(strings.TrimSpace(*request.Query))
		}
		candidates := []authoringv2.CatalogCandidate{}
		wire := authoringV2Catalog(snapshot, strings.TrimSpace(c.Params("explorerId")))
		for _, candidate := range wire.Candidates {
			if candidate.NodeID == request.NodeId && (query == "" || strings.Contains(strings.ToLower(candidate.Label), query) || strings.Contains(strings.ToLower(candidate.ID), query)) {
				candidates = append(candidates, candidate)
			}
		}
		return c.JSON(fiber.Map{"apiVersion": authoringv2.APIVersion, "kind": "ExplorerBuilderCandidateSuggestions", "snapshotToken": request.SnapshotToken, "nodeId": request.NodeId, "candidates": candidates, "diagnostics": []any{}})
	}

	// Suggested values stay out of the main capability document. They are
	// bounded during capability construction and fetched only when a user opens
	// a field control, while retaining the exact snapshot-token semantics used
	// by compilation.
	handlers.getCandidateSuggestions = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		if capabilities.CapabilityToken == nil {
			return authoringHTTPError(c, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured"))
		}
		project := explorerProjectParam(c)
		token, candidateID := strings.TrimSpace(c.Params("snapshotToken")), strings.TrimSpace(c.Params("candidateId"))
		if token == "" || candidateID == "" {
			return authoringHTTPError(c, malformedRouteError("capability", fmt.Errorf("snapshotToken and candidateId are required")))
		}
		snapshot, err := capabilities.CapabilityToken(c.Context(), project, token)
		if err != nil {
			return authoringHTTPError(c, explorerConflict("capability", "STALE_CATALOG_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": token}))
		}
		for _, candidate := range snapshot.Candidates {
			if candidate.ID != candidateID {
				continue
			}
			return c.JSON(explorerv2api.CandidateSuggestions{
				ApiVersion:    explorerv2api.LoomCalyprOrgexplorerAuthoringv2,
				Kind:          explorerv2api.ExplorerCandidateSuggestions,
				SnapshotToken: token, CandidateId: candidateID,
				Values:   append([]string(nil), candidate.SuggestedValues...),
				Complete: candidate.SuggestionsComplete, Truncated: candidate.SuggestionsTruncated,
			})
		}
		return authoringHTTPError(c, &explorer.AuthoringError{Status: http.StatusNotFound, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "capability", Code: "CANDIDATE_NOT_FOUND", Message: "candidate is not present in the capability snapshot"}})
	}

	handlers.getBuilder = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		_, wire, err := readCapability(c)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		owner, err := explorers.Get(c.Context(), project, id)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, LifecycleState: authoringv2.LifecycleNew, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Catalog: wire}
		var activeWorkspace *authoringv2.Workspace
		if owner.ActiveRevisionID != "" {
			active, activeErr := explorers.ActiveRevision(c.Context(), project, id)
			if activeErr != nil {
				return authoringHTTPError(c, explorerConflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision cannot be loaded as V2 authoring state", map[string]any{"revisionId": owner.ActiveRevisionID}))
			}
			workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle)
			if decodeErr != nil {
				return authoringHTTPError(c, explorerConflict("builder", "AUTHORING_STATE_MISSING", "the active Explorer revision has no valid V2 workspace", map[string]any{"revisionId": active.ID}))
			}
			activeWorkspace = &workspace
		}
		if len(owner.DraftConfig) != 0 {
			workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
			if decodeErr != nil {
				return authoringHTTPError(c, explorerConflict("builder", "DRAFT_STATE_INVALID", "the saved Explorer draft is not a valid V2 workspace", map[string]any{"draftVersion": owner.DraftVersion}))
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
			return authoringHTTPError(c, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error()))
		}
		return c.JSON(state)
	}

	handlers.saveDraft = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request struct {
			Workspace            authoringv2.Workspace `json:"workspace"`
			SnapshotToken        string                `json:"snapshotToken"`
			ExpectedDraftVersion *int64                `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string                `json:"expectedDraftDigest,omitempty"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || request.ExpectedDraftVersion == nil || strings.TrimSpace(request.SnapshotToken) == "" {
			if err == nil {
				err = fmt.Errorf("workspace, snapshotToken, and expectedDraftVersion are required")
			}
			return authoringHTTPError(c, malformedRouteError("draft", err))
		}
		if capabilities.CapabilityToken == nil {
			return authoringHTTPError(c, explorerUnavailable("draft", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured"))
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		snapshot, err := capabilities.CapabilityToken(c.Context(), project, request.SnapshotToken)
		if err != nil {
			return authoringHTTPError(c, explorerConflict("draft", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil))
		}
		state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, LifecycleState: authoringv2.LifecycleReady, Workspace: &request.Workspace, Catalog: authoringV2Catalog(snapshot, id)}
		if err := state.Validate(); err != nil {
			return authoringHTTPError(c, authoringSemanticRoute("draft", "$", workspaceValidationCode(err), err.Error(), nil))
		}
		stored, err := explorers.SaveWorkspaceDraft(c.Context(), project, id, request.Workspace, *request.ExpectedDraftVersion, request.ExpectedDraftDigest, subjectFromFiber(c))
		if errors.Is(err, explorer.ErrDraftConflict) {
			return authoringHTTPError(c, explorerConflict("draft", "DRAFT_CONFLICT", "the Explorer draft changed; reload before saving", nil))
		}
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(fiber.Map{"workspace": json.RawMessage(stored.DraftConfig), "draftVersion": stored.DraftVersion, "draftDigest": stored.DraftDigest})
	}

	handlers.exportWorkspace = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		owner, err := explorers.Get(c.Context(), project, id)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		raw := owner.DraftConfig
		if len(raw) == 0 && owner.ActiveRevisionID != "" {
			active, activeErr := explorers.ActiveRevision(c.Context(), project, id)
			if activeErr != nil {
				return authoringHTTPError(c, explorerConflict("export", "AUTHORING_STATE_MISSING", "the active Explorer revision cannot be exported", nil))
			}
			raw = active.AuthoringBundle
		}
		workspace, err := authoringv2.DecodeWorkspace(raw)
		if err != nil {
			return authoringHTTPError(c, explorerConflict("export", "AUTHORING_STATE_MISSING", "no canonical V2 workspace is available for export", nil))
		}
		canonical, err := workspace.CanonicalJSON()
		if err != nil {
			return authoringHTTPError(c, err)
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+id+`.explorer-workspace.json"`)
		return c.Send(canonical)
	}

	handlers.applyCommands = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request authoringv2.ApplyCommandsRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("commands", err))
		}
		if err := request.Validate(); err != nil {
			return authoringHTTPError(c, malformedRouteError("commands", err))
		}
		if capabilities.CapabilityToken == nil {
			return authoringHTTPError(c, explorerUnavailable("commands", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured"))
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		snapshot, err := capabilities.CapabilityToken(c.Context(), project, request.SnapshotToken)
		if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
			return authoringHTTPError(c, explorerConflict("commands", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil))
		}
		response, err := explorers.ApplyWorkspaceCommands(c.Context(), project, id, authoringV2Catalog(snapshot, id), request, subjectFromFiber(c))
		switch {
		case errors.Is(err, explorer.ErrDraftConflict):
			return authoringHTTPError(c, explorerConflict("commands", "DRAFT_CONFLICT", "the Explorer draft changed; reload before editing", nil))
		case errors.Is(err, explorer.ErrAuthoringCommandConflict):
			return authoringHTTPError(c, explorerConflict("commands", "COMMAND_ID_CONFLICT", "commandId was already used for different intent", nil))
		case err != nil:
			return authoringHTTPError(c, authoringSemanticRoute("commands", "$.commands", "INVALID_AUTHORING_COMMAND", err.Error(), nil))
		default:
			return c.JSON(response)
		}
	}

	compileWorkspace := func(c fiber.Ctx, workspace authoringv2.Workspace, snapshotToken string) error {
		if (capabilities.CapabilityToken == nil && capabilities.AuthorizedCapabilityCompile == nil) || capabilities.CompileReceipt == nil {
			return authoringHTTPError(c, explorerUnavailable("compile", "CAPABILITY_UNAVAILABLE", "Explorer V2 compiler is not configured"))
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		var err error
		var snapshot capability.Snapshot
		var authorized AuthorizedCapability
		if capabilities.AuthorizedCapabilityCompile != nil {
			authorized, err = capabilities.AuthorizedCapabilityCompile(c.Context(), project, snapshotToken)
			snapshot = authorized.Snapshot
		} else {
			snapshot, err = capabilities.CapabilityToken(c.Context(), project, snapshotToken)
		}
		if err != nil {
			return authoringHTTPError(c, explorerConflict("capability", "STALE_CATALOG_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": snapshotToken}))
		}
		wire := authoringV2Catalog(snapshot, id)
		state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: wire}
		if err := state.Validate(); err != nil {
			return authoringHTTPError(c, authoringSemanticRoute("intent", "$", workspaceValidationCode(err), err.Error(), nil))
		}
		stored, err := capabilities.CompileReceipt(c.Context(), ExplorerV2ReceiptCompileRequest{Project: project, ExplorerID: id, Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: requestIDFromFiber(c), Authorized: authorized})
		if err != nil {
			var compileErr *explorercompilation.Error
			if errors.As(err, &compileErr) {
				return authoringHTTPError(c, authoringSemanticRoute(compileErr.Stage, compileErr.Path, compilationErrorCode(compileErr.Code), compileErr.Message, compileErr.Details))
			}
			return authoringHTTPError(c, err)
		}
		if stored == nil || strings.TrimSpace(stored.ID) == "" {
			return authoringHTTPError(c, explorerUnavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "compiled authoring receipt was not persisted"))
		}
		return c.JSON(v2ReceiptResponse(stored, workspace))
	}
	compileHandler := func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request explorerv2api.CompileRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("intent", err))
		}
		return compileWorkspace(c, request.Workspace, request.SnapshotToken)
	}
	handlers.compileBuilder = compileHandler
	handlers.compile = compileHandler
	handlers.reconcile = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request struct {
			SnapshotToken string `json:"snapshotToken"`
			DraftVersion  int64  `json:"draftVersion"`
			DraftDigest   string `json:"draftDigest"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 0 {
			if err == nil {
				err = fmt.Errorf("snapshotToken, draftVersion, and draftDigest are required")
			}
			return authoringHTTPError(c, malformedRouteError("reconcile", err))
		}
		owner, err := explorers.Get(c.Context(), explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")))
		if err != nil {
			return authoringHTTPError(c, err)
		}
		if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
			return authoringHTTPError(c, explorerConflict("reconcile", "DRAFT_CONFLICT", "the Explorer draft changed; reload before reconciling", nil))
		}
		workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if err != nil {
			return authoringHTTPError(c, explorerConflict("reconcile", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be reconciled", nil))
		}
		return compileWorkspace(c, workspace, request.SnapshotToken)
	}

	handlers.preview = func(c fiber.Ctx) error {
		started := time.Now()
		previewCtx, cancel := context.WithTimeout(c.Context(), explorerPreviewTimeout)
		defer cancel()
		responseBytes, responseRows := 0, 0
		logReceiptID, logOutputID := "", ""
		lookupDuration, authorizationDuration := time.Duration(0), time.Duration(0)
		previewSummary := engine.PreviewSummary{}
		outcome := "failure"
		defer func() {
			if capabilities.Logger != nil {
				capabilities.Logger.Info("Explorer receipt preview", "operation", "receipt_preview", "project", explorerProjectParam(c), "explorer_id", strings.TrimSpace(c.Params("explorerId")), "receipt_id", logReceiptID, "output_id", logOutputID, "outcome", outcome, "duration_ms", time.Since(started).Milliseconds(), "lookup_ms", lookupDuration.Milliseconds(), "authorization_ms", authorizationDuration.Milliseconds(), "lowering_ms", previewSummary.LoweringDuration.Milliseconds(), "query_ms", previewSummary.QueryDuration.Milliseconds(), "plan_mode", previewSummary.PlanMode, "plan_profile", previewSummary.PlanProfile, "plan_fingerprint", previewSummary.PlanFingerprint, "traversal_count", previewSummary.TraversalCount, "rows", responseRows, "bytes", responseBytes)
			}
		}()
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		var request explorerv2api.PreviewRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.ReceiptId) == "" || strings.TrimSpace(request.OutputId) == "" {
			if err == nil {
				err = fmt.Errorf("receiptId and outputId are required")
			}
			return authoringHTTPError(c, malformedRouteError("preview", err))
		}
		logReceiptID, logOutputID = request.ReceiptId, request.OutputId
		limit := engine.DefaultPreviewLimit
		if request.Limit != nil {
			limit = *request.Limit
		}
		if limit < 1 || limit > engine.MaxPreviewLimit {
			return authoringHTTPError(c, authoringSemanticRoute("preview", "$.limit", "INVALID_PREVIEW_LIMIT", "limit must be between 1 and 1000", nil))
		}
		nativeReceiptPreview := capabilities.PreviewReceipt != nil
		if capabilities.AuthorizedCapabilityExecution == nil {
			return authoringHTTPError(c, explorerUnavailable("preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured"))
		}
		if capabilities.Preview == nil && capabilities.PreviewReceipt == nil {
			return authoringHTTPError(c, explorerUnavailable("preview", "PREVIEW_UNAVAILABLE", "Explorer preview is not configured"))
		}
		lookupStarted := time.Now()
		receipt, err := lookupV2Receipt(previewCtx, explorers, capabilities, explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")), request.ReceiptId)
		lookupDuration = time.Since(lookupStarted)
		if err != nil {
			return authoringHTTPError(c, receiptRouteError("preview", err))
		}
		if err := validateV2ReceiptRoute(c, receipt, capabilities); err != nil {
			return authoringHTTPError(c, err)
		}
		authorizationStarted := time.Now()
		authorized, err := capabilities.AuthorizedCapabilityExecution(previewCtx, receipt.Project, receipt.SnapshotToken)
		authorizationDuration = time.Since(authorizationStarted)
		snapshot := authorized.Snapshot
		if err != nil || snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
			return authoringHTTPError(c, explorerConflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil))
		}
		if capabilities.PreviewReceipt != nil {
			if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
				return authoringHTTPError(c, explorerConflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil))
			}
		}
		if nativeReceiptPreview {
			if !receiptHasOutput(receipt.Bundle, request.OutputId) {
				return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil))
			}
			if err := validateReceiptOutputContract(receipt, request.OutputId); err != nil {
				return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil))
			}
		} else if !receiptHasOutput(receipt.Bundle, request.OutputId) {
			return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil))
		}
		bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration, PreviewLimit: limit, OutputNames: []string{request.OutputId}}
		applyAuthorizedScope(&bindings, authorized, false)
		columns := make([]explorer.EmittedColumn, 0)
		for _, column := range receipt.EmittedColumns {
			if column.OutputID == request.OutputId {
				columns = append(columns, column)
			}
		}
		var encoded []byte
		if capabilities.PreviewReceipt != nil {
			encoder, encoderErr := newPreviewResponseEncoder(receipt, request.OutputId, columns, maxExplorerPreviewResponseBytes)
			if encoderErr != nil {
				return previewRouteFailure(c, encoderErr)
			}
			var streamErr error
			previewSummary, streamErr = capabilities.PreviewReceipt(previewCtx, receipt, bindings, encoder.Visit)
			err = streamErr
			responseRows = previewSummary.RowCount
			if err == nil {
				encoded, err = encoder.Finish()
			}
		} else {
			var rows map[string][]map[string]any
			rows, err = capabilities.Preview(previewCtx, receipt.Bundle, bindings)
			if err == nil {
				responseRows = len(rows[request.OutputId])
				encoded, err = encodeExplorerPreviewResponse(receipt, request.OutputId, columns, rows[request.OutputId], maxExplorerPreviewResponseBytes)
			}
		}
		if err != nil {
			return previewRouteFailure(c, err)
		}
		responseBytes = len(encoded)
		outcome = "success"
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(encoded)
	}

	handlers.publish = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request explorerv2api.PublishRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.ReceiptId) == "" {
			if err == nil {
				err = fmt.Errorf("receiptId is required")
			}
			return authoringHTTPError(c, malformedRouteError("publish", err))
		}
		if (capabilities.Materialize == nil && capabilities.MaterializeReceipt == nil) || capabilities.ActivateRelease == nil || capabilities.ValidateReleaseGeneration == nil || (capabilities.CapabilityToken == nil && capabilities.AuthorizedCapabilityExecution == nil) {
			return authoringHTTPError(c, explorerUnavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured"))
		}
		receipt, err := lookupV2Receipt(c.Context(), explorers, capabilities, explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")), request.ReceiptId)
		if err != nil {
			return authoringHTTPError(c, receiptRouteError("publish", err))
		}
		if err := validateV2ReceiptRoute(c, receipt, capabilities); err != nil {
			return authoringHTTPError(c, err)
		}
		var snapshot capability.Snapshot
		var authorized AuthorizedCapability
		if capabilities.AuthorizedCapabilityExecution != nil {
			authorized, err = capabilities.AuthorizedCapabilityExecution(c.Context(), receipt.Project, receipt.SnapshotToken)
			snapshot = authorized.Snapshot
		} else {
			snapshot, err = capabilities.CapabilityToken(c.Context(), receipt.Project, receipt.SnapshotToken)
		}
		if err != nil || strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
			return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil))
		}
		if capabilities.CompileReceipt != nil {
			if err := validateReceiptCapability(receipt, snapshot); err != nil {
				return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_STALE", err.Error(), nil))
			}
		}
		if len(receipt.EmittedColumns) == 0 || len(receipt.CompiledConfig) == 0 {
			return authoringHTTPError(c, authoringSemanticRoute("publish", "$.receiptId", "NO_SELECTED_COLUMNS", "select at least one output column before publishing", nil))
		}
		if capabilities.CompileReceipt != nil {
			workspace, decodeErr := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
			if decodeErr != nil {
				return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt contains invalid authoring intent", nil))
			}
			if err := workspace.ValidateForPublication(); err != nil {
				return authoringHTTPError(c, authoringSemanticRoute("publish", "$.receiptId", "NO_SELECTED_COLUMNS", "select at least one visible output column for every visible table before publishing", nil))
			}
		}
		if err := capabilities.ValidateReleaseGeneration(c.Context(), projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {
			return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_STALE", "the receipt generation is no longer active", map[string]any{"generation": receipt.SourceGeneration}))
		}
		bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration}
		applyAuthorizedScope(&bindings, authorized, true)
		var execution graphresolver.RecipeExecution
		if capabilities.MaterializeReceipt != nil {
			execution, err = capabilities.MaterializeReceipt(c.Context(), receipt, bindings)
		} else {
			execution, err = capabilities.Materialize(c.Context(), receipt.Bundle, bindings)
		}
		if err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "materialize", Code: "MATERIALIZATION_FAILED", Message: "Explorer materialization failed; the active revision was retained"}, Cause: err})
		}
		if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "materialize", Code: "MATERIALIZATION_FAILED", Message: "materialization did not produce queryable outputs"}, Cause: err})
		}
		if err := capabilities.ActivateRelease(c.Context(), projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle)); err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "activation", Code: "MATERIALIZATION_ACTIVATION_FAILED", Message: "dataset release activation failed; the prior Explorer revision was retained"}, Cause: err})
		}
		now := time.Now().UTC()
		revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
		revision, err := explorers.PublishAuthoring(c.Context(), *receipt, explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, ConfigDigest: receipt.IntentDigest, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, PublicOutputContract: receipt.PublicOutputContract, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: explorerMaterializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, Status: explorer.RevisionReady, CreatedBy: subjectFromFiber(c), CreatedAt: now, ReadyAt: &now})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		outputs := make([]explorerv2api.PublicationOutput, 0, len(revision.Materializations))
		for _, materialization := range revision.Materializations {
			outputs = append(outputs, explorerv2api.PublicationOutput{OutputId: firstNonEmpty(materialization.OutputID, materialization.Output), State: "READY", MaterializationId: materialization.MaterializationID})
		}
		return c.JSON(explorerv2api.PublishResponse{
			ApiVersion:  explorerv2api.LoomCalyprOrgexplorerAuthoringv2,
			Kind:        explorerv2api.ExplorerBuilderPublication,
			ReceiptId:   receipt.ID,
			RevisionId:  revision.ID,
			State:       string(revision.Status),
			Outputs:     outputs,
			Diagnostics: []explorerv2api.Diagnostic{},
		})
	}
	return handlers
}

func validateV2ReceiptRoute(c fiber.Ctx, receipt *explorer.CompilationReceipt, capabilities ExplorerV2LifecycleConfig) error {
	if receipt == nil || receipt.Project != explorerProjectParam(c) || receipt.ExplorerID != strings.TrimSpace(c.Params("explorerId")) {
		return &explorer.AuthoringError{Status: http.StatusNotFound, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "receipt", Code: "COMPILE_RECEIPT_NOT_FOUND", Message: "compilation receipt was not found"}}
	}
	if strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.RecipeDigest) == "" || len(receipt.Bundle.Outputs) == 0 {
		return explorerConflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported or incomplete compiler contract", nil)
	}
	nativeReceiptContract := capabilities.CompileReceipt != nil || capabilities.PreviewReceipt != nil || capabilities.MaterializeReceipt != nil
	if nativeReceiptContract && (receipt.ReceiptFormatVersion != explorer.CurrentReceiptFormatVersion || receipt.CompilerContractVersion != explorer.CurrentCompilerContractVersion) {
		return explorerConflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported compiler contract", nil)
	}
	if nativeReceiptContract {
		if err := receipt.Validate(); err != nil {
			return explorerConflict("receipt", "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt failed integrity validation and must be recompiled", nil)
		}
	}
	return nil
}

func lookupV2Receipt(ctx context.Context, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
	if capabilities.ReceiptLookup != nil {
		return capabilities.ReceiptLookup(ctx, project, explorerID, receiptID)
	}
	if explorers == nil {
		return nil, explorer.ErrNotFound
	}
	return explorers.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
}

func receiptRouteError(stage string, err error) error {
	if errors.Is(err, explorer.ErrNotFound) {
		return &explorer.AuthoringError{Status: http.StatusNotFound, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: "COMPILE_RECEIPT_NOT_FOUND", Message: "compilation receipt was not found"}}
	}
	if errors.Is(err, explorer.ErrReceiptRecompileRequired) {
		return explorerConflict(stage, "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported compiler contract", nil)
	}
	return explorerUnavailable(stage, "RECEIPT_STORE_UNAVAILABLE", "the compilation receipt store is unavailable")
}

func workspaceValidationCode(err error) string {
	message := err.Error()
	for _, code := range []string{"DUPLICATE_OUTPUT_ID", "DUPLICATE_TAB_ID", "INVALID_TAB_OUTPUT_MAPPING", "INVALID_TAB_ORDER", "ROW_ROOT_NOT_ELIGIBLE", "UNSUPPORTED_FILTER", "UNSUPPORTED_CHART", "NO_VISIBLE_COLUMNS"} {
		if strings.Contains(message, code) {
			return code
		}
	}
	switch {
	case strings.Contains(message, "rootNodeId"):
		return "INVALID_ROOT_NODE"
	case strings.Contains(message, "route") || strings.Contains(message, "edge"):
		return "INVALID_ROUTE"
	case strings.Contains(message, "occurrence"):
		return "INVALID_OCCURRENCE"
	case strings.Contains(message, "projection mode"):
		return "INVALID_PROJECTION_MODE"
	case strings.Contains(message, "duplicate selection"):
		return "DUPLICATE_SELECTION"
	default:
		return "INVALID_AUTHORING_INTENT"
	}
}

func compilationErrorCode(code string) string {
	switch code {
	case "STALE_ROOT_NODE":
		return "INVALID_ROOT_NODE"
	case "ROOT_NOT_ELIGIBLE", "UNSUPPORTED_ROW_ROOT":
		return "ROW_ROOT_NOT_ELIGIBLE"
	case "STALE_EDGE":
		return "STALE_EDGE_ID"
	case "DISCONNECTED_ROUTE", "REPEATED_EDGE_NOT_ALLOWED", "SELF_LOOP_NOT_ALLOWED", "ROUTE_TOO_LONG":
		return "INVALID_ROUTE"
	case "STALE_CANDIDATE":
		return "STALE_CANDIDATE_ID"
	case "STALE_OCCURRENCE", "DUPLICATE_OCCURRENCE":
		return "INVALID_OCCURRENCE"
	case "UNSUPPORTED_PROJECTION_MODE":
		return "INVALID_PROJECTION_MODE"
	default:
		return code
	}
}

func applyAuthorizedScope(bindings *recipe.RuntimeBindings, authorized AuthorizedCapability, includeAuthResourcePath bool) {
	if bindings == nil {
		return
	}
	bindings.AuthResourcePaths = append([]string(nil), authorized.Scope.AuthResourcePaths...)
	bindings.AuthScopeMode = authorized.Scope.Mode
	bindings.IncludeAuthResourcePath = includeAuthResourcePath
}

func v2ReceiptResponse(receipt *explorer.CompilationReceipt, workspace authoringv2.Workspace) explorerv2api.CompileResponse {
	if receipt != nil && len(receipt.NormalizedBundle) != 0 {
		if normalized, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle); err == nil {
			workspace = normalized
		}
	}
	outputs := make([]explorerv2api.ReceiptOutput, 0, len(workspace.Documents))
	for _, document := range workspace.Documents {
		rowGrain := ""
		for _, output := range receipt.Bundle.Outputs {
			if output.Name == document.Output.ID {
				rowGrain = output.RowGrain
				break
			}
		}
		columns := []explorerv2api.ContractColumn{}
		for _, column := range receipt.EmittedColumns {
			if column.OutputID != document.Output.ID {
				continue
			}
			label := column.Label
			if label == "" {
				label = column.PublicColumn
			}
			columns = append(columns, explorerv2api.ContractColumn{Column: column.PublicColumn, Label: label, LogicalType: column.LogicalType, Filterable: column.Filterable, Chartable: column.Chartable})
		}
		outputs = append(outputs, explorerv2api.ReceiptOutput{OutputId: document.Output.ID, Title: document.Output.Title, RowGrain: rowGrain, Columns: columns})
	}
	return explorerv2api.CompileResponse{
		ApiVersion: explorerv2api.LoomCalyprOrgexplorerAuthoringv2,
		Kind:       explorerv2api.ExplorerBuilderReceipt,
		ReceiptId:  receipt.ID, SnapshotToken: receipt.SnapshotToken,
		Generation: receipt.SourceGeneration, IntentDigest: receipt.IntentDigest,
		CompilerVersion: explorer.CurrentCompilerContractVersion + "+" + explorercompilation.TranslationVersion, Builder: workspace,
		Outputs: outputs, Diagnostics: []explorerv2api.Diagnostic{},
	}
}
