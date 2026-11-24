package dataset

import (
	"context"
	"errors"
	"sync/atomic"
)

type ReleaseRuntime struct {
	service                          *ReleaseService
	activationConflictOnce           atomic.Bool
	repositoryActivationConflictOnce atomic.Bool
}

func NewReleaseRuntime(service *ReleaseService, activationConflictOnce, repositoryActivationConflictOnce bool) *ReleaseRuntime {
	runtime := &ReleaseRuntime{service: service}
	runtime.activationConflictOnce.Store(activationConflictOnce)
	runtime.repositoryActivationConflictOnce.Store(repositoryActivationConflictOnce)
	return runtime
}

func (r *ReleaseRuntime) PrepareRepository(ctx context.Context, project, generation string, selectors []DataframeSelector) (ProjectRelease, int64, error) {
	expectedRevision := int64(0)
	devFaultArmed := r.repositoryActivationConflictOnce.CompareAndSwap(true, false)
	active, err := r.service.Active(ctx, project)
	if err == nil {
		expectedRevision = active.Revision
		if devFaultArmed {
			expectedRevision++
		}
	} else if !errors.Is(err, ErrNoActiveRelease) {
		return ProjectRelease{}, 0, err
	}
	release, err := r.service.Create(ctx, ActivationRequest{Project: project, Generation: generation, GitCommit: generation, OptionalSelectors: selectors})
	return release, expectedRevision, err
}

func (r *ReleaseRuntime) Prepare(ctx context.Context, project, generation string, selectors []DataframeSelector) (ProjectRelease, int64, error) {
	expectedRevision := int64(0)
	active, err := r.service.Active(ctx, project)
	if err == nil {
		expectedRevision = active.Revision
	} else if !errors.Is(err, ErrNoActiveRelease) {
		return ProjectRelease{}, 0, err
	}
	release, err := r.service.Create(ctx, ActivationRequest{
		Project: project, Generation: generation, GitCommit: generation,
		OptionalSelectors: selectors,
	})

	if err == nil && r.activationConflictOnce.CompareAndSwap(true, false) {
		expectedRevision++
	}
	return release, expectedRevision, err
}
