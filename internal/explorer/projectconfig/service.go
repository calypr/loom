package projectconfig

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

var projectConfigIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Service struct {
	explorers *explorer.Service
	revisions explorer.RevisionReader
	repo      explorer.ViewBuilderRepository
}

func New(explorers *explorer.Service, revisions explorer.RevisionReader, repository explorer.ViewBuilderRepository) *Service {
	return &Service{explorers: explorers, revisions: revisions, repo: repository}
}

func (v *Service) repository() (explorer.ViewBuilderRepository, error) {
	if v == nil || v.repo == nil {
		return nil, fmt.Errorf("view storage is not configured")
	}
	return v.repo, nil
}

func (v *Service) ViewBuilderState(ctx context.Context, project string) (viewer.ViewBuilderState, error) {
	repository, err := v.repository()
	if err != nil {
		return viewer.ViewBuilderState{}, err
	}
	publicProject, storageProject := projectid.CanonicalAndLegacy(project)
	settings, err := repository.GetViewBuilderSettings(ctx, storageProject)
	if err != nil {
		return viewer.ViewBuilderState{}, fmt.Errorf("read project view settings: %w", err)
	}
	settings.Project = publicProject
	if settings.Catalogs == nil {
		settings.Catalogs = map[string]string{}
	}
	result := viewer.ViewBuilderState{Settings: settings, Configs: []viewer.ViewBuilderConfig{}, Catalogs: []viewer.ViewBuilderCatalog{}}
	owners, err := v.explorers.List(ctx, storageProject)
	if err != nil {
		return viewer.ViewBuilderState{}, fmt.Errorf("list project views: %w", err)
	}
	dataframeTitles := make(map[string]string)
	for _, owner := range owners {
		if owner.ProjectConfig == nil {
			if owner.ActiveRevisionID != "" && owner.Title != "" {
				revision, err := v.revisions.GetRevision(ctx, strings.TrimSpace(owner.ActiveRevisionID))
				if err != nil {
					return viewer.ViewBuilderState{}, fmt.Errorf("read published dataframe title: %w", err)
				}
				if revision != nil && revision.Project == storageProject && revision.ExplorerID == owner.ExplorerID &&
					revision.Publication.ExecutionID != "" && settings.Catalogs[revision.Recipe.Name] != "" {
					dataframeTitles[revision.Recipe.Name] = owner.Title
				}
			}
			continue
		}
		definition, err := explorerconfig.CloneProjectConfigDefinition(*owner.ProjectConfig)
		if err != nil {
			return viewer.ViewBuilderState{}, fmt.Errorf("decode saved view %q: %w", owner.ExplorerID, err)
		}
		entry := viewer.ViewBuilderConfig{Config: definition, RevisionID: owner.ActiveRevisionID}
		if owner.ActiveRevisionID != "" {
			revision, err := v.revisions.GetRevision(ctx, strings.TrimSpace(owner.ActiveRevisionID))
			if err != nil {
				return viewer.ViewBuilderState{}, fmt.Errorf("read saved view diagnostics: %w", err)
			}
			if revision == nil || revision.Project != storageProject || revision.ExplorerID != owner.ExplorerID {
				return viewer.ViewBuilderState{}, fmt.Errorf("saved view revision identity is invalid")
			}
			entry.Diagnostics = append([]viewer.Diagnostic(nil), revision.Diagnostics...)
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
		revision, err := v.revisions.GetRevision(ctx, strings.TrimSpace(settings.Catalogs[name]))
		if err != nil {
			return viewer.ViewBuilderState{}, fmt.Errorf("read published dataframe catalog: %w", err)
		}
		if revision == nil || revision.Project != storageProject || revision.Recipe.Name != name || revision.ID != settings.Catalogs[name] {
			return viewer.ViewBuilderState{}, fmt.Errorf("published dataframe catalog identity is invalid")
		}
		catalog, err := explorer.ViewBuilderCatalogFromRevision(*revision)
		if err != nil {
			return viewer.ViewBuilderState{}, fmt.Errorf("decode published dataframe catalog: %w", err)
		}
		catalog.Title = dataframeTitles[name]
		result.Catalogs = append(result.Catalogs, catalog)
	}
	return result, nil
}

func (v *Service) saveViewBuilderConfig(ctx context.Context, project, id string, request viewer.ViewBuilderSaveRequest, actor string) error {
	repository, err := v.repository()
	if err != nil {
		return err
	}
	if !projectConfigIDPattern.MatchString(id) || id != request.Config.ID || strings.TrimSpace(request.CatalogRevisionID) == "" {
		return fmt.Errorf("%w: config ID and published catalog revision are required", viewer.ErrInvalidViewBuilder)
	}
	storageProject := projectid.Legacy(project)
	settings, err := repository.GetViewBuilderSettings(ctx, storageProject)
	if err != nil {
		return fmt.Errorf("read project view settings: %w", err)
	}
	if settings.Catalogs[request.Config.RecipeRef.Name] != request.CatalogRevisionID {
		return explorer.ErrDraftConflict
	}
	snapshot, err := v.revisions.GetRevision(ctx, strings.TrimSpace(request.CatalogRevisionID))
	if err != nil {
		return fmt.Errorf("read published dataframe catalog: %w", err)
	}
	if snapshot == nil || snapshot.Project != storageProject || snapshot.Recipe.Name != request.Config.RecipeRef.Name {
		return fmt.Errorf("%w: published catalog does not belong to this project and recipe", viewer.ErrInvalidViewBuilder)
	}
	publication, err := PrepareViewBuilderConfig(request.Config, *snapshot, actor)
	if err != nil {
		return err
	}
	if err := repository.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: storageProject, ExpectedRevisionID: request.ExpectedRevisionID, CatalogRevisionID: request.CatalogRevisionID,
		Publication: publication, Actor: actor,
	}); err != nil {
		return fmt.Errorf("save project view: %w", err)
	}
	return nil
}

func (v *Service) deleteViewBuilderConfig(ctx context.Context, project, id, expectedRevisionID string) error {
	repository, err := v.repository()
	if err != nil {
		return err
	}
	if !projectConfigIDPattern.MatchString(id) || strings.TrimSpace(expectedRevisionID) == "" {
		return fmt.Errorf("%w: view ID and expected revision are required", viewer.ErrInvalidViewBuilder)
	}
	if err := repository.DeleteViewBuilderConfig(ctx, projectid.Legacy(project), id, expectedRevisionID); err != nil {
		return fmt.Errorf("delete project view: %w", err)
	}
	return nil
}

func (v *Service) saveViewBuilderDefaults(ctx context.Context, project string, request viewer.ViewBuilderDefaultsRequest) error {
	repository, err := v.repository()
	if err != nil {
		return err
	}
	_, _, validFrame := parseDefaultFrameID(request.DefaultFrameID)
	if request.ExpectedVersion < 0 || !projectConfigIDPattern.MatchString(request.DefaultConfigID) || !validFrame {
		return fmt.Errorf("%w: select a saved view and a qualified published dataframe", viewer.ErrInvalidViewBuilder)
	}
	if err := repository.SaveViewBuilderDefaults(ctx, projectid.Legacy(project), request); err != nil {
		return fmt.Errorf("save project view defaults: %w", err)
	}
	return nil
}

func (v *Service) adoptViewBuilderCatalogs(ctx context.Context, project string) error {
	repository, err := v.repository()
	if err != nil {
		return err
	}
	if err := repository.AdoptViewBuilderCatalogs(ctx, projectid.Legacy(project)); err != nil {
		return fmt.Errorf("attach published dataframe catalogs: %w", err)
	}
	return nil
}

func parseDefaultFrameID(id string) (string, string, bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	if parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (v *Service) List(ctx context.Context, project string) ([]explorer.Explorer, error) {
	if v == nil || v.explorers == nil {
		return nil, fmt.Errorf("Explorer service is not configured")
	}
	return v.explorers.List(ctx, project)
}
