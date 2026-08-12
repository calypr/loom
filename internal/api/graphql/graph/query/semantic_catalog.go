package queryapi

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// SemanticCatalog reads only the active generation and caller-authorized
// catalog partitions. The root resource filter is applied at the catalog
// boundary so no concepts from another project or resource are disclosed.
func (s *Service) SemanticCatalog(ctx context.Context, req SemanticCatalogRequest) (*semantic.CatalogResult, error) {
	project := strings.TrimSpace(req.Project)
	root := strings.TrimSpace(req.RootResourceType)
	if project == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if root == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeRootResourceTypeRequired, "")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, project, s.scopeResolver != nil); err != nil {
		return nil, classifyError(err)
	}
	generation, err := s.resolveActiveGeneration(ctx, project)
	if err != nil {
		return nil, queryBackend(err)
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, project, generation, req.AuthResourcePaths)
	if err != nil {
		return nil, queryBackend(err)
	}
	fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		Project: project, DatasetGeneration: generation, ResourceType: root,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
	})
	if err != nil {
		return nil, queryBackend(err)
	}
	fields = aggregatePopulatedFields(fields)
	result := semantic.DiscoverCatalog(fields, semantic.CatalogOptions{
		Project: project, SourceGeneration: generation, ResourceType: root,
		ResourceLimit: req.ResourceLimit, ConceptLimitPerResource: req.ConceptLimitPerResource,
	})
	result.AuthResourcePaths = cloneStrings(scope.AuthResourcePaths)
	return &result, nil
}
