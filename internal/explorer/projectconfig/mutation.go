package projectconfig

import (
	"context"

	"github.com/calypr/loom/internal/explorer/viewer"
)

type MutationAccess struct {
	CheckState     func(context.Context, viewer.ViewBuilderState) (viewer.ViewBuilderState, error)
	CheckExecution func(context.Context, string) error
	StorageError   func(error) error
}

func (a MutationAccess) storageError(err error) error {
	if err == nil {
		return nil
	}
	return a.StorageError(err)
}

func (v *Service) SaveConfig(ctx context.Context, project, id, actor string, read func() (viewer.ViewBuilderSaveRequest, error), access MutationAccess) (viewer.ViewBuilderState, error) {
	return v.mutate(ctx, project, access, func() error {
		request, err := read()
		if err != nil {
			return err
		}
		err = v.saveViewBuilderConfig(ctx, project, id, request, actor)
		return access.storageError(err)
	})
}

func (v *Service) SaveDefaults(ctx context.Context, project string, read func() (viewer.ViewBuilderDefaultsRequest, error), access MutationAccess) (viewer.ViewBuilderState, error) {
	return v.mutate(ctx, project, access, func() error {
		request, err := read()
		if err != nil {
			return err
		}
		err = v.saveViewBuilderDefaults(ctx, project, request)
		return access.storageError(err)
	})
}

func (v *Service) DeleteConfig(ctx context.Context, project, id, expectedRevisionID string, access MutationAccess) (viewer.ViewBuilderState, error) {
	return v.mutate(ctx, project, access, func() error {
		err := v.deleteViewBuilderConfig(ctx, project, id, expectedRevisionID)
		return access.storageError(err)
	})
}

func (v *Service) AdoptCatalogs(ctx context.Context, project string, access MutationAccess) (viewer.ViewBuilderState, error) {
	return v.mutate(ctx, project, access, func() error {
		owners, err := v.List(ctx, project)
		if err != nil {
			return access.storageError(err)
		}
		for _, owner := range owners {
			if owner.ProjectConfig == nil || owner.ActiveRevisionID == "" {
				continue
			}
			revision, err := v.GetRevision(ctx, owner.ActiveRevisionID)
			if err != nil {
				return access.storageError(err)
			}
			if err := access.CheckExecution(ctx, revision.Publication.ExecutionID); err != nil {
				return err
			}
		}
		err = v.adoptViewBuilderCatalogs(ctx, project)
		return access.storageError(err)
	})
}

func (v *Service) mutate(ctx context.Context, project string, access MutationAccess, write func() error) (viewer.ViewBuilderState, error) {
	if _, err := v.checkedState(ctx, project, access); err != nil {
		return viewer.ViewBuilderState{}, err
	}
	if err := write(); err != nil {
		return viewer.ViewBuilderState{}, err
	}
	return v.checkedState(ctx, project, access)
}

func (v *Service) checkedState(ctx context.Context, project string, access MutationAccess) (viewer.ViewBuilderState, error) {
	state, err := v.ViewBuilderState(ctx, project)
	if err != nil {
		return state, access.storageError(err)
	}
	return access.CheckState(ctx, state)
}
