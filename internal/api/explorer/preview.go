package explorerapi

import (
	"time"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/proposal"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"

	loomapi "github.com/calypr/loom/api/loomapi"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"

	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
)

const (
	maxExplorerPreviewResponseBytes = 32 << 20
)

var ErrPreviewResponseTooLarge = errors.New("PREVIEW_RESPONSE_TOO_LARGE")

func previewRouteError(err error) error {
	if errors.Is(err, ErrPreviewResponseTooLarge) {
		return &explorer.AuthoringError{Status: 413, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "RESPONSE_TOO_LARGE", Message: "preview response exceeds the maximum size"}, Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &explorer.AuthoringError{Status: 504, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_TIMEOUT", Message: "preview exceeded its execution deadline"}, Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return &explorer.AuthoringError{Status: 499, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "CLIENT_CANCELED", Message: "preview request was canceled"}, Cause: err}
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		switch userErr.Code() {
		case string(dataframeerrors.CodeClientCanceled):
			return &explorer.AuthoringError{Status: 499, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "CLIENT_CANCELED", Message: "preview request was canceled"}, Cause: err}
		case string(dataframeerrors.CodePreviewTimeout):
			return &explorer.AuthoringError{Status: 504, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_TIMEOUT", Message: "preview exceeded its execution deadline"}, Cause: err}
		case string(dataframeerrors.CodePreviewResponseTooLarge):
			return &explorer.AuthoringError{Status: 413, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_RESPONSE_TOO_LARGE", Message: dataframeerrors.PublicMessage(err)}, Cause: err}
		case string(dataframeerrors.CodePlanTooExpensive):
			return &explorer.AuthoringError{Status: 429, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err)}, Cause: err}
		case string(dataframeerrors.CodeRelationshipCardinalityViolation),
			string(dataframeerrors.CodeConstructionExpansionEmpty),
			string(dataframeerrors.CodeExplicitGroupUnassignedMember),
			string(dataframeerrors.CodeConstructionRowValueMultipleValues),
			string(dataframeerrors.CodeTablePivotCellCardinality),
			string(dataframeerrors.CodeTablePivotUnlistedCategory),
			string(dataframeerrors.CodeTemporalAnchorInvalid),
			string(dataframeerrors.CodeTemporalPrecisionUnsupported),
			string(dataframeerrors.CodeTemporalTieAmbiguous),
			string(dataframeerrors.CodeUnitIdentityUnknown),
			string(dataframeerrors.CodeUnitDimensionIncompatible):
			return &explorer.AuthoringError{Status: http.StatusUnprocessableEntity, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err)}, Cause: err}
		case string(dataframeerrors.CodeBackendUnavailable), string(dataframeerrors.CodeReceiptStoreUnavailable):
			return &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err)}, Cause: err}
		case string(dataframeerrors.CodeQueryMemoryLimitExceeded), string(dataframeerrors.CodeQueryResourceLimitExceeded), string(dataframeerrors.CodeQueryBackendOutOfMemory):
			return &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err), Details: userErr.Details()}, Cause: err}
		case string(dataframeerrors.CodeRecipeContractViolation), string(dataframeerrors.CodeDynamicSchemaDrift):
			return receiptapi.PreviewConflict(err)
		default:
			return previewInternalError(err)
		}
	}
	var applicationError *ports.Error
	if errors.As(err, &applicationError) {
		return err
	}
	if errors.As(err, new(*receiptapi.PreviewResolutionError)) {
		return receiptapi.PreviewConflict(err)
	}
	return previewInternalError(err)
}

func previewInternalError(err error) error {
	return &explorer.AuthoringError{
		Status: http.StatusInternalServerError,
		Diagnostic: explorer.AuthoringDiagnostic{
			Severity: "ERROR",
			Stage:    "preview",
			Code:     "PREVIEW_FAILED",
			Message:  "receipt preview failed while executing the compiled query; inspect the server log for the request ID",
			Details:  map[string]any{"causeType": fmt.Sprintf("%T", err)},
		},
		Cause: err,
	}
}

type previewResponseTooLargeError struct {
	Limit int
}

type v2EmissionWire = loomapi.ContractColumn

func v2EmissionColumns(columns []viewer.EmittedColumn) []v2EmissionWire {
	out := make([]v2EmissionWire, 0, len(columns))
	for _, column := range columns {
		label := column.Label
		if label == "" {
			label = column.PublicColumn
		}
		wire := v2EmissionWire{
			Column: column.PublicColumn, Label: label, LogicalType: column.LogicalType,
			Filterable: column.Filterable, Chartable: column.Chartable,
			AuthoredColumns: stringSlicePointer(column.AuthoredColumns),
			Nullable:        pointerTo(column.Nullable), Shape: pointerTo(column.Shape),
		}
		if column.SourceResourceType != "" {
			wire.SourceResourceType = &column.SourceResourceType
		}
		if column.SourcePath != "" {
			wire.SourcePath = &column.SourcePath
		}
		if column.ResultUnit != nil {
			wire.ResultUnit = &loomapi.UnitIdentity{System: column.ResultUnit.System, Code: column.ResultUnit.Code}
		}
		if column.UnitNormalization != nil {
			rules := make([]loomapi.UnitRuleReference, 0, len(column.UnitNormalization.Rules))
			for _, rule := range column.UnitNormalization.Rules {
				rules = append(rules, loomapi.UnitRuleReference{Id: rule.ID, Version: rule.Version})
			}
			wire.UnitNormalization = &loomapi.UnitNormalizationContract{Target: loomapi.UnitIdentity{System: column.UnitNormalization.Target.System, Code: column.UnitNormalization.Target.Code}, Rules: rules}
		}
		out = append(out, wire)
	}
	return out
}

func stringSlicePointer(values []string) *[]string {
	if len(values) == 0 {
		return nil
	}
	copy := append([]string(nil), values...)
	return &copy
}

func (e *previewResponseTooLargeError) Error() string {
	if e == nil || e.Limit <= 0 {
		return ErrPreviewResponseTooLarge.Error()
	}
	return fmt.Sprintf("%s: response exceeds %d bytes", ErrPreviewResponseTooLarge, e.Limit)
}

func (e *previewResponseTooLargeError) Unwrap() error { return ErrPreviewResponseTooLarge }

type previewResponseEncoder struct {
	out            cappedPreviewBuffer
	rowCount       int
	firstRow       bool
	rowSources     []json.RawMessage
	rowSourceBytes int
}

func newPreviewResponseEncoder(receipt *receiptmodel.CompilationReceipt, outputID string, columns []viewer.EmittedColumn, limit int) (*previewResponseEncoder, error) {
	if receipt == nil {
		return nil, fmt.Errorf("compilation receipt is required")
	}
	encoder := &previewResponseEncoder{firstRow: true}
	encoder.out.limit = limit
	write := func(raw []byte) error { _, err := encoder.out.Write(raw); return err }
	if err := write([]byte(`{"apiVersion":"loom.calypr.org/explorer-authoring/v2","kind":"ExplorerBuilderPreview","receiptId":`)); err != nil {
		return nil, err
	}
	receiptID, err := json.Marshal(receipt.ID)
	if err != nil {
		return nil, err
	}
	if err := write(receiptID); err != nil {
		return nil, err
	}
	if err := write([]byte(`,"outputId":`)); err != nil {
		return nil, err
	}
	outputRaw, err := json.Marshal(outputID)
	if err != nil {
		return nil, err
	}
	if err := write(outputRaw); err != nil {
		return nil, err
	}
	encodedColumns, err := json.Marshal(v2EmissionColumns(columns))
	if err != nil {
		return nil, err
	}
	if err := write([]byte(`,"columns":`)); err != nil {
		return nil, err
	}
	if err := write(encodedColumns); err != nil {
		return nil, err
	}
	if err := write([]byte(`,"rows":[`)); err != nil {
		return nil, err
	}
	return encoder, nil
}

func (e *previewResponseEncoder) Visit(row map[string]any) error {
	if e == nil {
		return fmt.Errorf("preview response encoder is nil")
	}
	if row == nil {
		row = map[string]any{}
	}
	rowSource := normalizePreviewRowSource(row["__loom_preview_source"])
	encodedSource, err := json.Marshal(rowSource)
	if err != nil {
		return err
	}
	publicRow := make(map[string]any, len(row))
	for key, value := range row {
		switch key {
		case "__loom_preview_source", "__loom_source_resource_id":
			continue
		}
		publicRow[key] = value
	}
	encoded, err := json.Marshal(publicRow)
	if err != nil {
		return err
	}
	rowSeparatorBytes := 0
	if !e.firstRow {
		rowSeparatorBytes = 1
	}
	sourceSeparatorBytes := 0
	if len(e.rowSources) > 0 {
		sourceSeparatorBytes = 1
	}
	if e.out.limit > 0 {
		projectedSize := e.out.Len() + rowSeparatorBytes + len(encoded) + len(`],"rowSources":[`) +
			e.rowSourceBytes + sourceSeparatorBytes + len(encodedSource) + len(`],"rowCount":`) +
			len(strconv.Itoa(e.rowCount+1)) + len(`,"diagnostics":[]}`)
		if projectedSize > e.out.limit {
			return &previewResponseTooLargeError{Limit: e.out.limit}
		}
	}
	e.rowSources = append(e.rowSources, encodedSource)
	e.rowSourceBytes += sourceSeparatorBytes + len(encodedSource)
	if !e.firstRow {
		if _, err := e.out.Write([]byte(",")); err != nil {
			return err
		}
	}
	if _, err := e.out.Write(encoded); err != nil {
		return err
	}
	e.firstRow = false
	e.rowCount++
	return nil
}

func (e *previewResponseEncoder) Finish() ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("preview response encoder is nil")
	}
	if _, err := e.out.Write([]byte(`],"rowSources":[`)); err != nil {
		return nil, err
	}
	for index, source := range e.rowSources {
		if index > 0 {
			if _, err := e.out.Write([]byte(",")); err != nil {
				return nil, err
			}
		}
		if _, err := e.out.Write(source); err != nil {
			return nil, err
		}
	}
	if _, err := e.out.Write([]byte(`],"rowCount":`)); err != nil {
		return nil, err
	}
	count, _ := json.Marshal(e.rowCount)
	if _, err := e.out.Write(count); err != nil {
		return nil, err
	}
	if _, err := e.out.Write([]byte(`,"diagnostics":[]}`)); err != nil {
		return nil, err
	}
	return e.out.Bytes(), nil
}

func normalizePreviewRowSource(value any) map[string]string {
	metadata, ok := value.(map[string]any)
	if !ok {
		return map[string]string{"kind": "UNAVAILABLE"}
	}
	kind, _ := metadata["kind"].(string)
	switch kind {
	case "SINGLE":
		resourceType, resourceTypeOK := metadata["resourceType"].(string)
		id, idOK := metadata["id"].(string)
		if resourceTypeOK && resourceType != "" && idOK && id != "" {
			return map[string]string{"kind": kind, "resourceType": resourceType, "id": id}
		}
	case "COMPOSITE":
		return map[string]string{"kind": kind}
	}
	return map[string]string{"kind": "UNAVAILABLE"}
}

type cappedPreviewBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedPreviewBuffer) Write(p []byte) (int, error) {
	if b.limit > 0 && b.Len()+len(p) > b.limit {
		return 0, &previewResponseTooLargeError{Limit: b.limit}
	}
	return b.Buffer.Write(p)
}

func PreserveConfiguredQueryError(err error) error {
	err = executionarango.ClassifyDataframeQueryError(err)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return previewRouteError(err)
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		switch userErr.Code() {
		case string(dataframeerrors.CodeUnauthenticated):
			return &explorer.AuthoringError{Status: http.StatusUnauthorized, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		case string(dataframeerrors.CodeForbidden), string(dataframeerrors.CodeUnauthorizedProject):
			return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		}
		return previewRouteError(err)
	}
	return err
}

type proposalPreviewEncoder struct {
	encoder *previewResponseEncoder
}

func (e *proposalPreviewEncoder) response(value proposal.ProposalPreview) (loomapi.PreviewResponse, error) {
	if e.encoder == nil {
		return loomapi.PreviewResponse{}, previewRouteError(errors.New("native preview response sink was not configured"))
	}
	return previewResponseFromEncoder(e.encoder, value.Result.Summary)
}

func proposalPreviewFailure(err error) error {
	var execution *proposal.ProposalPreviewExecutionError
	if errors.As(err, &execution) {
		return previewRouteError(execution.Cause)
	}
	return err
}

func (h *Handlers) proposalPreviewOptions(project, outputID string, limit *int, timeout time.Duration, sampled bool) (proposal.ProposalPreviewOptions, *proposalPreviewEncoder) {
	output := &proposalPreviewEncoder{}
	return proposal.ProposalPreviewOptions{
		Limit: limit, Timeout: timeout, AllowSampledAggregate: sampled,
		Authorize: func(ctx context.Context) error { return h.authorizeAuthoringRead(ctx, project) },
		Sink: func(receipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
			var err error
			output.encoder, err = newPreviewResponseEncoder(receipt, outputID, columns, maxExplorerPreviewResponseBytes)
			if err != nil {
				return nil, err
			}
			return output.encoder.Visit, nil
		},
	}, output
}
