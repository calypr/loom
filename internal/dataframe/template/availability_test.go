package template

import "testing"

func TestResolveAlternativeFieldRefAndCatalogBoundedPivot(t *testing.T) {
	registry := DefaultRegistry()
	definition, _ := registry.Definition("file-manifest")
	availability := Resolve(definition, CapabilitySnapshot{
		Resources: []ResourceCapability{{
			ResourceType: "DocumentReference", Present: true,
			Fields: []FieldCapability{
				{ResourceType: "DocumentReference", FieldRef: "DocumentReference.id"},
				{ResourceType: "DocumentReference", FieldRef: "DocumentReference.content_attachment_title"},
				{ResourceType: "DocumentReference", FieldRef: "DocumentReference.content_attachment_url"},
			},
		}},
	})
	if availability.RootResourceType != "DocumentReference" {
		t.Fatalf("root = %q", availability.RootResourceType)
	}
	if availability.Status != StatusPartial {
		t.Fatalf("status = %s, want PARTIAL", availability.Status)
	}
	if len(availability.CommonColumns) != 3 {
		t.Fatalf("common columns = %#v", availability.CommonColumns)
	}
	if availability.CommonColumns[1].FieldRef != "DocumentReference.content_attachment_title" {
		t.Fatalf("unexpected selected title field = %#v", availability.CommonColumns[1])
	}
}

func TestResolveRequiresVisibleRootAndDoesNotInferFromFHIRSchema(t *testing.T) {
	definition, _ := DefaultRegistry().Definition("patient-cohort")
	availability := Resolve(definition, CapabilitySnapshot{})
	if availability.Status != StatusUnavailable {
		t.Fatalf("status = %s, want UNAVAILABLE", availability.Status)
	}
	if availability.RootResourceType != "" || len(availability.Starter.Fields) != 0 {
		t.Fatalf("unavailable template leaked starter data: %#v", availability)
	}
}

func TestResolveTraversalRequiresCatalogAndGeneratedFHIRMetadata(t *testing.T) {
	definition, _ := DefaultRegistry().Definition("study-enrollment")
	base := CapabilitySnapshot{Resources: []ResourceCapability{{ResourceType: "ResearchSubject", Present: true, Fields: []FieldCapability{{ResourceType: "ResearchSubject", FieldRef: "ResearchSubject.id"}}}}}
	missing := Resolve(definition, base)
	if len(missing.Traversals) != 0 || len(missing.Missing) == 0 {
		t.Fatalf("expected missing study traversal, got %#v", missing)
	}
	base.Relationships = []RelationshipCapability{{FromType: "ResearchSubject", Label: "study", ToType: "ResearchStudy"}}
	available := Resolve(definition, base)
	if len(available.Traversals) != 1 || available.Traversals[0].EdgeLabel != "study" {
		t.Fatalf("expected observed study route, got %#v", available.Traversals)
	}
}

func TestResolveRejectsUnknownRelationshipTuple(t *testing.T) {
	definition, _ := DefaultRegistry().Definition("study-enrollment")
	availability := Resolve(definition, CapabilitySnapshot{
		Resources:     []ResourceCapability{{ResourceType: "ResearchSubject", Present: true}},
		Relationships: []RelationshipCapability{{FromType: "ResearchSubject", Label: "not_generated", ToType: "ResearchStudy"}},
	})
	if len(availability.Traversals) != 0 {
		t.Fatalf("unknown generated route was advertised: %#v", availability.Traversals)
	}
}
