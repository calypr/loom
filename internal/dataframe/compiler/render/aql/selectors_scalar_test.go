package aql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// Keep the previous loop renderer as the differential execution oracle.
func legacySelectorArray(source string, selector spec.Selector, setSource, firstOnly bool, binds map[string]any) string {
	prefix, last := selector.Steps[:len(selector.Steps)-1], selector.Steps[len(selector.Steps)-1]
	lines, current := []string{"FOR __root IN [" + source + "]"}, "__root"
	if setSource {
		lines, current = []string{"FOR __item IN " + source, "FOR __root IN [__item.payload]"}, "__root"
	}
	for index, step := range prefix {
		next := fmt.Sprintf("__s%d", index)
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("FOR %s IN (%s.%s ? %s.%s : [])", next, current, step.Field, current, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("LET %s = ((%s.%s ? %s.%s : [])[%d])", next, current, step.Field, current, step.Field, *step.Index), "FILTER "+next+" != null")
		default:
			lines = append(lines, fmt.Sprintf("LET %s = %s.%s", next, current, step.Field), "FILTER "+next+" != null")
		}
		current = next
	}
	if selector.Filter != nil {
		binds["legacy_contains"] = selector.Filter.Needle
		lines = append(lines, fmt.Sprintf("FILTER CONTAINS(%s.%s ? %s.%s : \"\", @legacy_contains)", current, selector.Filter.Field, current, selector.Filter.Field))
	}
	lines = append(lines, "LET __value = "+extractFinalExpr(current, last), "FILTER __value != null")
	if firstOnly {
		lines = append(lines, "LIMIT 1")
	}
	return "(\n" + strings.Join(append(lines, "RETURN __value"), "\n") + "\n)"
}

func TestScalarSelectorArrayUsesDirectNullPreservingExpression(t *testing.T) {
	for _, path := range []string{"valueBoolean", "valueQuantity.value", "valueCodeableConcept.coding"} {
		selector, err := spec.ParseSelector(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, first := range []bool{false, true} {
			got, err := newSelectorTestRenderer().renderSelectorArrayFromSource("root", selector, false, first)
			if err != nil {
				t.Fatal(err)
			}
			value := "root." + path
			if want := "(" + value + " == null ? [] : [" + value + "])"; got != want {
				t.Fatalf("%s: got %s want %s", path, got, want)
			}
		}
	}
	if _, err := newSelectorTestRenderer().renderSelectorArrayFromSource("root", spec.Selector{}, false, false); err == nil {
		t.Fatal("empty selector accepted")
	}
}

func TestScalarSelectorArrayRetainsCollectionAndFilteredLoops(t *testing.T) {
	for _, path := range []string{"identifier[].value", "identifier[0].value", `valueString where usage contains "pivot"`, "valueString"} {
		selector, err := spec.ParseSelector(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, first := range []bool{false, true} {
			setSource := path == "valueString"
			got, err := newSelectorTestRenderer().renderSelectorArrayFromSource("root", selector, setSource, first)
			if err != nil {
				t.Fatal(err)
			}
			want := legacySelectorArray("root", selector, setSource, first, map[string]any{})
			if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(strings.ReplaceAll(want, "@legacy_contains", "@__loom_physical_selector_contains")), " ") {
				t.Fatalf("loop changed for %s: %s", path, got)
			}
		}
	}
}

func TestScalarSelectorArrayMatchesLegacyAgainstArango(t *testing.T) {
	endpoint := os.Getenv("LOOM_SELECTOR_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_SELECTOR_ARANGO_URL to an existing Arango database URL")
	}
	records := []any{map[string]any{"fallbackValue": false}, map[string]any{"value": 0, "fallbackValue": "later"}, nil, map[string]any{}, map[string]any{"value": nil}, map[string]any{"value": false, "usage": "pivot"}, map[string]any{"value": 0}, map[string]any{"value": ""}, map[string]any{"value": []any{}}, map[string]any{"value": []any{false, 0, "", nil}}, map[string]any{"value": map[string]any{"leaf": false}}, map[string]any{"value": map[string]any{"leaf": 0}}, map[string]any{"value": map[string]any{"leaf": ""}}, map[string]any{"value": map[string]any{"leaf": []any{}}}, map[string]any{"value": map[string]any{"leaf": map[string]any{"x": 1}}}, map[string]any{"value": []any{map[string]any{"leaf": "one"}, map[string]any{"leaf": "two"}}}, map[string]any{"identifier": []any{map[string]any{"value": "one"}, map[string]any{"value": "two"}}}}
	for _, path := range []string{"value", "value.leaf", "identifier[].value", "identifier[0].value", `value where usage contains "pivot"`, "fallback"} {
		for _, first := range []bool{false, true} {
			for _, setSource := range []bool{false, true} {
				selectorPath := path
				if path == "fallback" {
					selectorPath = "value"
				}
				selector, err := spec.ParseSelector(selectorPath)
				if err != nil {
					t.Fatal(err)
				}
				renderer := newSelectorTestRenderer()
				got, err := renderer.renderSelectorArrayFromSource("root", selector, setSource, first)
				if err != nil {
					t.Fatal(err)
				}
				binds := renderer.bindVars
				binds["records"] = records
				want := legacySelectorArray("root", selector, setSource, first, binds)
				if path == "fallback" {
					fallback, _ := spec.ParseSelector("fallbackValue")
					if setSource {
						renderer.setVariables["root"] = "root"
					}
					cardinality := ir.PhysicalArrayCardinality
					if first {
						cardinality = ir.PhysicalScalarCardinality
					}
					got, err = renderer.renderExtract(ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: cardinality, Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root"}, Selector: selector, Fallbacks: []ir.PhysicalSelectorFallback{{Source: ir.PhysicalValue{Variable: "root"}, Selector: fallback}}}})
					if err != nil {
						t.Fatal(err)
					}
					want = "FLATTEN([" + legacySelectorArray("root", selector, setSource, false, binds) + ", " + legacySelectorArray("root", fallback, setSource, false, binds) + "])"
					if first {
						want = "FIRST(" + want + ")"
					}
				}
				source := "record"
				if setSource {
					source = "[{payload: record}]"
				}
				query := "FOR record IN @records LET root = " + source + " RETURN {actual: " + got + ", expected: " + want + "}"
				payload, _ := json.Marshal(map[string]any{"query": query, "bindVars": binds, "batchSize": 1000})
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/_api/cursor", bytes.NewReader(payload))
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				var result struct {
					Error   bool   `json:"error"`
					Message string `json:"errorMessage"`
					HasMore bool   `json:"hasMore"`
					Result  []struct {
						Actual   any `json:"actual"`
						Expected any `json:"expected"`
					} `json:"result"`
				}
				err = json.NewDecoder(response.Body).Decode(&result)
				response.Body.Close()
				cancel()
				if err != nil || result.Error || result.HasMore || len(result.Result) != len(records) {
					t.Fatalf("%s first=%t set=%t: status=%d result=%+v decode=%v", path, first, setSource, response.StatusCode, result, err)
				}
				for i, row := range result.Result {
					if !reflect.DeepEqual(row.Actual, row.Expected) {
						t.Fatalf("%s first=%t set=%t record=%d actual=%#v expected=%#v", path, first, setSource, i, row.Actual, row.Expected)
					}
				}
			}
		}
	}
}
