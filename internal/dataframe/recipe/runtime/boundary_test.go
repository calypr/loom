package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

	"github.com/calypr/loom/internal/dataframe/recipeauth"

	"github.com/calypr/loom/internal/authscope"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipeAuthorizationTreatsWhitespaceProjectAsEmpty(t *testing.T) {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ResourceAccess: fencedFaultClient{}})
	authorizer := recipeauth.NewAuthorization(resolver)
	_, emptyErr := authorizer.AuthorizeRead(context.Background(), recipe.RuntimeBindings{Project: ""})
	emptyErrUser := recipefixtures.AssertTypedProjectRequired(t, emptyErr, "read empty")
	gotBlank, blankErr := authorizer.AuthorizeRead(context.Background(), recipe.RuntimeBindings{Project: "  "})
	blankErrUser := recipefixtures.AssertTypedProjectRequired(t, blankErr, "read whitespace")
	if gotBlank.Project != "" || gotBlank.SelectionProject != "" || gotBlank.AuthScopeMode != "" {
		t.Fatalf("blank-project guard must clear bindings on rejection, got %#v", gotBlank)
	}
	if emptyErrUser.Code() != blankErrUser.Code() || emptyErrUser.Retryable() != blankErrUser.Retryable() {
		t.Fatalf("whitespace leg diverges from empty leg: %s/%v vs %s/%v", blankErrUser.Code(), blankErrUser.Retryable(), emptyErrUser.Code(), emptyErrUser.Retryable())
	}
}

func TestRecipeAuthorizationResolverFaultStaysBackendUnavailable(t *testing.T) {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: fencedFaultClient{},
	})
	authorizer := recipeauth.NewAuthorization(resolver)
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "tester", AuthorizationHeader: "Bearer token"})
	_, err := authorizer.AuthorizeRead(ctx, recipe.RuntimeBindings{Project: "HTAN_INT/project-a"})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("auth fault escaped raw: %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
		t.Fatalf("auth fault typed %s retryable=%v want BACKEND_UNAVAILABLE retryable", userErr.Code(), userErr.Retryable())
	}
}

type fencedFaultClient struct{}

func (fencedFaultClient) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("fence backend down")
}
