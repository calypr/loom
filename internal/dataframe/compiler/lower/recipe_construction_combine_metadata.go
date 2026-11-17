package lower

import "github.com/calypr/loom/internal/dataframe/recipe"

func ConstructionCombineColumnType(column recipe.StageColumn) (logical, physical string, err error) {
	return constructionCombineColumnType(column)
}
