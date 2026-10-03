package lower

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func traversalColumnPrefix(naming recipe.TraversalColumnNaming, parentPrefix, alias string) string {
	switch naming.Normalized() {
	case recipe.TraversalColumnNamingExact:
		return ""
	case recipe.TraversalColumnNamingAlias:
		return alias
	default:
		parent := strings.TrimSuffix(parentPrefix, "__")
		if parent == "" {
			return alias
		}
		return parent + "__" + alias
	}
}

func traversalColumnName(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + "__" + local
}

func traversalColumnNamePrefix(prefix string) string {
	if prefix == "" {
		return ""
	}
	return prefix + "__"
}
