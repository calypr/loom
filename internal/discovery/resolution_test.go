package discovery

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/fhirschema"
)

func TestCapabilityResolverResolvesEverySnapshotIDFromSameFacts(t *testing.T) {
	facts := resolutionFacts()
	snapshot, err := BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	resolver, err := NewCapabilityResolver(facts)
	if err != nil {
		t.Fatalf("NewCapabilityResolver() error = %v", err)
	}

	for _, candidate := range snapshot.Columns {
		resolved, err := resolver.ResolveColumn(candidate.ID)
		if err != nil {
			t.Fatalf("ResolveColumn(%q) error = %v", candidate.ID, err)
		}
		if resolved.ID != candidate.ID || resolved.ResourceType != candidate.ResourceType || resolved.ValueKind != candidate.ValueKind || resolved.Repeated != candidate.Repeated || resolved.CanSelect != candidate.CanSelect || resolved.CanFilter != candidate.CanFilter || resolved.CanPivot != candidate.CanPivot {
			t.Errorf("column resolution for %q does not match snapshot candidate\nresolved: %+v\ncandidate: %+v", candidate.ID, resolved, candidate)
		}
	}
	for _, relationship := range snapshot.Relationships.Entries {
		resolved, err := resolver.ResolveRelationship(relationship.ID)
		if err != nil {
			t.Fatalf("ResolveRelationship(%q) error = %v", relationship.ID, err)
		}
		if resolved.ID != relationship.ID || resolved.FromResourceType != relationship.FromResourceType || resolved.ToResourceType != relationship.ToResourceType || resolved.Multiplicity != relationship.Multiplicity || resolved.ObservedEdgeCount != relationship.ObservedEdgeCount {
			t.Errorf("relationship resolution for %q does not match snapshot relationship\nresolved: %+v\nrelationship: %+v", relationship.ID, resolved, relationship)
		}
		if resolved.EdgeLabel != "subject_Patient" || resolved.Traversal.FromType != "Patient" || resolved.Traversal.ToType != "Specimen" {
			t.Errorf("relationship resolution lost generated route metadata: %+v", resolved)
		}
	}

	gender := findColumn(t, snapshot.Columns, "Patient", "Gender")
	resolvedGender, err := resolver.ResolveColumn(gender.ID)
	if err != nil {
		t.Fatalf("ResolveColumn(gender) error = %v", err)
	}
	if resolvedGender.CanonicalPath != "gender" || resolvedGender.Selector == nil || fhirschema.SelectorExpression(*resolvedGender.Selector) != "gender" {
		t.Errorf("gender resolution selector = %+v path=%q", resolvedGender.Selector, resolvedGender.CanonicalPath)
	}
	if !containsFilterOperator(resolvedGender.FilterOperators, FilterContains) || len(resolvedGender.FilterQuantifiers) != 0 {
		t.Errorf("gender resolution filter metadata = operators=%v quantifiers=%v", resolvedGender.FilterOperators, resolvedGender.FilterQuantifiers)
	}

	code := findColumn(t, snapshot.Columns, "Observation", "Code")
	resolvedCode, err := resolver.ResolveColumn(code.ID)
	if err != nil {
		t.Fatalf("ResolveColumn(code) error = %v", err)
	}
	if resolvedCode.Selector != nil || resolvedCode.Pivot == nil {
		t.Fatalf("code resolution = %+v, want pivot-only capability", resolvedCode)
	}
	if resolvedCode.Pivot.Family != fhirschema.PivotFamilyObservationCodeValue ||
		fhirschema.SelectorExpression(resolvedCode.Pivot.ColumnSelector) != "code.coding[].display" ||
		fhirschema.SelectorExpression(resolvedCode.Pivot.ValueSelector) != "valueInteger" ||
		!equalStrings(resolvedCode.Pivot.Columns, []string{"Hemoglobin"}) {
		t.Errorf("code pivot metadata = %+v", resolvedCode.Pivot)
	}

	resolvedRelationship, err := resolver.ResolveRelationship(snapshot.Relationships.Entries[0].ID)
	if err != nil {
		t.Fatalf("ResolveRelationship() before JSON check: %v", err)
	}
	for _, value := range []any{resolvedGender, resolvedCode, resolvedRelationship} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal resolved capability: %v", err)
		}
		if string(encoded) != "{}" {
			t.Errorf("resolved capability unexpectedly has JSON surface: %s", encoded)
		}
	}
}

func TestCapabilityResolverRejectsUnknownStaleAndCrossProjectFacts(t *testing.T) {
	facts := resolutionFacts()
	snapshot, err := BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	gender := findColumn(t, snapshot.Columns, "Patient", "Gender")
	relationshipID := snapshot.Relationships.Entries[0].ID

	freshFacts := CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{{
			Project: "project-a", ResourceType: "Patient", Path: "birthDate", Kind: "scalar", DocCount: 1,
		}},
	}
	resolver, err := NewCapabilityResolver(freshFacts)
	if err != nil {
		t.Fatalf("NewCapabilityResolver(fresh) error = %v", err)
	}
	if _, err := resolver.ResolveColumn(gender.ID); !errors.Is(err, ErrColumnUnavailable) {
		t.Errorf("stale column error = %v, want ErrColumnUnavailable", err)
	}
	if _, err := resolver.ResolveRelationship(relationshipID); !errors.Is(err, ErrRelationshipUnavailable) {
		t.Errorf("stale relationship error = %v, want ErrRelationshipUnavailable", err)
	}
	unknownColumn := ColumnID("col_" + strings.Repeat("0", 64))
	unknownRelationship := RelationshipID("rel_" + strings.Repeat("0", 64))
	if _, err := resolver.ResolveColumn(unknownColumn); !errors.Is(err, ErrColumnUnavailable) {
		t.Errorf("unknown column error = %v, want ErrColumnUnavailable", err)
	}
	if _, err := resolver.ResolveRelationship(unknownRelationship); !errors.Is(err, ErrRelationshipUnavailable) {
		t.Errorf("unknown relationship error = %v, want ErrRelationshipUnavailable", err)
	}

	_, err = NewCapabilityResolver(CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{{
			Project: "other-project", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 1,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Errorf("cross-project facts error = %v, want project mismatch", err)
	}
}

func TestInvalidCatalogPivotDoesNotBecomeResolvableCapability(t *testing.T) {
	facts := CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{{
			Project:           "project-a",
			ResourceType:      "Observation",
			Path:              "code",
			Kind:              "codeable_concept",
			DocCount:          1,
			PivotCandidate:    true,
			PivotColumnSelect: "code.coding[].display",
			PivotValueSelect:  "notARepresentedValue",
			PivotColumns:      []string{"Hemoglobin"},
		}},
	}
	snapshot, err := BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	for _, column := range snapshot.Columns {
		if column.ResourceType == "Observation" && column.Label == "Code" {
			t.Errorf("unsupported observed pivot leaked into Snapshot: %+v", column)
		}
	}
	resolver, err := NewCapabilityResolver(facts)
	if err != nil {
		t.Fatalf("NewCapabilityResolver() error = %v", err)
	}
	if _, err := resolver.ResolveColumn(opaqueColumnID("Observation", "code")); !errors.Is(err, ErrColumnUnavailable) {
		t.Errorf("unsupported pivot resolution error = %v, want ErrColumnUnavailable", err)
	}
}

func resolutionFacts() CatalogFacts {
	return CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{
			{
				Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 3,
				DistinctValues: []string{"female", "male"},
			},
			{
				Project:           "project-a",
				ResourceType:      "Observation",
				Path:              "code",
				Kind:              "codeable_concept",
				DocCount:          3,
				DistinctValues:    []string{"Hemoglobin"},
				PivotCandidate:    true,
				PivotFamily:       fhirschema.PivotFamilyObservationCodeValue,
				PivotColumnSelect: "code.coding[].display",
				PivotValueSelect:  "valueInteger",
				PivotColumns:      []string{"Hemoglobin"},
			},
		},
		Relationships: []catalog.PopulatedReference{
			{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 3},
		},
	}
}

func containsFilterOperator(operators []FilterOperator, want FilterOperator) bool {
	for _, operator := range operators {
		if operator == want {
			return true
		}
	}
	return false
}
