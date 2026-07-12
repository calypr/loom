package recipe

import (
	"reflect"
	"strings"
	"testing"
)

func TestListTemplatesReturnsAllProductIntentsInStableOrder(t *testing.T) {
	first := ListTemplates()
	second := ListTemplates()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("ListTemplates is not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	gotIDs := make([]TemplateID, len(first))
	for index, template := range first {
		gotIDs[index] = template.ID
		if template.Name == "" || template.Description == "" {
			t.Fatalf("template %q has incomplete presentation metadata: %#v", template.ID, template)
		}
		if len(template.AllowedGrains) == 0 || len(template.AllowedDestinations) == 0 {
			t.Fatalf("template %q has incomplete capabilities: %#v", template.ID, template)
		}
		if !templateAllowsDestination(template, template.DefaultDestination) {
			t.Fatalf("template %q default destination %q is not allowed", template.ID, template.DefaultDestination)
		}
	}
	wantIDs := []TemplateID{
		TemplatePatientCohort,
		TemplateSpecimenInventory,
		TemplateFileManifest,
		TemplateDiagnoses,
		TemplateLabsObservations,
		TemplateStudyEnrollment,
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("template order = %v, want %v", gotIDs, wantIDs)
	}
}

func TestTemplateMetadataAPIsAreDefensive(t *testing.T) {
	listed := ListTemplates()
	listed[0].Name = "changed"
	listed[0].AllowedGrains[0] = GrainFile
	listed[0].AllowedDestinations[0] = DestinationCSV

	lookedUp, ok := LookupTemplate(TemplatePatientCohort)
	if !ok {
		t.Fatal("LookupTemplate(patient_cohort) = not found")
	}
	lookedUp.Description = "changed"
	lookedUp.AllowedGrains[0] = GrainFile
	lookedUp.AllowedDestinations[0] = DestinationCSV

	freshList := ListTemplates()
	freshLookup, ok := LookupTemplate(TemplatePatientCohort)
	if !ok {
		t.Fatal("LookupTemplate(patient_cohort) after mutation = not found")
	}
	if freshList[0].Name == "changed" || freshList[0].AllowedGrains[0] != GrainPatient || freshList[0].AllowedDestinations[0] != DestinationPreview {
		t.Fatalf("ListTemplates leaked mutable registry data: %#v", freshList[0])
	}
	if freshLookup.Description == "changed" || freshLookup.AllowedGrains[0] != GrainPatient || freshLookup.AllowedDestinations[0] != DestinationPreview {
		t.Fatalf("LookupTemplate leaked mutable registry data: %#v", freshLookup)
	}
	if _, ok := LookupTemplate("unknown"); ok {
		t.Fatal("LookupTemplate accepted unknown template")
	}
}

func TestTemplateRegistryValidationRejectsDuplicateAndMissingMetadata(t *testing.T) {
	tests := []struct {
		name      string
		templates []TemplateMetadata
		contains  string
	}{
		{
			name: "duplicate ID",
			templates: func() []TemplateMetadata {
				got := ListTemplates()
				got[1].ID = got[0].ID
				return got
			}(),
			contains: "duplicated",
		},
		{
			name:      "missing metadata",
			templates: ListTemplates()[:5],
			contains:  "expected metadata",
		},
		{
			name: "missing capability",
			templates: func() []TemplateMetadata {
				got := ListTemplates()
				got[0].AllowedGrains = nil
				return got
			}(),
			contains: "allowedGrains",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateTemplateRegistry(test.templates)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validateTemplateRegistry() error = %v, want message containing %q", err, test.contains)
			}
		})
	}
}
