package catalog

import (
	"context"
	"strings"
	"testing"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestNormalizedProjectAllowlistSortsDeduplicatesAndTrims(t *testing.T) {
	got := normalizedProjectAllowlist([]string{" P2 ", "P1", "", "P2", "P1"})
	want := []string{"P1", "P2"}
	if len(got) != len(want) {
		t.Fatalf("normalized projects = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized projects = %#v, want %#v", got, want)
		}
	}
}

func TestReadDatasetResourceTypesDecodesRowsAndPreservesScopeBinds(t *testing.T) {
	var gotQuery string
	var gotBatch int
	var gotBinds map[string]any
	query := datasetRowsQuery(func(_ context.Context, query string, batch int, binds map[string]any, visit arangostore.RowVisitor) error {
		gotQuery = query
		gotBatch = batch
		gotBinds = binds
		if err := visit(map[string]any{
			"resource_type":         "Specimen",
			"document_count":        int64(7),
			"populated_field_count": int64(3),
			"pivot_candidate_count": int64(1),
		}); err != nil {
			return err
		}
		return visit(map[string]any{
			"resource_type":         "Patient",
			"document_count":        float64(9),
			"populated_field_count": float64(4),
			"pivot_candidate_count": float64(0),
		})
	})

	rows, err := readDatasetResourceTypes(context.Background(), query, 37, map[string]any{
		"project":                          "P1",
		"dataset_generation":               nil,
		"auth_resource_paths":              []string{},
		"auth_resource_paths_unrestricted": false,
	}, "P1")
	if err != nil {
		t.Fatalf("readDatasetResourceTypes() error = %v", err)
	}
	if gotBatch != 37 || gotBinds["dataset_generation"] != nil || gotBinds["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("query contract batch=%d binds=%#v", gotBatch, gotBinds)
	}
	if !strings.Contains(gotQuery, "COLLECT resource_type = d.resource_type, path = d.path") || !strings.Contains(gotQuery, "pivot_candidate_count") {
		t.Fatalf("dataset query does not preserve deduplicating aggregation: %s", gotQuery)
	}
	if len(rows) != 2 || rows[0].ResourceType != "Patient" || rows[1].ResourceType != "Specimen" {
		t.Fatalf("rows = %#v, want deterministic resource ordering", rows)
	}
	if rows[0].DocumentCount != 9 || rows[0].PopulatedFieldCount != 4 || rows[1].PivotCandidateCount != 1 {
		t.Fatalf("decoded rows = %#v", rows)
	}
}

func TestReadDatasetResourceTypesRejectsMalformedRows(t *testing.T) {
	tests := []struct {
		name string
		row  map[string]any
	}{
		{name: "missing resource type", row: map[string]any{"document_count": int64(1), "populated_field_count": int64(1), "pivot_candidate_count": int64(0)}},
		{name: "invalid count", row: map[string]any{"resource_type": "Patient", "document_count": "one", "populated_field_count": int64(1), "pivot_candidate_count": int64(0)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := datasetRowsQuery(func(_ context.Context, _ string, _ int, _ map[string]any, visit arangostore.RowVisitor) error {
				return visit(test.row)
			})
			if _, err := readDatasetResourceTypes(context.Background(), query, 1, nil, "P1"); err == nil {
				t.Fatal("expected malformed row error")
			}
		})
	}
}

func TestDiscoverDatasetSummariesRequiresExplicitProjects(t *testing.T) {
	rows, err := DiscoverDatasetSummaries(context.Background(), DatasetSummaryOptions{})
	if err != nil {
		t.Fatalf("DiscoverDatasetSummaries() error = %v", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("empty project allowlist result = %#v, want non-nil empty", rows)
	}
}
