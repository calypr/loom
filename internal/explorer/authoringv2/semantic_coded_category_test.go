package authoringv2

import (
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestResolveCodedCategoryPlanUsesSystemVersionScopeAndSameCodingFallback(t *testing.T) {
	for _, version := range []string{"", "v1"} {
		t.Run("version-"+version, func(t *testing.T) {
			observation := standaloneCodingCategoryObservation(version)
			plan := ResolveSemanticSelectionPlan(observation)
			if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
				t.Fatalf("category plan = %#v", plan)
			}
			binding := plan.Source.Lookup.Binding
			if binding.SelectionScope != fhirschema.CorrelatedSelectionScopeSystemVersion || binding.ValueScope != fhirschema.CorrelatedValueScopeCoding || binding.VersionPath != "version" || binding.CodePath != "code" || binding.ValuePath != "display" || len(binding.ValueFallback) != 1 || binding.ValueFallback[0] != "code" || !binding.FallbackOnBlankString {
				t.Fatalf("category binding = %#v", binding)
			}
			if plan.Source.Lookup.Key.System != "https://www.ebi.ac.uk/chembl/compound_name" || plan.Source.Lookup.Key.Code != "" || plan.Source.Lookup.Key.Version != version {
				t.Fatalf("category key = %#v", plan.Source.Lookup.Key)
			}
		})
	}
}

func TestResolveCodedCategoryRejectsAnExactCodeAndLegacyExactPlanRemainsExact(t *testing.T) {
	category := standaloneCodingCategoryObservation("")
	category.Key.Code = "CARBOXYAMIDOTRIAZOLE"
	if plan := ResolveSemanticSelectionPlan(category); plan.Readiness.Addable() || plan.Readiness.Code != "CODED_CATEGORY_HAS_EXACT_CODE" {
		t.Fatalf("category with exact code was not rejected: %#v", plan)
	}

	exact := category
	exact.RuleHint = catalog.SemanticRuleHintCodedConceptV1
	exact.Key.Code = "CARBOXYAMIDOTRIAZOLE"
	plan := ResolveSemanticSelectionPlan(exact)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		t.Fatalf("legacy exact plan = %#v", plan)
	}
	if plan.Source.Lookup.Binding.SelectionScope != "" || plan.Source.Lookup.Key.Code != "CARBOXYAMIDOTRIAZOLE" {
		t.Fatalf("legacy exact selection semantics changed: %#v", plan.Source.Lookup)
	}
}

func standaloneCodingCategoryObservation(version string) catalog.SemanticObservation {
	return catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Medication", Path: "Medication.code.coding[]"},
		Key: catalog.SemanticObservationKey{
			Selector: "code.coding[]", System: "https://www.ebi.ac.uk/chembl/compound_name", Version: version,
		},
		Value: catalog.SemanticObservationValue{
			Selector: "code.coding[].display", Type: "string", FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", Completeness: catalog.SemanticComplete, Status: "SUPPORTED",
		RuleHint: catalog.SemanticRuleHintCodedCategoryV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
}

func TestSemanticCategoryDefaultTitleUsesFieldForGenericSystem(t *testing.T) {
	if got := semanticCategoryDefaultTitle("Specimen", "Specimen.type.coding[]", "https://cda.readthedocs.io/"); got != "Specimen type" {
		t.Fatalf("generic system category title = %q", got)
	}
	if got := semanticCategoryDefaultTitle("Medication", "Medication.code.coding[]", "https://www.ebi.ac.uk/chembl/compound_name"); got != "Compound name" {
		t.Fatalf("ChEMBL category title = %q", got)
	}
	if got := semanticCategoryDefaultTitle("Specimen", "Specimen.type.coding[]", "https://example.org/my-code-system"); got != "My code system" {
		t.Fatalf("meaningful system-tail title = %q", got)
	}
}
