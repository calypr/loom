package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
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
			compiled, err = compiler.CompileCategoryScanStage(e.compileTarget, output, request.StageID, request.ColumnID, request.ValueColumnID, request.MaxValues, ir.DefaultPhysicalOptimizationPolicy())
		} else {
			compiled, err = compiler.CompileCategoryScanOutput(e.compileTarget, output, request.Column, request.MaxValues, ir.DefaultPhysicalOptimizationPolicy())
		}
		if err != nil {
			return CategoryScanResult{}, err
		}
		return e.ScanCategoriesCompiled(ctx, compiled)
	}
	return CategoryScanResult{}, fmt.Errorf("category scan output %q was not found", request.Output)
}

func (e *Engine) previewRowsPort() SourceRows {
	if e.previewSourceRows != nil {
		return e.previewSourceRows
	}
	return e.sourceRows
}

func (e *Engine) ScanCategoriesCompiled(ctx context.Context, compiled compiler.CompiledCategoryScanQuery) (CategoryScanResult, error) {
	if e == nil || e.sourceRows == nil {
		return CategoryScanResult{}, fmt.Errorf("category scan query executor is required")
	}
	if compiled.PresentColumn == "" || compiled.ValueColumn == "" || compiled.Proof.MaxValues < 1 {
		return CategoryScanResult{}, fmt.Errorf("category scan query is missing typed result metadata")
	}
	if err := ctx.Err(); err != nil {
		return CategoryScanResult{}, err
	}
	switch compiled.Command.Backend() {
	case command.CompiledBackendPostgreSQL:
		return e.scanCategoriesPostgreSQL(ctx, compiled)
	case command.CompiledBackendAQL:
	default:
		return CategoryScanResult{}, fmt.Errorf("%w: category scan command backend %q is not registered",
			command.ErrCompiledCommandBackendNotRegistered, compiled.Command.Backend())
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
	if compiled.OverflowWitness != nil {
		witnessCount := 0
		missing := false
		err := e.previewRowsPort()(ctx, RowsRequest{Command: compiled.OverflowWitness.Command, BatchSize: e.batchSize}, func(row map[string]any) error {
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
		if missing && !compiled.Proof.PresenceTracked {
			return CategoryScanResult{ConclusiveMissing: true, Proof: compiled.Proof}, nil
		}
	}
	return e.collectCategoryResultPage(ctx, compiled, nil, identityCategoryValue)
}

func (e *Engine) collectCategoryResultPage(
	ctx context.Context,
	compiled compiler.CompiledCategoryScanQuery,
	declaredColumns []string,
	decodeValue func(any) (any, error),
) (CategoryScanResult, error) {
	values := make([]CategoryValue, 0, compiled.Proof.MaxValues)
	overflow := false
	err := e.previewRowsPort()(ctx, RowsRequest{
		Command:         compiled.Command,
		DeclaredColumns: declaredColumns,
		BatchSize:       e.batchSize,
	}, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		present, ok := row[compiled.PresentColumn].(bool)
		if !ok {
			return fmt.Errorf("category scan row has invalid presence marker")
		}
		raw, exists := row[compiled.ValueColumn]
		if !exists {
			return fmt.Errorf("category scan row is missing value")
		}
		value, err := decodeValue(raw)
		if err != nil {
			return err
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

func identityCategoryValue(raw any) (any, error) {
	return raw, nil
}

func (e *Engine) scanCategoriesPostgreSQL(ctx context.Context, compiled compiler.CompiledCategoryScanQuery) (CategoryScanResult, error) {
	if compiled.OverflowWitness != nil {
		return CategoryScanResult{}, fmt.Errorf("category scan: a PostgreSQL command must not carry an overflow witness; the bounded maxValues+1 page owns truncation detection")
	}
	return e.collectCategoryResultPage(ctx, compiled, []string{compiled.PresentColumn, compiled.ValueColumn}, decodePostgreSQLCategoryValue)
}

func decodePostgreSQLCategoryValue(raw any) (any, error) {
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		if len(typed) == 0 || !json.Valid(typed) {
			return nil, fmt.Errorf("category scan row has invalid JSONB value")
		}
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			return nil, fmt.Errorf("category scan row has invalid JSONB value: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("category scan PostgreSQL row carries %T in the value column; the PostgreSQL adapter delivers JSON values only as raw JSON bytes", raw)
	}
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
