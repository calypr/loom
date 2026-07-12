package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/schemaidentity"
	arangostore "github.com/calypr/loom/internal/store/arango"

	"github.com/bmeg/jsonschemagraph/graph"
)

func TestPreflightFilesSelectsGeneratedAndGenericModesFromActiveSchema(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	dir := t.TempDir()
	patient := writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient"}`+"\n")
	diagnosticReport := writePreflightFixture(t, dir, "DiagnosticReport.ndjson", `{"resourceType":"DiagnosticReport"}`+"\n")

	report, err := PreflightFiles([]string{patient, diagnosticReport}, schema, 1)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !report.Valid() || len(report.Resources) != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if got := report.Resources[0]; got.ResourceType != "DiagnosticReport" || got.Mode != IngestionModeGeneric || !got.GraphSchemaSupported || got.GeneratedLoaderSupported || got.SampledRows != 1 {
		t.Fatalf("unexpected generic resource report: %+v", got)
	}
	if got := report.Resources[1]; got.ResourceType != "Patient" || got.Mode != IngestionModeGenerated || !got.GraphSchemaSupported || !got.GeneratedLoaderSupported || got.SampledRows != 1 {
		t.Fatalf("unexpected generated resource report: %+v", got)
	}
}

func TestPreflightFilesReportsAllStagedInputProblems(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
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
	for _, want := range []string{"invalid_json", "resource_type_mismatch", "unsupported_graph_schema_resource"} {
		if !codes[want] {
			t.Fatalf("issues missing %q: %+v", want, report.Issues)
		}
	}
	if len(preflightErr.Report.Issues) != len(report.Issues) {
		t.Fatalf("error report does not preserve issues: %+v", preflightErr.Report)
	}
}

func TestPreflightFilesBoundsPayloadSamplingPerFile(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
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

func TestLoadReturnsPreflightReportBeforeOpeningArango(t *testing.T) {
	dir := t.TempDir()
	writePreflightFixture(t, dir, "Unknown.ndjson", `{"resourceType":"Unknown"}`+"\n")
	schemaPath := repoPath(t, "schemas", "graph-fhir.json")
	wantIdentity, err := schemaidentity.Load(schemaPath)
	if err != nil {
		t.Fatalf("load expected schema identity: %v", err)
	}

	var events []string
	var preflightStart map[string]any

	summary, err := Load(context.Background(), LoadOptions{
		Schema:  schemaPath,
		MetaDir: dir,
		// A connection to this endpoint would fail. Receiving a structured
		// preflight error instead proves validation happens before backend open.
		ConnectionOptions: arangostore.ConnectionOptions{
			URL:      "http://127.0.0.1:1",
			Database: "preflight_must_not_connect",
		},
		EventSink: func(event string, fields map[string]any) {
			events = append(events, event)
			if event == "go_preflight_start" {
				preflightStart = fields
			}
		},
	})
	if err == nil {
		t.Fatal("expected preflight error")
	}
	var preflightErr *PreflightError
	if !errors.As(err, &preflightErr) {
		t.Fatalf("Load() error = %T %v, want PreflightError", err, err)
	}
	if summary.Preflight.Valid() || len(summary.Preflight.Issues) != 1 {
		t.Fatalf("Load() preflight summary = %+v", summary.Preflight)
	}
	if summary.SchemaIdentity == nil {
		t.Fatal("Load() schema identity is nil after schema-backed preflight")
	}
	if got, want := summary.SchemaIdentity.SchemaSHA256(), wantIdentity.SchemaSHA256(); got != want {
		t.Errorf("Load() schema digest = %q, want %q", got, want)
	}
	if got, want := summary.SchemaIdentity.GeneratedResourceTypes(), wantIdentity.GeneratedResourceTypes(); !reflect.DeepEqual(got, want) {
		t.Errorf("Load() generated roots = %#v, want %#v", got, want)
	}
	if preflightStart == nil {
		t.Fatal("missing go_preflight_start event")
	}
	if got, want := preflightStart["schemaSha256"], wantIdentity.SchemaSHA256(); got != want {
		t.Errorf("go_preflight_start schemaSha256 = %#v, want %#v", got, want)
	}
	if got, want := preflightStart["generatedRootCount"], len(wantIdentity.GeneratedResourceTypes()); got != want {
		t.Errorf("go_preflight_start generatedRootCount = %#v, want %#v", got, want)
	}
	for _, event := range events {
		if event == "go_backend_connect_start" || event == "go_backend_connect_complete" || event == "go_bootstrap_start" {
			t.Fatalf("Load() emitted %q after an invalid staged input; events = %v", event, events)
		}
	}
}

func TestLoadMissingSchemaReturnsNoSchemaIdentity(t *testing.T) {
	summary, err := Load(context.Background(), LoadOptions{
		Schema:  filepath.Join(t.TempDir(), "missing-graph.json"),
		MetaDir: t.TempDir(),
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() missing schema error = %v, want os.ErrNotExist", err)
	}
	if summary.SchemaIdentity != nil {
		t.Fatalf("Load() missing schema identity = %#v, want nil", summary.SchemaIdentity)
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
