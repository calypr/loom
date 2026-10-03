package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func newSelectorTestRenderer() *physicalPlanRenderer {
	return &physicalPlanRenderer{
		bindVars:     map[string]any{},
		setVariables: map[string]string{},
		reservedVars: map[string]struct{}{},
	}
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
	renderer := &physicalPlanRenderer{bindVars: map[string]any{}}
	query, err := renderer.renderAggregateItemValue(expression, "aggregate_item")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FOR __root IN [aggregate_item.payload]",
		"FOR __root IN [root.payload]",
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
		"FOR __root IN [aggregate_item_second.payload]",
		"FOR __root IN [root.payload]",
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
