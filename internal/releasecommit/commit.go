package releasecommit

import (
	"context"

	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	store "github.com/calypr/loom/internal/store/arango"
)

type Request struct {
	Release                  dataset.ProjectRelease
	ExpectedRevision         int64
	RequireCurrentGeneration bool
	Collections              store.TransactionCollections
	Prepare                  func(context.Context, store.RowQueryer) (Participant, error)
}

type Participant func(context.Context, store.RowQueryer, dataset.ActiveRelease) error

func GoLive(ctx context.Context, client store.TransactionalRowQueryer, request Request) (dataset.ActiveRelease, error) {
	collections := request.Collections
	collections.Write = append([]string{datasetarango.LifecycleCollection}, collections.Write...)
	var active dataset.ActiveRelease
	err := client.WithTransaction(ctx, collections, func(ctx context.Context, tx store.RowQueryer) error {
		var apply Participant
		if request.Prepare != nil {
			var err error
			apply, err = request.Prepare(ctx, tx)
			if err != nil {
				return err
			}
		}
		swap := datasetarango.CompareAndSwapActivateRelease
		if request.RequireCurrentGeneration {
			swap = datasetarango.CompareAndSwapPublishRelease
		}
		var err error
		active, err = swap(ctx, tx, request.Release, request.ExpectedRevision)
		if err != nil {
			return err
		}
		if apply != nil {
			return apply(ctx, tx, active)
		}
		return nil
	})
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	return active, nil
}
