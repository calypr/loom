package server

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer"
)

// projectConfigsForSourcePublication reads only saved project-owned
// definitions. It seeds a generic, editable config only for a recipe with no
// catalog anchor yet; deleting every view later must not recreate one.
func projectConfigsForSourcePublication(
	owners []explorer.Explorer,
	recipeName string,
	catalogAnchored bool,
	snapshot explorer.Revision,
) ([]explorer.ProjectConfig, map[string]string, error) {
	if recipeName == "" || snapshot.Recipe.Name != recipeName {
		return nil, nil, fmt.Errorf("catalog snapshot recipe %q does not match configured recipe %q", snapshot.Recipe.Name, recipeName)
	}
	if snapshot.Project == "" {
		return nil, nil, fmt.Errorf("catalog snapshot for recipe %q has no project identity", recipeName)
	}

	definitions := make([]explorer.ProjectConfig, 0)
	expectedRevisionIDs := make(map[string]string)
	ownersByID := make(map[string]explorer.Explorer, len(owners))
	for _, owner := range owners {
		if owner.ExplorerID == "" {
			return nil, nil, fmt.Errorf("project has an Explorer owner with an empty ID")
		}
		if owner.Project != snapshot.Project {
			return nil, nil, fmt.Errorf("Explorer owner %q belongs to project %q, want %q", owner.ExplorerID, owner.Project, snapshot.Project)
		}
		if _, exists := ownersByID[owner.ExplorerID]; exists {
			return nil, nil, fmt.Errorf("project has duplicate Explorer owner %q", owner.ExplorerID)
		}
		ownersByID[owner.ExplorerID] = owner
		if owner.ProjectConfig == nil || owner.ProjectConfig.RecipeRef.Name != recipeName {
			continue
		}
		if owner.ManagementMode != explorer.ManagementRepository || owner.ProjectConfig.ID != owner.ExplorerID {
			return nil, nil, fmt.Errorf("project config owner %q has inconsistent identity or management mode", owner.ExplorerID)
		}
		if owner.ActiveRevisionID == "" {
			return nil, nil, fmt.Errorf("project config owner %q has no active revision", owner.ExplorerID)
		}
		definitions = append(definitions, *owner.ProjectConfig)
		expectedRevisionIDs[owner.ExplorerID] = owner.ActiveRevisionID
	}

	if len(definitions) != 0 || catalogAnchored {
		return definitions, expectedRevisionIDs, nil
	}
	if len(snapshot.Recipe.Outputs) == 0 {
		return nil, nil, fmt.Errorf("catalog snapshot for recipe %q has no outputs", recipeName)
	}

	definition, err := explorer.InitialProjectConfigForRecipe(snapshot.Recipe)
	if err != nil {
		return nil, nil, fmt.Errorf("create initial project config: %w", err)
	}
	definition.ID = initialProjectConfigID(recipeName, ownersByID)
	definitions = append(definitions, definition)
	expectedRevisionIDs[definition.ID] = ""
	return definitions, expectedRevisionIDs, nil
}

func initialProjectConfigID(recipeName string, owners map[string]explorer.Explorer) string {
	if _, exists := owners[recipeName]; !exists {
		return recipeName
	}
	for suffix := 1; ; suffix++ {
		candidate := recipeName + "-views"
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-views-%d", recipeName, suffix)
		}
		if _, exists := owners[candidate]; !exists {
			return candidate
		}
	}
}
