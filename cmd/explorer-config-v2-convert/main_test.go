package main

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestInferCategoricalSourcePreservesKnownNamespace(t *testing.T) {
	source, logicalType, err := inferSource("p", "DocumentReference", "base", "document_reference_assay", "assay")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != authoringv2.SourceCategoricalBySystem || source.Categorical == nil || source.Categorical.System != "https://humantumoratlas.org/assay" || source.Categorical.ProjectionMode != "FIRST" || logicalType != "string" {
		t.Fatalf("converted categorical source = %#v", source)
	}
	if _, err := fhirschema.ValidateCategoricalBinding("DocumentReference", source.Categorical.Binding); err != nil {
		t.Fatal(err)
	}
}

func TestInferComponentSourceRejectsMissingNamespaceAndValueBinding(t *testing.T) {
	if _, _, err := inferSource("p", "Observation", "base", "observation_component_values__height", "height"); err == nil {
		t.Fatal("converted a legacy component name without a recoverable namespace or value binding")
	}
}
