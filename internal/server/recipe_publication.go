package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func recipeScopeDigest(bindings recipe.RuntimeBindings) string {
	paths := append([]string(nil), bindings.AuthResourcePaths...)
	sort.Strings(paths)
	hash := sha256.Sum256([]byte(bindings.Project + "\x00" + bindings.DatasetGeneration + "\x00" + string(bindings.AuthScopeMode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(hash[:])
}

// recipeOutputLogicalColumns is the one conversion point from the finalized
// compiler schema to the backend-neutral publication schema. Publication must
// not reconstruct nested names from semantic recipe nodes because those names
// are finalized by physical lowering.
func recipeOutputLogicalColumns(plan dataframeexecution.Resolved, outputName string) []publication.LogicalColumn {
	for _, output := range plan.Compiled.Outputs {
		if output.Name != outputName {
			continue
		}
		columns := make([]publication.LogicalColumn, 0, len(output.OutputSchema)+1)
		identityAdded := false
		for _, column := range output.OutputSchema {
			if column.Identity && column.Name == "__loom_row_id" {
				columns = append(columns, publication.LogicalColumn{Name: column.Name, SemanticPath: "loom:row_id", Kind: "string", IsIdentity: true, LoomOwned: true, Provenance: publication.ColumnExplicit})
				identityAdded = true
				break
			}
		}
		if !identityAdded {
			columns = append(columns, publication.LogicalColumn{Name: "__loom_row_id", SemanticPath: "loom:row_id", Kind: "string", IsIdentity: true, LoomOwned: true, Provenance: publication.ColumnExplicit})
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
			semanticPath := column.SemanticPath
			if semanticPath == "" {
				semanticPath = output.RootResourceType + "." + column.Name
			}
			provenance := publication.ColumnExplicit
			if column.Discovered {
				provenance = publication.ColumnDiscovered
			}
			name := column.Name
			if output.RootColumnNaming != recipe.RootColumnNamingExact {
				name = publication.FlatColumnName(output.RootResourceType, name)
			}
			columns = append(columns, publication.LogicalColumn{Name: name, SemanticPath: semanticPath, Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable, Provenance: provenance})
		}
		return columns
	}
	return []publication.LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}}
}

func recipeOutputRootResourceType(plan dataframeexecution.Resolved, outputName string) string {
	for _, output := range plan.Compiled.Outputs {
		if output.Name == outputName {
			return output.RootResourceType
		}
	}
	return ""
}

func recipeOutputUsesExactRootColumns(plan dataframeexecution.Resolved, outputName string) bool {
	for _, output := range plan.Compiled.Outputs {
		if output.Name == outputName {
			return output.RootColumnNaming == recipe.RootColumnNamingExact
		}
	}
	return false
}

func publishResolvedRecipe(ctx context.Context, recipeEngine *dataframeexecution.Engine, target publication.Target, name string, bindings recipe.RuntimeBindings, full dataframeexecution.Resolved, batchRows, batchBytes int) (publication.BundleIdentity, error) {
	streams, err := recipeEngine.Streams(ctx, full)
	if err != nil {
		return publication.BundleIdentity{}, err
	}
	identity := publication.BundleIdentity{
		Name: name, TranslationVersion: full.Semantic.SemanticPlan.TranslationVersion,
		OutputName: incrementalPublicationOutput(bindings, streams),
		Project:    bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		RecipeDigest: full.StoredRecipeDigest, SchemaDigest: full.ResolvedSchemaDigest,
		ScopeDigest: full.Semantic.ScopeDigest, EngineVersion: "loom-recipe-v1",
		AuthScopeMode:     string(bindings.AuthScopeMode),
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
	}
	streamInputs := make([]publication.OutputStream, 0, len(streams))
	for _, stream := range streams {
		stream := stream
		columns := recipeOutputLogicalColumns(full, stream.Name)
		rootResourceType := recipeOutputRootResourceType(full, stream.Name)
		exactRootColumns := recipeOutputUsesExactRootColumns(full, stream.Name)
		streamInputs = append(streamInputs, publication.OutputStream{
			Name: stream.Name, Columns: columns,
			Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
				_, err := stream.Stream(streamCtx, func(row map[string]any) error {
					if exactRootColumns {
						return visit(row)
					}
					qualified, err := publication.QualifyFlatRow(rootResourceType, row)
					if err != nil {
						return err
					}
					return visit(qualified)
				})
				return err
			},
		})
	}
	publicationIdentity := publication.PublicationIdentity{
		Name: identity.Name, TranslationVersion: identity.TranslationVersion,
		OutputName: identity.OutputName,
		Project:    identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
		AuthScopeMode:     identity.AuthScopeMode,
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
	}
	_, err = publication.Publish(ctx, target, publicationIdentity, streamInputs, publication.Limits{BatchRows: batchRows, BatchBytes: batchBytes})
	return identity, err
}

func incrementalPublicationOutput(bindings recipe.RuntimeBindings, streams []dataframeexecution.OutputStream) string {
	if len(bindings.OutputNames) != 1 || len(streams) != 1 {
		return ""
	}
	return streams[0].Name
}
