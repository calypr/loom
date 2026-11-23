package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

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
