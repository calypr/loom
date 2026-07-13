package dataframeapi

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataset"
)

type datasetDiscoveryManifestResolver struct {
	manifests map[string]dataset.Manifest
	errors    map[string]error
}

func (r datasetDiscoveryManifestResolver) ResolveActiveManifest(_ context.Context, project string) (dataset.Manifest, error) {
	if err := r.errors[project]; err != nil {
		return dataset.Manifest{}, err
	}
	manifest, ok := r.manifests[project]
	if !ok {
		return dataset.Manifest{}, errors.New("no active generation")
	}
	return manifest.Clone(), nil
}

func TestDiscoverDatasetsUsesPrincipalProjectsAndGenerationScope(t *testing.T) {
	manifest := builderReadyManifest(t, "P1", "generation-1")
	var got catalog.DatasetSummaryOptions
	service := NewService(Config{
		ActiveManifestResolver: datasetDiscoveryManifestResolver{manifests: map[string]dataset.Manifest{"P1": manifest}},
		DiscoverDatasets: func(_ context.Context, options catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error) {
			got = options
			return []catalog.DatasetSummary{{
				Project: "P1", DatasetGeneration: "generation-1", State: "READY",
				ResourceTypes: []catalog.ResourceTypeSummary{{ResourceType: "Patient", DocumentCount: 4, PopulatedFieldCount: 2, PivotCandidateCount: 1}},
			}}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"path-a"},
	})
	result, err := service.DiscoverDatasets(ctx)
	if err != nil {
		t.Fatalf("DiscoverDatasets() error = %v", err)
	}
	if len(result) != 1 || result[0].Project != "P1" || result[0].DatasetGeneration != "generation-1" || len(result[0].ResourceTypes) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if got.ProjectAllowlist[0] != "P1" || got.DatasetGenerationByProject["P1"] != "generation-1" || got.DatasetStateByProject["P1"] != "READY" {
		t.Fatalf("dataset selection = %+v", got)
	}
	scope := got.AuthScopesByProject["P1"]
	if scope.Unrestricted || len(scope.AuthResourcePaths) != 1 || scope.AuthResourcePaths[0] != "path-a" {
		t.Fatalf("dataset auth scope = %+v", scope)
	}
}

func TestDiscoverDatasetsIntersectsConfiguredProjectsAndSkipsInvalidActive(t *testing.T) {
	called := false
	service := NewService(Config{
		DatasetProjectAllowlist: []string{"P2", "P1", "P3"},
		ActiveManifestResolver: datasetDiscoveryManifestResolver{
			manifests: map[string]dataset.Manifest{"P1": builderReadyManifest(t, "P1", "generation-1")},
			errors:    map[string]error{"P2": errors.New("active pointer missing")},
		},
		DiscoverDatasets: func(_ context.Context, options catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error) {
			called = true
			if len(options.ProjectAllowlist) != 1 || options.ProjectAllowlist[0] != "P1" {
				t.Fatalf("selected projects = %#v", options.ProjectAllowlist)
			}
			return []catalog.DatasetSummary{}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Projects: []string{"P1", "P2"}})
	if _, err := service.DiscoverDatasets(ctx); err != nil {
		t.Fatalf("DiscoverDatasets() error = %v", err)
	}
	if !called {
		t.Fatal("catalog discovery was not called for surviving project")
	}
}

func TestDiscoverDatasetsDoesNotScanCatalogWithoutExplicitProjects(t *testing.T) {
	called := false
	service := NewService(Config{
		DiscoverDatasets: func(context.Context, catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error) {
			called = true
			return nil, nil
		},
	})
	result, err := service.DiscoverDatasets(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDatasets() error = %v", err)
	}
	if called || result == nil || len(result) != 0 {
		t.Fatalf("result=%#v catalogCalled=%v", result, called)
	}
}

func TestDatasetDiscoveryProjectsAreDeterministic(t *testing.T) {
	got := datasetDiscoveryProjects(&authscope.Principal{Projects: []string{" P2", "P1", "P2"}}, []string{"P3", "P1", "P2"})
	want := []string{"P1", "P2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("projects = %#v, want %#v", got, want)
	}
}
