package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestRenderSystemVersionSelectionKeepsCodesTogetherAndExcludesOtherScopes(t *testing.T) {
	for _, version := range []string{"", "v2"} {
		t.Run("version="+version, func(t *testing.T) {
			binding := fhirschema.CorrelatedBinding{
				KeyPath: "code.coding[]", VersionPath: "version", ValueScope: fhirschema.CorrelatedValueScopeCoding,
				SelectionScope: fhirschema.CorrelatedSelectionScopeSystemVersion,
				SystemPath:     "system", CodePath: "code", ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
			}
			correlation, err := lower.LowerCorrelatedBindingWithVersion("Condition", binding, ir.PhysicalValue{BindKey: "fixture"}, "selected_system", "", "selected_version")
			if err != nil {
				t.Fatal(err)
			}
			fixture := map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "urn:selected", "version": version, "code": "normal", "display": "Normal"},
				map[string]any{"system": "urn:selected", "version": version, "code": "tumor", "display": "Tumor"},
				map[string]any{"system": "urn:other", "version": version, "code": "normal", "display": "Wrong system"},
				map[string]any{"system": "urn:selected", "version": "other-version", "code": "tumor", "display": "Wrong version"},
			}}}
			renderer := &physicalPlanRenderer{
				bindVars:       map[string]any{"fixture": fixture, "selected_system": "urn:selected", "selected_version": version, "columns": []string{"category"}},
				collectionKeys: map[string]struct{}{}, setVariables: map[string]string{}, reservedVars: map[string]struct{}{},
			}
			projection, err := renderer.renderCorrelatedPivot(correlation, "columns", false, false, "DISTINCT")
			if err != nil {
				t.Fatal(err)
			}
			predicate, err := renderer.renderCorrelationPredicate(correlation)
			if err != nil {
				t.Fatal(err)
			}
			for name, query := range map[string]string{"projection": projection, "predicate": predicate} {
				for _, required := range []string{"FILTER __correlation_system == @selected_system", "FILTER __correlation_version == @selected_version", `NOT_NULL(FIRST(`, `, "")`} {
					if !strings.Contains(query, required) {
						t.Fatalf("%s lacks exact system/version scope %q:\n%s", name, required, query)
					}
				}
				for _, forbidden := range []string{"FILTER __correlation_code ==", "POSITION(", "@selected_code", "normal", "tumor", "Wrong system", "Wrong version"} {
					if strings.Contains(query, forbidden) {
						t.Fatalf("%s narrows or embeds fixture codes with %q:\n%s", name, forbidden, query)
					}
				}
			}
			if !strings.Contains(projection, "RETURN { key: FIRST(@columns),") || !strings.Contains(projection, "SORTED_UNIQUE(__correlation_flat_values)") {
				t.Fatalf("codes must share one category value collection:\n%s", projection)
			}
			if len(renderer.bindVars) != 4 || correlation.CodeBindKey != "" {
				t.Fatalf("category has unused code binding: %#v, %#v", renderer.bindVars, correlation)
			}
		})
	}
}
