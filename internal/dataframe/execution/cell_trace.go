package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const defaultCellTraceWitnessRows int64 = 1_000_000

// CellTraceStatus describes what the canonical output plan established for
// one published cell. Incomplete is reserved for a bounded trace that could
// not reach the requested row; it must never be presented as absence.
type CellTraceStatus string

const (
	CellTraceValue        CellTraceStatus = "VALUE"
	CellTraceNoMatch      CellTraceStatus = "NO_MATCH"
	CellTraceRecordedNull CellTraceStatus = "RECORDED_NULL"
	CellTraceAmbiguous    CellTraceStatus = "AMBIGUOUS"
	CellTraceInvalidType  CellTraceStatus = "INVALID_TYPE"
	CellTraceInvalidUnit  CellTraceStatus = "INCOMPATIBLE_UNIT"
	CellTraceIncomplete   CellTraceStatus = "INCOMPLETE"
)

type CellTraceRequest struct {
	Output         string
	RowID          string
	Column         string
	Offset         int
	Limit          int
	MaxWitnessRows int64
}

type CellTraceContribution struct {
	ResourceType string `json:"resourceType,omitempty"`
	ResourceID   string `json:"resourceId,omitempty"`
	Value        any    `json:"value"`
}

type CellTraceResult struct {
	RowID         string
	Column        string
	Value         any
	Status        CellTraceStatus
	Contributions []CellTraceContribution
	HasMore       bool
	NextOffset    int
	OmissionCode  string
	Complete      bool
}

var errCellTraceFound = errors.New("cell trace row found")
var errCellTraceWitnessLimit = errors.New("cell trace witness limit exceeded")

// CellTrace executes a diagnostic terminal compiled from the same resolved
// output as publication. It locates a published row by its stable opaque ID;
// callers never submit FHIR paths or expressions for interpretation here.
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

// CellTraceCompiled is the narrow execution seam used by focused tests and
// specialized adapters that already hold the compiler result.
func (e *Engine) CellTraceCompiled(ctx context.Context, compiled compiler.CompiledCellTraceQuery, request CellTraceRequest) (CellTraceResult, error) {
	return e.cellTraceCompiled(ctx, compiled, request)
}

func (e *Engine) cellTraceCompiled(ctx context.Context, compiled compiler.CompiledCellTraceQuery, request CellTraceRequest) (CellTraceResult, error) {
	if e == nil || e.queryRows == nil {
		return CellTraceResult{}, fmt.Errorf("cell trace query executor is required")
	}
	rowID := strings.TrimSpace(request.RowID)
	column := strings.TrimSpace(request.Column)
	if rowID == "" || column == "" {
		return CellTraceResult{}, fmt.Errorf("cell trace row ID and column are required")
	}
	maxWitness := request.MaxWitnessRows
	if maxWitness <= 0 {
		maxWitness = defaultCellTraceWitnessRows
	}
	if compiled.ValueColumn == "" || compiled.StatusColumn == "" || compiled.ContributionsColumn == "" || (compiled.IdentityPartsColumn == "" && compiled.ExplicitIdentityColumn == "") {
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
	return CellTraceResult{}, fmt.Errorf("published row %q was not found in output %q", rowID, request.Output)
}

func compileCellTraceOutput(resolved Resolved, request CellTraceRequest) (compiler.CompiledCellTraceQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == request.Output {
			return compiler.CompileCellTraceOutputWithPolicy(output, request.Column, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		}
	}
	return compiler.CompiledCellTraceQuery{}, fmt.Errorf("cell trace output %q was not found", request.Output)
}

func cellTraceRowIdentity(query compiler.CompiledCellTraceQuery, row map[string]any) (string, error) {
	if query.ExplicitIdentityColumn != "" {
		value, ok := row[query.ExplicitIdentityColumn]
		if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "", fmt.Errorf("cell trace row has an invalid explicit identity")
		}
		return fmt.Sprint(value), nil
	}
	parts, ok := row[query.IdentityPartsColumn].([]any)
	if !ok || query.RowIdentity == nil || len(parts) == 0 || len(parts) != len(query.RowIdentity.Fields) {
		return "", fmt.Errorf("cell trace row has an invalid default identity shape")
	}
	for _, part := range parts {
		if part == nil {
			return "", fmt.Errorf("cell trace row has a null default identity part")
		}
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("encode cell trace row identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func parseCellTraceRow(query compiler.CompiledCellTraceQuery, request CellTraceRequest, row map[string]any) (CellTraceResult, error) {
	status, ok := row[query.StatusColumn].(string)
	if !ok || !validCellTraceStatus(CellTraceStatus(status)) {
		return CellTraceResult{}, fmt.Errorf("cell trace row has an invalid status")
	}
	contributions, err := parseCellTraceContributions(row[query.ContributionsColumn])
	if err != nil {
		return CellTraceResult{}, err
	}
	hasMore, _ := row[query.HasMoreColumn].(bool)
	omission, _ := row[query.OmissionColumn].(string)
	nextOffset := 0
	if hasMore {
		nextOffset = request.Offset + len(contributions)
	}
	return CellTraceResult{
		RowID: strings.TrimSpace(request.RowID), Column: strings.TrimSpace(request.Column), Value: row[query.ValueColumn],
		Status: CellTraceStatus(status), Contributions: contributions, HasMore: hasMore, NextOffset: nextOffset,
		OmissionCode: omission, Complete: true,
	}, nil
}

func parseCellTraceContributions(value any) ([]CellTraceContribution, error) {
	if value == nil {
		return []CellTraceContribution{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("cell trace row has invalid contributions")
	}
	result := make([]CellTraceContribution, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cell trace row has an invalid contribution")
		}
		resourceType, _ := object["resourceType"].(string)
		resourceID, _ := object["resourceId"].(string)
		result = append(result, CellTraceContribution{ResourceType: resourceType, ResourceID: resourceID, Value: object["value"]})
	}
	return result, nil
}

func validCellTraceStatus(status CellTraceStatus) bool {
	return status == CellTraceValue || status == CellTraceNoMatch || status == CellTraceRecordedNull || status == CellTraceAmbiguous || status == CellTraceInvalidType || status == CellTraceInvalidUnit
}

func cellTraceFailureStatus(err error) (CellTraceStatus, string, bool) {
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		return "", "", false
	}
	switch dataframeerrors.ErrorCode(userErr.Code()) {
	case dataframeerrors.CodeRelationshipCardinalityViolation, dataframeerrors.CodeTemporalTieAmbiguous:
		return CellTraceAmbiguous, userErr.Code(), true
	case dataframeerrors.CodeUnitIdentityUnknown, dataframeerrors.CodeUnitDimensionIncompatible:
		return CellTraceInvalidUnit, userErr.Code(), true
	case dataframeerrors.CodeInvalidData, dataframeerrors.CodeRecipeContractViolation:
		return CellTraceInvalidType, userErr.Code(), true
	default:
		return "", "", false
	}
}
