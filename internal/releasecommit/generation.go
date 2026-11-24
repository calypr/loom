package releasecommit

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	store "github.com/calypr/loom/internal/store/arango"
)

func ActivateGeneration(ctx context.Context, client store.TransactionalRowQueryer, lifecycle *datasetarango.Store, manifest dataset.Manifest) error {
	if !manifest.IsStaged() {
		return lifecycle.Activate(ctx, manifest)
	}
	active, err := lifecycle.ReadActiveRelease(ctx, manifest.Dataset.Project)
	if errors.Is(err, dataset.ErrNoActiveRelease) {
		return lifecycle.Activate(ctx, manifest)
	}
	if err != nil {
		return err
	}
	if active.Release.Generation == manifest.Dataset.Generation {
		return nil
	}
	service := dataset.ReleaseService{Manifests: lifecycle, Releases: lifecycle}
	request := dataset.ActivationRequest{
		Project: manifest.Dataset.Project, Generation: manifest.Dataset.Generation, ExpectedRevision: active.Revision,
	}
	candidate, err := service.Create(ctx, request)
	if err != nil {
		return err
	}
	_, err = GoLive(ctx, client, Request{Release: candidate, ExpectedRevision: active.Revision})
	return err
}
