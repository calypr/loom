package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

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
		collections := []string{catalog.SemanticInventoryBuildCollection}
		if !incomplete {
			collections = append(collections, catalog.FieldCatalogCollection, catalog.RelationshipCatalogCollection, catalog.SemanticInventoryEntryCollection)
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
		builds := docs[catalog.SemanticInventoryBuildCollection]
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
	database := kcollPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { kcollDropDatabase(t, endpoint, database) })
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
