package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

// RegisterExplorerAuthoringV1Routes exposes the publish-only Builder contract.
// GET /builder hydrates active state; POST /builder resolves local intent using
// the same response model without persisting a browser draft.
func RegisterExplorerAuthoringV1Routes(app *fiber.App, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) {
	if app == nil || authorizer == nil || authorizeRead == nil || explorers == nil {
		return
	}
	prefix := "/api/v1/projects/:project/explorers/:explorerId/authoring/v1"

	app.Get(prefix+"/capabilities", func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		return c.JSON(fiber.Map{
			"apiVersion":  explorer.ExplorerAuthoringV1APIVersion,
			"kind":        "ExplorerAuthoringCapabilities",
			"operations":  []string{"builder", "preview", "publish", "active-export", "table-identity", "create"},
			"publication": "last-writer-wins",
			"features": fiber.Map{
				"emissionFilters": true, "emissionCharts": true,
				"sharedFilters": false, "fixedFilters": false,
				"fileActions": false, "deleteExplorer": false,
			},
		})
	})

	app.Post(prefix+"/table-identity", func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request struct {
			Title             string   `json:"title"`
			RequestID         string   `json:"requestId"`
			OccupiedOutputIDs []string `json:"occupiedOutputIds,omitempty"`
			OccupiedTabIDs    []string `json:"occupiedTabIds,omitempty"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil || strings.TrimSpace(request.Title) == "" || strings.TrimSpace(request.RequestID) == "" {
			if err == nil {
				err = fmt.Errorf("title and requestId are required")
			}
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 400, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "decode", Code: "MALFORMED_AUTHORING_REQUEST", JSONPath: "$", Message: err.Error()}})
		}
		seed := explorerProjectParam(c) + "\x00" + strings.TrimSpace(c.Params("explorerId")) + "\x00" + strings.TrimSpace(request.RequestID)
		outputID := explorer.OpaqueID("out_", seed)
		tabID := explorer.OpaqueID("tab_", seed)
		return c.JSON(fiber.Map{
			"apiVersion": explorer.ExplorerAuthoringV1APIVersion,
			"kind":       "ExplorerTableIdentity",
			"output":     explorer.ExplorerOutputIdentityV1{ID: outputID, Title: strings.TrimSpace(request.Title)},
			"tab":        fiber.Map{"id": tabID, "title": strings.TrimSpace(request.Title), "outputId": outputID},
		})
	})

	app.Get(prefix+"/builder", func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		state, err := loadExplorerBuilderState(c.Context(), explorers, capabilities, explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")), requestIDFromFiber(c))
		if err != nil {
			return authoringHTTPError(c, err)
		}
		emissionCount := 0
		for _, binding := range state.Bindings {
			emissionCount += len(binding.CandidateEmissions)
		}
		slog.Info("Explorer Builder state resolved",
			"request_id", requestIDFromFiber(c),
			"project", state.Project,
			"explorer_id", state.ExplorerID,
			"catalog_nodes", len(state.Catalog.Nodes),
			"catalog_candidates", len(state.Catalog.Candidates),
			"documents", len(state.Bundle.AuthoringDocuments()),
			"bindings", len(state.Bindings),
			"candidate_emissions", emissionCount,
		)
		return c.JSON(state)
	})

	app.Post(prefix+"/builder", func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request struct {
			Bundle        explorer.ExplorerAuthoringBundleV1 `json:"bundle"`
			SnapshotToken string                             `json:"snapshotToken"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("builder", err))
		}
		logAuthoringCompatibilityRequest(c, "builder", request.Bundle)
		if err := validateAuthoringRouteIdentity(c, request.Bundle); err != nil {
			return authoringHTTPError(c, err)
		}
		if capabilities.AuthoringCompile == nil || capabilities.Catalog == nil {
			return authoringHTTPError(c, explorerUnavailable("builder", "AUTHORING_COMPILER_UNAVAILABLE", "Explorer authoring resolver is not configured"))
		}
		project, id := explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId"))
		if len(request.Bundle.AuthoringDocuments()) == 0 {
			owner, err := explorers.Get(c.Context(), project, id)
			if err != nil {
				return authoringHTTPError(c, err)
			}
			snapshot, err := capabilities.Catalog(c.Context(), project, id, "")
			if err != nil || !snapshot.Complete || snapshot.Truncated {
				return authoringHTTPError(c, explorerUnavailable("catalog", "CATALOG_UNAVAILABLE", "Explorer authoring catalog is unavailable or incomplete"))
			}
			if err := snapshot.ValidateToken(request.SnapshotToken); err != nil {
				return authoringHTTPError(c, explorerConflict("catalog", "STALE_CATALOG_SNAPSHOT", err.Error(), map[string]any{"snapshotToken": request.SnapshotToken}))
			}
			active, activeErr := explorers.ActiveRevision(c.Context(), project, id)
			if errors.Is(activeErr, explorer.ErrNotFound) {
				active = nil
			} else if activeErr != nil {
				return authoringHTTPError(c, activeErr)
			}
			state := newBuilderState(owner, request.Bundle, snapshot, nil, active)
			return c.JSON(state)
		}
		resolved, err := capabilities.AuthoringCompile(c.Context(), ExplorerAuthoringV1CompileRequest{Bundle: request.Bundle, SnapshotToken: request.SnapshotToken, RequestID: requestIDFromFiber(c)})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		active, activeErr := explorers.ActiveRevision(c.Context(), project, id)
		if errors.Is(activeErr, explorer.ErrNotFound) {
			active = nil
		} else if activeErr != nil {
			return authoringHTTPError(c, activeErr)
		}
		state, err := builderStateFromResolved(c.Context(), explorers, capabilities, project, id, resolved, active)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(state)
	})

	app.Post(prefix+"/preview", func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		var request struct {
			Bundle        explorer.ExplorerAuthoringBundleV1 `json:"bundle"`
			SnapshotToken string                             `json:"snapshotToken"`
			OutputID      string                             `json:"outputId"`
			Limit         int                                `json:"limit"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("preview", err))
		}
		logAuthoringCompatibilityRequest(c, "preview", request.Bundle)
		if request.Limit == 0 {
			request.Limit = 25
		}
		if request.Limit < 1 || request.Limit > 1000 {
			return authoringHTTPError(c, authoringSemanticRoute("preview", "$.limit", "INVALID_PREVIEW_LIMIT", "limit must be between 1 and 1000", nil))
		}
		if err := validateAuthoringRouteIdentity(c, request.Bundle); err != nil {
			return authoringHTTPError(c, err)
		}
		if capabilities.AuthoringCompile == nil || capabilities.Preview == nil {
			return authoringHTTPError(c, explorerUnavailable("preview", "PREVIEW_UNAVAILABLE", "Explorer preview is not configured"))
		}
		resolved, err := capabilities.AuthoringCompile(c.Context(), ExplorerAuthoringV1CompileRequest{Bundle: request.Bundle, SnapshotToken: request.SnapshotToken, RequestID: requestIDFromFiber(c)})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		if !receiptHasOutput(resolved.Receipt.Bundle, request.OutputID) {
			return authoringHTTPError(c, authoringSemanticRoute("preview", "$.outputId", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the authoring bundle", nil))
		}
		rows, err := capabilities.Preview(c.Context(), resolved.Receipt.Bundle, recipe.RuntimeBindings{Project: projectid.Legacy(explorerProjectParam(c)), DatasetGeneration: resolved.SourceGeneration, PreviewLimit: request.Limit, OutputNames: []string{request.OutputID}})
		if err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 422, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "AUTHORING_PREVIEW_FAILED", Message: "authoring bundle could not be previewed"}, Cause: err})
		}
		return c.JSON(fiber.Map{"outputId": request.OutputID, "columns": previewColumnsForOutput(resolved.ResolvedBindings, request.OutputID), "rows": rows[request.OutputID], "rowCount": len(rows[request.OutputID]), "snapshotToken": request.SnapshotToken, "generation": resolved.SourceGeneration, "diagnostics": resolved.Diagnostics})
	})

	app.Post(prefix+"/publish", func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request struct {
			Bundle        explorer.ExplorerAuthoringBundleV1 `json:"bundle"`
			SnapshotToken string                             `json:"snapshotToken"`
		}
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("publish", err))
		}
		logAuthoringCompatibilityRequest(c, "publish", request.Bundle)
		if err := validateAuthoringRouteIdentity(c, request.Bundle); err != nil {
			return authoringHTTPError(c, err)
		}
		if capabilities.AuthoringCompile == nil || capabilities.Materialize == nil || capabilities.ActivateRelease == nil || capabilities.ValidateReleaseGeneration == nil {
			return authoringHTTPError(c, explorerUnavailable("publish", "PUBLICATION_UNAVAILABLE", "Explorer publication is not configured"))
		}
		resolved, err := capabilities.AuthoringCompile(c.Context(), ExplorerAuthoringV1CompileRequest{Bundle: request.Bundle, SnapshotToken: request.SnapshotToken, RequestID: requestIDFromFiber(c)})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		receipt := resolved.Receipt
		revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
		if active, activeErr := explorers.ActiveRevision(c.Context(), receipt.Project, receipt.ExplorerID); activeErr == nil && active.ID == revisionID {
			state, stateErr := builderStateFromResolved(c.Context(), explorers, capabilities, receipt.Project, receipt.ExplorerID, resolved, active)
			if stateErr != nil {
				return authoringHTTPError(c, stateErr)
			}
			return c.JSON(state)
		}
		if err := capabilities.ValidateReleaseGeneration(c.Context(), projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 409, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "snapshot", Code: "SNAPSHOT_CONFLICT", Message: "the catalog generation is no longer active", Details: map[string]any{"generation": receipt.SourceGeneration}}, Cause: err})
		}
		execution, err := capabilities.Materialize(c.Context(), receipt.Bundle, recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration})
		if err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "materialize", Code: "MATERIALIZATION_FAILED", Message: "Explorer materialization failed; the active revision was retained"}, Cause: err})
		}
		if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "materialize", Code: "MATERIALIZATION_FAILED", Message: "materialization did not produce queryable outputs"}, Cause: err})
		}
		if err := capabilities.ActivateRelease(c.Context(), projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle)); err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "activation", Code: "MATERIALIZATION_ACTIVATION_FAILED", Message: "dataset release activation failed; the active revision was retained"}, Cause: err})
		}
		now := time.Now().UTC()
		_, err = explorers.PublishAuthoring(c.Context(), receipt, explorer.Revision{ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID, Config: receipt.CompiledConfig, ConfigDigest: receipt.IntentDigest, AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Materializations: explorerMaterializations(receipt.Bundle, execution), EmittedColumns: receipt.EmittedColumns, Dataset: datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, receipt.ResolvedSchemaDigest, execution), Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: now}, Status: explorer.RevisionReady, CreatedBy: subjectFromFiber(c), CreatedAt: now, ReadyAt: &now})
		if err != nil {
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 500, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "persist", Code: "REVISION_STORE_FAILED", Message: "could not store the immutable Explorer revision"}, Cause: err})
		}
		active, err := explorers.ActiveRevision(c.Context(), receipt.Project, receipt.ExplorerID)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		state, err := builderStateFromResolved(c.Context(), explorers, capabilities, receipt.Project, receipt.ExplorerID, resolved, active)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(state)
	})

	app.Get(prefix+"/bundle/active", func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		revision, err := explorers.ActiveRevision(c.Context(), explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")))
		if err != nil {
			return authoringHTTPError(c, err)
		}
		if len(revision.AuthoringBundle) == 0 {
			return authoringHTTPError(c, authoringSemanticRoute("export", "$", "ACTIVE_AUTHORING_BUNDLE_MISSING", "active Explorer has no authoring bundle", nil))
		}
		bundle, err := explorer.DecodeAuthoringBundleV1ForMigration(revision.AuthoringBundle)
		if err != nil {
			return authoringHTTPError(c, err)
		}
		canonical, err := bundle.CanonicalJSON()
		if err != nil {
			return authoringHTTPError(c, err)
		}
		c.Set("Content-Type", "application/json")
		return c.Send(canonical)
	})
}

func validateAuthoringRouteIdentity(c fiber.Ctx, bundle explorer.ExplorerAuthoringBundleV1) error {
	if projectid.Canonical(bundle.Project) != explorerProjectParam(c) || bundle.ExplorerID != strings.TrimSpace(c.Params("explorerId")) {
		return explorerConflict("scope", "AUTHORING_IDENTITY_CONFLICT", "the authoring bundle identity does not match the route scope", nil)
	}
	return nil
}

func loadExplorerBuilderState(ctx context.Context, service *explorer.Service, capabilities ExplorerV2LifecycleConfig, project, id, requestID string) (*explorer.ExplorerBuilderStateV1, error) {
	owner, err := service.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if capabilities.Catalog == nil {
		return nil, explorerUnavailable("catalog", "CATALOG_UNAVAILABLE", "Explorer authoring catalog is not configured")
	}
	snapshot, err := capabilities.Catalog(ctx, project, id, "")
	if err != nil || !snapshot.Complete || snapshot.Truncated {
		return nil, explorerUnavailable("catalog", "CATALOG_UNAVAILABLE", "Explorer authoring catalog is unavailable or incomplete")
	}
	active, err := service.ActiveRevision(ctx, project, id)
	if errors.Is(err, explorer.ErrNotFound) {
		bundle := explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: projectid.Canonical(project), ExplorerID: id, Title: owner.Title, Documents: []explorer.ExplorerBuilderDocumentV1{}}
		state := newBuilderState(owner, bundle, snapshot, nil, nil)
		return &state, nil
	}
	if err != nil {
		return nil, err
	}
	if len(active.AuthoringBundle) == 0 {
		return nil, &explorer.AuthoringError{Status: 422, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "builder", Code: "ACTIVE_AUTHORING_BUNDLE_MISSING", Message: "active Explorer cannot be edited because its authoring bundle is missing", RequestID: requestID}}
	}
	bundle, err := explorer.DecodeAuthoringBundleV1ForMigration(active.AuthoringBundle)
	if err != nil {
		return nil, &explorer.AuthoringError{Status: 422, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "builder", Code: "ACTIVE_AUTHORING_BUNDLE_INVALID", Message: "active Explorer authoring state is invalid", RequestID: requestID}, Cause: err}
	}
	if capabilities.AuthoringCompile == nil {
		return nil, explorerUnavailable("builder", "AUTHORING_COMPILER_UNAVAILABLE", "Explorer authoring resolver is not configured")
	}
	resolved, err := capabilities.AuthoringCompile(ctx, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token, RequestID: requestID})
	if err != nil {
		return nil, err
	}
	return builderStateFromResolved(ctx, service, capabilities, project, id, resolved, active)
}

func builderStateFromResolved(ctx context.Context, service *explorer.Service, capabilities ExplorerV2LifecycleConfig, project, id string, resolved ExplorerAuthoringV1CompileResult, active *explorer.Revision) (*explorer.ExplorerBuilderStateV1, error) {
	owner, err := service.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	// ResolveAuthoringBundle already validated and resolved this exact
	// snapshot. Reuse it for the Builder response so catalog metadata and
	// bindings are guaranteed to describe the same generation/token.
	snapshot := resolved.Snapshot
	if snapshot.Token == "" {
		var snapshotErr error
		snapshot, snapshotErr = capabilities.Catalog(ctx, project, id, resolved.SourceGeneration)
		if snapshotErr != nil {
			return nil, explorerUnavailable("catalog", "CATALOG_UNAVAILABLE", snapshotErr.Error())
		}
	}
	state := newBuilderState(owner, resolved.Bundle, snapshot, resolved.ResolvedBindings, active)
	state.Diagnostics = append(state.Diagnostics, resolved.Diagnostics...)
	return &state, nil
}

func newBuilderState(owner *explorer.Explorer, bundle explorer.ExplorerAuthoringBundleV1, snapshot explorer.CatalogSnapshot, bindings []explorer.ExplorerResolvedBindingV1, active *explorer.Revision) explorer.ExplorerBuilderStateV1 {
	state := explorer.ExplorerBuilderStateV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerBuilderStateV1Kind, Project: projectid.Canonical(owner.Project), ExplorerID: owner.ExplorerID, Title: owner.Title, Bundle: bundle, Catalog: builderCatalog(snapshot), Bindings: bindings, Diagnostics: []explorer.AuthoringDiagnostic{}}
	if state.Bindings == nil {
		state.Bindings = []explorer.ExplorerResolvedBindingV1{}
	}
	if active != nil {
		state.Active = explorer.ExplorerBuilderPublicationV1{RevisionID: active.ID, State: string(active.Status), Generation: active.SourceGeneration, IntentDigest: active.IntentDigest}
		if active.ActivatedAt != nil {
			state.Active.PublishedAt = *active.ActivatedAt
		}
		if active.Migration != nil {
			state.Active.Migration = &explorer.ExplorerMigrationSummaryV1{
				Kind:                  active.Migration.Kind,
				Source:                active.Migration.Source,
				SourceProject:         active.Migration.SourceProject,
				SourceExplorerID:      active.Migration.SourceExplorerID,
				OriginalConfigDigest:  active.Migration.OriginalConfigDigest,
				OriginalMappingDigest: active.Migration.OriginalMappingDigest,
				MigratedAt:            active.Migration.MigratedAt,
			}
		}
	}
	return state
}

func builderCatalog(snapshot explorer.CatalogSnapshot) explorer.ExplorerBuilderCatalogV1 {
	result := explorer.ExplorerBuilderCatalogV1{SnapshotToken: snapshot.Token, Generation: snapshot.Generation, ResolvedSchemaDigest: snapshot.ResolvedSchemaDigest, AuthorizationScopeDigest: snapshot.AuthorizationScopeDigest, Nodes: []explorer.ExplorerCatalogNodeV1{}, Edges: []explorer.ExplorerCatalogEdgeV1{}, Candidates: []explorer.ExplorerCandidateV1{}}
	for _, node := range snapshot.Catalog.Nodes {
		result.Nodes = append(result.Nodes, explorer.ExplorerCatalogNodeV1{NodeID: node.ID, ResourceType: node.ResourceType})
	}
	for _, edge := range snapshot.Catalog.Edges {
		result.Edges = append(result.Edges, explorer.ExplorerCatalogEdgeV1{EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label})
	}
	ids := make([]string, 0, len(snapshot.Catalog.Selections))
	for id := range snapshot.Catalog.Selections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		candidate := snapshot.Catalog.Selections[id]
		result.Candidates = append(result.Candidates, explorer.ExplorerCandidateV1{CandidateID: candidate.ID, NodeID: candidate.NodeID, Label: candidate.FieldRef, LogicalType: candidate.LogicalType, Filterable: candidate.Filterable, Chartable: candidate.Chartable})
	}
	return result
}

// Retained for migration and contract tests; no standalone catalog route uses it.
func authoringCatalogResponse(snapshot explorer.CatalogSnapshot) fiber.Map {
	catalog := builderCatalog(snapshot)
	candidates := make([]fiber.Map, 0, len(catalog.Candidates))
	for _, candidate := range catalog.Candidates {
		candidates = append(candidates, fiber.Map{"candidateId": candidate.CandidateID, "nodeId": candidate.NodeID, "label": candidate.Label, "path": candidate.Label, "fieldRef": candidate.Label, "logicalType": candidate.LogicalType, "filterable": candidate.Filterable, "chartable": candidate.Chartable})
	}
	return fiber.Map{"apiVersion": explorer.ExplorerAuthoringV1APIVersion, "kind": "ExplorerAuthoringCatalog", "snapshotToken": snapshot.Token, "project": snapshot.Project, "sourceGeneration": snapshot.Generation, "authorizationScopeDigest": snapshot.AuthorizationScopeDigest, "resolvedSchemaDigest": snapshot.ResolvedSchemaDigest, "nodes": catalog.Nodes, "routeEdges": catalog.Edges, "candidates": candidates, "diagnostics": snapshot.Diagnostics}
}

func decodeAuthoringStrict(raw []byte, value any) error {
	if err := explorer.RejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func authoringRead(c fiber.Ctx, authorizeRead explorerConfigReadAuthorizer) error {
	if err := authorizeRead(c.Context(), principalFromFiber(c), explorerProjectParam(c)); err != nil {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err})
	}
	return nil
}
func authoringWrite(c fiber.Ctx, authorizer authscope.Authorizer) error {
	project := explorerProjectParam(c)
	path := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
	if err := authorizer.AuthorizeWrite(c.Context(), principalFromFiber(c), project, path); err != nil {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err})
	}
	return nil
}
func requestIDFromFiber(c fiber.Ctx) string {
	value, _ := c.Locals("request_id").(string)
	return value
}

// logAuthoringCompatibilityRequest is intentionally scoped to the incident
// request so normal Builder traffic never writes full authoring documents to
// logs. It records the raw envelope plus the three fields needed to determine
// whether the frontend or Loom dropped candidate occurrence mappings.
func logAuthoringCompatibilityRequest(c fiber.Ctx, operation string, bundle explorer.ExplorerAuthoringBundleV1) {
	if requestIDFromFiber(c) != "f9131e28d8e058da07145008a1e93970" {
		return
	}
	for index, document := range bundle.AuthoringDocuments() {
		slog.Info("Explorer authoring compatibility request",
			"request_id", requestIDFromFiber(c),
			"operation", operation,
			"document_index", index,
			"document_candidate_ids", document.CandidateIDs,
			"document_candidate_occurrences", document.CandidateOccurrences,
			"document_route_occurrences", document.RouteOccurrences,
			"raw_body", string(c.Body()),
		)
	}
}

func malformedRouteError(stage string, err error) error {
	return &explorer.AuthoringError{Status: 400, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: "MALFORMED_AUTHORING_REQUEST", JSONPath: "$", Message: err.Error()}, Cause: err}
}
func authoringSemanticRoute(stage, path, code, message string, details map[string]any) error {
	return &explorer.AuthoringError{Status: 422, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, JSONPath: path, Message: message, Details: details}}
}
func receiptHasOutput(bundle recipe.Bundle, id string) bool {
	for _, output := range bundle.Outputs {
		if output.Name == id {
			return true
		}
	}
	return false
}
func previewColumnsForOutput(bindings []explorer.ExplorerResolvedBindingV1, output string) []explorer.ExplorerPreviewColumnV1 {
	columns := []explorer.ExplorerPreviewColumnV1{}
	for _, binding := range bindings {
		if binding.OutputID != output {
			continue
		}
		for _, emission := range binding.CandidateEmissions {
			columns = append(columns, explorer.ExplorerPreviewColumnV1{
				OutputID: output, CandidateID: emission.CandidateID, OccurrenceID: emission.OccurrenceID,
				EmissionID: emission.EmissionID, PublicColumn: emission.PublicColumn, Label: emission.Label,
				LogicalType: emission.LogicalType, Filterable: emission.Filterable, Chartable: emission.Chartable,
			})
		}
	}
	return columns
}
func authoringHTTPError(c fiber.Ctx, err error) error {
	requestID := requestIDFromFiber(c)
	var value *explorer.AuthoringError
	if errors.As(err, &value) {
		value.Diagnostic.RequestID = requestID
		slog.Error("Explorer authoring request failed", "request_id", requestID, "project", explorerProjectParam(c), "explorer_id", strings.TrimSpace(c.Params("explorerId")), "stage", value.Diagnostic.Stage, "code", value.Diagnostic.Code, "json_path", value.Diagnostic.JSONPath, "message", value.Diagnostic.Message, "details", value.Diagnostic.Details, "cause", value.Cause)
		return c.Status(value.Status).JSON(fiber.Map{"error": fiber.Map{"code": value.Diagnostic.Code, "message": value.Diagnostic.Message, "requestId": requestID, "diagnostic": value.Diagnostic}, "diagnostics": []explorer.AuthoringDiagnostic{value.Diagnostic}})
	}
	if errors.Is(err, explorer.ErrNotFound) {
		return c.Status(404).JSON(fiber.Map{"error": fiber.Map{"code": "NOT_FOUND", "message": "Explorer resource not found", "requestId": requestID}})
	}
	return c.Status(500).JSON(fiber.Map{"error": fiber.Map{"code": "INTERNAL_ERROR", "message": "internal server error", "requestId": requestID}})
}
