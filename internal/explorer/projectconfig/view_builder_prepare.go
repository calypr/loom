package projectconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func invalidProjectConfig(format string, args ...any) error {
	return fmt.Errorf("%w: %s", explorer.ErrInvalidViewBuilder, fmt.Sprintf(format, args...))
}

func validateProjectConfigDefinitions(configs []explorer.ProjectConfig) error {
	for index, config := range configs {
		if err := config.Validate(); err != nil {
			return fmt.Errorf("configs[%d] %q: %w", index, config.ID, err)
		}
	}
	return nil
}

func PrepareViewBuilderCatalogSnapshot(
	resolved execution.Resolved,
	executionValue publication.BundleExecution,
	sourceCommit string,
) (explorer.Revision, error) {
	initial, err := InitialProjectConfigForRecipe(resolved.Bundle)
	if err != nil {
		return explorer.Revision{}, err
	}
	prepared, err := PrepareProjectConfigs([]explorer.ProjectConfig{initial}, resolved, executionValue, sourceCommit)
	if err != nil {
		return explorer.Revision{}, fmt.Errorf("prepare View Builder catalog metadata: %w", err)
	}
	if len(prepared) != 1 {
		return explorer.Revision{}, invalidProjectConfig("initial catalog preparation returned %d revisions", len(prepared))
	}
	revision := prepared[0].Revision
	revision.ID, err = viewBuilderCatalogRevisionID(executionValue, resolved.ResolvedSchemaDigest, sourceCommit)
	if err != nil {
		return explorer.Revision{}, fmt.Errorf("identify View Builder catalog snapshot: %w", err)
	}
	revision.ExplorerID = ""
	revision.Config = nil
	revision.AuthoringBundle = nil
	revision.IntentDigest = ""
	revision.CompilationReceiptID = ""
	revision.CreatedBy = ""
	revision.Publication.RevisionID = revision.ID
	return revision, nil
}

func InitialProjectConfigForRecipe(bundle recipe.Bundle) (explorer.ProjectConfig, error) {
	if strings.TrimSpace(bundle.Name) == "" || len(bundle.Outputs) == 0 {
		return explorer.ProjectConfig{}, invalidProjectConfig("initial project config requires a named recipe with outputs")
	}
	config := explorer.ProjectConfig{
		ID: bundle.Name, Title: bundle.Name,
		RecipeRef: explorer.ProjectConfigRecipeRef{Name: bundle.Name},
		Views:     make([]explorerconfig.ConfigView, 0, len(bundle.Outputs)),
	}
	for _, output := range bundle.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return explorer.ProjectConfig{}, invalidProjectConfig("recipe %q contains an unnamed output", bundle.Name)
		}
		config.Views = append(config.Views, explorerconfig.ConfigView{ID: output.Name, Title: output.Name, Output: output.Name})
	}
	if err := config.Validate(); err != nil {
		return explorer.ProjectConfig{}, invalidProjectConfig("initial project config: %v", err)
	}
	return config, nil
}

func PrepareViewBuilderConfig(config explorer.ProjectConfig, snapshot explorer.Revision, actor string) (explorer.ProjectConfigPublication, error) {
	if err := config.Validate(); err != nil {
		return explorer.ProjectConfigPublication{}, invalidProjectConfig("invalid project config: %v", err)
	}
	if config.RecipeRef.Name != snapshot.Recipe.Name {
		return explorer.ProjectConfigPublication{}, invalidProjectConfig("config recipe %q does not match catalog recipe %q", config.RecipeRef.Name, snapshot.Recipe.Name)
	}
	catalog, err := explorer.ViewBuilderCatalogFromRevision(snapshot)
	if err != nil {
		return explorer.ProjectConfigPublication{}, err
	}
	available, err := projectConfigAvailableFromRevision(snapshot, catalog)
	if err != nil {
		return explorer.ProjectConfigPublication{}, err
	}
	definition, err := explorer.CloneProjectConfigDefinition(config)
	if err != nil {
		return explorer.ProjectConfigPublication{}, invalidProjectConfig("clone project config: %v", err)
	}
	if err := validateProjectConfigBindings(definition, available); err != nil {
		return explorer.ProjectConfigPublication{}, invalidProjectConfig("config %q: %v", definition.ID, err)
	}
	digest, err := definition.Digest()
	if err != nil {
		return explorer.ProjectConfigPublication{}, invalidProjectConfig("digest project config %q: %v", definition.ID, err)
	}
	configJSON, err := projectConfigPresentationJSON(definition, snapshot.Project, snapshot.EmittedColumns)
	if err != nil {
		return explorer.ProjectConfigPublication{}, fmt.Errorf("encode project config %q presentation: %w", definition.ID, err)
	}
	revisionID, err := viewBuilderConfigRevisionID(snapshot, definition, digest, actor)
	if err != nil {
		return explorer.ProjectConfigPublication{}, fmt.Errorf("identify project config %q revision: %w", definition.ID, err)
	}
	revision, err := projectConfigRevisionFromSnapshot(snapshot, definition, digest, configJSON, revisionID, nil, actor)
	if err != nil {
		return explorer.ProjectConfigPublication{}, err
	}
	if _, err := explorer.BuildViewerProjection(&revision); err != nil {
		return explorer.ProjectConfigPublication{}, invalidProjectConfig("project config %q projection is invalid: %v", definition.ID, err)
	}
	return explorer.ProjectConfigPublication{Definition: definition, Revision: revision}, nil
}

func projectConfigRevisionFromSnapshot(
	snapshot explorer.Revision,
	definition explorer.ProjectConfig,
	digest string,
	configJSON json.RawMessage,
	revisionID string,
	diagnostics []explorer.Diagnostic,
	actor string,
) (explorer.Revision, error) {

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return explorer.Revision{}, fmt.Errorf("clone publication snapshot: %w", err)
	}
	var revision explorer.Revision
	if err := json.Unmarshal(raw, &revision); err != nil {
		return explorer.Revision{}, fmt.Errorf("clone publication snapshot: %w", err)
	}
	revision.ID = revisionID
	revision.ExplorerID = definition.ID
	revision.Config = configJSON
	revision.AuthoringBundle = nil
	revision.IntentDigest = digest
	revision.CompilationReceiptID = ""
	revision.Diagnostics = append([]explorer.Diagnostic{}, diagnostics...)
	revision.Status = explorer.RevisionReady
	revision.CreatedBy = actor
	revision.ActivatedAt = nil
	revision.FailedAt = nil
	if revision.ReadyAt == nil {
		readyAt := revision.CreatedAt
		revision.ReadyAt = &readyAt
	}
	revision.Publication = explorer.PublicationMetadata{
		State: string(explorer.RevisionReady), Generation: snapshot.SourceGeneration,
		ExecutionID: snapshot.Publication.ExecutionID, RevisionID: revisionID,
		UpdatedAt: snapshot.Publication.UpdatedAt,
	}
	return revision, nil
}

func projectConfigAvailableFromRevision(revision explorer.Revision, catalog explorer.ViewBuilderCatalog) (map[string]map[string]explorer.EmittedColumn, error) {
	emittedByOutput := make(map[string]map[string]explorer.EmittedColumn, len(catalog.Frames))
	for _, emitted := range revision.EmittedColumns {
		if emittedByOutput[emitted.OutputID] == nil {
			emittedByOutput[emitted.OutputID] = make(map[string]explorer.EmittedColumn)
		}
		emittedByOutput[emitted.OutputID][emitted.PublicColumn] = emitted
	}
	available := make(map[string]map[string]explorer.EmittedColumn, len(catalog.Frames))
	for _, frame := range catalog.Frames {
		available[frame.ID] = make(map[string]explorer.EmittedColumn, len(frame.Columns))
		for _, column := range frame.Columns {
			emitted, ok := emittedByOutput[frame.ID][column.Name]
			if !ok {
				return nil, invalidProjectConfig("catalog frame %q column %q has no compiler emission", frame.ID, column.Name)
			}
			available[frame.ID][column.Name] = emitted
		}
	}
	return available, nil
}

func projectConfigBindingDiagnostics(config explorer.ProjectConfig, public map[string]map[string]explorer.EmittedColumn) []explorer.Diagnostic {
	problems := make(map[string][]string, len(config.Views))
	add := func(view explorerconfig.ConfigView, message string) {
		problems[view.ID] = append(problems[view.ID], message)
	}
	for _, view := range config.Views {
		columns, ok := public[view.Output]
		if !ok {
			add(view, fmt.Sprintf("output %q is not in the current publication", view.Output))
			continue
		}
		for _, column := range view.Table.Columns {
			if _, ok := columns[column.Column]; !ok {
				add(view, fmt.Sprintf("table column %q is unavailable", column.Column))
			}
		}
		for _, filter := range view.Filters {
			column, ok := columns[filter.Column]
			if !ok || !column.Filterable {
				add(view, fmt.Sprintf("filter column %q is unavailable", filter.Column))
			}
		}
		for _, chart := range view.Charts {
			column, ok := columns[chart.Column]
			if !ok || !column.Chartable {
				add(view, fmt.Sprintf("chart column %q is unavailable", chart.Column))
			}
		}
		for name := range view.FixedFilters {
			column, ok := columns[name]
			if !ok || !column.Filterable {
				add(view, fmt.Sprintf("fixed filter column %q is unavailable", name))
			}
		}
		for index, action := range view.Actions {
			output := view.Output
			if action.Output != "" {
				output = action.Output
			}
			actionColumns, ok := public[output]
			if !ok {
				add(view, fmt.Sprintf("action %d output %q is unavailable", index, output))
				continue
			}
			for _, name := range action.Columns {
				if _, ok := actionColumns[name]; !ok {
					add(view, fmt.Sprintf("action %d column %q is unavailable", index, name))
				}
			}
			for name := range action.ExportHeaders {
				if _, ok := actionColumns[name]; !ok {
					add(view, fmt.Sprintf("action %d export column %q is unavailable", index, name))
				}
			}
		}
	}
	for filterName, filters := range config.SharedFilters {
		for _, filter := range filters {
			column, ok := public[filter.Output][filter.Column]
			if ok && column.Filterable {
				continue
			}
			message := fmt.Sprintf("shared filter %q binding %s.%s is unavailable", filterName, filter.Output, filter.Column)
			matched := false
			for _, view := range config.Views {
				if view.Output == filter.Output {
					add(view, message)
					matched = true
				}
			}
			if !matched {
				for _, view := range config.Views {
					add(view, message)
				}
			}
		}
	}
	viewIDs := make([]string, 0, len(problems))
	for viewID := range problems {
		viewIDs = append(viewIDs, viewID)
	}
	sort.Strings(viewIDs)
	diagnostics := make([]explorer.Diagnostic, 0, len(viewIDs))
	for _, viewID := range viewIDs {
		reasons := problems[viewID]
		sort.Strings(reasons)
		diagnostics = append(diagnostics, explorer.Diagnostic{
			Severity: "WARNING", Stage: "VIEW_BUILDER", Code: "VIEW_BINDING_UNAVAILABLE",
			FieldPath: "views." + viewID,
			Message:   "saved view has bindings unavailable in the current published schema; edit the view to restore it",
			Details:   map[string]any{"viewId": viewID, "unavailableBindings": reasons}, Retryable: false,
		})
	}
	return diagnostics
}

func viewBuilderCatalogRevisionID(executionValue publication.BundleExecution, resolvedSchemaDigest, sourceCommit string) (string, error) {
	identity := struct {
		Project, RecipeName, ExecutionID, RecipeDigest, SchemaDigest, ResolvedSchemaDigest, Generation, SourceCommit string
	}{
		Project: executionValue.Project, RecipeName: executionValue.Name, ExecutionID: executionValue.ID,
		RecipeDigest: executionValue.RecipeDigest, SchemaDigest: executionValue.SchemaDigest,
		ResolvedSchemaDigest: resolvedSchemaDigest, Generation: executionValue.DatasetGeneration, SourceCommit: sourceCommit,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "catalog_" + hex.EncodeToString(sum[:]), nil
}

func viewBuilderConfigRevisionID(snapshot explorer.Revision, config explorer.ProjectConfig, configDigest, actor string) (string, error) {
	identity := struct {
		Project, ConfigID, ConfigDigest, CatalogRevisionID, ExecutionID, Actor string
	}{
		Project: snapshot.Project, ConfigID: config.ID, ConfigDigest: configDigest,
		CatalogRevisionID: snapshot.ID, ExecutionID: snapshot.Publication.ExecutionID, Actor: actor,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "view_config_" + hex.EncodeToString(sum[:]), nil
}
