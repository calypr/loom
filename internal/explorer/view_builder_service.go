package explorer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) viewBuilderRepository() (ViewBuilderRepository, error) {
	repository, ok := s.store.(ViewBuilderRepository)
	if !ok {
		return nil, fmt.Errorf("view storage is not configured")
	}
	return repository, nil
}

// ViewBuilderState reads presentation and exact retained publication metadata.
// GET does not initialize defaults or adopt a legacy owner's snapshot.
func (s *Service) ViewBuilderState(ctx context.Context, project string) (ViewBuilderState, error) {
	repository, err := s.viewBuilderRepository()
	if err != nil {
		return ViewBuilderState{}, err
	}
	storageProject := projectid.Legacy(project)
	settings, err := repository.GetViewBuilderSettings(ctx, storageProject)
	if err != nil {
		return ViewBuilderState{}, fmt.Errorf("read project view settings: %w", err)
	}
	settings.Project = projectid.Canonical(project)
	if settings.Catalogs == nil {
		settings.Catalogs = map[string]string{}
	}
	result := ViewBuilderState{Settings: settings, Configs: []ViewBuilderConfig{}, Catalogs: []ViewBuilderCatalog{}}
	owners, err := s.store.List(ctx, storageProject)
	if err != nil {
		return ViewBuilderState{}, fmt.Errorf("list project views: %w", err)
	}
	for _, owner := range owners {
		if owner.ProjectConfig == nil {
			continue
		}
		definition, err := cloneProjectConfigDefinition(*owner.ProjectConfig)
		if err != nil {
			return ViewBuilderState{}, fmt.Errorf("decode saved view %q: %w", owner.ExplorerID, err)
		}
		entry := ViewBuilderConfig{Config: definition, RevisionID: owner.ActiveRevisionID}
		if owner.ActiveRevisionID != "" {
			revision, err := s.store.GetRevision(ctx, owner.ActiveRevisionID)
			if err != nil {
				return ViewBuilderState{}, fmt.Errorf("read saved view diagnostics: %w", err)
			}
			if revision == nil || revision.Project != storageProject || revision.ExplorerID != owner.ExplorerID {
				return ViewBuilderState{}, fmt.Errorf("saved view revision identity is invalid")
			}
			entry.Diagnostics = append([]Diagnostic(nil), revision.Diagnostics...)
			entry.ExecutionID = revision.Publication.ExecutionID
		}
		result.Configs = append(result.Configs, entry)
	}
	sort.Slice(result.Configs, func(i, j int) bool { return result.Configs[i].Config.ID < result.Configs[j].Config.ID })
	recipes := make([]string, 0, len(settings.Catalogs))
	for name := range settings.Catalogs {
		recipes = append(recipes, name)
	}
	sort.Strings(recipes)
	for _, name := range recipes {
		revision, err := s.store.GetRevision(ctx, settings.Catalogs[name])
		if err != nil {
			return ViewBuilderState{}, fmt.Errorf("read published dataframe catalog: %w", err)
		}
		if revision == nil || revision.Project != storageProject || revision.Recipe.Name != name || revision.ID != settings.Catalogs[name] {
			return ViewBuilderState{}, fmt.Errorf("published dataframe catalog identity is invalid")
		}
		catalog, err := ViewBuilderCatalogFromRevision(*revision)
		if err != nil {
			return ViewBuilderState{}, fmt.Errorf("decode published dataframe catalog: %w", err)
		}
		result.Catalogs = append(result.Catalogs, catalog)
	}
	return result, nil
}

func (s *Service) SaveViewBuilderConfig(ctx context.Context, project, id string, request ViewBuilderSaveRequest, actor string) (ViewBuilderState, error) {
	repository, err := s.viewBuilderRepository()
	if err != nil {
		return ViewBuilderState{}, err
	}
	if !projectConfigIDPattern.MatchString(id) || id != request.Config.ID || strings.TrimSpace(request.CatalogRevisionID) == "" {
		return ViewBuilderState{}, fmt.Errorf("%w: config ID and published catalog revision are required", ErrInvalidViewBuilder)
	}
	storageProject := projectid.Legacy(project)
	settings, err := repository.GetViewBuilderSettings(ctx, storageProject)
	if err != nil {
		return ViewBuilderState{}, fmt.Errorf("read project view settings: %w", err)
	}
	if settings.Catalogs[request.Config.RecipeRef.Name] != request.CatalogRevisionID {
		return ViewBuilderState{}, ErrDraftConflict
	}
	snapshot, err := s.store.GetRevision(ctx, request.CatalogRevisionID)
	if err != nil {
		return ViewBuilderState{}, fmt.Errorf("read published dataframe catalog: %w", err)
	}
	if snapshot == nil || snapshot.Project != storageProject || snapshot.Recipe.Name != request.Config.RecipeRef.Name {
		return ViewBuilderState{}, fmt.Errorf("%w: published catalog does not belong to this project and recipe", ErrInvalidViewBuilder)
	}
	publication, err := PrepareViewBuilderConfig(request.Config, *snapshot, actor)
	if err != nil {
		return ViewBuilderState{}, err
	}
	if err := repository.SaveViewBuilderConfig(ctx, ViewBuilderMutation{
		Project: storageProject, ExpectedRevisionID: request.ExpectedRevisionID, CatalogRevisionID: request.CatalogRevisionID,
		Publication: publication, Actor: actor,
	}); err != nil {
		return ViewBuilderState{}, fmt.Errorf("save project view: %w", err)
	}
	return s.ViewBuilderState(ctx, project)
}

func (s *Service) DeleteViewBuilderConfig(ctx context.Context, project, id, expectedRevisionID string) (ViewBuilderState, error) {
	repository, err := s.viewBuilderRepository()
	if err != nil {
		return ViewBuilderState{}, err
	}
	if !projectConfigIDPattern.MatchString(id) || strings.TrimSpace(expectedRevisionID) == "" {
		return ViewBuilderState{}, fmt.Errorf("%w: view ID and expected revision are required", ErrInvalidViewBuilder)
	}
	if err := repository.DeleteViewBuilderConfig(ctx, projectid.Legacy(project), id, expectedRevisionID); err != nil {
		return ViewBuilderState{}, fmt.Errorf("delete project view: %w", err)
	}
	return s.ViewBuilderState(ctx, project)
}

func (s *Service) SaveViewBuilderDefaults(ctx context.Context, project string, request ViewBuilderDefaultsRequest) (ViewBuilderState, error) {
	repository, err := s.viewBuilderRepository()
	if err != nil {
		return ViewBuilderState{}, err
	}
	_, _, validFrame := parseViewBuilderFrameID(request.DefaultFrameID)
	if request.ExpectedVersion < 0 || !projectConfigIDPattern.MatchString(request.DefaultConfigID) || !validFrame {
		return ViewBuilderState{}, fmt.Errorf("%w: select a saved view and a qualified published dataframe", ErrInvalidViewBuilder)
	}
	if err := repository.SaveViewBuilderDefaults(ctx, projectid.Legacy(project), request); err != nil {
		return ViewBuilderState{}, fmt.Errorf("save project view defaults: %w", err)
	}
	return s.ViewBuilderState(ctx, project)
}

func (s *Service) AdoptViewBuilderCatalogs(ctx context.Context, project string) (ViewBuilderState, error) {
	repository, err := s.viewBuilderRepository()
	if err != nil {
		return ViewBuilderState{}, err
	}
	if err := repository.AdoptViewBuilderCatalogs(ctx, projectid.Legacy(project)); err != nil {
		return ViewBuilderState{}, fmt.Errorf("attach published dataframe catalogs: %w", err)
	}
	return s.ViewBuilderState(ctx, project)
}
