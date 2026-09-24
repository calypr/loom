package compiler

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestStructuredSemanticValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	for _, tc := range []struct {
		resource, owner, key, arm string
		value                     map[string]any
	}{
		{"Task", "input[]", "type", "valuePeriod", map[string]any{"start": "2026-01-01", "end": "2026-02-01"}},
		{"Task", "output[]", "type", "valueRatio", map[string]any{"numerator": map[string]any{"value": float64(5), "unit": "mg"}, "denominator": map[string]any{"value": float64(2), "unit": "mL"}}},
		{"Observation", "component[]", "code", "valueRange", map[string]any{"low": map[string]any{"value": float64(0), "unit": "cm"}, "high": map[string]any{"value": float64(10), "unit": "cm"}}},
		{"Group", "characteristic[]", "code", "valueRange", map[string]any{"high": map[string]any{"value": float64(10), "unit": "cm"}}},
	} {
		t.Run(tc.resource+"/"+tc.arm, func(t *testing.T) {
			if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: tc.resource}}}); err != nil {
				t.Fatal(err)
			}
			project := "structured_value_" + uuid.NewString()
			key := map[string]any{"coding": []any{map[string]any{"system": "urn:feature", "code": "interval"}}}
			payload := map[string]any{"resourceType": tc.resource, "id": project, strings.TrimSuffix(tc.owner, "[]"): []any{map[string]any{tc.key: key, tc.arm: tc.value}}}
			raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": tc.resource, "payload": payload})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.InsertBatchRaw(ctx, tc.resource, []json.RawMessage{raw}, false, "document"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := client.QueryRows(ctx, "REMOVE @key IN @@collection", 1, map[string]any{"key": project, "@collection": tc.resource}, func(map[string]any) error { return nil }); err != nil {
					t.Errorf("remove fixture: %v", err)
				}
			}()
			binding := fhirschema.CorrelatedBinding{OwnerPath: tc.owner, KeyPath: tc.owner + "." + tc.key + ".coding[]", SystemPath: "system", CodePath: "code", ValuePath: tc.arm, ChoiceArms: []string{tc.arm}, LogicalType: "object"}
			root := semantic.SemanticNode{Alias: "root", ResourceType: tc.resource,
				Pivots:       []semantic.SemanticPivot{{Name: "feature", Columns: []string{"interval"}, ColumnAliases: map[string]string{"interval": "result"}, ProjectionMode: "ALL", Correlation: &binding, CorrelationSystem: "urn:feature", CorrelationCode: "interval"}},
				OwnerRecords: []semantic.SemanticOwnerRecords{{Name: "evidence", Binding: binding, Key: fhirschema.CorrelatedKey{System: "urn:feature", Code: "interval"}}},
			}
			physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := aql.RenderPhysicalPlan(physical)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || !reflect.DeepEqual(rows[0]["result"], []any{tc.value}) {
				t.Fatalf("complete value changed: %#v, want %#v", rows, tc.value)
			}
			evidence, ok := rows[0]["evidence"].([]any)
			if !ok || len(evidence) != 1 {
				t.Fatalf("missing evidence: %#v", rows[0])
			}
			record, ok := evidence[0].(map[string]any)
			if !ok || record["status"] != "VALUE" || !reflect.DeepEqual(record["value"], tc.value) {
				t.Fatalf("structured evidence changed: %#v", evidence)
			}
			t.Logf("%s.%s = %#v", tc.resource, tc.arm, rows[0]["result"])
		})
	}
}
