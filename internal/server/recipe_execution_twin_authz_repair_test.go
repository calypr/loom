package server

import (
	"context"
	"encoding/json"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
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
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionCatalog{execution: foreign}})

	foreignCtx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:  "operator-a",
		Projects: []string{"project-a"},
	})
	response, err := routes.GetRecipeExecution(foreignCtx, loomapi.GetRecipeExecutionRequestObject{Id: "execution-foreign"})
	if err != nil {
		t.Fatal(err)
	}
	denial, ok := response.(loomapi.GetRecipeExecution404JSONResponse)
	if !ok {
		resp, _ := json.Marshal(response)
		t.Fatalf("F-B20 REGRESSION: foreign-project principal received %T with body %s; want concealed 404", response, resp)
	}
	resp, _ := json.Marshal(denial)
	if string(resp) != `{"error":"recipe execution not found"}` {
		t.Fatalf("foreign-project denial body = %s, want concealed missing-execution envelope", resp)
	}

	memberCtx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:  "operator-b",
		Projects: []string{"project-a", "project-b"},
	})
	if response, err := routes.GetRecipeExecution(memberCtx, loomapi.GetRecipeExecutionRequestObject{Id: "execution-foreign"}); err != nil {
		t.Fatal(err)
	} else if _, ok := response.(loomapi.GetRecipeExecution200JSONResponse); !ok {
		t.Fatalf("same-project member response = %T, want 200 (membership gate must not over-deny)", response)
	}

	if response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-foreign"}); err != nil {
		t.Fatal(err)
	} else if _, ok := response.(loomapi.GetRecipeExecution200JSONResponse); !ok {
		t.Fatalf("principal-less (single-operator) response = %T, want 200 (anonymous posture preserved)", response)
	}
}
