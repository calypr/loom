package server

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionCapabilitiesHTTPPreservesOptionalNodeOccurrences(t *testing.T) {
	for _, test := range []struct {
		name        string
		occurrences map[string]string
	}{
		{name: "catalog nodes mapped to authored occurrences", occurrences: map[string]string{"n_patient": "root", "n_specimen": "specimen_subject"}},
		{name: "optional mapping omitted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			converted, err := directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](lifecycle.ConstructionCapabilitiesResponse{NodeOccurrences: test.occurrences})
			if err != nil {
				t.Fatal(err)
			}
			response := loomapi.GetExplorerConstructionCapabilities200JSONResponse(converted)
			app := fiber.New()
			app.Get("/", func(ctx fiber.Ctx) error { return response.VisitGetExplorerConstructionCapabilitiesResponse(ctx) })
			wire, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer wire.Body.Close()
			if wire.StatusCode != 200 {
				t.Fatalf("status=%d", wire.StatusCode)
			}
			body, err := io.ReadAll(wire.Body)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			raw, present := decoded["nodeOccurrences"]
			if test.occurrences == nil {
				if present {
					t.Fatalf("optional mapping emitted: %s", body)
				}
				return
			}
			if !present {
				t.Fatalf("typed HTTP conversion dropped populated nodeOccurrences: %s", body)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.occurrences) {
				t.Fatalf("nodeOccurrences=%v want%v", got, test.occurrences)
			}
		})
	}
}
