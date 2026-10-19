package lifecycle

import (
	"context"
	"errors"
	"testing"

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

func TestCreateInterpretationRevisionClassifiesRepositoryFailureAsInternal(t *testing.T) {
	parent := lifecyclePrepareInterpretation(t)
	repository := &libraryRepository{
		library:   explorer.InterpretationLibrary{ID: parent.LibraryID, Project: parent.Project},
		revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{},
		createErr: errors.New("database unavailable"),
	}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	_, err := service.CreateInterpretationRevision(context.Background(), CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), Applicability: parent.Applicability,
		Rules: parent.Rules, Explanation: parent.Explanation, Author: parent.Author,
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassInternal || lifecycleErr.Code != "INTERPRETATION_REVISION_CREATE_FAILED" {
		t.Fatalf("repository failure = %v", err)
	}
}

func TestCreateInterpretationRevisionRejectsRepositoryIntegrityMutation(t *testing.T) {
	parent := lifecyclePrepareInterpretation(t)
	repository := &libraryRepository{
		library:      explorer.InterpretationLibrary{ID: parent.LibraryID, Project: parent.Project},
		revisions:    map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{},
		mutateCreate: true,
	}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	_, err := service.CreateInterpretationRevision(context.Background(), CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), Applicability: parent.Applicability,
		Rules: parent.Rules, Explanation: parent.Explanation, Author: parent.Author,
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassInternal || lifecycleErr.Code != "INTERPRETATION_REVISION_INTEGRITY" {
		t.Fatalf("repository integrity error = %v", err)
	}
}

func TestInterpretationLibraryBrowseAndExactGetExpandImmutableHead(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	repository := &libraryRepository{
		library:   explorer.InterpretationLibrary{ID: revision.LibraryID, Project: revision.Project, HeadRevisionID: revision.ID, HeadDigest: revision.ContentDigest},
		revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision},
	}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	result, err := service.ListInterpretationLibraries(context.Background(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Project != "project-a" || len(result.Libraries) != 1 || result.Libraries[0].Head == nil || result.Libraries[0].Head.ID != revision.ID {
		t.Fatalf("library result = %#v", result)
	}
	got, err := service.GetInterpretationRevision(context.Background(), GetInterpretationRevisionRequest{Project: "project-a", RevisionID: string(revision.ID)})
	if err != nil || got.ID != revision.ID {
		t.Fatalf("exact revision = %#v, err=%v", got, err)
	}
	_, err = service.GetInterpretationRevision(context.Background(), GetInterpretationRevisionRequest{Project: "other-project", RevisionID: string(revision.ID)})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassNotFound {
		t.Fatalf("cross-project revision error = %v", err)
	}
}

func TestCreateInterpretationRevisionUsesHeadParentAndRejectsStaleParent(t *testing.T) {
	parent := lifecyclePrepareInterpretation(t)
	repository := &libraryRepository{
		library:   explorer.InterpretationLibrary{ID: parent.LibraryID, Project: parent.Project, HeadRevisionID: parent.ID, HeadDigest: parent.ContentDigest},
		revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{parent.ID: parent},
	}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	created, err := service.CreateInterpretationRevision(context.Background(), CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), ParentRevisionID: string(parent.ID), Applicability: parent.Applicability,
		Rules: parent.Rules, Explanation: "updated", Author: "researcher",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.createSeen == nil || repository.createSeen.ParentRevisionID == nil || *repository.createSeen.ParentRevisionID != parent.ID || created.ParentRevisionID == nil || *created.ParentRevisionID != parent.ID {
		t.Fatalf("created revision parent = %#v; repository saw %#v", created.ParentRevisionID, repository.createSeen)
	}
	oldHead := repository.library.HeadRevisionID
	repository.library.HeadRevisionID = "revision-newer"
	_, err = service.CreateInterpretationRevision(context.Background(), CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), ParentRevisionID: string(oldHead),
		Applicability: parent.Applicability, Rules: parent.Rules, Explanation: "stale", Author: "researcher",
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassConflict {
		t.Fatalf("stale parent error = %v", err)
	}
}

func TestCreateRootInterpretationRevisionRetriesIdempotently(t *testing.T) {
	parent := lifecyclePrepareInterpretation(t)
	repository := &libraryRepository{revisions: map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{}}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	request := CreateInterpretationRevisionRequest{Project: parent.Project, LibraryID: "new-library", Applicability: parent.Applicability, Rules: parent.Rules, Explanation: parent.Explanation, Author: parent.Author}
	first, err := service.CreateInterpretationRevision(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateInterpretationRevision(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.ContentDigest != second.ContentDigest || repository.library.HeadRevisionID != first.ID {
		t.Fatalf("root retry advanced revision: first=%#v second=%#v library=%#v", first, second, repository.library)
	}
}
