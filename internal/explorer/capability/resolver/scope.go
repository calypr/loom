package resolver

import (
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
)

func ValidateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if len(scope.AuthResourcePaths) != 0 {
			return fmt.Errorf("%w: unrestricted scope must not carry resource paths", explorer.ErrReceiptExecutionContract)
		}
	case authscope.ReadScopeRestricted:
	default:
		return fmt.Errorf("%w: authorization scope mode is missing or invalid", explorer.ErrReceiptExecutionContract)
	}
	if ScopeDigest(scope) != expectedDigest {
		return fmt.Errorf("%w: authorized scope does not match receipt scope digest", explorer.ErrReceiptExecutionContract)
	}
	return nil
}
