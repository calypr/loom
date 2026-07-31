package queryapi

import (
	"context"
	"errors"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataset"
)

// resolveActiveGeneration pins builder-side catalog discovery to the exact
// READY manifest selected for this request. Without a configured resolver the
// legacy catalog namespace remains the explicit empty generation.
func (s *Service) resolveActiveGeneration(ctx context.Context, project string) (string, error) {
	if s == nil || s.activeManifestResolver == nil {
		return "", nil
	}
	manifest, err := dataset.ResolveReadyActiveManifest(ctx, s.activeManifestResolver, project)
	if err != nil {
		if errors.Is(err, dataset.ErrNoActiveGeneration) {
			return "", dataframeerrors.Wrap(err, dataframeerrors.CodeNoActiveGeneration, "")
		}
		return "", queryBackend(err)
	}
	return manifest.Dataset.Generation, nil
}
