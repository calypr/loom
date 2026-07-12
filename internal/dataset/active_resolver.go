package dataset

import (
	"context"
	"fmt"
)

// ActiveManifestResolver is the read-side persistence boundary for selecting
// a project's active dataset generation. Implementations must resolve the
// currently active pointer and return the exact READY manifest it names in one
// consistent storage read. They must not infer a generation from catalog rows
// or silently fall back to another manifest.
//
// The interface intentionally carries no authorization scope: generation
// selection is project-wide immutable metadata, while authorization remains a
// per-read concern owned by the request service.
type ActiveManifestResolver interface {
	ResolveActiveManifest(context.Context, string) (Manifest, error)
}

// ResolveReadyActiveManifest validates an ActiveManifestResolver result at the
// persistence-neutral boundary. It protects request services from a malformed
// or stale adapter result before they propagate its generation into catalog or
// dataframe work. The returned value is a defensive clone.
func ResolveReadyActiveManifest(ctx context.Context, resolver ActiveManifestResolver, project string) (Manifest, error) {
	if resolver == nil {
		return Manifest{}, fmt.Errorf("%w: active manifest resolver is required", ErrInvalidActiveGeneration)
	}
	manifest, err := resolver.ResolveActiveManifest(ctx, project)
	if err != nil {
		return Manifest{}, err
	}
	active, err := ActiveGenerationFor(manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve active manifest for project %q: %w", project, err)
	}
	if active.Dataset.Project != project {
		return Manifest{}, fmt.Errorf("%w: resolver returned project %q for requested project %q", ErrInvalidActiveGeneration, active.Dataset.Project, project)
	}
	return manifest.Clone(), nil
}
