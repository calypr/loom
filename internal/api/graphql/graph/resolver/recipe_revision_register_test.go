package resolver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type registerRevisionStore struct {
	registered []recipe.RecipeRevision
}

func (s *registerRevisionStore) Register(_ context.Context, project string, bundle recipe.Bundle, parent string) (recipe.RecipeRevision, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(bundle.Name) == "" {
		return recipe.RecipeRevision{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	value := recipe.RecipeRevision{Project: project, Name: bundle.Name, Parent: parent}
	s.registered = append(s.registered, value)
	return value, nil
}

func (s *registerRevisionStore) Get(context.Context, string, string, string) (recipe.RecipeRevision, error) {
	return recipe.RecipeRevision{}, recipe.ErrRecipeRevisionNotFound
}

func (s *registerRevisionStore) List(context.Context, string, string) ([]recipe.RecipeRevision, error) {
	return nil, nil
}

func TestRegisterDataframeRecipeRevisionMalformedRecipeJSONIsTyped(t *testing.T) {
	store := &registerRevisionStore{}
	resolver := NewResolver(ResolverConfig{RecipeRevisions: store})
	for _, raw := range []string{"{{{", "[]", "{}", `{"name":null,"steps":"x"}`} {
		_, err := resolver.Mutation().RegisterDataframeRecipeRevision(context.Background(), model.RegisterDataframeRecipeRevisionInput{
			ProjectID: "project-a",
			Recipe:    json.RawMessage(raw),
		})
		if err == nil {
			t.Fatalf("raw=%s expected rejection", raw)
		}
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("raw=%s expected typed user error, got untyped %v", raw, err)
		}
		if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
			t.Fatalf("raw=%s code=%s want INVALID_REQUEST", raw, userErr.Code())
		}
		if userErr.Retryable() {
			t.Fatalf("raw=%s malformed document must be non-retryable", raw)
		}
		if regexp.MustCompile(`invalid character|unexpected end of JSON|does not match\b|json: `).MatchString(userErr.Error()) {
			t.Fatalf("raw=%s leaked raw json syntax text: %q", raw, userErr.Error())
		}
	}
	if len(store.registered) != 0 {
		t.Fatalf("malformed documents reached the registry: %#v", store.registered)
	}
	_, err := resolver.Mutation().RegisterDataframeRecipeRevision(context.Background(), model.RegisterDataframeRecipeRevisionInput{
		ProjectID: "project-a",
		Recipe:    json.RawMessage(`{"name":"zzz_probe_recipe"}`),
	})
	if err != nil {
		t.Fatalf("valid document must reach registry path, got %v", err)
	}
	if len(store.registered) != 1 || store.registered[0].Name != "zzz_probe_recipe" {
		t.Fatalf("registered=%#v", store.registered)
	}
	if strings.Contains(dataframeerrors.PublicMessage(err), "invalid character") {
		t.Fatal("public message leaked syntax detail")
	}
}
