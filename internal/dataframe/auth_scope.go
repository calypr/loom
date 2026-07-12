package dataframe

import "github.com/calypr/loom/internal/authscope"

// effectiveAuthScopeUnrestricted preserves the legacy direct-Builder contract
// while making resolved request scopes authoritative. In particular, a
// resolver-provided restricted empty path set returns false.
func effectiveAuthScopeUnrestricted(paths []string, mode authscope.ReadScopeMode) bool {
	switch mode {
	case authscope.ReadScopeUnrestricted:
		return true
	case authscope.ReadScopeRestricted:
		return false
	case "":
		return len(paths) == 0
	default:
		// An invalid internal mode must fail closed rather than bypass scope.
		return false
	}
}

func builderAuthScopeUnrestricted(builder Builder) bool {
	return effectiveAuthScopeUnrestricted(builder.AuthResourcePaths, builder.AuthScopeMode)
}

func semanticAuthScopeUnrestricted(plan SemanticPlan) bool {
	return effectiveAuthScopeUnrestricted(plan.AuthResourcePaths, plan.AuthScopeMode)
}
