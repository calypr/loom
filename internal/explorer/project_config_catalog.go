package explorer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// PrepareProjectConfigCatalogSnapshot stores one immutable compiler snapshot for
// a recipe execution without creating an Explorer owner or a user view config.
func PrepareProjectConfigCatalogSnapshot(
	resolved execution.Resolved,
	executionValue publication.BundleExecution,
	sourceCommit string,
) (Revision, error) {
	initial, err := InitialProjectConfigForRecipe(resolved.Bundle)
	if err != nil {
		return Revision{}, err
	}
	prepared, err := PrepareProjectConfigs([]ProjectConfig{initial}, resolved, executionValue, sourceCommit)
	if err != nil {
		return Revision{}, fmt.Errorf("prepare project-config catalog metadata: %w", err)
	}
	if len(prepared) != 1 {
		return Revision{}, fmt.Errorf("initial catalog preparation returned %d revisions", len(prepared))
	}
	revision := prepared[0].Revision
	revision.ID, err = projectConfigCatalogRevisionID(executionValue, resolved.ResolvedSchemaDigest, sourceCommit)
	if err != nil {
		return Revision{}, fmt.Errorf("identify project-config catalog snapshot: %w", err)
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

// InitialProjectConfigForRecipe creates the one-time project-owned starter
// definition. Empty table bindings let the existing projection expose every
// compiler-public column for each output.
func InitialProjectConfigForRecipe(bundle recipe.Bundle) (ProjectConfig, error) {
	if strings.TrimSpace(bundle.Name) == "" || len(bundle.Outputs) == 0 {
		return ProjectConfig{}, fmt.Errorf("initial project config requires a named recipe with outputs")
	}
	config := ProjectConfig{
		ID: bundle.Name, Title: bundle.Name,
		RecipeRef: ProjectConfigRecipeRef{Name: bundle.Name},
		Views:     make([]ConfigView, 0, len(bundle.Outputs)),
	}
	for _, output := range bundle.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return ProjectConfig{}, fmt.Errorf("recipe %q contains an unnamed output", bundle.Name)
		}
		config.Views = append(config.Views, ConfigView{ID: output.Name, Title: output.Name, Output: output.Name})
	}
	if err := config.validate(); err != nil {
		return ProjectConfig{}, fmt.Errorf("initial project config: %w", err)
	}
	return config, nil
}
func projectConfigBindingDiagnostics(config ProjectConfig, public map[string]map[string]EmittedColumn) []Diagnostic {
	problems := make(map[string][]string, len(config.Views))
	add := func(view ConfigView, message string) {
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
	diagnostics := make([]Diagnostic, 0, len(viewIDs))
	for _, viewID := range viewIDs {
		reasons := problems[viewID]
		sort.Strings(reasons)
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "WARNING", Stage: "VIEW_BUILDER", Code: "VIEW_BINDING_UNAVAILABLE",
			FieldPath: "views." + viewID,
			Message:   "saved view has bindings unavailable in the current published schema; edit the view to restore it",
			Details:   map[string]any{"viewId": viewID, "unavailableBindings": reasons}, Retryable: false,
		})
	}
	return diagnostics
}

func projectConfigCatalogRevisionID(executionValue publication.BundleExecution, resolvedSchemaDigest, sourceCommit string) (string, error) {
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
