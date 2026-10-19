package recipe

import (
	"encoding/json"
	"strings"
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCorrelatedCategoryRecipeRequiresExplicitScopeAndOneColumn(t *testing.T) {
	pivot := Pivot{
		Name: "diagnosis", Columns: []string{"category"}, ProjectionMode: "DISTINCT", CorrelationSystem: "urn:test",
		Correlation: &fhirschema.CorrelatedBinding{
			KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", VersionPath: "version",
			ValueScope: fhirschema.CorrelatedValueScopeCoding, SelectionScope: fhirschema.CorrelatedSelectionScopeSystemVersion,
			ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
		},
	}
	encoded, err := json.Marshal(pivot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Pivot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	budget := 0
	if err := decoded.validateAt("pivot", &budget); err != nil {
		t.Fatalf("category recipe rejected: %v", err)
	}
	if decoded.Correlation == nil || decoded.Correlation.SelectionScope != fhirschema.CorrelatedSelectionScopeSystemVersion || decoded.CorrelationCode != "" {
		t.Fatalf("category recipe round-trip = %#v", decoded)
	}
	for name, mutate := range map[string]func(*Pivot){
		"implicit blank code": func(p *Pivot) { p.Correlation.SelectionScope = "" },
		"selected code":       func(p *Pivot) { p.CorrelationCode = "normal" },
		"multiple columns":    func(p *Pivot) { p.Columns = []string{"first", "second"} },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := decoded
			binding := *decoded.Correlation
			invalid.Correlation = &binding
			mutate(&invalid)
			budget := 0
			if err := invalid.validateAt("pivot", &budget); err == nil {
				t.Fatalf("invalid category recipe accepted: %#v", invalid)
			}
		})
	}
	bundle := Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion, Name: "category", TranslationVersion: "test",
		Outputs: []Output{{Name: "conditions", RootResourceType: "Condition", RowGrain: "condition", OwnerRecords: []OwnerRecordProjection{{
			Name: "records", Binding: *decoded.Correlation, Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "normal"},
		}}}},
	}
	if err := bundle.Validate(); err == nil || !strings.Contains(err.Error(), "SYSTEM_VERSION") {
		t.Fatalf("owner records category error = %v", err)
	}
}
