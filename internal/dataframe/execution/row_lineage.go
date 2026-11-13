package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

var ErrRowLineageRowNotFound = errors.New("preview row was not found in the receipt output")

type RowLineageRequest struct {
	Output string
	RowID  string
	Offset int
	Limit  int
}

type RowLineageContributor struct {
	ResourceType  string `json:"resourceType"`
	ResourceID    string `json:"resourceId"`
	OccurrenceKey string `json:"occurrenceKey"`
}

type RowLineageResult struct {
	Contributors []RowLineageContributor
	HasMore      bool
	NextOffset   int
}

func (e *Engine) RowLineage(ctx context.Context, resolved Resolved, request RowLineageRequest) (RowLineageResult, error) {
	if strings.TrimSpace(request.Output) == "" || strings.TrimSpace(request.RowID) == "" {
		return RowLineageResult{}, fmt.Errorf("row lineage output and row ID are required")
	}
	if request.Offset < 0 || request.Limit < 0 || request.Limit > compiler.MaxRowLineageContributors {
		return RowLineageResult{}, fmt.Errorf("row lineage page is outside the supported range")
	}
	compiled, err := e.compileRowLineageOutput(resolved, request)
	if err != nil {
		return RowLineageResult{}, err
	}
	return e.rowLineageCompiled(ctx, compiled)
}

func (e *Engine) rowLineageCompiled(ctx context.Context, compiled compiler.CompiledRowLineageQuery) (RowLineageResult, error) {
	if e == nil || e.sourceRows == nil {
		return RowLineageResult{}, fmt.Errorf("row lineage query executor is required")
	}
	if compiled.FoundColumn == "" || compiled.ContributorsColumn == "" || compiled.HasMoreColumn == "" || compiled.Limit < 1 || compiled.Limit > compiler.MaxRowLineageContributors {
		return RowLineageResult{}, fmt.Errorf("row lineage query is missing its bounded result contract")
	}
	declaredColumns := []string{compiled.FoundColumn, compiled.ContributorsColumn, compiled.HasMoreColumn}
	for i := range declaredColumns {
		for j := i + 1; j < len(declaredColumns); j++ {
			if declaredColumns[i] == declaredColumns[j] {
				return RowLineageResult{}, fmt.Errorf("row lineage query is missing its bounded result contract")
			}
		}
	}
	var result RowLineageResult
	found := false
	if err := e.sourceRows(ctx, RowsRequest{Command: compiled.Command, DeclaredColumns: declaredColumns, BatchSize: e.batchSize}, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, ok := row[compiled.FoundColumn].(bool)
		if !ok {
			return fmt.Errorf("row lineage query returned an invalid found flag")
		}
		if !value {
			return nil
		}
		found = true
		contributors, err := rowLineageContributors(row[compiled.ContributorsColumn])
		if err != nil {
			return err
		}
		if len(contributors) > compiled.Limit {
			return fmt.Errorf("row lineage query exceeded the requested contributor page size")
		}
		result.Contributors = make([]RowLineageContributor, 0, len(contributors))
		for _, item := range contributors {
			fields, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("row lineage query returned an invalid contributor")
			}
			resourceType, resourceTypeOK := rowLineageString(fields["resourceType"])
			resourceID, resourceIDOK := rowLineageString(fields["resourceId"])
			occurrenceKey, occurrenceKeyOK := rowLineageString(fields["occurrenceKey"])
			if !resourceTypeOK || !resourceIDOK || !occurrenceKeyOK {
				return fmt.Errorf("row lineage query returned a contributor without a FHIR resource identity")
			}
			contributor := RowLineageContributor{ResourceType: resourceType, ResourceID: resourceID, OccurrenceKey: occurrenceKey}
			result.Contributors = append(result.Contributors, contributor)
		}
		hasMore, ok := row[compiled.HasMoreColumn].(bool)
		if !ok {
			return fmt.Errorf("row lineage query returned an invalid hasMore flag")
		}
		result.HasMore = hasMore
		if hasMore {
			result.NextOffset = compiled.Offset + len(result.Contributors)
		}
		return nil
	}); err != nil {
		return RowLineageResult{}, fmt.Errorf("execute row lineage query: %w", err)
	}
	if !found {
		return RowLineageResult{}, ErrRowLineageRowNotFound
	}
	if result.Contributors == nil {
		result.Contributors = []RowLineageContributor{}
	}
	return result, nil
}

func rowLineageContributors(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case json.RawMessage:
		if len(typed) == 0 || !json.Valid(typed) {
			return nil, fmt.Errorf("row lineage query returned an invalid contributor page")
		}
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			return nil, fmt.Errorf("row lineage query returned an invalid contributor page: %w", err)
		}
		items, ok := decoded.([]any)
		if !ok {
			return nil, fmt.Errorf("row lineage query returned an invalid contributor page")
		}
		return items, nil
	default:
		return nil, fmt.Errorf("row lineage query returned an invalid contributor page")
	}
}

func rowLineageString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func (e *Engine) compileRowLineageOutput(resolved Resolved, request RowLineageRequest) (compiler.CompiledRowLineageQuery, error) {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == request.Output {
			return compiler.CompileRowLineageOutput(e.compileTarget, output, request.RowID, request.Offset, request.Limit, ir.DefaultPhysicalOptimizationPolicy())
		}
	}
	return compiler.CompiledRowLineageQuery{}, fmt.Errorf("row lineage output %q was not found", request.Output)
}
