package recipetest

import (
	_ "embed"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

//go:embed default_aced.json
var defaultACEDJSON []byte

func DefaultACED() (recipe.Bundle, error) { return recipe.Parse(defaultACEDJSON) }
