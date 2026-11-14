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

// NativePivotColumnName exposes the compiler's exact frozen native pivot-key
// naming correspondence to import authoring. Prefix is the already-normalized
// traversal projection prefix, including its separator when nonempty.
func NativePivotColumnName(prefix, family, key string, aliases map[string]string) string {
	if alias, ok := aliases[key]; ok {
		return prefix + alias
	}
	return prefix + family + "__" + sanitizeColumnName(key)
}

// NativeTraversalColumnPrefix returns the existing compiler namespace for a
// traversal occurrence. Import identity inspection uses the same naming rules.
func NativeTraversalColumnPrefix(naming recipe.TraversalColumnNaming, parentPrefix, alias string) string {
	return traversalColumnPrefix(naming, parentPrefix, alias)
}
