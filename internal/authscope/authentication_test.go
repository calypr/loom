package authscope

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
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
