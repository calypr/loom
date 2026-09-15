package published

import (
	"context"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type pageQueryer struct{ queries []string }

func (q *pageQueryer) QueryRowsArgs(_ context.Context, query string, _ []string, _ ...any) ([]map[string]any, error) {
	q.queries = append(q.queries, query)
	return []map[string]any{{"__loom_row_id": "2", "__loom_total": int64(2)}}, nil
}

func (q *pageQueryer) QueryRowsArgsVisit(context.Context, string, []string, func(map[string]any) error, ...any) error {
	return nil
}

func TestPageCursorAddsWhereForUnrestrictedTable(t *testing.T) {
	queryer := &pageQueryer{}
	materialization := Materialization{
		PhysicalTable: "published_patient",
		Columns:       []Column{{Name: "name", ClickHouse: "String"}},
	}
	request := PageRequest{First: 1, Unrestricted: true}
	binding, err := cursorFingerprint(materialization, request)
	if err != nil {
		t.Fatal(err)
	}
	request.After = encodeBoundCursor("1", nil, binding)
	_, err = (&Reader{ClickHouse: queryer}).Page(context.Background(), materialization, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(queryer.queries) != 1 {
		t.Fatalf("queries = %d, want 1", len(queryer.queries))
	}
	query := queryer.queries[0]
	if !strings.Contains(query, " WHERE `__loom_row_id` > ?") {
		t.Fatalf("cursor predicate missing WHERE: %s", query)
	}
	if strings.Contains(query, " AND `__loom_row_id`") {
		t.Fatalf("cursor predicate incorrectly uses AND without WHERE: %s", query)
	}
}

func TestPageRejectsCursorReplayedAgainstChangedQuery(t *testing.T) {
	queryer := &pageQueryer{}
	materialization := Materialization{ID: "execution:Patient", Revision: "execution", Project: "project", DatasetGeneration: "generation", PhysicalTable: "published_patient", Selector: DataframeSelector{Recipe: "recipe", TranslationVersion: "v1", Output: "Patient"}, Columns: []Column{{Name: "name", ClickHouse: "String"}}}
	original := PageRequest{First: 1, Sort: &Sort{Column: "name"}, Filters: []Filter{{Column: "name", Op: "EQ", Value: "alice"}}, Unrestricted: true}
	binding, err := cursorFingerprint(materialization, original)
	if err != nil {
		t.Fatal(err)
	}
	original.After = encodeBoundCursor("1", "alice", binding)
	changed := original
	changed.Filters = []Filter{{Column: "name", Op: "EQ", Value: "bob"}}
	_, err = (&Reader{ClickHouse: queryer}).Page(context.Background(), materialization, changed)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != "STALE_CURSOR" {
		t.Fatalf("replayed cursor error = %#v, want STALE_CURSOR", err)
	}
	if len(queryer.queries) != 0 {
		t.Fatalf("query count = %d, want no backend call for mismatched cursor", len(queryer.queries))
	}
}

func TestPageRejectsCursorWhenPublicationBindingChanges(t *testing.T) {
	baseMaterialization := Materialization{ID: "execution:Patient", Revision: "revision-a", Project: "project", DatasetGeneration: "generation-a", PhysicalTable: "published_patient_a", Selector: DataframeSelector{Recipe: "recipe", TranslationVersion: "v1", Output: "Patient"}, Columns: []Column{{Name: "name", ClickHouse: "String"}}}
	baseRequest := PageRequest{First: 1, Sort: &Sort{Column: "name"}, Unrestricted: true}
	binding, err := cursorFingerprint(baseMaterialization, baseRequest)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(Materialization, PageRequest) (Materialization, PageRequest)
	}{
		{name: "sort", change: func(materialization Materialization, request PageRequest) (Materialization, PageRequest) {
			request.Sort.Desc = true
			return materialization, request
		}},
		{name: "generation", change: func(materialization Materialization, request PageRequest) (Materialization, PageRequest) {
			materialization.DatasetGeneration = "generation-b"
			return materialization, request
		}},
		{name: "physical table", change: func(materialization Materialization, request PageRequest) (Materialization, PageRequest) {
			materialization.PhysicalTable = "published_patient_b"
			return materialization, request
		}},
		{name: "selector", change: func(materialization Materialization, request PageRequest) (Materialization, PageRequest) {
			materialization.Selector.Output = "Observation"
			return materialization, request
		}},
		{name: "revision", change: func(materialization Materialization, request PageRequest) (Materialization, PageRequest) {
			materialization.Revision = "revision-b"
			return materialization, request
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := baseRequest
			if baseRequest.Sort != nil {
				sortCopy := *baseRequest.Sort
				request.Sort = &sortCopy
			}
			materialization, request := test.change(baseMaterialization, request)
			request.After = encodeBoundCursor("1", "alice", binding)
			queryer := &pageQueryer{}
			_, err := (&Reader{ClickHouse: queryer}).Page(context.Background(), materialization, request)
			userErr, ok := dataframeerrors.AsUserError(err)
			if !ok || userErr.Code() != "STALE_CURSOR" {
				t.Fatalf("replayed cursor error = %#v, want STALE_CURSOR", err)
			}
			if len(queryer.queries) != 0 {
				t.Fatalf("query count = %d, want no backend call for mismatched cursor", len(queryer.queries))
			}
		})
	}
}
