package generation

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReturnsPreflightReportBeforeOpeningArango(t *testing.T) {
	dir := t.TempDir()
	writePreflightFixture(t, dir, "Unknown.ndjson", `{"resourceType":"Unknown"}`+"\n")
	wantIdentity, err := ingest.LoadSchemaSnapshot()
	if err != nil {
		t.Fatalf("load expected schema identity: %v", err)
	}

	var events []string
	var preflightStart map[string]any

	summary, err := Load(context.Background(), LoadOptions{
		MetaDir: dir,

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
	var preflightErr *ingest.PreflightError
	if !errors.As(err, &preflightErr) {
		t.Fatalf("Load() error = %T %v, want ingest.PreflightError", err, err)
	}
	if summary.Preflight.Valid() || len(summary.Preflight.Issues) != 1 {
		t.Fatalf("Load() preflight summary = %+v", summary.Preflight)
	}
	if summary.SchemaIdentity == nil {
		t.Fatal("Load() schema identity is nil after schema-backed preflight")
	}
	if got, want := summary.SchemaIdentity.SchemaSHA256, wantIdentity.SchemaSHA256; got != want {
		t.Errorf("Load() schema digest = %q, want %q", got, want)
	}
	if preflightStart == nil {
		t.Fatal("missing go_preflight_start event")
	}
	if got, want := preflightStart["schemaSha256"], wantIdentity.SchemaSHA256; got != want {
		t.Errorf("go_preflight_start schemaSha256 = %#v, want %#v", got, want)
	}
	if got, want := preflightStart["generatedRootCount"], len(wantIdentity.GeneratedResourceTypes); got != want {
		t.Errorf("go_preflight_start generatedRootCount = %#v, want %#v", got, want)
	}
	for _, event := range events {
		if event == "go_backend_connect_start" || event == "go_backend_connect_complete" || event == "go_bootstrap_start" {
			t.Fatalf("Load() emitted %q after an invalid staged input; events = %v", event, events)
		}
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
