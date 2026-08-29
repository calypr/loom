package server

import (
	"context"
	"strings"

	explorerv2api "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type explorerAuthoringHandlers struct {
	getCapability     fiber.Handler
	searchSuggestions fiber.Handler
	getBuilder        fiber.Handler
	applyCommands     fiber.Handler
	compileBuilder    fiber.Handler
	reconcile         fiber.Handler
	preview           fiber.Handler
	publish           fiber.Handler
}

// newExplorerAuthoringHandlers is a transport adapter. All Builder, receipt,
// preview, and publication policy is implemented by lifecycle.Service.
func newExplorerAuthoringHandlers(authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, capabilities ExplorerV2LifecycleConfig) *explorerAuthoringHandlers {
	handlers := &explorerAuthoringHandlers{}
	if authorizer == nil || authorizeRead == nil || explorers == nil {
		return handlers
	}
	application := newExplorerLifecycleApplication(explorers, capabilities)
	handlers.getCapability = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		return c.JSON(explorerv2api.AuthoringCapability{ApiVersion: explorerv2api.LoomCalyprOrgexplorerAuthoringv2, Kind: explorerv2api.ExplorerAuthoringCapabilities, Operations: []explorerv2api.AuthoringCapabilityOperations{explorerv2api.Builder, explorerv2api.Suggestions, explorerv2api.Preview, explorerv2api.Publish, explorerv2api.Commands, explorerv2api.Reconcile}, PreviewLimits: []int{10, 25, 50, 100}, Features: explorerv2api.AuthoringFeatures{EmissionFilters: true, EmissionCharts: true}})
	}
	handlers.searchSuggestions = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		var request explorerv2api.CandidateSearchRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("suggestions", err))
		}
		query := ""
		if request.Query != nil {
			query = *request.Query
		}
		value, err := application.Suggestions(c.Context(), lifecycle.SuggestionsRequest{Project: explorerProjectParam(c), ExplorerID: strings.TrimSpace(c.Params("explorerId")), SnapshotToken: request.SnapshotToken, NodeID: request.NodeId, Query: query})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(fiber.Map{"apiVersion": authoringv2.APIVersion, "kind": "ExplorerBuilderCandidateSuggestions", "snapshotToken": value.SnapshotToken, "nodeId": value.NodeID, "candidates": value.Candidates, "diagnostics": []any{}})
	}
	handlers.getBuilder = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		value, err := application.Builder(c.Context(), lifecycle.BuilderRequest{Project: explorerProjectParam(c), ExplorerID: strings.TrimSpace(c.Params("explorerId"))})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(value)
	}
	handlers.applyCommands = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request authoringv2.ApplyCommandsRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("commands", err))
		}
		value, err := application.ApplyCommands(c.Context(), explorerProjectParam(c), strings.TrimSpace(c.Params("explorerId")), request, subjectFromFiber(c))
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(value)
	}
	handlers.compileBuilder = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request explorerv2api.CompileRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("intent", err))
		}
		receipt, err := application.Compile(c.Context(), lifecycle.CompileRequest{Project: explorerProjectParam(c), ExplorerID: strings.TrimSpace(c.Params("explorerId")), Workspace: request.Workspace, SnapshotToken: request.SnapshotToken, RequestID: requestIDFromFiber(c)})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(v2ReceiptResponse(receipt, request.Workspace))
	}
	handlers.reconcile = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request explorerv2api.ReconcileRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("reconcile", err))
		}
		receipt, err := application.Reconcile(c.Context(), lifecycle.ReconcileRequest{Project: explorerProjectParam(c), ExplorerID: strings.TrimSpace(c.Params("explorerId")), SnapshotToken: request.SnapshotToken, DraftVersion: request.DraftVersion, DraftDigest: request.DraftDigest})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		return c.JSON(v2ReceiptResponse(receipt, authoringv2.Workspace{}))
	}
	handlers.preview = func(c fiber.Ctx) error {
		if err := authoringRead(c, authorizeRead); err != nil {
			return err
		}
		previewCtx, cancel := context.WithTimeout(c.Context(), explorerPreviewTimeout)
		defer cancel()
		var request explorerv2api.PreviewRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("preview", err))
		}
		var finish func() ([]byte, error)
		value, err := application.Preview(previewCtx, lifecycle.PreviewRequest{Project: explorerProjectParam(c), ExplorerID: strings.TrimSpace(c.Params("explorerId")), ReceiptID: request.ReceiptId, OutputID: request.OutputId, Limit: previewLimit(request.Limit), SinkFactory: func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
			encoder, encoderErr := newPreviewResponseEncoder(receipt, request.OutputId, columns, maxExplorerPreviewResponseBytes)
			if encoderErr != nil {
				return nil, encoderErr
			}
			finish = encoder.Finish
			return encoder.Visit, nil
		}})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		var encoded []byte
		if value.Rows != nil {
			encoded, err = encodeExplorerPreviewResponse(value.Receipt, request.OutputId, value.Columns, value.Rows[request.OutputId], maxExplorerPreviewResponseBytes)
		} else if finish != nil {
			encoded, err = finish()
		}
		if err != nil {
			return previewRouteFailure(c, err)
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(encoded)
	}
	handlers.publish = func(c fiber.Ctx) error {
		if err := authoringWrite(c, authorizer); err != nil {
			return err
		}
		var request explorerv2api.PublishRequest
		if err := decodeAuthoringStrict(c.Body(), &request); err != nil {
			return authoringHTTPError(c, malformedRouteError("publish", err))
		}
		value, err := application.Publish(c.Context(), lifecycle.PublishRequest{Project: explorerProjectParam(c), ExplorerID: strings.TrimSpace(c.Params("explorerId")), ReceiptID: request.ReceiptId, Actor: subjectFromFiber(c)})
		if err != nil {
			return authoringHTTPError(c, err)
		}
		outputs := make([]explorerv2api.PublicationOutput, 0, len(value.Revision.Materializations))
		for _, materialization := range value.Revision.Materializations {
			outputs = append(outputs, explorerv2api.PublicationOutput{OutputId: firstNonEmpty(materialization.OutputID, materialization.Output), State: "READY", MaterializationId: materialization.MaterializationID})
		}
		return c.JSON(explorerv2api.PublishResponse{ApiVersion: explorerv2api.LoomCalyprOrgexplorerAuthoringv2, Kind: explorerv2api.ExplorerBuilderPublication, ReceiptId: value.Receipt.ID, RevisionId: value.Revision.ID, State: string(value.Revision.Status), Outputs: outputs, Diagnostics: []explorerv2api.Diagnostic{}})
	}
	return handlers
}

func previewLimit(value *int) int {
	if value == nil {
		return dataframeexecution.DefaultPreviewLimit
	}
	if *value == 0 {
		return -1
	}
	return *value
}

// v2ReceiptResponse is transport conversion only; receipt construction and
// validation belong to lifecycle.Service.
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
	return explorerv2api.CompileResponse{ApiVersion: explorerv2api.LoomCalyprOrgexplorerAuthoringv2, Kind: explorerv2api.ExplorerBuilderReceipt, ReceiptId: receipt.ID, SnapshotToken: receipt.SnapshotToken, Generation: receipt.SourceGeneration, IntentDigest: receipt.IntentDigest, CompilerVersion: explorer.CurrentCompilerContractVersion + "+" + explorercompilation.TranslationVersion, Builder: workspace, Outputs: outputs, Diagnostics: []explorerv2api.Diagnostic{}}
}
