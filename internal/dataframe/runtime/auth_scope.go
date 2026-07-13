package runtime

import (
	"github.com/calypr/loom/internal/authscope"
)

// builderAuthScopeUnrestricted preserves the historical runtime preparation
// contract while the compiler-owned semantic scope helper lives in compiler.
func builderAuthScopeUnrestricted(builder Builder) bool {
	switch builder.AuthScopeMode {
	case authscope.ReadScopeUnrestricted:
		return true
	case authscope.ReadScopeRestricted:
		return false
	case "":
		return len(builder.AuthResourcePaths) == 0
	default:
		return false
	}
}
