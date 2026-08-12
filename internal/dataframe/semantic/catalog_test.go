package semantic

import (
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestDiscoverCatalogEmptyIsValidAndDistinctFromPartial(t *testing.T) {
	empty := DiscoverCatalog(nil, CatalogOptions{Project: "P1", SourceGeneration: "g1"})
	if empty.Completeness.State != "empty" || len(empty.Resources) != 0 || len(empty.Diagnostics) != 0 {
		t.Fatalf("empty catalog = %#v", empty)
	}
	partial := DiscoverCatalog([]catalog.PopulatedField{{ResourceType: "Observation", Path: "code", DocCount: 2, SemanticObservations: []catalog.SemanticObservation{{Source: catalog.SemanticObservationSource{Canonical: "Observation.code", Type: "Observation", Path: "code"}, Value: catalog.SemanticObservationValue{Selector: "valueString", Type: "string"}, Cardinality: "single", Population: 2, Examples: []string{"x"}, ExamplesTruncated: true, RuleHint: "future.rule.v9", RuleVersion: "9"}}}}, CatalogOptions{Project: "P1", SourceGeneration: "g1"})
	if partial.Completeness.State != "complete" {
		t.Fatalf("example truncation should not imply resource scan partial: %#v", partial.Completeness)
	}
	if len(partial.Diagnostics) != 1 || partial.Diagnostics[0].Code != "unknown_rule" {
		t.Fatalf("unknown rule diagnostic = %#v", partial.Diagnostics)
	}
	concept := partial.Resources[0].Families[0].Concepts[0]
	if concept.RuleID != "future.rule.v9" || concept.Output.Generic != true || concept.Source.PopulationCount != 2 || !concept.Examples.Limited {
		t.Fatalf("catalog metadata not preserved: %#v", concept)
	}
}

func TestCatalogResultFeedsConceptLoweringAndPlanWithoutTranslation(t *testing.T) {
	fields := []catalog.PopulatedField{{
		ResourceType: "Patient", Path: "identifier[]", DocCount: 4,
		SemanticObservations: []catalog.SemanticObservation{{
			Source:      catalog.SemanticObservationSource{Canonical: "Patient.identifier[]", Type: "Patient", Path: "identifier[]"},
			Key:         catalog.SemanticObservationKey{Selector: "identifier[].system", System: "urn:mrn"},
			Value:       catalog.SemanticObservationValue{Selector: "identifier[].value", Type: "string"},
			Cardinality: CardinalityRepeated, Population: 3, Examples: []string{"123"},
			RuleHint: "IDENTIFIER_SYSTEM_VALUE", RuleVersion: "2",
		}},
	}}
	catalogResult := DiscoverCatalog(fields, CatalogOptions{Project: "P1", SourceGeneration: "g1"})
	byResource := catalogResult.ResultsByResource()
	concepts := byResource["Patient"].Concepts
	if len(concepts) != 1 {
		t.Fatalf("adapter concepts = %#v", concepts)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "semantic", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", ConceptSelections: []recipe.ConceptSelection{{ConceptID: concepts[0].ID, RuleID: concepts[0].RuleID, ColumnName: "mrn"}}}}}
	plan, err := BuildRecipePlanWithConcepts(bundle, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "g1"}, byResource)
	if err != nil {
		t.Fatalf("catalog -> concept lowering -> plan: %v", err)
	}
	output := plan.Outputs[0]
	if len(output.DynamicMaps) != 1 || output.DynamicMaps[0].Source.Expression.Selector.Path != "identifier[]" {
		t.Fatalf("dynamic map unexpectedly shaped: %#v", output.DynamicMaps)
	}
	if output.Unnest != nil {
		t.Fatalf("dynamic concept introduced row expansion: %#v", output.Unnest)
	}
	if len(output.ConceptColumns) != 1 || output.ConceptColumns[0].ConceptID != concepts[0].ID || !output.ConceptColumns[0].Repeated {
		t.Fatalf("concept audit metadata lost: %#v", output.ConceptColumns)
	}
}

func TestDiscoverCatalogMergesPartitionsAndPreservesStableIdentity(t *testing.T) {
	obs := catalog.SemanticObservation{SchemaVersion: 1, Source: catalog.SemanticObservationSource{Canonical: "Observation.code", Type: "Observation", Path: "code"}, Key: catalog.SemanticObservationKey{Selector: "code.coding[]", System: "s", Code: "c"}, Value: catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "number"}, Cardinality: "repeated", Population: 3, Examples: []string{"2"}, RuleHint: "OBSERVATION_CODE_VALUE", RuleVersion: "1"}
	left := catalog.PopulatedField{ResourceType: "Observation", Path: "code", DocCount: 4, SemanticObservations: []catalog.SemanticObservation{obs}}
	right := obs
	right.Population = 5
	right.Examples = []string{"1"}
	first := DiscoverCatalog([]catalog.PopulatedField{left, {ResourceType: "Observation", Path: "code", DocCount: 5, SemanticObservations: []catalog.SemanticObservation{right}}}, CatalogOptions{Project: "P1"})
	second := DiscoverCatalog([]catalog.PopulatedField{{ResourceType: "Observation", Path: "code", DocCount: 5, SemanticObservations: []catalog.SemanticObservation{right}}, left}, CatalogOptions{Project: "P1"})
	if len(first.Resources) != 1 || len(first.Resources[0].Families) != 1 || len(first.Resources[0].Families[0].Concepts) != 1 {
		t.Fatalf("unexpected merged catalog: %#v", first)
	}
	a, b := first.Resources[0].Families[0].Concepts[0], second.Resources[0].Families[0].Concepts[0]
	if a.ID != b.ID || a.Source.PopulationCount != 8 || len(a.Examples.Values) != 2 || a.Output.Cardinality != CardinalityRepeated || !a.Source.Repeated {
		t.Fatalf("unstable or incomplete merge: %#v %#v", a, b)
	}
}
