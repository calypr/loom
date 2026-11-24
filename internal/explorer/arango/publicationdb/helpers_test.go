package publicationdb

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func projectConfigTestBundle(name, translation string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name, TranslationVersion: translation,
		Outputs: []recipe.Output{{Name: "output", RootResourceType: "Patient", RowGrain: "resource"}},
	}
}
