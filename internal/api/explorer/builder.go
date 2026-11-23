package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/rowsource"
	"github.com/calypr/loom/internal/projectid"
)

func (h *Handlers) authorizeAuthoringRead(ctx context.Context, project string) error {
	if h == nil || h.authorizeRead == nil {
		return explorer.ExplorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return &explorer.AuthoringError{Status: http.StatusServiceUnavailable, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: authoringBackendUnavailableCode, Message: authoringBackendUnavailableMessage}, Cause: err}
		}
		return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func (h *Handlers) authorizeAuthoringWrite(ctx context.Context, project, authResourcePath string) error {
	if h == nil || h.authorizer == nil {
		return explorer.ExplorerUnavailable("authorization", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return &explorer.AuthoringError{Status: http.StatusServiceUnavailable, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: authoringBackendUnavailableCode, Message: authoringBackendUnavailableMessage}, Cause: err}
		}
		return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
	}
	return nil
}

func (r *Handlers) ApplyExplorerBuilderCommands(ctx context.Context, request loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.ApplyExplorerBuilderCommandsJSONRequestBody) (loomapi.ApplyCommandsResponse, error) {
		var result loomapi.ApplyCommandsResponse
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("commands", errors.New("request body is required"))
		}
		request, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](*body)
		if err != nil {
			return result, explorer.MalformedRouteError("commands", err)
		}
		var encoder *previewResponseEncoder
		sinkFactory := func(receipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
			var err error
			encoder, err = newPreviewResponseEncoder(receipt, request.Commands[0].OutputID, columns, maxExplorerPreviewResponseBytes)
			if err != nil {
				return nil, err
			}
			return encoder.Visit, nil
		}
		applied, err := h.application.ApplyBuilderCommands(ctx, project, explorerID, request, authscope.SubjectFromContext(ctx), sinkFactory)
		if err != nil {
			return result, err
		}
		if applied == nil || applied.Commands == nil {
			return result, explorer.ExplorerUnavailable("commands", "AUTHORING_UNAVAILABLE", "construction Apply returned no command response")
		}
		result, err = directAuthoringJSON[loomapi.ApplyCommandsResponse](applied.Commands)
		if err != nil {
			return result, err
		}
		if applied.Accepted == nil || encoder == nil || applied.Accepted.Preview.Receipt == nil {
			return result, nil
		}
		preview, err := previewResponseFromEncoder(encoder, applied.Accepted.Preview.Summary)
		if err != nil {
			return result, nil
		}
		receipt := v2ReceiptResponse(applied.Accepted.Preview.Receipt, applied.Commands.Workspace)
		result.AcceptedConstruction = &loomapi.AcceptedConstruction{
			Receipt:      acceptedConstructionReceiptResponse(receipt),
			Preview:      preview,
			PreviewLimit: applied.Accepted.PreviewLimit,
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ApplyExplorerBuilderCommands200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "applyExplorerBuilderCommands", err)
	return loomapi.NewApplyExplorerBuilderCommandsErrorResponse(status, failure)
}

func (r *Handlers) AssessExplorerRowChange(ctx context.Context, request loomapi.AssessExplorerRowChangeRequestObject) (loomapi.AssessExplorerRowChangeResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.AssessExplorerRowChangeJSONRequestBody) (loomapi.RowChangeAssessmentResponse, error) {
		var result loomapi.RowChangeAssessmentResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("row-change", errors.New("request body is required"))
		}
		rootOccurrenceID := ""
		if body.RootOccurrenceId != nil {
			rootOccurrenceID = *body.RootOccurrenceId
		}
		routeRebase := []authoringv2.RouteRebaseChoice{}
		if body.RouteRebase != nil {
			converted, err := directAuthoringJSON[[]authoringv2.RouteRebaseChoice](*body.RouteRebase)
			if err != nil {
				return result, explorer.MalformedRouteError("row-change", err)
			}
			routeRebase = converted
		}
		value, err := h.application.AssessRowChange(ctx, rowdef.AssessRowChangeRequest{
			Project: project, ExplorerID: explorerID, Mode: optionalEnumString(body.Mode), SnapshotToken: body.SnapshotToken,
			DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: body.OutputId,
			RootNodeID: body.RootNodeId, RootOccurrenceID: rootOccurrenceID, RouteRebase: routeRebase,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RowChangeAssessmentResponse](struct {
			authoringv2.RowChangeAssessment
			SnapshotToken        string               `json:"snapshotToken"`
			DraftVersion         int64                `json:"draftVersion"`
			DraftDigest          string               `json:"draftDigest"`
			CandidateReceiptID   string               `json:"candidateReceiptId,omitempty"`
			CandidateDocument    *ws.Document         `json:"candidateDocument,omitempty"`
			CandidateNetwork     *ws.AuthoringNetwork `json:"candidateNetwork,omitempty"`
			CandidateRowIdentity *ws.RowIdentityPlan  `json:"candidateRowIdentity,omitempty"`
			Diagnostics          []map[string]any     `json:"diagnostics"`
		}{
			RowChangeAssessment: value.Assessment,
			SnapshotToken:       value.SnapshotToken,
			DraftVersion:        value.DraftVersion,
			DraftDigest:         value.DraftDigest,
			CandidateReceiptID:  value.CandidateReceiptID,
			CandidateDocument:   value.CandidateDocument, CandidateNetwork: value.CandidateNetwork, CandidateRowIdentity: value.CandidateRowIdentity,
			Diagnostics: []map[string]any{},
		})
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.AssessExplorerRowChange200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "assessExplorerRowChange", err)
	return loomapi.NewAssessExplorerRowChangeErrorResponse(status, failure)
}

func (r *Handlers) CheckExplorerPopulationMapping(ctx context.Context, request loomapi.CheckExplorerPopulationMappingRequestObject) (loomapi.CheckExplorerPopulationMappingResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.CheckExplorerPopulationMappingJSONRequestBody) (loomapi.PopulationMappingResponse, error) {
		var result loomapi.PopulationMappingResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("populationMapping", errors.New("receiptId and outputId are required"))
		}
		request := population.PopulationMappingRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, OutputID: body.OutputId}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		value, err := h.application.PopulationMapping(ctx, request)
		if err != nil {
			return result, err
		}
		result.Binding = loomapi.PopulationMappingBinding{
			ReceiptId: value.Report.Binding.ReceiptID, OutputId: value.Report.Binding.OutputID,
			Project: value.Report.Binding.Project, ExplorerId: value.Report.Binding.ExplorerID,
			Generation: value.Report.Binding.Generation, ScopeDigest: value.Report.Binding.ScopeDigest,
			SelectionRevisionId: value.Report.Binding.SelectionRevisionID, MembershipDigest: value.Report.Binding.MembershipDigest,
			ResourceType: value.Report.Binding.ResourceType,
		}
		result.Status = loomapi.PopulationMappingResponseStatus(value.Report.Status)
		result.Unmapped = make([]loomapi.SelectionResourceRef, 0, len(value.Report.Unmapped))
		for _, ref := range value.Report.Unmapped {
			result.Unmapped = append(result.Unmapped, loomapi.SelectionResourceRef{Project: ref.Project, Generation: ref.Generation, ResourceType: ref.ResourceType, Id: ref.ID})
		}
		if value.Report.NextCursor != "" {
			result.NextCursor = &value.Report.NextCursor
		}
		if value.Report.Counts != nil {
			result.Counts = &loomapi.PopulationMappingCounts{Selected: value.Report.Counts.Selected, Mapped: value.Report.Counts.Mapped, Unmapped: value.Report.Counts.Unmapped, EmittedRows: value.Report.Counts.EmittedRows}
		}
		result.Diagnostics = make([]loomapi.Diagnostic, 0, len(value.Report.Diagnostics))
		for _, diagnostic := range value.Report.Diagnostics {
			result.Diagnostics = append(result.Diagnostics, loomapi.Diagnostic{Severity: "INFO", Stage: "populationMapping", Code: diagnostic.Code, Message: diagnostic.Message})
		}
		return result, nil
	}(r, ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.CheckExplorerPopulationMapping200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "checkExplorerPopulationMapping", err)
	return loomapi.NewCheckExplorerPopulationMappingErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerAuthoringCapability(ctx context.Context, request loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project string) (loomapi.AuthoringCapability, error) {
		var result loomapi.AuthoringCapability
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		return loomapi.AuthoringCapability{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerAuthoringCapabilities, Operations: []loomapi.AuthoringCapabilityOperations{loomapi.AuthoringCapabilityOperationsBuilder, loomapi.AuthoringCapabilityOperationsSuggestions, loomapi.AuthoringCapabilityOperationsRowChange, loomapi.AuthoringCapabilityOperationsPreview, loomapi.AuthoringCapabilityOperationsInterpretationPreview, loomapi.AuthoringCapabilityOperationsConfiguredColumnContext, loomapi.AuthoringCapabilityOperationsInterpretationCreate, loomapi.AuthoringCapabilityOperationsPublish, loomapi.AuthoringCapabilityOperationsCommands, loomapi.AuthoringCapabilityOperationsReconcile, loomapi.AuthoringCapabilityOperationsSourceRuleCreate}, PreviewLimits: []int{10, 25, 50, 100}, Features: loomapi.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true}}, nil
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerAuthoringCapability", err)
	return loomapi.NewGetExplorerAuthoringCapabilityErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerBuilder(ctx context.Context, request loomapi.GetExplorerBuilderRequestObject) (loomapi.GetExplorerBuilderResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string) (loomapi.BuilderState, error) {
		var result loomapi.BuilderState
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.Builder(ctx, lifecycle.BuilderRequest{Project: project, ExplorerID: explorerID})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.BuilderState](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerBuilder", err)
	return loomapi.NewGetExplorerBuilderErrorResponse(status, failure)
}

func (r *Handlers) ReconcileExplorerBuilder(ctx context.Context, request loomapi.ReconcileExplorerBuilderRequestObject) (loomapi.ReconcileExplorerBuilderResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.ReconcileExplorerBuilderJSONRequestBody) (loomapi.CompileResponse, error) {
		var result loomapi.CompileResponse
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("reconcile", errors.New("request body is required"))
		}
		reconciled, err := h.application.ReconcileWithWorkspace(ctx, lifecycle.ReconcileRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: optionalString(body.OutputId)})
		if err != nil {
			return result, err
		}
		return v2ReceiptResponse(reconciled.Receipt, reconciled.Workspace), nil
	}(r, ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.ReconcileExplorerBuilder200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "reconcileExplorerBuilder", err)
	return loomapi.NewReconcileExplorerBuilderErrorResponse(status, failure)
}

func (r *Handlers) ResolveConfiguredColumnContext(ctx context.Context, request loomapi.ResolveConfiguredColumnContextRequestObject) (loomapi.ResolveConfiguredColumnContextResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ResolveConfiguredColumnContextJSONRequestBody) (loomapi.ConfiguredColumnContextResponse, error) {
		var result loomapi.ConfiguredColumnContextResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-context", errors.New("request body is required"))
		}
		value, err := h.application.ConfiguredColumnContext(ctx, interpretation.ConfiguredColumnContextRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
		})
		if err != nil {
			return result, err
		}
		return configuredColumnContextResponse(value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ResolveConfiguredColumnContext200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "resolveConfiguredColumnContext", err)
	return loomapi.NewResolveConfiguredColumnContextErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerCandidates(ctx context.Context, request loomapi.SearchExplorerCandidatesRequestObject) (loomapi.SearchExplorerCandidatesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SearchExplorerCandidatesJSONRequestBody) (loomapi.CandidateSearchResponse, error) {
		var result loomapi.CandidateSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("suggestions", errors.New("request body is required"))
		}
		query := ""
		if body.Query != nil {
			query = *body.Query
		}
		value, err := h.application.Suggestions(ctx, lifecycle.SuggestionsRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, NodeID: body.NodeId, Query: query})
		if err != nil {
			return result, err
		}
		candidates := make([]loomapi.CatalogCandidate, 0, len(value.Candidates))
		for _, candidate := range value.Candidates {
			wire, convertErr := directAuthoringJSON[loomapi.CatalogCandidate](candidate)
			if convertErr != nil {
				return result, convertErr
			}
			candidates = append(candidates, wire)
		}
		return loomapi.CandidateSearchResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderCandidateSuggestions, SnapshotToken: value.SnapshotToken, NodeId: value.NodeID, Candidates: candidates, Diagnostics: []loomapi.Diagnostic{}}, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerCandidates200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerCandidates", err)
	return loomapi.NewSearchExplorerCandidatesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerSchemaFields(ctx context.Context, request loomapi.SearchExplorerSchemaFieldsRequestObject) (loomapi.SearchExplorerSchemaFieldsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SchemaFieldSearchRequest) (loomapi.SchemaFieldSearchResponse, error) {
		var result loomapi.SchemaFieldSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("schema-fields", errors.New("request body is required"))
		}
		request := lifecycle.SearchSchemaFieldsRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, NodeID: body.NodeId,
		}
		if body.Query != nil {
			request.Query = *body.Query
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		value, err := h.application.SearchSchemaFields(ctx, request)
		if err != nil {
			return result, err
		}
		wire, err := directAuthoringJSON[loomapi.SchemaFieldSearchResponse](value)
		wire.ApiVersion = loomapi.LoomCalyprOrgexplorerAuthoringv2
		wire.Kind = loomapi.ExplorerBuilderGeneratedSchemaFields
		return wire, err
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerSchemaFields200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerSchemaFields", err)
	return loomapi.NewSearchExplorerSchemaFieldsErrorResponse(status, failure)
}

func (r *Handlers) TraceExplorerCell(ctx context.Context, request loomapi.TraceExplorerCellRequestObject) (loomapi.TraceExplorerCellResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.TraceExplorerCellJSONRequestBody) (loomapi.CellTraceResponse, error) {
		var result loomapi.CellTraceResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("trace", errors.New("receiptId, outputId, rowId, and column are required"))
		}
		request := lifecycle.CellTraceRequest{
			Project: project, ExplorerID: explorerID,
			ReceiptID: body.ReceiptId, OutputID: body.OutputId,
			RowID: body.RowId, Column: body.Column,
		}
		if body.Offset != nil {
			request.Offset = *body.Offset
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		value, err := h.application.CellTrace(ctx, request)
		if err != nil {
			return result, err
		}
		result.Binding = loomapi.CellTraceBinding{
			ReceiptId: value.Binding.ReceiptID, OutputId: value.Binding.OutputID,
			Project: value.Binding.Project, ExplorerId: value.Binding.ExplorerID,
			Generation: value.Binding.Generation, ScopeDigest: value.Binding.ScopeDigest,
		}
		result.Feature = loomapi.CellTraceFeature{
			OutputId: value.Feature.OutputID, Column: value.Feature.Column,
			AuthoredColumn: value.Feature.AuthoredColumn, OccurrenceId: value.Feature.OccurrenceID,
			Label: value.Feature.Label, LogicalType: value.Feature.LogicalType,
			ProjectionMode: value.Feature.ProjectionMode, Lossless: value.Feature.Lossless,
			LossReasons: append([]string{}, value.Feature.LossReasons...),
		}
		if value.Feature.SourceResourceType != "" {
			result.Feature.SourceResourceType = &value.Feature.SourceResourceType
		}
		if value.Feature.SourcePath != "" {
			result.Feature.SourcePath = &value.Feature.SourcePath
		}
		trace := loomapi.CellTraceTrace{
			RowId: value.Trace.RowID, Column: value.Trace.Column, Value: value.Trace.Value,
			Status: loomapi.CellTraceTraceStatus(value.Trace.Status), HasMore: value.Trace.HasMore,
			NextOffset: value.Trace.NextOffset, Complete: value.Trace.Complete,
		}
		if value.Trace.OmissionCode != "" {
			trace.OmissionCode = &value.Trace.OmissionCode
		}
		trace.Contributions = make([]loomapi.CellTraceContribution, 0, len(value.Trace.Contributions))
		for _, contribution := range value.Trace.Contributions {
			resourceType, resourceID := contribution.ResourceType, contribution.ResourceID
			wire := loomapi.CellTraceContribution{Value: contribution.Value}
			if resourceType != "" {
				wire.ResourceType = &resourceType
			}
			if resourceID != "" {
				wire.ResourceId = &resourceID
			}
			trace.Contributions = append(trace.Contributions, wire)
		}
		result.Trace = trace
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerCell200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerCell", err)
	return loomapi.NewTraceExplorerCellErrorResponse(status, failure)
}

func (r *Handlers) TraceExplorerRowLineage(ctx context.Context, request loomapi.TraceExplorerRowLineageRequestObject) (loomapi.TraceExplorerRowLineageResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.TraceExplorerRowLineageJSONRequestBody) (loomapi.RowLineageResponse, error) {
		var result loomapi.RowLineageResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("rowLineage", errors.New("receiptId, outputId, and rowId are required"))
		}
		request := rowdef.RowLineageRequest{
			Project: project, ExplorerID: explorerID,
			ReceiptID: body.ReceiptId, OutputID: body.OutputId, RowID: body.RowId,
		}
		if body.Offset != nil {
			request.Offset = *body.Offset
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		value, err := h.application.RowLineage(ctx, request)
		if err != nil {
			return result, err
		}
		result.ReceiptId = value.ReceiptID
		result.OutputId = value.OutputID
		result.RowId = value.RowID
		result.Status = loomapi.RowLineageResponseStatus(value.Status)
		result.HasMore = &value.HasMore
		if value.NextOffset != nil {
			result.NextOffset = value.NextOffset
		}
		contributors := make([]loomapi.RowLineageContributor, 0, len(value.Contributors))
		for _, contributor := range value.Contributors {
			contributors = append(contributors, loomapi.RowLineageContributor{
				ResourceType: contributor.ResourceType, ResourceId: contributor.ResourceID,
				OccurrenceKey: contributor.OccurrenceKey,
			})
		}
		result.Contributors = &contributors
		if value.ReasonCode != "" {
			result.ReasonCode = &value.ReasonCode
		}
		if value.Operation != "" {
			result.Operation = &value.Operation
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.TraceExplorerRowLineage200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "traceExplorerRowLineage", err)
	return loomapi.NewTraceExplorerRowLineageErrorResponse(status, failure)
}

func (r *Handlers) DescribeExplorerAuthoringNetwork(ctx context.Context, request loomapi.DescribeExplorerAuthoringNetworkRequestObject) (loomapi.DescribeExplorerAuthoringNetworkResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.AuthoringNetworkRequest) (loomapi.AuthoringNetworkResponse, error) {
		var empty loomapi.AuthoringNetworkResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return empty, err
		}
		if body == nil {
			return empty, explorer.MalformedRouteError("describeExplorerAuthoringNetwork", nil)
		}
		input, err := directAuthoringJSON[lifecycle.AuthoringNetworkRequest](*body)
		if err != nil {
			return empty, explorer.MalformedRouteError("describeExplorerAuthoringNetwork", err)
		}
		input.Project, input.ExplorerID = project, explorerID
		value, err := h.application.DescribeAuthoringNetwork(ctx, input)
		if err != nil {
			return empty, err
		}
		return directAuthoringJSON[loomapi.AuthoringNetworkResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DescribeExplorerAuthoringNetwork200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "describeExplorerAuthoringNetwork", err)
	return loomapi.NewDescribeExplorerAuthoringNetworkErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerColumnPolicy(ctx context.Context, request loomapi.ProposeExplorerColumnPolicyRequestObject) (loomapi.ProposeExplorerColumnPolicyResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ColumnPolicyProposalRequest) (loomapi.ColumnPolicyProposal, error) {
		var empty loomapi.ColumnPolicyProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return empty, err
		}
		if body == nil {
			return empty, explorer.MalformedRouteError("proposeExplorerColumnPolicy", nil)
		}
		input, err := directAuthoringJSON[proposal.ColumnPolicyProposalRequest](*body)
		if err != nil {
			return empty, explorer.MalformedRouteError("proposeExplorerColumnPolicy", err)
		}
		input.Project, input.ExplorerID = project, explorerID
		value, err := h.application.ProposeColumnPolicy(ctx, input)
		if err != nil {
			return empty, err
		}
		return directAuthoringJSON[loomapi.ColumnPolicyProposal](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerColumnPolicy200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerColumnPolicy", err)
	return loomapi.NewProposeExplorerColumnPolicyErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerConstructionChoices(ctx context.Context, request loomapi.SearchExplorerConstructionChoicesRequestObject) (loomapi.SearchExplorerConstructionChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionChoiceSearchRequest) (loomapi.ConstructionChoiceSearchResponse, error) {
		var result loomapi.ConstructionChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-choices", errors.New("request body and exact source identity are required"))
		}
		sourceJSON, err := body.Source.MarshalJSON()
		if err != nil {
			return result, explorer.MalformedRouteError("construction-choices", err)
		}
		var source construction.ConstructionChoiceSearchSource
		if err := json.Unmarshal(sourceJSON, &source); err != nil {
			return result, explorer.MalformedRouteError("construction-choices", err)
		}
		request := construction.ConstructionChoiceSearchRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, OutputID: body.OutputId, Source: source,
		}
		if body.OccurrenceId != nil {
			request.OccurrenceID = *body.OccurrenceId
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		value, err := h.application.SearchConstructionChoices(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ConstructionChoiceSearchResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerConstructionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerConstructionChoices", err)
	return loomapi.NewSearchExplorerConstructionChoicesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerRelatedExpandChoices(ctx context.Context, request loomapi.SearchExplorerRelatedExpandChoicesRequestObject) (loomapi.SearchExplorerRelatedExpandChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RelatedExpandChoiceSearchRequest) (loomapi.RelatedExpandChoiceSearchResponse, error) {
		var result loomapi.RelatedExpandChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("related-expand-choices", errors.New("request body with exact stage and target identities is required"))
		}
		request := construction.RelatedExpandChoiceSearchRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, StageID: body.StageId, AnchorColumnID: body.AnchorColumnId, TargetResourceType: body.TargetResourceType,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		if body.SavedGraphOnly != nil {
			request.SavedGraphOnly = *body.SavedGraphOnly
		}
		value, err := h.application.SearchRelatedExpandChoices(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RelatedExpandChoiceSearchResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandChoices", err)
	return loomapi.NewSearchExplorerRelatedExpandChoicesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerRelatedFieldChoices(ctx context.Context, request loomapi.SearchExplorerRelatedFieldChoicesRequestObject) (loomapi.SearchExplorerRelatedFieldChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RelatedFieldChoiceSearchRequest) (loomapi.RelatedFieldChoiceSearchResponse, error) {
		var result loomapi.RelatedFieldChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("related-field-choices", errors.New("request body with exact stage identity is required"))
		}
		request := construction.RelatedFieldChoiceSearchRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, StageID: body.StageId,
		}
		if body.Query != nil {
			request.Query = *body.Query
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		value, err := h.application.SearchRelatedFieldChoices(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RelatedFieldChoiceSearchResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedFieldChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedFieldChoices", err)
	return loomapi.NewSearchExplorerRelatedFieldChoicesErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerRelatedExpandContributors(ctx context.Context, request loomapi.SearchExplorerRelatedExpandContributorsRequestObject) (loomapi.SearchExplorerRelatedExpandContributorsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RelatedExpandContributorChoiceSearchRequest) (loomapi.RelatedExpandContributorChoiceSearchResponse, error) {
		var result loomapi.RelatedExpandContributorChoiceSearchResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("related-expand-contributors", errors.New("request body with exact stage and route choice identities is required"))
		}
		request := construction.RelatedExpandContributorChoiceSearchRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, StageID: body.StageId, RouteChoiceID: body.RouteChoiceId,
		}
		if body.Query != nil {
			request.Query = *body.Query
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		value, err := h.application.SearchRelatedExpandContributorChoices(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RelatedExpandContributorChoiceSearchResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerRelatedExpandContributors200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerRelatedExpandContributors", err)
	return loomapi.NewSearchExplorerRelatedExpandContributorsErrorResponse(status, failure)
}

func (r *Handlers) SearchExplorerPopulationRoutes(ctx context.Context, request loomapi.SearchExplorerPopulationRoutesRequestObject) (loomapi.SearchExplorerPopulationRoutesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PopulationRoutesRequest) (loomapi.PopulationRoutesResponse, error) {
		var result loomapi.PopulationRoutesResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("population-routes", errors.New("request body is required"))
		}
		request := population.PopulationRoutesRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			OutputID: body.OutputId, SelectionRevisionID: body.SelectionRevisionId,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		value, err := h.application.SearchPopulationRoutes(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.PopulationRoutesResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.SearchExplorerPopulationRoutes200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "searchExplorerPopulationRoutes", err)
	return loomapi.NewSearchExplorerPopulationRoutesErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerColumnSource(ctx context.Context, request loomapi.GetExplorerColumnSourceRequestObject) (loomapi.GetExplorerColumnSourceResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ColumnSourceRequest) (loomapi.ColumnSourceResponse, error) {
		var result loomapi.ColumnSourceResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("column-source", errors.New("request body is required"))
		}
		value, err := h.application.ColumnSource(ctx, lifecycle.ColumnSourceRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			OutputID: body.OutputId, Column: body.Column,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ColumnSourceResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerColumnSource200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerColumnSource", err)
	return loomapi.NewGetExplorerColumnSourceErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerConstructionChoice(ctx context.Context, request loomapi.ProposeExplorerConstructionChoiceRequestObject) (loomapi.ProposeExplorerConstructionChoiceResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionChoiceProposalRequest) (loomapi.ConstructionChoiceProposalResponse, error) {
		var result loomapi.ConstructionChoiceProposalResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-choice-proposal", nil)
		}
		choices := make([]construction.ConstructionChoiceProposalSelection, len(body.ConstructionChoices))
		for index, choice := range body.ConstructionChoices {
			choices[index] = construction.ConstructionChoiceProposalSelection{
				ChoiceID: choice.ChoiceId, Form: capability.ConstructionChoiceForm(choice.Form),
			}
			if choice.RowValuePolicy != nil {
				choices[index].RowValuePolicy = ws.ConstructionRowValuePolicy(*choice.RowValuePolicy)
			}
			if choice.FrameId != nil {
				choices[index].FrameID = *choice.FrameId
			}
			if choice.Title != nil {
				choices[index].Title = choice.Title
			}
		}
		request := construction.ConstructionChoiceProposalRequest{
			CommandID: body.CommandId, Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, ConstructionChoices: choices,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		options, encoder := h.proposalPreviewOptions(project, body.OutputId, body.Limit, executionarango.ExplorerPreviewTimeout, false)
		run, err := h.application.ProposeConstructionChoiceAndPreview(ctx, request, options, func(value construction.ConstructionChoiceProposalResponse) error {
			var err error
			result, err = directAuthoringJSON[loomapi.ConstructionChoiceProposalResponse](value)
			if err != nil {
				return err
			}
			if value.PreviewReceiptID == "" || value.CandidateWorkspaceDigest == "" || len(value.CandidateColumnIDs) != len(value.ConstructionChoices) {
				return explorer.MalformedRouteError("construction-choice-proposal", nil)
			}
			return nil
		})
		if err != nil {
			return result, proposalPreviewFailure(err)
		}
		preview, err := encoder.response(run)
		result.PreviewDurationMs = time.Since(run.Started).Milliseconds()
		if err != nil {
			return result, err
		}
		result.Preview = preview
		result.PreviewStatus = loomapi.ConstructionChoiceProposalResponsePreviewStatusREADY
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerConstructionChoice200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstructionChoice", err)
	return loomapi.NewProposeExplorerConstructionChoiceErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerConstructionInputs(ctx context.Context, request loomapi.GetExplorerConstructionInputsRequestObject) (loomapi.GetExplorerConstructionInputsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionInputsRequest) (loomapi.ConstructionInputsResponse, error) {
		var result loomapi.ConstructionInputsResponse
		if body == nil {
			_, err := h.readConstructionInputs(ctx, project, explorerID, nil)
			return result, err
		}
		request := &constructioninputs.Request{
			SnapshotToken: body.SnapshotToken, ExpectedDraftVersion: int64(body.ExpectedDraftVersion),
			ExpectedDraftDigest: body.ExpectedDraftDigest,
		}
		if body.Query != nil {
			request.Query = *body.Query
		}
		if body.Cursor != nil {
			request.Cursor = *body.Cursor
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		value, err := h.readConstructionInputs(ctx, project, explorerID, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ConstructionInputsResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionInputs200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionInputs", err)
	return loomapi.NewGetExplorerConstructionInputsErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerConstructionCapabilities(ctx context.Context, request loomapi.GetExplorerConstructionCapabilitiesRequestObject) (loomapi.GetExplorerConstructionCapabilitiesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionCapabilitiesRequest) (loomapi.ConstructionCapabilitiesResponse, error) {
		var result loomapi.ConstructionCapabilitiesResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-capabilities", nil)
		}
		groupStepID := ""
		if body.GroupStepId != nil {
			groupStepID = *body.GroupStepId
		}
		value, err := h.application.GetConstructionCapabilities(ctx, construction.ConstructionCapabilitiesRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, StageID: body.StageId, GroupStepID: groupStepID,
			IncludeGroupChoices: body.IncludeGroupChoices != nil && *body.IncludeGroupChoices,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionCapabilities200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionCapabilities", err)
	return loomapi.NewGetExplorerConstructionCapabilitiesErrorResponse(status, failure)
}

func (r *Handlers) DiscoverExplorerConstructionCategories(ctx context.Context, request loomapi.DiscoverExplorerConstructionCategoriesRequestObject) (loomapi.DiscoverExplorerConstructionCategoriesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionCategoryDiscoveryRequest) (loomapi.ConstructionCategoryDiscoveryResponse, error) {
		var result loomapi.ConstructionCategoryDiscoveryResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-category-discovery", nil)
		}
		request, err := directAuthoringJSON[construction.ConstructionCategoryDiscoveryRequest](body)
		if err != nil {
			return result, explorer.MalformedRouteError("construction-category-discovery", err)
		}
		request.Project, request.ExplorerID = project, explorerID
		value, err := h.application.DiscoverConstructionCategories(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ConstructionCategoryDiscoveryResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.DiscoverExplorerConstructionCategories200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "discoverExplorerConstructionCategories", err)
	return loomapi.NewDiscoverExplorerConstructionCategoriesErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerConstructionColumnPopulation(ctx context.Context, request loomapi.GetExplorerConstructionColumnPopulationRequestObject) (loomapi.GetExplorerConstructionColumnPopulationResponseObject, error) {
	if r == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionColumnPopulation", explorer.ExplorerUnavailable("construction-column-population", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.GetExplorerConstructionColumnPopulation503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionColumnPopulationRequest) (loomapi.ConstructionColumnPopulationResponse, error) {
		var result loomapi.ConstructionColumnPopulationResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-column-population", nil)
		}
		value, err := h.application.GetConstructionColumnPopulation(ctx, construction.ConstructionColumnPopulationRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, StageID: body.StageId, ColumnIDs: body.ColumnIds,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.ConstructionColumnPopulationResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.GetExplorerConstructionColumnPopulation200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerConstructionColumnPopulation", err)
	return loomapi.NewGetExplorerConstructionColumnPopulationErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerConstruction(ctx context.Context, request loomapi.ProposeExplorerConstructionRequestObject) (loomapi.ProposeExplorerConstructionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.ConstructionProposalRequest) (loomapi.ConstructionProposalResponse, error) {
		var result loomapi.ConstructionProposalResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("construction-proposal", nil)
		}
		candidate, err := directAuthoringJSON[ws.Construction](body.CandidateConstruction)
		if err != nil {
			return result, explorer.MalformedRouteError("construction-proposal", err)
		}
		changedStepID := ""
		if body.ChangedStepId != nil {
			changedStepID = *body.ChangedStepId
		}
		request := construction.ConstructionProposalRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: int64(body.ExpectedDraftVersion), ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, ChangedStepID: changedStepID,
			CandidateConstruction: candidate,
		}
		if body.PivotSources != nil {
			request.PivotSources = make([]construction.ConstructionPivotSourceSelection, 0, len(*body.PivotSources))
			for _, selection := range *body.PivotSources {
				request.PivotSources = append(request.PivotSources, construction.ConstructionPivotSourceSelection{
					ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
				})
			}
		}
		if body.FilterSources != nil {
			request.FilterSources = make([]rowsource.ConstructionFilterSourceSelection, 0, len(*body.FilterSources))
			for _, selection := range *body.FilterSources {
				request.FilterSources = append(request.FilterSources, rowsource.ConstructionFilterSourceSelection{
					ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
				})
			}
		}
		if body.DeriveSources != nil {
			request.DeriveSources = make([]rowsource.ConstructionDeriveSourceSelection, 0, len(*body.DeriveSources))
			for _, selection := range *body.DeriveSources {
				request.DeriveSources = append(request.DeriveSources, rowsource.ConstructionDeriveSourceSelection{
					ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
				})
			}
		}
		if body.UnpivotSources != nil {
			request.UnpivotSources = make([]rowsource.ConstructionUnpivotSourceSelection, 0, len(*body.UnpivotSources))
			for _, selection := range *body.UnpivotSources {
				request.UnpivotSources = append(request.UnpivotSources, rowsource.ConstructionUnpivotSourceSelection{
					ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
				})
			}
		}
		if body.ExpandSources != nil {
			request.ExpandSources = make([]rowsource.ConstructionExpandSourceSelection, 0, len(*body.ExpandSources))
			for _, selection := range *body.ExpandSources {
				request.ExpandSources = append(request.ExpandSources, rowsource.ConstructionExpandSourceSelection{
					ChoiceID: selection.ChoiceId, ColumnID: selection.ColumnId,
				})
			}
		}
		if body.GroupSources != nil {
			request.GroupSources = make([]construction.ConstructionGroupSourceSelection, 0, len(*body.GroupSources))
			for _, selection := range *body.GroupSources {
				request.GroupSources = append(request.GroupSources, construction.ConstructionGroupSourceSelection{RowChoiceID: selection.RowChoiceId, ColumnID: selection.ColumnId})
			}
		}
		if body.GroupSource != nil {
			request.GroupSource = &construction.ConstructionGroupSourceSelection{
				RowChoiceID: body.GroupSource.RowChoiceId,
				ColumnID:    body.GroupSource.ColumnId,
			}
		}
		if body.RemoveStepIds != nil {
			request.RemoveStepIDs = append([]string(nil), (*body.RemoveStepIds)...)
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		options, encoder := h.proposalPreviewOptions(project, body.OutputId, body.Limit, 0, true)
		options.Budget = func(ctx context.Context) (context.Context, time.Duration) {
			return constructionProposalPreviewBudget(ctx, candidate)
		}
		run, err := h.application.ProposeConstructionAndPreview(ctx, request, options, func(value construction.ConstructionProposalResponse) error {
			var err error
			result, err = directAuthoringJSON[loomapi.ConstructionProposalResponse](value)
			if err != nil {
				return err
			}
			if value.PreviewStatus == "NEEDS_REPAIR" {
				return nil
			}
			if value.PreviewStatus != "PREVIEW_PENDING" || result.ProposalId == nil || *result.ProposalId == "" {
				return explorer.MalformedRouteError("construction-proposal", nil)
			}
			return nil
		})
		if err != nil || !run.Completed {
			return result, proposalPreviewFailure(err)
		}
		preview, err := encoder.response(run)
		result.PreviewDurationMs = time.Since(run.Started).Milliseconds()
		if err != nil {
			return result, err
		}
		result.Preview = &preview
		result.PreviewStatus = loomapi.ConstructionProposalResponsePreviewStatus("READY")
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerConstruction200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerConstruction", err)
	return loomapi.NewProposeExplorerConstructionErrorResponse(status, failure)
}

func (r *Handlers) ListExplorerRowDefinitionChoices(ctx context.Context, request loomapi.ListExplorerRowDefinitionChoicesRequestObject) (loomapi.ListExplorerRowDefinitionChoicesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, params loomapi.ListExplorerRowDefinitionChoicesParams) (loomapi.RowDefinitionChoicesResponse, error) {
		var result loomapi.RowDefinitionChoicesResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.ListRowDefinitionChoices(ctx, rowdef.RowDefinitionChoicesRequest{
			Project: project, ExplorerID: explorerID, OutputID: params.OutputId, SnapshotToken: params.SnapshotToken,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RowDefinitionChoicesResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Params)
	if err == nil {
		return loomapi.ListExplorerRowDefinitionChoices200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listExplorerRowDefinitionChoices", err)
	return loomapi.NewListExplorerRowDefinitionChoicesErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerRowDefinition(ctx context.Context, request loomapi.ProposeExplorerRowDefinitionRequestObject) (loomapi.ProposeExplorerRowDefinitionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.RowDefinitionProposalRequest) (loomapi.RowDefinitionProposal, error) {
		var result loomapi.RowDefinitionProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("row-definition-proposal", nil)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return result, explorer.MalformedRouteError("row-definition-proposal", err)
		}
		var input struct {
			SnapshotToken        string                        `json:"snapshotToken"`
			ExpectedDraftVersion int64                         `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string                        `json:"expectedDraftDigest"`
			OutputID             string                        `json:"outputId"`
			Selection            rowdef.RowDefinitionSelection `json:"selection"`
			Limit                int                           `json:"limit,omitempty"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return result, explorer.MalformedRouteError("row-definition-proposal", err)
		}
		value, err := h.application.ProposeRowDefinition(ctx, rowdef.RowDefinitionProposalRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: input.SnapshotToken,
			ExpectedDraftVersion: input.ExpectedDraftVersion, ExpectedDraftDigest: input.ExpectedDraftDigest,
			OutputID: input.OutputID, Selection: input.Selection, Limit: input.Limit,
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.RowDefinitionProposal](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerRowDefinition200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerRowDefinition", err)
	return loomapi.NewProposeExplorerRowDefinitionErrorResponse(status, failure)
}

func (h *Handlers) SaveProjectViewDefaults(ctx context.Context, request loomapi.SaveProjectViewDefaultsRequestObject) (loomapi.SaveProjectViewDefaultsResponseObject, error) {
	fail := func(err error) (loomapi.SaveProjectViewDefaultsResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewSaveProjectViewDefaultsErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.views.SaveDefaults(ctx, project, func() (viewer.ViewBuilderDefaultsRequest, error) {
		var body viewer.ViewBuilderDefaultsRequest
		err := decodeViewBuilderBody(request.Body, &body)
		return body, err
	}, h.viewMutationAccess(project, resource))
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.SaveProjectViewDefaults200JSONResponse(result), nil
}

func (h *Handlers) SaveProjectViewConfig(ctx context.Context, request loomapi.SaveProjectViewConfigRequestObject) (loomapi.SaveProjectViewConfigResponseObject, error) {
	fail := func(err error) (loomapi.SaveProjectViewConfigResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewSaveProjectViewConfigErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.views.SaveConfig(ctx, project, string(request.ExplorerId), authscope.SubjectFromContext(ctx), func() (viewer.ViewBuilderSaveRequest, error) {
		var body viewer.ViewBuilderSaveRequest
		err := decodeViewBuilderBody(request.Body, &body)
		return body, err
	}, h.viewMutationAccess(project, resource))
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.SaveProjectViewConfig200JSONResponse(result), nil
}

func (h *Handlers) DeleteProjectViewConfig(ctx context.Context, request loomapi.DeleteProjectViewConfigRequestObject) (loomapi.DeleteProjectViewConfigResponseObject, error) {
	fail := func(err error) (loomapi.DeleteProjectViewConfigResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewDeleteProjectViewConfigErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.views.DeleteConfig(ctx, project, string(request.ExplorerId), request.Params.ExpectedRevisionId, h.viewMutationAccess(project, resource))
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.DeleteProjectViewConfig200JSONResponse(result), nil
}
