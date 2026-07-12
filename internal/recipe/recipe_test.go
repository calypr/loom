package recipe

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeRecipeV1(t *testing.T) {
	recipe := validRecipe()
	recipe.Columns = append(recipe.Columns, ColumnSelection{ID: " patient.id "})
	recipe.Filters = []Filter{{ColumnID: "patient.id", Operator: " EQUALS ", Values: []string{" 123 "}}}
	got, err := recipe.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(got.Columns) != 2 || got.Columns[0].ID != "patient.id" || got.Filters[0].Operator != FilterEquals || got.Filters[0].Values[0] != "123" {
		t.Fatalf("unexpected normalization: %#v", got)
	}
	if len(recipe.Columns) != 3 {
		t.Fatal("Normalize mutated its input")
	}
}

func TestAllProductTemplatesValidate(t *testing.T) {
	for _, template := range ListTemplates() {
		r := validRecipe()
		r.Template, r.Grain = template.ID, template.AllowedGrains[0]
		if err := r.Validate(); err != nil {
			t.Errorf("template %s: %v", template.ID, err)
		}
	}
}

func TestRecipeValidationErrorsAreTyped(t *testing.T) {
	r := validRecipe()
	r.Grain = GrainFile
	err := r.Validate()
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != "incompatible_grain" || validation.Field != "grain" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestRecipeRejectsUnsafeOrAmbiguousIntent(t *testing.T) {
	tests := []func(*Recipe){
		func(r *Recipe) { r.Version = "v2" },
		func(r *Recipe) { r.Project = "" },
		func(r *Recipe) { r.GenerationPolicy = GenerationPinned },
		func(r *Recipe) { r.Columns = nil },
		func(r *Recipe) { r.Columns[0].ID = "FOR x IN users" },
		func(r *Recipe) { r.Columns[1].OutputName = r.Columns[0].OutputName },
		func(r *Recipe) {
			r.Filters = []Filter{{ColumnID: "not.selected", Operator: FilterEquals, Values: []string{"x"}}}
		},
		func(r *Recipe) {
			r.Filters = []Filter{{ColumnID: "patient.id", Operator: FilterBetween, Values: []string{"x"}}}
		},
		func(r *Recipe) { r.Destination.Type = "shell" },
	}
	for index, mutate := range tests {
		r := validRecipe()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("case %d unexpectedly succeeded: %#v", index, r)
		}
	}
}

func TestNormalizeDefensiveCopies(t *testing.T) {
	r := validRecipe()
	r.Filters = []Filter{{ColumnID: "patient.id", Operator: FilterIn, Values: []string{"a", "b"}}}
	got, err := r.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	got.Columns[0].ID = "changed"
	got.Filters[0].Values[0] = "changed"
	if reflect.DeepEqual(r, got) || r.Columns[0].ID == "changed" || r.Filters[0].Values[0] == "changed" {
		t.Fatal("normalized recipe aliases input storage")
	}
}

func validRecipe() Recipe {
	return Recipe{
		Version: VersionV1, Template: TemplatePatientCohort, TemplateVersion: 1,
		Project: "demo", GenerationPolicy: GenerationLatest, Grain: GrainPatient,
		Columns:     []ColumnSelection{{ID: "patient.id", OutputName: "patient_id"}, {ID: "patient.gender", OutputName: "gender"}},
		Destination: Destination{Type: DestinationPreview},
	}
}
