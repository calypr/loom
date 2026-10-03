package main

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestInferSourceRejectsLegacyCodedSourcesWithoutLossyFallback(t *testing.T) {
	tests := []struct {
		name          string
		rootResource  string
		leaf          string
		physical      string
		wantErrorPart string
	}{
		{
			name:          "Observation component by code",
			rootResource:  "Observation",
			leaf:          "observation_component_values__8480-6",
			physical:      "observation_component_values__8480-6",
			wantErrorPart: "cannot be represented losslessly",
		},
		{
			name:          "DocumentReference coding by system",
			rootResource:  "DocumentReference",
			leaf:          "document_reference_tumor_site",
			physical:      "document_reference_tumor_site",
			wantErrorPart: "identifies only a system",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, logicalType, err := inferSource("htan", test.rootResource, authoringv2.RootOccurrenceID, test.leaf, test.physical)
			if err == nil || !strings.Contains(err.Error(), test.wantErrorPart) {
				t.Fatalf("inferSource error = %v, want error containing %q", err, test.wantErrorPart)
			}
			if source.Kind != "" || logicalType != "" {
				t.Fatalf("unsupported legacy source produced a fallback: source=%#v logicalType=%q", source, logicalType)
			}
		})
	}
}

func TestInferSourceStillMapsSupportedSources(t *testing.T) {
	source, logicalType, err := inferSource("htan", "Specimen", authoringv2.RootOccurrenceID, "specimen_id", "specimen_id")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != authoringv2.SourceField || source.Field == nil || source.Field.Path != "id" || logicalType != "string" {
		t.Fatalf("specimen field source = %#v, logical type %q", source, logicalType)
	}

	source, logicalType, err = inferSource("htan", "Patient", authoringv2.RootOccurrenceID, "identifier_by_system_https___aced_idp_org_htan", "identifier_by_system_https___aced_idp_org_htan")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != authoringv2.SourceIdentifierBySystem || source.Lookup == nil || source.Lookup.Match != "https://aced-idp.org/htan" || source.Lookup.Path != "identifier[]" || logicalType != "string" {
		t.Fatalf("identifier lookup source = %#v, logical type %q", source, logicalType)
	}
}
