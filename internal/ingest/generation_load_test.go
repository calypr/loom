package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestGenerationSemanticCoverageCertificatesPresentAndMissingRoots(t *testing.T) {
	identity := catalogarango.SemanticCoverageBuildIdentity{
		Project: "project-a", DatasetGeneration: "generation-a", BuildID: "coverage-build",
		SchemaIdentity: strings.Repeat("a", 64), SourceKind: catalog.SemanticInventorySourceFile,
	}
	store := &generationCoverageLifecycleFake{states: map[string]string{}}
	resources, expected, err := beginGenerationSemanticCoverageResources(
		context.Background(), store, identity,
		[]string{"Patient", "Observation"},
		[]string{filepath.Join(t.TempDir(), "Observation.ndjson")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, []string{"Observation", "Patient"}) || store.states["Observation"] != "BUILDING" || store.states["Patient"] != "MISSING" {
		t.Fatalf("expected roots=%v resource states=%v, want sorted roots and present/missing markers", expected, store.states)
	}
	if err := resources["Observation"].sourceSet.AddSource("Observation.ndjson#1", "scope-a"); err != nil {
		t.Fatal(err)
	}
	resources["Observation"].sourceCount = 1
	if err := finalizeGenerationSemanticCoverageResources(context.Background(), store, identity, expected, resources, 10); err != nil {
		t.Fatal(err)
	}
	if store.states["Observation"] != "COMPLETE" || len(store.expectations) != 1 || store.globalState != catalogarango.SemanticCoverageBuildPartial || !reflect.DeepEqual(store.finalized, expected) {
		t.Fatalf("completed resource states=%v expectations=%d finalized=%v global=%q", store.states, len(store.expectations), store.finalized, store.globalState)
	}
	expectation := store.expectations[0]
	if expectation.ResourceType != "Observation" || !expectation.SourceExists || !expectation.Terminal || expectation.SourceCount != 1 {
		t.Fatalf("terminal source expectation = %+v", expectation)
	}
	if expectation.SourceSetDigest == "" || len(expectation.Partitions) != 1 || expectation.Partitions[0].AuthResourcePath != "scope-a" || expectation.Partitions[0].SourceCount != 1 {
		t.Fatalf("terminal source set digest/authorization partition = %q/%+v", expectation.SourceSetDigest, expectation.Partitions)
	}
}

type generationCoverageLifecycleFake struct {
	states       map[string]string
	expectations []catalogarango.SemanticCoverageResourceExpectation
	finalized    []string
	globalState  catalogarango.SemanticCoverageBuildState
}

func (f *generationCoverageLifecycleFake) BeginSemanticCoverageResource(_ context.Context, _ catalogarango.SemanticCoverageBuildIdentity, resourceType string) error {
	f.states[resourceType] = "BUILDING"
	return nil
}

func (f *generationCoverageLifecycleFake) MarkSemanticCoverageResourceMissing(_ context.Context, _ catalogarango.SemanticCoverageBuildIdentity, resourceType string) error {
	f.states[resourceType] = "MISSING"
	return nil
}

func (f *generationCoverageLifecycleFake) CompleteSemanticCoverageResource(_ context.Context, _ catalogarango.SemanticCoverageBuildIdentity, expectation catalogarango.SemanticCoverageResourceExpectation, _ int) error {
	f.expectations = append(f.expectations, expectation)
	f.states[expectation.ResourceType] = "COMPLETE"
	return nil
}

func (f *generationCoverageLifecycleFake) FailSemanticCoverageResource(_ context.Context, _ catalogarango.SemanticCoverageBuildIdentity, resourceType, _ string) error {
	f.states[resourceType] = "FAILED"
	return nil
}

func (f *generationCoverageLifecycleFake) FinalizeSemanticCoverageBuild(_ context.Context, _ catalogarango.SemanticCoverageBuildIdentity, resourceTypes []string) error {
	f.finalized = append([]string(nil), resourceTypes...)
	for _, resourceType := range resourceTypes {
		if f.states[resourceType] != "COMPLETE" {
			f.globalState = catalogarango.SemanticCoverageBuildPartial
			return nil
		}
	}
	f.globalState = catalogarango.SemanticCoverageBuildComplete
	return nil
}

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
