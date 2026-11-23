package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
)

type dataframeRouteCatalog struct {
	publication.BundleCatalog
	execution publication.BundleExecution
}

func (c dataframeRouteCatalog) ListExecutions(context.Context, publication.BundleState, time.Time) ([]publication.BundleExecution, error) {
	return []publication.BundleExecution{c.execution}, nil
}

func (c dataframeRouteCatalog) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	return publication.BundlePointer{ExecutionID: c.execution.ID}, nil
}

func (c dataframeRouteCatalog) FindExecutionBySelector(_ context.Context, project, generation string, selector publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	output := c.execution.Outputs[0]
	if c.execution.Project == project && c.execution.DatasetGeneration == generation && c.execution.Selector(output.Name) == selector {
		return c.execution, output, nil
	}
	return publication.BundleExecution{}, publication.BundleOutputRecord{}, publication.ErrBundleNotFound
}

type dataframeRouteQueryer struct{}

func (dataframeRouteQueryer) QueryRowsArgs(_ context.Context, sql string, _ []string, args ...any) ([]map[string]any, error) {
	if strings.Contains(sql, "__loom_0_missing_count") {
		return []map[string]any{{"__loom_0_missing_count": int64(1)}}, nil
	}
	rowA := map[string]any{"name": "Alice", "__loom_row_id": "a", "__loom_total": int64(2)}
	rowB := map[string]any{"name": "April", "__loom_row_id": "b", "__loom_total": int64(2)}
	if len(args) > 1 && args[len(args)-1] == "a" {
		return []map[string]any{rowB}, nil
	}
	return []map[string]any{rowA, rowB}, nil
}

func (q dataframeRouteQueryer) QueryRowsArgsVisit(ctx context.Context, sql string, columns []string, visit func(map[string]any) error, args ...any) error {
	rows, err := q.QueryRowsArgs(ctx, sql, columns, args...)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func TestPublishedDataframeREST(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	reader := &published.Reader{ClickHouse: dataframeRouteQueryer{}, Catalog: dataframeRouteCatalog{
		execution: publication.BundleExecution{
			ID: "execution", BundleIdentity: publication.BundleIdentity{
				Name: "recipe", TranslationVersion: "v1", Project: "P1", DatasetGeneration: "generation",
			},
			State: publication.BundlePublished, UpdatedAt: now,
			Outputs: []publication.BundleOutputRecord{{
				Name: "Patient", PhysicalTable: "physical_patient",
				Columns: []publication.PhysicalColumn{
					{Name: "__loom_row_id", ClickHouse: "String"},
					{Name: "name", ClickHouse: "Nullable(String)"},
				},
				State: publication.BundlePublished, VerifiedAt: &now,
			}},
		},
	}}
	authorizer := authscope.AllowAllAuthorizer{}
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{
		Authenticator: authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "user", Projects: []string{"P1"}}},
		Authorizer:    authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	load, err := loadapi.NewService(loadapi.ServiceConfig{LoadGeneration: routeRunner{}.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	readRoutes := dataframeReadRoutes{published: dataframeapi.NewService(dataframeapi.Config{Reader: reader})}
	if err := registerRoutes(server, load, authorizer, &explorerHandlers{}, readRoutes); err != nil {
		t.Fatal(err)
	}
	post := func(path string, input any, status int) map[string]any {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.App().Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s status = %d, want %d: %#v", path, resp.StatusCode, status, got)
		}
		return got
	}
	selector := map[string]string{"recipe": "recipe", "translationVersion": "v1", "output": "Patient"}
	rows := map[string]any{
		"projectId": "P1", "selector": selector, "columns": []string{"name"}, "first": 1,
		"filters": []map[string]string{{"column": "name", "op": "STARTS_WITH", "value": "A"}},
		"sort":    map[string]any{"column": "name", "desc": false},
	}
	aggregations := map[string]any{
		"projectId": "P1", "selector": selector, "filters": rows["filters"],
		"specs": []map[string]string{{"name": "missing_name", "kind": "MISSING", "column": "name"}},
	}
	const rowsPath = "/api/v1/projects/P1/dataframes/rows"
	const aggregationsPath = "/api/v1/projects/P1/dataframes/aggregations"
	result := post(rowsPath, rows, http.StatusOK)
	if !reflect.DeepEqual(result["rows"], []any{map[string]any{"name": "Alice"}}) {
		t.Fatalf("unexpected first page: %#v", result)
	}
	cursor, ok := result["pageInfo"].(map[string]any)["endCursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("expected bound cursor: %#v", result["pageInfo"])
	}
	rows["after"] = cursor
	result = post(rowsPath, rows, http.StatusOK)
	if !reflect.DeepEqual(result["rows"], []any{map[string]any{"name": "April"}}) {
		t.Fatalf("unexpected second page: %#v", result)
	}
	facets := post(aggregationsPath, aggregations, http.StatusOK)
	if results, ok := facets["aggregations"].([]any); !ok || len(results) != 1 {
		t.Fatalf("expected one aggregation: %#v", facets)
	}
	assertError := func(path string, status int, code string) {
		t.Helper()
		body := post(path, rows, status)["error"].(map[string]any)
		id, _ := body["requestId"].(string)
		if body["code"] != code || id == "" || body["message"] == "" {
			t.Fatalf("unexpected error: %#v", body)
		}
	}
	rows["projectId"] = "foreign"
	assertError("/api/v1/projects/foreign/dataframes/rows", http.StatusForbidden, "FORBIDDEN")
	rows["projectId"] = "P1"
	rows["after"] = "malformed"
	assertError(rowsPath, http.StatusBadRequest, "INVALID_CURSOR")
	rows["after"] = cursor
	rows["filters"] = nil
	assertError(rowsPath, http.StatusConflict, "STALE_CURSOR")
}
