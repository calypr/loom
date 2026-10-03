package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func correlatedAuthoringDocument(lookup *LookupSource) Document {
	return Document{
		Kind: Kind, Output: Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route:   RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"},
		Rows:    RecordsRowDefinition(),
		Columns: []Column{{Column: "component__shared", Label: "Shared", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceCodedValue, Lookup: lookup}}},
	}
}

func ownerRecordsAuthoringDocument(source *OwnerRecordsSource) Document {
	return Document{
		Kind: Kind, Output: Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"},
		Rows:  RecordsRowDefinition(),
		Columns: []Column{{Column: "components", Label: "Components", OccurrenceID: RootOccurrenceID,
			Source: ColumnSource{Kind: SourceOwnerRecords, OwnerRecords: source}}},
	}
}

func validOwnerRecordsSource() *OwnerRecordsSource {
	lookup := validCorrelatedLookup()
	return &OwnerRecordsSource{Binding: *lookup.Binding, Key: *lookup.Key}
}

func TestOwnerRecordsSourceIsClosedAndSchemaValidated(t *testing.T) {
	document := ownerRecordsAuthoringDocument(validOwnerRecordsSource())
	if err := document.Validate(); err != nil {
		t.Fatalf("valid owner-record source was rejected: %v", err)
	}
	raw, err := json.Marshal(document.Columns[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ColumnSource
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.OwnerRecords == nil || decoded.Lookup != nil {
		t.Fatalf("owner-record source did not round trip as its own variant: source=%#v err=%v", decoded, err)
	}

	mixed := document
	mixed.Columns = append([]Column(nil), document.Columns...)
	mixed.Columns[0].Source.Lookup = validCorrelatedLookup()
	if err := mixed.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("mixed owner-record source error = %v", err)
	}

	invalid := validOwnerRecordsSource()
	invalid.Binding.KeyPath = "code.coding[]"
	if err := ownerRecordsAuthoringDocument(invalid).Validate(); err == nil || !strings.Contains(err.Error(), "ownerRecords.binding") {
		t.Fatalf("cross-owner binding error = %v", err)
	}

	missingKey := validOwnerRecordsSource()
	missingKey.Key.Code = ""
	if err := ownerRecordsAuthoringDocument(missingKey).Validate(); err == nil || !strings.Contains(err.Error(), "key.system/code") {
		t.Fatalf("missing owner-record key error = %v", err)
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
	if err := correlatedAuthoringDocument(missingKey).Validate(); err == nil || !strings.Contains(err.Error(), "lookup.key.system/code") {
		t.Fatalf("missing correlated key error = %v", err)
	}
	legacyKey := &LookupSource{Match: "shared", Key: &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "shared"}}
	if err := correlatedAuthoringDocument(legacyKey).Validate(); err == nil || !strings.Contains(err.Error(), "lookup.binding") {
		t.Fatalf("legacy key error = %v", err)
	}
}

func TestCorrelatedBindingIsClosedToSupportedLookupKinds(t *testing.T) {
	for _, kind := range []string{SourceIdentifierBySystem, SourceExtensionByURL} {
		document := correlatedAuthoringDocument(validCorrelatedLookup())
		document.Columns[0].Source.Kind = kind
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "does not accept a correlated binding") {
			t.Fatalf("kind %s validation = %v", kind, err)
		}
	}
}

func validExtensionLookup() *LookupSource {
	return &LookupSource{Extension: &fhirschema.ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueString", LogicalType: "string", ChoiceArms: []string{"valueString"},
	}}
}

func extensionAuthoringDocument(lookup *LookupSource) Document {
	return Document{
		Kind: Kind, Output: Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route:   RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"},
		Rows:    RecordsRowDefinition(),
		Columns: []Column{{Column: "left_leaf", Label: "Left leaf", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceExtensionByURL, Lookup: lookup}}},
	}
}

func TestExtensionLookupKeepsURLAncestryAndRejectsMixedWrites(t *testing.T) {
	if err := extensionAuthoringDocument(validExtensionLookup()).Validate(); err != nil {
		t.Fatal(err)
	}
	conflicting := validExtensionLookup()
	conflicting.Match = "leaf"
	if err := extensionAuthoringDocument(conflicting).Validate(); err == nil || !strings.Contains(err.Error(), "must not combine") {
		t.Fatalf("mixed extension lookup error = %v", err)
	}
	legacy := &LookupSource{Match: "leaf", Path: "extension[].valueString"}
	if err := extensionAuthoringDocument(legacy).Validate(); err != nil {
		t.Fatalf("historical legacy source should remain readable: %v", err)
	}
}

func identifierAuthoringDocument(lookup *LookupSource, kind string) Document {
	return Document{
		Kind: Kind, Output: Output{ID: "out", Title: "Output"}, RootResourceType: "Patient",
		Route:   RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"},
		Rows:    RecordsRowDefinition(),
		Columns: []Column{{Column: "case_id", Label: "Case ID", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: kind, Lookup: lookup}}},
	}
}

func validIdentifierLookup() *LookupSource {
	return &LookupSource{Identifier: &fhirschema.IdentifierBinding{
		OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value",
		SystemURI: "urn:study:case-id", LogicalType: "string",
	}}
}

func TestIdentifierLookupIsExclusiveAndLegacySourcesRemainReadable(t *testing.T) {
	if err := identifierAuthoringDocument(validIdentifierLookup(), SourceIdentifierBySystem).Validate(); err != nil {
		t.Fatalf("typed identifier source was rejected: %v", err)
	}

	for name, mutate := range map[string]func(*LookupSource){
		"legacy match": func(lookup *LookupSource) { lookup.Match = "urn:study:case-id" },
		"legacy path":  func(lookup *LookupSource) { lookup.Path = "identifier[]" },
		"coding binding": func(lookup *LookupSource) {
			lookup.Binding = &fhirschema.CorrelatedBinding{KeyPath: "identifier[]", SystemPath: "system", CodePath: "code", ValuePath: "value", LogicalType: "string"}
		},
		"extension binding": func(lookup *LookupSource) {
			lookup.Extension = &fhirschema.ExtensionBinding{OwnerPath: "extension[]", URLPath: []string{"urn:leaf"}, ValuePath: "valueString", LogicalType: "string"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			lookup := validIdentifierLookup()
			mutate(lookup)
			if err := identifierAuthoringDocument(lookup, SourceIdentifierBySystem).Validate(); err == nil {
				t.Fatalf("mixed lookup alternative was accepted: %#v", lookup)
			}
		})
	}

	if err := identifierAuthoringDocument(validIdentifierLookup(), SourceExtensionByURL).Validate(); err == nil {
		t.Fatal("identifier binding was accepted by a non-identifier lookup kind")
	}
	legacy := &LookupSource{Match: "urn:study:case-id", Path: "identifier[]"}
	if err := identifierAuthoringDocument(legacy, SourceIdentifierBySystem).Validate(); err != nil {
		t.Fatalf("legacy identifier match/path was not readable: %v", err)
	}
}
