package dataframebuilder

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/discovery"
	"github.com/calypr/loom/internal/recipe"
	"github.com/calypr/loom/internal/recipecompiler"
)

// RecipeRequest is the scoped, internal invocation envelope for a product
// recipe. Recipe itself deliberately contains no authorization paths: they are
// resolved from the calling principal at execution time instead of becoming
// durable recipe data.
//
// Column and filter values in Recipe are opaque capability IDs. This request
// accepts neither FHIR selectors nor graph labels.
type RecipeRequest struct {
	Recipe            recipe.Recipe
	AuthResourcePaths []string
}

// RecipeRunRequest adds a bounded dataframe-service limit to RecipeRequest.
// A non-positive limit retains dataframe.Service's existing default behavior.
// Destination delivery is intentionally outside this type: RunRecipe evaluates
// rows for callers such as a preview, but does not create files or write to
// Elasticsearch.
type RecipeRunRequest struct {
	Recipe            recipe.Recipe
	AuthResourcePaths []string
	Limit             int
}

// PrepareRecipe resolves a product recipe against fresh catalog facts for the
// caller's current project and authorization scope. It never caches facts or
// treats an opaque capability ID as a FHIR path. The returned Builder carries
// the resolved scope so its subsequent dataframe execution uses the exact same
// scope that supplied the capabilities.
//
// The current recipe compiler supports root-only selections and filters. It
// receives all currently scoped catalog fields because recipe IDs are opaque;
// narrowing the catalog read based on an untrusted recipe ID would both leak
// implementation details and make stale IDs ambiguous.
func (s *Service) PrepareRecipe(ctx context.Context, req RecipeRequest) (recipecompiler.Plan, error) {
	normalized, err := req.Recipe.Normalize()
	if err != nil {
		return recipecompiler.Plan{}, err
	}

	project := strings.TrimSpace(normalized.Project)
	if project == "" {
		// Normalize currently guarantees this condition, but keep the service
		// boundary closed if the recipe contract changes in the future.
		return recipecompiler.Plan{}, fmt.Errorf("project is required")
	}

	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authorizeProject(principal, project, s.scopeResolver != nil); err != nil {
		return recipecompiler.Plan{}, err
	}
	generation, err := s.resolveActiveGeneration(ctx, project)
	if err != nil {
		return recipecompiler.Plan{}, err
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, project, generation, req.AuthResourcePaths)
	if err != nil {
		return recipecompiler.Plan{}, err
	}

	fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
		// Empty ResourceType is the existing catalog reader's all-types query.
		// recipecompiler resolves the chosen opaque IDs against this one fresh,
		// scope-constrained fact set.
		ResourceType: "",
		PivotOnly:    false,
	})
	if err != nil {
		return recipecompiler.Plan{}, err
	}

	plan, err := recipecompiler.Build(normalized, discovery.CatalogFacts{
		Project: project,
		Fields:  fields,
	})
	if err != nil {
		return recipecompiler.Plan{}, err
	}

	// recipecompiler intentionally does not own authorization. Copy rather
	// than aliasing the resolver's result so callers cannot mutate a prepared
	// plan by retaining the request or resolver-owned slice.
	if len(scope.AuthResourcePaths) == 0 {
		plan.Builder.AuthResourcePaths = nil
	} else {
		plan.Builder.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	}
	plan.Builder.DatasetGeneration = generation
	plan.Builder.AuthScopeMode = scope.Mode
	return plan, nil
}

// RunRecipe prepares fresh authorized facts and evaluates the resulting
// root-only dataframe plan through dataframe.Service. It is an execution
// primitive for preview-like callers; Recipe.Destination is validated as
// product intent but no export or Elasticsearch delivery occurs here.
func (s *Service) RunRecipe(ctx context.Context, req RecipeRunRequest) (*dataframe.Result, error) {
	plan, err := s.PrepareRecipe(ctx, RecipeRequest{
		Recipe:            req.Recipe,
		AuthResourcePaths: req.AuthResourcePaths,
	})
	if err != nil {
		return nil, err
	}
	return s.dataframes.Run(ctx, dataframe.RunRequest{
		Builder: plan.Builder,
		Limit:   req.Limit,
	})
}

// StreamRecipe prepares fresh authorized facts and forwards rows through the
// dataframe row-stream. As with RunRecipe, this does not implement any recipe
// destination sink; NDJSON, CSV, and Elasticsearch remain consumers to add on
// top of this shared stream.
func (s *Service) StreamRecipe(ctx context.Context, req RecipeRunRequest, visit func(map[string]any) error) (dataframe.StreamResult, error) {
	plan, err := s.PrepareRecipe(ctx, RecipeRequest{
		Recipe:            req.Recipe,
		AuthResourcePaths: req.AuthResourcePaths,
	})
	if err != nil {
		return dataframe.StreamResult{}, err
	}
	return s.dataframes.Stream(ctx, dataframe.RunRequest{
		Builder: plan.Builder,
		Limit:   req.Limit,
	}, visit)
}
