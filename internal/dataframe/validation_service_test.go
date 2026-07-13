package dataframe

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestRequestFingerprintIsDeterministicAndSensitive(t *testing.T) {
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
	}
	first, err := requestFingerprint(builder, 25)
	if err != nil {
		t.Fatalf("requestFingerprint() error = %v", err)
	}
	second, err := requestFingerprint(builder, 25)
	if err != nil {
		t.Fatalf("requestFingerprint() second error = %v", err)
	}
	if first == "" || first != second || len(first) != 64 {
		t.Fatalf("fingerprint is not deterministic sha256: %q / %q", first, second)
	}

	changed := builder
	changed.Project = "P2"
	third, err := requestFingerprint(changed, 25)
	if err != nil {
		t.Fatalf("requestFingerprint() changed error = %v", err)
	}
	if first == third {
		t.Fatalf("fingerprint did not change when request changed: %q", first)
	}
	changedLimit, err := requestFingerprint(builder, 26)
	if err != nil {
		t.Fatalf("requestFingerprint() limit error = %v", err)
	}
	if first == changedLimit {
		t.Fatalf("fingerprint did not include limit")
	}
}

func TestValidationWarningsExposeStableCodes(t *testing.T) {
	warnings := validationWarnings(Builder{}, CompiledQuery{Limit: 1001})
	if len(warnings) != 2 {
		t.Fatalf("validationWarnings() len = %d, want 2", len(warnings))
	}
	seen := map[string]bool{}
	for _, warning := range warnings {
		seen[warning.Code] = true
		if strings.TrimSpace(warning.Message) == "" {
			t.Errorf("warning %q has empty message", warning.Code)
		}
	}
	if !seen["NO_SELECTED_COLUMNS"] || !seen["PREVIEW_LIMIT_CAPPED"] {
		t.Fatalf("warning codes = %#v", seen)
	}
	cloned := cloneValidationWarnings(warnings)
	cloned[1].Details["limit"] = 1
	if warnings[1].Details["limit"] == 1 {
		t.Fatal("cloneValidationWarnings aliased Details")
	}
}

func TestValidateCompilesWithoutExecutingRows(t *testing.T) {
	executed := false
	service := NewService(ServiceConfig{
		DiscoverFields: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return []catalog.PopulatedField{}, nil
		},
		DiscoverReferences: func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			return []catalog.PopulatedReference{}, nil
		},
		ExecuteRows: func(context.Context, ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error {
			executed = true
			return nil
		},
	})
	result, err := service.Validate(context.Background(), ValidateRequest{
		Builder: Builder{Project: "P1", RootResourceType: "Patient"},
		Limit:   25,
	})
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
}
