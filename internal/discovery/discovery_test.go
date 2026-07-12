package discovery

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/fhirschema"
)

func TestGeneratedRootSummariesFollowActiveGeneratedSchema(t *testing.T) {
	got := GeneratedRootSummaries()
	want := fhirschema.ResourceTypes()
	if len(got) != len(want) {
		t.Fatalf("GeneratedRootSummaries() length = %d, want %d", len(got), len(want))
	}
	for index, resourceType := range want {
		root := got[index]
		if root.ResourceType != resourceType {
			t.Errorf("root %d resourceType = %q, want %q", index, root.ResourceType, resourceType)
		}
		if !root.Supported || root.Available || root.SupportReason != RootSupportNotObservedInCatalog {
			t.Errorf("root %q = %+v, want generated-only supported root", resourceType, root)
		}
		if root.CandidateColumnCount != 0 || root.RelationshipCount != 0 {
			t.Errorf("root %q carries catalog counts without catalog facts: %+v", resourceType, root)
		}
	}
}

func TestBuildSnapshotBuildsOpaqueCatalogAndSchemaBackedContract(t *testing.T) {
	facts := CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{
			{
				Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar",
				DocCount: 4, SampleCount: 2, DistinctValues: []string{"male", "female", "female"},
			},
			{
				Project: "project-a", AuthResourcePath: "restricted", ResourceType: "Patient", Path: "gender", Kind: "scalar",
				DocCount: 2, SampleCount: 2, DistinctValues: []string{"unknown", "female"}, DistinctTruncated: true,
			},
			{
				Project: "project-a", ResourceType: "Patient", Path: "birthDate", Kind: "scalar",
				DocCount: 5, SampleCount: 2, DistinctValues: []string{"1990-01-01", "2000-02-02"},
			},
			{
				Project: "project-a", ResourceType: "Patient", Path: "name[].family", Kind: "scalar",
				DocCount: 4, SampleCount: 2, DistinctValues: []string{"Ng", "Smith"},
			},
			{
				Project: "project-a", ResourceType: "Observation", Path: "code", Kind: "codeable_concept",
				DocCount: 4, SampleCount: 1, DistinctValues: []string{"Hemoglobin"}, PivotCandidate: true,
			},
			{
				Project: "project-a", ResourceType: "Observation", Path: "valueInteger", Kind: "scalar",
				DocCount: 4, SampleCount: 2, DistinctValues: []string{"12", "13"},
			},
			// Unsupported schema paths and resource types must never reach the
			// guided response merely because they appear in an old catalog.
			{Project: "project-a", ResourceType: "Patient", Path: "notRepresented", Kind: "scalar", DocCount: 1},
			{Project: "project-a", ResourceType: "Encounter", Path: "status", Kind: "scalar", DocCount: 1},
		},
		Relationships: []catalog.PopulatedReference{
			{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 7},
			// Duplicate source facts normalize idempotently rather than making
			// the discovery result depend on caller pagination/retries.
			{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 2},
			{FromType: "Patient", Label: "not_a_generated_route", ToType: "Specimen", EdgeCount: 99},
			{FromType: "Encounter", Label: "subject_Patient", ToType: "Patient", EdgeCount: 3},
		},
	}

	snapshot, err := BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("snapshot.Validate() error = %v", err)
	}

	patient := findRoot(t, snapshot.Dataset.Roots, "Patient")
	if !patient.Supported || !patient.Available || patient.SupportReason != RootSupportObservedInCatalog || patient.CandidateColumnCount != 3 || patient.RelationshipCount != 1 {
		t.Errorf("Patient summary = %+v, want observed supported root with 3 columns and 1 route", patient)
	}
	observation := findRoot(t, snapshot.Dataset.Roots, "Observation")
	if !observation.Available || observation.CandidateColumnCount != 2 || observation.RelationshipCount != 0 {
		t.Errorf("Observation summary = %+v, want 2 candidate columns", observation)
	}
	specimen := findRoot(t, snapshot.Dataset.Roots, "Specimen")
	if !specimen.Available || specimen.SupportReason != RootSupportObservedInCatalog || specimen.CandidateColumnCount != 0 {
		t.Errorf("Specimen summary = %+v, want availability through incoming route", specimen)
	}
	diagnosticReport := findRoot(t, snapshot.Dataset.Roots, "DiagnosticReport")
	if !diagnosticReport.Supported || diagnosticReport.Available || diagnosticReport.SupportReason != RootSupportNotObservedInCatalog {
		t.Errorf("DiagnosticReport summary = %+v, want supported but not observed", diagnosticReport)
	}

	if len(snapshot.Relationships.Entries) != 1 {
		t.Fatalf("relationship count = %d, want 1: %+v", len(snapshot.Relationships.Entries), snapshot.Relationships.Entries)
	}
	relationship := snapshot.Relationships.Entries[0]
	if relationship.FromResourceType != "Patient" || relationship.ToResourceType != "Specimen" || relationship.Label != "Subject" || relationship.ObservedEdgeCount != 7 {
		t.Errorf("relationship = %+v, want normalized Patient Subject Specimen route", relationship)
	}
	if strings.Contains(string(relationship.ID), "subject_Patient") {
		t.Errorf("relationship ID leaked underlying graph label: %q", relationship.ID)
	}

	gender := findColumn(t, snapshot.Columns, "Patient", "Gender")
	if gender.ValueKind != ValueKindString || !gender.CanSelect || !gender.CanFilter || gender.CanPivot || gender.Repeated || gender.PopulatedDocumentCount != 6 {
		t.Errorf("gender column = %+v", gender)
	}
	if !reflect.DeepEqual(gender.SuggestedValues, []string{"female", "male", "unknown"}) || !gender.ValuesTruncated {
		t.Errorf("gender values = %#v truncated=%t", gender.SuggestedValues, gender.ValuesTruncated)
	}
	if strings.Contains(string(gender.ID), "gender") {
		t.Errorf("column ID leaked canonical field path: %q", gender.ID)
	}

	pivot := findColumn(t, snapshot.Columns, "Observation", "Code")
	if pivot.ValueKind != ValueKindComposite || pivot.CanSelect || pivot.CanFilter || !pivot.CanPivot || pivot.Repeated {
		t.Errorf("Observation code pivot candidate = %+v", pivot)
	}

	repeated := findFilter(t, snapshot.Filters, "Patient", "Name Family")
	if !repeated.Repeated || !reflect.DeepEqual(repeated.Quantifiers, []FilterQuantifier{FilterAny, FilterAll, FilterNone}) {
		t.Errorf("repeated filter = %+v", repeated)
	}
	date := findFilter(t, snapshot.Filters, "Patient", "Birth Date")
	if !slices.Contains(date.Operators, FilterGreaterEq) || !slices.Contains(date.Operators, FilterLessEq) {
		t.Errorf("date filter lacks ordered operations: %+v", date.Operators)
	}
	if got, want := len(snapshot.Filters), 4; got != want {
		t.Errorf("filter count = %d, want %d", got, want)
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, forbidden := range []string{"name[].family", "subject_Patient", "auth_resource_path", "selector", "fieldRef"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("snapshot JSON leaked internal catalog/query detail %q: %s", forbidden, encoded)
		}
	}
}

func TestBuildSnapshotIsDeterministicAcrossCatalogFactOrder(t *testing.T) {
	facts := CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{
			{Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 3, DistinctValues: []string{"male", "female"}},
			{Project: "project-a", ResourceType: "Patient", Path: "birthDate", Kind: "scalar", DocCount: 3, DistinctValues: []string{"2000-01-01"}},
		},
		Relationships: []catalog.PopulatedReference{
			{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 4},
		},
	}
	first, err := BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("first BuildSnapshot() error = %v", err)
	}
	facts.Fields = append([]catalog.PopulatedField(nil), facts.Fields...)
	facts.Relationships = append([]catalog.PopulatedReference(nil), facts.Relationships...)
	slices.Reverse(facts.Fields)
	slices.Reverse(facts.Relationships)
	second, err := BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("second BuildSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("BuildSnapshot() changed with input order\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestBuildSnapshotBoundsSuggestedValuesAndRejectsWrongScope(t *testing.T) {
	values := make([]string, 0, maxSuggestedValues+5)
	for index := 0; index < maxSuggestedValues+5; index++ {
		values = append(values, string(rune('a'+index/10))+string(rune('0'+index%10)))
	}
	snapshot, err := BuildSnapshot(CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{{
			Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 55, DistinctValues: values,
		}},
	})
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	column := findColumn(t, snapshot.Columns, "Patient", "Gender")
	if len(column.SuggestedValues) != maxSuggestedValues || !column.ValuesTruncated {
		t.Errorf("bounded values = %d truncated=%t, want %d/true", len(column.SuggestedValues), column.ValuesTruncated, maxSuggestedValues)
	}

	_, err = BuildSnapshot(CatalogFacts{
		Project: "project-a",
		Fields:  []catalog.PopulatedField{{Project: "other-project", ResourceType: "Patient", Path: "gender", Kind: "scalar"}},
	})
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Errorf("scope-mismatched catalog field error = %v, want project error", err)
	}
}

func TestSnapshotValidateRejectsNonOpaqueColumnIdentifier(t *testing.T) {
	snapshot, err := BuildSnapshot(CatalogFacts{
		Project: "project-a",
		Fields:  []catalog.PopulatedField{{Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 1}},
	})
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	snapshot.Columns[0].ID = "Patient.gender"
	if err := snapshot.Validate(); err == nil || !strings.Contains(err.Error(), "opaque") {
		t.Errorf("Validate() error = %v, want opaque ID failure", err)
	}
}

func findRoot(t *testing.T, roots []RootResourceSummary, resourceType string) RootResourceSummary {
	t.Helper()
	for _, root := range roots {
		if root.ResourceType == resourceType {
			return root
		}
	}
	t.Fatalf("root %q not found", resourceType)
	return RootResourceSummary{}
}

func findColumn(t *testing.T, columns []CandidateColumn, resourceType, label string) CandidateColumn {
	t.Helper()
	for _, column := range columns {
		if column.ResourceType == resourceType && column.Label == label {
			return column
		}
	}
	t.Fatalf("column %s/%s not found in %+v", resourceType, label, columns)
	return CandidateColumn{}
}

func findFilter(t *testing.T, filters []GuidedFilterSuggestion, resourceType, label string) GuidedFilterSuggestion {
	t.Helper()
	for _, filter := range filters {
		if filter.ResourceType == resourceType && filter.Label == label {
			return filter
		}
	}
	t.Fatalf("filter %s/%s not found in %+v", resourceType, label, filters)
	return GuidedFilterSuggestion{}
}
