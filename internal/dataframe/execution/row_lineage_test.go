package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

func TestRowLineagePageIsBoundedAndKeepsOccurrenceIdentity(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), batchSize: 32, sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		batchSize := request.BatchSize
		query, binds, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

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
		Command: mustAQLCommand("row-lineage-query", map[string]any{"row_id": "row-7"}),
		Offset:  24, Limit: 1, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contributors) != 1 || result.Contributors[0].ResourceID != "fhir-1" || result.Contributors[0].OccurrenceKey != "db-key-1" || !result.HasMore || result.NextOffset != 25 {
		t.Fatalf("row lineage page = %#v", result)
	}
}

func TestRowLineageDistinguishesMissingKeyedGroupFromEmptyKeylessGroup(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return visit(map[string]any{"found": false, "contributors": []any{}, "hasMore": false})
	}}
	_, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("keyed-group-query", nil), Limit: 25, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if !errors.Is(err, ErrRowLineageRowNotFound) {
		t.Fatalf("keyed zero-match Group error = %v, want row not found", err)
	}
	engine.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return visit(map[string]any{"found": true, "contributors": []any{}, "hasMore": false})
	}
	result, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("keyless-empty-group-query", nil), Limit: 25, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Contributors == nil || len(result.Contributors) != 0 || result.HasMore {
		t.Fatalf("keyless zero-input Group page = %#v, want empty complete page", result)
	}
}

func TestRowLineageRejectsQueryResultsLargerThanRequestedPage(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return visit(map[string]any{"found": true, "contributors": []any{
			map[string]any{"resourceType": "Observation", "resourceId": "one", "occurrenceKey": "one"},
			map[string]any{"resourceType": "Observation", "resourceId": "two", "occurrenceKey": "two"},
		}, "hasMore": false})
	}}
	_, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("row-lineage-query", nil), Limit: 1, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err == nil {
		t.Fatal("row lineage accepted more contributors than the requested page size")
	}
}
