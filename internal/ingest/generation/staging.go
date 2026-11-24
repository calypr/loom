package generation

import (
	"context"

	"github.com/calypr/loom/internal/dataset"
)

type manifestStager interface {
	TransitionManifest(context.Context, dataset.Manifest, dataset.State) (dataset.Manifest, error)
}

func StageManifest(ctx context.Context, store manifestStager, candidate dataset.Manifest, completeInventory func() error) (dataset.Manifest, error) {
	if err := completeInventory(); err != nil {
		return dataset.Manifest{}, err
	}
	return store.TransitionManifest(ctx, candidate, dataset.StateStaged)
}
