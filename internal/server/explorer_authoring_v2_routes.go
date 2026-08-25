package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
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
		_, wire, err := readCapability(c)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(wire)
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
			return authoringHTTPError(c, explorerConflict("capability", "STALE_CAPABILITY_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": token}))
		}
		for _, candidate := range snapshot.Candidates {
			if candidate.ID != candidateID {
				continue
			}
			return c.JSON(fiber.Map{
				"apiVersion": authoringv2.APIVersion, "kind": "ExplorerCandidateSuggestions",
				"snapshotToken": token, "candidateId": candidateID,
				"values":   append([]string(nil), candidate.SuggestedValues...),
				"complete": candidate.SuggestionsComplete, "truncated": candidate.SuggestionsTruncated,
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
			if document, decodeErr := authoringv2.DecodeDocument(active.AuthoringBundle); decodeErr == nil {
				state.Document = &document
			} else if bundle, decodeErr := explorer.DecodeAuthoringBundleV1ForMigration(active.AuthoringBundle); decodeErr == nil {
				if documents := bundle.AuthoringDocuments(); len(documents) > 0 {
					if document, migrateErr := migrateV1DocumentToCapability(documents[0], snapshot, wire); migrateErr == nil {
						state.Document = &document
					}
				}
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
		var request struct {
			Document      authoringv2.Document `json:"document"`
			SnapshotToken string               `json:"snapshotToken"`
		}
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
			return authoringHTTPError(c, explorerConflict("capability", "STALE_CAPABILITY_SNAPSHOT", "the capability snapshot is stale or unavailable", map[string]any{"snapshotToken": request.SnapshotToken}))
		}
		wire := authoringV2Catalog(snapshot, id)
		state := authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Document: &request.Document, Catalog: wire}
		if err := state.Validate(); err != nil {
			return authoringHTTPError(c, authoringSemanticRoute("intent", "$", "INVALID_AUTHORING_INTENT", err.Error(), nil))
		}
		var stored *explorer.CompilationReceipt
		nativeReceipt := capabilities.CompileReceipt != nil
		if nativeReceipt {
			stored, err = capabilities.CompileReceipt(c.Context(), ExplorerV2ReceiptCompileRequest{Project: project, ExplorerID: id, Document: request.Document, SnapshotToken: snapshot.Token, RequestID: requestIDFromFiber(c), Authorized: authorized})
		} else {
			// Compatibility path for migration-only callers. Production wiring
			// supplies CompileReceipt and never enters this V1 adapter.
			bundle, bundleErr := authoringV2Bundle(project, id, request.Document, wire)
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
		return c.JSON(fiber.Map{
			"apiVersion": authoringv2.APIVersion, "kind": "ExplorerCompileResult",
			"builder": state, "receiptId": stored.ID, "snapshotToken": snapshot.Token,
			"sourceGeneration": stored.SourceGeneration, "resolvedSchemaDigest": stored.ResolvedSchemaDigest,
			"emittedColumns": stored.EmittedColumns,
		})
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
		var request struct {
			ReceiptID string `json:"receiptId"`
			OutputID  string `json:"outputId"`
			Limit     int    `json:"limit,omitempty"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" {
			if err == nil {
				err = fmt.Errorf("receiptId and outputId are required")
			}
			return authoringHTTPError(c, malformedRouteError("preview", err))
		}
		logReceiptID, logOutputID = request.ReceiptID, request.OutputID
		if request.Limit == 0 {
			request.Limit = engine.DefaultPreviewLimit
		}
		if request.Limit < 1 || request.Limit > engine.MaxPreviewLimit {
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
		receipt, err := lookupV2Receipt(previewCtx, explorers, capabilities, explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")), request.ReceiptID)
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
			return authoringHTTPError(c, explorerConflict("preview", "RECEIPT_INPUT_UNAVAILABLE", "the receipt's capability snapshot is no longer authorized or retained", nil))
		}
		if capabilities.PreviewReceipt != nil {
			if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
				return authoringHTTPError(c, explorerConflict("preview", "RECEIPT_INPUT_UNAVAILABLE", "the receipt's capability snapshot is no longer authorized or retained", nil))
			}
		}
		if nativeReceiptPreview {
			if !receiptHasOutput(receipt.Bundle, request.OutputID) {
				return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil))
			}
			if err := validateReceiptOutputContract(receipt, request.OutputID); err != nil {
				return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil))
			}
		} else if !receiptHasOutput(receipt.Bundle, request.OutputID) {
			return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil))
		}
		bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration, PreviewLimit: request.Limit, OutputNames: []string{request.OutputID}}
		applyAuthorizedScope(&bindings, authorized, false)
		columns := make([]explorer.EmittedColumn, 0)
		for _, column := range receipt.EmittedColumns {
			if column.OutputID == request.OutputID {
				columns = append(columns, column)
			}
		}
		var encoded []byte
		if capabilities.PreviewReceipt != nil {
			encoder, encoderErr := newPreviewResponseEncoder(receipt, request.OutputID, columns, maxExplorerPreviewResponseBytes)
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
				responseRows = len(rows[request.OutputID])
				encoded, err = encodeExplorerPreviewResponse(receipt, request.OutputID, columns, rows[request.OutputID], maxExplorerPreviewResponseBytes)
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
		var request struct {
			ReceiptID string `json:"receiptId"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.ReceiptID) == "" {
			if err == nil {
				err = fmt.Errorf("receiptId is required")
			}
			return authoringHTTPError(c, malformedRouteError("publish", err))
		}
		if (capabilities.Materialize == nil && capabilities.MaterializeReceipt == nil) || capabilities.ActivateRelease == nil || capabilities.ValidateReleaseGeneration == nil || (capabilities.CapabilityToken == nil && capabilities.AuthorizedCapabilityExecution == nil) {
			return authoringHTTPError(c, explorerUnavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured"))
		}
		receipt, err := lookupV2Receipt(c.Context(), explorers, capabilities, explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")), request.ReceiptID)
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
			return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_INPUT_UNAVAILABLE", "the receipt's capability snapshot is no longer authorized or retained", nil))
		}
		if capabilities.CompileReceipt != nil {
			if err := validateReceiptCapability(receipt, snapshot); err != nil {
				return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_INPUT_UNAVAILABLE", err.Error(), nil))
			}
		}
		if len(receipt.EmittedColumns) == 0 || len(receipt.CompiledConfig) == 0 {
			return authoringHTTPError(c, authoringSemanticRoute("publish", "$.receiptId", "NO_SELECTED_COLUMNS", "select at least one output column before publishing", nil))
		}
		if err := capabilities.ValidateReleaseGeneration(c.Context(), projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {
			return authoringHTTPError(c, explorerConflict("publish", "RECEIPT_INPUT_UNAVAILABLE", "the receipt generation is no longer active", map[string]any{"generation": receipt.SourceGeneration}))
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
		return c.JSON(fiber.Map{"apiVersion": authoringv2.APIVersion, "kind": "ExplorerPublishResult", "receiptId": receipt.ID, "revisionId": revision.ID, "state": revision.Status, "sourceGeneration": revision.SourceGeneration})
	})
}

func validateV2ReceiptRoute(c fiber.Ctx, receipt *explorer.CompilationReceipt, capabilities ExplorerV2LifecycleConfig) error {
	if receipt == nil || receipt.Project != explorerProjectParam(c) || receipt.ExplorerID != strings.TrimSpace(c.Params("explorerId")) {
		return &explorer.AuthoringError{Status: http.StatusNotFound, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "receipt", Code: "RECEIPT_NOT_FOUND", Message: "compilation receipt was not found"}}
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
		return &explorer.AuthoringError{Status: http.StatusNotFound, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: "RECEIPT_NOT_FOUND", Message: "compilation receipt was not found"}}
	}
	if errors.Is(err, explorer.ErrReceiptRecompileRequired) {
		return explorerConflict(stage, "RECEIPT_RECOMPILE_REQUIRED", "the compilation receipt is from an unsupported compiler contract", nil)
	}
	return explorerUnavailable(stage, "RECEIPT_STORE_UNAVAILABLE", "the compilation receipt store is unavailable")
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
	for _, reference := range input.CandidateOccurrences {
		candidateID := candidateIDs[reference.CandidateID]
		if candidateID == "" {
			continue
		}
		mode := reference.ProjectionMode
		if mode == "" || !containsString(candidates[candidateID].ProjectionModes, mode) {
			mode = candidates[candidateID].DefaultProjectionMode
		}
		document.Selections = append(document.Selections, authoringv2.Selection{CandidateID: candidateID, OccurrenceID: reference.OccurrenceID, ProjectionMode: mode})
	}
	if err := (authoringv2.BuilderState{APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind, Document: &document, Catalog: wire}).Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	return document, nil
}
