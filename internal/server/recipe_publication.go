package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
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
				kind := column.Kind
				if kind == "" {
					kind = "string"
				}
				// Group row identity is a structured tuple in the compiler. The
				// publication row key is its canonical JSON text so ClickHouse can
				// order and page by the hidden identity column.
				if output.RowGrain == spec.RowGrainGroups && kind == "object" {
					kind = "string"
				}
				columns = append(columns, publication.LogicalColumn{Name: column.Name, SemanticPath: "loom:row_id", Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable, IsIdentity: true, LoomOwned: true, Provenance: publication.ColumnExplicit})
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

func publishResolvedRecipe(ctx context.Context, recipeEngine *dataframeexecution.Engine, target publication.Target, name string, bindings recipe.RuntimeBindings, full dataframeexecution.Resolved, receiptID string, sourceRows map[string]*publication.SourceRowMetadata, batchRows, batchBytes int, qualityMaxRows, qualityMaxDistinctKeys int64) (publication.BundleIdentity, error) {
	streams, err := recipeEngine.Streams(ctx, full)
	if err != nil {
		return publication.BundleIdentity{}, err
	}
	identity := publication.BundleIdentity{
		ReceiptID: receiptID,
		Name:      name, TranslationVersion: full.Semantic.SemanticPlan.TranslationVersion,
		OutputName: incrementalPublicationOutput(bindings, streams),
		Project:    bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		RecipeDigest: full.StoredRecipeDigest, SchemaDigest: full.ResolvedSchemaDigest,
		ScopeDigest: full.Semantic.ScopeDigest, EngineVersion: "loom-recipe-v2",
		AuthScopeMode:     string(bindings.AuthScopeMode),
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
	}
	streamInputs := make([]publication.OutputStream, 0, len(streams))
	for _, stream := range streams {
		stream := stream
		compiledOutput, ok := recipeCompiledOutput(full, stream.Name)
		if !ok {
			return publication.BundleIdentity{}, fmt.Errorf("compiled recipe output %q is missing", stream.Name)
		}
		columns := recipeOutputLogicalColumns(full, stream.Name)
		rootResourceType := compiledOutput.RootResourceType
		exactRootColumns := compiledOutput.RootColumnNaming == recipe.RootColumnNamingExact
		streamInputs = append(streamInputs, publication.OutputStream{
			Name: stream.Name, Columns: columns,
			SourceRow: sourceRows[stream.Name],
			Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
				_, err := stream.Stream(streamCtx, func(row map[string]any) error {
					if err := canonicalizeGroupPublicationIdentity(compiledOutput, row); err != nil {
						return err
					}
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
		ReceiptID: receiptID,
		Name:      identity.Name, TranslationVersion: identity.TranslationVersion,
		OutputName: identity.OutputName,
		Project:    identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
		AuthScopeMode:     identity.AuthScopeMode,
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
	}
	_, err = publication.Publish(ctx, target, publicationIdentity, streamInputs, publication.Limits{
		BatchRows: batchRows, BatchBytes: batchBytes,
		Quality: publication.QualityPolicy{
			Version: publication.DefaultQualityPolicyVersion, MaxRows: qualityMaxRows,
			MaxDistinctKeys: qualityMaxDistinctKeys, RequireUniqueIdentity: true,
		},
	})
	return identity, err
}

func recipeCompiledOutput(plan dataframeexecution.Resolved, outputName string) (lower.CompiledRecipeOutput, bool) {
	for _, output := range plan.Compiled.Outputs {
		if output.Name == outputName {
			return output, true
		}
	}
	return lower.CompiledRecipeOutput{}, false
}

func canonicalizeGroupPublicationIdentity(output lower.CompiledRecipeOutput, row map[string]any) error {
	if output.RowGrain != spec.RowGrainGroups {
		return nil
	}
	identity := output.RowIdentity
	if identity == nil || identity.Grain != spec.RowGrainGroups || len(identity.Fields) != 2 ||
		identity.Fields[0] != "group_revision_id" || identity.Fields[1] != "group_id" {
		return fmt.Errorf("output %q has an unsupported group identity schema", output.Name)
	}
	parts, ok := row["__loom_row_id"].(map[string]any)
	if !ok || len(parts) != 2 {
		return fmt.Errorf("output %q has a malformed group identity object", output.Name)
	}
	revisionID, revisionOK := parts["group_revision_id"].(string)
	groupID, groupOK := parts["group_id"].(string)
	if !revisionOK || strings.TrimSpace(revisionID) == "" || !groupOK || strings.TrimSpace(groupID) == "" {
		return fmt.Errorf("output %q group identity requires non-empty revision and group ids", output.Name)
	}
	encoded, err := json.Marshal(struct {
		GroupRevisionID string `json:"group_revision_id"`
		GroupID         string `json:"group_id"`
	}{GroupRevisionID: revisionID, GroupID: groupID})
	if err != nil {
		return fmt.Errorf("output %q encode group identity: %w", output.Name, err)
	}
	row["__loom_row_id"] = string(encoded)
	return nil
}

func incrementalPublicationOutput(bindings recipe.RuntimeBindings, streams []dataframeexecution.OutputStream) string {
	if len(bindings.OutputNames) != 1 || len(streams) != 1 {
		return ""
	}
	return streams[0].Name
}
