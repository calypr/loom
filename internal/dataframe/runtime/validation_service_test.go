package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestValidationRecipeDigestIsDeterministicAndSensitive(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "patients", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	first, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second || len(first) != 64 {
		t.Fatalf("recipe digest is not deterministic sha256: %q / %q", first, second)
	}
	bundle.Outputs[0].Name = "changed"
	third, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("recipe digest did not change when recipe changed")
	}
}

func TestValidateCompilesWithoutExecutingRows(t *testing.T) {
	executed := false
	service := NewService(ServiceConfig{QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		executed = true
		return nil
	}})
	recipeInput := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "patients", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	result, err := service.Validate(context.Background(), ValidateRequest{Recipe: recipeInput, Bindings: recipe.RuntimeBindings{Project: "P1"}, Limit: 25})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !result.Valid || result.Project != "P1" || result.RootResourceType != "Patient" {
		t.Fatalf("unexpected validation result: %#v", result)
	}
	if result.RequestFingerprint == "" || !result.PreviewAllowed || !result.ExportAllowed {
		t.Fatalf("validation result omitted frontend contract fields: %#v", result)
	}
	if executed {
		t.Fatal("Validate executed rows")
	}
	if result.Diagnostics.Compilation <= 0 || result.Diagnostics.Total <= 0 {
		t.Fatalf("validation diagnostics missing compilation/total: %#v", result.Diagnostics)
	}
	warnings := result.Warnings
	if len(warnings) != 1 || strings.TrimSpace(warnings[0].Message) == "" {
		t.Fatalf("unexpected validation warnings: %#v", warnings)
	}
}
