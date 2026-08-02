package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogGenerationBindsUseExactOrLegacyNullNamespace(t *testing.T) {
	if got := DatasetGenerationBindValue(" generation-a "); got != "generation-a" {
		t.Fatalf("generation bind = %#v, want normalized generation", got)
	}
	if got := DatasetGenerationBindValue(" "); got != nil {
		t.Fatalf("legacy generation bind = %#v, want nil", got)
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
