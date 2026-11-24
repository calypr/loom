package dataframe

import (
	"context"
	"reflect"
	"testing"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

func TestResolveSelectorRequiresCompleteExplicitSelector(t *testing.T) {
	selector, err := resolveSelector(&dataframemodel.DataframeSelectorInput{Recipe: "documents", TranslationVersion: "v2", Output: "Patient"})
	if err != nil {
		t.Fatal(err)
	}
	if selector.Recipe != "documents" || selector.TranslationVersion != "v2" || selector.Output != "Patient" {
		t.Fatalf("selector = %#v", selector)
	}
}

func TestResolveSelectorRejectsMissingSelector(t *testing.T) {
	_, err := resolveSelector(nil)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidSelector) {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeProjectUsesPrincipalProjectGrant(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}}
	if err := service.authorizeProject(context.Background(), principal, "P1"); err != nil {
		t.Fatal(err)
	}
	if err := service.authorizeProject(context.Background(), principal, "P2"); err == nil {
		t.Fatal("unauthorized project was accepted")
	}
}

func TestAuthorizeProjectRequiresProject(t *testing.T) {
	service := NewService(Config{})
	err := service.authorizeProject(context.Background(), &authscope.Principal{}, " ")
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeRestrictedMaterializationRequiresResourcePathsWithoutResolver(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}}
	_, err := service.authorizeMaterialization(context.Background(), principal, dfmaterialization.Materialization{Project: "P1"})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDatasetNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeUnrestrictedMaterializationWithoutResolver(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}}
	access, err := service.authorizeMaterialization(context.Background(), principal, dfmaterialization.Materialization{Project: "P1", ScopeUnrestricted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !access.unrestricted || len(access.authResourcePaths) != 0 {
		t.Fatalf("access = %#v", access)
	}
}

func TestMapReaderErrorUsesDataframeReadAuthorizationPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		code      string
		retryable bool
	}{
		{name: "forbidden", err: authscope.ErrForbidden, code: "FORBIDDEN"},
		{name: "authorization outage", err: authscope.ErrAuthorizationBackendUnavailable, code: "BACKEND_UNAVAILABLE", retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mapped := mapReaderError(test.err)
			userErr, ok := dataframeerrors.AsUserError(mapped)
			if !ok || userErr.Code() != test.code || userErr.Retryable() != test.retryable {
				t.Fatalf("mapped error = %#v, want code=%s retryable=%t", mapped, test.code, test.retryable)
			}
		})
	}
}

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
