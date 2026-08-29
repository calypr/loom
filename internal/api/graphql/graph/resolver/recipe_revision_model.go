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
	return &model.DataframeRecipeRevision{ProjectID: value.Project, Name: value.Name, Digest: value.Digest, ParentDigest: parent, Recipe: canonical, CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00")}
}
