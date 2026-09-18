package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestUnitNormalizationLiteralValuesAgainstArango(t *testing.T) {
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

	valueSelector, err := spec.ParseSelector("valueQuantity.value")
	if err != nil {
		t.Fatal(err)
	}
	systemSelector, err := spec.ParseSelector("valueQuantity.system")
	if err != nil {
		t.Fatal(err)
	}
	codeSelector, err := spec.ParseSelector("valueQuantity.code")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, policy, system, code string
		value, want                float64
		wantUnknown                bool
	}{
		{name: "identity centimeters", policy: "to-centimeters", system: "http://unitsofmeasure.org", code: "cm", value: 180, want: 180},
		{name: "linear meters to centimeters", policy: "to-centimeters", system: "http://unitsofmeasure.org", code: "m", value: 1.8, want: 180},
		{name: "affine Celsius to Fahrenheit", policy: "to-fahrenheit", system: "http://unitsofmeasure.org", code: "Cel", value: 0, want: 32},
		{name: "affine Fahrenheit to Celsius", policy: "to-celsius", system: "http://unitsofmeasure.org", code: "[degF]", value: 32, want: 0},
		{name: "display label is not an identity", policy: "to-centimeters", code: "cm", value: 180, wantUnknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "loom_unit_" + uuid.NewString()
			payload := map[string]any{
				"id": project, "resourceType": "Observation",
				"valueQuantity": map[string]any{"value": tc.value, "system": tc.system, "code": tc.code, "unit": tc.code},
			}
			raw, marshalErr := json.Marshal(map[string]any{
				"_key": project, "id": project, "project": project, "project_id": project,
				"resourceType": "Observation", "payload": payload,
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, false, "document"); err != nil {
				t.Fatal(err)
			}

			policy, policyErr := unit.ResolveApprovedUnitPolicy(tc.policy, "1")
			if policyErr != nil {
				t.Fatal(policyErr)
			}
			dimension, rules, resolveErr := unit.ResolveApprovedUnitRules(policy.Rules, policy.Target)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			root := semantic.SemanticNode{Alias: "root", ResourceType: "Observation", Aggregates: []semantic.SemanticAggregate{{
				Name: "normalized", OutputName: "normalized", Operation: "REQUIRE_ONE", Selector: &valueSelector,
				UnitSystemSelector: &systemSelector, UnitCodeSelector: &codeSelector,
				UnitNormalization: &unit.UnitNormalization{Target: policy.Target, Dimension: dimension, Rules: rules},
			}}}
			physical, buildErr := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			rendered, renderErr := aql.RenderPhysicalPlan(physical)
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			rows := []map[string]any{}
			queryErr := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			})
			if tc.wantUnknown {
				if queryErr == nil || !strings.Contains(queryErr.Error(), "UNIT_IDENTITY_UNKNOWN") {
					t.Fatalf("query error = %v, want UNIT_IDENTITY_UNKNOWN\n%s", queryErr, rendered.Query)
				}
				return
			}
			if queryErr != nil {
				t.Fatalf("execute unit normalization query: %v\n%s", queryErr, rendered.Query)
			}
			if len(rows) != 1 {
				t.Fatalf("rows=%#v, want one normalized row", rows)
			}
			got, ok := rows[0]["normalized"].(float64)
			if !ok || math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("normalized=%#v, want %v", rows[0]["normalized"], tc.want)
			}
		})
	}
}

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

func TestContributorPredicatesRemainFeatureLocalAgainstArango(t *testing.T) {
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
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_contributor_" + uuid.NewString()
	patientKey := func(id string) string { return project + "_" + id }
	document := func(key, resourceType string, payload map[string]any) json.RawMessage {
		t.Helper()
		raw, marshalErr := json.Marshal(map[string]any{
			"_key": key, "id": payload["id"], "project": project, "project_id": project,
			"resourceType": resourceType, "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return raw
	}
	patients := []json.RawMessage{
		document(patientKey("p1"), "Patient", map[string]any{"id": "p1", "resourceType": "Patient"}),
		document(patientKey("p2"), "Patient", map[string]any{"id": "p2", "resourceType": "Patient"}),
	}
	observations := []json.RawMessage{}
	edges := []json.RawMessage{}
	for index, status := range []string{"registered", "cancelled", "cancelled"} {
		observationKey := fmt.Sprintf("%s_o%d", project, index+1)
		observations = append(observations, document(observationKey, "Observation", map[string]any{
			"id": fmt.Sprintf("o%d", index+1), "resourceType": "Observation", "status": status,
			"subject": map[string]any{"reference": "Patient/p1"},
		}))
		edge, marshalErr := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_e%d", project, index+1),
			"_from": "Observation/" + observationKey, "_to": "Patient/" + patientKey("p1"),
			"project": project, "project_id": project,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		edges = append(edges, edge)
	}
	if err := client.InsertBatchRaw(ctx, "Patient", patients, false, "document"); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", observations, false, "document"); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "fhir_edge", edges, false, "document"); err != nil {
		t.Fatal(err)
	}

	predicate := func(value string) *spec.TypedFilter {
		return &spec.TypedFilter{FieldRef: "observation-status", Selector: "status", FieldKind: spec.FilterString,
			Operator: spec.FilterEquals, Values: []spec.FilterValue{{Kind: spec.FilterString, String: &value}}}
	}
	root := semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{testSemanticField("patient_id", spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}, spec.ProjectionFirst)},
		Children: []semantic.SemanticNode{
			{Alias: "registered_observations", ResourceType: "Observation", EdgeLabel: "subject_Patient", Aggregates: []semantic.SemanticAggregate{{Name: "registered_count", OutputName: "registered_count", Operation: "COUNT", Predicate: predicate("registered")}}},
			{Alias: "cancelled_observations", ResourceType: "Observation", EdgeLabel: "subject_Patient", Aggregates: []semantic.SemanticAggregate{{Name: "cancelled_count", OutputName: "cancelled_count", Operation: "COUNT", Predicate: predicate("cancelled")}}},
		},
	}
	physical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.BindVars["aggregate_child_set_1_registered_count_predicate_value"] != "registered" || rendered.BindVars["aggregate_child_set_2_cancelled_count_predicate_value"] != "cancelled" {
		t.Fatalf("sibling contributor binds collided or disappeared: %#v", rendered.BindVars)
	}
	rows := []map[string]any{}
	if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute contributor query: %v\n%s", err, rendered.Query)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%#v, want both Patient roots", rows)
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[fmt.Sprint(row["patient_id"])] = row
	}
	for id, want := range map[string][2]float64{"p1": {1, 2}, "p2": {0, 0}} {
		row := byID[id]
		if row == nil || row["registered_count"] != want[0] || row["cancelled_count"] != want[1] {
			t.Fatalf("patient %s row=%#v, want registered=%v cancelled=%v; all rows=%#v", id, row, want[0], want[1], rows)
		}
	}

	root.Children[0].MatchMode = spec.TraversalMatchRequired
	requiredPhysical, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: project}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	requiredRendered, err := aql.RenderPhysicalPlan(requiredPhysical)
	if err != nil {
		t.Fatal(err)
	}
	requiredRows := []map[string]any{}
	if err := client.QueryRows(ctx, requiredRendered.Query, 100, requiredRendered.BindVars, func(row map[string]any) error {
		requiredRows = append(requiredRows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute required-match query: %v\n%s", err, requiredRendered.Query)
	}
	if len(requiredRows) != 1 || requiredRows[0]["patient_id"] != "p1" || requiredRows[0]["registered_count"] != float64(1) || requiredRows[0]["cancelled_count"] != float64(2) {
		t.Fatalf("required-match rows=%#v, want only p1 with independent contributor counts 1 and 2", requiredRows)
	}
}
