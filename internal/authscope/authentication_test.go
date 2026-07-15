package authscope

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/user/user" {
			return nil, fmt.Errorf("Fence path = %q", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			return nil, fmt.Errorf("authorization header was not forwarded")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"authz":{"/programs/example/projects/demo":[{"method":"read","service":"*"}]}}`)), Header: make(http.Header)}, nil
	})
	iss := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://fence.example/user"}`))
	token := "Bearer e30." + iss + ".sig"
	client := NewFenceUserAccessClientWithTTL(&http.Client{Transport: transport}, time.Minute)
	for i := 0; i < 2; i++ {
		resources, err := client.GetAllowedResources(context.Background(), token, "read", "*")
		if err != nil || len(resources) != 1 {
			t.Fatalf("GetAllowedResources() = %#v, %v", resources, err)
		}
	}
	if calls != 1 {
		t.Fatalf("Fence calls = %d, want one cached lookup", calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
