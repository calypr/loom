package semantic

import (
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestGeneratedDatatypeRegistryCoversEveryComplexDatatype(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	registry, err := GeneratedDatatypeRegistry()
	if err != nil {
		t.Fatalf("GeneratedDatatypeRegistry: %v", err)
	}
	if registry.Version() != DatatypeRegistryVersion {
		t.Fatalf("registry version = %d, want %d", registry.Version(), DatatypeRegistryVersion)
	}
	if err := registry.Validate(index); err != nil {
		t.Fatalf("Validate generated index: %v", err)
	}
	for _, definition := range index.Definitions() {
		if len(definition.Elements) == 0 {
			continue
		}
		descriptor, ok := registry.Lookup(definition.Name)
		if !ok {
			t.Errorf("generated complex datatype %q has no registry entry", definition.Name)
			continue
		}
		if descriptor.Disposition == "" {
			t.Errorf("generated complex datatype %q has no explicit disposition", definition.Name)
		}
	}
	for _, test := range []struct {
		datatype    schema.DefinitionName
		disposition Disposition
		projection  string
	}{
		{datatype: "Identifier", disposition: DispositionValueAssociation},
		{datatype: "Extension", disposition: DispositionValueAssociation},
		{datatype: "CodeableConcept", disposition: DispositionCategorical, projection: "text"},
		{datatype: "Coding", disposition: DispositionCategorical},
		{datatype: "Quantity", disposition: DispositionCompositeValue, projection: "value"},
		{datatype: "Reference", disposition: DispositionNavigationOnly},
	} {
		descriptor, ok := registry.Lookup(test.datatype)
		if !ok || descriptor.Disposition != test.disposition {
			t.Errorf("%s disposition = %q, want %q", test.datatype, descriptor.Disposition, test.disposition)
		}
		if test.projection != "" && (descriptor.ScalarProjection == nil || descriptor.ScalarProjection.Path != test.projection) {
			t.Errorf("%s projection = %#v, want %q", test.datatype, descriptor.ScalarProjection, test.projection)
		}
	}
}

func TestClassifyDirectFieldUsesDatatypeOwnershipInsteadOfResourceNames(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	tests := []struct {
		name        string
		resource    schema.DefinitionName
		path        string
		eligible    bool
		reason      DirectFieldReason
		owner       schema.DefinitionName
		disposition Disposition
	}{
		{name: "direct primitive", resource: "Patient", path: "birthDate", eligible: true, reason: DirectFieldPrimitiveLeaf, owner: "Patient"},
		{name: "identifier namespace", resource: "Patient", path: "identifier[].system", reason: DirectFieldSemanticDatatype, owner: "Identifier", disposition: DispositionValueAssociation},
		{name: "identifier value", resource: "Patient", path: "identifier[].value", reason: DirectFieldSemanticDatatype, owner: "Identifier", disposition: DispositionValueAssociation},
		{name: "coding system", resource: "Coding", path: "system", reason: DirectFieldSemanticDatatype, owner: "Coding", disposition: DispositionCategorical},
		{name: "coding code", resource: "Coding", path: "code", reason: DirectFieldSemanticDatatype, owner: "Coding", disposition: DispositionCategorical},
		{name: "coding display", resource: "Coding", path: "display", reason: DirectFieldSemanticDatatype, owner: "Coding", disposition: DispositionCategorical},
		{name: "codeable concept coding", resource: "DiagnosticReport", path: "code.coding[].code", reason: DirectFieldSemanticDatatype, owner: "CodeableConcept", disposition: DispositionCategorical},
		{name: "extension URL", resource: "Patient", path: "extension[].url", reason: DirectFieldSemanticDatatype, owner: "Extension", disposition: DispositionValueAssociation},
		{name: "extension value choice", resource: "Patient", path: "extension[].valueString", reason: DirectFieldSemanticDatatype, owner: "Extension", disposition: DispositionValueAssociation},
		{name: "quantity value", resource: "Observation", path: "valueQuantity.value", reason: DirectFieldSemanticDatatype, owner: "Quantity", disposition: DispositionCompositeValue},
		{name: "quantity unit", resource: "Observation", path: "valueQuantity.unit", reason: DirectFieldSemanticDatatype, owner: "Quantity", disposition: DispositionCompositeValue},
		{name: "reference target", resource: "Observation", path: "subject.reference", reason: DirectFieldSemanticDatatype, owner: "Reference", disposition: DispositionNavigationOnly},
		{name: "inherited resource id", resource: "Encounter", path: "id", eligible: true, reason: DirectFieldPrimitiveLeaf, owner: "Resource"},
		{name: "inherited resource type", resource: "Encounter", path: "resourceType", eligible: true, reason: DirectFieldPrimitiveLeaf, owner: "Resource"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification, err := ClassifyDirectField(index, test.resource, test.path)
			if err != nil {
				t.Fatalf("ClassifyDirectField: %v", err)
			}
			if classification.Eligible != test.eligible || classification.Reason != test.reason || classification.OwningDatatype != test.owner || classification.Disposition != test.disposition {
				t.Fatalf("classification = %#v, want eligible=%v reason=%q owner=%q disposition=%q", classification, test.eligible, test.reason, test.owner, test.disposition)
			}
		})
	}
}

func TestClassifyDirectFieldRejectsNonLeafAndInvalidPaths(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	classification, err := ClassifyDirectField(index, "DiagnosticReport", "code")
	if err != nil {
		t.Fatalf("ClassifyDirectField object: %v", err)
	}
	if classification.Eligible || classification.Reason != DirectFieldNonPrimitive || classification.OwningDatatype != "CodeableConcept" {
		t.Fatalf("object classification = %#v", classification)
	}
	if _, err := ClassifyDirectField(index, "Patient", "identifier.system"); err == nil {
		t.Fatal("repeated Identifier path without [] was accepted")
	}
	if _, err := ClassifyDirectField(index, "Patient", "unknownMember"); err == nil {
		t.Fatal("unknown member was accepted")
	}
}
