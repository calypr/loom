package lower

import "github.com/calypr/loom/internal/dataframe/recipe"

// ConstructionCombineColumnType exposes the lowerer's authoritative physical
// type mapping for receipt-bound workspace schema metadata.
func ConstructionCombineColumnType(column recipe.StageColumn) (logical, physical string, err error) {
	return constructionCombineColumnType(column)
}
