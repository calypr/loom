package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
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
		Schema:   repoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:  dir,
		Project:  ref.Project,
		Dataset:  &ref,
		Truncate: true, // would be rejected only after preflight succeeds.
		ConnectionOptions: arangostore.ConnectionOptions{
			URL:      "http://127.0.0.1:1",
			Database: "generation_preflight_must_not_connect",
		},
		EventSink: func(event string, fields map[string]any) {
			events = append(events, event)
		},
	})
	var preflightErr *PreflightError
	if !errors.As(err, &preflightErr) {
		t.Fatalf("Load() error = %T %v, want PreflightError", err, err)
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
		Schema:  repoPath(t, "schemas", "graph-fhir.json"),
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

func generationRowErrorSample(file string, line int, resourceType string) RowErrorSample {
	return RowErrorSample{
		File:         file,
		Line:         line,
		ResourceType: resourceType,
		Category:     string(rowErrorValidation),
		Message:      fmt.Sprintf("%s row %d: invalid payload", file, line),
	}
}

func generationRowErrorSamples(file string, resourceType string, lines ...int) []RowErrorSample {
	samples := make([]RowErrorSample, 0, len(lines))
	for _, line := range lines {
		samples = append(samples, generationRowErrorSample(file, line, resourceType))
	}
	return samples
}

func generationRowErrorKey(sample RowErrorSample) string {
	return fmt.Sprintf("%s:%d", sample.File, sample.Line)
}

func TestSelectGenerationRowErrorSamplesCoversEveryFailingFile(t *testing.T) {
	candidates := [][]RowErrorSample{
		generationRowErrorSamples("Condition.ndjson", "Condition", 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12),
		generationRowErrorSamples("Medication.ndjson", "Medication", 1, 2, 3),
		generationRowErrorSamples("Patient.ndjson", "Patient", 1, 2),
	}

	samples := selectGenerationRowErrorSamples(candidates)
	if len(samples) > rowErrorSampleLimit {
		t.Fatalf("samples = %d, want at most %d", len(samples), rowErrorSampleLimit)
	}

	perFile := map[string][]RowErrorSample{}
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
	// Round-robin by file order fills each smaller file completely before the
	// largest file consumes the leftover budget, and the summary reports the
	// picks in file-then-line order.
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

func generationRowErrorKeys(samples []RowErrorSample) []string {
	keys := make([]string, 0, len(samples))
	for _, sample := range samples {
		keys = append(keys, generationRowErrorKey(sample))
	}
	return keys
}

func TestSelectGenerationRowErrorSamplesIsBoundedAndDeterministic(t *testing.T) {
	const failingFileCount = 25
	candidates := make([][]RowErrorSample, 0, failingFileCount+1)
	for i := 0; i < failingFileCount; i++ {
		file := fmt.Sprintf("Patient-%02d.ndjson", i)
		candidates = append(candidates, generationRowErrorSamples(file, "Patient", 1, 2, 3))
	}
	candidates = append(candidates, nil)

	samples := selectGenerationRowErrorSamples(candidates)
	if len(samples) != rowErrorSampleLimit {
		t.Fatalf("samples = %d, want the capped budget of %d from %d failing files", len(samples), rowErrorSampleLimit, failingFileCount)
	}
	// More failing files than budget: the earliest files in discovery order win.
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
		for _, candidates := range [][][]RowErrorSample{nil, {}, {nil}, {{}, {}}} {
			if samples := selectGenerationRowErrorSamples(candidates); len(samples) != 0 {
				t.Fatalf("selectGenerationRowErrorSamples(%v) = %v, want no samples", candidates, samples)
			}
		}
	})

	t.Run("duplicates consume no budget and cannot repeat", func(t *testing.T) {
		duplicate := generationRowErrorSample("Patient.ndjson", 7, "Patient")
		candidates := [][]RowErrorSample{
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
		candidates := [][]RowErrorSample{
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
		candidates := [][]RowErrorSample{
			generationRowErrorSamples("Patient.ndjson", "Patient", 1, 2, 3),
		}
		before := append([]RowErrorSample(nil), candidates[0]...)
		selectGenerationRowErrorSamples(candidates)
		if !reflect.DeepEqual(before, candidates[0]) {
			t.Fatalf("selector mutated per-file candidates: %v then %v", before, candidates[0])
		}
	})
}

func TestGenerationRowErrorSamplesCoverEveryFailingStagedFile(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	invalid := "{\n"
	// Sorted discovery order: Condition, Medication, Observation, Patient.
	writePreflightFixture(t, dir, "Condition.ndjson", strings.Repeat(invalid, 12))
	writePreflightFixture(t, dir, "Medication.ndjson", invalid+invalid+invalid)
	writePreflightFixture(t, dir, "Observation.ndjson", invalid+invalid)
	writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient","id":"patient-1"}`+"\n")

	files, err := DiscoverNDJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("discovered files = %v, want four staged files", files)
	}

	opts := normalizeLoadOptions(LoadOptions{Project: "project-a", WriterCount: 2, WorkerCount: 2})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }

	var totals fileLoadResult
	var candidates [][]RowErrorSample
	for _, file := range files {
		result, err := loadFile(context.Background(), opts, nil, schema, file, "generation-a", false, time.Now(), 0, 0, insert)
		if err != nil {
			t.Fatalf("loadFile(%s): %v", filepath.Base(file), err)
		}
		totals.ValidationErrors += result.ValidationErrors
		totals.GenerationErrors += result.GenerationErrors
		totals.EdgeErrors += result.EdgeErrors
		totals.Rows += result.Rows
		candidates = append(candidates, result.RowErrors)
	}

	// Aggregate counts are exact and independent of how samples are selected.
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
		if sample.Category != string(rowErrorValidation) || sample.Message == "" ||
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
	// 12 + 3 + 2 invalid rows compete for ten slots: each failing file keeps its
	// two earliest lines before Condition takes the remaining slots.
	if got, want := generationRowErrorKeys(samples), []string{
		"Condition.ndjson:1", "Condition.ndjson:2", "Condition.ndjson:3", "Condition.ndjson:4", "Condition.ndjson:5",
		"Medication.ndjson:1", "Medication.ndjson:2", "Medication.ndjson:3",
		"Observation.ndjson:1", "Observation.ndjson:2",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("samples = %v, want %v", got, want)
	}
	// Every sample reproduces a real invalid line from its own staged file.
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
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writePreflightFixture(t, dir, "Patient.ndjson", `{"resourceType":"Patient","id":"patient-1"}`+"\n")
	writePreflightFixture(t, dir, "Practitioner.ndjson", `{"resourceType":"Practitioner","id":"practitioner-1"}`+"\n")

	files, err := DiscoverNDJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project-a", WriterCount: 2, WorkerCount: 2})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }

	var candidates [][]RowErrorSample
	var totals fileLoadResult
	for _, file := range files {
		result, err := loadFile(context.Background(), opts, nil, schema, file, "generation-a", false, time.Now(), 0, 0, insert)
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
	cache := catalog.NewShapePlanCacheWithLimit(catalog.DefaultProfileLimits().MaxShapePlans)
	keys := map[generationCatalogKey]*catalog.Profiler{
		{project: "project-a", datasetGeneration: "generation-b", authResourcePath: "scope-b", resourceType: "Patient"}: catalog.NewProfilerForGenerationWithLimits("project-a", "generation-b", "scope-b", "Patient", cache, catalog.DefaultProfileLimits()),
		{project: "project-a", datasetGeneration: "generation-a", authResourcePath: "scope-a", resourceType: "Patient"}: catalog.NewProfilerForGenerationWithLimits("project-a", "generation-a", "scope-a", "Patient", cache, catalog.DefaultProfileLimits()),
	}
	sorted := sortedGenerationCatalogKeys(keys)
	if len(sorted) != 2 || sorted[0].datasetGeneration != "generation-a" || sorted[1].datasetGeneration != "generation-b" {
		t.Fatalf("sorted generation catalog keys = %#v", sorted)
	}
}

func loadGenerationSchemaIdentity(t *testing.T) publication.SchemaSnapshot {
	t.Helper()
	identity, err := loadSchemaSnapshot(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("load schema identity: %v", err)
	}
	return identity
}
