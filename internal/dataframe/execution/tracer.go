package execution

import (
	"context"

	celltrace "github.com/calypr/loom/internal/dataframe/compiler/celltrace"

	"errors"
	"fmt"

	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func (e *Engine) CellTrace(ctx context.Context, resolved Resolved, request CellTraceRequest) (CellTraceResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return CellTraceResult{}, fmt.Errorf("cell trace output is required")
	}
	compiled, err := compileCellTraceOutput(resolved, request)
	if err != nil {
		return CellTraceResult{}, err
	}
	return e.cellTraceCompiled(ctx, compiled, request)
}

func (e *Engine) CellTraceCompiled(ctx context.Context, compiled celltrace.CompiledCellTraceQuery, request CellTraceRequest) (CellTraceResult, error) {
	return e.cellTraceCompiled(ctx, compiled, request)
}

func (e *Engine) cellTraceCompiled(ctx context.Context, compiled celltrace.CompiledCellTraceQuery, request CellTraceRequest) (CellTraceResult, error) {
	if e == nil || e.queryRows == nil {
		return CellTraceResult{}, fmt.Errorf("cell trace query executor is required")
	}
	rowID := strings.TrimSpace(request.RowID)
	column := strings.TrimSpace(request.Column)
	if rowID == "" || column == "" {
		return CellTraceResult{}, fmt.Errorf("cell trace row ID and column are required")
	}
	if compiled.ExplicitIdentityObject {
		canonical, err := canonicalCellTraceIdentityJSON(rowID)
		if err != nil {
			return CellTraceResult{}, fmt.Errorf("cell trace row ID has an invalid object identity: %w", err)
		}
		rowID = canonical
	}
	maxWitness := request.MaxWitnessRows
	if maxWitness <= 0 {
		maxWitness = defaultCellTraceWitnessRows
	}
	if compiled.ValueColumn == "" || compiled.StatusColumn == "" || compiled.ContributionsColumn == "" || (compiled.IdentityPartsColumn == "" && compiled.ExplicitIdentityColumn == "") ||
		(compiled.ExplicitIdentityObject && compiled.ExplicitIdentityColumn == "") {
		return CellTraceResult{}, fmt.Errorf("cell trace query is missing typed result columns")
	}

	var result CellTraceResult
	witnessRows := int64(0)
	err := e.queryRows(ctx, compiled.Query, e.batchSize, compiled.BindVars, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		witnessRows++
		if witnessRows > maxWitness {
			return errCellTraceWitnessLimit
		}
		identity, identityErr := cellTraceRowIdentity(compiled, row)
		if identityErr != nil {
			return identityErr
		}
		if identity != rowID {
			return nil
		}
		parsed, parseErr := parseCellTraceRow(compiled, request, row)
		if parseErr != nil {
			return parseErr
		}
		result = parsed
		return errCellTraceFound
	})
	if errors.Is(err, errCellTraceFound) {
		return result, nil
	}
	if errors.Is(err, errCellTraceWitnessLimit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CellTraceResult{RowID: rowID, Column: column, Status: CellTraceIncomplete, OmissionCode: "TRACE_WITNESS_INCOMPLETE", Complete: false}, nil
	}
	if err != nil {
		if status, omission, ok := cellTraceFailureStatus(err); ok {
			return CellTraceResult{RowID: rowID, Column: column, Status: status, OmissionCode: omission, Contributions: []CellTraceContribution{}, Complete: true}, nil
		}
		return CellTraceResult{}, fmt.Errorf("execute cell trace: %w", err)
	}
	return CellTraceResult{}, dataframeerrors.Wrap(ErrCellTraceRowNotFound, dataframeerrors.CodeInvalidRequest, fmt.Sprintf("published row %q was not found in output %q", rowID, request.Output), dataframeerrors.WithDetails(map[string]any{"rowId": rowID, "output": request.Output}))
}
