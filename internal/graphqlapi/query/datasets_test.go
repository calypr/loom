package queryapi

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/publication"
)

type datasetDiscoveryManifestResolver struct {
	manifests map[string]publication.Manifest
	errors    map[string]error
}

func (r datasetDiscoveryManifestResolver) ResolveActiveManifest(_ context.Context, project string) (publication.Manifest, error) {
	if err := r.errors[project]; err != nil {
		return publication.Manifest{}, err
	}
	manifest, ok := r.manifests[project]
	if !ok {
		return publication.Manifest{}, publication.ErrNoActiveGeneration
	}
	return manifest, nil
}

func TestDiscoverDatasetsUsesPrincipalProjectsAndGenerationScope(t *testing.T) {
	manifest := builderReadyManifest(t, "P1", "generation-1")
	var got catalog.DatasetSummaryOptions
	service := NewService(Config{
		ActiveManifestResolver: datasetDiscoveryManifestResolver{manifests: map[string]publication.Manifest{"P1": manifest}},
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
			manifests: map[string]publication.Manifest{"P1": builderReadyManifest(t, "P1", "generation-1")},
			errors:    map[string]error{"P2": publication.ErrNoActiveGeneration},
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

func TestDiscoverDatasetsPropagatesActiveGenerationBackendFailure(t *testing.T) {
	backendErr := errors.New("arango unavailable")
	service := NewService(Config{
		DatasetProjectAllowlist: []string{"P1"},
		ActiveManifestResolver:  datasetDiscoveryManifestResolver{errors: map[string]error{"P1": backendErr}},
		DiscoverDatasets: func(context.Context, catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error) {
			t.Fatal("catalog discovery should not run")
			return nil, nil
		},
	})
	_, err := service.DiscoverDatasets(context.Background())
	if err == nil {
		t.Fatal("DiscoverDatasets() error = nil")
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
		t.Fatalf("error = %v (%T), want retryable backend unavailable", err, err)
	}
	if !errors.Is(err, backendErr) {
		t.Fatalf("error %v does not preserve backend cause", err)
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
