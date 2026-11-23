package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
)

type libraryRepository struct {
	library      explorer.InterpretationLibrary
	revisions    map[explorer.InterpretationRevisionID]explorer.InterpretationRevision
	createSeen   *explorer.InterpretationRevision
	createErr    error
	mutateCreate bool
}

func (r *libraryRepository) ListInterpretationLibraries(context.Context, string) ([]explorer.InterpretationLibrary, error) {
	if r.library.ID == "" {
		return nil, nil
	}
	return []explorer.InterpretationLibrary{r.library}, nil
}

func (r *libraryRepository) GetInterpretationRevision(_ context.Context, project string, id explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	revision, ok := r.revisions[id]
	if !ok || revision.Project != project {
		return nil, explorer.ErrNotFound
	}
	return &revision, nil
}

func (r *libraryRepository) CreateInterpretationRevision(_ context.Context, revision explorer.InterpretationRevision, expectedParent *explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	if existing, ok := r.revisions[revision.ID]; ok {
		return &existing, nil
	}
	if expectedParent == nil {
		if r.library.HeadRevisionID != "" {
			return nil, explorer.ErrInterpretationParentConflict
		}
	} else if r.library.HeadRevisionID != *expectedParent {
		return nil, explorer.ErrInterpretationParentConflict
	}
	r.createSeen = &revision
	if r.mutateCreate {
		revision.Explanation = "mutated by adapter"
	}
	r.revisions[revision.ID] = revision
	r.library.HeadRevisionID = revision.ID
	r.library.HeadDigest = revision.ContentDigest
	return &revision, nil
}
