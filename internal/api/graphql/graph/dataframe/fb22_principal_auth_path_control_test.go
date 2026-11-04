package dataframe

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

// F-B22 Tier-1 durable control (REPAIR-18 / U1): the resolver-less
// authorizeMaterialization branch in this package's service.go copied
// principal.AuthResourcePaths RAW into projectAccess.authResourcePaths. That
// slice is handed verbatim to PageRequest.AuthResourcePaths (rows.go Rows and
// export.go), which becomes the ClickHouse `auth_resource_path IN ?` bind list
// in published/page.go (and Stream), bypassing the boundary-normalization
// contract that repair-7 applied at
// internal/api/graphql/graph/query/input_resolution.go via
// authscope.NormalizeAuthResourcePaths. The reviewer's threat: a principal
// that mixes a control-byte entry into its declared paths reaches the IN-list
// with that raw NUL entry, so a NUL-bearing grant still participates in the
// \x00-joined scope digest that folds two distinct restricted grants onto one
// digest (finding F-B22).
//
// The archived CONFIRMED shape (raw copy, no normalization) is preserved
// verbatim below as the failing branch, so a regression re-opens loudly the
// same way the sibling queryapi REPAIR-7 control does.

func TestAuthorizeMaterializationDropsControlCharPrincipalPathBeforeClickHouseINList(t *testing.T) {
	service := NewService(Config{}) // nil scopeResolver selects the resolver-less :85 branch
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}, AuthResourcePaths: []string{"a\x00b", "p1"}}
	value := dfmaterialization.Materialization{Project: "P1"} // restricted: ScopeUnrestricted == false

	access, err := service.authorizeMaterialization(context.Background(), principal, value)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED resolver-less restricted authorization errored: %v", err)
	}
	// access.authResourcePaths is the exact slice bound to
	// PageRequest.AuthResourcePaths -> `auth_resource_path IN ?` (page.go:77).
	if reflect.DeepEqual(access.authResourcePaths, []string{"a\x00b", "p1"}) {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): principal.AuthResourcePaths were copied raw into projectAccess; the ClickHouse auth_resource_path IN ? list is %q and still carries the NUL entry, so a NUL-bearing grant reaches the \\x00-join digest", access.authResourcePaths)
	}
	if access.unrestricted {
		t.Fatalf("VERDICT=REGRESSION: a restricted principal was widened to unrestricted access")
	}
	if !reflect.DeepEqual(access.authResourcePaths, []string{"p1"}) {
		t.Fatalf("VERDICT=UNEXPECTED CH auth_resource_path IN-list = %q, want [\"p1\"]", access.authResourcePaths)
	}
	t.Logf("VERDICT=REPAIRED CH auth_resource_path IN-list = %q; boundary normalization dropped the NUL-bearing entry", access.authResourcePaths)
}

// A principal whose only declared path is control-byte garbage must fail
// closed at the resolver-less boundary instead of reaching the IN-list as a
// restricted-but-empty scope (which the AQL layer could mistake for an
// unrestricted scope) or as a raw control-byte grant.
func TestAuthorizeMaterializationGarbageOnlyPrincipalPathFailsClosed(t *testing.T) {
	service := NewService(Config{}) // nil scopeResolver selects the resolver-less :85 branch
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}, AuthResourcePaths: []string{"a\x00b"}}
	value := dfmaterialization.Materialization{Project: "P1"}

	access, err := service.authorizeMaterialization(context.Background(), principal, value)
	if access.unrestricted {
		t.Fatalf("VERDICT=REGRESSION: a garbage-only principal was widened to unrestricted access")
	}
	if err != nil {
		t.Logf("VERDICT=REPAIRED garbage-only principal paths fail closed: %v", err)
		return
	}
	if len(access.authResourcePaths) != 0 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): control-byte-only grant reached the CH IN-list as %q", access.authResourcePaths)
	}
	t.Logf("VERDICT=REPAIRED garbage-only principal produced an empty restricted IN-list (fail-closed, no raw control byte)")
}
