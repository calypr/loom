package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	server, err := NewHTTPServer(HTTPConfig{Authenticator: authscope.BasicAuthenticator{Username: "u", Password: "p"}, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/health", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

func TestHealthRoutesDoNotRequireAuthentication(t *testing.T) {
	server, err := NewHTTPServer(HTTPConfig{
		Authenticator: authscope.BasicAuthenticator{Username: "u", Password: "p"},
		Authorizer:    authscope.AllowAllAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health", "/livez", "/readyz"} {
		resp, testErr := server.App().Test(httptest.NewRequest(http.MethodGet, path, nil))
		if testErr != nil {
			t.Fatalf("GET %s: %v", path, testErr)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}
	}
}

func TestLivenessDoesNotCheckDependencies(t *testing.T) {
	checks := 0
	server, err := NewHTTPServer(HTTPConfig{
		Authorizer: authscope.AllowAllAuthorizer{},
		CoreReadyCheck: func(_ context.Context) error {
			checks++
			return errors.New("unavailable")
		},
		ClickHouseEnabled: true,
		ClickHouseReadyCheck: func(_ context.Context) error {
			checks++
			return errors.New("unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/livez", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || checks != 0 {
		t.Fatalf("liveness status = %d, dependency checks = %d", resp.StatusCode, checks)
	}
}

func TestReadinessRequiresCoreAndClickHouse(t *testing.T) {
	tests := []struct {
		name              string
		coreErr           error
		clickHouseErr     error
		clickHouseEnabled bool
		wantStatus        int
		wantOverall       string
		wantCore          string
		wantDataframe     string
	}{
		{name: "ready", clickHouseEnabled: true, wantStatus: http.StatusOK, wantOverall: "ready", wantCore: "ready", wantDataframe: "ready"},
		{name: "core unavailable", coreErr: errors.New("core unavailable"), clickHouseEnabled: true, wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "not_ready"},
		{name: "ClickHouse unavailable", clickHouseErr: errors.New("ClickHouse unavailable"), clickHouseEnabled: true, wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "ready", wantDataframe: "backend_unavailable"},
		{name: "ClickHouse disabled", wantStatus: http.StatusOK, wantOverall: "ready", wantCore: "ready", wantDataframe: "disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewHTTPServer(HTTPConfig{
				Authorizer: authscope.AllowAllAuthorizer{},
				CoreReadyCheck: func(_ context.Context) error {
					return tt.coreErr
				},
				ClickHouseEnabled: tt.clickHouseEnabled,
				ClickHouseReadyCheck: func(_ context.Context) error {
					return tt.clickHouseErr
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.wantStatus || body["status"] != tt.wantOverall || body["core"] != tt.wantCore || body["dataframe"] != tt.wantDataframe {
				t.Fatalf("status = %d, body = %#v", resp.StatusCode, body)
			}
		})
	}
}

func TestHealthRetainsDegradedClickHouseCompatibility(t *testing.T) {
	server, err := NewHTTPServer(HTTPConfig{
		Authorizer:        authscope.AllowAllAuthorizer{},
		ClickHouseEnabled: true,
		ClickHouseReadyCheck: func(_ context.Context) error {
			return errors.New("ClickHouse unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/health", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || body["status"] != "degraded" || body["dataframe"] != "backend_unavailable" {
		t.Fatalf("status = %d, body = %#v", resp.StatusCode, body)
	}
}

func TestNewHTTPServerRequiresAuthorizer(t *testing.T) {
	if _, err := NewHTTPServer(HTTPConfig{}); err == nil {
		t.Fatal("expected missing authorizer error")
	}
}

func TestLoggingMiddlewareEmitsStructuredResponseDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server, err := NewHTTPServer(HTTPConfig{
		Authenticator: authscope.StaticAuthenticator{},
		Authorizer:    authscope.AllowAllAuthorizer{},
		Logger:        logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.App().Put("/authoring-failure", func(c fiber.Ctx) error {
		return c.Status(http.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": fiber.Map{
				"code":    "UNSUPPORTED_ROW_GRAIN",
				"message": "the selected base resource has no supported row grain",
			},
			"diagnostics": []fiber.Map{{
				"stage":        "lower",
				"code":         "UNSUPPORTED_ROW_GRAIN",
				"jsonPath":     "$.document.baseNodeId",
				"resourceType": "patient",
			}},
		})
	})

	request := httptest.NewRequest(http.MethodPut, "/authoring-failure?auth_resource_path=%2Fprograms%2FHTAN_INT%2Fprojects%2FBForePC", nil)
	request.Header.Set("X-Request-ID", "request-diagnostic")
	response, err := server.App().Test(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}

	logText := logs.String()
	for _, want := range []string{
		"http request failed",
		"request_id=request-diagnostic",
		"error_code=UNSUPPORTED_ROW_GRAIN",
		"error_message=",
		"supported row grain",
		"error_diagnostics=",
		"resourceType",
		"patient",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("logs missing %q:\n%s", want, logText)
		}
	}
}
