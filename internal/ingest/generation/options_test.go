package generation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	publication "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/ingest/bootstrap"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/ingest/source"
	"github.com/calypr/loom/internal/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

const failLegPrefix = "loom_verify_failleg_"

func failLegEndpoint(t *testing.T) string {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the generation FAILED-cleanup guarantee e2e")
	}
	return endpoint
}

func failLegPatientRow(index int) string {
	return fmt.Sprintf(`{"resourceType":"Patient","id":"leg-%03d","gender":"female"}`, index)
}

func TestGenerationLoadFailureMarksManifestFailedAndBlocksActivation(t *testing.T) {
	endpoint := failLegEndpoint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	t.Run("pre_staged_fault", func(t *testing.T) {
		database := failLegPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
		t.Cleanup(func() { failLegDropDatabase(t, endpoint, database) })
		metaDir := t.TempDir()
		rows := make([]string, 0, 26)
		for i := 0; i < 25; i++ {
			rows = append(rows, failLegPatientRow(i))
		}

		rows = append(rows, `{oops`)
		for i := 25; i < 30; i++ {
			rows = append(rows, failLegPatientRow(i))
		}
		if err := os.WriteFile(filepath.Join(metaDir, "Patient.ndjson"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		project := "FAILLEG_FAULT"
		ref, err := publication.NewRef(project, "gen-fault")
		if err != nil {
			t.Fatalf("ref: %v", err)
		}
		_, loadErr := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			MetaDir:           metaDir,
			Project:           project,
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		})
		if loadErr == nil {
			t.Fatalf("PIN 1 pre-fault: expected GenerationLoadIncompleteError for a row beyond the preflight sample, got nil")
		}
		var incomplete *GenerationLoadIncompleteError
		if !errors.As(loadErr, &incomplete) {
			t.Fatalf("PIN 1 fault class: expected *GenerationLoadIncompleteError, got %T: %v", loadErr, loadErr)
		}
		t.Logf("FAILLEG fault load | %v", loadErr)

		client, err := arangostore.Open(ctx, endpoint, database)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = client.Close(context.Background()) }()
		store, err := publicationarango.New(client)
		if err != nil {
			t.Fatalf("lifecycle store: %v", err)
		}

		manifest, err := store.ReadManifest(ctx, ref)
		if err != nil {
			t.Fatalf("PIN 1 readback: ReadManifest after failed load: %v", err)
		}
		if manifest.State != publication.StateFailed {
			t.Errorf("PIN 1 | failed load left manifest in state %q, want %q (deferred cleanup leg must reach the store; ReadManifest is the store-visible readback)", manifest.State, publication.StateFailed)
		}

		if err := store.Activate(ctx, manifest); !errors.Is(err, publication.ErrGenerationNotReady) {
			t.Errorf("PIN 2 | Activate(failed generation) returned %v, want ErrGenerationNotReady", err)
		}
		if active, err := store.ResolveActiveManifest(ctx, project); !errors.Is(err, publication.ErrNoActiveGeneration) {
			t.Errorf("PIN 3 | failed generation reached active pointer: active=%+v err=%v", active, err)
		}

		activeKey, pointerFound, err := failLegReadActivePointer(ctx, client, project)
		if err != nil {
			t.Fatalf("PIN 3 raw active read: %v", err)
		}
		if pointerFound && strings.TrimSpace(activeKey) != "" {
			t.Errorf("PIN 3 | lifecycle active document points at manifest %q after failed load, want no pointer", activeKey)
		}

		if _, err := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			MetaDir:           metaDir,
			Project:           project,
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		}); !errors.Is(err, publicationarango.ErrManifestAlreadyExists) {

			t.Errorf("PIN 4 | re-load of failed generation returned %v, want ErrManifestAlreadyExists", err)
		}

		buildState, buildDiagnostic, buildCount, found, err := failLegReadBuildState(ctx, client, semantic.BuildCollection, project, "gen-fault")
		if err != nil {
			t.Fatalf("PIN 5 inventory readback: %v", err)
		}
		if !found {
			t.Fatalf("PIN 5 | no semantic inventory build document found for the failed generation (Begin ran before the fault; the pairing deferred must mark it failed)")
		}
		if buildCount != 1 {
			t.Errorf("PIN 5 | %d build documents for the failed generation, want exactly 1 (a stale duplicate can mask the paired failure by scan order)", buildCount)
		}
		if buildState != string(semantic.Failed) {
			t.Errorf("PIN 5 | inventory build state %q, want %q", buildState, semantic.Failed)
		}
		if !strings.Contains(buildDiagnostic, "is incomplete") || !strings.Contains(buildDiagnostic, "FAILLEG_FAULT/gen-fault") {
			t.Errorf("PIN 5b | inventory build diagnostics %q, want the specific load failure propagated (contains %q and the dataset ref)", buildDiagnostic, "is incomplete")
		}
	})

	t.Run("positive_staged_control", func(t *testing.T) {
		database := failLegPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
		t.Cleanup(func() { failLegDropDatabase(t, endpoint, database) })
		metaDir := t.TempDir()
		rows := make([]string, 0, 3)
		for i := 0; i < 3; i++ {
			rows = append(rows, failLegPatientRow(i))
		}
		if err := os.WriteFile(filepath.Join(metaDir, "Patient.ndjson"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		project := "FAILLEG_OK"
		ref, err := publication.NewRef(project, "gen-clean")
		if err != nil {
			t.Fatalf("ref: %v", err)
		}
		summary, err := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			MetaDir:           metaDir,
			Project:           project,
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		})
		if err != nil {
			t.Fatalf("positive control load failed: %v", err)
		}
		client, err := arangostore.Open(ctx, endpoint, database)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = client.Close(context.Background()) }()
		store, err := publicationarango.New(client)
		if err != nil {
			t.Fatalf("lifecycle store: %v", err)
		}
		active, err := store.ResolveActiveManifest(ctx, project)
		if err != nil {
			t.Fatalf("positive control: ResolveActiveManifest: %v", err)
		}
		if !active.IsStaged() {
			t.Errorf("positive control | active manifest state %q is not staged", active.State)
		}
		count, err := client.CollectionCount(ctx, "Patient")
		if err != nil {
			t.Fatalf("positive control count: %v", err)
		}
		if count != 3 || summary.VerticesInserted != 3 {
			t.Errorf("positive control | acked=%d persisted=%d, want 3/3", summary.VerticesInserted, count)
		}
	})

	t.Cleanup(func() { failLegAssertNoResidue(t, endpoint) })
}

func failLegReadActivePointer(ctx context.Context, client *arangostore.Client, project string) (string, bool, error) {
	exists, err := client.CollectionExists(ctx, publicationarango.LifecycleCollection)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, nil
	}
	var manifestKey string
	var found bool
	err = client.QueryRows(ctx, `FOR a IN @@lifecycle FILTER a._key == @active_key AND a.recordType == "active_generation" RETURN {manifestKey: a.manifestKey}`, 1, map[string]any{
		"@lifecycle": publicationarango.LifecycleCollection,
		"active_key": publicationarango.ActiveDocumentKey(project),
	}, func(row map[string]any) error {
		value, _ := row["manifestKey"].(string)
		manifestKey, found = value, true
		return nil
	})
	return manifestKey, found, err
}

func failLegReadBuildState(ctx context.Context, client *arangostore.Client, collection, project, generation string) (string, string, int, bool, error) {
	exists, err := client.CollectionExists(ctx, collection)
	if err != nil {
		return "", "", 0, false, err
	}
	if !exists {
		return "", "", 0, false, nil
	}
	bindVars := map[string]any{
		"@collection": collection,
		"project":     project,
		"generation":  generation,
	}
	var state string
	var diagnostic string
	var found bool
	var count int
	err = client.QueryRows(ctx, `FOR b IN @@collection FILTER b.project == @project AND b.dataset_generation == @generation RETURN {state: b.state, diagnostics: b.diagnostics}`, 10, bindVars, func(row map[string]any) error {
		count++
		value, _ := row["state"].(string)
		state, found = value, true
		if list, ok := row["diagnostics"].([]any); ok && len(list) > 0 {
			diagnostic, _ = list[0].(string)
		}
		return nil
	})
	return state, diagnostic, count, found, err
}

func failLegDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	if !strings.HasPrefix(database, failLegPrefix) {
		t.Errorf("FAILLEG refusal to drop %s outside %s", database, failLegPrefix)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Logf("FAILLEG_CLEANUP_WARN: build drop: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("FAILLEG_CLEANUP_WARN: drop %s: %v", database, err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		t.Logf("FAILLEG_CLEANUP_WARN: drop %s status %d", database, response.StatusCode)
	}
}

func failLegAssertNoResidue(t *testing.T, endpoint string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/_api/database", nil)
	if err != nil {
		t.Errorf("FAILLEG_RESIDUE_WARN: build list: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("FAILLEG_RESIDUE_WARN: list databases: %v", err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	var payload struct {
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Errorf("FAILLEG_RESIDUE_WARN: decode list: %v", err)
		return
	}
	for _, db := range payload.Result {
		if strings.HasPrefix(db, failLegPrefix) {
			t.Errorf("FAILLEG residue verification database left behind: %s", db)
		}
	}
}

func TestRunGenerationFilesSharesWorkerAndWriterBudgets(t *testing.T) {
	directory := t.TempDir()
	files := make([]string, 9)
	for index := range files {
		path := filepath.Join(directory, string(rune('a'+index))+".ndjson")
		if err := os.WriteFile(path, make([]byte, (len(files)-index)*16), 0o600); err != nil {
			t.Fatal(err)
		}
		files[index] = path
	}
	var activeFiles int64
	var maximumFiles int64
	var activeWorkers int64
	var maximumWorkers int64
	var activeWriters int64
	var maximumWriters int64
	updateMaximum := func(counter *int64, value int64) {
		for {
			current := atomic.LoadInt64(counter)
			if value <= current || atomic.CompareAndSwapInt64(counter, current, value) {
				return
			}
		}
	}
	opts := normalizeLoadOptions(LoadOptions{WorkerCount: 8, WriterCount: 8, FileConcurrency: 3})
	err := runGenerationFiles(context.Background(), opts, files, func(ctx context.Context, fileOpts LoadOptions, _ int, _ string) error {
		filesNow := atomic.AddInt64(&activeFiles, 1)
		updateMaximum(&maximumFiles, filesNow)
		defer atomic.AddInt64(&activeFiles, -1)
		var workers sync.WaitGroup
		for worker := 0; worker < fileOpts.WorkerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for row := 0; row < 2; row++ {
					select {
					case fileOpts.fileWorkerBudget <- struct{}{}:
					case <-ctx.Done():
						return
					}
					workersNow := atomic.AddInt64(&activeWorkers, 1)
					updateMaximum(&maximumWorkers, workersNow)
					time.Sleep(3 * time.Millisecond)
					atomic.AddInt64(&activeWorkers, -1)
					<-fileOpts.fileWorkerBudget
				}
			}()
		}
		workers.Wait()
		var writers sync.WaitGroup
		for writer := 0; writer < fileOpts.WriterCount; writer++ {
			writers.Add(1)
			go func() {
				defer writers.Done()
				select {
				case fileOpts.fileWriterBudget <- struct{}{}:
				case <-ctx.Done():
					return
				}
				writersNow := atomic.AddInt64(&activeWriters, 1)
				updateMaximum(&maximumWriters, writersNow)
				time.Sleep(3 * time.Millisecond)
				atomic.AddInt64(&activeWriters, -1)
				<-fileOpts.fileWriterBudget
			}()
		}
		writers.Wait()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if maximumFiles != 3 || maximumWorkers != 8 || maximumWriters != 8 {
		t.Fatalf("concurrent files/workers/writers = %d/%d/%d, want 3/8/8", maximumFiles, maximumWorkers, maximumWriters)
	}
}

func TestRunGenerationFilesReusesIdleWorkerPermitsForTailFiles(t *testing.T) {
	directory := t.TempDir()
	files := []string{"short.ndjson", "long-one.ndjson", "long-two.ndjson", "tail.ndjson"}
	sizes := map[string]int{"short.ndjson": 10, "long-one.ndjson": 1000, "long-two.ndjson": 900, "tail.ndjson": 1}
	for index, name := range files {
		if err := os.WriteFile(filepath.Join(directory, name), make([]byte, sizes[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		files[index] = filepath.Join(directory, name)
	}
	var longOneActive int64
	var tailUsedWhileLongOneActive int64
	opts := normalizeLoadOptions(LoadOptions{WorkerCount: 8, WriterCount: 1, FileConcurrency: 3})
	err := runGenerationFiles(context.Background(), opts, files, func(ctx context.Context, fileOpts LoadOptions, _ int, file string) error {
		name := filepath.Base(file)
		if name == "long-one.ndjson" {
			atomic.StoreInt64(&longOneActive, 1)
			defer atomic.StoreInt64(&longOneActive, 0)
		}
		if name == "tail.ndjson" && atomic.LoadInt64(&longOneActive) == 1 {
			atomic.StoreInt64(&tailUsedWhileLongOneActive, 1)
		}
		iterations := 1
		if name == "long-one.ndjson" || name == "long-two.ndjson" {
			iterations = 30
		}
		var workers sync.WaitGroup
		for worker := 0; worker < fileOpts.WorkerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for row := 0; row < iterations; row++ {
					select {
					case fileOpts.fileWorkerBudget <- struct{}{}:
					case <-ctx.Done():
						return
					}
					time.Sleep(time.Millisecond)
					<-fileOpts.fileWorkerBudget
				}
			}()
		}
		workers.Wait()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&tailUsedWhileLongOneActive) != 1 {
		t.Fatal("tail file did not start while a long file still held row work")
	}
}

func TestRunGenerationFilesSerialModeKeepsInputOrder(t *testing.T) {
	directory := t.TempDir()
	files := []string{"small.ndjson", "large.ndjson", "middle.ndjson"}
	for _, file := range files {
		size := 1
		if file == "large.ndjson" {
			size = 100
		}
		if file == "middle.ndjson" {
			size = 10
		}
		if err := os.WriteFile(filepath.Join(directory, file), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	err := runGenerationFiles(context.Background(), LoadOptions{WorkerCount: 1, WriterCount: 1, FileConcurrency: 1}, []string{
		filepath.Join(directory, files[0]), filepath.Join(directory, files[1]), filepath.Join(directory, files[2]),
	}, func(_ context.Context, _ LoadOptions, _ int, file string) error {
		got = append(got, filepath.Base(file))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, files) {
		t.Fatalf("serial load order = %v, want original file order %v", got, files)
	}
}

func TestRunGenerationFilesFailFastCancelsSiblings(t *testing.T) {
	directory := t.TempDir()
	files := []string{"small.ndjson", "large.ndjson", "middle.ndjson", "tail.ndjson"}
	for _, file := range files {
		size := 10
		if file == "large.ndjson" {
			size = 100
		}
		if err := os.WriteFile(filepath.Join(directory, file), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := normalizeLoadOptions(LoadOptions{WorkerCount: 4, WriterCount: 4, FileConcurrency: 2, FailFast: true})
	wantErr := errors.New("file load failed")
	var started int64
	err := runGenerationFiles(context.Background(), opts, []string{
		filepath.Join(directory, files[0]), filepath.Join(directory, files[1]),
		filepath.Join(directory, files[2]), filepath.Join(directory, files[3]),
	}, func(ctx context.Context, _ LoadOptions, _ int, file string) error {
		atomic.AddInt64(&started, 1)
		if filepath.Base(file) == "large.ndjson" {
			return wantErr
		}
		<-ctx.Done()
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("scheduler error = %v, want %v", err, wantErr)
	}
	if started >= int64(len(files)) {
		t.Fatalf("started %d files after fail-fast error, want canceled queued work", started)
	}
}

func TestConcurrentGenerationMatchesSerialAgainstArango(t *testing.T) {
	endpoint := failLegEndpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	meta := writeConcurrentGenerationFixture(t)
	type generationSnapshot struct {
		summary LoadSummary
		docs    map[string][]map[string]any
	}
	load := func(concurrency int, incomplete bool) generationSnapshot {
		t.Helper()
		opts, ref := concurrentGenerationTestOptions(t, endpoint, meta, concurrency)
		summary, err := Load(ctx, opts)
		var incompleteErr *GenerationLoadIncompleteError
		if incomplete {
			if !errors.As(err, &incompleteErr) || summary.ValidationErrors != 2 || len(summary.RowErrors) != 2 {
				t.Fatalf("concurrency=%d incomplete load err=%v, summary=%+v", concurrency, err, summary)
			}
		} else if err != nil || summary.VerticesInserted != 4 || summary.EdgesInserted != 2 {
			t.Fatalf("concurrency=%d load err=%v, summary=%+v", concurrency, err, summary)
		}
		summary.StageSeconds = nil
		summary.BatchCounts = nil
		client, err := arangostore.Open(ctx, endpoint, opts.Database)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close(context.Background()) }()
		docs := make(map[string][]map[string]any)
		collections := []string{semantic.BuildCollection}
		if !incomplete {
			collections = append(collections, fieldprofile.FieldCatalogCollection, catalog.RelationshipCatalogCollection, semantic.EntryCollection)
		}
		for _, collection := range collections {
			err := client.QueryRows(ctx, `FOR doc IN @@collection
FILTER doc.project == @project AND doc.dataset_generation == @generation
SORT doc._key
RETURN UNSET(doc, "_id", "_rev")`, 50, map[string]any{
				"@collection": collection, "project": ref.Project, "generation": ref.Generation,
			}, func(row map[string]any) error {
				docs[collection] = append(docs[collection], row)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(docs[collection]) == 0 {
				t.Fatalf("concurrency=%d collection %s returned no rows", concurrency, collection)
			}
		}
		builds := docs[semantic.BuildCollection]
		if len(builds) != 1 || builds[0]["checkpoint"] != "z/Patient.ndjson" || numericInt64(builds[0]["scanned_resources"]) != 4 {
			t.Fatalf("concurrency=%d unexpected checkpoint %+v", concurrency, builds)
		}
		return generationSnapshot{summary: summary, docs: docs}
	}
	compare := func(incomplete bool) {
		t.Helper()
		serial, concurrent := load(1, incomplete), load(3, incomplete)
		if !reflect.DeepEqual(serial.summary, concurrent.summary) {
			t.Fatalf("summaries differ: serial %+v, concurrent %+v", serial.summary, concurrent.summary)
		}
		for collection, expected := range serial.docs {
			if !reflect.DeepEqual(expected, concurrent.docs[collection]) {
				t.Fatalf("%s differs: serial %+v, concurrent %+v", collection, expected, concurrent.docs[collection])
			}
		}
	}
	compare(false)

	for index, path := range []string{"a/Patient.ndjson", "z/Patient.ndjson"} {
		file, err := os.OpenFile(filepath.Join(meta, path), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString(strings.Repeat("\n", index) + "{oops\n")
		if err := errors.Join(writeErr, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	compare(true)
}

func TestConcurrentGenerationCancellationMarksFailedAgainstArango(t *testing.T) {
	endpoint := failLegEndpoint(t)
	meta := writeConcurrentGenerationFixture(t)
	opts, ref := concurrentGenerationTestOptions(t, endpoint, meta, 3)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := 0
	opts.EventSink = func(event string, _ map[string]any) {
		if event == "go_load_file_start" {
			started++
			if started == 2 {
				cancel()
			}
		}
	}
	if _, err := Load(ctx, opts); !errors.Is(err, context.Canceled) || started < 2 {
		t.Fatalf("canceled load after %d starts returned %v", started, err)
	}
	client, err := arangostore.Open(t.Context(), endpoint, opts.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	store, err := publicationarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.ReadManifest(t.Context(), ref)
	if err != nil || manifest.State != publication.StateFailed {
		t.Fatalf("canceled generation manifest = %+v, err = %v", manifest, err)
	}
	if _, err := store.ResolveActiveManifest(t.Context(), ref.Project); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Fatalf("canceled generation reached active pointer: %v", err)
	}
}

func writeConcurrentGenerationFixture(t *testing.T) string {
	t.Helper()
	meta := t.TempDir()
	fixture := map[string]string{
		"a/Patient.ndjson": `{"resourceType":"Patient","id":"one","gender":"female","identifier":[{"system":"urn:example:patient","value":"one"}]}`,
		"z/Patient.ndjson": `{"resourceType":"Patient","id":"two","gender":"male","identifier":[{"system":"urn:example:patient","value":"two"}]}`,
		"Observation.ndjson": strings.Join([]string{
			`{"resourceType":"Observation","id":"obs-one","status":"final","subject":{"reference":"Patient/one"},"code":{"coding":[{"system":"urn:example:observation","code":"one"}]},"valueInteger":2}`,
			`{"resourceType":"Observation","id":"obs-two","status":"final","subject":{"reference":"Patient/two"},"code":{"coding":[{"system":"urn:example:observation","code":"one"}]},"valueInteger":3}`,
		}, "\n"),
	}
	for path, content := range fixture {
		path = filepath.Join(meta, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return meta
}

func concurrentGenerationTestOptions(t *testing.T, endpoint, meta string, concurrency int) (LoadOptions, publication.Ref) {
	t.Helper()
	database := arangotest.KeyCollisionDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { arangotest.DropKeyCollisionDatabase(t, endpoint, database) })
	ref, err := publication.NewRef("FILE_CONCURRENCY", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	return LoadOptions{
		ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
		MetaDir:           meta, Project: ref.Project, Dataset: &ref, StageOnly: true,
		AuthResourcePath: "/programs/perf/projects/concurrency", PreflightSampleRows: 1,
		FileConcurrency: concurrency, WorkerCount: 2, WriterCount: 1, BatchSize: 1,
	}, ref
}

func numericInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	default:
		return 0
	}
}

type ingestPerfVolume struct {
	Docs  int64 `json:"docs"`
	Bytes int64 `json:"bytes"`
}

type ingestPerfResult struct {
	Wall             float64                     `json:"wall_seconds"`
	Rows             int                         `json:"rows"`
	RowsPerSecond    float64                     `json:"rows_per_second"`
	Edges            int                         `json:"edges"`
	ValidationErrors int                         `json:"validation_errors"`
	GenerationErrors int                         `json:"generation_errors"`
	EdgeErrors       int                         `json:"edge_errors"`
	Stages           map[string]float64          `json:"stage_worker_seconds"`
	Volumes          map[string]ingestPerfVolume `json:"volumes"`
}

func TestIngestPerf(t *testing.T) {
	meta := os.Getenv("LOOM_INGEST_PERF_META")
	if meta == "" {
		t.Skip("set LOOM_INGEST_PERF_META to run the ingestion benchmark")
	}
	files, err := source.DiscoverNDJSON(meta)
	if err != nil || len(files) == 0 {
		t.Fatalf("discover files in %q: %d files, %v", meta, len(files), err)
	}
	capture := os.Getenv("LOOM_INGEST_PERF_CAPTURE")
	outputs := map[string]*bufio.Writer{}
	handles := map[string]*os.File{}
	if capture != "" {
		if err = os.MkdirAll(capture, 0755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for name, output := range outputs {
				if err := output.Flush(); err != nil {
					t.Error(err)
				}
				if err := handles[name].Close(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	result := ingestPerfResult{Stages: map[string]float64{}, Volumes: map[string]ingestPerfVolume{}}
	var mu sync.Mutex
	insert := func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, _ bool, _ string) error {
		var bytes int64
		for _, doc := range docs {
			bytes += int64(len(doc))
		}
		class := "vertices"
		if collection == bootstrap.EdgeCollection || collection == semantic.Collection || collection == semantic.EntryCollection {
			class = collection
		}
		mu.Lock()
		defer mu.Unlock()
		volume := result.Volumes[class]
		volume.Bytes += bytes
		volume.Docs += int64(len(docs))
		result.Volumes[class] = volume
		if capture == "" {
			return nil
		}
		output := outputs[collection]
		if output == nil {
			handle, err := os.OpenFile(filepath.Join(capture, collection+".ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
			if err != nil {
				return err
			}
			handles[collection] = handle
			output = bufio.NewWriterSize(handle, 1<<20)
			outputs[collection] = output
		}
		for _, doc := range docs {
			if _, err := output.Write(doc); err != nil {
				return err
			}
			if err := output.WriteByte('\n'); err != nil {
				return err
			}
		}
		return nil
	}

	schema := source.LoadRuntimeFHIRSchema()
	opts := normalizeLoadOptions(LoadOptions{Project: "perf-project", MetaDir: meta, AuthResourcePath: "/programs/perf/projects/p"})
	generation := os.Getenv("LOOM_INGEST_PERF_GEN")
	start := time.Now()
	err = runGenerationFiles(t.Context(), opts, files, func(ctx context.Context, opts LoadOptions, index int, file string) error {
		fileStart := time.Now()
		loaded, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: generation, overwrite: false, insert: insert}.load(ctx, file, start, 0, 0)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		wall := time.Since(fileStart).Seconds()
		mu.Lock()
		defer mu.Unlock()
		result.Rows += loaded.Rows
		result.Edges += loaded.EdgesBuilt
		result.ValidationErrors += loaded.ValidationErrors
		result.GenerationErrors += loaded.GenerationErrors
		result.EdgeErrors += loaded.EdgeErrors
		for name, seconds := range loaded.StageSeconds {
			result.Stages[name] += seconds
		}
		fmt.Printf("%-30s rows=%d edges=%d wall=%.3fs rows/s=%.0f errors=%d/%d/%d\n", filepath.Base(file), loaded.Rows, loaded.EdgesBuilt, wall, float64(loaded.Rows)/wall, loaded.ValidationErrors, loaded.GenerationErrors, loaded.EdgeErrors)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result.Wall = time.Since(start).Seconds()
	result.RowsPerSecond = float64(result.Rows) / result.Wall
	fmt.Printf("total rows=%d edges=%d wall=%.3fs rows/s=%.0f errors=%d/%d/%d\n", result.Rows, result.Edges, result.Wall, result.RowsPerSecond, result.ValidationErrors, result.GenerationErrors, result.EdgeErrors)
	for name, seconds := range result.Stages {
		fmt.Printf("stage %-30s %.3f worker-s\n", name, seconds)
	}
	for name, volume := range result.Volumes {
		fmt.Printf("collection %-30s %d docs %d bytes\n", name, volume.Docs, volume.Bytes)
	}
	line, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("perf_result %s\n", line)
}

func kcollEndpoint(t *testing.T) string {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the F-B25 sanitize-key collision reproduction")
	}
	return endpoint
}

func TestFB25SanitizeKeyCollisionLosesAcknowledgedWrite(t *testing.T) {
	endpoint := kcollEndpoint(t)
	directCollision := map[string]string{}
	for _, pair := range [][2]string{{"coll/1", "coll_1"}} {
		a, b := row.SanitizeKey(pair[0]), row.SanitizeKey(pair[1])
		if a != b {
			t.Fatalf("F-B25 probe premise lost — row.SanitizeKey(%q)=%q != row.SanitizeKey(%q)=%q", pair[0], a, pair[1], b)
		}
		directCollision[pair[0]] = a
	}
	t.Logf("FB25 GATE | SanitizeKey non-injective confirmed in-process | %q and %q both -> %q (documents.go:107-113 validKeyPart :17)", "coll/1", "coll_1", directCollision["coll/1"])

	lines := []string{
		`{"resourceType":"Patient","id":"coll/1","gender":"female"}`,
		`{"resourceType":"Patient","id":"coll_1","gender":"male"}`,
		`{"resourceType":"Patient","id":"x9","gender":"female"}`,
	}
	label := "Generated"
	t.Run(label, func(t *testing.T) {
		database := arangotest.KeyCollisionDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		t.Cleanup(func() { arangotest.DropKeyCollisionDatabase(t, endpoint, database) })

		metaDir := t.TempDir()
		file := filepath.Join(metaDir, "Patient.ndjson")
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		ref, err := publication.NewRef("KCOLL_TEST", "gen-"+label)
		if err != nil {
			t.Fatalf("ref: %v", err)
		}
		summary, err := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			MetaDir:           metaDir,
			Project:           "KCOLL_TEST",
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		})
		if err != nil && !strings.Contains(err.Error(), "is incomplete") {
			t.Fatalf("F-B25 %s: production Load failed (non-rejection error): %v", label, err)
		}
		if err != nil {
			t.Logf("FB25 %s | Load reported row rejections (classification follows the switch below): %v", label, err)
		}
		samples, _ := json.Marshal(summary.RowErrors)
		t.Logf("FB25 %s | LOAD_SUMMARY acked=%d validation_errors=%d generation_errors=%d row_errors=%s",
			label, summary.VerticesInserted, summary.ValidationErrors, summary.GenerationErrors, samples)

		client, err := arangostore.Open(ctx, endpoint, database)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = client.Close(context.Background()) }()
		exists, err := client.CollectionExists(ctx, "Patient")
		if err != nil {
			t.Fatalf("collection exists: %v", err)
		}
		collected := int64(0)
		if exists {
			collected, err = client.CollectionCount(ctx, "Patient")
			if err != nil {
				t.Fatalf("collection count: %v", err)
			}
		}
		t.Logf("FB25 %s | COUNTS acknowledged=%d persisted=%d (non-write error samples=%d)", label, summary.VerticesInserted, collected, summary.ValidationErrors+summary.GenerationErrors+summary.EdgeErrors)

		switch {
		case collected == int64(len(lines)):
			t.Logf("FB25 %s verdict | PREVENTED — every submitted id persisted, no replacement occurred on this path", label)
		case summary.VerticesInserted > 0 && collected < int64(summary.VerticesInserted) && summary.ValidationErrors == 0 && summary.GenerationErrors == 0:
			t.Errorf("FB25 %s RED-ON-PURPOSE CONFIRMED silent replacement | Load acknowledged %d vertices (counter += len(sent docs), generation_workers.go:426) but only %d documents exist in Patient: one of ids coll/1 vs coll_1 (both SanitizeKey->coll_1, documents.go:107-113) was overwritten by onDuplicate=replace with zero error surfaced to the caller — lost acknowledged write", label, summary.VerticesInserted, collected)
		case summary.VerticesInserted == 0 && (summary.ValidationErrors+summary.GenerationErrors) > 0:
			t.Errorf("FB25 %s unexpected total rejection (all %d rows errored): row_errors=%s — probe premise about this path's validation strength lost; classify separately", label, len(lines), samples)
		default:

			if collected == int64(summary.VerticesInserted) {
				t.Logf("FB25 %s verdict | PREVENTED by validation on this path | acknowledged=%d persisted=%d, %d row(s) rejected before write (see row_errors above)", label, summary.VerticesInserted, collected, summary.ValidationErrors+summary.GenerationErrors)
			} else {
				t.Errorf("FB25 %s INCONCLUSIVE accounting mismatch | acknowledged=%d persisted=%d errors val=%d gen=%d — manual classification needed, row_errors=%s", label, summary.VerticesInserted, collected, summary.ValidationErrors, summary.GenerationErrors, samples)
			}
		}
	})

	t.Cleanup(func() { kcollAssertNoResidue(t, endpoint) })
}

func kcollAssertNoResidue(t *testing.T, endpoint string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/_api/database", nil)
	if err != nil {
		t.Logf("FB25_RESIDUE_WARN: build list: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("FB25_RESIDUE_WARN: list databases: %v", err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	var payload struct {
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Logf("FB25_RESIDUE_WARN: decode list: %v", err)
		return
	}
	for _, db := range payload.Result {
		if strings.HasPrefix(db, arangotest.KeyCollisionDatabasePrefix) {
			t.Errorf("F-B25 residue verification database left behind: %s", db)
		}
	}
}

func TestNormalizeLoadOptionsUsesBoundedConcurrencyDefaults(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{WriterCount: 3})
	if got.WorkerCount != 3 {
		t.Fatalf("worker count = %d, want writer count 3", got.WorkerCount)
	}
	if got.LineQueueSize <= 0 || got.WriteQueueSize != 2 {
		t.Fatalf("queue sizes = line %d write %d, want positive line limit and two write batches", got.LineQueueSize, got.WriteQueueSize)
	}
	if got.CatalogLimits.MaxDistinctValuesPerField <= 0 || got.CatalogLimits.MaxShapePlans <= 0 {
		t.Fatalf("catalog limits = %#v, want finite defaults", got.CatalogLimits)
	}
}

func TestNormalizeLoadOptionsPreservesExplicitConcurrencyAndCatalogLimits(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{
		WriterCount:    1,
		WorkerCount:    1,
		LineQueueSize:  32,
		WriteQueueSize: 4,
		CatalogLimits: fieldprofile.ProfileLimits{
			MaxFields:                  4,
			MaxDistinctValuesPerField:  2,
			MaxDistinctValueBytes:      8,
			MaxPivotColumnsPerField:    2,
			MaxExtensionValuesPerField: 2,
			MaxShapePlans:              1,
		},
	})
	if got.WorkerCount != 1 || got.LineQueueSize != 32 || got.WriteQueueSize != 4 {
		t.Fatalf("normalized concurrency = %#v, want explicit values", got)
	}
	if got.CatalogLimits.MaxDistinctValuesPerField != 2 {
		t.Fatalf("normalized catalog limits = %#v, want explicit value", got.CatalogLimits)
	}
}

func TestNormalizeLoadOptions(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{})
	if got.BatchSize != 5000 || got.ProgressEvery != 50000 || got.WriterCount != 8 || got.FileConcurrency != 2 || got.WriteAPI != "import" {
		t.Fatalf("normalizeLoadOptions defaults = %+v", got)
	}

	got = normalizeLoadOptions(LoadOptions{BatchSize: 13, ProgressEvery: 17, WriterCount: 3, FileConcurrency: 4, WriteAPI: "documents"})
	if got.BatchSize != 13 || got.ProgressEvery != 17 || got.WriterCount != 3 || got.FileConcurrency != 4 || got.WriteAPI != "documents" {
		t.Fatalf("normalizeLoadOptions overwrote explicit values: %+v", got)
	}
}
