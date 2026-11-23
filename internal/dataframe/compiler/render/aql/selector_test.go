package aql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func newSelectorTestRenderer() *physicalPlanRenderer {
	return &physicalPlanRenderer{renderContext: renderContext{
		bindVars:     map[string]any{},
		setVariables: map[string]string{},
		reservedVars: map[string]struct{}{},
	}}
}

func TestAggregateItemRenderingKeepsDifferentSourceFallbackAtRoot(t *testing.T) {
	primary, err := spec.ParseSelector("valueString")
	if err != nil {
		t.Fatal(err)
	}
	localFallback, err := spec.ParseSelector("identifier[].value")
	if err != nil {
		t.Fatal(err)
	}
	rootFallback, err := spec.ParseSelector("identifier[].value")
	if err != nil {
		t.Fatal(err)
	}
	expression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		Extract: &ir.PhysicalExtract{
			Source: ir.PhysicalValue{Variable: "ancestor"}, ResourceType: "Observation", Selector: primary,
			Fallbacks: []ir.PhysicalSelectorFallback{
				{Source: ir.PhysicalValue{Variable: "ancestor"}, ResourceType: "Observation", Selector: localFallback},
				{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Observation", Selector: rootFallback},
			},
		},
	}
	renderer := &physicalPlanRenderer{renderContext: renderContext{bindVars: map[string]any{}}}
	query, err := renderer.renderAggregateItemValue(expression, "aggregate_item")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LET __root = aggregate_item.payload",
		"LET __root = root.payload",
		"__root.identifier",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("aggregate item query missing %q:\n%s", want, query)
		}
	}
	if expression.Extract.Source.Variable != "ancestor" || expression.Extract.Fallbacks[0].Source.Variable != "ancestor" || expression.Extract.Fallbacks[1].Source.Variable != "root" {
		t.Fatalf("rendering mutated the saved extract bindings: %#v", expression.Extract)
	}
	secondQuery, err := renderer.renderAggregateItemValue(expression, "aggregate_item_second")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LET __root = aggregate_item_second.payload",
		"LET __root = root.payload",
	} {
		if !strings.Contains(secondQuery, want) {
			t.Errorf("second aggregate query missing %q:\n%s", want, secondQuery)
		}
	}
	if strings.Contains(secondQuery, "aggregate_item.payload") {
		t.Fatalf("second render retained the previous item's fallback source:\n%s", secondQuery)
	}
}

func TestRenderConditionalSelectorArrayUsesInlineExpansionForOneIteratedArray(t *testing.T) {
	selector, err := spec.ParseSelector("type.coding[].display")
	if err != nil {
		t.Fatal(err)
	}
	renderer := &physicalPlanRenderer{}
	got, err := renderer.renderConditionalSelectorArray("root.payload", selector, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"root.payload.type.coding == null ? []",
		"ASSERT(IS_ARRAY(root.payload.type.coding), \"CONSTRUCTION_SELECTOR_ARRAY_TYPE_MISMATCH\")",
		"[* FILTER CURRENT.display != null RETURN CURRENT.display]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inline selector %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "FOR __loom_selector_") || strings.Contains(got, "LET __value") {
		t.Fatalf("one-array selector still renders a per-row subquery:\n%s", got)
	}
}

func TestRenderConditionalSelectorArrayKeepsSubqueryForUnsupportedShapes(t *testing.T) {
	for _, test := range []struct {
		name      string
		path      string
		firstOnly bool
	}{
		{name: "multiple arrays", path: "extension[].extension[].url"},
		{name: "first value", path: "coding[].display", firstOnly: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selector, err := spec.ParseSelector(test.path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&physicalPlanRenderer{}).renderConditionalSelectorArray("root.payload", selector, test.firstOnly)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, "FOR __loom_selector_") || !strings.Contains(got, "RETURN __value") {
				t.Fatalf("unsupported selector shape lost its ordered subquery:\n%s", got)
			}
			if strings.Contains(got, "[* FILTER CURRENT") {
				t.Fatalf("unsupported selector shape used inline expansion:\n%s", got)
			}
			if test.firstOnly && !strings.Contains(got, "LIMIT 1") {
				t.Fatalf("first-value selector lost its limit:\n%s", got)
			}
		})
	}
}

func TestRenderExtractFlattensTerminalRepeatedValuesAndDropsNullLeaves(t *testing.T) {
	for _, mode := range []ir.PhysicalSelectorExecutionMode{ir.PhysicalSelectorGeneric, ir.PhysicalSelectorConditionalArray} {
		for _, path := range []string{"code[]", "category[].coding[].code[]"} {
			t.Run(string(mode)+"/"+path, func(t *testing.T) {
				selector, err := spec.ParseSelector(path)
				if err != nil {
					t.Fatal(err)
				}
				expression := ir.PhysicalExpression{
					Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
					Extract: &ir.PhysicalExtract{
						Source:       ir.PhysicalValue{Variable: "record", Path: []string{"payload"}},
						ResourceType: "Observation", Selector: selector, ExecutionMode: mode,
					},
				}
				got, err := newSelectorTestRenderer().renderExtract(expression)
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{
					"FLATTEN(", "FILTER __loom_physical_selector_terminal_value != null", "RETURN __loom_physical_selector_terminal_value",
					": []", "FILTER __value != null",
				} {
					if !strings.Contains(got, want) {
						t.Errorf("terminal repeated selector %q omitted %q:\n%s", path, want, got)
					}
				}
			})
		}
	}
}

func TestRenderExtractFlattensTerminalRepeatedFallbackWithoutChangingDistinctPaths(t *testing.T) {
	primary, err := spec.ParseSelector("category[].coding[].code")
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := spec.ParseSelector("category[].coding[].display[]")
	if err != nil {
		t.Fatal(err)
	}
	baselineSource := ir.PhysicalValue{Variable: "record", Path: []string{"payload"}}
	expression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		Extract: &ir.PhysicalExtract{
			Source: baselineSource, ResourceType: "Observation", Selector: primary,
			ExecutionMode: ir.PhysicalSelectorGeneric,
			Fallbacks:     []ir.PhysicalSelectorFallback{{Source: baselineSource, ResourceType: "Observation", Selector: fallback}},
		},
	}
	got, err := newSelectorTestRenderer().renderExtract(expression)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "FILTER __loom_physical_selector_terminal_value != null") || strings.Count(got, "FLATTEN(") < 2 {
		t.Fatalf("terminal repeated fallback was not flattened and null-filtered independently:\n%s", got)
	}

	distinct, err := spec.ParseSelector("category[].coding[].code")
	if err != nil {
		t.Fatal(err)
	}
	distinctExpression := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		Extract: &ir.PhysicalExtract{
			Source: baselineSource, ResourceType: "Observation", Selector: distinct,
			ExecutionMode: ir.PhysicalSelectorConditionalArray, Distinct: true,
		},
	}
	distinctQuery, err := newSelectorTestRenderer().renderExtract(distinctExpression)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(distinctQuery, "SORTED_UNIQUE(") || strings.Contains(distinctQuery, "FLATTEN(") {
		t.Fatalf("ordinary repeated-leaf DISTINCT path changed shape unexpectedly:\n%s", distinctQuery)
	}
}

func TestRenderReturnQuotesReservedAndUnsafePhysicalPathSegments(t *testing.T) {
	const unusual = "quote\"and`backtick"
	query, err := newSelectorTestRenderer().renderReturn(ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
		Name:  "result",
		Value: ir.PhysicalValue{Variable: "row", Path: []string{"ordinary", "RETURN", "null", unusual}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "row.ordinary[\"RETURN\"][\"null\"][" + strconv.Quote(unusual) + "]"
	if !strings.Contains(query, want) {
		t.Fatalf("rendered return missing safely addressed path %q:\n%s", want, query)
	}
}

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
			want = strings.ReplaceAll(want, "FOR __root IN [root]", "LET __root = root")
			want = strings.ReplaceAll(want, "FOR __root IN [__item.payload]", "LET __root = __item.payload")
			if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(strings.ReplaceAll(want, "@legacy_contains", "@__loom_physical_selector_contains")), " ") {
				t.Fatalf("loop changed for %s: %s", path, got)
			}
		}
	}
}

func TestSelectorArrayBindsSingletonSourceWithoutEnumeratingOneItemArray(t *testing.T) {
	selector, err := spec.ParseSelector("valueCodeableConcept.coding[].display")
	if err != nil {
		t.Fatal(err)
	}

	got, err := newSelectorTestRenderer().renderSelectorArrayFromSource("root.payload", selector, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "FOR __root IN [root.payload]") || !strings.Contains(got, "LET __root = root.payload") {
		t.Fatalf("singleton source should be bound directly while retaining nested iteration:\n%s", got)
	}

	got, err = newSelectorTestRenderer().renderSelectorArrayFromSource("items", selector, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "FOR __root IN [__item.payload]") || !strings.Contains(got, "LET __root = __item.payload") || !strings.Contains(got, "FOR __item IN items") || !strings.Contains(got, "LIMIT 1") {
		t.Fatalf("set source should retain its item loop and first-only bound without singleton enumeration:\n%s", got)
	}
}

func TestScalarSelectorArrayMatchesLegacyAgainstArango(t *testing.T) {
	endpoint := arangotest.Shared(t).URL
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
