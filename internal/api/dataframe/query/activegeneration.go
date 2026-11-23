package queryapi

import (
	"context"
	"errors"

	publication "github.com/calypr/loom/internal/dataset"
)

func (s *Service) resolveActiveGeneration(ctx context.Context, project string) (string, error) {
	if s == nil || s.activeManifestResolver == nil {
		return "", nil
	}
	manifest, err := publication.ResolveActive(ctx, s.activeManifestResolver, project)
	if err != nil {
		if errors.Is(err, publication.ErrNoActiveGeneration) {
			return "", nil
		}
		return "", queryBackend(err)
	}
	return manifest.Dataset.Generation, nil
}
