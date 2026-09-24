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

func TestCategoricalResultPresentationAgainstArango(t *testing.T) {
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
	for _, tc := range []struct{ resource, owner, key string }{
		{"Observation", "", "code"},
		{"Observation", "component[]", "code"},
		{"Task", "input[]", "type"},
		{"Group", "characteristic[]", "code"},
	} {
		t.Run(tc.resource+"/"+tc.owner, func(t *testing.T) {
			if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: tc.resource}}}); err != nil {
				t.Fatal(err)
			}
			project := "semantic_result_" + uuid.NewString()
			key := map[string]any{"coding": []any{map[string]any{"system": "urn:measure", "code": "stage"}}}
			coding := []any{
				map[string]any{"system": "urn:result:A", "code": "a", "display": "Stage IIA"},
				map[string]any{"system": "urn:result:B", "code": "b"},
				map[string]any{"system": "urn:result:C", "code": "c", "display": "  "},
			}
			payload := map[string]any{"resourceType": tc.resource, "id": project}
			ownerValue := map[string]any{tc.key: key, "valueCodeableConcept": map[string]any{"coding": coding}}
			if tc.owner == "" {
				for field, value := range ownerValue {
					payload[field] = value
				}
			} else {
				payload[strings.TrimSuffix(tc.owner, "[]")] = []any{ownerValue}
			}
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
			keyPath := tc.key + ".coding[]"
			if tc.owner != "" {
				keyPath = tc.owner + "." + keyPath
			}
			binding := fhirschema.CorrelatedBinding{OwnerPath: tc.owner, KeyPath: keyPath, SystemPath: "system", CodePath: "code", ValuePath: "valueCodeableConcept.coding[].code", ChoiceArms: []string{"valueCodeableConcept"}, LogicalType: "string"}
			for _, presentation := range []string{"", fhirschema.ValuePresentationDisplayOrCode} {
				binding.ValuePresentation = presentation
				root := semantic.SemanticNode{Alias: "root", ResourceType: tc.resource, Pivots: []semantic.SemanticPivot{{Name: "feature", Columns: []string{"stage"}, ColumnAliases: map[string]string{"stage": "stage_result"}, ProjectionMode: "ALL", Correlation: &binding, CorrelationSystem: "urn:measure", CorrelationCode: "stage", StringifyValue: true}}}
				root.OwnerRecords = []semantic.SemanticOwnerRecords{{Name: "evidence", Binding: binding, Key: fhirschema.CorrelatedKey{System: "urn:measure", Code: "stage"}}}
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
					t.Fatalf("query: %v\n%s", err, rendered.Query)
				}
				want := []any{"a", "b", "c"}
				if presentation != "" {
					want[0] = "Stage IIA"
				}
				if len(rows) != 1 || !reflect.DeepEqual(rows[0]["stage_result"], want) {
					t.Fatalf("presentation %q: rows=%#v, want %v", presentation, rows, want)
				}
				evidence, ok := rows[0]["evidence"].([]any)
				if !ok || len(evidence) != 1 {
					t.Fatalf("missing evidence: %#v", rows[0])
				}
				record, ok := evidence[0].(map[string]any)
				if !ok {
					t.Fatalf("invalid evidence record: %#v", evidence[0])
				}
				if record["status"] != "VALUE" || !reflect.DeepEqual(record["value"], want) {
					t.Fatalf("repeated categorical values misclassified: %#v", record)
				}
				owner, ok := record["owner"].(map[string]any)
				if !ok || !reflect.DeepEqual(owner["valueCodeableConcept"], map[string]any{"coding": coding}) {
					t.Fatalf("original coding identities lost from evidence: %#v", record)
				}
				t.Logf("%s presentation=%q values=%v", tc.resource, presentation, rows[0]["stage_result"])
			}
		})
	}
}

func TestCategoricalNamespaceDoesNotCrossCodingSystemsAgainstArango(t *testing.T) {
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
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "semantic_namespace_" + uuid.NewString()
	payload := map[string]any{
		"resourceType": "Observation", "id": project,
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:namespace:A", "code": "same", "display": "A label"},
			map[string]any{"system": "urn:namespace:A", "code": "same", "display": "A label"},
			map[string]any{"system": "urn:namespace:B", "code": "same"},
		}},
	}
	raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": "Observation", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.QueryRows(ctx, "REMOVE @key IN @@collection", 1, map[string]any{"key": project, "@collection": "Observation"}, func(map[string]any) error { return nil }); err != nil {
			t.Errorf("remove fixture: %v", err)
		}
	}()
	binding := fhirschema.CategoricalBinding{
		OwnerPath: "code", KeyPath: "code.coding[]", SystemPath: "system", ValuePath: "code",
		ValueFallback: []string{"display"}, LogicalType: "string", ValuePresentation: fhirschema.ValuePresentationDisplayOrCode,
	}
	for _, test := range []struct {
		system       string
		want         []any
		distinctWant []any
	}{
		{system: "urn:namespace:A", want: []any{"A label", "A label"}, distinctWant: []any{"A label"}},
		{system: "urn:namespace:B", want: []any{"same"}, distinctWant: []any{"same"}},
		{system: "urn:namespace:missing", want: []any{}, distinctWant: []any{}},
	} {
		t.Run(test.system, func(t *testing.T) {
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
				Name: "namespace", Columns: []string{test.system}, ColumnAliases: map[string]string{test.system: "namespace_result"}, ProjectionMode: "ALL",
				Categorical: &binding, CategoricalSystem: test.system, StringifyValue: true,
			}}}
			root.Pivots = append(root.Pivots, semantic.SemanticPivot{
				Name: "distinct_namespace", Columns: []string{test.system}, ColumnAliases: map[string]string{test.system: "distinct_result"}, ProjectionMode: "DISTINCT",
				Categorical: &binding, CategoricalSystem: test.system, StringifyValue: true,
			})
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
				t.Fatalf("query: %v\n%s", err, rendered.Query)
			}
			if len(rows) != 1 || !reflect.DeepEqual(rows[0]["namespace_result"], test.want) {
				t.Fatalf("system %q returned %#v, want %v\n%s", test.system, rows, test.want, rendered.Query)
			}
			if !reflect.DeepEqual(rows[0]["distinct_result"], test.distinctWant) {
				t.Fatalf("system %q DISTINCT returned %#v, want %v", test.system, rows[0]["distinct_result"], test.distinctWant)
			}
		})
	}
}

func TestCategoricalNamespaceRendersSystemOnlyBinding(t *testing.T) {
	binding := fhirschema.CategoricalBinding{
		OwnerPath: "code", KeyPath: "code.coding[]", SystemPath: "system", ValuePath: "code",
		ValueFallback: []string{"display"}, LogicalType: "string", ValuePresentation: fhirschema.ValuePresentationDisplayOrCode,
	}
	root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
		Name: "namespace", Columns: []string{"urn:namespace:A"}, ColumnAliases: map[string]string{"urn:namespace:A": "namespace_result"}, ProjectionMode: "ALL",
		Categorical: &binding, CategoricalSystem: "urn:namespace:A", StringifyValue: true,
	}}}
	physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: "p"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered.BindVars["pivot_root_namespace_columns_system"]; got != "urn:namespace:A" {
		t.Fatalf("categorical system bind = %#v, binds=%#v", got, rendered.BindVars)
	}
	if _, exists := rendered.BindVars["pivot_root_namespace_columns_code"]; exists {
		t.Fatalf("categorical namespace unexpectedly created a code identity bind: %#v", rendered.BindVars)
	}
	if !strings.Contains(rendered.Query, "__categorical_system") || !strings.Contains(rendered.Query, "__categorical_display") {
		t.Fatalf("categorical namespace query lost same-Coding system/display selection:\n%s", rendered.Query)
	}
}
