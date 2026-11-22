package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestCorrelatedScalarSelectorsPreserveOwnersAgainstArango(t *testing.T) {
	endpoint := arangotest.Shared(t).URL
	coding := func(system, code, version, display string) map[string]any {
		return map[string]any{"system": system, "code": code, "version": version, "display": display}
	}
	component := func(codings []any, value map[string]any) map[string]any {
		value["code"] = map[string]any{"coding": codings}
		return value
	}
	for _, tc := range []struct {
		name    string
		binding *fhirschema.CorrelatedBinding
		payload map[string]any
		want    any
	}{
		{name: "owner quantity and duplicate coding", binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"}, payload: map[string]any{"component": []any{
			component([]any{coding("A", "shared", "v2", ""), coding("A", "shared", "v2", "")}, map[string]any{"valueQuantity": map[string]any{"value": 111}}),
			component([]any{coding("B", "shared", "v2", ""), coding("A", "other", "v2", "")}, map[string]any{"valueQuantity": map[string]any{"value": 222}}),
			component([]any{coding("A", "shared", "v2", "")}, map[string]any{"valueString": "wrong arm"}),
		}}, want: map[string]any{"status": "INVALID_CHOICE_ARM", "raw": []any{"wrong arm"}}},
		{name: "matched coding display and blank fallback", binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", VersionPath: "version", ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, ValueScope: fhirschema.CorrelatedValueScopeCoding, LogicalType: "string"}, payload: map[string]any{"component": []any{
			component([]any{coding("B", "shared", "v2", "decoy system"), coding("A", "shared", "v1", "decoy version"), coding("A", "shared", "v2", "chosen")}, map[string]any{}),
			component([]any{coding("A", "shared", "v2", "  ")}, map[string]any{}),
		}}, want: []any{"chosen", "shared"}},
		{name: "owner quantity keeps zero and separate owners", binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"}, payload: map[string]any{"component": []any{
			component([]any{coding("A", "shared", "v2", ""), coding("A", "shared", "v2", "")}, map[string]any{"valueQuantity": map[string]any{"value": 0}}),
			component([]any{coding("B", "shared", "v2", ""), coding("A", "other", "v2", "")}, map[string]any{"valueQuantity": map[string]any{"value": 222}}),
			component([]any{coding("A", "shared", "v2", "")}, map[string]any{"valueQuantity": map[string]any{"value": 111}}),
		}}, want: []any{float64(0), float64(111)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := recipeplan.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []recipeplan.SemanticPivot{{Name: "feature", Columns: []string{"shared"}, ColumnAliases: map[string]string{"shared": "selected"}, ProjectionMode: "ALL", Correlation: tc.binding, CorrelationSystem: "A", CorrelationCode: "shared", CorrelationVersion: "v2", StringifyValue: tc.binding.LogicalType == "string"}}}
			plan, err := lower.BuildGenericPhysicalPlanWithPolicy(recipeplan.OutputPlan{Root: root}, recipeplan.ExecutionContext{Project: "selector-parity", DatasetGeneration: "generation"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := aql.RenderPhysicalPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			query := strings.Replace(rendered.Query, "FOR root IN @@root_collection", "FOR root IN @records", 1)
			if query == rendered.Query {
				t.Fatal("root collection substitution did not apply")
			}
			delete(rendered.BindVars, "@root_collection")
			rendered.BindVars["records"] = []any{map[string]any{"_key": "one", "project": "selector-parity", "dataset_generation": "generation", "payload": tc.payload}}
			body, err := json.Marshal(map[string]any{"query": query, "bindVars": rendered.BindVars, "batchSize": 100})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/_api/cursor", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var result struct {
				Error   bool             `json:"error"`
				Message string           `json:"errorMessage"`
				HasMore bool             `json:"hasMore"`
				Rows    []map[string]any `json:"result"`
			}
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if result.Error || result.HasMore || len(result.Rows) != 1 {
				t.Fatalf("status=%d response=%+v", response.StatusCode, result)
			}
			if !reflect.DeepEqual(result.Rows[0]["selected"], tc.want) {
				t.Fatalf("selected=%#v want=%#v", result.Rows[0]["selected"], tc.want)
			}
		})
	}
}
