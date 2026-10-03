package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

func TestRowLineagePageIsBoundedAndKeepsOccurrenceIdentity(t *testing.T) {
	engine := &Engine{batchSize: 32, queryRows: func(_ context.Context, query string, batchSize int, binds map[string]any, visit func(map[string]any) error) error {
		if batchSize != 32 || query != "row-lineage-query" || binds["row_id"] != "row-7" {
			t.Fatalf("unexpected query invocation: batch=%d query=%q binds=%#v", batchSize, query, binds)
		}
		return visit(map[string]any{
			"found": true,
			"contributors": []any{
				map[string]any{"resourceType": "BodyStructure", "resourceId": "fhir-1", "occurrenceKey": "db-key-1"},
			},
			"hasMore": true,
		})
	}}
	result, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Query: "row-lineage-query", BindVars: map[string]any{"row_id": "row-7"},
		Offset: 24, Limit: 1, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contributors) != 1 || result.Contributors[0].ResourceID != "fhir-1" || result.Contributors[0].OccurrenceKey != "db-key-1" || !result.HasMore || result.NextOffset != 25 {
		t.Fatalf("row lineage page = %#v", result)
	}
}

func TestRowLineageDistinguishesMissingKeyedGroupFromEmptyKeylessGroup(t *testing.T) {
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"found": false, "contributors": []any{}, "hasMore": false})
	}}
	_, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Query: "keyed-group-query", Limit: 25, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if !errors.Is(err, ErrRowLineageRowNotFound) {
		t.Fatalf("keyed zero-match Group error = %v, want row not found", err)
	}
	engine.queryRows = func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"found": true, "contributors": []any{}, "hasMore": false})
	}
	result, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Query: "keyless-empty-group-query", Limit: 25, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Contributors == nil || len(result.Contributors) != 0 || result.HasMore {
		t.Fatalf("keyless zero-input Group page = %#v, want empty complete page", result)
	}
}

func TestRowLineageRejectsQueryResultsLargerThanRequestedPage(t *testing.T) {
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"found": true, "contributors": []any{
			map[string]any{"resourceType": "Observation", "resourceId": "one", "occurrenceKey": "one"},
			map[string]any{"resourceType": "Observation", "resourceId": "two", "occurrenceKey": "two"},
		}, "hasMore": false})
	}}
	_, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Query: "row-lineage-query", Limit: 1, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err == nil {
		t.Fatal("row lineage accepted more contributors than the requested page size")
	}
}
