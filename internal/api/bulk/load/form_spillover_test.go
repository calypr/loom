package load

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

const spilloverFileBytes = 20 * 1024 * 1024

const measurementReps = 3

type spilloverRunner struct {
	project       string
	stagedAt      []int
	spillsAtLoad  []string
	stagedBytesAt int64
	spillBytesAt  int64
	otherBytesAt  int64
}

func (r *spilloverRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.project = req.Project
	r.stagedAt = nil
	r.spillsAtLoad = nil
	r.stagedBytesAt = 0
	r.spillBytesAt = 0
	r.otherBytesAt = 0
	for _, name := range []string{"Patient.ndjson", "Specimen.ndjson"} {
		data, err := os.ReadFile(filepath.Join(req.StagedDir, name))
		if err != nil {
			return ingest.LoadSummary{}, fmt.Errorf("staged %s unreadable at load time: %w", name, err)
		}
		r.stagedAt = append(r.stagedAt, len(data))
		r.stagedBytesAt += int64(len(data))
	}
	snapshot, err := snapshotTempRoot(req.StagedDir)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	r.spillsAtLoad = snapshot.spillNames
	r.spillBytesAt = snapshot.spillBytes
	r.otherBytesAt = snapshot.otherBytes
	return ingest.LoadSummary{Files: 2}, nil
}

type tempRootSnapshot struct {
	spillBytes int64
	spillNames []string
	otherBytes int64
}

func snapshotTempRoot(stagingDir string) (tempRootSnapshot, error) {
	var snapshot tempRootSnapshot
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return snapshot, fmt.Errorf("read temp root: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == filepath.Base(stagingDir) {
			continue
		}
		size, err := dirBytes(filepath.Join(root, entry.Name()))
		if err != nil {
			return snapshot, fmt.Errorf("measure %s: %w", entry.Name(), err)
		}
		if strings.HasPrefix(entry.Name(), "multipart") {
			snapshot.spillBytes += size
			snapshot.spillNames = append(snapshot.spillNames, entry.Name())
			continue
		}
		snapshot.otherBytes += size
	}
	return snapshot, nil
}

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

type tempRootSampler struct {
	root   string
	peak   atomic.Int64
	done   chan struct{}
	closed sync.Once
}

func newTempRootSampler(root string) *tempRootSampler {
	sampler := &tempRootSampler{root: root, done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(200 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-sampler.done:
				return
			case <-ticker.C:
				if total, err := dirBytes(sampler.root); err == nil && total > sampler.peak.Load() {
					sampler.peak.Store(total)
				}
			}
		}
	}()
	return sampler
}

func (s *tempRootSampler) stop() int64 {
	s.closed.Do(func() { close(s.done) })
	return s.peak.Load()
}

type stagingRun struct {
	path        string
	rep         int
	payload     int64
	stagedBytes int64
	spillBytes  int64
	spillFiles  int
	otherBytes  int64
	peakBytes   int64
	elapsed     time.Duration
}

func (r stagingRun) String() string {
	return fmt.Sprintf("path=%s rep=%d payload=%d staged=%d spill=%d(%d files) other=%d peak=%d elapsed_ms=%.1f",
		r.path, r.rep, r.payload, r.stagedBytes, r.spillBytes, r.spillFiles, r.otherBytes, r.peakBytes,
		float64(r.elapsed.Microseconds())/1000)
}

func measureStagingRun(t *testing.T, path string, rep int, baseline bool) stagingRun {
	t.Helper()
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	if resolved := os.TempDir(); resolved != tempDir {
		t.Fatalf("TMPDIR not in effect: os.TempDir()=%s tempDir=%s", resolved, tempDir)
	}
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)
	payload := int64(len(body))

	sampler := newTempRootSampler(tempDir)
	start := time.Now()
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var err error
	if baseline {
		_, err = handler.createDatasetGenerationBaseline(context.Background(), "P1", "generation-1", reader, nil)
	} else {
		_, err = handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", reader, nil)
	}
	elapsed := time.Since(start)
	peak := sampler.stop()

	if err != nil {
		t.Fatalf("%s rep %d: load: %v", path, rep, err)
	}
	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("%s rep %d: temp files orphaned after load: %v", path, rep, orphans)
	}
	if leftovers := listUploadTempFiles(t, tempDir); len(leftovers) != 0 {
		t.Fatalf("%s rep %d: temporary upload files left behind: %v", path, rep, leftovers)
	}
	return stagingRun{
		path:        path,
		rep:         rep,
		payload:     payload,
		stagedBytes: runner.stagedBytesAt,
		spillBytes:  runner.spillBytesAt,
		spillFiles:  len(runner.spillsAtLoad),
		otherBytes:  runner.otherBytesAt,
		peakBytes:   peak,
		elapsed:     elapsed,
	}
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

func listUploadTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	var leftovers []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasPrefix(name, "loom-generation-") || strings.HasPrefix(name, "multipart") {
			leftovers = append(leftovers, name)
		}
	}
	return leftovers
}

func countOrphanMultipartFiles(t *testing.T, dir string) []string {
	t.Helper()
	var orphans []string
	for _, name := range listUploadTempFiles(t, dir) {
		if strings.HasPrefix(name, "multipart") {
			orphans = append(orphans, name)
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

	if runner.project != "P1" || len(runner.stagedAt) != 2 {
		t.Fatalf("loader view of the bundle: project=%s staged=%v", runner.project, runner.stagedAt)
	}
	for i, size := range runner.stagedAt {
		if size != spilloverFileBytes {
			t.Fatalf("staged file %d size=%d want=%d", i, size, spilloverFileBytes)
		}
	}

	if runner.spillsAtLoad != nil {
		t.Fatalf("duplicate multipart spill present while the loader ran: %v", runner.spillsAtLoad)
	}
	if runner.stagedBytesAt != 2*spilloverFileBytes {
		t.Fatalf("staged bytes at load time = %d, want %d", runner.stagedBytesAt, 2*spilloverFileBytes)
	}
	if runner.spillBytesAt != 0 || runner.otherBytesAt != 0 {
		t.Fatalf("temporary bytes outside the staged bundle while the loader ran = spill %d other %d, want 0 0",
			runner.spillBytesAt, runner.otherBytesAt)
	}
	t.Logf("SPLILLOVER_DISPOSED_ORPHANS=0 STAGED_AT_LOAD_TIME=2 DUPLICATE_TEMP_BYTES=0 loaderProject=%s", runner.project)
}

func TestCreateDatasetGenerationTemporaryBytesBeforeAfter(t *testing.T) {
	var before, after []stagingRun
	for rep := 1; rep <= measurementReps; rep++ {
		if rep%2 == 0 {
			after = append(after, measureStagingRun(t, "streaming", rep, false))
		}
		before = append(before, measureStagingRun(t, "readform-copy", rep, true))
		if rep%2 == 1 {
			after = append(after, measureStagingRun(t, "streaming", rep, false))
		}
	}

	payload := int64(2 * spilloverFileBytes)
	for _, run := range before {
		t.Logf("BEFORE %s", run)
		if run.stagedBytes != payload {
			t.Fatalf("shipped path staged %d bytes, want %d", run.stagedBytes, payload)
		}
		if run.spillFiles == 0 || run.spillBytes == 0 {
			t.Fatalf("shipped path produced no duplicate spill on a payload above the ReadForm budget; the measurement no longer exercises the behavior it compares (%s)", run)
		}
		if run.peakBytes <= payload {
			t.Fatalf("shipped path peak temp bytes %d does not exceed the %d byte payload; the sampler missed the duplicate (%s)", run.peakBytes, payload, run)
		}
	}
	for _, run := range after {
		t.Logf("AFTER  %s", run)
		if run.stagedBytes != payload {
			t.Fatalf("streaming path staged %d bytes, want %d", run.stagedBytes, payload)
		}
		if run.spillFiles != 0 || run.spillBytes != 0 {
			t.Fatalf("streaming path created a duplicate multipart spill: %s", run)
		}
		if run.otherBytes != 0 {
			t.Fatalf("streaming path left %d other temporary bytes behind: %s", run.otherBytes, run)
		}
		if run.peakBytes != payload {
			t.Fatalf("streaming path peak temp bytes = %d, want exactly the %d byte payload (a second copy would exceed it)", run.peakBytes, payload)
		}
	}

	t.Logf("MEASUREMENT payload_bytes=%d reps=%d temp_fs=%s", payload, measurementReps, tempFSType())
	t.Logf("MEASUREMENT BEFORE peak_bytes=%v", peakSeries(before))
	t.Logf("MEASUREMENT AFTER  peak_bytes=%v", peakSeries(after))
	t.Logf("MEASUREMENT BEFORE spill_bytes=%v", spillSeries(before))
	t.Logf("MEASUREMENT AFTER  spill_bytes=%v", spillSeries(after))
	t.Logf("MEASUREMENT elapsed_ms BEFORE median=%.1f AFTER median=%.1f (recorded, not asserted)",
		medianMillis(before), medianMillis(after))
}

func peakSeries(runs []stagingRun) []int64 {
	values := make([]int64, 0, len(runs))
	for _, run := range runs {
		values = append(values, run.peakBytes)
	}
	return values
}

func spillSeries(runs []stagingRun) []int64 {
	values := make([]int64, 0, len(runs))
	for _, run := range runs {
		values = append(values, run.spillBytes)
	}
	return values
}

func medianMillis(runs []stagingRun) float64 {
	values := make([]float64, 0, len(runs))
	for _, run := range runs {
		values = append(values, float64(run.elapsed.Microseconds())/1000)
	}
	sort.Float64s(values)
	if len(values) == 0 {
		return 0
	}
	return values[len(values)/2]
}

func tempFSType() string {
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		root := os.TempDir()
		best, bestType := 0, "unknown"
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			if strings.HasPrefix(root, fields[1]) && len(fields[1]) > best {
				best, bestType = len(fields[1]), fields[2]
			}
		}
		return bestType
	}
	return "unknown"
}

func TestCreateDatasetGenerationSpilloverDisposedOnValidationRejection(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)

	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "  ", multipart.NewReader(bytes.NewReader(body), boundary), nil); err == nil {
		t.Fatal("expected validation rejection for blank generation")
	}

	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("multipart spillover temp files orphaned after validation rejection: %v", orphans)
	}
	if leftovers := listUploadTempFiles(t, tempDir); len(leftovers) != 0 {
		t.Fatalf("staged files left behind after validation rejection: %v", leftovers)
	}
}
