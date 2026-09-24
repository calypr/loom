package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRenderCorrelatedPivotProjectsCollectGroups(t *testing.T) {
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
			query := renderGenericCorrelatedPivotTestQuery(t, true, test.mode)
			for _, want := range []string{
				"COLLECT __correlation_owner_key = __correlation_candidate.key INTO __correlation_owner_group = { values: __correlation_candidate.values, unsupported: __correlation_candidate.unsupported }",
				"UNIQUE(FLATTEN(__correlation_owner_group[*].values))",
				"UNIQUE(FLATTEN(__correlation_owner_group[*].unsupported))",
				"COLLECT __correlation_key = __correlation_pair.key INTO __correlation_group = { values: __correlation_pair.values, unsupported: __correlation_pair.unsupported }",
				"LET __correlation_flat_values = FLATTEN(__correlation_group[*].values)",
				"LET __correlation_unsupported_values = FLATTEN(__correlation_group[*].unsupported)",
				"INVALID_CHOICE_ARM",
				test.want,
			} {
				if !strings.Contains(query, want) {
					t.Errorf("query is missing %q:\n%s", want, query)
				}
			}
			for _, absent := range []string{
				"__correlation_owner_group[*].__correlation_candidate.values",
				"__correlation_owner_group[*].__correlation_candidate.unsupported",
				"__correlation_group[*].__correlation_pair.values",
				"__correlation_group[*].__correlation_pair.unsupported",
			} {
				if strings.Contains(query, absent) {
					t.Errorf("query retained unprojected group state %q:\n%s", absent, query)
				}
			}
			if test.mode == "ALL" && strings.Contains(query, "UNIQUE(FLATTEN(__correlation_group[*].values))") {
				t.Fatalf("ALL query deduplicates values across owners:\n%s", query)
			}
		})
	}

	objectQuery := renderGenericCorrelatedPivotTestQuery(t, false, "ALL")
	if !strings.Contains(objectQuery, "RETURN { [__correlation_key]:") {
		t.Fatalf("object pivot lost its keyed result:\n%s", objectQuery)
	}
	if !strings.Contains(objectQuery, "INTO __correlation_group = { values: __correlation_pair.values, unsupported: __correlation_pair.unsupported }") {
		t.Fatalf("object pivot does not project the outer collect group:\n%s", objectQuery)
	}
}

func renderGenericCorrelatedPivotTestQuery(t *testing.T, flattenSingle bool, mode string) string {
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
		bindVars:       map[string]any{"columns": []string{"selected-code"}, "system": "urn:system", "code": "selected-code"},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	correlation := ir.PhysicalCorrelation{
		Source:          ir.PhysicalValue{Variable: "root"},
		KeySelector:     selector("coding[]"),
		SystemSelector:  selector("system"),
		CodeSelector:    selector("code"),
		ValueScope:      ir.PhysicalCorrelationValueKeyItem,
		ValueSelector:   selector("valueString"),
		ChoiceArms:      []string{"valueString"},
		ChoiceSelectors: []spec.Selector{selector("valueString"), selector("valueInteger")},
		ValuePrimitive:  "string",
		SystemBindKey:   "system",
		CodeBindKey:     "code",
	}
	query, err := renderer.renderCorrelatedPivot(correlation, "columns", false, flattenSingle, mode)
	if err != nil {
		t.Fatal(err)
	}
	return query
}
