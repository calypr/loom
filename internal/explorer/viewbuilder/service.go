package viewbuilder

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/projectconfig"
	"github.com/calypr/loom/internal/projectid"
)

var projectConfigIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Service struct {
	explorers *explorer.Service
	repo      explorer.ViewBuilderRepository
}

func New(explorers *explorer.Service) *Service {
	var repository explorer.ViewBuilderRepository
	if explorers != nil {
		repository = explorers.ViewBuilderStorage()
	}
	return &Service{explorers: explorers, repo: repository}
}

func (v *Service) repository() (explorer.ViewBuilderRepository, error) {
	if v == nil || v.repo == nil {
		return nil, fmt.Errorf("view storage is not configured")
	}
	return v.repo, nil
}

func (v *Service) ViewBuilderState(ctx context.Context, project string) (explorer.ViewBuilderState, error) {
	repository, err := v.repository()
	if err != nil {
		return explorer.ViewBuilderState{}, err
	}
	storageProject := projectid.Legacy(project)
	settings, err := repository.GetViewBuilderSettings(ctx, storageProject)
	if err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("read project view settings: %w", err)
	}
	settings.Project = projectid.Canonical(project)
	if settings.Catalogs == nil {
		settings.Catalogs = map[string]string{}
	}
	result := explorer.ViewBuilderState{Settings: settings, Configs: []explorer.ViewBuilderConfig{}, Catalogs: []explorer.ViewBuilderCatalog{}}
	owners, err := v.explorers.List(ctx, storageProject)
	if err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("list project views: %w", err)
	}
	dataframeTitles := make(map[string]string)
	for _, owner := range owners {
		if owner.ProjectConfig == nil {
			if owner.ActiveRevisionID != "" && owner.Title != "" {
				revision, err := v.explorers.GetRevision(ctx, owner.ActiveRevisionID)
				if err != nil {
					return explorer.ViewBuilderState{}, fmt.Errorf("read published dataframe title: %w", err)
				}
				if revision != nil && revision.Project == storageProject && revision.ExplorerID == owner.ExplorerID &&
					revision.Publication.ExecutionID != "" && settings.Catalogs[revision.Recipe.Name] != "" {
					dataframeTitles[revision.Recipe.Name] = owner.Title
				}
			}
			continue
		}
		definition, err := explorer.CloneProjectConfigDefinition(*owner.ProjectConfig)
		if err != nil {
			return explorer.ViewBuilderState{}, fmt.Errorf("decode saved view %q: %w", owner.ExplorerID, err)
		}
		entry := explorer.ViewBuilderConfig{Config: definition, RevisionID: owner.ActiveRevisionID}
		if owner.ActiveRevisionID != "" {
			revision, err := v.explorers.GetRevision(ctx, owner.ActiveRevisionID)
			if err != nil {
				return explorer.ViewBuilderState{}, fmt.Errorf("read saved view diagnostics: %w", err)
			}
			if revision == nil || revision.Project != storageProject || revision.ExplorerID != owner.ExplorerID {
				return explorer.ViewBuilderState{}, fmt.Errorf("saved view revision identity is invalid")
			}
			entry.Diagnostics = append([]explorer.Diagnostic(nil), revision.Diagnostics...)
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
		revision, err := v.explorers.GetRevision(ctx, settings.Catalogs[name])
		if err != nil {
			return explorer.ViewBuilderState{}, fmt.Errorf("read published dataframe catalog: %w", err)
		}
		if revision == nil || revision.Project != storageProject || revision.Recipe.Name != name || revision.ID != settings.Catalogs[name] {
			return explorer.ViewBuilderState{}, fmt.Errorf("published dataframe catalog identity is invalid")
		}
		catalog, err := explorer.ViewBuilderCatalogFromRevision(*revision)
		if err != nil {
			return explorer.ViewBuilderState{}, fmt.Errorf("decode published dataframe catalog: %w", err)
		}
		catalog.Title = dataframeTitles[name]
		result.Catalogs = append(result.Catalogs, catalog)
	}
	return result, nil
}

func (v *Service) SaveViewBuilderConfig(ctx context.Context, project, id string, request explorer.ViewBuilderSaveRequest, actor string) (explorer.ViewBuilderState, error) {
	repository, err := v.repository()
	if err != nil {
		return explorer.ViewBuilderState{}, err
	}
	if !projectConfigIDPattern.MatchString(id) || id != request.Config.ID || strings.TrimSpace(request.CatalogRevisionID) == "" {
		return explorer.ViewBuilderState{}, fmt.Errorf("%w: config ID and published catalog revision are required", explorer.ErrInvalidViewBuilder)
	}
	storageProject := projectid.Legacy(project)
	settings, err := repository.GetViewBuilderSettings(ctx, storageProject)
	if err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("read project view settings: %w", err)
	}
	if settings.Catalogs[request.Config.RecipeRef.Name] != request.CatalogRevisionID {
		return explorer.ViewBuilderState{}, explorer.ErrDraftConflict
	}
	snapshot, err := v.explorers.GetRevision(ctx, request.CatalogRevisionID)
	if err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("read published dataframe catalog: %w", err)
	}
	if snapshot == nil || snapshot.Project != storageProject || snapshot.Recipe.Name != request.Config.RecipeRef.Name {
		return explorer.ViewBuilderState{}, fmt.Errorf("%w: published catalog does not belong to this project and recipe", explorer.ErrInvalidViewBuilder)
	}
	publication, err := projectconfig.PrepareViewBuilderConfig(request.Config, *snapshot, actor)
	if err != nil {
		return explorer.ViewBuilderState{}, err
	}
	if err := repository.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: storageProject, ExpectedRevisionID: request.ExpectedRevisionID, CatalogRevisionID: request.CatalogRevisionID,
		Publication: publication, Actor: actor,
	}); err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("save project view: %w", err)
	}
	return v.ViewBuilderState(ctx, project)
}

func (v *Service) DeleteViewBuilderConfig(ctx context.Context, project, id, expectedRevisionID string) (explorer.ViewBuilderState, error) {
	repository, err := v.repository()
	if err != nil {
		return explorer.ViewBuilderState{}, err
	}
	if !projectConfigIDPattern.MatchString(id) || strings.TrimSpace(expectedRevisionID) == "" {
		return explorer.ViewBuilderState{}, fmt.Errorf("%w: view ID and expected revision are required", explorer.ErrInvalidViewBuilder)
	}
	if err := repository.DeleteViewBuilderConfig(ctx, projectid.Legacy(project), id, expectedRevisionID); err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("delete project view: %w", err)
	}
	return v.ViewBuilderState(ctx, project)
}

func (v *Service) SaveViewBuilderDefaults(ctx context.Context, project string, request explorer.ViewBuilderDefaultsRequest) (explorer.ViewBuilderState, error) {
	repository, err := v.repository()
	if err != nil {
		return explorer.ViewBuilderState{}, err
	}
	_, _, validFrame := parseDefaultFrameID(request.DefaultFrameID)
	if request.ExpectedVersion < 0 || !projectConfigIDPattern.MatchString(request.DefaultConfigID) || !validFrame {
		return explorer.ViewBuilderState{}, fmt.Errorf("%w: select a saved view and a qualified published dataframe", explorer.ErrInvalidViewBuilder)
	}
	if err := repository.SaveViewBuilderDefaults(ctx, projectid.Legacy(project), request); err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("save project view defaults: %w", err)
	}
	return v.ViewBuilderState(ctx, project)
}

func (v *Service) AdoptViewBuilderCatalogs(ctx context.Context, project string) (explorer.ViewBuilderState, error) {
	repository, err := v.repository()
	if err != nil {
		return explorer.ViewBuilderState{}, err
	}
	if err := repository.AdoptViewBuilderCatalogs(ctx, projectid.Legacy(project)); err != nil {
		return explorer.ViewBuilderState{}, fmt.Errorf("attach published dataframe catalogs: %w", err)
	}
	return v.ViewBuilderState(ctx, project)
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

func (v *Service) GetRevision(ctx context.Context, revisionID string) (*explorer.Revision, error) {
	if v == nil || v.explorers == nil {
		return nil, fmt.Errorf("Explorer service is not configured")
	}
	return v.explorers.GetRevision(ctx, revisionID)
}
