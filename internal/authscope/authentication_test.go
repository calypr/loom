package authscope

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBasicAuthenticator(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("loom:secret"))
	principal, err := (BasicAuthenticator{Username: "loom", Password: "secret"}).Authenticate(context.Background(), map[string][]string{"Authorization": {"Basic " + encoded}})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if principal.Subject != "loom" || principal.AuthorizationHeader != "" {
		t.Fatalf("principal = %#v", principal)
	}
	if _, err := (BasicAuthenticator{Username: "loom", Password: "secret"}).Authenticate(context.Background(), map[string][]string{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("loom:wrong"))}}); err == nil {
		t.Fatal("wrong credentials unexpectedly authenticated")
	}
}

func TestCalyprAuthenticatorRequiresBearer(t *testing.T) {
	if _, err := (CalyprAuthenticator{}).Authenticate(context.Background(), nil); err == nil {
		t.Fatal("missing authorization unexpectedly authenticated")
	}
	principal, err := (CalyprAuthenticator{}).Authenticate(context.Background(), map[string][]string{"Authorization": {"Bearer abc.def.ghi"}})
	if err != nil {
		t.Fatalf("Bearer Authenticate() error = %v", err)
	}
	if principal.AuthorizationHeader != "Bearer abc.def.ghi" {
		t.Fatalf("authorization header = %q", principal.AuthorizationHeader)
	}
}

func TestFenceUserAccessClientCachesParsedSnapshot(t *testing.T) {
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/fence/base/user" {
			t.Errorf("Fence path = %q", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("authorization header was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authz":{"/programs/example/projects/demo":[{"method":"read","service":"*"}]}}`))
	}))
	trustedIssuer := serverURL + "/fence/base"
	httpClient := &http.Client{Timeout: 2 * time.Second}
	client, err := NewFenceUserAccessClientWithTrustedIssuer(httpClient, trustedIssuer, time.Minute)
	if err != nil {
		t.Fatalf("NewFenceUserAccessClientWithTrustedIssuer() error = %v", err)
	}
	if client.client.Timeout != httpClient.Timeout {
		t.Fatalf("client timeout = %s, want configured timeout %s", client.client.Timeout, httpClient.Timeout)
	}
	token := bearerTokenWithIssuer(trustedIssuer)
	for i := 0; i < 2; i++ {
		resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
		if err != nil || len(resources) != 1 {
			t.Fatalf("GetAllowedResources() = %#v, %v", resources, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("Fence calls = %d, want one cached lookup", calls.Load())
	}
}

func TestFenceUserAccessClientRejectsMismatchedIssuerBeforeRequest(t *testing.T) {
	for _, issuer := range []string{"https://attacker.example", "https://fence.example/"} {
		t.Run(issuer, func(t *testing.T) {
			var calls atomic.Int32
			client, err := NewFenceUserAccessClientWithTrustedIssuer(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("unexpected request")
			})}, "https://fence.example", time.Minute)
			if err != nil {
				t.Fatalf("NewFenceUserAccessClientWithTrustedIssuer() error = %v", err)
			}
			_, err = client.GetAllowedResources(context.Background(), bearerTokenWithIssuer(issuer), "read", "*")
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("GetAllowedResources() error = %v, want ErrUnauthenticated", err)
			}
			if calls.Load() != 0 {
				t.Fatalf("transport calls = %d, want zero before issuer match", calls.Load())
			}
		})
	}
}

func TestFenceUserAccessClientDoesNotForwardBearerAcrossRedirect(t *testing.T) {
	var redirectHits atomic.Int32
	redirectTargetURL := startIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("redirect target received Authorization %q", authorization)
		}
		_, _ = w.Write([]byte(`{"authz":{"/programs/example/projects/demo":[{"method":"read","service":"*"}]}}`))
	}))

	var originHits atomic.Int32
	originURL := startIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		if authorization := r.Header.Get("Authorization"); !strings.HasPrefix(authorization, "Bearer ") {
			t.Errorf("trusted issuer Authorization = %q", authorization)
		}
		http.Redirect(w, r, redirectTargetURL+"/user", http.StatusFound)
	}))

	client, err := NewFenceUserAccessClientWithTrustedIssuer(&http.Client{}, originURL, time.Minute)
	if err != nil {
		t.Fatalf("NewFenceUserAccessClientWithTrustedIssuer() error = %v", err)
	}
	resources, err := client.GetAllowedResources(context.Background(), bearerTokenWithIssuer(originURL), "read", "*")
	if !errors.Is(err, ErrAuthorizationBackendUnavailable) {
		t.Fatalf("GetAllowedResources() error = %v, want ErrAuthorizationBackendUnavailable", err)
	}
	if len(resources) != 0 {
		t.Fatalf("resources = %#v, want no grants from redirect response", resources)
	}
	if originHits.Load() != 1 || redirectHits.Load() != 0 {
		t.Fatalf("origin hits = %d, redirect target hits = %d; want 1 and 0", originHits.Load(), redirectHits.Load())
	}
}

func TestNewFenceUserAccessClientRequiresValidTrustedIssuer(t *testing.T) {
	for _, issuer := range []string{
		"",
		"not a URL",
		"//fence.example",
		"ftp://fence.example",
		"http:///user",
		"https://user:password@fence.example",
		"https://fence.example?next=/user",
		"https://fence.example#user",
		"https://fence.example:70000",
	} {
		t.Run(issuer, func(t *testing.T) {
			if _, err := NewFenceUserAccessClientWithTrustedIssuer(nil, issuer, time.Minute); err == nil {
				t.Fatalf("NewFenceUserAccessClientWithTrustedIssuer(%q) unexpectedly succeeded", issuer)
			}
		})
	}
}

func bearerTokenWithIssuer(issuer string) string {
	encodedIssuer := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	return "Bearer e30." + encodedIssuer + ".signature"
}

// bearerTokenWithIssuerAndNonce mints a distinct bearer token for the same
// issuer so each call hashes to its own cache key, the way a renewing client
// does. The nonce is echoed back as the granted resource by
// fenceGrantHandler so a test can tell which snapshot a lookup served.
func bearerTokenWithIssuerAndNonce(issuer, nonce string) string {
	encodedIssuer := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	return "Bearer e30." + encodedIssuer + ".sig-" + nonce
}

func fenceGrantHandler(calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		token := cleanAccessToken(r.Header.Get("Authorization"))
		parts := strings.Split(token, ".")
		nonce := parts[len(parts)-1]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"authz":{"/programs/example/projects/%s":[{"method":"read","service":"*"}]}}`, nonce)
	}
}

// fenceGrantedResource is the resource fenceGrantHandler advertises for a
// token minted with the given nonce.
func fenceGrantedResource(nonce string) string {
	return "/programs/example/projects/sig-" + nonce
}

type fenceTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fenceTestClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fenceTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func fenceCacheLen(client *FenceUserAccessClient) int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.cache)
}

func fenceCachedExpiry(client *FenceUserAccessClient, authorizationHeader string) (time.Time, bool) {
	sum := sha256.Sum256([]byte(authorizationHeader))
	client.mu.Lock()
	defer client.mu.Unlock()
	entry, ok := client.cache[fmt.Sprintf("%x", sum[:])]
	return entry.expiresAt, ok
}

func TestFenceUserAccessClientEvictsExpiredSnapshotAndRefetches(t *testing.T) {
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second}, serverURL, 45*time.Second, 8, clock.get)
	token := bearerTokenWithIssuerAndNonce(serverURL, "first")

	for i := 0; i < 2; i++ {
		resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
		if err != nil || len(resources) != 1 || resources[0] != fenceGrantedResource("first") {
			t.Fatalf("GetAllowedResources() = %#v, %v", resources, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("Fence calls = %d, want one cached lookup", calls.Load())
	}

	clock.advance(46 * time.Second)
	resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
	if err != nil || len(resources) != 1 || resources[0] != fenceGrantedResource("first") {
		t.Fatalf("GetAllowedResources() after expiry = %#v, %v", resources, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("Fence calls = %d, want a fresh fetch after expiry", calls.Load())
	}
	// The expired snapshot is replaced, not accumulated.
	if got := fenceCacheLen(client); got != 1 {
		t.Fatalf("retained entries = %d, want 1 after refetch", got)
	}
	if expiry, ok := fenceCachedExpiry(client, token); !ok || !expiry.After(clock.get()) {
		t.Fatalf("refetched entry expiry = %s (present %v), want a future expiry", expiry, ok)
	}
}

func TestFenceUserAccessClientCapsRetainedSnapshotsAcrossTokenChurn(t *testing.T) {
	const bound = 4
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second}, serverURL, time.Minute, bound, clock.get)

	const tokens = 60
	newest := ""
	for i := 0; i < tokens; i++ {
		nonce := fmt.Sprintf("t%03d", i)
		token := bearerTokenWithIssuerAndNonce(serverURL, nonce)
		newest = token
		resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
		if err != nil || len(resources) != 1 || resources[0] != fenceGrantedResource(nonce) {
			t.Fatalf("GetAllowedResources(%s) = %#v, %v", nonce, resources, err)
		}
		if got := fenceCacheLen(client); got > bound {
			t.Fatalf("retained entries = %d after %d distinct tokens, want <= %d", got, i+1, bound)
		}
	}
	if got := fenceCacheLen(client); got != bound {
		t.Fatalf("retained entries = %d, want the configured bound %d", got, bound)
	}

	// The most recent token is still cached; the churned-away oldest is not.
	before := calls.Load()
	if _, err := client.GetAllowedResources(context.Background(), newest, "read", "*"); err != nil {
		t.Fatalf("GetAllowedResources(newest) error = %v", err)
	}
	if calls.Load() != before {
		t.Fatalf("Fence calls = %d, want the newest token served from cache", calls.Load())
	}
	oldest := bearerTokenWithIssuerAndNonce(serverURL, "t000")
	if _, err := client.GetAllowedResources(context.Background(), oldest, "read", "*"); err != nil {
		t.Fatalf("GetAllowedResources(oldest) error = %v", err)
	}
	if calls.Load() != before+1 {
		t.Fatalf("Fence calls = %d, want the evicted token refetched", calls.Load())
	}
}

func TestFenceUserAccessClientDefaultBoundCapsTokenChurn(t *testing.T) {
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	client, err := NewFenceUserAccessClientWithTrustedIssuer(&http.Client{Timeout: 2 * time.Second}, serverURL, time.Minute)
	if err != nil {
		t.Fatalf("NewFenceUserAccessClientWithTrustedIssuer() error = %v", err)
	}
	for i := 0; i < defaultFenceAccessCacheEntries+64; i++ {
		token := bearerTokenWithIssuerAndNonce(serverURL, fmt.Sprintf("d%05d", i))
		if _, err := client.GetAllowedResources(context.Background(), token, "read", "*"); err != nil {
			t.Fatalf("GetAllowedResources() error = %v", err)
		}
		if got := fenceCacheLen(client); got > defaultFenceAccessCacheEntries {
			t.Fatalf("retained entries = %d after %d distinct tokens, want <= default bound %d", got, i+1, defaultFenceAccessCacheEntries)
		}
	}
	if got := fenceCacheLen(client); got != defaultFenceAccessCacheEntries {
		t.Fatalf("retained entries = %d, want the default bound %d", got, defaultFenceAccessCacheEntries)
	}
	if int(calls.Load()) != defaultFenceAccessCacheEntries+64 {
		t.Fatalf("Fence calls = %d, want one fetch per distinct token", calls.Load())
	}
}

func TestFenceUserAccessClientDropsExpiredEntriesOnInsert(t *testing.T) {
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second}, serverURL, time.Minute, 8, clock.get)

	for _, nonce := range []string{"a", "b", "c"} {
		if _, err := client.GetAllowedResources(context.Background(), bearerTokenWithIssuerAndNonce(serverURL, nonce), "read", "*"); err != nil {
			t.Fatalf("GetAllowedResources(%s) error = %v", nonce, err)
		}
	}
	if got := fenceCacheLen(client); got != 3 {
		t.Fatalf("retained entries = %d, want 3 before expiry", got)
	}

	// Churned-away tokens are never looked up again, so only the insert-time
	// sweep can release them; the cap alone would leave them retained.
	clock.advance(2 * time.Minute)
	if _, err := client.GetAllowedResources(context.Background(), bearerTokenWithIssuerAndNonce(serverURL, "d"), "read", "*"); err != nil {
		t.Fatalf("GetAllowedResources(d) error = %v", err)
	}
	if got := fenceCacheLen(client); got != 1 {
		t.Fatalf("retained entries = %d, want only the fresh snapshot after the sweep", got)
	}
}

func TestFenceUserAccessClientEvictionDoesNotMixGrantsAcrossTokens(t *testing.T) {
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second}, serverURL, time.Minute, 2, clock.get)

	tokenA := bearerTokenWithIssuerAndNonce(serverURL, "a")
	tokenB := bearerTokenWithIssuerAndNonce(serverURL, "b")
	tokenC := bearerTokenWithIssuerAndNonce(serverURL, "c")
	for _, token := range []string{tokenA, tokenB, tokenC} {
		if _, err := client.GetAllowedResources(context.Background(), token, "read", "*"); err != nil {
			t.Fatalf("GetAllowedResources() error = %v", err)
		}
	}
	if got := fenceCacheLen(client); got != 2 {
		t.Fatalf("retained entries = %d, want the bound 2", got)
	}
	// Evicting A must leave B's own grants intact.
	before := calls.Load()
	snapshotB, err := client.getResourceAccess(context.Background(), tokenB)
	if err != nil {
		t.Fatalf("getResourceAccess(B) error = %v", err)
	}
	if calls.Load() != before {
		t.Fatalf("Fence calls = %d, want B served from cache after evicting A", calls.Load())
	}
	if _, ok := snapshotB[fenceGrantedResource("b")]; !ok {
		t.Fatalf("B snapshot = %#v, want only B's grants", snapshotB)
	}
	if _, ok := snapshotB[fenceGrantedResource("a")]; ok {
		t.Fatalf("B snapshot = %#v, want no grants from the evicted token", snapshotB)
	}
	if _, ok := snapshotB[fenceGrantedResource("c")]; ok {
		t.Fatalf("B snapshot = %#v, want no grants from another token", snapshotB)
	}

	// Reads hand out independent copies: mutating a returned snapshot must not
	// reach the cached one, for the same token or any other.
	snapshotB[fenceGrantedResource("b")][0].Service = "tampered"
	delete(snapshotB, fenceGrantedResource("b"))
	snapshotB["/programs/example/projects/injected"] = []resourceAccessRecord{{Method: "write", Service: "*"}}
	reread, err := client.getResourceAccess(context.Background(), tokenB)
	if err != nil {
		t.Fatalf("getResourceAccess(B) re-read error = %v", err)
	}
	records, ok := reread[fenceGrantedResource("b")]
	if !ok || len(records) != 1 || records[0].Method != "read" || records[0].Service != "*" {
		t.Fatalf("re-read B snapshot = %#v, want the untouched cached grants", reread)
	}
	if _, ok := reread["/programs/example/projects/injected"]; ok {
		t.Fatalf("re-read B snapshot = %#v, want no injected resource", reread)
	}
}

func TestFenceUserAccessClientDropsExpiredEntryWhenRefetchFails(t *testing.T) {
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	backend := &fenceTransport{}
	client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second, Transport: backend}, serverURL, time.Minute, 8, clock.get)
	token := bearerTokenWithIssuerAndNonce(serverURL, "stale")

	if _, err := client.GetAllowedResources(context.Background(), token, "read", "*"); err != nil {
		t.Fatalf("GetAllowedResources() error = %v", err)
	}
	if _, ok := fenceCachedExpiry(client, token); !ok {
		t.Fatal("snapshot absent before expiry")
	}

	// The expired snapshot can never be served again, so the lookup that
	// discovers it must release the slot even when the refresh fails.
	clock.advance(2 * time.Minute)
	backend.down.Store(true)
	if _, err := client.GetAllowedResources(context.Background(), token, "read", "*"); err == nil {
		t.Fatal("GetAllowedResources() unexpectedly succeeded with the backend down")
	}
	if got := fenceCacheLen(client); got != 0 {
		t.Fatalf("retained entries = %d, want the expired snapshot released", got)
	}

	// Recovery refetches and repopulates the released slot.
	backend.down.Store(false)
	resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
	if err != nil || len(resources) != 1 || resources[0] != fenceGrantedResource("stale") {
		t.Fatalf("GetAllowedResources() after recovery = %#v, %v", resources, err)
	}
	if got := fenceCacheLen(client); got != 1 {
		t.Fatalf("retained entries = %d, want 1 after recovery", got)
	}
}

// fenceTransport fronts the test Fence server so a refresh can be failed on
// demand without tearing the server down.
type fenceTransport struct {
	down atomic.Bool
}

func (f *fenceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.down.Load() {
		return nil, errors.New("fence backend down")
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestFenceUserAccessClientDoesNotCacheFailedSnapshots(t *testing.T) {
	for _, statusCode := range []int{http.StatusInternalServerError, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			var calls atomic.Int32
			serverURL := startIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(statusCode)
			}))
			clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
			client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second}, serverURL, time.Minute, 4, clock.get)
			token := bearerTokenWithIssuerAndNonce(serverURL, "failed")
			for i := 0; i < 2; i++ {
				if _, err := client.GetAllowedResources(context.Background(), token, "read", "*"); err == nil {
					t.Fatal("GetAllowedResources() unexpectedly succeeded")
				}
			}
			if got := fenceCacheLen(client); got != 0 {
				t.Fatalf("retained entries = %d, want failed lookups uncached", got)
			}
			if calls.Load() != 2 {
				t.Fatalf("Fence calls = %d, want a request per failed lookup", calls.Load())
			}
		})
	}
}

func TestFenceUserAccessClientConcurrentLookupsStayBounded(t *testing.T) {
	const bound = 8
	var calls atomic.Int32
	serverURL := startIPv4TestServer(t, fenceGrantHandler(&calls))
	clock := &fenceTestClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	client := newFenceUserAccessClientWithCachePolicy(&http.Client{Timeout: 2 * time.Second}, serverURL, time.Minute, bound, clock.get)

	const workers = 24
	var failures atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			nonce := fmt.Sprintf("c%02d", i%4)
			token := bearerTokenWithIssuerAndNonce(serverURL, nonce)
			for j := 0; j < 40; j++ {
				resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
				if err != nil || len(resources) != 1 || resources[0] != fenceGrantedResource(nonce) {
					failures.Add(1)
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if got := failures.Load(); got != 0 {
		t.Fatalf("%d concurrent lookups returned wrong grants or errors", got)
	}
	if got := fenceCacheLen(client); got > bound {
		t.Fatalf("retained entries = %d, want <= %d", got, bound)
	}
}

func startIPv4TestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on IPv4 loopback: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return "http://" + listener.Addr().String()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
