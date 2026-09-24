package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRenderCategoricalNamespaceSingletonPivotUsesDirectReduction(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{mode: "ALL", want: `LENGTH(__correlation_unsupported_values) > 0 ? { status: "INVALID_CHOICE_ARM", raw: __correlation_unsupported_values } : __correlation_flat_values`},
		{mode: "DISTINCT", want: `LENGTH(__correlation_unsupported_values) > 0 ? { status: "INVALID_CHOICE_ARM", raw: __correlation_unsupported_values } : SORTED_UNIQUE(__correlation_flat_values)`},
		{mode: "VALUE", want: `LENGTH(__correlation_unsupported_values) > 0 ? { status: "INVALID_CHOICE_ARM", raw: __correlation_unsupported_values } : LENGTH(__correlation_flat_values) == 1 ? FIRST(__correlation_flat_values) : { status: "INVALID_MULTIPLE_VALUES", raw: __correlation_flat_values }`},
		{mode: "FIRST", want: `LENGTH(__correlation_unsupported_values) > 0 ? { status: "INVALID_CHOICE_ARM", raw: __correlation_unsupported_values } : FIRST(SORTED(__correlation_flat_values))`},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			query := renderCategoricalNamespaceSingletonTestQuery(t, []string{"urn:single"}, true, test.mode)
			for _, want := range []string{
				"FILTER __categorical_system != null AND __categorical_system != \"\"",
				"FILTER __categorical_system == @system",
				"FILTER POSITION(@columns, __categorical_system)",
				"FILTER LENGTH(__categorical_candidate_flat_values) > 0",
				"RETURN { key: __categorical_system, values: __categorical_candidate_values, unsupported: __categorical_candidate_unsupported_values }",
				"LET __categorical_candidate_unsupported_values = []",
				"FILTER LENGTH(__correlation_flat_values) > 0",
				test.want,
			} {
				if !strings.Contains(query, want) {
					t.Errorf("query is missing %q:\n%s", want, query)
				}
			}
			for _, absent := range []string{"COLLECT", "MERGE("} {
				if strings.Contains(query, absent) {
					t.Errorf("singleton %s query unexpectedly contains %q:\n%s", test.mode, absent, query)
				}
			}
			if test.mode == "DISTINCT" {
				if !strings.Contains(query, "SORTED_UNIQUE(__correlation_flat_values)") {
					t.Errorf("DISTINCT query does not deduplicate values:\n%s", query)
				}
			} else if strings.Contains(query, "UNIQUE(") {
				t.Errorf("%s query unexpectedly deduplicates values:\n%s", test.mode, query)
			}
		})
	}
}

func TestRenderCategoricalNamespaceSingletonPivotPreservesEmptyMapAndGenericPath(t *testing.T) {
	singletonQuery := renderCategoricalNamespaceSingletonTestQuery(t, []string{"urn:single"}, false, "ALL")
	if !strings.Contains(singletonQuery, "RETURN LENGTH(__categorical_reduction_pairs) == 0 ? {} : {") {
		t.Fatalf("singleton object pivot must return an empty object when no values match:\n%s", singletonQuery)
	}
	if strings.Contains(singletonQuery, "COLLECT") || strings.Contains(singletonQuery, "MERGE(") {
		t.Fatalf("singleton object pivot retained grouping operators:\n%s", singletonQuery)
	}

	multiColumnQuery := renderCategoricalNamespaceSingletonTestQuery(t, []string{"urn:first", "urn:second"}, false, "ALL")
	if !strings.Contains(multiColumnQuery, "COLLECT __categorical_owner_key") || !strings.Contains(multiColumnQuery, "MERGE(") {
		t.Fatalf("multi-column renderer no longer uses the existing grouped path:\n%s", multiColumnQuery)
	}
}

func renderCategoricalNamespaceSingletonTestQuery(t *testing.T, columns []string, flattenSingle bool, mode string) string {
	t.Helper()
	selector := func(path string) spec.Selector {
		t.Helper()
		parsed, err := spec.ParseSelector(path)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"columns": columns, "system": "urn:single"},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	correlation := ir.PhysicalCorrelation{
		Source:         ir.PhysicalValue{Variable: "root"},
		KeySelector:    selector("coding[]"),
		SystemSelector: selector("system"),
		ValueSelector:  selector("display"),
		SystemBindKey:  "system",
		ValuePrimitive: "string",
		NamespaceOnly:  true,
	}
	query, err := renderer.renderCorrelatedPivot(correlation, "columns", false, flattenSingle, mode)
	if err != nil {
		t.Fatal(err)
	}
	return query
}
