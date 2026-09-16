package authoringv2

import (
	"strings"
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func correlatedAuthoringDocument(lookup *LookupSource) Document {
	return Document{
		Kind: Kind, Output: Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route:   RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"},
		Columns: []Column{{Column: "component__shared", Label: "Shared", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceObservationComponentByCode, Lookup: lookup}}},
	}
}

func validCorrelatedLookup() *LookupSource {
	return &LookupSource{
		Binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
		Key:     &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "shared"},
	}
}

func TestCorrelatedLookupRequiresClosedSelectedKey(t *testing.T) {
	if err := correlatedAuthoringDocument(validCorrelatedLookup()).Validate(); err != nil {
		t.Fatal(err)
	}
	conflicting := validCorrelatedLookup()
	conflicting.Path = "component[]"
	if err := correlatedAuthoringDocument(conflicting).Validate(); err == nil || !strings.Contains(err.Error(), "binding with legacy") {
		t.Fatalf("conflicting correlated lookup error = %v", err)
	}
	missingKey := validCorrelatedLookup()
	missingKey.Key = nil
	if err := correlatedAuthoringDocument(missingKey).Validate(); err == nil || !strings.Contains(err.Error(), "key.system and key.code") {
		t.Fatalf("missing correlated key error = %v", err)
	}
	legacyKey := &LookupSource{Match: "shared", Key: &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "shared"}}
	if err := correlatedAuthoringDocument(legacyKey).Validate(); err == nil || !strings.Contains(err.Error(), "legacy lookup") {
		t.Fatalf("legacy key error = %v", err)
	}
}

func TestCorrelatedBindingIsClosedToSupportedLookupKinds(t *testing.T) {
	for _, kind := range []string{SourceIdentifierBySystem, SourceExtensionByURL} {
		document := correlatedAuthoringDocument(validCorrelatedLookup())
		document.Columns[0].Source.Kind = kind
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "only supported for codingBySystem") {
			t.Fatalf("kind %s validation = %v", kind, err)
		}
	}
}
