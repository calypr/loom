package dataframebuilder

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/discovery"
)

// GuidedDiscoveryRequest describes the small project/scope input needed to
// power the product's first questions. It intentionally has no FHIR paths,
// root choice, or AQL fragments: those are derived from the scoped catalog and
// generated FHIR metadata.
type GuidedDiscoveryRequest struct {
	Project           string
	AuthResourcePaths []string
}

// DiscoverGuided returns a safe, schema-backed capability snapshot for the
// guided dataframe flow. It composes the existing project- and
// auth-scope-aware catalog queries, then removes their implementation details
// behind opaque IDs in internal/discovery. It is not a public transport API
// yet; GraphQL/HTTP exposure remains deliberately unwired while generation
// selection is an internal service dependency.
func (s *Service) DiscoverGuided(ctx context.Context, req GuidedDiscoveryRequest) (discovery.Snapshot, error) {
	project := strings.TrimSpace(req.Project)
	if project == "" {
		return discovery.Snapshot{}, fmt.Errorf("project is required")
	}

	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authorizeProject(principal, project, s.scopeResolver != nil); err != nil {
		return discovery.Snapshot{}, err
	}
	generation, err := s.resolveActiveGeneration(ctx, project)
	if err != nil {
		return discovery.Snapshot{}, err
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, project, generation, req.AuthResourcePaths)
	if err != nil {
		return discovery.Snapshot{}, err
	}

	fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
		// An empty resource type is the existing catalog reader's all-types
		// query. Discovery filters the result back through generated FHIR
		// metadata before anything leaves this service.
		ResourceType: "",
		PivotOnly:    false,
	})
	if err != nil {
		return discovery.Snapshot{}, err
	}
	relationships, err := s.discoverReferences(ctx, catalog.PopulatedReferenceOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
		Mode:                          catalog.TraversalModeStorage,
	})
	if err != nil {
		return discovery.Snapshot{}, err
	}

	return discovery.BuildSnapshot(discovery.CatalogFacts{
		Project:       project,
		Fields:        fields,
		Relationships: relationships,
	})
}
