package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightFilesReportsGeneratedResourceSupport(t *testing.T) {
	schema := LoadRuntimeFHIRSchema()
	dir := t.TempDir()
	patient := writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient"}`+"\n")
	unknown := writePreflightFixture(t, dir, "Unknown.ndjson", `{"resourceType":"Unknown"}`+"\n")

	report, err := PreflightFiles([]string{patient, unknown}, schema, 1)
	if err == nil {
		t.Fatal("expected unsupported resource error")
	}
	if report.Valid() || len(report.Resources) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if got := report.Resources[0]; got.ResourceType != "Patient" || got.Mode != IngestionModeGenerated || !got.FHIRSchemaSupported || !got.GeneratedLoaderSupported || got.SampledRows != 1 {
		t.Fatalf("unexpected Patient report: %+v", got)
	}
	if got := report.Resources[1]; got.ResourceType != "Unknown" || got.Mode != IngestionModeUnsupported || got.FHIRSchemaSupported || got.GeneratedLoaderSupported || got.SampledRows != 1 {
		t.Fatalf("unexpected Unknown report: %+v", got)
	}
}

func TestPreflightUsesGeneratedResourceRegistry(t *testing.T) {
	schema := LoadRuntimeFHIRSchema()
	if !schema.HasClass("Observation") || schema.HasClass("MadeUpResource") {
		t.Fatalf("generated resource registry has unexpected membership")
	}
	observation := writePreflightFixture(t, t.TempDir(), "Observation.ndjson", `{"resourceType":"Observation"}`+"\n")
	report, err := PreflightFiles([]string{observation}, schema, 1)
	if err != nil {
		t.Fatalf("preflight generated Observation: %v", err)
	}
	if got := report.Resources[0]; got.ResourceType != "Observation" || got.Mode != IngestionModeGenerated || !got.FHIRSchemaSupported || !got.GeneratedLoaderSupported {
		t.Fatalf("unexpected Observation report: %+v", got)
	}
}

func TestPreflightFilesReportsAllStagedInputProblems(t *testing.T) {
	schema := LoadRuntimeFHIRSchema()
	dir := t.TempDir()
	patient := writePreflightFixture(t, dir, "Patient.ndjson", "not-json\n")
	specimen := writePreflightFixture(t, dir, "Specimen.ndjson", `{"resourceType":"Patient"}`+"\n")
	unknown := writePreflightFixture(t, dir, "Unknown.ndjson", `{"resourceType":"Unknown"}`+"\n")

	report, err := PreflightFiles([]string{patient, specimen, unknown}, schema, 10)
	if err == nil {
		t.Fatal("expected preflight error")
	}
	var preflightErr *PreflightError
	if !errors.As(err, &preflightErr) {
		t.Fatalf("expected PreflightError, got %T: %v", err, err)
	}
	if report.Valid() || len(report.Issues) != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	for _, want := range []string{"invalid_json", "resource_type_mismatch", "unsupported_fhir_resource"} {
		if !codes[want] {
			t.Fatalf("issues missing %q: %+v", want, report.Issues)
		}
	}
	if len(preflightErr.Report.Issues) != len(report.Issues) {
		t.Fatalf("error report does not preserve issues: %+v", preflightErr.Report)
	}
}

func TestPreflightFilesBoundsPayloadSamplingPerFile(t *testing.T) {
	schema := LoadRuntimeFHIRSchema()
	dir := t.TempDir()
	patient := writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient"}`+"\n"+`{"resourceType":"Specimen"}`+"\n")

	report, err := PreflightFiles([]string{patient}, schema, 1)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if got := report.Resources[0].SampledRows; got != 1 {
		t.Fatalf("sampled rows = %d, want 1", got)
	}
}

func writePreflightFixture(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
	return path
}
