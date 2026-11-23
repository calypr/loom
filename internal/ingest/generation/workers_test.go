package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog/fieldprofile"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/ingest/source"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestNewGenerationLoadPlanRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	identity := loadGenerationSchemaIdentity(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "Patient.ndjson")
	if err := os.WriteFile(file, []byte(`{"resourceType":"Patient"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	validRef, err := publication.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		opts  LoadOptions
		files []string
		want  error
	}{
		{
			name:  "invalid dataset reference",
			opts:  LoadOptions{MetaDir: dir, Project: "project-a", Dataset: &publication.Ref{Project: "project-a"}},
			files: []string{file},
			want:  publication.ErrInvalidDatasetRef,
		},
		{
			name:  "project mismatch",
			opts:  LoadOptions{MetaDir: dir, Project: "project-b", Dataset: &validRef},
			files: []string{file},
			want:  ErrGenerationDatasetProjectMismatch,
		},
		{
			name:  "truncate forbidden",
			opts:  LoadOptions{MetaDir: dir, Project: "project-a", Dataset: &validRef, Truncate: true},
			files: []string{file},
			want:  ErrGenerationLoadTruncateForbidden,
		},
		{
			name:  "file is not directory",
			opts:  LoadOptions{MetaDir: file, Project: "project-a", Dataset: &validRef},
			files: []string{file},
			want:  ErrGenerationLoadRequiresDirectory,
		},
		{
			name:  "empty staged directory",
			opts:  LoadOptions{MetaDir: t.TempDir(), Project: "project-a", Dataset: &validRef},
			files: nil,
			want:  ErrGenerationLoadRequiresFiles,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newGenerationLoadPlan(test.opts, test.files, identity); !errors.Is(err, test.want) {
				t.Fatalf("newGenerationLoadPlan() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNewGenerationLoadPlanSnapshotsDatasetAndSchemaBeforeBackend(t *testing.T) {
	identity := loadGenerationSchemaIdentity(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "Patient.ndjson")
	if err := os.WriteFile(file, []byte(`{"resourceType":"Patient"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	ref, err := publication.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newGenerationLoadPlan(LoadOptions{MetaDir: dir, Project: ref.Project, Dataset: &ref}, []string{file}, identity)
	if err != nil {
		t.Fatalf("newGenerationLoadPlan: %v", err)
	}
	if plan.Dataset != ref || plan.Manifest.Dataset != ref {
		t.Fatalf("generation plan dataset = %#v manifest = %#v, want %v", plan.Dataset, plan.Manifest.Dataset, ref)
	}
	if got, want := plan.Manifest.State, publication.StateLoading; got != want {
		t.Fatalf("generation manifest state = %q, want %q", got, want)
	}
	if got, want := plan.Manifest.SchemaIdentity.SchemaSHA256, identity.SchemaSHA256; got != want {
		t.Fatalf("generation schema digest = %q, want %q", got, want)
	}
}

func TestGenerationLoadPreflightRunsBeforeOptionRejectionOrBackend(t *testing.T) {
	dir := t.TempDir()
	writePreflightFixture(t, dir, "Unknown.ndjson", `{"resourceType":"Unknown"}`+"\n")
	ref, err := publication.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	summary, err := Load(context.Background(), LoadOptions{
		MetaDir:  dir,
		Project:  ref.Project,
		Dataset:  &ref,
		Truncate: true,
		ConnectionOptions: arangostore.ConnectionOptions{
			URL:      "http://127.0.0.1:1",
			Database: "generation_preflight_must_not_connect",
		},
		EventSink: func(event string, fields map[string]any) {
			events = append(events, event)
		},
	})
	var preflightErr *source.PreflightError
	if !errors.As(err, &preflightErr) {
		t.Fatalf("Load() error = %T %v, want source.PreflightError", err, err)
	}
	if summary.SchemaIdentity == nil || summary.Preflight.Valid() {
		t.Fatalf("Load() summary = %#v, want schema evidence and invalid preflight", summary)
	}
	for _, event := range events {
		if strings.HasPrefix(event, "go_backend_") || event == "go_bootstrap_start" {
			t.Fatalf("Load() emitted %q before generation preflight passed; events = %v", event, events)
		}
	}
}

func TestGenerationLoadEmptyDirectoryDoesNotOpenBackend(t *testing.T) {
	dir := t.TempDir()
	ref, err := publication.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	summary, err := Load(context.Background(), LoadOptions{
		MetaDir: dir,
		Project: ref.Project,
		Dataset: &ref,
		ConnectionOptions: arangostore.ConnectionOptions{
			URL:      "http://127.0.0.1:1",
			Database: "empty_generation_must_not_connect",
		},
		EventSink: func(event string, fields map[string]any) {
			events = append(events, event)
		},
	})
	if !errors.Is(err, ErrGenerationLoadRequiresFiles) {
		t.Fatalf("Load() error = %v, want %v", err, ErrGenerationLoadRequiresFiles)
	}
	if summary.SchemaIdentity == nil || !summary.Preflight.Valid() {
		t.Fatalf("Load() summary = %#v, want schema evidence and valid empty preflight", summary)
	}
	for _, event := range events {
		if strings.HasPrefix(event, "go_backend_") || event == "go_bootstrap_start" {
			t.Fatalf("Load() emitted %q for empty generation input; events = %v", event, events)
		}
	}
}

func generationRowErrorSample(file string, line int, resourceType string) row.RowErrorSample {
	return row.RowErrorSample{
		File:         file,
		Line:         line,
		ResourceType: resourceType,
		Category:     string(row.RowErrorValidation),
		Message:      fmt.Sprintf("%s row %d: invalid payload", file, line),
	}
}

func generationRowErrorSamples(file string, resourceType string, lines ...int) []row.RowErrorSample {
	samples := make([]row.RowErrorSample, 0, len(lines))
	for _, line := range lines {
		samples = append(samples, generationRowErrorSample(file, line, resourceType))
	}
	return samples
}

func generationRowErrorKey(sample row.RowErrorSample) string {
	return fmt.Sprintf("%s:%d", sample.File, sample.Line)
}

func TestSelectGenerationRowErrorSamplesCoversEveryFailingFile(t *testing.T) {
	candidates := [][]row.RowErrorSample{
		generationRowErrorSamples("Condition.ndjson", "Condition", 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		generationRowErrorSamples("Medication.ndjson", "Medication", 1, 2, 3),
		generationRowErrorSamples("Patient.ndjson", "Patient", 1, 2),
	}

	samples := selectGenerationRowErrorSamples(candidates)
	if len(samples) > rowErrorSampleLimit {
		t.Fatalf("samples = %d, want at most %d", len(samples), rowErrorSampleLimit)
	}

	perFile := map[string][]row.RowErrorSample{}
	for _, sample := range samples {
		perFile[sample.File] = append(perFile[sample.File], sample)
	}
	for _, want := range []struct {
		file         string
		resourceType string
		minSamples   int
	}{
		{file: "Condition.ndjson", resourceType: "Condition", minSamples: 1},
		{file: "Medication.ndjson", resourceType: "Medication", minSamples: 3},
		{file: "Patient.ndjson", resourceType: "Patient", minSamples: 2},
	} {
		got := perFile[want.file]
		if len(got) < want.minSamples {
			t.Fatalf("samples for %s = %v, want at least %d", want.file, generationRowErrorKeys(samples), want.minSamples)
		}
		for _, sample := range got {
			if sample.ResourceType != want.resourceType || sample.Line < 1 {
				t.Fatalf("sample = %+v, want a positive line of %s", sample, want.file)
			}
		}
	}
	if got := len(samples); got != rowErrorSampleLimit {
		t.Fatalf("samples = %d, want the full budget of %d", got, rowErrorSampleLimit)
	}

	want := []string{
		"Condition.ndjson:1", "Condition.ndjson:2", "Condition.ndjson:3",
		"Condition.ndjson:4", "Condition.ndjson:5",
		"Medication.ndjson:1", "Medication.ndjson:2", "Medication.ndjson:3",
		"Patient.ndjson:1", "Patient.ndjson:2",
	}
	if len(samples) != len(want) {
		t.Fatalf("samples = %d, want %d", len(samples), len(want))
	}
	for i, sample := range samples {
		if got := generationRowErrorKey(sample); got != want[i] {
			t.Fatalf("sample[%d] = %s, want %s (full selection = %v)", i, got, want[i], generationRowErrorKeys(samples))
		}
	}
}

func generationRowErrorKeys(samples []row.RowErrorSample) []string {
	keys := make([]string, 0, len(samples))
	for _, sample := range samples {
		keys = append(keys, generationRowErrorKey(sample))
	}
	return keys
}

func TestSelectGenerationRowErrorSamplesIsBoundedAndDeterministic(t *testing.T) {
	const failingFileCount = 25
	candidates := make([][]row.RowErrorSample, 0, failingFileCount+1)
	for i := 0; i < failingFileCount; i++ {
		file := fmt.Sprintf("Patient-%02d.ndjson", i)
		candidates = append(candidates, generationRowErrorSamples(file, "Patient", 1, 2, 3))
	}
	candidates = append(candidates, nil)

	samples := selectGenerationRowErrorSamples(candidates)
	if len(samples) != rowErrorSampleLimit {
		t.Fatalf("samples = %d, want the capped budget of %d from %d failing files", len(samples), rowErrorSampleLimit, failingFileCount)
	}

	for i, sample := range samples {
		if want := fmt.Sprintf("Patient-%02d.ndjson", i); sample.File != want {
			t.Fatalf("sample[%d] file = %q, want %q (selection = %v)", i, sample.File, want, generationRowErrorKeys(samples))
		}
	}
	if again := generationRowErrorKeys(selectGenerationRowErrorSamples(candidates)); !reflect.DeepEqual(again, generationRowErrorKeys(samples)) {
		t.Fatalf("selection is not deterministic: %v then %v", generationRowErrorKeys(samples), again)
	}
}

func TestSelectGenerationRowErrorSamplesEdges(t *testing.T) {
	t.Run("clean load returns no samples", func(t *testing.T) {
		for _, candidates := range [][][]row.RowErrorSample{nil, {}, {nil}, {{}, {}}} {
			if samples := selectGenerationRowErrorSamples(candidates); len(samples) != 0 {
				t.Fatalf("selectGenerationRowErrorSamples(%v) = %v, want no samples", candidates, samples)
			}
		}
	})

	t.Run("duplicates consume no budget and cannot repeat", func(t *testing.T) {
		duplicate := generationRowErrorSample("Patient.ndjson", 7, "Patient")
		candidates := [][]row.RowErrorSample{
			{duplicate, duplicate, duplicate},
			generationRowErrorSamples("Observation.ndjson", "Observation", 1, 2),
		}
		samples := selectGenerationRowErrorSamples(candidates)
		if len(samples) != 3 {
			t.Fatalf("samples = %v, want three distinct samples", generationRowErrorKeys(samples))
		}
		seen := map[string]struct{}{}
		for _, sample := range samples {
			key := generationRowErrorKey(sample)
			if _, already := seen[key]; already {
				t.Fatalf("duplicate sample %s in %v", key, generationRowErrorKeys(samples))
			}
			seen[key] = struct{}{}
		}
	})

	t.Run("selection keeps file then line order", func(t *testing.T) {
		candidates := [][]row.RowErrorSample{
			generationRowErrorSamples("Patient.ndjson", "Patient", 2, 9),
			{},
			generationRowErrorSamples("Practitioner.ndjson", "Practitioner", 3, 4, 5),
		}
		samples := selectGenerationRowErrorSamples(candidates)
		if len(samples) != 5 {
			t.Fatalf("samples = %v, want all five candidates", generationRowErrorKeys(samples))
		}
		for i := 1; i < len(samples); i++ {
			previous, current := samples[i-1], samples[i]
			if previous.File > current.File ||
				(previous.File == current.File && previous.Line > current.Line) {
				t.Fatalf("samples out of file-then-line order: %v", generationRowErrorKeys(samples))
			}
		}
	})

	t.Run("input candidates are not mutated", func(t *testing.T) {
		candidates := [][]row.RowErrorSample{
			generationRowErrorSamples("Patient.ndjson", "Patient", 1, 2, 3),
		}
		before := append([]row.RowErrorSample(nil), candidates[0]...)
		selectGenerationRowErrorSamples(candidates)
		if !reflect.DeepEqual(before, candidates[0]) {
			t.Fatalf("selector mutated per-file candidates: %v then %v", before, candidates[0])
		}
	})
}

func TestGenerationRowErrorSamplesCoverEveryFailingStagedFile(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	dir := t.TempDir()
	invalid := "{\n"

	writePreflightFixture(t, dir, "Condition.ndjson", strings.Repeat(invalid, 12))
	writePreflightFixture(t, dir, "Medication.ndjson", invalid+invalid+invalid)
	writePreflightFixture(t, dir, "Observation.ndjson", invalid+invalid)
	writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient","id":"patient-1"}`+"\n")

	files, err := source.DiscoverNDJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("discovered files = %v, want four staged files", files)
	}

	opts := normalizeLoadOptions(LoadOptions{Project: "project-a", WriterCount: 2, WorkerCount: 2})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }

	var totals fileLoadResult
	var candidates [][]row.RowErrorSample
	for _, file := range files {
		result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-a", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
		if err != nil {
			t.Fatalf("loadFile(%s): %v", filepath.Base(file), err)
		}
		totals.ValidationErrors += result.ValidationErrors
		totals.GenerationErrors += result.GenerationErrors
		totals.EdgeErrors += result.EdgeErrors
		totals.Rows += result.Rows
		candidates = append(candidates, result.RowErrors)
	}

	if totals.ValidationErrors != 17 || totals.GenerationErrors != 0 || totals.EdgeErrors != 0 {
		t.Fatalf("totals = %+v, want 17 validation errors and no generation or edge errors", totals)
	}
	if totals.Rows != 1 {
		t.Fatalf("loaded rows = %d, want the single valid Patient row", totals.Rows)
	}

	samples := selectGenerationRowErrorSamples(candidates)
	if len(samples) > rowErrorSampleLimit {
		t.Fatalf("samples = %d, want at most %d", len(samples), rowErrorSampleLimit)
	}
	perFile := map[string][]int{}
	for _, sample := range samples {
		if sample.Category != string(row.RowErrorValidation) || sample.Message == "" ||
			sample.ResourceType != strings.TrimSuffix(sample.File, ".ndjson") {
			t.Fatalf("sample = %+v, want a validation sample for its resource file", sample)
		}
		perFile[sample.File] = append(perFile[sample.File], sample.Line)
	}
	for _, failing := range []string{"Condition.ndjson", "Medication.ndjson", "Observation.ndjson"} {
		if len(perFile[failing]) == 0 {
			t.Fatalf("no sample from failing file %s; samples = %v", failing, generationRowErrorKeys(samples))
		}
	}
	if _, clean := perFile["Patient.ndjson"]; clean {
		t.Fatalf("clean file Patient.ndjson produced samples: %v", generationRowErrorKeys(samples))
	}

	if got, want := generationRowErrorKeys(samples), []string{
		"Condition.ndjson:1", "Condition.ndjson:2", "Condition.ndjson:3", "Condition.ndjson:4", "Condition.ndjson:5",
		"Medication.ndjson:1", "Medication.ndjson:2", "Medication.ndjson:3",
		"Observation.ndjson:1", "Observation.ndjson:2",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("samples = %v, want %v", got, want)
	}

	for _, sample := range samples {
		contents, readErr := os.ReadFile(filepath.Join(dir, sample.File))
		if readErr != nil {
			t.Fatal(readErr)
		}
		lines := strings.Split(strings.TrimRight(string(contents), "\n"), "\n")
		if sample.Line < 1 || sample.Line > len(lines) || strings.TrimSpace(lines[sample.Line-1]) != "{" {
			t.Fatalf("sample %+v does not point at an invalid line of %s", sample, sample.File)
		}
	}
}

func TestGenerationRowErrorSamplesLeaveCleanLoadWithoutSamples(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	dir := t.TempDir()
	writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient","id":"patient-1"}`+"\n")
	writePreflightFixture(t, dir, "Practitioner.ndjson", `{"resourceType":"Practitioner","id":"practitioner-1"}`+"\n")

	files, err := source.DiscoverNDJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project-a", WriterCount: 2, WorkerCount: 2})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }

	var candidates [][]row.RowErrorSample
	var totals fileLoadResult
	for _, file := range files {
		result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-a", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
		if err != nil {
			t.Fatalf("loadFile(%s): %v", filepath.Base(file), err)
		}
		totals.Rows += result.Rows
		totals.ValidationErrors += result.ValidationErrors
		totals.GenerationErrors += result.GenerationErrors
		totals.EdgeErrors += result.EdgeErrors
		candidates = append(candidates, result.RowErrors)
	}
	if totals.Rows != 2 || totals.ValidationErrors != 0 || totals.GenerationErrors != 0 || totals.EdgeErrors != 0 {
		t.Fatalf("totals = %+v, want two clean rows and no errors", totals)
	}
	if samples := selectGenerationRowErrorSamples(candidates); len(samples) != 0 {
		t.Fatalf("clean load produced samples: %v", generationRowErrorKeys(samples))
	}
}

func TestSortedGenerationCatalogKeysKeepFullIdentityDistinct(t *testing.T) {
	cache := fieldprofile.NewShapePlanCacheWithLimits(fieldprofile.DefaultProfileLimits().MaxShapePlans, 0)
	keys := map[generationCatalogKey]*fieldprofile.Profiler{
		{project: "project-a", datasetGeneration: "generation-b", authResourcePath: "scope-b", resourceType: "Patient"}: fieldprofile.NewProfilerForGenerationWithLimits("project-a", "generation-b", "scope-b", "Patient", cache, fieldprofile.DefaultProfileLimits()),
		{project: "project-a", datasetGeneration: "generation-a", authResourcePath: "scope-a", resourceType: "Patient"}: fieldprofile.NewProfilerForGenerationWithLimits("project-a", "generation-a", "scope-a", "Patient", cache, fieldprofile.DefaultProfileLimits()),
	}
	sorted := sortedGenerationCatalogKeys(keys)
	if len(sorted) != 2 || sorted[0].datasetGeneration != "generation-a" || sorted[1].datasetGeneration != "generation-b" {
		t.Fatalf("sorted generation catalog keys = %#v", sorted)
	}
}

func loadGenerationSchemaIdentity(t *testing.T) publication.SchemaSnapshot {
	t.Helper()
	identity, err := source.LoadSchemaSnapshot()
	if err != nil {
		t.Fatalf("load schema identity: %v", err)
	}
	return identity
}

func TestLoadFileSupportsGenerationAndCancellation(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})

	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatalf("loadFile(): %v", err)
	}
	if result.ResourceType != "Patient" || result.Rows != 0 || result.Catalog == nil {
		t.Fatalf("loadFile() = %+v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return nil
	}}.load(ctx, file, time.Now(), 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadFile(canceled) error = %v, want context.Canceled", err)
	}
}

func TestLoadFilePreservesGenerationWrites(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(file, []byte("{\"resourceType\":\"Patient\",\"id\":\"patient-1\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})

	for _, test := range []struct {
		name, generation string
	}{
		{name: "generation", generation: "generation-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var documents []json.RawMessage
			var collections []string
			var overwrites []bool
			insert := func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
				mu.Lock()
				defer mu.Unlock()
				collections = append(collections, collection)
				overwrites = append(overwrites, overwrite)
				documents = append(documents, docs...)
				return nil
			}
			result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: test.generation, overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != 1 || result.VerticesInserted != 1 || result.ValidationErrors != 0 || len(result.Catalog.Documents()) == 0 {
				t.Fatalf("result = %+v", result)
			}
			if len(collections) != 1 || collections[0] != "Patient" || len(overwrites) != 1 || overwrites[0] || len(documents) != 1 {
				t.Fatalf("writes collections=%v overwrite=%v documents=%d", collections, overwrites, len(documents))
			}
			var vertex map[string]any
			if err := json.Unmarshal(documents[0], &vertex); err != nil {
				t.Fatal(err)
			}
			if vertex["id"] != "patient-1" {
				t.Fatalf("logical id = %#v", vertex["id"])
			}
			if vertex["dataset_generation"] != test.generation {
				t.Fatalf("dataset_generation = %#v, want %q", vertex["dataset_generation"], test.generation)
			}
		})
	}
}

func TestLoadFileReportsPhysicalLinesAndBoundedErrors(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }

	t.Run("fail fast physical line", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "Patient.ndjson")
		if err := os.WriteFile(file, []byte("\n \n\t\n{\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		failFastOpts := opts
		failFastOpts.FailFast = true
		_, err := fileLoader{options: failFastOpts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
		if err == nil || !strings.Contains(err.Error(), "Patient.ndjson Patient row 4:") {
			t.Fatalf("error = %v, want physical row 4", err)
		}
	})

	t.Run("earliest ten errors", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "Patient.ndjson")
		var input strings.Builder
		input.WriteString("\n")
		for i := 0; i < 12; i++ {
			if i == 1 {
				input.WriteString("\n")
			}
			input.WriteString("{\n")
		}
		if err := os.WriteFile(file, []byte(input.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines := []int{2, 4, 5, 6, 7, 8, 9, 10, 11, 12}
		if result.ValidationErrors != 12 || len(result.RowErrors) != len(wantLines) {
			t.Fatalf("validation errors=%d samples=%d", result.ValidationErrors, len(result.RowErrors))
		}
		for i, sample := range result.RowErrors {
			if sample.File != "Patient.ndjson" || sample.ResourceType != "Patient" || sample.Category != "validation" || sample.Message == "" || sample.Line != wantLines[i] {
				t.Fatalf("sample[%d] = %+v, want line %d validation error", i, sample, wantLines[i])
			}
		}
	})
}

func TestLoadFilePreservesRowErrorsAcrossLineBatches(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	data := "{\"resourceType\":\"Patient\",\"id\":\"one\"}\n{\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"three\"}\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"four\"}\n"
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", LineQueueSize: 2, WorkerCount: 2})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }
	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 || result.VerticesInserted != 3 || result.ValidationErrors != 1 || len(result.RowErrors) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.RowErrors[0].Line != 2 {
		t.Fatalf("error line = %d, want physical line 2", result.RowErrors[0].Line)
	}
}

func TestLoadFileStopsAtCancellationBetweenLinesInABatch(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	data := "{\"resourceType\":\"Patient\",\"id\":\"one\"}\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"two\"}\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"three\"}\n"
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	progressEvents := 0
	opts := normalizeLoadOptions(LoadOptions{
		Project:       "project",
		LineQueueSize: 8,
		WorkerCount:   1,
		ProgressEvery: 1,
		EventSink: func(event string, fields map[string]any) {
			if event == "go_load_progress" {
				progressEvents++
				if progressEvents == 1 {
					cancel()
				}
			}
		},
	})
	workerBudget := make(chan struct{}, 1)
	opts.fileWorkerBudget = workerBudget
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }
	_, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(ctx, file, time.Now(), 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadFile error = %v, want context.Canceled", err)
	}
	if progressEvents != 1 {
		t.Fatalf("progress events = %d, want only the completed first row", progressEvents)
	}
	if len(workerBudget) != 0 {
		t.Fatalf("worker permit leaked after cancellation: %d", len(workerBudget))
	}
}

func TestLoadFileBatchesAndPropagatesWriterFailure(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	data := []byte("{\"resourceType\":\"Patient\",\"id\":\"patient-1\"}\n{\"resourceType\":\"Patient\",\"id\":\"patient-2\"}\n{\"resourceType\":\"Patient\",\"id\":\"patient-3\"}\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", BatchSize: 1, WriterCount: 2})
	var inserted int64
	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(_ context.Context, _ *arangostore.Client, _ string, docs []json.RawMessage, _ bool, _ string) error {
		atomic.AddInt64(&inserted, int64(len(docs)))
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.VerticesInserted != 3 || result.VertexBatches != 3 || atomic.LoadInt64(&inserted) != 3 {
		t.Fatalf("result=%+v inserted=%d", result, inserted)
	}

	writeErr := errors.New("writer failed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	writerBudget := make(chan struct{}, 1)
	budgetedOpts := opts
	budgetedOpts.fileWriterBudget = writerBudget
	_, err = fileLoader{options: budgetedOpts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return writeErr
	}}.load(ctx, file, time.Now(), 0, 0)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want %v", err, writeErr)
	}
	if len(writerBudget) != 0 {
		t.Fatalf("writer permit leaked after insert error: %d permits held", len(writerBudget))
	}
}

func TestLoadFilePersistsSemanticInventoryFromSharedEmitter(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Observation.ndjson")
	data := []byte(`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"urn:inventory","version":"v1","code":"panel"}]},"component":[{"code":{"coding":[{"system":"urn:inventory","version":"v1","code":"glucose","display":"Glucose"}]},"valueQuantity":{"value":1,"unit":"mg"}}]}` + "\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema := source.LoadRuntimeFHIRSchema()
	opts := normalizeLoadOptions(LoadOptions{
		Project:     "project",
		MetaDir:     root,
		BatchSize:   1,
		WorkerCount: 1,
		WriterCount: 1,
	})
	var inventoryCollectionWrites []string
	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
		if collection == semanticarango.SemanticCoverageCollection {
			t.Fatalf("normal generation load wrote optional semantic coverage collection %q", collection)
		}
		if collection == semantic.Collection || collection == semantic.EntryCollection {
			inventoryCollectionWrites = append(inventoryCollectionWrites, collection)
		}
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventoryCollectionWrites) != 0 || result.SemanticInventoryBatches != 0 || len(result.SemanticInventoryEntryRuns) == 0 {
		message := "no row error sample"
		if len(result.RowErrors) > 0 {
			message = result.RowErrors[0].Message
		}
		t.Fatalf("inventory writes=%v batches=%d runs=%d rows=%d validation=%d sample=%s", inventoryCollectionWrites, result.SemanticInventoryBatches, len(result.SemanticInventoryEntryRuns), result.Rows, result.ValidationErrors, message)
	}
	var inventoryDocs []json.RawMessage
	err = mergeSemanticInventoryEntryRuns(context.Background(), result.SemanticInventoryEntryRuns, func(partial semantic.EntryPartial) error {
		document := semantic.EntryDocument(partial, "project", "generation-1", semantic.BuildID("project", "generation-1"), semantic.SourceFile)
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		inventoryDocs = append(inventoryDocs, encoded)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventoryDocs) != 2 {
		t.Fatalf("materialized entries = %d, want two", len(inventoryDocs))
	}
	var entry map[string]any
	if err := json.Unmarshal(inventoryDocs[0], &entry); err != nil {
		t.Fatal(err)
	}
	if entry["resource_type"] != "Observation" || entry["auth_resource_path"] != "" {
		t.Fatalf("inventory resource/scope = %q/%q", entry["resource_type"], entry["auth_resource_path"])
	}
	if entry["concept_id"] == "" || entry["binding_id"] == "" {
		t.Fatalf("inventory identity = %+v", entry)
	}
	ownerOptions := opts
	ownerOptions.SourceFileOwner = "Observation"
	var uploadedEntries []json.RawMessage
	result, err = fileLoader{options: ownerOptions, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
		if collection == semantic.Collection {
			t.Fatal("single-file upload wrote raw inventory contributions")
		}
		if collection == semantic.EntryCollection {
			if !overwrite {
				t.Fatal("single-file inventory entry write must replace prior rows")
			}
			uploadedEntries = append(uploadedEntries, docs...)
		}
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(uploadedEntries) != 2 || len(result.SemanticInventoryEntryRuns) != 0 {
		t.Fatalf("single-file entry writes=%d remaining runs=%d", len(uploadedEntries), len(result.SemanticInventoryEntryRuns))
	}
}

func TestLoadFileWaitsForInventorySpillOwnershipAfterWriterFailure(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Observation.ndjson")
	data := []byte(`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"urn:inventory","code":"panel"}]},"component":[{"code":{"coding":[{"system":"urn:inventory","code":"glucose","display":"Glucose"}]},"valueQuantity":{"value":1,"unit":"mg"}}]}` + "\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := filepath.Glob(filepath.Join(os.TempDir(), "loom-semantic-entry-*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", MetaDir: root, BatchSize: 1, WorkerCount: 1, WriterCount: 1, FailFast: true})
	_, err = fileLoader{options: opts, client: nil, schema: source.LoadRuntimeFHIRSchema(), datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return errors.New("forced writer failure")
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err == nil || !strings.Contains(err.Error(), "forced writer failure") {
		t.Fatalf("load error = %v, want writer failure", err)
	}
	after, err := filepath.Glob(filepath.Join(os.TempDir(), "loom-semantic-entry-*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	known := make(map[string]struct{}, len(before))
	for _, path := range before {
		known[path] = struct{}{}
	}
	for _, path := range after {
		if _, existed := known[path]; !existed {
			t.Errorf("failed file load leaked inventory spill %q", path)
		}
	}
}

func TestSemanticInventorySourceLocatorIncludesRelativeDirectory(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a", "Observation.ndjson")
	second := filepath.Join(root, "b", "Observation.ndjson")
	firstID := semanticInventorySourceRecordID(semanticInventorySourceFileID(root, first), 7)
	secondID := semanticInventorySourceRecordID(semanticInventorySourceFileID(root, second), 7)
	if firstID == secondID {
		t.Fatalf("same-basename source files collided at %q", firstID)
	}
	if firstID != "a/Observation.ndjson#7" || secondID != "b/Observation.ndjson#7" {
		t.Fatalf("relative source IDs = %q and %q", firstID, secondID)
	}
}

func TestGenerationWriteStatsSummarizesRequestsByClass(t *testing.T) {
	stats := &generationWriteStats{samples: make(map[string][]generationWriteSample)}
	for i := 20; i > 0; i-- {
		stats.record("edge", generationWriteSample{bytes: 10, permitWait: time.Second, duration: time.Duration(i) * time.Second})
	}
	stats.record("vertex", generationWriteSample{bytes: 3, duration: time.Second})
	summary := stats.summary()
	edges := summary["edge"]
	if edges["requests"] != 20 || edges["request_bytes"] != int64(200) ||
		edges["writer_permit_wait_seconds"] != 20.0 || edges["request_seconds"] != 210.0 ||
		edges["p95_request_seconds"] != 19.0 {
		t.Fatalf("unexpected edge write statistics: %v", edges)
	}
	if vertices := summary["vertex"]; vertices["requests"] != 1 || vertices["request_bytes"] != int64(3) {
		t.Fatalf("unexpected vertex write statistics: %v", vertices)
	}
}
