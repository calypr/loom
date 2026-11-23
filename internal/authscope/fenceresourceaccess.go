package authscope

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FenceUserAccessClient struct {
	client          *http.Client
	trustedIssuer   string
	cacheTTL        time.Duration
	maxCacheEntries int
	now             func() time.Time
	mu              sync.Mutex
	cache           map[string]cachedResourceAccess
	accessSeq       uint64
}

func NewFenceUserAccessClientWithTrustedIssuer(client *http.Client, trustedIssuer string, cacheTTL time.Duration) (*FenceUserAccessClient, error) {
	if err := ValidateTrustedFenceIssuer(trustedIssuer); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return newFenceUserAccessClient(&clientCopy, trustedIssuer, cacheTTL), nil
}

func newFenceUserAccessClient(client *http.Client, trustedIssuer string, cacheTTL time.Duration) *FenceUserAccessClient {
	return newFenceUserAccessClientWithCachePolicy(client, trustedIssuer, cacheTTL, defaultFenceAccessCacheEntries, nil)
}

func newFenceUserAccessClientWithCachePolicy(client *http.Client, trustedIssuer string, cacheTTL time.Duration, maxEntries int, now func() time.Time) *FenceUserAccessClient {
	if client == nil {
		client = http.DefaultClient
	}
	if cacheTTL <= 0 {
		cacheTTL = 30 * time.Second
	}
	if maxEntries < 1 {
		maxEntries = 1
	}
	return &FenceUserAccessClient{
		client:          client,
		trustedIssuer:   trustedIssuer,
		cacheTTL:        cacheTTL,
		maxCacheEntries: maxEntries,
		now:             now,
		cache:           make(map[string]cachedResourceAccess),
	}
}

func ValidateTrustedFenceIssuer(issuer string) error {
	_, err := parseTrustedFenceIssuer(issuer)
	return err
}

func parseTrustedFenceIssuer(issuer string) (*url.URL, error) {
	if issuer == "" || strings.TrimSpace(issuer) != issuer {
		return nil, fmt.Errorf("trusted Fence issuer is required and must not have surrounding whitespace")
	}
	parsed, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("trusted Fence issuer must be an absolute HTTP(S) URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Opaque != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, fmt.Errorf("trusted Fence issuer must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("trusted Fence issuer must not include userinfo, query, or fragment")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("trusted Fence issuer has an invalid port")
		}
	}
	return parsed, nil
}

func (c *FenceUserAccessClient) GetAllowedResources(ctx context.Context, authorizationHeader, method, service string) ([]string, error) {
	snapshot, err := c.getResourceAccess(ctx, authorizationHeader)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(snapshot))
	for resource, records := range snapshot {
		if resourceAccessAllows(records, method, service) {
			out = append(out, resource)
		}
	}
	sort.Strings(out)
	return out, nil
}

type resourceAccessRecord struct {
	Method  string
	Service string
}

func (c *FenceUserAccessClient) getResourceAccess(ctx context.Context, authorizationHeader string) (map[string][]resourceAccessRecord, error) {
	authHeader, err := validateAuthorizationHeader(authorizationHeader)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	issuer, err := issuerFromAuthorizationHeader(authHeader)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	if issuer != c.trustedIssuer {
		return nil, fmt.Errorf("%w: token issuer does not match the configured Fence issuer", ErrUnauthenticated)
	}
	sum := sha256.Sum256([]byte(authHeader))
	key := fmt.Sprintf("%x", sum[:])
	now := c.currentTime()
	c.mu.Lock()
	if cached, ok := c.cache[key]; ok {
		if now.Before(cached.expiresAt) {
			snapshot := cloneResourceAccess(cached.snapshot)

			cached.seq = c.nextAccessSeqLocked()
			c.cache[key] = cached
			c.mu.Unlock()
			return snapshot, nil
		}

		delete(c.cache, key)
	}
	c.mu.Unlock()
	endpoint, err := fenceUserEndpoint(c.trustedIssuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAuthorizationBackendUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build authorization snapshot request: %w", ErrAuthorizationBackendUnavailable, err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: authorization snapshot request failed: %w", ErrAuthorizationBackendUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read authorization snapshot response: %w", ErrAuthorizationBackendUnavailable, err)
	}
	if resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: authorization snapshot rejected", ErrUnauthenticated)
		}
		if resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: authorization snapshot denied", ErrForbidden)
		}
		return nil, fmt.Errorf("%w: authorization snapshot request failed with HTTP %d", ErrAuthorizationBackendUnavailable, resp.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: invalid authorization snapshot response: %w", ErrAuthorizationBackendUnavailable, err)
	}
	snapshot, err := parseResourceAccessSnapshot(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid authorization snapshot response: %w", ErrAuthorizationBackendUnavailable, err)
	}
	c.mu.Lock()
	c.storeResourceAccessLocked(key, snapshot, c.currentTime())
	c.mu.Unlock()
	return snapshot, nil
}

func parseResourceAccessSnapshot(payload map[string]any) (map[string][]resourceAccessRecord, error) {
	resourceAccess, ok := payload["authz"].(map[string]any)
	if !ok || len(resourceAccess) == 0 {
		resourceAccess, ok = payload["project_access"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("authorization snapshot response did not include authz/project_access")
		}
	}
	out := make(map[string][]resourceAccessRecord, len(resourceAccess))
	for resource, raw := range resourceAccess {
		entries, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, entry := range entries {
			record, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			method, _ := record["method"].(string)
			service, _ := record["service"].(string)
			out[resource] = append(out[resource], resourceAccessRecord{Method: method, Service: service})
		}
	}
	return out, nil
}

func resourceAccessAllows(records []resourceAccessRecord, method, service string) bool {
	for _, record := range records {
		if !strings.EqualFold(record.Method, method) && record.Method != "*" {
			continue
		}
		if record.Service == service || record.Service == "*" || service == "*" {
			return true
		}
	}
	return false
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func issuerFromAuthorizationHeader(authorizationHeader string) (string, error) {
	token := cleanAccessToken(authorizationHeader)
	if token == "" {
		return "", fmt.Errorf("authorization header is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("authorization token does not look like a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode authorization token payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse authorization token payload: %w", err)
	}
	issuer, _ := claims["iss"].(string)
	if issuer == "" {
		return "", fmt.Errorf("authorization token does not include iss")
	}
	return issuer, nil
}

func fenceUserEndpoint(trustedIssuer string) (string, error) {
	issuer, err := parseTrustedFenceIssuer(trustedIssuer)
	if err != nil {
		return "", err
	}
	issuer.Path = strings.TrimRight(issuer.Path, "/") + "/user"
	issuer.RawPath = ""
	return issuer.String(), nil
}

func validateAuthorizationHeader(raw string) (string, error) {
	header := strings.TrimSpace(raw)
	if header == "" {
		return "", fmt.Errorf("authorization header is required")
	}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", fmt.Errorf("authorization header must use bearer auth")
	}
	return header, nil
}
