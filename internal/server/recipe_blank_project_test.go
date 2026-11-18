package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"

	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
)

type blankProjectRegistryFailures struct{ loads int }

func (r *blankProjectRegistryFailures) LoadRecipe(context.Context, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, errors.New("recipe registry consulted for blank project")
}

func (r *blankProjectRegistryFailures) LoadRecipeVersion(context.Context, string, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, errors.New("recipe registry consulted for blank project")
}

func blankProjectEngine(t *testing.T) (*dataframeexecution.Engine, *blankProjectRegistryFailures) {
	t.Helper()
	registry := &blankProjectRegistryFailures{}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: registry,
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return errors.New("query executor consulted for blank project")
		},
	})
	if err != nil {
		t.Fatalf("create recipe engine: %v", err)
	}
	return engine, registry
}

func TestRecipeControlBlankProjectGuardTypesThrowsAtEscapeSite(t *testing.T) {
	engine, registry := blankProjectEngine(t)
	control := dataframeexecution.Control{Engine: engine}
	for _, leg := range []struct {
		name string
		call func(context.Context, string, recipe.RuntimeBindings) error
	}{
		{"run", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := control.Run(ctx, name, b)
			return err
		}},
		{"preview", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := control.Preview(ctx, name, b)
			return err
		}},
		{"resolve", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := control.Resolve(ctx, name, b)
			return err
		}},
		{"validate", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := control.Validate(ctx, name, b)
			return err
		}},
		{"explain", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := control.Explain(ctx, name, b)
			return err
		}},
	} {
		t.Run(leg.name, func(t *testing.T) {
			for _, project := range []string{"", "  "} {
				err := leg.call(context.Background(), "patients", recipe.RuntimeBindings{Project: project, DatasetGeneration: "generation-a"})
				recipefixtures.AssertTypedProjectRequired(t, err, "control."+leg.name)
			}
		})
	}
	if registry.loads != 0 {
		t.Fatalf("blank project guard must reject before registry lookup, got %d loads", registry.loads)
	}
}

func TestRecipeAuthorizationTreatsWhitespaceProjectAsEmpty(t *testing.T) {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ResourceAccess: fencedFaultClient{}})
	authorizer := reciperuntime.NewAuthorization(resolver)
	for name, authorize := range map[string]func(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error){
		"read":  authorizer.AuthorizeRead,
		"write": authorizer.AuthorizeWrite,
	} {
		t.Run(name, func(t *testing.T) {
			_, emptyErr := authorize(context.Background(), recipe.RuntimeBindings{Project: ""})
			emptyErrUser := recipefixtures.AssertTypedProjectRequired(t, emptyErr, name+" empty")
			_, blankErr := authorize(context.Background(), recipe.RuntimeBindings{Project: "  "})
			blankErrUser := recipefixtures.AssertTypedProjectRequired(t, blankErr, name+" whitespace")
			if emptyErrUser.Code() != blankErrUser.Code() || emptyErrUser.Retryable() != blankErrUser.Retryable() {
				t.Fatalf("whitespace leg diverges from empty leg: %s/%v vs %s/%v", blankErrUser.Code(), blankErrUser.Retryable(), emptyErrUser.Code(), emptyErrUser.Retryable())
			}
		})
	}
}

func TestRecipeControlForeignFaultStaysInternal(t *testing.T) {
	err := errors.New("arango: invalid collection name")
	status, response := httpapi.MapServiceError(err, context.Background())
	if status != http.StatusInternalServerError || response.Error.Code != string(dataframeerrors.CodeInternalError) {
		t.Fatalf("foreign control fault mapped to %d %s", status, response.Error.Code)
	}
	if strings.Contains(response.Error.Message, err.Error()) {
		t.Fatalf("foreign control fault exposed backend text: %q", response.Error.Message)
	}
}

func TestRecipeAuthorizationResolverFaultStaysBackendUnavailable(t *testing.T) {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: fencedFaultClient{},
	})
	authorizer := reciperuntime.NewAuthorization(resolver)
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
