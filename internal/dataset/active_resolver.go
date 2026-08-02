package dataset

import (
	"context"
	"errors"
	"fmt"
)

type ActiveResolver interface {
	ResolveActiveManifest(context.Context, string) (Manifest, error)
}

func ResolveActive(ctx context.Context, resolver ActiveResolver, project string) (Manifest, error) {
	if resolver == nil {
		return Manifest{}, fmt.Errorf("%w: active manifest resolver is required", ErrInvalidActiveGeneration)
	}
	if _, err := NewRef(project, "active-resolution"); err != nil {
		return Manifest{}, err
	}
	manifest, err := resolver.ResolveActiveManifest(ctx, project)
	if err != nil {
		if errors.Is(err, ErrNoActiveGeneration) {
			return Manifest{}, ErrNoActiveGeneration
		}
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("resolve active manifest for project %q: %w", project, err)
	}
	if manifest.Dataset.Project != project {
		return Manifest{}, fmt.Errorf("%w: resolver returned project %q for requested project %q", ErrInvalidActiveGeneration, manifest.Dataset.Project, project)
	}
	if !manifest.IsReady() {
		return Manifest{}, fmt.Errorf("%w: %s is %s", ErrGenerationNotReady, manifest.Dataset.Generation, manifest.State)
	}
	return manifest, nil
}
