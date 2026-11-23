package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	celltrace "github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const defaultCellTraceWitnessRows int64 = 1_000_000

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
	ResourceType   string `json:"resourceType,omitempty"`
	ResourceID     string `json:"resourceId,omitempty"`
	InputStageID   string `json:"inputStageId,omitempty"`
	InputColumnID  string `json:"inputColumnId,omitempty"`
	InputColumn    string `json:"inputColumn,omitempty"`
	OutputStageID  string `json:"outputStageId,omitempty"`
	OutputColumnID string `json:"outputColumnId,omitempty"`
	OutputColumn   string `json:"outputColumn,omitempty"`
	FinalStageID   string `json:"finalStageId,omitempty"`
	ConstructionID string `json:"constructionId,omitempty"`
	Operation      string `json:"operation,omitempty"`
	Value          any    `json:"value"`
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

var ErrCellTraceRowNotFound = errors.New("published cell trace row was not found")

func compileCellTraceOutput(resolved Resolved, request CellTraceRequest) (celltrace.CompiledCellTraceQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == request.Output {
			return celltrace.CompileCellTraceOutputWithPolicy(output, request.Column, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		}
	}
	return celltrace.CompiledCellTraceQuery{}, fmt.Errorf("cell trace output %q was not found", request.Output)
}

func cellTraceRowIdentity(query celltrace.CompiledCellTraceQuery, row map[string]any) (string, error) {
	if query.ExplicitIdentityColumn != "" {
		value, ok := row[query.ExplicitIdentityColumn]
		if !ok || value == nil {
			return "", fmt.Errorf("cell trace row has an invalid explicit identity")
		}
		if query.ExplicitIdentityObject {
			return canonicalCellTraceObjectValue(value)
		}
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
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

func canonicalCellTraceObjectValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode cell trace object identity: %w", err)
	}
	return canonicalCellTraceIdentityJSON(string(encoded))
}

func canonicalCellTraceIdentityJSON(raw string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	start, err := decoder.Token()
	if err != nil {
		return "", fmt.Errorf("decode object identity: %w", err)
	}
	if start != json.Delim('{') {
		return "", fmt.Errorf("object identity must be a JSON object")
	}
	parts := make(map[string]string, 2)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		if keyErr != nil {
			return "", fmt.Errorf("decode object identity key: %w", keyErr)
		}
		key, ok := keyToken.(string)
		if !ok || (key != "group_revision_id" && key != "group_id") {
			return "", fmt.Errorf("object identity contains an unsupported key")
		}
		if _, exists := parts[key]; exists {
			return "", fmt.Errorf("object identity repeats key %q", key)
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("decode object identity key %q: %w", key, err)
		}
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("object identity key %q is empty", key)
		}
		parts[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		if err != nil {
			return "", fmt.Errorf("decode object identity end: %w", err)
		}
		return "", fmt.Errorf("object identity has an invalid terminator")
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("object identity contains trailing JSON")
		}
		return "", fmt.Errorf("decode trailing object identity: %w", err)
	}
	if len(parts) != 2 {
		return "", fmt.Errorf("object identity must contain group revision and group keys")
	}
	canonical, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("canonicalize object identity: %w", err)
	}
	return string(canonical), nil
}

func parseCellTraceRow(query celltrace.CompiledCellTraceQuery, request CellTraceRequest, row map[string]any) (CellTraceResult, error) {
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
		inputStageID, _ := object["inputStageId"].(string)
		inputColumnID, _ := object["inputColumnId"].(string)
		inputColumn, _ := object["inputColumn"].(string)
		outputStageID, _ := object["outputStageId"].(string)
		outputColumnID, _ := object["outputColumnId"].(string)
		outputColumn, _ := object["outputColumn"].(string)
		finalStageID, _ := object["finalStageId"].(string)
		constructionID, _ := object["constructionId"].(string)
		operation, _ := object["operation"].(string)
		result = append(result, CellTraceContribution{
			ResourceType: resourceType, ResourceID: resourceID,
			InputStageID: inputStageID, InputColumnID: inputColumnID, InputColumn: inputColumn,
			OutputStageID: outputStageID, OutputColumnID: outputColumnID, OutputColumn: outputColumn, FinalStageID: finalStageID,
			ConstructionID: constructionID, Operation: operation, Value: object["value"],
		})
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
