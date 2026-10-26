package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type TableShapeExclusionStatus string

const (
	TableShapeExclusionComplete          TableShapeExclusionStatus = "COMPLETE"
	TableShapeExclusionIncomplete        TableShapeExclusionStatus = "INCOMPLETE"
	TableShapeExclusionUnsupported       TableShapeExclusionStatus = "UNSUPPORTED"
	TableShapeExclusionNoExclusionPolicy TableShapeExclusionStatus = "NO_EXCLUSION_POLICY"
)

type TableShapeExclusionRequest struct {
	Output string
	Offset int
	Limit  int
}

type TableShapeSourceIdentity struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
}

type TableShapeExclusion struct {
	SourceIdentity *TableShapeSourceIdentity `json:"sourceIdentity,omitempty"`
	Category       CategoryValue             `json:"category"`
	CategoryType   string                    `json:"categoryType"`
	OutputRowID    string                    `json:"outputRowId"`
	Reason         string                    `json:"reason"`
	OmissionCode   string                    `json:"omissionCode,omitempty"`
}

type TableShapeExclusionResult struct {
	Status     TableShapeExclusionStatus
	Exclusions []TableShapeExclusion
	Complete   bool
	HasMore    bool
	NextOffset int
}

func (e *Engine) TableShapeExclusions(ctx context.Context, resolved Resolved, request TableShapeExclusionRequest) (TableShapeExclusionResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion output is required")
	}
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != request.Output {
			continue
		}
		compiled, err := compiler.CompileTableShapeExclusionsWithPolicy(output, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			var refusal *compiler.TableShapeExclusionRefusal
			if errors.As(err, &refusal) {
				switch refusal.Code {
				case compiler.TableShapeExclusionUnsupported:
					return TableShapeExclusionResult{Status: TableShapeExclusionUnsupported, Exclusions: []TableShapeExclusion{}}, nil
				case compiler.TableShapeExclusionNoPolicy:
					return TableShapeExclusionResult{Status: TableShapeExclusionNoExclusionPolicy, Exclusions: []TableShapeExclusion{}}, nil
				}
			}
			return TableShapeExclusionResult{}, err
		}
		return e.TableShapeExclusionsCompiled(ctx, compiled)
	}
	return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion output %q was not found", request.Output)
}

// TableShapeExclusionsCompiled is the narrow execution seam for callers and
// tests holding the compiler-produced canonical diagnostic query.
func (e *Engine) TableShapeExclusionsCompiled(ctx context.Context, compiled compiler.CompiledTableShapeExclusionQuery) (TableShapeExclusionResult, error) {
	if e == nil || e.queryRows == nil {
		return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion query executor is required")
	}
	if compiled.Query == "" || compiled.Limit <= 0 || compiled.Offset < 0 || compiled.ResourceTypeField == "" || compiled.ResourceIDField == "" || compiled.CategoryPresentField == "" || compiled.CategoryValueField == "" || compiled.CategoryTypeField == "" || compiled.OutputRowIDField == "" || compiled.ReasonField == "" || compiled.IdentityStatusField == "" || compiled.OmissionField == "" {
		return TableShapeExclusionResult{}, fmt.Errorf("table-shape exclusion query is missing typed result metadata")
	}
	rows := make([]TableShapeExclusion, 0, compiled.Limit+1)
	tooMany := false
	err := e.queryRows(ctx, compiled.Query, e.batchSize, compiled.BindVars, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(rows) == compiled.Limit+1 {
			tooMany = true
			return errTableShapeExclusionOverflow
		}
		parsed, parseErr := parseTableShapeExclusion(compiled, row)
		if parseErr != nil {
			return parseErr
		}
		rows = append(rows, parsed)
		return nil
	})
	if errors.Is(err, errTableShapeExclusionOverflow) {
		err = nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return TableShapeExclusionResult{Status: TableShapeExclusionIncomplete, Exclusions: []TableShapeExclusion{}, Complete: false, NextOffset: compiled.Offset}, nil
		}
		return TableShapeExclusionResult{}, fmt.Errorf("execute table-shape exclusions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return TableShapeExclusionResult{Status: TableShapeExclusionIncomplete, Exclusions: []TableShapeExclusion{}, Complete: false, NextOffset: compiled.Offset}, nil
	}
	hasMore := tooMany || len(rows) > compiled.Limit
	if hasMore {
		rows = rows[:compiled.Limit]
	}
	complete := !hasMore
	for _, exclusion := range rows {
		if exclusion.OmissionCode != "" {
			complete = false
		}
	}
	status := TableShapeExclusionComplete
	if !complete {
		status = TableShapeExclusionIncomplete
	}
	nextOffset := compiled.Offset
	if hasMore {
		nextOffset += len(rows)
	}
	return TableShapeExclusionResult{
		Status: status, Exclusions: rows, Complete: complete, HasMore: hasMore, NextOffset: nextOffset,
	}, nil
}

var errTableShapeExclusionOverflow = errors.New("table-shape exclusion page overflow")

func parseTableShapeExclusion(compiled compiler.CompiledTableShapeExclusionQuery, row map[string]any) (TableShapeExclusion, error) {
	categoryPresent, ok := row[compiled.CategoryPresentField].(bool)
	if !ok {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category presence")
	}
	categoryValue, exists := row[compiled.CategoryValueField]
	if !exists {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion is missing category value")
	}
	categoryType, ok := row[compiled.CategoryTypeField].(string)
	if !ok || categoryType == "" {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid category type")
	}
	rowID, ok := row[compiled.OutputRowIDField].(string)
	if !ok || rowID == "" {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid output row identity")
	}
	reason, ok := row[compiled.ReasonField].(string)
	if !ok || reason != ir.PhysicalTableShapeExclusionReasonUnlistedCategory {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid reason")
	}
	identityStatus, ok := row[compiled.IdentityStatusField].(string)
	if !ok {
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has invalid source identity status")
	}
	result := TableShapeExclusion{
		Category:     CategoryValue{Present: categoryPresent, Value: categoryValue},
		CategoryType: categoryType, OutputRowID: rowID, Reason: reason,
	}
	switch identityStatus {
	case ir.PhysicalTableShapeExclusionIdentityExact:
		resourceType, typeOK := row[compiled.ResourceTypeField].(string)
		resourceID, idOK := row[compiled.ResourceIDField].(string)
		if !typeOK || resourceType == "" || !idOK || resourceID == "" {
			return TableShapeExclusion{}, fmt.Errorf("exact table-shape source identity is incomplete")
		}
		result.SourceIdentity = &TableShapeSourceIdentity{ResourceType: resourceType, ResourceID: resourceID}
	case ir.PhysicalTableShapeExclusionIdentityUnavailable:
		omission, omissionOK := row[compiled.OmissionField].(string)
		if !omissionOK || omission != ir.PhysicalTableShapeExclusionSourceIdentityUnavailable {
			return TableShapeExclusion{}, fmt.Errorf("table-shape source identity omission is invalid")
		}
		result.OmissionCode = omission
	default:
		return TableShapeExclusion{}, fmt.Errorf("table-shape exclusion has unknown source identity status %q", identityStatus)
	}
	return result, nil
}
