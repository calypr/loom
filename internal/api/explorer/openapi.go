package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/projectconfig"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/rowsource"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
	"github.com/calypr/loom/internal/projectid"
)

func (r *Handlers) PrepareExplorerArtifact(ctx context.Context, request loomapi.PrepareExplorerArtifactRequestObject) (loomapi.PrepareExplorerArtifactResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PrepareExplorerArtifactJSONRequestBody) (loomapi.Artifact, error) {
		var response loomapi.Artifact
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return response, err
		}
		if h == nil || h.application == nil {
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
		}
		if body == nil {
			return response, explorer.MalformedRouteError("artifact", errors.New("request body is required"))
		}
		result, err := h.application.PrepareArtifact(ctx, lifecycle.ArtifactRequest{
			Project: project, ExplorerID: explorerID, RevisionID: body.RevisionId,
			OutputID: body.OutputId, IdempotencyKey: body.IdempotencyKey,
		})
		if err != nil {
			return response, err
		}
		if result.Record == nil || result.Record.State != explorer.ArtifactComplete || result.Record.CompletedAt == nil {
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact preparation did not produce a complete archive")
		}
		return directAuthoringJSON[loomapi.Artifact](*result.Record)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PrepareExplorerArtifact200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "prepareExplorerArtifact", err)
	return loomapi.NewPrepareExplorerArtifactErrorResponse(status, failure)
}

func (r *Handlers) DownloadExplorerArtifact(ctx context.Context, request loomapi.DownloadExplorerArtifactRequestObject) (loomapi.DownloadExplorerArtifactResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, artifactID string) (loomapi.DownloadExplorerArtifact200ApplicationzipResponse, error) {
		var response loomapi.DownloadExplorerArtifact200ApplicationzipResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return response, err
		}
		if h == nil || h.application == nil {
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
		}
		reader, record, err := h.application.OpenArtifact(ctx, lifecycle.ArtifactOpenRequest{Project: project, ExplorerID: explorerID, ArtifactID: artifactID})
		if err != nil {
			return response, err
		}
		if reader == nil || record == nil || record.State != explorer.ArtifactComplete {
			if reader != nil {
				_ = reader.Close()
			}
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact is not complete")
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": record.Filename})
		digest := record.ArchiveSHA256
		return loomapi.DownloadExplorerArtifact200ApplicationzipResponse{
			Body: reader, ContentLength: record.Bytes,
			Headers: loomapi.DownloadExplorerArtifact200ResponseHeaders{ContentDisposition: &disposition, XLoomArtifactSHA256: &digest},
		}, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.ArtifactId)
	if err == nil {
		return loomapi.DownloadExplorerArtifact200ApplicationzipResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "downloadExplorerArtifact", err)
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) && lifecycleErr.Code == "ARTIFACT_EXPIRED" {
		status = http.StatusGone
	}
	return loomapi.NewDownloadExplorerArtifactErrorResponse(status, failure)
}

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

func (h *Handlers) previewSavedReceipt(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody) (loomapi.PreviewResponse, error) {
	return h.previewSavedReceiptWithTimeout(ctx, project, explorerID, body, executionarango.ExplorerPreviewTimeout, false)
}

func (h *Handlers) previewSavedReceiptWithTimeout(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody, timeout time.Duration, allowSampledAggregate bool) (loomapi.PreviewResponse, error) {
	var result loomapi.PreviewResponse
	if err := h.authorizeAuthoringRead(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("preview", errors.New("receiptId and outputId are required"))
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
		if limit == 0 {
			limit = -1
		}
	}
	previewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var encoder *previewResponseEncoder
	preview, err := h.application.Preview(previewCtx, lifecycle.PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, OutputID: body.OutputId, Limit: limit, AllowSampledAggregatePreview: allowSampledAggregate, SinkFactory: func(receipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
		var encoderErr error
		encoder, encoderErr = newPreviewResponseEncoder(receipt, body.OutputId, columns, maxExplorerPreviewResponseBytes)
		if encoderErr != nil {
			return nil, encoderErr
		}
		return encoder.Visit, nil
	}})
	if err != nil {
		return result, previewRouteError(err)
	}
	if encoder == nil {
		return result, previewRouteError(errors.New("native preview response sink was not configured"))
	}
	return previewResponseFromEncoder(encoder, preview.Summary)
}

func (h *Handlers) publishReceipt(ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.PublishExplorerJSONRequestBody) (loomapi.PublishResponse, error) {
	var result loomapi.PublishResponse
	if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("publish", errors.New("receiptId is required"))
	}
	value, err := h.application.Publish(ctx, lifecycle.PublishRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, Actor: authscope.SubjectFromContext(ctx)})
	if err != nil {
		return result, err
	}
	outputs := make([]loomapi.PublicationOutput, 0, len(value.Revision.Materializations))
	for _, materialization := range value.Revision.Materializations {
		outputs = append(outputs, loomapi.PublicationOutput{OutputId: firstNonEmpty(materialization.OutputID, materialization.Output), State: "READY", MaterializationId: materialization.MaterializationID})
	}
	return loomapi.PublishResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderPublication, ReceiptId: value.Receipt.ID, RevisionId: value.Revision.ID, State: string(value.Revision.Status), Outputs: outputs, Diagnostics: []loomapi.Diagnostic{}}, nil
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

func (r *Handlers) CreateInterpretationRevision(ctx context.Context, request loomapi.CreateInterpretationRevisionRequestObject) (loomapi.CreateInterpretationRevisionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, authResourcePath string, body *loomapi.CreateInterpretationRevisionJSONRequestBody) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretations", errors.New("request body is required"))
		}
		applicability, err := directAuthoringJSON[interpretation.InterpretationApplicability](body.Applicability)
		if err != nil {
			return result, explorer.MalformedRouteError("interpretations", err)
		}
		rules, err := directAuthoringJSON[[]interpretation.InterpretationRule](body.Rules)
		if err != nil {
			return result, explorer.MalformedRouteError("interpretations", err)
		}
		parentRevisionID := ""
		if body.ParentRevisionId != nil {
			parentRevisionID = *body.ParentRevisionId
		}
		value, err := h.application.InterpretationService().CreateInterpretationRevision(ctx, interpretation.CreateInterpretationRevisionRequest{
			Project: project, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
			Applicability: applicability, Rules: rules, Explanation: body.Explanation,
			Author: authscope.SubjectFromContext(ctx),
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevision", err)
	return loomapi.NewCreateInterpretationRevisionErrorResponse(status, failure)
}

func (r *Handlers) CreateInterpretationRevisionFromColumn(ctx context.Context, request loomapi.CreateInterpretationRevisionFromColumnRequestObject) (loomapi.CreateInterpretationRevisionFromColumnResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.CreateInterpretationRevisionFromColumnJSONRequestBody) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-create", errors.New("request body is required"))
		}
		parentRevisionID := ""
		if body.ParentRevisionId != nil {
			parentRevisionID = *body.ParentRevisionId
		}
		value, err := h.application.CreateInterpretationRevisionFromColumn(ctx, lifecycle.CreateInterpretationRevisionFromColumnRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, Column: body.Column, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
			Explanation: body.Explanation, Author: authscope.SubjectFromContext(ctx),
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevisionFromColumn201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevisionFromColumn", err)
	return loomapi.NewCreateInterpretationRevisionFromColumnErrorResponse(status, failure)
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

func (r *Handlers) GetInterpretationRevision(ctx context.Context, request loomapi.GetInterpretationRevisionRequestObject) (loomapi.GetInterpretationRevisionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, revisionID string) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.InterpretationService().GetInterpretationRevision(ctx, interpretation.GetInterpretationRevisionRequest{Project: project, RevisionID: revisionID})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), request.RevisionId)
	if err == nil {
		return loomapi.GetInterpretationRevision200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getInterpretationRevision", err)
	return loomapi.NewGetInterpretationRevisionErrorResponse(status, failure)
}

func (r *Handlers) ListInterpretationLibraries(ctx context.Context, request loomapi.ListInterpretationLibrariesRequestObject) (loomapi.ListInterpretationLibrariesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project string) (loomapi.InterpretationLibraryListResponse, error) {
		var result loomapi.InterpretationLibraryListResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.application.InterpretationService().ListInterpretationLibraries(ctx, project)
		if err != nil {
			return result, err
		}
		result.Project = value.Project
		result.Libraries = make([]loomapi.InterpretationLibraryView, 0, len(value.Libraries))
		for _, view := range value.Libraries {
			library, convertErr := directAuthoringJSON[loomapi.InterpretationLibrary](view.Library)
			if convertErr != nil {
				return result, convertErr
			}
			wire := loomapi.InterpretationLibraryView{Library: library}
			if view.Head != nil {
				head, convertErr := directAuthoringJSON[loomapi.InterpretationRevision](*view.Head)
				if convertErr != nil {
					return result, convertErr
				}
				wire.Head = &head
			}
			result.Libraries = append(result.Libraries, wire)
		}
		return result, nil
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.ListInterpretationLibraries200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listInterpretationLibraries", err)
	return loomapi.NewListInterpretationLibrariesErrorResponse(status, failure)
}

func (r *Handlers) PreviewInterpretationCandidate(ctx context.Context, request loomapi.PreviewInterpretationCandidateRequestObject) (loomapi.PreviewInterpretationCandidateResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PreviewInterpretationCandidateJSONRequestBody) (loomapi.InterpretationPreviewResponse, error) {
		var result loomapi.InterpretationPreviewResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-preview", errors.New("request body is required"))
		}
		limit := dataframeexecution.DefaultPreviewLimit
		if body.Limit != nil {
			limit = *body.Limit
		}
		previewCtx, cancel := context.WithTimeout(ctx, executionarango.ExplorerPreviewTimeout)
		defer cancel()
		value, err := h.application.PreviewInterpretationCandidate(previewCtx, interpretation.PreviewInterpretationCandidateRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, Column: body.Column, RevisionID: body.RevisionId, Limit: limit,
		})
		if err != nil {
			var lifecycleErr *ports.Error
			if errors.As(err, &lifecycleErr) {
				return result, err
			}
			return result, previewRouteError(err)
		}
		result, err = directAuthoringJSON[loomapi.InterpretationPreviewResponse](value)
		if err != nil {
			return result, err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return result, err
		}
		if len(encoded) > maxExplorerPreviewResponseBytes {
			return result, previewRouteError(&previewResponseTooLargeError{Limit: maxExplorerPreviewResponseBytes})
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PreviewInterpretationCandidate200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewInterpretationCandidate", err)
	return loomapi.NewPreviewInterpretationCandidateErrorResponse(status, failure)
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

func (r *Handlers) createExplorer(ctx context.Context, rawProject, authResourcePath string, body *loomapi.CreateExplorerJSONRequestBody) (loomapi.CreateExplorer201JSONResponse, error) {
	var result loomapi.CreateExplorer201JSONResponse
	if r == nil || r.application == nil || r.authorizer == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
		}
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	if body == nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", nil)
	}
	var request struct {
		Name             string `json:"name"`
		Title            string `json:"title,omitempty"`
		SourceExplorerID string `json:"sourceExplorerId,omitempty"`
	}
	raw, err := json.Marshal(*body)
	if err != nil || httpapi.DecodeStrict(raw, &request) != nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", err)
	}
	value, err := r.application.Create(ctx, lifecycle.CreateRequest{Project: project, Name: request.Name, Title: request.Title, SourceExplorerID: request.SourceExplorerID, Actor: authscope.SubjectFromContext(ctx)})
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Handlers) getExplorer(ctx context.Context, rawProject, rawID string) (loomapi.GetExplorer200JSONResponse, error) {
	var result loomapi.GetExplorer200JSONResponse
	if r == nil || r.application == nil || r.authorizeRead == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizeRead(ctx, principal, project); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
		}
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	value, err := r.application.Get(ctx, project, rawID)
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	if id := value.Generated.Publication.ExecutionID; id != "" && r.authorizeExecution != nil {
		if err := r.authorizeExecution(ctx, id); err != nil {
			userErr, concealed := dataframeerrors.AsUserError(err)
			if concealed && dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
				return result, legacyOperationFailure(http.StatusNotFound, "EXPLORER_NOT_FOUND", "Explorer not found", err)
			}
			status, body := httpapi.MapServiceError(err, ctx)
			if status == http.StatusNotFound {
				return result, legacyOperationFailure(status, "EXPLORER_NOT_FOUND", "Explorer not found", err)
			}
			if status == http.StatusServiceUnavailable {
				return result, legacyOperationFailure(status, "EXPLORER_UNAVAILABLE", body.Error.Message, err)
			}
			return result, legacyOperationFailure(http.StatusInternalServerError, "EXPLORER_UNAVAILABLE", body.Error.Message, err)
		}
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Handlers) publishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfig200JSONResponse, error) {
	var result loomapi.PublishRepositoryExplorerConfig200JSONResponse
	if r == nil || r.application == nil || r.authorizer == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(string(request.Project))
	generation := strings.TrimSpace(string(request.Generation))
	if project == "" || generation == "" {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "project and generation are required", nil)
	}
	authResourcePath := ""
	if request.Params.AuthResourcePath != nil {
		authResourcePath = authscope.NormalizeAuthResourcePath(strings.TrimSpace(*request.Params.AuthResourcePath))
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
		}
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	if request.Body == nil {
		return result, legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_WORKSPACE", "workspace is required", nil)
	}
	workspace := ws.Workspace(*request.Body)
	if err := workspace.ValidateForPublication(); err != nil {
		return result, legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_WORKSPACE", err.Error(), err)
	}
	commit := strings.TrimSpace(request.Params.XLoomSourceCommit)
	if commit == "" {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "X-Loom-Source-Commit is required", nil)
	}
	value, err := r.application.PublishRepository(ctx, lifecycle.RepositoryPublishRequest{Project: project, Generation: generation, Workspace: workspace, Commit: commit, Actor: authscope.SubjectFromContext(ctx)})
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	return loomapi.PublishRepositoryExplorerConfig200JSONResponse(loomapi.RawJSON{"project": project, "generation": generation, "explorerId": "default", "receiptId": value.Receipt.ID, "revisionId": value.Revision.ID, "executionId": value.Execution.ID, "recipe": value.Receipt.Bundle.Name, "translationVersion": value.Receipt.Bundle.TranslationVersion, "activated": true}), nil
}

func (r *Handlers) ListExplorers(ctx context.Context, request loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	value, err := func(r *Handlers, ctx context.Context, rawProject string) (loomapi.ListExplorers200JSONResponse, error) {
		var result loomapi.ListExplorers200JSONResponse
		if r == nil || r.application == nil || r.authorizeRead == nil {
			return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
		}
		project := projectid.Canonical(rawProject)
		principal, _ := authscope.PrincipalFromContext(ctx)
		if err := r.authorizeRead(ctx, principal, project); err != nil {
			if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
				return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
			}
			return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
		}
		value, err := r.application.List(ctx, project)
		if err != nil {
			return result, lifecycleLegacyFailure(err)
		}
		raw, err := json.Marshal(value.Summaries)
		if err != nil {
			return result, err
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		return result, nil
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.ListExplorers200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewListExplorersErrorResponse(status, failure)
}

func (r *Handlers) CreateExplorerExplicitGroupRevision(ctx context.Context, request loomapi.CreateExplorerExplicitGroupRevisionRequestObject) (loomapi.CreateExplorerExplicitGroupRevisionResponseObject, error) {
	var value loomapi.ExplicitGroupRevisionSummary
	var err error
	if r.application == nil {
		err = explorer.ExplorerUnavailable("explicit-groups", "AUTHORING_UNAVAILABLE", "explicit group authoring is not configured")
	} else {
		value, err = func(h *Handlers, ctx context.Context, request loomapi.CreateExplorerExplicitGroupRevisionRequestObject) (loomapi.ExplicitGroupRevisionSummary, error) {
			if err := h.authorizeAuthoringWrite(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath)); err != nil {
				return loomapi.ExplicitGroupRevisionSummary{}, err
			}
			if request.Body == nil {
				return loomapi.ExplicitGroupRevisionSummary{}, explorer.MalformedRouteError("explicit-groups", nil)
			}
			groups := make([]explicitgroup.ExplicitGroupInput, len(request.Body.Groups))
			for i, group := range request.Body.Groups {
				groups[i] = explicitgroup.ExplicitGroupInput{ID: group.Id, Label: group.Label, Ordinal: group.Ordinal, MemberIDs: group.MemberIds}
			}
			value, err := h.application.CreateExplicitGroupRevision(ctx, explicitgroup.ExplicitGroupCreateRequest{
				Project: string(request.Project), ExplorerID: string(request.ExplorerId), SnapshotToken: request.Body.SnapshotToken,
				SelectionID: request.SelectionRevision, IdempotencyKey: request.Body.IdempotencyKey, Groups: groups,
			})
			if err != nil {
				return loomapi.ExplicitGroupRevisionSummary{}, err
			}
			response := loomapi.ExplicitGroupRevisionSummary{
				RevisionId: value.RevisionID, SourceSelectionRevisionId: value.SourceSelectionRevisionID,
				GroupCount: value.GroupCount, MemberCount: value.MemberCount, CreatedAt: value.CreatedAt,
				Groups: make([]loomapi.ExplicitGroupSummary, 0, len(value.Groups)),
			}
			for _, group := range value.Groups {
				response.Groups = append(response.Groups, loomapi.ExplicitGroupSummary{Id: group.ID, Label: group.Label, Ordinal: group.Ordinal, MemberCount: group.MemberCount})
			}
			return response, nil
		}(r, ctx, request)
	}
	if err == nil {
		return loomapi.CreateExplorerExplicitGroupRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createExplorerExplicitGroupRevision", err)
	return loomapi.NewCreateExplorerExplicitGroupRevisionErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerPopulationMemberRemoval(ctx context.Context, request loomapi.ProposeExplorerPopulationMemberRemovalRequestObject) (loomapi.ProposeExplorerPopulationMemberRemovalResponseObject, error) {
	if r.application == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", explorer.ExplorerUnavailable("population-member-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerPopulationMemberRemoval503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PopulationMemberRemovalProposalRequest) (loomapi.PopulationMemberRemovalProposalResponse, error) {
		var result loomapi.PopulationMemberRemovalProposalResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("population-member-proposal", nil)
		}
		removedMember, err := directAuthoringJSON[explorerselection.ResourceRef](body.RemovedMember)
		if err != nil {
			return result, explorer.MalformedRouteError("population-member-proposal", err)
		}
		request := population.PopulationMemberRemovalProposalRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, BaseSelectionID: body.BaseSelectionRevisionId, RemovedMember: removedMember,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		var proposalID string
		options, encoder := h.proposalPreviewOptions(project, body.OutputId, body.Limit, executionarango.ExplorerPreviewTimeout, false)
		run, err := h.application.ProposePopulationMemberRemovalAndPreview(ctx, request, options, func(value population.PopulationMemberRemovalProposalResponse) error {
			if value.ProposalID == "" || value.BaseSelection == nil || value.CandidateSelection == nil || value.PreviewStatus != "PREVIEW_PENDING" {
				return explorer.MalformedRouteError("population-member-proposal", nil)
			}
			proposalID = value.ProposalID
			var err error
			result, err = directAuthoringJSON[loomapi.PopulationMemberRemovalProposalResponse](value)
			return err
		})
		if err != nil {
			return result, proposalPreviewFailure(err)
		}
		preview, err := encoder.response(run)
		result.PreviewDurationMs = time.Since(run.Started).Milliseconds()
		if err != nil {
			return result, err
		}
		if preview.ReceiptId != proposalID || preview.OutputId != body.OutputId {
			return result, explorer.MalformedRouteError("population-member-proposal", nil)
		}
		result.Preview = preview
		result.PreviewStatus = loomapi.PopulationMemberRemovalProposalResponsePreviewStatusREADY
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerPopulationMemberRemoval200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", err)
	return loomapi.NewProposeExplorerPopulationMemberRemovalErrorResponse(status, failure)
}

func (r *Handlers) CreateExplorer(ctx context.Context, request loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	value, err := r.createExplorer(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateExplorer201JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewCreateExplorerErrorResponse(status, failure)
}

func (r *Handlers) GetExplorer(ctx context.Context, request loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	value, err := r.getExplorer(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorer200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewGetExplorerErrorResponse(status, failure)
}

func (r *Handlers) PublishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	value, err := r.publishRepositoryExplorerConfig(ctx, request)
	if err == nil {
		return loomapi.PublishRepositoryExplorerConfig200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewPublishRepositoryExplorerConfigErrorResponse(status, failure)
}

func (r *Handlers) PreviewExplorer(ctx context.Context, request loomapi.PreviewExplorerRequestObject) (loomapi.PreviewExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.previewSavedReceipt(ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.PreviewExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewExplorer", err)
	return loomapi.NewPreviewExplorerErrorResponse(status, failure)
}

func (r *Handlers) PublishExplorer(ctx context.Context, request loomapi.PublishExplorerRequestObject) (loomapi.PublishExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.publishReceipt(ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.PublishExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "publishExplorer", err)
	return loomapi.NewPublishExplorerErrorResponse(status, failure)
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

func (h *Handlers) createSelection(ctx context.Context, request loomapi.CreateExplorerSelectionRequestObject) (loomapi.SelectionRevision, error) {
	var result loomapi.SelectionRevision
	if err := h.authorizeAuthoringWrite(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath)); err != nil {
		return result, err
	}
	if request.Body == nil {
		return result, explorer.MalformedRouteError("selection", errors.New("request body is required"))
	}
	body := request.Body
	intent := lifecycle.SelectionIntentCreateRequest{
		Project: string(request.Project), ExplorerID: string(request.ExplorerId), Actor: authscope.SubjectFromContext(ctx),
		SnapshotToken: body.SnapshotToken, IdempotencyKey: body.IdempotencyKey,
		Source: lifecycle.SelectionSourceIntent{Kind: string(body.Source.Kind)},
	}
	if body.Source.Resources != nil {
		intent.Source.Resources = selectionRefs(body.Source.Resources.Refs)
		if body.Source.Resources.ResourceType != nil {
			intent.ResourceType = *body.Source.Resources.ResourceType
		}
	}
	if source := body.Source.PublishedOutput; source != nil {
		intent.Source.PublishedOutput = &lifecycle.PublishedOutputIntent{RevisionID: source.RevisionId, OutputID: source.OutputId}
		if source.Filters != nil {
			filters, err := directAuthoringJSON[[]published.Filter](*source.Filters)
			if err != nil {
				return result, explorer.MalformedRouteError("selection", err)
			}
			intent.Source.PublishedOutput.Filters = filters
		}
	}
	if source := body.Source.SelectionRevision; source != nil {
		intent.Source.SelectionRevisionID = source.SelectionRevisionId
	}
	if body.Exclusions != nil {
		intent.Exclusions = selectionRefs(*body.Exclusions)
	}
	value, err := h.application.SelectionService().CreateSelection(ctx, intent)
	if err != nil {
		return result, err
	}
	return directAuthoringJSON[loomapi.SelectionRevision](value.Header)
}

func (h *Handlers) readSelection(ctx context.Context, request loomapi.GetExplorerSelectionRequestObject) (loomapi.SelectionPage, error) {
	var result loomapi.SelectionPage
	if err := h.authorizeAuthoringRead(ctx, string(request.Project)); err != nil {
		return result, err
	}
	intent := lifecycle.SelectionReadIntentRequest{Project: string(request.Project), ExplorerID: string(request.ExplorerId), RevisionID: request.SelectionRevision, Limit: 100}
	if request.Params.Cursor != nil {
		intent.Cursor = *request.Params.Cursor
	}
	if request.Params.Limit != nil {
		intent.Limit = *request.Params.Limit
	}
	value, err := h.application.SelectionService().ReadSelection(ctx, intent)
	if err != nil {
		return result, err
	}
	revision, err := directAuthoringJSON[loomapi.SelectionRevision](value.Header)
	if err != nil {
		return result, err
	}
	members := make([]loomapi.SelectionMember, 0, len(value.Members))
	for _, member := range value.Members {
		wire, err := directAuthoringJSON[loomapi.SelectionMember](member)
		if err != nil {
			return result, err
		}
		members = append(members, wire)
	}
	result = loomapi.SelectionPage{Revision: revision, Members: members}
	if value.NextCursor != "" {
		result.NextCursor = &value.NextCursor
	}
	return result, nil
}

func (r *Handlers) CreateExplorerSelection(ctx context.Context, request loomapi.CreateExplorerSelectionRequestObject) (loomapi.CreateExplorerSelectionResponseObject, error) {
	var value loomapi.SelectionRevision
	var err error
	if r.application == nil {
		err = explorer.ExplorerUnavailable("selection", "AUTHORING_UNAVAILABLE", "Explorer selections are not configured")
	} else {
		value, err = r.createSelection(ctx, request)
	}
	if err == nil {
		return loomapi.CreateExplorerSelection201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createExplorerSelection", err)
	return loomapi.NewCreateExplorerSelectionErrorResponse(status, failure)
}

func (r *Handlers) GetExplorerSelection(ctx context.Context, request loomapi.GetExplorerSelectionRequestObject) (loomapi.GetExplorerSelectionResponseObject, error) {
	var value loomapi.SelectionPage
	var err error
	if r.application == nil {
		err = explorer.ExplorerUnavailable("selection", "AUTHORING_UNAVAILABLE", "Explorer selections are not configured")
	} else {
		value, err = r.readSelection(ctx, request)
	}
	if err == nil {
		return loomapi.GetExplorerSelection200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getExplorerSelection", err)
	return loomapi.NewGetExplorerSelectionErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerSourceRelationship(ctx context.Context, request loomapi.ProposeExplorerSourceRelationshipRequestObject) (loomapi.ProposeExplorerSourceRelationshipResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SourceRelationshipProposalRequest) (loomapi.SourceRelationshipProposal, error) {
		var empty loomapi.SourceRelationshipProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return empty, err
		}
		if body == nil {
			return empty, explorer.MalformedRouteError("proposeExplorerSourceRelationship", nil)
		}
		input, err := directAuthoringJSON[proposal.SourceRelationshipProposalRequest](*body)
		if err != nil {
			return empty, explorer.MalformedRouteError("proposeExplorerSourceRelationship", err)
		}
		input.Project, input.ExplorerID = project, explorerID
		var encoder *previewResponseEncoder
		if input.IncludePreview {
			input.PreviewSinkFactory = func(receipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
				var err error
				encoder, err = newPreviewResponseEncoder(receipt, input.OutputID, columns, maxExplorerPreviewResponseBytes)
				if err != nil {
					return nil, err
				}
				return encoder.Visit, nil
			}
		}
		value, err := h.application.ProposeSourceRelationship(ctx, input)
		if err != nil {
			var authoringError *explorer.AuthoringError
			if input.IncludePreview && !errors.As(err, &authoringError) {
				return empty, previewRouteError(err)
			}
			return empty, err
		}
		result, err := directAuthoringJSON[loomapi.SourceRelationshipProposal](value)
		if err != nil {
			return empty, err
		}
		if input.IncludePreview {
			if value.NativePreview == nil || encoder == nil {
				return empty, previewRouteError(errors.New("source proposal preview sink was not configured"))
			}
			preview, err := previewResponseFromEncoder(encoder, value.NativePreview.Summary)
			if err != nil {
				return empty, err
			}
			result.Preview = &preview
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerSourceRelationship200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerSourceRelationship", err)
	return loomapi.NewProposeExplorerSourceRelationshipErrorResponse(status, failure)
}

func (r *Handlers) BrowseExplorerFrameSourceOptions(ctx context.Context, request loomapi.BrowseExplorerFrameSourceOptionsRequestObject) (loomapi.BrowseExplorerFrameSourceOptionsResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerFrameSourceOptionsJSONRequestBody) (loomapi.FrameSourceOptionsResponse, error) {
		var result loomapi.FrameSourceOptionsResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("frame-source-options", nil)
		}
		request := lifecycle.BrowseFrameSourceOptionsRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, OutputID: body.OutputId,
		}
		if body.ResourceType != nil {
			request.ResourceType = *body.ResourceType
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
		value, err := h.application.BrowseFrameSourceOptions(ctx, request)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.FrameSourceOptionsResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerFrameSourceOptions200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerFrameSourceOptions", err)
	return loomapi.NewBrowseExplorerFrameSourceOptionsErrorResponse(status, failure)
}

func (r *Handlers) BrowseExplorerSemanticInventory(ctx context.Context, request loomapi.BrowseExplorerSemanticInventoryRequestObject) (loomapi.BrowseExplorerSemanticInventoryResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.BrowseExplorerSemanticInventoryJSONRequestBody) (loomapi.SemanticInventoryBrowseResponse, error) {
		var result loomapi.SemanticInventoryBrowseResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("catalog", errors.New("request body is required"))
		}
		req := semanticbrowse.BrowseSemanticInventoryRequest{Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken, RowRoot: body.RowRoot}
		if body.SelectionMode != nil {
			req.SelectionMode = string(*body.SelectionMode)
		}
		if body.OutputId != nil {
			req.OutputID = *body.OutputId
		}
		if body.FieldPresentation != nil {
			presentation := semanticbrowse.FieldPresentationRequest{
				ExpectedDraftVersion: int64(body.FieldPresentation.ExpectedDraftVersion),
				ExpectedDraftDigest:  body.FieldPresentation.ExpectedDraftDigest,
				SourceNodeID:         body.FieldPresentation.SourceNodeId,
				OccurrenceID:         body.FieldPresentation.OccurrenceId,
				CandidateIDs:         append([]string(nil), body.FieldPresentation.CandidateIds...),
			}
			if body.FieldPresentation.StageId != nil {
				stageID := *body.FieldPresentation.StageId
				presentation.StageID = &stageID
			}
			req.FieldPresentation = &presentation
		}
		if body.FrameId != nil {
			req.FrameID = *body.FrameId
		}
		if body.SourceChoiceId != nil {
			req.SourceChoiceID = *body.SourceChoiceId
		}
		if body.ResourceType != nil {
			req.ResourceType = *body.ResourceType
		}
		if body.Query != nil {
			req.Query = *body.Query
		}
		if body.Cursor != nil {
			req.Cursor = *body.Cursor
		}
		if body.Limit != nil {
			req.Limit = *body.Limit
		}
		value, err := h.application.BrowseSemanticInventory(ctx, req)
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.SemanticInventoryBrowseResponse](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.BrowseExplorerSemanticInventory200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "browseExplorerSemanticInventory", err)
	return loomapi.NewBrowseExplorerSemanticInventoryErrorResponse(status, failure)
}

func (h *Handlers) authorizeViewBuilder(ctx context.Context, project, resource string, write bool) error {
	if strings.TrimSpace(resource) == "" {
		resource = projectid.Legacy(project)
	}
	if h == nil || h.views == nil || h.authorizeRead == nil || h.authorizer == nil {
		return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "View editing is unavailable", nil)
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
		}
		return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "You cannot read this project", err)
	}
	if write {
		if err := h.authorizer.AuthorizeWrite(ctx, principal, project, resource); err != nil {
			if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
				return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
			}
			return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "You cannot edit views in this project", err)
		}
	}
	return nil
}

func (h *Handlers) authorizedViewBuilderState(ctx context.Context, project, resource string) (viewer.ViewBuilderState, error) {
	value, err := h.views.ViewBuilderState(ctx, project)
	if err != nil {
		return value, viewBuilderFailure(err)
	}
	return h.checkViewBuilderState(ctx, project, resource, value)
}

func (h *Handlers) viewMutationAccess(project, resource string) projectconfig.MutationAccess {
	return projectconfig.MutationAccess{
		CheckState: func(ctx context.Context, value viewer.ViewBuilderState) (viewer.ViewBuilderState, error) {
			return h.checkViewBuilderState(ctx, project, resource, value)
		},
		CheckExecution: h.authorizeViewBuilderExecution,
		StorageError:   viewBuilderFailure,
	}
}

func (h *Handlers) checkViewBuilderState(ctx context.Context, project, resource string, value viewer.ViewBuilderState) (viewer.ViewBuilderState, error) {
	if strings.TrimSpace(resource) == "" {
		resource = projectid.Legacy(project)
	}

	executionIDs := make(map[string]bool)
	for _, config := range value.Configs {
		if config.ExecutionID != "" {
			executionIDs[config.ExecutionID] = true
		}
	}
	for _, catalog := range value.Catalogs {
		executionIDs[catalog.ExecutionID] = true
	}
	for id := range executionIDs {
		if err := h.authorizeViewBuilderExecution(ctx, id); err != nil {
			return viewer.ViewBuilderState{}, err
		}
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	err := h.authorizer.AuthorizeWrite(ctx, principal, project, resource)
	if err != nil && authorizerResponseStatus(err) == http.StatusServiceUnavailable {
		return viewer.ViewBuilderState{}, legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
	}
	value.CanEdit = err == nil
	return value, nil
}

func (h *Handlers) authorizeViewBuilderExecution(ctx context.Context, id string) error {
	if h.authorizeExecution == nil {
		return nil
	}
	err := h.authorizeExecution(ctx, id)
	if err == nil {
		return nil
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if ok && dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
		return legacyOperationFailure(http.StatusNotFound, "VIEW_NOT_FOUND", "Published data is unavailable in this scope", err)
	}
	status, _ := httpapi.MapServiceError(err, ctx)
	if status == http.StatusForbidden {
		return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "Published data is unavailable in this scope", err)
	}
	return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Published data authorization is temporarily unavailable", err)
}

func (h *Handlers) GetProjectViewBuilder(ctx context.Context, request loomapi.GetProjectViewBuilderRequestObject) (loomapi.GetProjectViewBuilderResponseObject, error) {
	fail := func(err error) (loomapi.GetProjectViewBuilderResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewGetProjectViewBuilderErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, false); err != nil {
		return fail(err)
	}
	value, err := h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.GetProjectViewBuilder200JSONResponse(result), nil
}

func (h *Handlers) AdoptProjectViewBuilder(ctx context.Context, request loomapi.AdoptProjectViewBuilderRequestObject) (loomapi.AdoptProjectViewBuilderResponseObject, error) {
	fail := func(err error) (loomapi.AdoptProjectViewBuilderResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewAdoptProjectViewBuilderErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.views.AdoptCatalogs(ctx, project, h.viewMutationAccess(project, resource))
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.AdoptProjectViewBuilder200JSONResponse(result), nil
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
