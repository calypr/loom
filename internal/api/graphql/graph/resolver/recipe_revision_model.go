package resolver

import (
	"encoding/json"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func recipeRevisionModel(value recipe.RecipeRevision) *model.DataframeRecipeRevision {
	canonical, _ := json.Marshal(value.Bundle)
	var parent *string
	if value.Parent != "" {
		parent = &value.Parent
	}
	var id, executionID, recipeName, translation, generation *string
	if value.ID != "" {
		id = &value.ID
	}
	if value.ExecutionID != "" {
		executionID = &value.ExecutionID
	}
	if value.RecipeName != "" {
		recipeName = &value.RecipeName
	}
	if value.TranslationVersion != "" {
		translation = &value.TranslationVersion
	}
	if value.SourceGeneration != "" {
		generation = &value.SourceGeneration
	}
	outputs := make([]*model.DataframeRecipeRevisionOutput, 0, len(value.Outputs))
	for _, output := range value.Outputs {
		var materializationID *string
		if output.MaterializationID != "" {
			materializationID = &output.MaterializationID
		}
		outputs = append(outputs, &model.DataframeRecipeRevisionOutput{Output: output.Output, MaterializationID: materializationID})
	}
	status := string(value.Status)
	revisionNumber := int(value.RevisionNumber)
	return &model.DataframeRecipeRevision{ID: id, ProjectID: value.Project, Name: value.Name, Digest: value.Digest, ParentDigest: parent, Recipe: canonical, CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"), RevisionNumber: &revisionNumber, Status: &status, RecipeName: recipeName, TranslationVersion: translation, ExecutionID: executionID, SourceGeneration: generation, Outputs: outputs}
}
