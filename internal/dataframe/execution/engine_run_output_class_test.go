package execution

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type runOutputRegistry struct{ loads int }

func (r *runOutputRegistry) LoadRecipe(context.Context, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, errors.New("registry must not be consulted")
}

func (r *runOutputRegistry) LoadRecipeVersion(context.Context, string, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, errors.New("registry must not be consulted")
}

func newRunTestFixture(t *testing.T) (*Engine, *runOutputRegistry) {
	t.Helper()
	registry := &runOutputRegistry{}
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      registry,
		QueryRows: func(_ context.Context, command command.CompiledCommand, _ int, _ RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return errors.New("query executor must not be consulted")
		},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	return engine, registry
}

func runOutputResolved(names ...string) Resolved {
	compiled := lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: "patients", RootResourceType: "Patient"}}}
	return Resolved{
		Bundle: recipe.Bundle{Name: "patients"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{Project: "HTAN_INT/project-a", OutputNames: names}},
		},
		Compiled: compiled,
	}
}

func TestEngineRunUnknownOutputIsUserFault(t *testing.T) {
	e, _ := newRunTestFixture(t)
	_, err := e.streamsWithLimit(context.Background(), runOutputResolved("does-not-exist"), 0)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("unknown run output escaped as untyped (Engine.Run -> recipeControlBackend publishes retryable BACKEND_UNAVAILABLE for the raw leaf; the receipt-validation Streams channel reaches the leaf, and Control.Run's own output-selection rejection is a separate pre-wrapper 500 channel): %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("code=%s want %s", userErr.Code(), dataframeerrors.CodeInvalidRequest)
	}
	if userErr.Retryable() {
		t.Fatal("unknown output must be non-retryable")
	}
	if got := userErr.Details()["output"]; got != "does-not-exist" {
		t.Fatalf("details=%v want echoing requested output", userErr.Details())
	}
}

func TestEngineRunBlankOutputIsUserFault(t *testing.T) {
	e, _ := newRunTestFixture(t)
	for _, name := range []string{"", "   "} {
		_, err := e.streamsWithLimit(context.Background(), runOutputResolved(name), 0)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("blank run output %q escaped as untyped: %v", name, err)
		}
		if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) || userErr.Retryable() {
			t.Fatalf("blank output %q classified %s retryable=%v", name, userErr.Code(), userErr.Retryable())
		}
	}
}

func TestEngineRunKnownOutputStillResolves(t *testing.T) {
	e, _ := newRunTestFixture(t)
	_, err := e.streamsWithLimit(context.Background(), runOutputResolved("patients"), 0)
	if err != nil && strings.Contains(err.Error(), "was not found") {
		t.Fatalf("valid output must not take the unknown-output arm: %v", err)
	}
}
