package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

func TestLivenessDoesNotCheckDependencies(t *testing.T) {
	checks := 0
	server, err := NewHTTPServer(HTTPConfig{
		Authorizer:           authscope.AllowAllAuthorizer{},
		CoreReadyCheck:       func(context.Context) error { checks++; return errors.New("unavailable") },
		ClickHouseEnabled:    true,
		ClickHouseReadyCheck: func(context.Context) error { checks++; return errors.New("unavailable") },
	})
	if err != nil {
		t.Fatal(err)
	}
	body, status := server.Liveness(context.Background())
	if status != http.StatusOK || checks != 0 || body["status"] != "live" {
		t.Fatalf("liveness status=%d checks=%d body=%#v", status, checks, body)
	}
}

func TestReadinessRequiresCoreAndClickHouse(t *testing.T) {
	tests := []struct {
		name                                 string
		coreErr, clickHouseErr               error
		clickHouseEnabled                    bool
		wantStatus                           int
		wantOverall, wantCore, wantDataframe string
	}{
		{name: "ready", clickHouseEnabled: true, wantStatus: http.StatusOK, wantOverall: "ready", wantCore: "ready", wantDataframe: "ready"},
		{name: "core unavailable", coreErr: errors.New("core unavailable"), clickHouseEnabled: true, wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "not_ready"},
		{name: "ClickHouse unavailable", clickHouseErr: errors.New("ClickHouse unavailable"), clickHouseEnabled: true, wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "ready", wantDataframe: "backend_unavailable"},
		{name: "ClickHouse disabled", wantStatus: http.StatusOK, wantOverall: "ready", wantCore: "ready", wantDataframe: "disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewHTTPServer(HTTPConfig{
				Authorizer:           authscope.AllowAllAuthorizer{},
				CoreReadyCheck:       func(context.Context) error { return tt.coreErr },
				ClickHouseEnabled:    tt.clickHouseEnabled,
				ClickHouseReadyCheck: func(context.Context) error { return tt.clickHouseErr },
			})
			if err != nil {
				t.Fatal(err)
			}
			body, status := server.Readiness(context.Background())
			dataframe, _ := body["dataframe"].(string)
			if status != tt.wantStatus || body["status"] != tt.wantOverall || body["core"] != tt.wantCore || dataframe != tt.wantDataframe {
				t.Fatalf("status=%d body=%#v", status, body)
			}
		})
	}
}

func TestHealthRetainsDegradedClickHouseCompatibility(t *testing.T) {
	server, err := NewHTTPServer(HTTPConfig{
		Authorizer:           authscope.AllowAllAuthorizer{},
		ClickHouseEnabled:    true,
		ClickHouseReadyCheck: func(context.Context) error { return errors.New("ClickHouse unavailable") },
	})
	if err != nil {
		t.Fatal(err)
	}
	body, status := server.Health(context.Background())
	if status != http.StatusOK || body["status"] != "degraded" || body["dataframe"] != "backend_unavailable" {
		t.Fatalf("status=%d body=%#v", status, body)
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

func TestRecoveryMiddlewareLogsStackAndPreservesInternalErrorResponse(t *testing.T) {
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
	server.App().Post("/panic", func(fiber.Ctx) error {
		panic("fixture panic")
	})

	request := httptest.NewRequest(http.MethodPost, "/panic", strings.NewReader("request-body-secret"))
	request.Header.Set("X-Request-ID", "panic-diagnostic")
	response, err := server.App().Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.StatusCode)
	}
	if response.Header.Get("X-Request-ID") != "panic-diagnostic" {
		t.Fatalf("X-Request-ID = %q, want panic-diagnostic", response.Header.Get("X-Request-ID"))
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(responseBody, &body); err != nil {
		t.Fatalf("decode response body %q: %v", responseBody, err)
	}
	if body.Error.Code != "INTERNAL_ERROR" || body.Error.RequestID != "panic-diagnostic" {
		t.Fatalf("error response = %#v, want INTERNAL_ERROR for panic-diagnostic", body.Error)
	}

	logText := logs.String()
	for _, want := range []string{
		"panic recovered",
		"request_id=panic-diagnostic",
		`panic="fixture panic"`,
		"stack=",
		"TestRecoveryMiddlewareLogsStackAndPreservesInternalErrorResponse",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("logs missing %q:\n%s", want, logText)
		}
	}
	if strings.Contains(logText, "request-body-secret") {
		t.Fatalf("logs unexpectedly contain request body:\n%s", logText)
	}
}
