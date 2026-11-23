package population

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
)

func ValidateRemovalSelection(base, candidate *selection.SelectionRevision, removed selection.ResourceRef, snapshot capability.Snapshot) error {
	if base == nil || candidate == nil {
		return fmt.Errorf("base and candidate selection headers are required")
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	project, generation, scope := projectid.Canonical(snapshot.Identity.Project), snapshot.Identity.Generation, snapshot.Identity.AuthorizationScopeDigest
	for name, selected := range map[string]*selection.SelectionRevision{"base": base, "candidate": candidate} {
		if !selected.Complete || projectid.Canonical(selected.Project) != project || selected.Generation != generation || selected.ScopeDigest != scope {
			return fmt.Errorf("%s selection is incomplete or outside the authorized project, generation, or scope", name)
		}
	}
	if base.ID == candidate.ID || base.MembershipDigest == candidate.MembershipDigest ||
		base.Project != candidate.Project || base.Generation != candidate.Generation || base.ScopeDigest != candidate.ScopeDigest || base.ResourceType != candidate.ResourceType {
		return fmt.Errorf("candidate selection does not retain the exact base selection scope")
	}
	removed = removed.Canonical()
	if err := removed.Validate(project, generation, base.ResourceType); err != nil {
		return err
	}
	if base.MemberCount < 1 || candidate.MemberCount != base.MemberCount-1 {
		return fmt.Errorf("candidate selection does not remove exactly one member")
	}
	if candidate.Source.Kind != selection.SelectionSourceRevision || candidate.Source.RevisionID != base.ID || candidate.Source.Generation != base.Generation ||
		candidate.Source.ResourceType != base.ResourceType || candidate.Source.MembershipDigest != base.MembershipDigest ||
		candidate.Rule.Kind != selection.SelectionRuleExplicit || len(candidate.Exclusions) != 1 || candidate.Exclusions[0].Canonical() != removed {
		return fmt.Errorf("candidate selection provenance does not identify the exact base-minus-member derivation")
	}
	return nil
}

func RemovalRouteMatches(populationRoute []authoringv2.PopulationRouteStep, signedRoute []capability.ConstructionRouteStep) bool {
	if len(populationRoute) != len(signedRoute) {
		return false
	}
	for index, step := range populationRoute {
		signed := signedRoute[index]
		if step.ResourceType != signed.ToResourceType || step.Relationship != signed.Relationship ||
			(step.CatalogEdgeID != "" && step.CatalogEdgeID != signed.EdgeID) ||
			(step.StorageDirection != "" && step.StorageDirection != signed.StorageDirection) {
			return false
		}
	}
	return true
}
