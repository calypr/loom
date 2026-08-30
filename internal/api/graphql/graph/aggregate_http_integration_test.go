package graphqlapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	dfpublication "github.com/calypr/loom/internal/dataframe/publication"
	dfpublished "github.com/calypr/loom/internal/dataframe/published"
)

type aggregateHTTPCatalog struct {
	dfpublication.BundleCatalog
	execution dfpublication.BundleExecution
	lists     atomic.Int32
}

func (c *aggregateHTTPCatalog) ListExecutions(context.Context, dfpublication.BundleState, time.Time) ([]dfpublication.BundleExecution, error) {
	c.lists.Add(1)
	return []dfpublication.BundleExecution{c.execution}, nil
}

func (c *aggregateHTTPCatalog) GetPointer(context.Context, string) (dfpublication.BundlePointer, error) {
	return dfpublication.BundlePointer{ExecutionID: c.execution.ID}, nil
}

type aggregateHTTPQueryer struct {
	mu      sync.Mutex
	queries []string
}

func (q *aggregateHTTPQueryer) QueryRowsArgs(_ context.Context, query string, _ []string, _ ...any) ([]map[string]any, error) {
	q.mu.Lock()
	q.queries = append(q.queries, query)
	q.mu.Unlock()
	return nil, nil
}

func (q *aggregateHTTPQueryer) QueryRowsArgsVisit(ctx context.Context, query string, columns []string, visit func(map[string]any) error, args ...any) error {
	rows, err := q.QueryRowsArgs(ctx, query, columns, args...)
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

func TestGraphQLAggregateAliasesUseOneGroupingStatement(t *testing.T) {
	columns := []dfpublication.PhysicalColumn{{Name: "__loom_row_id", ClickHouse: "String"}}
	for i := 0; i < 156; i++ {
		columns = append(columns, dfpublication.PhysicalColumn{Name: fmt.Sprintf("facet_%03d", i), ClickHouse: "Nullable(String)"})
	}
	now := time.Now().UTC()
	catalog := &aggregateHTTPCatalog{execution: dfpublication.BundleExecution{
		ID: "execution", BundleIdentity: dfpublication.BundleIdentity{
			Name: "recipe", TranslationVersion: "v1", Project: "P1", DatasetGeneration: "generation",
		},
		State: dfpublication.BundlePublished, UpdatedAt: now,
		Outputs: []dfpublication.BundleOutputRecord{{
			Name: "Patient", PhysicalTable: "physical_patient", Columns: columns,
			State: dfpublication.BundlePublished, VerifiedAt: &now,
		}},
	}}
	queryer := &aggregateHTTPQueryer{}
	root := graphresolver.NewResolver(graphresolver.ResolverConfig{
		MaterializationReader: &dfpublished.Reader{ClickHouse: queryer, Catalog: catalog},
	})
	server, err := newGraphServer(root, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "user", Projects: []string{"P1"}}})
	if err != nil {
		t.Fatal(err)
	}

	var fields strings.Builder
	for i := 0; i < 156; i++ {
		directive := ""
		if i == 0 {
			directive = " @include(if: $include)"
		}
		fmt.Fprintf(&fields, " a%03d: dataframeAggregate(input: {projectId: \"P1\", selector: $selector, groupBy: [\"facet_%03d\"], operation: \"COUNT\"})%s { columns rows materialization { id } }", i, i, directive)
	}
	fields.WriteString(" totalA: dataframeAggregate(input: {projectId: \"P1\", selector: $selector, operation: \"COUNT\"}) { columns rows materialization { id } }")
	fields.WriteString(" totalB: dataframeAggregate(input: {projectId: \"P1\", selector: $selector, operation: \"COUNT\"}) { columns rows materialization { id } }")
	query := "query AggregateTable($selector: DataframeSelectorInput!, $include: Boolean!) {" +
		" page: dataframeRows(input: {projectId: \"P1\", selector: $selector, columns: [\"facet_000\"], first: 1}) { columns rows }" +
		" ...FacetFields" +
		" skipped: dataframeAggregate(input: {projectId: \"P1\", selector: $selector, operation: \"COUNT\"}) @skip(if: true) { columns }" +
		"} fragment FacetFields on Query {" + fields.String() + "}"
	body, err := json.Marshal(map[string]any{
		"query": query,
		"variables": map[string]any{
			"selector": map[string]any{"recipe": "recipe", "translationVersion": "v1", "output": "Patient"},
			"include":  true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/graphql/graph", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response, err := server.App().Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if errorsValue, ok := payload["errors"].([]any); ok && len(errorsValue) != 0 {
		t.Fatalf("unexpected GraphQL errors: %#v", errorsValue)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data: %#v", payload)
	}
	for i := 0; i < 156; i++ {
		if _, ok := data[fmt.Sprintf("a%03d", i)]; !ok {
			t.Fatalf("response is missing alias a%03d", i)
		}
	}
	if _, ok := data["totalA"]; !ok {
		t.Fatal("response is missing totalA")
	}
	if _, ok := data["totalB"]; !ok {
		t.Fatal("response is missing totalB")
	}
	if _, ok := data["skipped"]; ok {
		t.Fatal("skipped alias unexpectedly executed")
	}

	queryer.mu.Lock()
	queries := append([]string(nil), queryer.queries...)
	queryer.mu.Unlock()
	aggregateCalls := 0
	for _, statement := range queries {
		if strings.Contains(statement, "GROUP BY GROUPING SETS") {
			aggregateCalls++
		}
	}
	if aggregateCalls != 1 {
		t.Fatalf("aggregate ClickHouse calls = %d, want 1 (all calls %d)", aggregateCalls, len(queries))
	}
	if len(queries) > 5 {
		t.Fatalf("total ClickHouse calls = %d, want at most 5", len(queries))
	}
	if catalog.lists.Load() != 1 {
		t.Fatalf("catalog discovery calls = %d, want 1", catalog.lists.Load())
	}
}

func TestGraphQLTableRenderCombinesRowsAndTermsFacets(t *testing.T) {
	columns := []dfpublication.PhysicalColumn{{Name: "__loom_row_id", ClickHouse: "String"}}
	for i := 0; i < 3; i++ {
		columns = append(columns, dfpublication.PhysicalColumn{Name: fmt.Sprintf("facet_%03d", i), ClickHouse: "Nullable(String)"})
	}
	now := time.Now().UTC()
	catalog := &aggregateHTTPCatalog{execution: dfpublication.BundleExecution{
		ID: "execution", BundleIdentity: dfpublication.BundleIdentity{
			Name: "recipe", TranslationVersion: "v1", Project: "P1", DatasetGeneration: "generation",
		},
		State: dfpublication.BundlePublished, UpdatedAt: now,
		Outputs: []dfpublication.BundleOutputRecord{{
			Name: "Patient", PhysicalTable: "physical_patient", Columns: columns,
			State: dfpublication.BundlePublished, VerifiedAt: &now,
		}},
	}}
	queryer := &aggregateHTTPQueryer{}
	root := graphresolver.NewResolver(graphresolver.ResolverConfig{
		MaterializationReader: &dfpublished.Reader{ClickHouse: queryer, Catalog: catalog},
	})
	server, err := newGraphServer(root, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "user", Projects: []string{"P1"}}})
	if err != nil {
		t.Fatal(err)
	}

	query := `query TableRender($rows: DataframeRowsInput!, $facets: DataframeAggregationsInput!) {
		table: dataframeRows(input: $rows) {
			materialization { id revision }
			columns rows totalCount
			pageInfo { hasNextPage endCursor }
		}
		facets: dataframeAggregations(input: $facets) {
			materialization { id revision }
			aggregations
		}
	}`
	selector := map[string]any{"recipe": "recipe", "translationVersion": "v1", "output": "Patient"}
	specs := make([]map[string]any, 0, 3)
	for i := 0; i < 3; i++ {
		specs = append(specs, map[string]any{
			"name": fmt.Sprintf("facet_%03d", i), "kind": "TERMS", "column": fmt.Sprintf("facet_%03d", i),
			"size": 50, "excludeSelfFilter": true,
		})
	}
	body, err := json.Marshal(map[string]any{
		"query": query,
		"variables": map[string]any{
			"rows":   map[string]any{"projectId": "P1", "selector": selector, "columns": []string{"facet_000", "facet_001", "facet_002"}, "filters": []any{}, "first": 10},
			"facets": map[string]any{"projectId": "P1", "selector": selector, "filters": []any{}, "specs": specs},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/graphql/graph", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response, err := server.App().Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if errorsValue, ok := payload["errors"].([]any); ok && len(errorsValue) != 0 {
		t.Fatalf("unexpected GraphQL errors: %#v", errorsValue)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["table"] == nil || data["facets"] == nil {
		t.Fatalf("response is missing table or facets: %#v", payload)
	}

	queryer.mu.Lock()
	queries := append([]string(nil), queryer.queries...)
	queryer.mu.Unlock()
	termsCalls := 0
	missingCalls := 0
	for _, statement := range queries {
		if strings.Contains(statement, "row_number() OVER") {
			termsCalls++
			projected := strings.Index(statement, "AS __loom_terms_projected")
			if projected < 0 {
				t.Fatalf("terms projection boundary is missing: %s", statement)
			}
			if !strings.Contains(statement, "`__loom_slot` = 0") || strings.Contains(statement[projected:], "__loom_mask_") {
				t.Fatalf("terms ranking references the wrong projection scope: %s", statement)
			}
		}
		if strings.Contains(statement, "__loom_0_missing_count") {
			missingCalls++
		}
	}
	if termsCalls != 1 || missingCalls != 1 {
		t.Fatalf("aggregate calls = terms %d missing %d, want 1 and 1 (all calls %d)", termsCalls, missingCalls, len(queries))
	}
	if catalog.lists.Load() != 1 {
		t.Fatalf("catalog discovery calls = %d, want 1", catalog.lists.Load())
	}
}
