package projectconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/viewer"

	explorerconfig "github.com/calypr/loom/internal/explorer/config"

	"github.com/calypr/loom/internal/explorer"
)

type viewBuilderServiceStore struct {
	explorer.ExplorerStore
	explorer.RevisionStore
	explorer.ViewBuilderRepository
	settings      viewer.ViewBuilderSettings
	owners        []explorer.Explorer
	revisions     map[string]explorer.Revision
	writes        int
	settingsReads int
	ownerLists    int
	lastProject   string
	settingsErr   error
	afterWrite    func()
}

func (s *viewBuilderServiceStore) List(_ context.Context, project string) ([]explorer.Explorer, error) {
	s.ownerLists++
	s.lastProject = project
	return s.owners, nil
}
func (s *viewBuilderServiceStore) GetRevision(_ context.Context, id string) (*explorer.Revision, error) {
	value, ok := s.revisions[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}
func (s *viewBuilderServiceStore) GetViewBuilderSettings(context.Context, string) (viewer.ViewBuilderSettings, error) {
	s.settingsReads++
	return s.settings, s.settingsErr
}
func (s *viewBuilderServiceStore) SaveViewBuilderConfig(_ context.Context, request explorer.ViewBuilderMutation) error {
	s.writes++
	definition := request.Publication.Definition
	revision := request.Publication.Revision
	revision.Status = explorer.RevisionActive
	s.revisions[revision.ID] = revision
	s.owners = []explorer.Explorer{{Project: request.Project, ExplorerID: definition.ID, ProjectConfig: &definition, ActiveRevisionID: revision.ID}}
	if s.afterWrite != nil {
		s.afterWrite()
	}
	return nil
}

func TestViewBuilderServiceGETDoesNotInitializeProject(t *testing.T) {
	store := &viewBuilderServiceStore{}
	explorers, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service := New(explorers)
	state, err := service.ViewBuilderState(context.Background(), "HTAN_INT/BForePC")
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 || len(state.Configs) != 0 || len(state.Catalogs) != 0 || state.Settings.Version != 0 {
		t.Fatalf("GET initialized project: %+v writes=%d", state, store.writes)
	}
	if store.lastProject != "HTAN_INT-BForePC" || state.Settings.Project != "HTAN_INT/BForePC" {
		t.Fatalf("project boundary storage=%q public=%q", store.lastProject, state.Settings.Project)
	}
	if state.Configs == nil || state.Catalogs == nil || state.Settings.Catalogs == nil {
		t.Fatal("GET returned null collections")
	}
}

func TestViewBuilderServiceSavesPresentationAgainstExactCatalog(t *testing.T) {
	resolved, published, _ := projectConfigPublicationFixture()
	snapshot, err := PrepareViewBuilderCatalogSnapshot(resolved, published, "commit-a")
	if err != nil {
		t.Fatal(err)
	}
	store := &viewBuilderServiceStore{
		settings:  viewer.ViewBuilderSettings{Version: 1, Catalogs: map[string]string{resolved.Bundle.Name: snapshot.ID}},
		revisions: map[string]explorer.Revision{snapshot.ID: snapshot},
	}
	explorers, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service := New(explorers)
	config := explorerconfig.ProjectConfig{ID: "custom", Title: "Custom", RecipeRef: explorerconfig.ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []explorerconfig.ConfigView{{ID: "documents", Title: "Documents", Output: "Documents", Table: explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{{Column: "document_reference_id", Visible: true, Label: "My ID"}}}}}}
	request := viewer.ViewBuilderSaveRequest{CatalogRevisionID: snapshot.ID, Config: config}
	stateChecks := 0
	access := MutationAccess{
		CheckState: func(_ context.Context, state viewer.ViewBuilderState) (viewer.ViewBuilderState, error) {
			stateChecks++
			return state, nil
		},
		CheckExecution: func(context.Context, string) error { return nil },
		StorageError:   func(err error) error { return err },
	}
	store.settingsReads = 0
	store.ownerLists = 0
	state, err := service.SaveConfig(context.Background(), "HTAN_INT/BForePC", config.ID, "editor", func() (viewer.ViewBuilderSaveRequest, error) {
		return request, nil
	}, access)
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 || len(state.Configs) != 1 || state.Configs[0].Config.Title != "Custom" {
		t.Fatalf("save=%+v writes=%d", state, store.writes)
	}
	if store.settingsReads != 3 || store.ownerLists != 2 || stateChecks != 2 {
		t.Fatalf("mutation reads settings=%d owners=%d stateChecks=%d, want settings=3 owners=2 checks=2", store.settingsReads, store.ownerLists, stateChecks)
	}
	revision := store.revisions[state.Configs[0].RevisionID]
	if revision.Publication.ExecutionID != published.ID || revision.SourceGeneration != published.DatasetGeneration || revision.CreatedBy != "editor" {
		t.Fatalf("view save changed execution or lost actor: %+v", revision.Publication)
	}
	request.CatalogRevisionID = "stale-catalog"
	_, err = service.SaveConfig(context.Background(), "HTAN_INT/BForePC", config.ID, "editor", func() (viewer.ViewBuilderSaveRequest, error) {
		return request, nil
	}, access)
	if !errors.Is(err, explorer.ErrDraftConflict) || store.writes != 1 {
		t.Fatalf("stale catalog err=%v writes=%d", err, store.writes)
	}
	request.CatalogRevisionID = snapshot.ID
	if err := service.saveViewBuilderConfig(context.Background(), "different/project", config.ID, request, "editor"); !errors.Is(err, viewer.ErrInvalidViewBuilder) || store.writes != 1 {
		t.Fatalf("foreign project err=%v writes=%d", err, store.writes)
	}

	request.ExpectedRevisionID = store.owners[0].ActiveRevisionID
	read := func() (viewer.ViewBuilderSaveRequest, error) { return request, nil }

	denied := errors.New("execution access denied")
	checks := 0
	access.CheckState = func(_ context.Context, state viewer.ViewBuilderState) (viewer.ViewBuilderState, error) {
		checks++
		if checks == 2 {
			return viewer.ViewBuilderState{}, denied
		}
		return state, nil
	}
	_, err = service.SaveConfig(context.Background(), "HTAN_INT/BForePC", config.ID, "editor", read, access)
	if !errors.Is(err, denied) || store.writes != 2 || checks != 2 {
		t.Fatalf("post-write authorization err=%v writes=%d checks=%d", err, store.writes, checks)
	}

	readFailed := errors.New("settings read failed")
	mapped := errors.New("mapped storage failure")
	store.afterWrite = func() { store.settingsErr = readFailed }
	access.StorageError = func(err error) error {
		if !errors.Is(err, readFailed) {
			t.Fatalf("storage error = %v", err)
		}
		return mapped
	}
	checks = 0
	_, err = service.SaveConfig(context.Background(), "HTAN_INT/BForePC", config.ID, "editor", read, access)
	if !errors.Is(err, mapped) || store.writes != 3 || checks != 1 {
		t.Fatalf("post-write read err=%v writes=%d checks=%d", err, store.writes, checks)
	}
}

func (s *viewBuilderServiceStore) Get(_ context.Context, project, id string) (*explorer.Explorer, error) {
	for _, owner := range s.owners {
		if owner.Project == project && owner.ExplorerID == id {
			return &owner, nil
		}
	}
	return nil, explorer.ErrNotFound
}
