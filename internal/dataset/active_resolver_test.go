package dataset

import (
	"context"
	"errors"
	"testing"
)

type staticActiveManifestResolver struct {
	manifest Manifest
	err      error
	projects []string
}

func (r *staticActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (Manifest, error) {
	r.projects = append(r.projects, project)
	if r.err != nil {
		return Manifest{}, r.err
	}
	return r.manifest.Clone(), nil
}

func TestResolveReadyActiveManifestValidatesResolverBoundary(t *testing.T) {
	ready := readyManifest(t, "project-a", "generation-a")
	resolver := &staticActiveManifestResolver{manifest: ready}

	got, err := ResolveReadyActiveManifest(context.Background(), resolver, "project-a")
	if err != nil {
		t.Fatalf("ResolveReadyActiveManifest() error = %v", err)
	}
	if !got.Dataset.Equal(ready.Dataset) || got.State != ManifestStateReady {
		t.Fatalf("resolved manifest = %#v, want READY %#v", got, ready)
	}
	if len(resolver.projects) != 1 || resolver.projects[0] != "project-a" {
		t.Fatalf("resolver projects = %#v, want project-a", resolver.projects)
	}

	wrongProject := &staticActiveManifestResolver{manifest: ready}
	if _, err := ResolveReadyActiveManifest(context.Background(), wrongProject, "project-b"); !errors.Is(err, ErrInvalidActiveGeneration) {
		t.Fatalf("wrong-project resolver error = %v, want ErrInvalidActiveGeneration", err)
	}

	notReady := &staticActiveManifestResolver{manifest: fixtureManifest(t, "project-a", "generation-a")}
	if _, err := ResolveReadyActiveManifest(context.Background(), notReady, "project-a"); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("not-ready resolver error = %v, want ErrGenerationNotReady", err)
	}

	if _, err := ResolveReadyActiveManifest(context.Background(), nil, "project-a"); !errors.Is(err, ErrInvalidActiveGeneration) {
		t.Fatalf("nil resolver error = %v, want ErrInvalidActiveGeneration", err)
	}
}
