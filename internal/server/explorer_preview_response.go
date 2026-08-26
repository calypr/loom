package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	explorerv2api "github.com/calypr/loom/generated/explorerv2"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

const (
	explorerPreviewTimeout          = 10 * time.Second
	maxExplorerPreviewResponseBytes = 32 << 20
)

var ErrPreviewResponseTooLarge = errors.New("PREVIEW_RESPONSE_TOO_LARGE")

type receiptPreviewResolutionError struct{ Err error }

func (e *receiptPreviewResolutionError) Error() string { return e.Err.Error() }
func (e *receiptPreviewResolutionError) Unwrap() error { return e.Err }

func previewRouteFailure(c fiber.Ctx, err error) error {
	if errors.Is(err, ErrPreviewResponseTooLarge) {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 413, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "RESPONSE_TOO_LARGE", Message: "preview response exceeds the maximum size"}, Cause: err})
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 504, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_TIMEOUT", Message: "preview exceeded its execution deadline"}, Cause: err})
	}
	if errors.Is(err, context.Canceled) {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 499, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "CLIENT_CANCELED", Message: "preview request was canceled"}, Cause: err})
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		switch userErr.Code() {
		case string(dataframeerrors.CodeClientCanceled):
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 499, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "CLIENT_CANCELED", Message: "preview request was canceled"}, Cause: err})
		case string(dataframeerrors.CodePreviewTimeout):
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 504, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_TIMEOUT", Message: "preview exceeded its execution deadline"}, Cause: err})
		case string(dataframeerrors.CodePreviewResponseTooLarge):
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 413, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_RESPONSE_TOO_LARGE", Message: dataframeerrors.PublicMessage(err)}, Cause: err})
		case string(dataframeerrors.CodePlanTooExpensive):
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 429, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err)}, Cause: err})
		case string(dataframeerrors.CodeBackendUnavailable), string(dataframeerrors.CodeReceiptStoreUnavailable):
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err)}, Cause: err})
		case string(dataframeerrors.CodeRecipeContractViolation), string(dataframeerrors.CodeDynamicSchemaDrift):
			return authoringHTTPError(c, receiptPreviewConflict(err))
		default:
			return authoringHTTPError(c, &explorer.AuthoringError{Status: 500, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_FAILED", Message: "receipt preview failed"}, Cause: err})
		}
	}
	if errors.As(err, new(*receiptPreviewResolutionError)) {
		return authoringHTTPError(c, receiptPreviewConflict(err))
	}
	return authoringHTTPError(c, &explorer.AuthoringError{Status: 500, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "preview", Code: "PREVIEW_FAILED", Message: "receipt preview failed"}, Cause: err})
}

func receiptPreviewConflict(cause error) error {
	return &explorer.AuthoringError{
		Status: http.StatusConflict,
		Diagnostic: explorer.AuthoringDiagnostic{
			Severity: "ERROR",
			Stage:    "preview",
			Code:     "RECEIPT_RECOMPILE_REQUIRED",
			Message:  "receipt deterministic lowering no longer matches the stored artifact",
		},
		Cause: cause,
	}
}

type previewResponseTooLargeError struct {
	Limit int
}

type v2EmissionWire = explorerv2api.Emission

func v2EmissionColumns(columns []explorer.EmittedColumn) []v2EmissionWire {
	out := make([]v2EmissionWire, 0, len(columns))
	for _, column := range columns {
		label := column.Label
		if label == "" {
			label = column.PublicColumn
		}
		out = append(out, v2EmissionWire{
			OutputId: column.OutputID, CandidateId: column.CandidateID,
			OccurrenceId: column.OccurrenceID, ProjectionMode: column.ProjectionMode,
			EmissionId: column.EmissionID, PublicColumn: column.PublicColumn,
			Label: label, LogicalType: column.LogicalType,
			Filterable: column.Filterable, Chartable: column.Chartable,
		})
	}
	return out
}

func (e *previewResponseTooLargeError) Error() string {
	if e == nil || e.Limit <= 0 {
		return ErrPreviewResponseTooLarge.Error()
	}
	return fmt.Sprintf("%s: response exceeds %d bytes", ErrPreviewResponseTooLarge, e.Limit)
}

func (e *previewResponseTooLargeError) Unwrap() error { return ErrPreviewResponseTooLarge }

// encodeExplorerPreviewResponse constructs the complete response before it is
// handed to Fiber. A capped writer makes overflow recoverable without ever
// sending a truncated JSON document to the client.
func encodeExplorerPreviewResponse(receipt *explorer.CompilationReceipt, outputID string, columns []explorer.EmittedColumn, rows []map[string]any, limit int) ([]byte, error) {
	if limit <= 0 {
		limit = maxExplorerPreviewResponseBytes
	}
	var out cappedPreviewBuffer
	out.limit = limit
	write := func(raw []byte) error {
		if _, err := out.Write(raw); err != nil {
			return err
		}
		return nil
	}
	writeString := func(value string) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return write(raw)
	}
	if err := write([]byte(`{"apiVersion":"loom.calypr.org/explorer-authoring/v2","kind":"ExplorerBuilderPreview","receiptId":`)); err != nil {
		return nil, err
	}
	if err := writeString(receipt.ID); err != nil {
		return nil, err
	}
	if err := write([]byte(`,"outputId":`)); err != nil {
		return nil, err
	}
	if err := writeString(outputID); err != nil {
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
	for index, row := range rows {
		if index > 0 {
			if err := write([]byte(",")); err != nil {
				return nil, err
			}
		}
		encodedRow, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		if err := write(encodedRow); err != nil {
			return nil, err
		}
	}
	if err := write([]byte(`],"rowCount":`)); err != nil {
		return nil, err
	}
	count, err := json.Marshal(len(rows))
	if err != nil {
		return nil, err
	}
	if err := write(count); err != nil {
		return nil, err
	}
	if err := write([]byte(`,"diagnostics":[]}`)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type previewResponseEncoder struct {
	out      cappedPreviewBuffer
	rowCount int
	firstRow bool
}

func newPreviewResponseEncoder(receipt *explorer.CompilationReceipt, outputID string, columns []explorer.EmittedColumn, limit int) (*previewResponseEncoder, error) {
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
	if !e.firstRow {
		if _, err := e.out.Write([]byte(",")); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
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
