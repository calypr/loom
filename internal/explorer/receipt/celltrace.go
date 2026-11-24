package receipt

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/projectid"
)

type CellTraceRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	RowID      string
	Column     string
	Offset     int
	Limit      int
}

type CellTraceBinding struct {
	ReceiptID   string `json:"receiptId"`
	OutputID    string `json:"outputId"`
	Project     string `json:"project"`
	ExplorerID  string `json:"explorerId"`
	Generation  string `json:"generation"`
	ScopeDigest string `json:"scopeDigest"`
}

type CellTraceResult struct {
	Binding CellTraceBinding                   `json:"binding"`
	Feature CellTraceFeature                   `json:"feature"`
	Trace   dataframeexecution.CellTraceResult `json:"trace"`
}

type CellTraceFeature struct {
	OutputID           string   `json:"outputId"`
	Column             string   `json:"column"`
	AuthoredColumn     string   `json:"authoredColumn"`
	OccurrenceID       string   `json:"occurrenceId"`
	Label              string   `json:"label"`
	LogicalType        string   `json:"logicalType"`
	SourceResourceType string   `json:"sourceResourceType,omitempty"`
	SourcePath         string   `json:"sourcePath,omitempty"`
	ProjectionMode     string   `json:"projectionMode,omitempty"`
	Lossless           bool     `json:"lossless"`
	LossReasons        []string `json:"lossReasons"`
}

type CellTraceExecutor = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error)

type CellTraceReceiptReader interface {
	LookupReceipt(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error)
	ValidateExecutableReceiptRoute(*receiptmodel.CompilationReceipt, string, string) error
}

type CellTraceServiceConfig struct {
	Reader                     CellTraceReceiptReader
	ForExecution               func(context.Context, string, string) (ports.AuthorizedCapability, error)
	SelectionMembersCollection string
	Executor                   CellTraceExecutor
}

type CellTraceService struct {
	reader                     CellTraceReceiptReader
	forExecution               func(context.Context, string, string) (ports.AuthorizedCapability, error)
	selectionMembersCollection string
	executor                   CellTraceExecutor
}

func NewCellTraceService(config CellTraceServiceConfig) *CellTraceService {
	return &CellTraceService{
		reader: config.Reader, forExecution: config.ForExecution,
		selectionMembersCollection: config.SelectionMembersCollection, executor: config.Executor,
	}
}

func receiptCellTraceFeature(receipt *receiptmodel.CompilationReceipt, outputID, column string) (CellTraceFeature, bool) {
	if receipt == nil {
		return CellTraceFeature{}, false
	}
	for _, emitted := range receipt.EmittedColumns {
		if emitted.OutputID == outputID && emitted.PublicColumn == column {
			authoredColumn := emitted.PublicColumn
			if len(emitted.AuthoredColumns) > 0 {
				authoredColumn = emitted.AuthoredColumns[0]
			}
			label := strings.TrimSpace(emitted.Label)
			if label == "" {
				label = emitted.PublicColumn
			}
			occurrenceID := strings.TrimSpace(emitted.OccurrenceID)
			if occurrenceID == "" {
				occurrenceID = "base"
			}
			return CellTraceFeature{
				OutputID: emitted.OutputID, Column: emitted.PublicColumn, AuthoredColumn: authoredColumn,
				OccurrenceID: occurrenceID, Label: label, LogicalType: emitted.LogicalType,
				SourceResourceType: emitted.SourceResourceType, SourcePath: emitted.SourcePath,
				ProjectionMode: emitted.ProjectionMode, Lossless: emitted.Lossless,
				LossReasons: append([]string(nil), emitted.LossReasons...),
			}, true
		}
	}
	return CellTraceFeature{}, false
}

func (s *CellTraceService) CellTrace(ctx context.Context, request CellTraceRequest) (CellTraceResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RowID) == "" || strings.TrimSpace(request.Column) == "" {
		return CellTraceResult{}, support.Malformed("cellTrace", "receiptId, outputId, rowId, and column are required", nil)
	}
	if request.Offset < 0 {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "INVALID_CELL_TRACE_OFFSET", "offset cannot be negative", nil)
	}
	if request.Limit < 0 || request.Limit > facade.MaxCellTraceContributions {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "INVALID_CELL_TRACE_LIMIT", "limit must be between 1 and 100", nil)
	}
	if s == nil || s.reader == nil || s.forExecution == nil || s.executor == nil {
		return CellTraceResult{}, support.Unavailable("cellTrace", "CELL_TRACE_UNAVAILABLE", "cell explanation is not configured", nil)
	}
	prepared, err := support.PrepareReceiptOutput(ctx, s.reader, s.forExecution, support.ReceiptOutputRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: request.ReceiptID, OutputID: request.OutputID, Stage: "cellTrace",
	})
	if err != nil {
		return CellTraceResult{}, err
	}
	receipt := prepared.Receipt
	feature, ok := receiptCellTraceFeature(receipt, prepared.OutputID, request.Column)
	if !ok {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "UNKNOWN_OUTPUT_COLUMN", "column is not in the receipt output", nil)
	}

	bindings := prepared.Bindings(s.selectionMembersCollection)
	bindings.OutputNames = []string{prepared.OutputID}
	trace, err := s.executor(ctx, receipt, bindings, dataframeexecution.CellTraceRequest{
		Output: prepared.OutputID, RowID: strings.TrimSpace(request.RowID), Column: strings.TrimSpace(request.Column),
		Offset: request.Offset, Limit: request.Limit,
	})
	if err != nil {
		if errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
			return CellTraceResult{}, support.NotFound("cellTrace", "PREVIEW_ROW_NOT_FOUND", "rowId does not exist in the receipt output", err)
		}
		var typed *ports.Error
		if errors.As(err, &typed) {
			return CellTraceResult{}, typed
		}
		if userErr, ok := dataframeerrors.AsUserError(err); ok {
			return CellTraceResult{}, support.FailureDetails(ports.ClassUnprocessable, "cellTrace", userErr.Code(), dataframeerrors.PublicMessage(err), userErr.Details(), err)
		}
		return CellTraceResult{}, err
	}
	return CellTraceResult{
		Binding: CellTraceBinding{ReceiptID: receipt.ID, OutputID: prepared.OutputID, Project: projectid.Canonical(receipt.Project), ExplorerID: receipt.ExplorerID, Generation: receipt.SourceGeneration, ScopeDigest: receipt.AuthorizationScopeDigest},
		Feature: feature,
		Trace:   trace,
	}, nil
}
