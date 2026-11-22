package authscope

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

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

		_ = subtle.ConstantTimeCompare(leftBytes, rightBytes)
		return false
	}
	return subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}

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

type BearerTokenAuthenticator struct{}

func (a BearerTokenAuthenticator) Authenticate(ctx context.Context, headers map[string][]string) (*Principal, error) {
	principal := &Principal{Subject: "anonymous"}
	auth := firstHeaderValue(headers, "Authorization")
	if auth == "" {
		return principal, nil
	}
	principal.AuthorizationHeader = strings.TrimSpace(auth)
	if sub := subjectFromAuthorizationHeader(auth); sub != "" {
		principal.Subject = sub
	}
	return principal, nil
}

func firstHeaderValue(headers map[string][]string, key string) string {
	for headerKey, values := range headers {
		if !strings.EqualFold(headerKey, key) || len(values) == 0 {
			continue
		}
		return values[0]
	}
	return ""
}

func subjectFromAuthorizationHeader(authHeader string) string {
	token := cleanAccessToken(authHeader)
	if token == "" {
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if sub, _ := claims["sub"].(string); sub != "" {
		return sub
	}
	if user, _ := claims["user_name"].(string); user != "" {
		return user
	}
	return ""
}

func cleanAccessToken(raw string) string {
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	return token
}
