package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/interpretation"

	"github.com/calypr/loom/internal/explorer"
)

type libraryRepository struct {
	library      interpretation.InterpretationLibrary
	revisions    map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision
	createSeen   *interpretation.InterpretationRevision
	createErr    error
	mutateCreate bool
}

func (r *libraryRepository) ListInterpretationLibraries(context.Context, string) ([]interpretation.InterpretationLibrary, error) {
	if r.library.ID == "" {
		return nil, nil
	}
	return []interpretation.InterpretationLibrary{r.library}, nil
}

func (r *libraryRepository) GetInterpretationRevision(_ context.Context, project string, id interpretation.InterpretationRevisionID) (*interpretation.InterpretationRevision, error) {
	revision, ok := r.revisions[id]
	if !ok || revision.Project != project {
		return nil, explorer.ErrNotFound
	}
	return &revision, nil
}

func (r *libraryRepository) CreateInterpretationRevision(_ context.Context, revision interpretation.InterpretationRevision, expectedParent *interpretation.InterpretationRevisionID) (*interpretation.InterpretationRevision, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	if existing, ok := r.revisions[revision.ID]; ok {
		return &existing, nil
	}
	if expectedParent == nil {
		if r.library.HeadRevisionID != "" {
			return nil, interpretation.ErrInterpretationParentConflict
		}
	} else if r.library.HeadRevisionID != *expectedParent {
		return nil, interpretation.ErrInterpretationParentConflict
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
