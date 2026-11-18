package explorer

import (
	"context"
	"errors"
	"testing"
)

type viewBuilderServiceStore struct {
	Store
	ViewBuilderRepository
	settings    ViewBuilderSettings
	owners      []Explorer
	revisions   map[string]Revision
	writes      int
	lastProject string
}

func (s *viewBuilderServiceStore) List(_ context.Context, project string) ([]Explorer, error) {
	s.lastProject = project
	return s.owners, nil
}
func (s *viewBuilderServiceStore) GetRevision(_ context.Context, id string) (*Revision, error) {
	value, ok := s.revisions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &value, nil
}
func (s *viewBuilderServiceStore) GetViewBuilderSettings(context.Context, string) (ViewBuilderSettings, error) {
	return s.settings, nil
}
func (s *viewBuilderServiceStore) SaveViewBuilderConfig(_ context.Context, request ViewBuilderMutation) error {
	s.writes++
	definition := request.Publication.Definition
	revision := request.Publication.Revision
	revision.Status = RevisionActive
	s.revisions[revision.ID] = revision
	s.owners = []Explorer{{Project: request.Project, ExplorerID: definition.ID, ProjectConfig: &definition, ActiveRevisionID: revision.ID}}
	return nil
}

func TestViewBuilderServiceGETDoesNotInitializeProject(t *testing.T) {
	store := &viewBuilderServiceStore{}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
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
		settings:  ViewBuilderSettings{Version: 1, Catalogs: map[string]string{resolved.Bundle.Name: snapshot.ID}},
		revisions: map[string]Revision{snapshot.ID: snapshot},
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := ProjectConfig{ID: "custom", Title: "Custom", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []ConfigView{{ID: "documents", Title: "Documents", Output: "Documents", Table: ConfigTable{Columns: []ConfigColumn{{Column: "document_reference_id", Visible: true, Label: "My ID"}}}}}}
	request := ViewBuilderSaveRequest{CatalogRevisionID: snapshot.ID, Config: config}
	state, err := service.SaveViewBuilderConfig(context.Background(), "HTAN_INT/BForePC", config.ID, request, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 || len(state.Configs) != 1 || state.Configs[0].Config.Title != "Custom" {
		t.Fatalf("save=%+v writes=%d", state, store.writes)
	}
	revision := store.revisions[state.Configs[0].RevisionID]
	if revision.Publication.ExecutionID != published.ID || revision.SourceGeneration != published.DatasetGeneration || revision.CreatedBy != "editor" {
		t.Fatalf("view save changed execution or lost actor: %+v", revision.Publication)
	}
	request.CatalogRevisionID = "stale-catalog"
	_, err = service.SaveViewBuilderConfig(context.Background(), "HTAN_INT/BForePC", config.ID, request, "editor")
	if !errors.Is(err, ErrDraftConflict) || store.writes != 1 {
		t.Fatalf("stale catalog err=%v writes=%d", err, store.writes)
	}
	request.CatalogRevisionID = snapshot.ID
	_, err = service.SaveViewBuilderConfig(context.Background(), "different/project", config.ID, request, "editor")
	if !errors.Is(err, ErrInvalidViewBuilder) || store.writes != 1 {
		t.Fatalf("foreign project err=%v writes=%d", err, store.writes)
	}
}
