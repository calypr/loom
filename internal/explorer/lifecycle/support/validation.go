package support

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
)

func RequireExactIdentity(value, name string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be an exact non-empty token", name)
	}
	return nil
}

func ValidateAuthorizedReadScope(scope authscope.ReadScope, expectedDigest string) error {
	switch scope.Mode {
	case authscope.ReadScopeUnrestricted:
		if len(scope.AuthResourcePaths) != 0 {
			return fmt.Errorf("unrestricted scope must not carry resource paths")
		}
	case authscope.ReadScopeRestricted:

	default:
		return fmt.Errorf("authorization scope mode is missing or invalid")
	}
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	if hex.EncodeToString(sum[:]) != expectedDigest {
		return fmt.Errorf("authorized scope does not match receipt scope digest")
	}
	return nil
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
