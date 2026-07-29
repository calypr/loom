package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogGenerationBindsUseExactOrLegacyNullNamespace(t *testing.T) {
	fields := populatedFieldsBindVars(PopulatedFieldOptions{
		Project:           "P1",
		DatasetGeneration: " generation-a ",
	})
	if got := fields["dataset_generation"]; got != "generation-a" {
		t.Fatalf("field dataset_generation bind = %#v, want normalized generation", got)
	}

	references := populatedReferencesBindVars(PopulatedReferenceOptions{
		Project:           "P1",
		DatasetGeneration: "generation-a",
	}, TraversalModeStorage)
	if got := references["dataset_generation"]; got != "generation-a" {
		t.Fatalf("reference dataset_generation bind = %#v, want generation-a", got)
	}
	builderReferences := populatedReferencesBindVars(PopulatedReferenceOptions{
		Project:  "P1",
		NodeType: "Patient",
	}, TraversalModeBuilder)
	if _, ok := builderReferences["from_type"]; ok {
		t.Fatalf("builder reference binds retained storage-only from_type: %#v", builderReferences)
	}
	if _, ok := builderReferences["mode"]; ok {
		t.Fatalf("reference binds retained obsolete mode parameter: %#v", builderReferences)
	}

	legacyFields := populatedFieldsBindVars(PopulatedFieldOptions{Project: "P1"})
	if got, present := legacyFields["dataset_generation"]; !present || got != nil {
		t.Fatalf("legacy field dataset_generation bind = %#v (present=%t), want explicit nil", got, present)
	}
	legacyReferences := populatedReferencesBindVars(PopulatedReferenceOptions{Project: "P1"}, TraversalModeStorage)
	if got, present := legacyReferences["dataset_generation"]; !present || got != nil {
		t.Fatalf("legacy reference dataset_generation bind = %#v (present=%t), want explicit nil", got, present)
	}

	for name, query := range map[string]struct {
		query  string
		prefix string
	}{
		"fields":     {query: populatedFieldsAQL, prefix: "d"},
		"references": {query: relationshipCatalogBuilderAQL, prefix: "d"},
		"auth paths": {query: existingAuthResourcePathsAQL, prefix: "d"},
	} {
		if !strings.Contains(query.query, "FILTER "+query.prefix+".dataset_generation == @dataset_generation") {
			t.Fatalf("%s query is missing exact dataset-generation predicate:\n%s", name, query.query)
		}
	}
	if !strings.Contains(existingAuthResourcePathsAQL, "RETURN { auth_resource_path: auth_resource_path }") {
		t.Fatalf("auth path query must return an object for QueryRows decoding:\n%s", existingAuthResourcePathsAQL)
	}
}

func TestCatalogGenerationResultContractsExposeGeneration(t *testing.T) {
	fields, err := json.Marshal(PopulatedField{DatasetGeneration: "generation-a", ResourceType: "Patient", Path: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fields), `"dataset_generation":"generation-a"`) {
		t.Fatalf("field result JSON omitted generation: %s", fields)
	}

	references, err := json.Marshal(PopulatedReference{DatasetGeneration: "generation-a", FromType: "Patient", Label: "subject_Patient", ToType: "Specimen"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(references), `"dataset_generation":"generation-a"`) {
		t.Fatalf("reference result JSON omitted generation: %s", references)
	}
}
