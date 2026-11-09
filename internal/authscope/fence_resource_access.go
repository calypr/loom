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

// defaultFenceAccessCacheEntries bounds how many parsed Fence snapshots one
// client retains. A snapshot only has to survive one TTL, so the useful working
// set is the number of distinct bearer tokens seen within a TTL. Token churn
// keeps minting sha256 keys that are never read again, and the TTL only decides
// whether a snapshot is served, never whether its memory is released: the entry
// cap is the memory bound, and the expiry sweeps on read and on insert are what
// keep churned-away snapshots from holding slots. Past the cap the least
// recently used token loses its slot and simply refetches.
const defaultFenceAccessCacheEntries = 1024

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

type cachedResourceAccess struct {
	snapshot  map[string][]resourceAccessRecord
	expiresAt time.Time
	// seq orders cache touches (inserts and served hits) so cap eviction is
	// deterministic and never depends on map iteration order.
	seq uint64
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

// newFenceUserAccessClientWithCachePolicy is the seam used by tests to pin the
// entry bound and the clock. A maxEntries below one disables retention
// instead of admitting nothing.
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

func (c *FenceUserAccessClient) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// ValidateTrustedFenceIssuer requires an absolute HTTP(S) issuer URL without
// userinfo, query, or fragment. The URL path is allowed because some issuers
// expose their user endpoint below a base path.
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
			// A served hit makes this snapshot the most recently used, so
			// cap eviction discards churned-away tokens first.
			cached.seq = c.nextAccessSeqLocked()
			c.cache[key] = cached
			c.mu.Unlock()
			return snapshot, nil
		}
		// Expired entries are never served, so release the slot instead of
		// leaving token churn to accumulate dead snapshots.
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

// storeResourceAccessLocked installs a fresh snapshot valid until now+TTL and
// holds the cache within maxCacheEntries: snapshots past their TTL are dropped
// first (they can never be served again), then the least-recently-used entries
// until the cap is met. Eviction removes whole per-token keys, so one token can
// never lose grants belonging to another. Callers must hold c.mu.
func (c *FenceUserAccessClient) storeResourceAccessLocked(key string, snapshot map[string][]resourceAccessRecord, now time.Time) {
	c.cache[key] = cachedResourceAccess{snapshot: cloneResourceAccess(snapshot), expiresAt: now.Add(c.cacheTTL), seq: c.nextAccessSeqLocked()}
	for token, entry := range c.cache {
		if !now.Before(entry.expiresAt) {
			delete(c.cache, token)
		}
	}
	for len(c.cache) > c.maxCacheEntries {
		victim, ok := c.leastRecentlyUsedCacheKeyLocked()
		if !ok {
			return
		}
		delete(c.cache, victim)
	}
}

func (c *FenceUserAccessClient) nextAccessSeqLocked() uint64 {
	c.accessSeq++
	return c.accessSeq
}

func (c *FenceUserAccessClient) leastRecentlyUsedCacheKeyLocked() (string, bool) {
	var victim string
	var oldest uint64
	found := false
	for token, entry := range c.cache {
		if !found || entry.seq < oldest {
			victim = token
			oldest = entry.seq
			found = true
		}
	}
	return victim, found
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
	// Control characters (including NUL) must never participate in scope
	// paths: every scope-digest encoder joins normalized paths with \x00,
	// so a NUL inside one declared path folds distinct restricted grant
	// lists onto one digest (finding F-B22). Such declarations are refused
	// at ingest; consumers already treat an empty normalization as absent.
	if containsAuthPathControlChar(value) {
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
			if containsAuthPathControlChar(key) {
				continue
			}
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

func cleanAccessToken(raw string) string {
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	return token
}

func calyprProjectKeyFromResourcePath(resource string) (string, bool) {
	normalized := normalizeResourcePath(resource)
	if containsAuthPathControlChar(normalized) {
		return "", false
	}
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

// NormalizeAuthResourcePaths is the exported view of ingest-time scope-path
// normalization, for callers that build restricted read-scope paths without a
// wired ScopeResolver. Entries normalizing to empty or containing control
// characters are dropped (see the F-B22 note in NormalizeAuthResourcePath).
func NormalizeAuthResourcePaths(paths []string) []string {
	return normalizeAuthResourcePathList(paths)
}

// containsAuthPathControlChar reports whether value holds an ASCII control
// byte (C0 range or DEL). Those bytes never legitimately appear in
// authorization resource paths and they enable the F-B22 digest fold.
func containsAuthPathControlChar(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return true
		}
	}
	return false
}
