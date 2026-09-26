package execution

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type CategoryScanRequest struct {
	Output        string
	Column        string
	StageID       string
	ColumnID      string
	ValueColumnID string
	MaxValues     int
}

type CategoryValue struct {
	Present bool
	Value   any
}

type CategoryScanResult struct {
	Values   []CategoryValue
	Complete bool
	Overflow bool
	Proof    compiler.CategoryScanProof
}

func (e *Engine) ScanCategories(ctx context.Context, resolved Resolved, request CategoryScanRequest) (CategoryScanResult, error) {
	if strings.TrimSpace(request.Output) == "" {
		return CategoryScanResult{}, fmt.Errorf("category scan output is required")
	}
	for _, output := range resolved.Compiled.Outputs {
		if output.Name != request.Output {
			continue
		}
		var compiled compiler.CompiledCategoryScanQuery
		var err error
		if request.StageID != "" {
			compiled, err = compiler.CompileCategoryScanStageWithPolicy(output, request.StageID, request.ColumnID, request.ValueColumnID, request.MaxValues, ir.DefaultPhysicalOptimizationPolicy())
		} else {
			compiled, err = compiler.CompileCategoryScanOutputWithPolicy(output, request.Column, request.MaxValues, ir.DefaultPhysicalOptimizationPolicy())
		}
		if err != nil {
			return CategoryScanResult{}, err
		}
		return e.ScanCategoriesCompiled(ctx, compiled)
	}
	return CategoryScanResult{}, fmt.Errorf("category scan output %q was not found", request.Output)
}

func (e *Engine) ScanCategoriesCompiled(ctx context.Context, compiled compiler.CompiledCategoryScanQuery) (CategoryScanResult, error) {
	if e == nil || e.queryRows == nil {
		return CategoryScanResult{}, fmt.Errorf("category scan query executor is required")
	}
	if compiled.PresentColumn == "" || compiled.ValueColumn == "" || compiled.Proof.MaxValues < 1 {
		return CategoryScanResult{}, fmt.Errorf("category scan query is missing typed result metadata")
	}
	if err := ctx.Err(); err != nil {
		return CategoryScanResult{}, err
	}
	values := make([]CategoryValue, 0, compiled.Proof.MaxValues)
	overflow := false
	err := e.queryRows(ctx, compiled.Query, e.batchSize, compiled.BindVars, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		present, ok := row[compiled.PresentColumn].(bool)
		if !ok {
			return fmt.Errorf("category scan row has invalid presence marker")
		}
		value, exists := row[compiled.ValueColumn]
		if !exists {
			return fmt.Errorf("category scan row is missing value")
		}
		if len(values) == compiled.Proof.MaxValues {
			overflow = true
			return nil
		}
		values = append(values, CategoryValue{Present: present, Value: value})
		return nil
	})
	if err != nil {
		return CategoryScanResult{}, fmt.Errorf("execute category scan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return CategoryScanResult{}, err
	}
	return CategoryScanResult{Values: values, Complete: !overflow, Overflow: overflow, Proof: compiled.Proof}, nil
}
