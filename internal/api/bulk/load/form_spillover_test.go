package load

// Durable regression for the bulk-load multipart spillover leak (discovery
// round 73) and for the single-write staging contract that replaced the
// intermediate copy.
//
// The handler used to parse the request with multipart.Reader.ReadForm(32<<20):
// every byte beyond the 32 MiB in-memory budget was spooled to a temporary
// file under os.TempDir, and the shipped handler disposed only its own staged
// copy (defer os.RemoveAll(stagedDir)) and never called form.RemoveAll, so
// every dataset-generation load larger than the budget left full-size copies
// in /tmp for the container's lifetime: unbounded disk growth under normal
// authenticated use. That leak was repaired by adding the disposal (commit
// d2ff3d037) and then removed entirely: CreateDatasetGeneration now parses with
// stageGenerationBundle, which streams each file part once into the loader's
// own staging directory, so no multipart-* spill file is ever created.
//
// The assertions are behavior-level and cover both shapes:
//   - after a load whose payload provably exceeds the old spillover budget
//     (two 20 MiB .ndjson files through the real handler, with the loader
//     faked), TMPDIR holds no orphan multipart temp files and the staged
//     loader copies survive intact with their content;
//   - at load time the staged directory is the only copy of the payload on
//     disk — no full-size duplicate multipart spill exists while the loader
//     runs, which is what a second write would necessarily leave behind;
//   - the error-path companion covers the validation-rejection returns, which
//     parsed first and returned before any staging cleanup existed. These
//     FAILED on the pre-fix handler — each run left at least one whole 20 MiB
//     orphan (empirically exactly one: the 32 MiB budget keeps one part fully
//     in memory) — and PASS with the repair.

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
// strictly greater than the handler's historical 32<<20 ReadForm budget, so
// the old path spooled at least one whole file to disk regardless of how the
// budget was apportioned across parts.
const spilloverFileBytes = 20 * 1024 * 1024

// spilloverRunner verifies staged integrity at load time — after the handler
// returns, its cleanup removes the staged directory by design — and records
// what else occupied the temp root while the loader ran.
type spilloverRunner struct {
	project       string
	stagedAt      []int
	spillsAtLoad  []string
	stagedBytesAt int64
	otherBytesAt  int64
}

func (r *spilloverRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.project = req.Project
	r.stagedAt = nil
	r.spillsAtLoad = nil
	r.stagedBytesAt = 0
	r.otherBytesAt = 0
	for _, name := range []string{"Patient.ndjson", "Specimen.ndjson"} {
		data, err := os.ReadFile(filepath.Join(req.StagedDir, name))
		if err != nil {
			return ingest.LoadSummary{}, fmt.Errorf("staged %s unreadable at load time: %w", name, err)
		}
		r.stagedAt = append(r.stagedAt, len(data))
		r.stagedBytesAt += int64(len(data))
	}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return ingest.LoadSummary{}, fmt.Errorf("read temp root at load time: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		// The staged bundle is the one copy the loader is reading; everything
		// else in the temp root is a candidate duplicate.
		if name == filepath.Base(req.StagedDir) {
			continue
		}
		if strings.HasPrefix(name, "multipart") {
			r.spillsAtLoad = append(r.spillsAtLoad, name)
		}
		other, err := dirBytes(filepath.Join(os.TempDir(), name))
		if err != nil {
			return ingest.LoadSummary{}, fmt.Errorf("measure %s at load time: %w", name, err)
		}
		r.otherBytesAt += other
	}
	return ingest.LoadSummary{Files: 2}, nil
}

// dirBytes sums the sizes of every regular file at or below path.
func dirBytes(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
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
	// time — staging disposes of nothing the load consumes.
	if runner.project != "P1" || len(runner.stagedAt) != 2 {
		t.Fatalf("loader view of the bundle: project=%s staged=%v", runner.project, runner.stagedAt)
	}
	for i, size := range runner.stagedAt {
		if size != spilloverFileBytes {
			t.Fatalf("staged file %d size=%d want=%d", i, size, spilloverFileBytes)
		}
	}

	// Single-write proof: while the loader ran, the payload existed exactly
	// once on disk. The old path (ReadForm + io.Copy into the staging
	// directory) held the 40 MiB staged bundle AND a ~20 MiB multipart spill
	// file simultaneously, and the spill alone trips both assertions.
	if len(runner.spillsAtLoad) != 0 {
		t.Fatalf("duplicate multipart spill present while the loader ran: %v", runner.spillsAtLoad)
	}
	if runner.stagedBytesAt != 2*spilloverFileBytes {
		t.Fatalf("staged bytes at load time = %d, want %d", runner.stagedBytesAt, 2*spilloverFileBytes)
	}
	if runner.otherBytesAt != 0 {
		t.Fatalf("temporary bytes outside the staged bundle while the loader ran = %d, want 0", runner.otherBytesAt)
	}
	t.Logf("SPLILLOVER_DISPOSED_ORPHANS=0 STAGED_AT_LOAD_TIME=2 DUPLICATE_TEMP_BYTES=0 loaderProject=%s", runner.project)
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
