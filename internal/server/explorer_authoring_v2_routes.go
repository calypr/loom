package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	explorerv2api "github.com/calypr/loom/generated/explorerv2"
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

// RegisterExplorerAuthoringV2Routes is the hard-cut traversal Builder
// contract. V1 remains an internal stored-document migration format and is
// deliberately not registered as an HTTP surface.
func RegisterExplorerAuthoringV2Routes(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil {
		return
	}
	prefix := "/api/v1/projects/:project/explorers/:explorerId/authoring/v2"

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

	app.Get(prefix+"/capability", func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		return c.JSON(explorerv2api.AuthoringCapability{
			ApiVersion:    explorerv2api.LoomCalyprOrgexplorerAuthoringv2,
			Kind:          explorerv2api.ExplorerAuthoringCapabilities,
			Operations:    []explorerv2api.AuthoringCapabilityOperations{explorerv2api.Builder, explorerv2api.Compile, explorerv2api.Suggestions, explorerv2api.Preview, explorerv2api.Publish},
			PreviewLimits: []int{10, 25, 50, 100},
			Features:      explorerv2api.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true},
		})
	})

	app.Post(prefix+"/suggestions", func(c fiber.Ctx) error {
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
	})

	// Suggested values stay out of the main capability document. They are
	// bounded during capability construction and fetched only when a user opens
	// a field control, while retaining the exact snapshot-token semantics used
	// by compilation.
	app.Get(prefix+"/capabilities/:snapshotToken/candidates/:candidateId/suggestions", func(c fiber.Ctx) error {
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
	})

	app.Get(prefix+"/builder", func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		snapshot, wire, err := readCapability(c)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		owner, err := explorers.Get(c.Context(), project, id)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		_ = owner
		state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Catalog: wire}
		if active, activeErr := explorers.ActiveRevision(c.Context(), project, id); activeErr == nil && len(active.AuthoringBundle) != 0 {
			if workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle); decodeErr == nil {
				state.Workspace = &workspace
			} else if document, decodeErr := authoringv2.DecodeDocument(active.AuthoringBundle); decodeErr == nil {
				document.APIVersion = ""
				workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Documents: []authoringv2.Document{document}, Tabs: []authoringv2.Tab{{ID: document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: 0, Visible: true}}}
				state.Workspace = &workspace
			} else if bundle, decodeErr := explorer.DecodeAuthoringBundleV1ForMigration(active.AuthoringBundle); decodeErr == nil {
				workspace, migrateErr := migrateV1WorkspaceToCapability(bundle, snapshot, wire, active.EmittedColumns)
				if migrateErr != nil {
					return authoringHTTPError(c, authoringSemanticRoute("migration", "$", "MIGRATION_FAILED", migrateErr.Error(), nil))
				}
				state.Workspace = &workspace
			}
		} else if activeErr != nil && !errors.Is(activeErr, explorer.ErrNotFound) {
			return authoringHTTPError(c, activeErr)
		}
		if err := state.Validate(); err != nil {
			return authoringHTTPError(c, explorerUnavailable("capability", "CAPABILITY_UNAVAILABLE", err.Error()))
		}
		return c.JSON(state)
	})

	compileHandler := func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request explorerv2api.CompileRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("intent", err))
		}
		if (capabilities.CapabilityToken == nil && capabilities.AuthorizedCapabilityCompile == nil) || (capabilities.CompileReceipt == nil && capabilities.AuthoringCompile == nil) {
			return authoringHTTPError(c, explorerUnavailable("compile", "CAPABILITY_UNAVAILABLE", "Explorer V2 compiler is not configured"))
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		var err error
		var snapshot capability.Snapshot
		var authorized AuthorizedCapability
		if capabilities.AuthorizedCapabilityCompile != nil {
			authorized, err = capabilities.AuthorizedCapabilityCompile(c.Context(), project, request.SnapshotToken)
			snapshot = authorized.Snapshot
		} else {
			snapshot, err = capabilities.CapabilityToken(c.Context(), project, request.SnapshotToken)
		}
		if err != nil {
			return authoringHTTPError(c, explorerConflict("capability", "STALE_CATALOG_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": request.SnapshotToken}))
		}
		wire := authoringV2Catalog(snapshot, id)
		state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &request.Workspace, Catalog: wire}
		if err := state.Validate(); err != nil {
			return authoringHTTPError(c, authoringSemanticRoute("intent", "$", workspaceValidationCode(err), err.Error(), nil))
		}
		var stored *explorer.CompilationReceipt
		nativeReceipt := capabilities.CompileReceipt != nil
		if nativeReceipt {
			stored, err = capabilities.CompileReceipt(c.Context(), ExplorerV2ReceiptCompileRequest{Project: project, ExplorerID: id, Workspace: request.Workspace, SnapshotToken: snapshot.Token, RequestID: requestIDFromFiber(c), Authorized: authorized})
		} else {
			// Compatibility path for migration-only callers. Production wiring
			// supplies CompileReceipt and never enters this V1 adapter.
			bundle, bundleErr := authoringV2WorkspaceBundle(project, id, request.Workspace, wire)
			if bundleErr != nil {
				return authoringHTTPError(c, authoringSemanticRoute("intent", "$", "INVALID_AUTHORING_INTENT", bundleErr.Error(), nil))
			}
			resolved, compileErr := capabilities.AuthoringCompile(c.Context(), ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token, RequestID: requestIDFromFiber(c)})
			if compileErr == nil {
				stored = &resolved.Receipt
			}
			err = compileErr
		}
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
		// The native compiler owns durable insert/readback so an idempotent hit is
		// one tenant-scoped lookup. The V1 migration adapter still needs the route
		// to persist its legacy receipt.
		if !nativeReceipt {
			stored, err = explorers.StoreCompilationReceipt(c.Context(), *stored)
			if err != nil {
				return authoringHTTPError(c, explorerUnavailable("compile", "COMPILATION_RECEIPT_STORE_FAILED", "compiled authoring receipt could not be persisted"))
			}
		}
		return c.JSON(v2ReceiptResponse(stored, request.Workspace))
	}
	app.Post(prefix+"/builder", compileHandler)
	app.Post(prefix+"/compile", compileHandler)

	app.Post(prefix+"/preview", func(c fiber.Ctx) error {
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
	})

	app.Post(prefix+"/publish", func(c fiber.Ctx) error {
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
		revision, err := explorers.PublishAuthoring(c.Context(), *receipt, explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, ConfigDigest: receipt.IntentDigest, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: explorerMaterializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, Status: explorer.RevisionReady, CreatedBy: subjectFromFiber(c), CreatedAt: now, ReadyAt: &now})
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
	})
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

func authoringV2Bundle(project, explorerID string, document authoringv2.Document, catalog authoringv2.CatalogSnapshot) (explorer.ExplorerAuthoringBundleV1, error) {
	occurrences, err := document.Occurrences(catalog)
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, err
	}
	v1 := explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: document.Output.ID, Title: document.Output.Title},
		BaseNodeID: document.RootNodeID, RowNodeID: occurrences[len(occurrences)-1].NodeID,
		Presentation: map[string]explorer.ExplorerPresentationBindingV1{},
	}
	for _, step := range document.RouteSteps {
		v1.RouteEdgeIDs = append(v1.RouteEdgeIDs, step.EdgeID)
	}
	for i, occurrence := range occurrences {
		v1.RouteOccurrences = append(v1.RouteOccurrences, explorer.ExplorerRouteOccurrenceV1{ID: occurrence.ID, Index: i, NodeID: occurrence.NodeID, IncomingEdgeID: occurrence.IncomingEdgeID})
	}
	seenCandidates := map[string]bool{}
	for _, selection := range document.Selections {
		if !seenCandidates[selection.CandidateID] {
			v1.CandidateIDs = append(v1.CandidateIDs, selection.CandidateID)
			seenCandidates[selection.CandidateID] = true
		}
		occurrenceID := selection.OccurrenceID
		if occurrenceID == "" {
			occurrenceID = occurrences[len(occurrences)-1].ID
		}
		v1.CandidateOccurrences = append(v1.CandidateOccurrences, explorer.ExplorerCandidateOccurrenceV1{CandidateID: selection.CandidateID, OccurrenceID: occurrenceID, ProjectionMode: selection.ProjectionMode})
	}
	for key, presentation := range document.Presentation {
		v1.Presentation[key] = explorer.ExplorerPresentationBindingV1{Label: presentation.Label, Visible: presentation.Visible, Order: presentation.Order}
	}
	return explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: projectid.Canonical(project), ExplorerID: explorerID, Title: document.Output.Title, Documents: []explorer.ExplorerBuilderDocumentV1{v1}, Tabs: []explorer.ExplorerTabV1{}}, nil
}

func authoringV2WorkspaceBundle(project, explorerID string, workspace authoringv2.Workspace, catalog authoringv2.CatalogSnapshot) (explorer.ExplorerAuthoringBundleV1, error) {
	result := explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: projectid.Canonical(project), ExplorerID: explorerID, Documents: []explorer.ExplorerBuilderDocumentV1{}, Tabs: []explorer.ExplorerTabV1{}}
	for _, document := range workspace.Documents {
		one, err := authoringV2Bundle(project, explorerID, document, catalog)
		if err != nil {
			return result, err
		}
		result.Documents = append(result.Documents, one.Documents...)
	}
	for _, tab := range workspace.Tabs {
		visible := tab.Visible
		result.Tabs = append(result.Tabs, explorer.ExplorerTabV1{ID: tab.ID, Title: tab.Title, OutputID: tab.OutputID, Order: tab.Order, Visible: &visible})
	}
	return result, nil
}

func migrateV1WorkspaceToCapability(bundle explorer.ExplorerAuthoringBundleV1, snapshot capability.Snapshot, wire authoringv2.CatalogSnapshot, emitted []explorer.EmittedColumn) (authoringv2.Workspace, error) {
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}}
	for i, input := range bundle.AuthoringDocuments() {
		if len(input.Presentation) > 0 {
			mapped := make(map[string]explorer.ExplorerPresentationBindingV1, len(input.Presentation))
			for key, binding := range input.Presentation {
				mappedKey := key
				for _, emission := range emitted {
					if emission.OutputID == input.Output.ID && emission.EmissionID == key {
						mappedKey = emission.CandidateID + "\x00" + emission.OccurrenceID
						break
					}
				}
				mapped[mappedKey] = binding
			}
			input.Presentation = mapped
		}
		document, err := migrateV1DocumentToCapability(input, snapshot, wire)
		if err != nil {
			return workspace, fmt.Errorf("documents[%d]: %w", i, err)
		}
		workspace.Documents = append(workspace.Documents, document)
	}
	for _, input := range bundle.AuthoringTabs() {
		visible := true
		if input.Visible != nil {
			visible = *input.Visible
		}
		workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: input.ID, Title: input.Title, OutputID: input.OutputID, Order: input.Order, Visible: visible})
	}
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Workspace: &workspace, Catalog: wire}).Validate(); err != nil {
		return workspace, err
	}
	return workspace, nil
}

func v2ReceiptResponse(receipt *explorer.CompilationReceipt, workspace authoringv2.Workspace) explorerv2api.CompileResponse {
	outputs := make([]explorerv2api.ReceiptOutput, 0, len(workspace.Documents))
	for _, document := range workspace.Documents {
		rowGrain := ""
		for _, output := range receipt.Bundle.Outputs {
			if output.Name == document.Output.ID {
				rowGrain = output.RowGrain
				break
			}
		}
		emissions := []explorerv2api.Emission{}
		for _, column := range receipt.EmittedColumns {
			if column.OutputID != document.Output.ID {
				continue
			}
			mode, label := column.ProjectionMode, column.Label
			if label == "" {
				label = column.PublicColumn
			}
			emissions = append(emissions, explorerv2api.Emission{OutputId: column.OutputID, CandidateId: column.CandidateID, OccurrenceId: column.OccurrenceID, ProjectionMode: mode, EmissionId: column.EmissionID, PublicColumn: column.PublicColumn, Label: label, LogicalType: column.LogicalType, Filterable: column.Filterable, Chartable: column.Chartable})
		}
		outputs = append(outputs, explorerv2api.ReceiptOutput{OutputId: document.Output.ID, Title: document.Output.Title, RowGrain: rowGrain, Emissions: emissions})
	}
	return explorerv2api.CompileResponse{
		ApiVersion: explorerv2api.LoomCalyprOrgexplorerAuthoringv2,
		Kind:       explorerv2api.ExplorerBuilderReceipt,
		ReceiptId:  receipt.ID, SnapshotToken: receipt.SnapshotToken,
		Generation: receipt.SourceGeneration, IntentDigest: receipt.IntentDigest,
		CompilerVersion: "explorer-authoring-v2", Builder: workspace,
		Outputs: outputs, Diagnostics: []explorerv2api.Diagnostic{},
	}
}

func migrateV1DocumentToCapability(input explorer.ExplorerBuilderDocumentV1, snapshot capability.Snapshot, wire authoringv2.CatalogSnapshot) (authoringv2.Document, error) {
	nodeIDs := map[string]string{}
	for _, node := range snapshot.Nodes {
		nodeIDs[node.ID] = node.ID
		nodeIDs[explorer.OpaqueID("n_", node.ResourceType)] = node.ID
	}
	edgeIDs := map[string]string{}
	for _, edge := range snapshot.Edges {
		edgeIDs[edge.ID] = edge.ID
		edgeIDs[explorer.OpaqueID("e_", edge.SourceResourceType+"\x00"+edge.Label+"\x00"+edge.TargetResourceType)] = edge.ID
	}
	candidateIDs := map[string]string{}
	for _, candidate := range snapshot.Candidates {
		candidateIDs[candidate.ID] = candidate.ID
		candidateIDs[explorer.OpaqueID("s_", candidate.ResourceType+"\x00"+candidate.FieldPath)] = candidate.ID
	}
	root := nodeIDs[input.BaseNodeID]
	if root == "" {
		return authoringv2.Document{}, fmt.Errorf("stored root node is not present in the current capability snapshot")
	}
	title := input.Output.Title
	if strings.TrimSpace(title) == "" {
		title = input.Output.ID
	}
	document := authoringv2.Document{APIVersion: authoringv2.APIVersion, Kind: authoringv2.Kind, Output: authoringv2.Output{ID: input.Output.ID, Title: title}, RootNodeID: root, RouteSteps: []authoringv2.RouteStep{}, Selections: []authoringv2.Selection{}, Presentation: map[string]authoringv2.Presentation{}}
	document.APIVersion = ""
	for i, oldEdgeID := range input.RouteEdgeIDs {
		edgeID := edgeIDs[oldEdgeID]
		if edgeID == "" {
			return authoringv2.Document{}, fmt.Errorf("stored route edge is not present in the current capability snapshot")
		}
		occurrenceID := ""
		occurrenceIndex := i
		if len(input.RouteOccurrences) == len(input.RouteEdgeIDs)+1 {
			occurrenceIndex++
		}
		if occurrenceIndex < len(input.RouteOccurrences) {
			occurrenceID = input.RouteOccurrences[occurrenceIndex].ID
		}
		document.RouteSteps = append(document.RouteSteps, authoringv2.RouteStep{EdgeID: edgeID, OccurrenceID: occurrenceID})
	}
	candidates := map[string]authoringv2.CatalogCandidate{}
	for _, candidate := range wire.Candidates {
		candidates[candidate.ID] = candidate
	}
	references := append([]explorer.ExplorerCandidateOccurrenceV1(nil), input.CandidateOccurrences...)
	if len(references) == 0 {
		occurrences, err := document.Occurrences(wire)
		if err != nil {
			return authoringv2.Document{}, err
		}
		for _, oldCandidateID := range input.CandidateIDs {
			candidateID := candidateIDs[oldCandidateID]
			if candidateID == "" {
				return authoringv2.Document{}, fmt.Errorf("stored candidate %q is not present in the current capability snapshot", oldCandidateID)
			}
			matches := []string{}
			for _, occurrence := range occurrences {
				if occurrence.NodeID == candidates[candidateID].NodeID {
					matches = append(matches, occurrence.ID)
				}
			}
			if len(matches) != 1 {
				return authoringv2.Document{}, fmt.Errorf("stored candidate %q cannot be mapped to exactly one route occurrence", oldCandidateID)
			}
			references = append(references, explorer.ExplorerCandidateOccurrenceV1{CandidateID: oldCandidateID, OccurrenceID: matches[0]})
		}
	}
	for _, reference := range references {
		candidateID := candidateIDs[reference.CandidateID]
		if candidateID == "" {
			return authoringv2.Document{}, fmt.Errorf("stored candidate %q is not present in the current capability snapshot", reference.CandidateID)
		}
		mode := reference.ProjectionMode
		if mode == "" || !containsString(candidates[candidateID].ProjectionModes, mode) {
			mode = candidates[candidateID].DefaultProjectionMode
		}
		document.Selections = append(document.Selections, authoringv2.Selection{CandidateID: candidateID, OccurrenceID: reference.OccurrenceID, ProjectionMode: mode})
	}
	for oldKey, binding := range input.Presentation {
		matchedKey := ""
		for _, selection := range document.Selections {
			occurrence := selection.OccurrenceID
			if occurrence == "" {
				occurrence = authoringv2.RootOccurrenceID
				if len(document.RouteSteps) > 0 {
					occurrence = authoringv2.DerivedOccurrenceID(len(document.RouteSteps) - 1)
				}
			}
			newKey := authoringv2.PresentationKey(selection.CandidateID, occurrence, selection.ProjectionMode)
			if !legacyPresentationKeyMatches(oldKey, input.Output.ID, selection.CandidateID, occurrence, newKey, references, candidateIDs) {
				continue
			}
			if matchedKey != "" && matchedKey != newKey {
				return authoringv2.Document{}, fmt.Errorf("stored presentation %q maps to more than one selection", oldKey)
			}
			matchedKey = newKey
		}
		if matchedKey == "" {
			return authoringv2.Document{}, fmt.Errorf("stored presentation %q cannot be mapped exactly", oldKey)
		}
		if _, exists := document.Presentation[matchedKey]; exists {
			return authoringv2.Document{}, fmt.Errorf("multiple stored presentations map to selection %q", matchedKey)
		}
		presentation := authoringv2.Presentation{Label: binding.Label, Visible: binding.Visible, Order: binding.Order}
		if binding.Table != nil {
			presentation.Table = &authoringv2.TablePresentation{Pinned: binding.Table.Pinned}
		}
		if binding.Filter != nil {
			presentation.Filter = &authoringv2.FilterPresentation{Label: binding.Filter.Label}
		}
		if binding.Chart != nil {
			presentation.Chart = &authoringv2.ChartPresentation{Type: binding.Chart.Type, Title: binding.Chart.Title}
		}
		document.Presentation[matchedKey] = presentation
	}
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Document: &document, Catalog: wire}).Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	return document, nil
}

func legacyPresentationKeyMatches(oldKey, outputID, currentCandidateID, occurrenceID, newKey string, references []explorer.ExplorerCandidateOccurrenceV1, ids map[string]string) bool {
	aliases := map[string]bool{
		currentCandidateID:                         true,
		currentCandidateID + "\x00" + occurrenceID: true,
		explorer.OpaqueID("em_", outputID+"\x00"+occurrenceID+"\x00"+currentCandidateID): true,
		newKey: true,
	}
	for _, reference := range references {
		if ids[reference.CandidateID] != currentCandidateID {
			continue
		}
		referenceOccurrence := reference.OccurrenceID
		if referenceOccurrence == "" {
			referenceOccurrence = occurrenceID
		}
		if referenceOccurrence != occurrenceID {
			continue
		}
		aliases[reference.CandidateID] = true
		aliases[reference.CandidateID+"\x00"+occurrenceID] = true
		aliases[explorer.OpaqueID("em_", outputID+"\x00"+occurrenceID+"\x00"+reference.CandidateID)] = true
	}
	return aliases[oldKey] || ids[oldKey] == currentCandidateID
}
