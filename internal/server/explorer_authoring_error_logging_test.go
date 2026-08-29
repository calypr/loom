package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestAuthoringHTTPErrorLogsGenericCauseWithoutExposingIt(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	app := fiber.New()
	app.Get("/api/v1/projects/:project/explorers/:explorerId", func(c fiber.Ctx) error {
		c.Locals("request_id", "builder-log-test")
		cause := fmt.Errorf("compile receipt: %w", fmt.Errorf("semantic lowering exploded"))
		return authoringHTTPError(c, cause)
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/projects/HTAN_INT%252FBForePC/explorers/default", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "INTERNAL_ERROR" || body["message"] != "internal server error" {
		t.Fatalf("response = %#v", body)
	}
	if encoded, err := json.Marshal(body); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(encoded), "semantic lowering exploded") {
		t.Fatalf("response exposed internal cause: %s", encoded)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v\n%s", err, logs.String())
	}
	for key, want := range map[string]any{
		"msg":         "Explorer authoring request failed",
		"request_id":  "builder-log-test",
		"method":      http.MethodGet,
		"path":        "/api/v1/projects/HTAN_INT%252FBForePC/explorers/default",
		"status":      float64(http.StatusInternalServerError),
		"project":     "HTAN_INT/BForePC",
		"explorer_id": "default",
		"stage":       "internal",
		"code":        "INTERNAL_ERROR",
		"cause":       "compile receipt: semantic lowering exploded",
		"cause_type":  "*fmt.wrapError",
	} {
		if got := entry[key]; got != want {
			t.Errorf("log[%q] = %#v, want %#v; log=%s", key, got, want, logs.String())
		}
	}
}
