package clickhouse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
	"github.com/gofiber/fiber/v3"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
)

type fakeMaterializationService struct {
	datasetsCalls     int
	datasetCalls      int
	rowsCalls         int
	aggregateCalls    int
	aggregationsCalls int
	value             dfmaterialization.Materialization
}

func (f *fakeMaterializationService) Datasets(context.Context) ([]dfmaterialization.Materialization, error) {
	f.datasetsCalls++
	return []dfmaterialization.Materialization{f.value}, nil
}

func (f *fakeMaterializationService) Dataset(context.Context, model.DataframeDatasetInput) (*dfmaterialization.Materialization, error) {
	f.datasetCalls++
	value := f.value
	return &value, nil
}

func (f *fakeMaterializationService) Rows(context.Context, model.DataframeRowsInput) (dfmaterialization.Page, error) {
	f.rowsCalls++
	return dfmaterialization.Page{
		Materialization: f.value,
		Columns:         []string{"id"},
		Rows:            []map[string]any{{"id": "row-1"}},
		TotalCount:      1,
	}, nil
}

func (f *fakeMaterializationService) AggregateInput(context.Context, model.DataframeAggregateInput) (dfmaterialization.AggregateResult, error) {
	f.aggregateCalls++
	return dfmaterialization.AggregateResult{
		Materialization: f.value,
		Columns:         []string{"count"},
		Rows:            []map[string]any{{"count": 1}},
	}, nil
}

func (f *fakeMaterializationService) AggregationsInput(context.Context, model.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error) {
	f.aggregationsCalls++
	return dfmaterialization.AggregationsResult{Dataset: dfmaterialization.FederatedDataset{Name: f.value.Name, Revision: f.value.Revision, Columns: f.value.Columns, RowCount: f.value.RowCount}}, nil
}

func TestHandlerDelegatesFlatQueriesToMaterializationService(t *testing.T) {
	fake := &fakeMaterializationService{value: dfmaterialization.Materialization{
		ID:        "mat-1",
		Name:      "example",
		Revision:  "rev-1",
		State:     dfmaterialization.StateReady,
		Columns:   []dfmaterialization.Column{{Name: "id", ClickHouse: "String"}},
		RowCount:  1,
		CreatedAt: time.Unix(0, 0).UTC(),
	}}
	handler := NewHandler(fake)
	request := httptest.NewRequest(http.MethodPost, "/graphql/flat", strings.NewReader(`{"query":"query { dataframeDatasets { id name } dataframeDataset(input: { dataType: \"file\" }) { id name } dataframeRows(input: { dataType: \"file\", first: 1 }) { columns rows totalCount pageInfo { hasNextPage } } dataframeAggregate(input: { dataType: \"file\", operation: \"COUNT\" }) { columns rows } dataframeAggregations(input: { dataType: \"file\", specs: [{ name: \"categories\", kind: \"TERMS\", column: \"id\" }] }) { materialization { name } aggregations } }"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []json.RawMessage          `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode GraphQL response: %v", err)
	}
	if len(payload.Errors) != 0 {
		t.Fatalf("unexpected GraphQL errors: %s", response.Body.String())
	}
	for _, field := range []string{"dataframeDatasets", "dataframeDataset", "dataframeRows", "dataframeAggregate", "dataframeAggregations"} {
		if _, ok := payload.Data[field]; !ok {
			t.Errorf("missing response field %q: %s", field, response.Body.String())
		}
	}
	if fake.datasetsCalls != 1 || fake.datasetCalls != 1 || fake.rowsCalls != 1 || fake.aggregateCalls != 1 || fake.aggregationsCalls != 1 {
		t.Fatalf("unexpected service calls: datasets=%d dataset=%d rows=%d aggregate=%d aggregations=%d", fake.datasetsCalls, fake.datasetCalls, fake.rowsCalls, fake.aggregateCalls, fake.aggregationsCalls)
	}
}

type principalCheckingService struct {
	fakeMaterializationService
	subject string
}

func (s *principalCheckingService) Datasets(ctx context.Context) ([]dfmaterialization.Materialization, error) {
	principal, ok := authscope.PrincipalFromContext(ctx)
	if ok {
		s.subject = principal.Subject
	}
	return s.fakeMaterializationService.Datasets(ctx)
}

func TestHandlerPreservesFiberAuthenticationContext(t *testing.T) {
	service := &principalCheckingService{}
	handler := NewHandler(service)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authscope.ContextWithPrincipal(
			c.Context(),
			&authscope.Principal{Subject: "authenticated-user"},
		))
		return c.Next()
	})
	app.Post("/graphql/flat", fiberadaptor.HTTPHandlerWithContext(handler))

	request := httptest.NewRequest(
		http.MethodPost,
		"/graphql/flat",
		strings.NewReader(`{"query":"query { dataframeDatasets { name } }"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("serve GraphQL request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
	if service.subject != "authenticated-user" {
		t.Fatalf("principal subject = %q, want authenticated-user", service.subject)
	}
}
