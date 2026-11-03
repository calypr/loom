package load

// Durable regression for the bulk-load multipart spillover leak (discovery
// round 73). Handler.CreateDatasetGeneration parses the request with
// multipart.Reader.ReadForm(32<<20): every byte beyond the 32 MiB in-memory
// budget is spooled to a temporary file under os.TempDir, and the Go contract
// requires multipart.Form.RemoveAll to dispose of them. The shipped handler
// disposed only its own staged copy (defer os.RemoveAll(stagedDir)) and never
// called form.RemoveAll, so every dataset-generation load larger than the
// budget left full-size copies in /tmp for the container's lifetime:
// unbounded disk growth under normal authenticated use.
//
// The assertions are behavior-level: after a load whose payload provably
// exceeds the spillover budget (two 20 MiB .ndjson files through the real
// handler, with the loader faked), TMPDIR holds no orphan multipart temp
// files, and the staged loader copies survive intact with their content.
// The error-path companion covers the validation-rejection returns, which
// spill first and return before any staging cleanup exists. These FAIL on
// the pre-fix handler — each run leaves at least one whole 20 MiB orphan
// (empirically exactly one: the 32 MiB budget keeps one part fully in
// memory) — and PASS
// with the repair.

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

// spilloverFileBytes is 20 MiB per file; two files total 40 MiB, which is
// strictly greater than the handler's hard-coded 32<<20 ReadForm budget, so
// Go's ReadForm spools at least one whole file to disk regardless of how the
// budget is apportioned across parts.
const spilloverFileBytes = 20 * 1024 * 1024

// spilloverRunner verifies staged integrity at load time — after the handler
// returns, its pre-existing defer removes the staged directory by design.
type spilloverRunner struct {
	project  string
	stagedAt []int
}

func (r *spilloverRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.project = req.Project
	r.stagedAt = nil
	for _, name := range []string{"Patient.ndjson", "Specimen.ndjson"} {
		data, err := os.ReadFile(filepath.Join(req.StagedDir, name))
		if err != nil {
			return ingest.LoadSummary{}, fmt.Errorf("staged %s unreadable at load time: %w", name, err)
		}
		r.stagedAt = append(r.stagedAt, len(data))
	}
	return ingest.LoadSummary{Files: 2}, nil
}

func spilloverHandler(t *testing.T, runner *spilloverRunner) *Handler {
	t.Helper()
	service, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func spilloverMultipart(t *testing.T, deferActivation bool) ([]byte, string) {
	t.Helper()
	chunk := bytes.Repeat([]byte("x"), spilloverFileBytes)
	return generationMultipart(t, deferActivation, map[string][]byte{
		"Patient.ndjson":  chunk,
		"Specimen.ndjson": chunk,
	})
}

func countOrphanMultipartFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	var orphans []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "multipart") {
			orphans = append(orphans, entry.Name())
		}
	}
	return orphans
}

func TestCreateDatasetGenerationLeaksNoSpilloverTempFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)

	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil); err != nil {
		t.Fatalf("load: %v", err)
	}

	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("multipart spillover temp files orphaned in TMPDIR after load: %v", orphans)
	}

	// Positive companion: the loader saw both staged copies intact at load
	// time — the spillover disposal removes only ReadForm temp copies, not
	// the staged bundle the load consumes.
	if runner.project != "P1" || len(runner.stagedAt) != 2 {
		t.Fatalf("loader view of the bundle: project=%s staged=%v", runner.project, runner.stagedAt)
	}
	for i, size := range runner.stagedAt {
		if size != spilloverFileBytes {
			t.Fatalf("staged file %d size=%d want=%d", i, size, spilloverFileBytes)
		}
	}
	t.Logf("SPLILLOVER_DISPOSED_ORPHANS=0 STAGED_AT_LOAD_TIME=2 loaderProject=%s", runner.project)
}

func TestCreateDatasetGenerationSpilloverDisposedOnValidationRejection(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)

	// Empty generation: the handler reads (and spills) the form before the
	// validation return, so this path leaked even earlier than the success
	// path.
	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "  ", multipart.NewReader(bytes.NewReader(body), boundary), nil); err == nil {
		t.Fatal("expected validation rejection for blank generation")
	}

	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("multipart spillover temp files orphaned after validation rejection: %v", orphans)
	}
}
