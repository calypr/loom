package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	graphresolver "github.com/calypr/loom/generated/graphql/graph/resolver"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/materialization"
	materializationarango "github.com/calypr/loom/internal/dataframe/materialization/arango"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
)

func recipeScopeDigest(bindings recipe.RuntimeBindings) string {
	paths := append([]string(nil), bindings.AuthResourcePaths...)
	sort.Strings(paths)
	hash := sha256.Sum256([]byte(bindings.Project + "\x00" + bindings.DatasetGeneration + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(hash[:])
}

// recipeOutputLogicalColumns is the one conversion point from the finalized
// compiler schema to the backend-neutral publication schema. Publication must
// not reconstruct nested names from semantic recipe nodes because those names
// are finalized by physical lowering.
func recipeOutputLogicalColumns(plan engine.Resolved, outputName string) []publication.LogicalColumn {
	for _, output := range plan.Compiled.Outputs {
		if output.Name != outputName {
			continue
		}
		columns := make([]publication.LogicalColumn, 0, len(output.OutputSchema)+1)
		identityAdded := false
		for _, column := range output.OutputSchema {
			if column.Identity && column.Name == "__loom_row_id" {
				columns = append(columns, publication.LogicalColumn{Name: column.Name, Kind: "string", IsIdentity: true})
				identityAdded = true
				break
			}
		}
		if !identityAdded {
			columns = append(columns, publication.LogicalColumn{Name: "__loom_row_id", Kind: "string", IsIdentity: true})
		}
		for _, column := range output.OutputSchema {
			if column.Internal {
				continue
			}
			kind := column.Kind
			if kind == "date_time" {
				kind = "date-time"
			}
			if kind == "" {
				kind = "string"
			}
			columns = append(columns, publication.LogicalColumn{Name: materialization.FlatColumnName(output.RootResourceType, column.Name), Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable})
		}
		return columns
	}
	return []publication.LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}}
}

func recipeOutputRootResourceType(plan engine.Resolved, outputName string) string {
	for _, output := range plan.Compiled.Outputs {
		if output.Name == outputName {
			return output.RootResourceType
		}
	}
	return ""
}

func recipeMaterializer(recipeEngine *engine.Engine, bundleTarget publication.Target, registry *materializationarango.Registry, degradation error, logger *slog.Logger, batchRows, batchBytes int) func(context.Context, string, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
	return func(ctx context.Context, name string, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		if bundleTarget == nil {
			cause := degradation
			if cause == nil {
				cause = dataframeerrors.ErrBackendUnavailable
			}
			return graphresolver.RecipeExecution{}, dataframeerrors.Wrap(cause, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		bindings.IncludeAuthResourcePath = true
		var identity materialization.BundleIdentity
		_, err := recipeEngine.Materialize(ctx, name, bindings, func(ctx context.Context, full engine.Resolved) error {
			streams, err := recipeEngine.Streams(ctx, full)
			if err != nil {
				return err
			}
			identity = materialization.BundleIdentity{Name: name, Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration, RecipeDigest: full.StoredRecipeDigest, SchemaDigest: full.ResolvedSchemaDigest, ScopeDigest: full.Semantic.ScopeDigest, EngineVersion: "loom-recipe-v1", AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...)}
			streamInputs := make([]publication.OutputStream, 0, len(streams))
			for _, stream := range streams {
				stream := stream
				columns := recipeOutputLogicalColumns(full, stream.Name)
				rootResourceType := recipeOutputRootResourceType(full, stream.Name)
				streamInputs = append(streamInputs, publication.OutputStream{
					Name: stream.Name, Columns: columns,
					Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
						_, err := stream.Stream(streamCtx, func(row map[string]any) error {
							qualified, err := materialization.QualifyFlatRow(rootResourceType, row)
							if err != nil {
								return err
							}
							return visit(qualified)
						})
						return err
					},
				})
			}
			publicationIdentity := publication.PublicationIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...)}
			_, err = publication.Publish(ctx, bundleTarget, publicationIdentity, streamInputs, publication.Limits{BatchRows: batchRows, BatchBytes: batchBytes})
			return err
		})
		if err != nil {
			logger.Error("recipe materialization failed", "name", name, "project", bindings.Project, "error", err.Error())
			return graphresolver.RecipeExecution{}, err
		}
		published, err := registry.FindExecutionByKey(ctx, identity.Key())
		if err != nil {
			logger.Error("load published recipe execution failed", "name", name, "project", bindings.Project, "error", err.Error())
			return graphresolver.RecipeExecution{}, fmt.Errorf("load published recipe execution: %w", err)
		}
		return graphresolver.RecipeExecution{ID: published.ID, Name: name, RecipeDigest: identity.RecipeDigest, ResolvedSchemaDigest: identity.SchemaDigest, SourceGeneration: identity.DatasetGeneration, State: string(materialization.BundleReady)}, nil
	}
}
