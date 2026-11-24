package recipeplan

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type ExecutionContext struct {
	Project                    string
	DatasetGeneration          string
	AuthResourcePaths          []string
	AuthScopeMode              authscope.ReadScopeMode
	SelectionProject           string
	SelectionMembersCollection string
	GroupRowsCollections       recipe.GroupRowsCollections `json:"-"`
}
