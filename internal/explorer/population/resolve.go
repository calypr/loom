package population

import (
	"context"
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
)

type SelectionReader interface {
	GetSelection(context.Context, string, string) (*selection.SelectionRevision, error)
}

func ResolveWorkspacePopulations(ctx context.Context, project string, workspace ws.Workspace, snapshot capability.Snapshot, scopeDigest string, selections SelectionReader) (explorercompilation.ResolvedInputs, error) {
	project = projectid.Canonical(project)
	inputs := explorercompilation.ResolvedInputs{}
	for index, document := range workspace.Documents {
		population := document.Population
		if population == nil {
			continue
		}
		selected, err := selections.GetSelection(ctx, project, population.SelectionRevisionID)
		if err != nil {
			return inputs, support.Unavailable("population", "POPULATION_SELECTION_UNAVAILABLE", fmt.Sprintf("population at documents[%d]: selection %q could not be resolved", index, population.SelectionRevisionID), err)
		}
		if selected == nil {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q was not found", index, population.SelectionRevisionID)
		}
		if err := selected.Validate(); err != nil {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q is invalid or incomplete: %w", index, population.SelectionRevisionID, err)
		}
		if !selected.Complete {
			return inputs, fmt.Errorf("population at documents[%d]: selection %q is incomplete", index, population.SelectionRevisionID)
		}
		if projectid.Canonical(selected.Project) != project {
			return inputs, fmt.Errorf("population at documents[%d]: selection project does not match the workspace project", index)
		}
		if strings.TrimSpace(selected.Generation) != strings.TrimSpace(snapshot.Identity.Generation) {
			return inputs, fmt.Errorf("population at documents[%d]: selection generation is stale", index)
		}
		if strings.TrimSpace(selected.ScopeDigest) != strings.TrimSpace(snapshot.Identity.AuthorizationScopeDigest) || (scopeDigest != "" && strings.TrimSpace(selected.ScopeDigest) != strings.TrimSpace(scopeDigest)) {
			return inputs, fmt.Errorf("population at documents[%d]: selection authorization scope is stale", index)
		}
		resolvedRoute, err := ValidateRoute(document, *population, selected.ResourceType, snapshot)
		if err != nil {
			return inputs, fmt.Errorf("population at documents[%d]: %w", index, err)
		}
		inputs.Populations = append(inputs.Populations, explorercompilation.ResolvedPopulation{
			OutputID: document.Output.ID, SelectionRevisionID: selected.ID, MembershipDigest: selected.MembershipDigest,
			MemberCount: selected.MemberCount, ResourceType: selected.ResourceType, Route: resolvedRoute,
		})
	}
	return inputs.Canonical(), nil
}
