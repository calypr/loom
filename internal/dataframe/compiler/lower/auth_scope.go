package lower

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func effectiveAuthScopeUnrestricted(paths []string, mode authscope.ReadScopeMode) bool {
	switch mode {
	case authscope.ReadScopeUnrestricted:
		return true
	case authscope.ReadScopeRestricted:
		return false
	case "":
		return len(paths) == 0
	default:

		return false
	}
}

func semanticAuthScopeUnrestricted(context recipeplan.ExecutionContext) bool {
	return effectiveAuthScopeUnrestricted(context.AuthResourcePaths, context.AuthScopeMode)
}
