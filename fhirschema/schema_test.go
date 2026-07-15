package fhirschema

import "testing"

func TestFieldsForResourceIncludesExpectedPaths(t *testing.T) {
	cases := []struct {
		resourceType string
		path         string
	}{
		{"Patient", "identifier[].value"},
		{"Patient", "extension[].valueCode"},
		{"Condition", "code.coding[].display"},
		{"Specimen", "type.coding[].display"},
		{"ResearchSubject", "study.reference"},
		{"DocumentReference", "content[].attachment.title"},
		{"Observation", "code.coding[].display"},
		{"ImagingStudy", "series[].instance[].uid"},
		{"MedicationAdministration", "status"},
		{"Group", "member[].entity.reference"},
		{"ResearchStudy", "identifier[].value"},
	}
	for _, tc := range cases {
		if _, ok := LookupField(tc.resourceType, tc.path); !ok {
			t.Fatalf("expected %s path %q to exist", tc.resourceType, tc.path)
		}
	}
}

func TestSelectorFromFieldRoundTrips(t *testing.T) {
	field, ok := LookupField("DocumentReference", "content[].attachment.title")
	if !ok {
		t.Fatal("expected document reference field")
	}
	spec := SelectorFromField(field)
	if spec.SourcePath != "content[].attachment" {
		t.Fatalf("unexpected source path: %q", spec.SourcePath)
	}
	if spec.ValuePath != "title" {
		t.Fatalf("unexpected value path: %q", spec.ValuePath)
	}
	if got := SelectorExpression(spec); got != "content[].attachment.title" {
		t.Fatalf("unexpected selector expression: %q", got)
	}
}

func TestParseSelectorCanonicalizesIndexedPaths(t *testing.T) {
	sel, err := ParseSelector(`identifier[0].value where system contains "case_id"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.CanonicalPath(); got != "identifier[].value" {
		t.Fatalf("unexpected canonical path: %q", got)
	}
	if sel.Filter == nil || sel.Filter.Field != "system" || sel.Filter.Needle != "case_id" {
		t.Fatalf("unexpected filter: %#v", sel.Filter)
	}
}

func TestChoiceValueSelectorOptions(t *testing.T) {
	options := ChoiceValueSelectorOptions("Observation")
	if len(options) == 0 {
		t.Fatal("expected observation value selector options")
	}
	found := false
	for _, option := range options {
		if SelectorExpression(option) == "valueQuantity.value" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected valueQuantity.value option, got %#v", options)
	}
}

func TestValidatePivotSelectors(t *testing.T) {
	cc, err := ValidatePivotSelectors("Condition", FieldSelectorSpecFromPath("code.coding[].display"), FieldSelectorSpecFromPath("code.text"))
	if err != nil {
		t.Fatalf("codeable concept pivot validation failed: %v", err)
	}
	if cc.Family != PivotFamilyCodeableConcept {
		t.Fatalf("unexpected family: %q", cc.Family)
	}
	if cc.CatalogRootPath != "code" {
		t.Fatalf("unexpected codeable concept catalog root: %q", cc.CatalogRootPath)
	}

	obs, err := ValidatePivotSelectors("Observation", FieldSelectorSpecFromPath("code.coding[].display"), FieldSelectorSpecFromPath("valueQuantity.value"))
	if err != nil {
		t.Fatalf("observation pivot validation failed: %v", err)
	}
	if obs.Family != PivotFamilyObservationCodeValue {
		t.Fatalf("unexpected observation family: %q", obs.Family)
	}
	if obs.CatalogRootPath != "code" {
		t.Fatalf("unexpected observation catalog root: %q", obs.CatalogRootPath)
	}
	component, ok := DefaultPivotSpec("Observation", "component[].code", "")
	if !ok {
		t.Fatal("component pivot was not discovered from generated schema")
	}
	if component.ItemSourcePath != "component[]" || component.ItemResourceType != "ObservationComponent" {
		t.Fatalf("unexpected component item scope: %#v", component)
	}
	if CanonicalPath(component.ColumnSelector) != "code.text" || len(component.ValueSelectors) == 0 {
		t.Fatalf("unexpected component selectors: %#v", component)
	}
	observation, ok := DefaultPivotSpec("Observation", "code", "valueString")
	if !ok || CanonicalPath(observation.ColumnSelector) != "code.text" || CanonicalPath(observation.ValueSelector) != "valueString" {
		t.Fatalf("unexpected observation pivot selectors: %#v", observation)
	}
}

func TestLookupTraversal(t *testing.T) {
	cases := []struct {
		fromType  string
		edgeLabel string
		toType    string
	}{
		{"Patient", "subject_Patient", "Condition"},
		{"Patient", "subject_Patient", "Specimen"},
		{"Patient", "focus_Patient", "Observation"},
		{"Specimen", "subject_Specimen", "DocumentReference"},
		{"Group", "subject_Group", "DocumentReference"},
	}
	for _, tc := range cases {
		spec, ok := LookupTraversal(tc.fromType, tc.edgeLabel, tc.toType)
		if !ok {
			t.Fatalf("expected traversal %s %s %s", tc.fromType, tc.edgeLabel, tc.toType)
		}
		if spec.FromType != tc.fromType || spec.EdgeLabel != tc.edgeLabel || spec.ToType != tc.toType {
			t.Fatalf("unexpected traversal spec: %#v", spec)
		}
	}
}

func TestLookupTraversalRejectsUnknownTuple(t *testing.T) {
	if _, ok := LookupTraversal("Patient", "subject_Patient", "Medication"); ok {
		t.Fatal("expected unsupported tuple to miss")
	}
}
