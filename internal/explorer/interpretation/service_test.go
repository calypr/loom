package interpretation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
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

func newService(t *testing.T, repository interpretation.Repository) *interpretation.Service {
	t.Helper()
	service, err := interpretation.NewService(interpretation.Config{
		Repository: repository,
		Errors: interpretation.ErrorFactory{
			Malformed: support.Malformed, Unavailable: support.Unavailable, Conflict: support.Conflict,
			NotFound: support.NotFound, Unprocessable: support.Unprocessable, Internal: support.Internal,
			PersistenceFailure: support.PersistenceFailure,
		},
		IsNotFound: func(err error) bool { return errors.Is(err, explorer.ErrNotFound) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCreateInterpretationRevisionClassifiesRepositoryFailureAsInternal(t *testing.T) {
	parent := explorerfixtures.PrepareInterpretationRevision(t, explorerfixtures.InterpretationRevisionInput())
	repository := &libraryRepository{
		library:   interpretation.InterpretationLibrary{ID: parent.LibraryID, Project: parent.Project},
		revisions: map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{},
		createErr: errors.New("database unavailable"),
	}
	service := newService(t, repository)
	_, err := service.CreateInterpretationRevision(context.Background(), interpretation.CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), Applicability: parent.Applicability,
		Rules: parent.Rules, Explanation: parent.Explanation, Author: parent.Author,
	})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "PERSISTENCE_UNAVAILABLE" {
		t.Fatalf("repository failure = %v", err)
	}
}

func TestCreateInterpretationRevisionRejectsRepositoryIntegrityMutation(t *testing.T) {
	parent := explorerfixtures.PrepareInterpretationRevision(t, explorerfixtures.InterpretationRevisionInput())
	repository := &libraryRepository{
		library:      interpretation.InterpretationLibrary{ID: parent.LibraryID, Project: parent.Project},
		revisions:    map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{},
		mutateCreate: true,
	}
	service := newService(t, repository)
	_, err := service.CreateInterpretationRevision(context.Background(), interpretation.CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), Applicability: parent.Applicability,
		Rules: parent.Rules, Explanation: parent.Explanation, Author: parent.Author,
	})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassInternal || lifecycleErr.Code != "INTERPRETATION_REVISION_INTEGRITY" {
		t.Fatalf("repository integrity error = %v", err)
	}
}

func TestInterpretationLibraryBrowseAndExactGetExpandImmutableHead(t *testing.T) {
	revision := explorerfixtures.PrepareInterpretationRevision(t, explorerfixtures.InterpretationRevisionInput())
	repository := &libraryRepository{
		library:   interpretation.InterpretationLibrary{ID: revision.LibraryID, Project: revision.Project, HeadRevisionID: revision.ID, HeadDigest: revision.ContentDigest},
		revisions: map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{revision.ID: revision},
	}
	service := newService(t, repository)
	result, err := service.ListInterpretationLibraries(context.Background(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Project != "project-a" || len(result.Libraries) != 1 || result.Libraries[0].Head == nil || result.Libraries[0].Head.ID != revision.ID {
		t.Fatalf("library result = %#v", result)
	}
	got, err := service.GetInterpretationRevision(context.Background(), interpretation.GetInterpretationRevisionRequest{Project: "project-a", RevisionID: string(revision.ID)})
	if err != nil || got.ID != revision.ID {
		t.Fatalf("exact revision = %#v, err=%v", got, err)
	}
	_, err = service.GetInterpretationRevision(context.Background(), interpretation.GetInterpretationRevisionRequest{Project: "other-project", RevisionID: string(revision.ID)})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassNotFound {
		t.Fatalf("cross-project revision error = %v", err)
	}
}

func TestCreateInterpretationRevisionUsesHeadParentAndRejectsStaleParent(t *testing.T) {
	parent := explorerfixtures.PrepareInterpretationRevision(t, explorerfixtures.InterpretationRevisionInput())
	repository := &libraryRepository{
		library:   interpretation.InterpretationLibrary{ID: parent.LibraryID, Project: parent.Project, HeadRevisionID: parent.ID, HeadDigest: parent.ContentDigest},
		revisions: map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{parent.ID: parent},
	}
	service := newService(t, repository)
	created, err := service.CreateInterpretationRevision(context.Background(), interpretation.CreateInterpretationRevisionRequest{
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
	_, err = service.CreateInterpretationRevision(context.Background(), interpretation.CreateInterpretationRevisionRequest{
		Project: parent.Project, LibraryID: string(parent.LibraryID), ParentRevisionID: string(oldHead),
		Applicability: parent.Applicability, Rules: parent.Rules, Explanation: "stale", Author: "researcher",
	})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassConflict {
		t.Fatalf("stale parent error = %v", err)
	}
}

func TestCreateRootInterpretationRevisionRetriesIdempotently(t *testing.T) {
	parent := explorerfixtures.PrepareInterpretationRevision(t, explorerfixtures.InterpretationRevisionInput())
	repository := &libraryRepository{revisions: map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{}}
	service := newService(t, repository)
	request := interpretation.CreateInterpretationRevisionRequest{Project: parent.Project, LibraryID: "new-library", Applicability: parent.Applicability, Rules: parent.Rules, Explanation: parent.Explanation, Author: parent.Author}
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
