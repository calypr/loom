package execution

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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
	Values            []CategoryValue
	Complete          bool
	Overflow          bool
	ConclusiveMissing bool
	Proof             compiler.CategoryScanProof
}

const (
	previewIndexPrewarmLimit   = 2
	previewIndexPrewarmTimeout = 8 * time.Second
)

var previewIndexPrewarmState struct {
	sync.Mutex
	inFlight map[string]struct{}
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
	if compiled.CategoryIndex != nil && e.preparePreviewIndex != nil {
		_ = e.preparePreviewIndex(ctx, *compiled.CategoryIndex)
		if err := ctx.Err(); err != nil {
			return CategoryScanResult{}, err
		}
	}
	if compiled.PreviewCoveringIndex != nil {
		schedulePreviewIndexPrewarm(ctx, e, *compiled.PreviewCoveringIndex)
	}
	values := make([]CategoryValue, 0, compiled.Proof.MaxValues)
	overflow := false
	queryRows := e.queryRows
	if e.previewQueryRows != nil {
		queryRows = e.previewQueryRows
	}
	if compiled.OverflowWitness != nil {
		witnessCount := 0
		missing := false
		err := queryRows(ctx, compiled.OverflowWitness.Query, e.batchSize, compiled.OverflowWitness.BindVars, func(row map[string]any) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			present, ok := row[compiled.PresentColumn].(bool)
			if !ok {
				return fmt.Errorf("category overflow witness has invalid presence marker")
			}
			missing = missing || !present
			if _, ok := row[compiled.ValueColumn]; !ok {
				return fmt.Errorf("category overflow witness is missing value")
			}
			witnessCount++
			return nil
		})
		if err != nil {
			return CategoryScanResult{}, fmt.Errorf("execute category overflow witness: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return CategoryScanResult{}, err
		}
		if witnessCount > compiled.Proof.MaxValues {
			return CategoryScanResult{Overflow: true, Proof: compiled.Proof}, nil
		}
		if missing {
			return CategoryScanResult{ConclusiveMissing: true, Proof: compiled.Proof}, nil
		}
	}
	err := queryRows(ctx, compiled.Query, e.batchSize, compiled.BindVars, func(row map[string]any) error {
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

func schedulePreviewIndexPrewarm(ctx context.Context, engine *Engine, spec compiler.PreviewCoveringIndexSpec) {
	if engine == nil || engine.preparePreviewIndex == nil || spec.Collection == "" || spec.Name == "" || len(spec.Fields) == 0 {
		return
	}
	key := fmt.Sprintf("%p:%s:%s", engine, spec.Collection, spec.Name)
	previewIndexPrewarmState.Lock()
	if previewIndexPrewarmState.inFlight == nil {
		previewIndexPrewarmState.inFlight = make(map[string]struct{})
	}
	if _, exists := previewIndexPrewarmState.inFlight[key]; exists || len(previewIndexPrewarmState.inFlight) >= previewIndexPrewarmLimit {
		previewIndexPrewarmState.Unlock()
		return
	}
	previewIndexPrewarmState.inFlight[key] = struct{}{}
	previewIndexPrewarmState.Unlock()

	prepare := engine.preparePreviewIndex
	indexSpec := compiler.PreviewCoveringIndexSpec{
		Collection:          spec.Collection,
		Name:                spec.Name,
		Fields:              append([]string(nil), spec.Fields...),
		PrepareAfterPreview: spec.PrepareAfterPreview,
	}
	go func() {
		defer func() {
			previewIndexPrewarmState.Lock()
			delete(previewIndexPrewarmState.inFlight, key)
			previewIndexPrewarmState.Unlock()
		}()
		prepareCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), previewIndexPrewarmTimeout)
		defer cancel()
		_ = prepare(prepareCtx, indexSpec)
	}()
}
