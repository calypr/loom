package queryapi

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func (s *Service) resolveRecipeBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipe.Bundle, error) {
	resolved, err := schema.Resolve(ctx, bundle, schema.Scope{
		Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...), AuthScopeMode: string(bindings.AuthScopeMode),
	}, recipeFieldDiscovery{read: s.discoverFields})
	if err != nil {
		return recipe.Bundle{}, queryInvalidErrorOrBackend(err)
	}
	if !bundleHasConceptSelections(resolved.Bundle) {
		return resolved.Bundle, nil
	}
	fields := make([]catalog.PopulatedField, 0)
	for _, resourceType := range conceptResourceTypes(resolved.Bundle) {
		part, discoverErr := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
			Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration, ResourceType: resourceType,
			AuthResourcePaths:             append([]string(nil), bindings.AuthResourcePaths...),
			AuthResourcePathsUnrestricted: authScopeUnrestricted(bindings),
		})
		if discoverErr != nil {
			return recipe.Bundle{}, queryBackend(discoverErr)
		}
		fields = append(fields, part...)
	}
	fields = aggregatePopulatedFields(fields)
	concepts := semantic.DiscoverCatalog(fields, semantic.CatalogOptions{Project: bindings.Project, SourceGeneration: bindings.DatasetGeneration})
	lowered, err := semantic.LowerBundleConceptSelections(resolved.Bundle, concepts.ResultsByResource())
	if err != nil {
		return recipe.Bundle{}, queryInvalidErrorOrBackend(err)
	}
	// ConceptSelections are authoring references and remain persisted in the
	// immutable draft/revision. The execution planner consumes the concrete
	// fields/maps produced above; removing the references from this transient
	// resolved copy prevents the legacy planner from attempting to interpret
	// them a second time.
	for index := range lowered.Outputs {
		lowered.Outputs[index].ConceptSelections = nil
	}
	return lowered, nil
}

func bundleHasConceptSelections(bundle recipe.Bundle) bool {
	for _, output := range bundle.Outputs {
		if len(output.ConceptSelections) != 0 {
			return true
		}
	}
	return false
}

func conceptResourceTypes(bundle recipe.Bundle) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, output := range bundle.Outputs {
		if len(output.ConceptSelections) == 0 {
			continue
		}
		resourceType := strings.TrimSpace(output.RootResourceType)
		if resourceType == "" {
			continue
		}
		if _, ok := seen[resourceType]; ok {
			continue
		}
		seen[resourceType] = struct{}{}
		result = append(result, resourceType)
	}
	return result
}

func authScopeUnrestricted(bindings recipe.RuntimeBindings) *bool {
	value := false
	if bindings.AuthScopeMode == authscope.ReadScopeUnrestricted {
		value = true
	}
	if bindings.AuthScopeMode == "" && len(bindings.AuthResourcePaths) == 0 {
		value = true
	}
	return &value
}
