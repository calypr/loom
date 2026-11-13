package queryapi

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func errorcode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var userErr dataframeerrors.UserError
	if errors.As(err, &userErr) {
		return userErr.Code()
	}
	return "untyped:" + err.Error()
}

func TestClientSuppliedControlCharPathRefused(t *testing.T) {
	svc := NewService(Config{CompileTarget: compiler.ArangoTarget()})
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), nil, "proj-a", "gen-1", []string{"x\x00y"})
	if scope.Mode == authscope.ReadScopeRestricted && reflect.DeepEqual(scope.AuthResourcePaths, []string{"x\x00y"}) && err == nil {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): client-supplied path with NUL was copied raw into a restricted scope (AuthResourcePaths=%q); the \x00-join digest fold of distinct restricted grants remains constructible from request bytes", scope.AuthResourcePaths)
	}
	if err == nil || errorcode(t, err) != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v err=%v code=%q", scope, err, errorcode(t, err))
	}
	if scope.Mode != "" || len(scope.AuthResourcePaths) != 0 {
		t.Fatalf("VERDICT=UNEXPECTED refused request still produced scope %+v", scope)
	}
	t.Logf("VERDICT=REPAIRED all-control-char request refused with CodeInvalidRequest")
}

func TestClientSuppliedMixedPathsNormalized(t *testing.T) {
	svc := NewService(Config{CompileTarget: compiler.ArangoTarget()})
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), nil, "proj-a", "gen-1", []string{"/c-d/", "a-b", "junk\x01x", "a-b"})
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED err=%v", err)
	}
	if reflect.DeepEqual(scope.AuthResourcePaths, []string{"/c-d/", "a-b", "junk\x01x", "a-b"}) {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): requested paths were copied raw into the restricted scope (AuthResourcePaths=%q): duplicates, stray slashes, and the control-byte entry all reach the \x00-join digest", scope.AuthResourcePaths)
	}
	if scope.Mode != authscope.ReadScopeRestricted || !reflect.DeepEqual(scope.AuthResourcePaths, []string{"a-b", "c-d"}) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v", scope)
	}
	t.Logf("VERDICT=REPAIRED mixed request normalized to %q (control-char entry dropped)", scope.AuthResourcePaths)
}

func TestPrincipalGarbageOnlyPathsFailClosed(t *testing.T) {
	svc := NewService(Config{CompileTarget: compiler.ArangoTarget()})
	principal := &authscope.Principal{AuthResourcePaths: []string{"a\x00b"}}
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), principal, "proj-a", "gen-1", nil)
	if scope.Mode == authscope.ReadScopeRestricted && reflect.DeepEqual(scope.AuthResourcePaths, []string{"a\x00b"}) && err == nil {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): IdP-supplied path with NUL survived into the restricted scope (AuthResourcePaths=%q)", scope.AuthResourcePaths)
	}
	if scope.Mode == authscope.ReadScopeUnrestricted {
		t.Fatalf("VERDICT=REGRESSION: principal whose only declared path is garbage was widened to unrestricted scope")
	}
	if err == nil || errorcode(t, err) != string(dataframeerrors.CodeForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v err=%v code=%q", scope, err, errorcode(t, err))
	}
	t.Logf("VERDICT=REPAIRED garbage-only principal paths fail closed with CodeForbidden")
}

func TestMembershipComparedInNormalizedSpace(t *testing.T) {
	svc := NewService(Config{CompileTarget: compiler.ArangoTarget()})
	principal := &authscope.Principal{AuthResourcePaths: []string{"a-b"}}
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), principal, "proj-a", "gen-1", []string{"/a-b/"})
	if err != nil && strings.HasPrefix(errorcode(t, err), string(dataframeerrors.CodeForbidden)) {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived side finding): membership used raw string equality, so the authorized caller requesting %q was refused although it is entitled to %q", []string{"/a-b/"}, "a-b")
	}
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED err=%v code=%q", err, errorcode(t, err))
	}
	if scope.Mode != authscope.ReadScopeRestricted || !reflect.DeepEqual(scope.AuthResourcePaths, []string{"a-b"}) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v", scope)
	}
	t.Logf("VERDICT=REPAIRED slash-variant request matched the authorized path")
}
