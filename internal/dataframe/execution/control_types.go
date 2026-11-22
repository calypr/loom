package execution

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type Validation struct {
	Entry recipe.Entry
	Plan  semantic.RecipePlan
}

type Preview struct {
	Plan    semantic.ResolvedRecipePlan
	Outputs []OutputRows
}

type OutputRows struct {
	Name    string
	Columns []string
	Rows    []map[string]any
}
