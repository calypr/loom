package dataframe

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

func TestAuthorizeMaterializationDropsControlCharPrincipalPathBeforeClickHouseINList(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}, AuthResourcePaths: []string{"a\x00b", "p1"}}
	value := dfmaterialization.Materialization{Project: "P1"}

	access, err := service.authorizeMaterialization(context.Background(), principal, value)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED resolver-less restricted authorization errored: %v", err)
	}
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

func TestAuthorizeMaterializationGarbageOnlyPrincipalPathFailsClosed(t *testing.T) {
	service := NewService(Config{})
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
