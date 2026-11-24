package physical

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TraversalColumnPrefix(naming recipe.TraversalColumnNaming, parentPrefix, alias string) string {
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

func TraversalColumnName(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + "__" + local
}

func TraversalColumnNamePrefix(prefix string) string {
	if prefix == "" {
		return ""
	}
	return prefix + "__"
}

func NativePivotColumnName(prefix, family, key string, aliases map[string]string) string {
	if alias, ok := aliases[key]; ok {
		return prefix + alias
	}
	return prefix + family + "__" + SanitizeColumnName(key)
}

func NativeTraversalColumnPrefix(naming recipe.TraversalColumnNaming, parentPrefix, alias string) string {
	return TraversalColumnPrefix(naming, parentPrefix, alias)
}
