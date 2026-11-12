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
	// The bounded result contract travels on the request as the declared
	// column contract the PostgreSQL row adapter enforces against the
	// server's field descriptions (count, order, name) before the first row
	// is visited. Both dialects' lineage terminals publish exactly
	// {found, contributors, hasMore} in this order; the AQL adapter ignores
	// the contract, so carrying it is dialect-neutral. A duplicated name
	// would make the adapter's positional check meaningless, so the
	// contract must also be pairwise distinct.
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

// rowLineageContributors reads the published contributor page from one
// result row. Both dialects hand back a JSON array of contributor objects,
// but through different driver shapes: the AQL adapter delivers a native
// []any, while the PostgreSQL adapter decodes the jsonb column to
// json.RawMessage (adapter decodeColumn), so the array arrives as raw JSON
// bytes. A published contributor page is always a JSON array — the terminal
// COALESCEs the aggregate to an empty array — so empty bytes, malformed
// JSON, the JSON null scalar, and any non-array value are contract
// violations refused fail-closed with no value, never a silent empty page.
// SQL NULL (adapter decodeColumn nil) is likewise refused: a published page
// is never SQL NULL. The decoded array re-enters the same per-contributor
// validation as the native arm.
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
