package authscope

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type FenceUserAccessClient struct {
	client   *http.Client
	cacheTTL time.Duration
	mu       sync.Mutex
	cache    map[string]cachedResourceAccess
}

type cachedResourceAccess struct {
	snapshot  map[string][]resourceAccessRecord
	expiresAt time.Time
}

func NewFenceUserAccessClient(client *http.Client) *FenceUserAccessClient {
	return NewFenceUserAccessClientWithTTL(client, 30*time.Second)
}

func NewFenceUserAccessClientWithTTL(client *http.Client, cacheTTL time.Duration) *FenceUserAccessClient {
	if client == nil {
		client = http.DefaultClient
	}
	if cacheTTL <= 0 {
		cacheTTL = 30 * time.Second
	}
	return &FenceUserAccessClient{client: client, cacheTTL: cacheTTL, cache: make(map[string]cachedResourceAccess)}
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
	sum := sha256.Sum256([]byte(authorizationHeader))
	key := fmt.Sprintf("%x", sum[:])
	now := time.Now()
	c.mu.Lock()
	if cached, ok := c.cache[key]; ok && now.Before(cached.expiresAt) {
		snapshot := cloneResourceAccess(cached.snapshot)
		c.mu.Unlock()
		return snapshot, nil
	}
	c.mu.Unlock()
	endpoint, err := fenceUserEndpoint(authorizationHeader)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAuthorizationBackendUnavailable, err)
	}
	authHeader, err := validateAuthorizationHeader(authorizationHeader)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
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
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("%w: authorization snapshot rejected", ErrUnauthenticated)
		}
		if resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: authorization snapshot denied", ErrForbidden)
		}
		return nil, fmt.Errorf("%w: authorization snapshot request failed", ErrAuthorizationBackendUnavailable)
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
	c.cache[key] = cachedResourceAccess{snapshot: cloneResourceAccess(snapshot), expiresAt: time.Now().Add(c.cacheTTL)}
	c.mu.Unlock()
	return snapshot, nil
}

func cloneResourceAccess(in map[string][]resourceAccessRecord) map[string][]resourceAccessRecord {
	out := make(map[string][]resourceAccessRecord, len(in))
	for resource, records := range in {
		out[resource] = append([]resourceAccessRecord(nil), records...)
	}
	return out
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

func NormalizeAuthResourcePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if key, ok := calyprProjectKeyFromResourcePath(value); ok {
		return key
	}
	return strings.Trim(value, "/")
}

func authResourcePathsFromResources(resources []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		if key, ok := calyprProjectKeyFromResourcePath(resource); ok {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeAuthResourcePathList(paths []string) []string {
	if paths == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		normalized := NormalizeAuthResourcePath(path)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
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

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
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

func fenceUserEndpoint(authorizationHeader string) (string, error) {
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
	iss, _ := claims["iss"].(string)
	iss = strings.TrimSpace(iss)
	if iss == "" {
		return "", fmt.Errorf("authorization token does not include iss")
	}
	return strings.TrimRight(iss, "/") + "/user", nil
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

func cleanAccessToken(raw string) string {
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	return token
}

func calyprProjectKeyFromResourcePath(resource string) (string, bool) {
	normalized := normalizeResourcePath(resource)
	parts := strings.Split(normalized, "/")
	if len(parts) < 5 {
		return "", false
	}
	if parts[1] != "programs" || parts[3] != "projects" {
		return "", false
	}
	if parts[2] == "" || parts[4] == "" {
		return "", false
	}
	return parts[2] + "-" + parts[4], true
}

func normalizeResourcePath(resource string) string {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return ""
	}
	resource = "/" + strings.Trim(resource, "/")
	return strings.TrimSuffix(resource, "/")
}
