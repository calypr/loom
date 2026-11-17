package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestRecipeExecutionTwinRefusesForeignProjectPrincipal(t *testing.T) {
	foreign := publication.BundleExecution{
		ID: "execution-foreign",
		BundleIdentity: publication.BundleIdentity{
			Project: "project-b", DatasetGeneration: "generation-b", RecipeDigest: "rd", SchemaDigest: "sd",
			AuthResourcePaths: []string{"path-b"},
		},
		State: publication.BundlePublished,
	}
	routes := &HTTPRoutes{releases: recipeExecutionCatalog{execution: foreign}}

	foreignCtx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:  "operator-a",
		Projects: []string{"project-a"},
	})
	body, statusCode := routes.recipeExecution(foreignCtx, "execution-foreign")
	if statusCode != http.StatusNotFound {
		resp, _ := json.Marshal(body)
		t.Fatalf("F-B20 REGRESSION: foreign-project principal received status %d with body %s; "+
			"the REST twin disclosed cross-project execution metadata without asserting project membership", statusCode, resp)
	}
	if denial, ok := body.(map[string]any); !ok || denial["error"] != "recipe execution not found" {
		t.Fatalf("foreign-project denial = %#v, want the concealed missing-execution envelope", body)
	}

	memberCtx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:  "operator-b",
		Projects: []string{"project-a", "project-b"},
	})
	if _, statusCode := routes.recipeExecution(memberCtx, "execution-foreign"); statusCode != http.StatusOK {
		t.Fatalf("same-project member status = %d, want 200 (membership gate must not over-deny)", statusCode)
	}

	if _, statusCode := routes.recipeExecution(context.Background(), "execution-foreign"); statusCode != http.StatusOK {
		t.Fatalf("principal-less (single-operator) status = %d, want 200 (anonymous posture preserved)", statusCode)
	}
}
