package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestExplorerProjectParamDecodesEncodedSlash(t *testing.T) {
	app := fiber.New()
	app.Get("/api/v1/projects/:project", func(c fiber.Ctx) error {
		return c.SendString(explorerProjectParam(c))
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/HTAN_INT%2FBForePC", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "HTAN_INT/BForePC" {
		t.Fatalf("decoded project = %q, want %q", got, "HTAN_INT/BForePC")
	}
}
