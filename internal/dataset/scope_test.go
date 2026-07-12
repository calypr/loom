package dataset

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestAuthScopeFingerprintCanonicalizesWithoutRetainingPaths(t *testing.T) {
	paths := []string{"project-beta", "project-alpha"}
	first, err := RestrictedAuthScopeFingerprint(paths)
	if err != nil {
		t.Fatalf("RestrictedAuthScopeFingerprint: %v", err)
	}
	second, err := RestrictedAuthScopeFingerprint([]string{"project-alpha", "project-beta"})
	if err != nil {
		t.Fatalf("RestrictedAuthScopeFingerprint reordered: %v", err)
	}
	if !first.Equal(second) {
		t.Fatalf("reordered scopes differ\nfirst:  %#v\nsecond: %#v", first, second)
	}
	paths[0] = "mutated-after-construction"
	if !first.Equal(second) {
		t.Fatal("fingerprint changed after source slice mutation")
	}

	emptyRestricted, err := RestrictedAuthScopeFingerprint(nil)
	if err != nil {
		t.Fatalf("RestrictedAuthScopeFingerprint(empty): %v", err)
	}
	unrestricted := UnrestrictedAuthScopeFingerprint()
	if emptyRestricted.Equal(unrestricted) {
		t.Fatal("restricted empty scope collided with unrestricted scope")
	}
	if got, want := emptyRestricted.Mode(), AuthScopeRestricted; got != want {
		t.Fatalf("empty restricted mode = %q, want %q", got, want)
	}

	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("json.Marshal(AuthScopeFingerprint): %v", err)
	}
	if bytes.Contains(encoded, []byte("project-alpha")) || bytes.Contains(encoded, []byte("project-beta")) {
		t.Fatalf("scope fingerprint JSON exposed raw paths: %s", encoded)
	}
	var decoded AuthScopeFingerprint
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(AuthScopeFingerprint): %v", err)
	}
	if !decoded.Equal(first) {
		t.Fatalf("scope fingerprint did not round trip\ngot:  %#v\nwant: %#v", decoded, first)
	}
}

func TestAuthScopeFingerprintRejectsAmbiguousOrInvalidValues(t *testing.T) {
	for _, paths := range [][]string{
		{"project-a", "project-a"},
		{" project-a"},
		{""},
		{"project-a\nproject-b"},
	} {
		if _, err := RestrictedAuthScopeFingerprint(paths); !errors.Is(err, ErrInvalidAuthScopeFingerprint) {
			t.Errorf("RestrictedAuthScopeFingerprint(%#v) error = %v, want ErrInvalidAuthScopeFingerprint", paths, err)
		}
	}

	for _, raw := range []string{
		`{"mode":"restricted","algorithm":"md5","digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`,
		`{"mode":"restricted","algorithm":"sha256","digest":"not-a-digest"}`,
		`{"mode":"restricted","algorithm":"sha256","digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","unknown":true}`,
	} {
		var fingerprint AuthScopeFingerprint
		if err := json.Unmarshal([]byte(raw), &fingerprint); !errors.Is(err, ErrInvalidAuthScopeFingerprint) {
			t.Errorf("json.Unmarshal(%s) error = %v, want ErrInvalidAuthScopeFingerprint", raw, err)
		}
	}
}
