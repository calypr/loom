package compiler

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestExtensionCompilerLiteralValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_extension_" + uuid.NewString()
	leaf := func(value string) map[string]any { return map[string]any{"url": "urn:leaf", "valueString": value} }
	payload := map[string]any{"id": project, "resourceType": "Observation", "extension": []any{
		map[string]any{"url": "urn:left", "extension": []any{leaf("left-one"), leaf("left-two")}},
		map[string]any{"url": "urn:right", "extension": []any{leaf("right-only")}},
	}}
	raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": "Observation", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, parent, mode, path, kind string
		want                           any
	}{
		{"all retains both owners", "urn:left", "ALL", "valueString", "string", []any{"left-one", "left-two"}},
		{"value rejects both owners", "urn:left", "VALUE", "valueString", "string", map[string]any{"status": "INVALID_MULTIPLE_VALUES", "raw": []any{"left-one", "left-two"}}},
		{"right parent stays separate", "urn:right", "ALL", "valueString", "string", []any{"right-only"}},
		{"missing parent stays null", "urn:absent", "ALL", "valueString", "string", nil},
		{"omitted choice arms expose mismatch", "urn:right", "ALL", "valueQuantity.value", "decimal", map[string]any{"status": "INVALID_CHOICE_ARM", "raw": []any{"right-only"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := &fhirschema.ExtensionBinding{OwnerPath: "extension[].extension[]", URLPath: []string{tc.parent, "urn:leaf"}, ValuePath: tc.path, LogicalType: tc.kind}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "extension", Columns: []string{"feature"}, ColumnAliases: map[string]string{"feature": "feature"}, ProjectionMode: tc.mode, ExtensionCorrelation: binding}}}
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
				t.Fatalf("execute: %v\n%s", err, rendered.Query)
			}
			if len(rows) != 1 || !reflect.DeepEqual(rows[0]["feature"], tc.want) {
				t.Fatalf("rows=%#v; want feature=%#v\n%s", rows, tc.want, rendered.Query)
			}
		})
	}
}

func TestCorrelatedCompilerLiteralValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	coding := func(system, code string) map[string]any { return map[string]any{"system": system, "code": code} }
	component := func(codes []any, field string, value any) map[string]any {
		return map[string]any{"code": map[string]any{"coding": codes}, field: value}
	}
	quantity := func(value int) map[string]any { return map[string]any{"value": value, "unit": "cm"} }
	paired := []any{
		component([]any{coding("A", "shared"), coding("B", "decoy"), coding("A", "shared")}, "valueQuantity", quantity(111)),
		component([]any{coding("B", "shared"), coding("A", "other")}, "valueQuantity", quantity(222)),
		component([]any{coding("A", "shared")}, "valueQuantity", quantity(111)),
	}
	for _, tc := range []struct {
		name, system, code, mode, path, kind string
		components                           []any
		want                                 any
	}{
		{name: "all retains owners not duplicate coding", system: "A", code: "shared", mode: "ALL", components: paired, want: []any{float64(111), float64(111)}},
		{name: "distinct is explicit", system: "A", code: "shared", mode: "DISTINCT", components: paired, want: []any{float64(111)}},
		{name: "first is explicit", system: "A", code: "shared", mode: "FIRST", components: paired, want: float64(111)},
		{name: "value refuses multiplicity", system: "A", code: "shared", mode: "VALUE", components: paired, want: map[string]any{"status": "INVALID_MULTIPLE_VALUES", "raw": []any{float64(111), float64(111)}}},
		{name: "same coding and owning component", system: "B", code: "shared", mode: "VALUE", components: paired, want: float64(222)},
		{name: "cross element code cannot match", system: "B", code: "other", mode: "VALUE", components: paired, want: nil},
		{name: "missing system cannot match", system: "A", code: "shared", mode: "VALUE", components: []any{component([]any{map[string]any{"code": "shared"}}, "valueQuantity", quantity(333))}, want: nil},
		{name: "wrong choice arm remains visible", system: "A", code: "shared", mode: "VALUE", components: []any{component([]any{coding("A", "shared")}, "valueString", "not-numeric")}, want: map[string]any{"status": "INVALID_CHOICE_ARM", "raw": []any{"not-numeric"}}},
		{name: "string all remains array", system: "A", code: "shared", mode: "ALL", path: "valueString", kind: "string", components: []any{
			component([]any{coding("A", "shared"), coding("A", "shared")}, "valueString", "left"),
			component([]any{coding("A", "shared")}, "valueString", "right"),
		}, want: []any{"left", "right"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "loom_correlated_" + uuid.NewString()
			path, kind := tc.path, tc.kind
			if path == "" {
				path, kind = "valueQuantity.value", "decimal"
			}
			binding := &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: path, LogicalType: kind}
			payload := map[string]any{"id": project, "resourceType": "Observation", "component": tc.components}
			raw, err := json.Marshal(map[string]any{"_key": project, "id": project, "project": project, "project_id": project, "resourceType": "Observation", "payload": payload})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
				t.Fatal(err)
			}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "feature", Columns: []string{tc.code}, ColumnAliases: map[string]string{tc.code: "height_cm"}, ProjectionMode: tc.mode, Correlation: binding, CorrelationSystem: tc.system, CorrelationCode: tc.code, StringifyValue: kind == "string"}}}
			var lastQuery string
			var lastBinds map[string]any
			query := func(node semantic.SemanticNode) []map[string]any {
				t.Helper()
				physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: node}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Fatal(err)
				}
				rendered, err := aql.RenderPhysicalPlan(physical)
				if err != nil {
					t.Fatal(err)
				}
				lastQuery, lastBinds = rendered.Query, rendered.BindVars
				rows := []map[string]any{}
				if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil }); err != nil {
					t.Fatalf("execute rendered query: %v\n%s", err, rendered.Query)
				}
				return rows
			}
			rows := query(root)
			if len(rows) != 1 || !reflect.DeepEqual(rows[0]["height_cm"], tc.want) {
				t.Fatalf("rows=%#v; height_cm must equal %#v\nbinds=%#v\n%s", rows, tc.want, lastBinds, lastQuery)
			}
			if tc.name == "same coding and owning component" {
				filter := func(system, code string) spec.TypedFilter {
					return spec.TypedFilter{FieldRef: "component", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: system, Code: code}}}, Correlation: binding}
				}
				root.Filters = []spec.TypedFilter{filter("A", "shared"), filter("B", "shared")}
				if got := query(root); len(got) != 1 {
					t.Fatalf("both present pairs must match: %#v", got)
				}
				root.Filters = []spec.TypedFilter{filter("B", "other"), filter("B", "shared")}
				if got := query(root); len(got) != 0 {
					t.Fatalf("independent absent pair must reject row: %#v", got)
				}
			}
		})
	}
}
