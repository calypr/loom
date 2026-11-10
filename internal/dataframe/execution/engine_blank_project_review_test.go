package execution

import (
	"context"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/projectid"
)

type reviewBlankRegistry struct{ loads int }

func (r *reviewBlankRegistry) LoadRecipe(context.Context, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, nil
}

func (r *reviewBlankRegistry) LoadRecipeVersion(context.Context, string, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, nil
}

// Review attack 1: direct engine entry with a caller-constructed blank
// bindings, bypassing any GraphQL authorizer.
func TestEngineResolveBlankProjectDirectClassification(t *testing.T) {
	registry := &reviewBlankRegistry{}
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      registry,
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Resolve(context.Background(), "patients", recipe.RuntimeBindings{Project: "  ", DatasetGeneration: "g1"})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeProjectRequired) || userErr.Retryable() {
		t.Fatalf("direct Engine.Resolve blank project: ok=%v err=%v", ok, err)
	}
	if registry.loads != 0 {
		t.Fatalf("guard ordered after registry: %d loads", registry.loads)
	}
	_, err = engine.ResolveBundle(context.Background(), recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "x", TranslationVersion: "t"}, recipe.RuntimeBindings{Project: "\t\n "})
	if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodeProjectRequired) {
		t.Fatalf("direct Engine.ResolveBundle blank project: ok=%v err=%v", ok, err)
	}
	_, err = engine.CompileResolvedBundle(context.Background(), recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "x", TranslationVersion: "t"}, recipe.RuntimeBindings{Project: "   "})
	if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodeProjectRequired) {
		t.Fatalf("direct Engine.CompileResolvedBundle blank project: ok=%v err=%v", ok, err)
	}
}

// coverAllFiveGuards exercises every blank-project guard, including the two
// unexported ones (resolveEntry, compileResolvedBundle) in-package, plus the
// ResolveVersion lane that reaches resolveEntry without a preceding guard.
func TestEngineAllFiveBlankProjectGuards(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "x", TranslationVersion: "t"}
	blank := recipe.RuntimeBindings{Project: " \t\n "}
	requireTyped := func(site string, err error) {
		t.Helper()
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeProjectRequired) || userErr.Retryable() {
			t.Fatalf("%s: ok=%v err=%v", site, ok, err)
		}
	}
	registry := &reviewBlankRegistry{}
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      registry,
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Resolve(context.Background(), "x", blank)
	requireTyped("Resolve", err)
	_, err = engine.ResolveBundle(context.Background(), bundle, blank)
	requireTyped("ResolveBundle", err)
	_, err = engine.CompileResolvedBundle(context.Background(), bundle, blank)
	requireTyped("CompileResolvedBundle", err)
	_, err = engine.resolveEntry(context.Background(), recipeexec.Entry{Bundle: bundle, Digest: "digest"}, blank)
	requireTyped("resolveEntry(unexported)", err)
	if _, err := engine.compileResolvedBundle(context.Background(), bundle, blank, "digest"); err == nil {
		t.Fatal("compileResolvedBundle accepted blank")
	} else {
		requireTyped("compileResolvedBundle(unexported)", err)
	}
	// ResolveVersion has no own guard: blank must be typed by resolveEntry.
	_, err = engine.ResolveVersion(context.Background(), "x", "v1", blank)
	requireTyped("ResolveVersion->resolveEntry lane", err)
	if registry.loads == 0 {
		t.Fatal("ResolveVersion lane must consult registry before the resolveEntry guard")
	}
	// Attack-2 divergence artifact: whitespace guard and Canonical emptiness
	// predicates differ. "/" is non-blank per the guards yet Canonical("") —
	// it reaches the registry instead of being classified. If this load
	// attempt ever becomes a rejection, the two predicates converged.
	divergeRegistry := &reviewBlankRegistry{}
	divergeEngine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      divergeRegistry,
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := projectid.Canonical("/"); got != "" {
		t.Fatalf("Canonical(%q)=%q want empty", "/", got)
	}
	_, _ = divergeEngine.Resolve(context.Background(), "x", recipe.RuntimeBindings{Project: " / "})
	if divergeRegistry.loads != 1 {
		t.Fatalf("slash project divergence: loads=%d want 1 (guards let Canonical-empty identity through)", divergeRegistry.loads)
	}
}
