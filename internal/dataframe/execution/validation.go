package execution

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

type Validation struct {
	Entry recipe.Entry
	Plan  recipeplan.RecipePlan
}

type Preview struct {
	Plan    recipeplan.ResolvedRecipePlan
	Outputs []OutputRows
}

type OutputRows struct {
	Name    string
	Columns []string
	Rows    []map[string]any
}
