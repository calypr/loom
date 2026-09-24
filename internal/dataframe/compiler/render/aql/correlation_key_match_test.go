package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestCorrelationKeyMatchUsesDirectCodingArrayWhenSchemaShapeIsSimple(t *testing.T) {
	selector := func(path string) spec.Selector {
		t.Helper()
		value, err := spec.ParseSelector(path)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	renderer := &physicalPlanRenderer{setVariables: map[string]string{}, reservedVars: map[string]struct{}{}}
	match := ir.PhysicalCorrelationKeyMatch{
		Source:      ir.PhysicalValue{Variable: "observation", Path: []string{"payload"}},
		KeySelector: selector("code.coding[]"), SystemSelector: selector("system"), CodeSelector: selector("code"),
		SystemBindKey: "selected_system", CodeBindKey: "selected_code",
	}
	query, err := renderer.renderCorrelationKeyMatch(match)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`IS_ARRAY(observation.payload["code"]["coding"])`,
		`["system"] == @selected_system`,
		`["code"] == @selected_code`,
		"LIMIT 1",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("direct coding test missing %q: %s", want, query)
		}
	}
	if strings.Contains(query, "FLATTEN") {
		t.Fatalf("simple Coding array used nested selector extraction: %s", query)
	}

	match.OwnerSelector = selector("component[]")
	query, err = renderer.renderCorrelationKeyMatch(match)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "FLATTEN") {
		t.Fatalf("repeated owners must retain correlated generic extraction: %s", query)
	}
}
