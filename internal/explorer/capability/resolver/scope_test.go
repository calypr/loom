package resolver

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
)

func TestValidateAuthorizedReadScopePreservesRestrictedEmpty(t *testing.T) {
	expected := ScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeRestricted})
	if err := ValidateAuthorizedReadScope(authscope.ReadScope{Mode: authscope.ReadScopeRestricted}, expected); err != nil {
		t.Fatalf("restricted-empty scope rejected: %v", err)
	}
	if err := ValidateAuthorizedReadScope(authscope.ReadScope{}, expected); !errors.Is(err, explorer.ErrReceiptExecutionContract) {
		t.Fatalf("empty scope error=%v, want contract mismatch", err)
	}
	if err := ValidateAuthorizedReadScope(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, expected); !errors.Is(err, explorer.ErrReceiptExecutionContract) {
		t.Fatalf("widened scope error=%v, want contract mismatch", err)
	}
}
