package authscope

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// BasicAuthenticator authenticates the standalone/operator mode. A basic
// principal is deliberately unrestricted: Basic is a process-level operator
// credential, not a substitute for a per-project Fence ACL.
type BasicAuthenticator struct {
	Username string
	Password string
}

func (a BasicAuthenticator) Authenticate(_ context.Context, headers map[string][]string) (*Principal, error) {
	raw := strings.TrimSpace(firstHeaderValue(headers, "Authorization"))
	if raw == "" {
		return nil, fmt.Errorf("%w", ErrUnauthenticated)
	}
	prefix, encoded, ok := strings.Cut(raw, " ")
	if !ok || !strings.EqualFold(prefix, "basic") || strings.TrimSpace(encoded) == "" {
		return nil, fmt.Errorf("%w: basic authentication required", ErrUnauthenticated)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid credentials", ErrUnauthenticated)
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok || !constantTimeEqual(username, a.Username) || !constantTimeEqual(password, a.Password) {
		return nil, fmt.Errorf("%w: invalid credentials", ErrUnauthenticated)
	}
	return &Principal{Subject: username}, nil
}

func constantTimeEqual(left, right string) bool {
	leftBytes, rightBytes := []byte(left), []byte(right)
	if len(leftBytes) != len(rightBytes) {
		// Still perform a comparison to avoid making the common length mismatch
		// an immediately observable fast path.
		_ = subtle.ConstantTimeCompare(leftBytes, rightBytes)
		return false
	}
	return subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}

// CalyprAuthenticator is the strict HTTP boundary for Fence-backed mode. It
// validates presence and shape here; the ScopeResolver performs the signed
// resource authorization lookup using the same bearer header.
type CalyprAuthenticator struct{}

func (CalyprAuthenticator) Authenticate(_ context.Context, headers map[string][]string) (*Principal, error) {
	auth := strings.TrimSpace(firstHeaderValue(headers, http.CanonicalHeaderKey("Authorization")))
	if auth == "" {
		return nil, fmt.Errorf("%w", ErrUnauthenticated)
	}
	if _, err := validateAuthorizationHeader(auth); err != nil {
		return nil, err
	}
	principal := &Principal{AuthorizationHeader: auth, Subject: "anonymous"}
	if sub := subjectFromAuthorizationHeader(auth); sub != "" {
		principal.Subject = sub
	}
	return principal, nil
}
