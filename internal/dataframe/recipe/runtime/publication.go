package runtime

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

func ScopeDigest(bindings recipe.RuntimeBindings) string {
	paths := append([]string(nil), bindings.AuthResourcePaths...)
	sort.Strings(paths)
	hash := sha256.Sum256([]byte(bindings.Project + "\x00" + bindings.DatasetGeneration + "\x00" + string(bindings.AuthScopeMode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(hash[:])
}

type PublishRequest struct {
	Engine                 *dataframeexecution.Engine
	Target                 publication.Target
	Name                   string
	Bindings               recipe.RuntimeBindings
	Resolved               dataframeexecution.Resolved
	ReceiptID              string
	SourceRows             map[string]*publication.SourceRowMetadata
	BatchRows              int
	BatchBytes             int
	QualityMaxRows         int64
	QualityMaxDistinctKeys int64
}

func PublishResolvedRecipe(ctx context.Context, request PublishRequest) (publication.BundleIdentity, error) {
	streams, err := request.Engine.Streams(ctx, request.Resolved)
	if err != nil {
		return publication.BundleIdentity{}, err
	}
	identity := publication.BundleIdentity{
		ReceiptID: request.ReceiptID,
		Name:      request.Name, TranslationVersion: request.Resolved.Semantic.SemanticPlan.TranslationVersion,
		OutputName: incrementalPublicationOutput(request.Bindings, streams),
		Project:    request.Bindings.Project, DatasetGeneration: request.Bindings.DatasetGeneration,
		RecipeDigest: request.Resolved.StoredRecipeDigest, SchemaDigest: request.Resolved.ResolvedSchemaDigest,
		ScopeDigest: request.Resolved.Semantic.ScopeDigest, EngineVersion: "loom-recipe-v2",
		AuthScopeMode:     string(request.Bindings.AuthScopeMode),
		AuthResourcePaths: append([]string(nil), request.Bindings.AuthResourcePaths...),
	}
	streamInputs := make([]publication.OutputStream, 0, len(streams))
	for _, stream := range streams {
		stream := stream
		compiledOutput, ok := recipeCompiledOutput(request.Resolved, stream.Name)
		if !ok {
			return publication.BundleIdentity{}, fmt.Errorf("compiled recipe output %q is missing", stream.Name)
		}
		columns := PublicationColumns(request.Resolved, stream.Name)
		rootResourceType := compiledOutput.RootResourceType
		exactRootColumns := compiledOutput.RootColumnNaming == recipe.RootColumnNamingExact
		streamInputs = append(streamInputs, publication.OutputStream{
			Name: stream.Name, Columns: columns,
			SourceRow: request.SourceRows[stream.Name],
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
		ReceiptID: request.ReceiptID,
		Name:      identity.Name, TranslationVersion: identity.TranslationVersion,
		OutputName: identity.OutputName,
		Project:    identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
		AuthScopeMode:     identity.AuthScopeMode,
		AuthResourcePaths: append([]string(nil), request.Bindings.AuthResourcePaths...),
	}
	_, err = publication.Publish(ctx, request.Target, publicationIdentity, streamInputs, publication.Limits{
		BatchRows: request.BatchRows, BatchBytes: request.BatchBytes,
		Quality: publication.QualityPolicy{
			Version: publication.DefaultQualityPolicyVersion, MaxRows: request.QualityMaxRows,
			MaxDistinctKeys: request.QualityMaxDistinctKeys, RequireUniqueIdentity: true,
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
	if identity == nil || identity.Grain != spec.RowGrainGroups {
		return fmt.Errorf("output %q has an unsupported group identity schema", output.Name)
	}
	if len(identity.Fields) == 1 && identity.Fields[0] == "__loom_row_id" {
		generatedIdentitySchema := 0
		for _, column := range output.OutputSchema {
			if column.Name != "__loom_row_id" {
				continue
			}
			if column.ID != "__loom_row_id" || !column.Internal || !column.Identity ||
				column.Kind != "string" || column.Cardinality != "required_one" {
				return fmt.Errorf("output %q has an unsupported group identity schema", output.Name)
			}
			generatedIdentitySchema++
		}
		if generatedIdentitySchema != 1 {
			return fmt.Errorf("output %q has an unsupported group identity schema", output.Name)
		}
		rowID, ok := row["__loom_row_id"].(string)
		if !ok || strings.TrimSpace(rowID) == "" {
			return fmt.Errorf("output %q has a malformed generated group identity", output.Name)
		}
		return nil
	}
	if len(identity.Fields) != 2 || identity.Fields[0] != "group_revision_id" || identity.Fields[1] != "group_id" {
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
