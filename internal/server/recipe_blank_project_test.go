package server

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// blankProjectRegistryFailures prove the engine-level project guard fires
// before any registry lookup, mirroring the deployed 500 ordering where a
// whitespace-only project never reached LoadRecipe.
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

func assertTypedProjectRequired(t *testing.T, err error, site string) dataframeerrors.UserError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: whitespace-only project accepted; want typed PROJECT_REQUIRED", site)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("%s: error escaped as raw (INTERNAL fault would be synthesized): %v", site, err)
	}
	if userErr.Code() != string(dataframeerrors.CodeProjectRequired) {
		t.Fatalf("%s: code=%s want %s (err=%v)", site, userErr.Code(), dataframeerrors.CodeProjectRequired, err)
	}
	if userErr.Retryable() {
		t.Fatalf("%s: PROJECT_REQUIRED must stay non-retryable", site)
	}
	if message := dataframeerrors.PublicMessage(err); message != dataframeerrors.PublicMessage(dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")) {
		t.Fatalf("%s: message=%q want default publish text", site, message)
	}
	return userErr
}

// (a) Throwing-site reproduction: the deployed 500 escape path
// Control.Run/Preview/Resolve -> Engine.Resolve raw guard error.
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
	} {
		t.Run(leg.name, func(t *testing.T) {
			err := leg.call(context.Background(), "patients", recipe.RuntimeBindings{Project: "  ", DatasetGeneration: "generation-a"})
			assertTypedProjectRequired(t, err, "control."+leg.name)
		})
	}
	if registry.loads != 0 {
		t.Fatalf("blank project guard must reject before registry lookup, got %d loads", registry.loads)
	}
}

// (b) Authorizer parity: whitespace-only == empty, both non-retryable typed.
func TestRecipeAuthorizationTreatsWhitespaceProjectAsEmpty(t *testing.T) {
	authorizer := recipeAuthorization{}
	for name, authorize := range map[string]func(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error){
		"read":  authorizer.AuthorizeRead,
		"write": authorizer.AuthorizeWrite,
	} {
		t.Run(name, func(t *testing.T) {
			_, emptyErr := authorize(context.Background(), recipe.RuntimeBindings{Project: ""})
			emptyErrUser := assertTypedProjectRequired(t, emptyErr, name+" empty")
			_, blankErr := authorize(context.Background(), recipe.RuntimeBindings{Project: "  "})
			blankErrUser := assertTypedProjectRequired(t, blankErr, name+" whitespace")
			if emptyErrUser.Code() != blankErrUser.Code() || emptyErrUser.Retryable() != blankErrUser.Retryable() {
				t.Fatalf("whitespace leg diverges from empty leg: %s/%v vs %s/%v", blankErrUser.Code(), blankErrUser.Retryable(), emptyErrUser.Code(), emptyErrUser.Retryable())
			}
		})
	}
}

type blankProjectFamilyControl struct{ calls int }

func (c *blankProjectFamilyControl) Validate(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Validation, error) {
	c.calls++
	return dataframeexecution.Validation{}, errors.New("control validate consulted")
}

func (c *blankProjectFamilyControl) Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	c.calls++
	return semantic.RecipePlanExplanation{}, errors.New("control explain consulted")
}

func (c *blankProjectFamilyControl) ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (dataframeexecution.PhysicalExplanation, error) {
	c.calls++
	return dataframeexecution.PhysicalExplanation{}, errors.New("control explain physical consulted")
}

func (c *blankProjectFamilyControl) rawFault() error {
	c.calls++
	return errors.New("arango: invalid collection name '  _fhir_document'")
}

func (c *blankProjectFamilyControl) Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	return semantic.ResolvedRecipePlan{}, c.rawFault()
}

func (c *blankProjectFamilyControl) Preview(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Preview, error) {
	return dataframeexecution.Preview{}, c.rawFault()
}

func (c *blankProjectFamilyControl) Run(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Preview, error) {
	return dataframeexecution.Preview{}, c.rawFault()
}

// (b) Family legs: run/preview/preflight must reject whitespace-only with the
// same typed PROJECT_REQUIRED the family already publishes for empty, before
// the control plane is consulted.
func TestRecipeFamilyLegsRejectWhitespaceProjectBeforeControl(t *testing.T) {
	legs := []struct {
		name string
		call func(graphresolver.Resolver, string) error
	}{
		{"runDataframeRecipe", func(r graphresolver.Resolver, project string) error {
			_, err := r.Mutation().RunDataframeRecipe(context.Background(), model.RunDataframeRecipeInput{
				Name:     "patients",
				Bindings: &model.DataframeRecipeBindingsInput{Project: project},
			})
			return err
		}},
		{"previewDataframeRecipe", func(r graphresolver.Resolver, project string) error {
			_, err := r.Mutation().PreviewDataframeRecipe(context.Background(), model.PreviewDataframeRecipeInput{
				Name:     "patients",
				Bindings: &model.DataframeRecipeBindingsInput{Project: project},
			})
			return err
		}},
		{"preflightDataframeRecipe", func(r graphresolver.Resolver, project string) error {
			_, err := r.Query().PreflightDataframeRecipe(context.Background(), model.PreflightDataframeRecipeInput{
				Name:     "patients",
				Bindings: &model.DataframeRecipeBindingsInput{Project: project},
			})
			return err
		}},
	}
	for _, leg := range legs {
		t.Run(leg.name, func(t *testing.T) {
			control := &blankProjectFamilyControl{}
			resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
				RecipeControl:    control,
				RecipeAuthorizer: recipeAuthorization{},
			})
			emptyErr := leg.call(*resolver, "")
			emptyUser := assertTypedProjectRequired(t, emptyErr, leg.name+" empty")
			blankErr := leg.call(*resolver, "  ")
			blankUser := assertTypedProjectRequired(t, blankErr, leg.name+" whitespace")
			if blankUser.Code() != emptyUser.Code() || blankUser.Retryable() != emptyUser.Retryable() ||
				dataframeerrors.PublicMessage(blankErr) != dataframeerrors.PublicMessage(emptyErr) {
				t.Fatalf("whitespace leg diverges from empty leg: %s/%v/%q vs %s/%v/%q",
					blankUser.Code(), blankUser.Retryable(), dataframeerrors.PublicMessage(blankErr),
					emptyUser.Code(), emptyUser.Retryable(), dataframeerrors.PublicMessage(emptyErr))
			}
			if control.calls != 0 {
				t.Fatalf("family must classify whitespace project before control plane, got %d control calls", control.calls)
			}
		})
	}
}

// (c) Foreign faults still surface untouched: genuinely unclassifiable
// control errors keep falling through to INTERNAL_ERROR.
func TestRecipeControlForeignFaultStillFallsThroughToInternal(t *testing.T) {
	control := &blankProjectFamilyControl{}
	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		RecipeControl:    control,
		RecipeAuthorizer: recipeAuthorization{},
	})
	_, err := resolver.Mutation().RunDataframeRecipe(context.Background(), model.RunDataframeRecipeInput{
		Name:     "patients",
		Bindings: &model.DataframeRecipeBindingsInput{Project: "HTAN_INT/project-a"},
	})
	if err == nil {
		t.Fatal("foreign control fault accepted")
	}
	normalized := dataframeerrors.Normalize(err)
	if normalized == nil || normalized.Code() != string(dataframeerrors.CodeInternalError) {
		t.Fatalf("foreign fault must fall through to INTERNAL_ERROR, got %#v (raw=%v)", normalized, err)
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		t.Fatal("foreign fault must not be classified at the resolver; presentation normalizes it")
	}
}

// (c) Auth resolver faults keep typing BACKEND_UNAVAILABLE retryable through
// recipeAuthorizationError (unchanged semantics).
func TestRecipeAuthorizationResolverFaultStaysBackendUnavailable(t *testing.T) {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: fencedFaultClient{},
	})
	authorizer := recipeAuthorization{resolver: resolver}
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
