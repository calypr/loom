package dataframe

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataset"
)

var (
	// ErrActiveGenerationConflict reports a caller-supplied generation that is
	// not the one selected by the configured active-manifest resolver. Failing
	// closed here prevents a catalog/compiled-query split across generations.
	ErrActiveGenerationConflict = errors.New("requested dataset generation conflicts with active generation")
)

// resolveActiveBuilder pins one execution request to the configured active
// READY manifest before any catalog or compiler work starts. Without a
// resolver the historical direct-Builder contract is unchanged: the builder's
// generation (or legacy null namespace) is used as supplied.
func (s *Service) resolveActiveBuilder(ctx context.Context, builder Builder) (Builder, error) {
	if s == nil || s.activeManifestResolver == nil {
		return builder, nil
	}
	manifest, err := dataset.ResolveReadyActiveManifest(ctx, s.activeManifestResolver, builder.Project)
	if err != nil {
		return Builder{}, fmt.Errorf("resolve active dataset generation: %w", err)
	}
	requested := normalizeDatasetGeneration(builder.DatasetGeneration)
	active := manifest.Dataset.Generation
	if requested != "" && requested != active {
		return Builder{}, fmt.Errorf("%w: project %q requested %q but active is %q", ErrActiveGenerationConflict, builder.Project, requested, active)
	}
	builder.DatasetGeneration = active
	return builder, nil
}
