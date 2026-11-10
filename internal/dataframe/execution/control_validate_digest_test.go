package execution

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
)

// validateDataframeRecipe/explainDataframeRecipe used the named-registry
// lookup unconditionally, ignoring bindings.recipeDigest. Since the named
// registry only holds the startup default recipe, every digest-addressed
// validation of a registered revision returned RECIPE_NOT_FOUND even though
// the digest-addressed readback and preview/run paths resolved it (control
// asymmetry: Validate was the only control entry bypassing Engine.Resolve).

type valNamedAbsent struct{ loads int }

func (r *valNamedAbsent) LoadRecipe(context.Context, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, recipeexec.ErrRecipeNotFound
}

func (r *valNamedAbsent) LoadRecipeVersion(context.Context, string, string) (recipeexec.Entry, error) {
	r.loads++
	return recipeexec.Entry{}, recipeexec.ErrRecipeNotFound
}

type valRevisions struct {
	revision   recipe.RecipeRevision
	gets       int
	lastDigest string
}

func (rs *valRevisions) Register(context.Context, string, recipe.Bundle, string) (recipe.RecipeRevision, error) {
	return recipe.RecipeRevision{}, errors.New("unexpected Register")
}

func (rs *valRevisions) Get(_ context.Context, project, name, digest string) (recipe.RecipeRevision, error) {
	rs.gets++
	rs.lastDigest = digest
	if project != rs.revision.Project || name != rs.revision.Name || digest != rs.revision.Digest {
		return recipe.RecipeRevision{}, recipe.ErrRecipeRevisionNotFound
	}
	return rs.revision, nil
}

func (rs *valRevisions) List(context.Context, string, string) ([]recipe.RecipeRevision, error) {
	return nil, nil
}

func valControl(t *testing.T, revisions *valRevisions) (*Engine, *valNamedAbsent) {
	t.Helper()
	registry := &valNamedAbsent{}
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      registry,
		Revisions:     revisions,
		QueryRows: func(_ context.Context, command command.CompiledCommand, _ int, _ RowVisitor) error {
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
	return engine, registry
}

func revisionFor(rowGrain, digest string) recipe.RecipeRevision {
	return recipe.RecipeRevision{
		Project: "HTAN_INT", Name: "rows-a", Digest: digest,
		Bundle: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "rows-a", TranslationVersion: "t",
			Outputs: []recipe.Output{{Name: "rows", RootResourceType: "Patient", RowGrain: rowGrain}},
		},
	}
}

func TestValidateDigestAddressedRevisionResolves(t *testing.T) {
	controlEngine, registry := valControl(t, &valRevisions{revision: revisionFor("patient", "abc")})
	control := Control{Engine: controlEngine}
	validation, err := control.Validate(context.Background(), "rows-a", recipe.RuntimeBindings{Project: "HTAN_INT", RecipeDigest: "abc"})
	if err != nil {
		t.Fatalf("digest-addressed validation of a stored revision failed (pre-repair: named-only lookup => RECIPE_NOT_FOUND): %v", err)
	}
	if validation.Entry.Digest != "abc" {
		t.Fatalf("validation entry digest = %q, want abc", validation.Entry.Digest)
	}
	if registry.loads != 0 {
		t.Fatalf("digest-addressed validate fell through to named registry: %d loads", registry.loads)
	}
	if revisions2 := controlEngine.revisions.(*valRevisions); revisions2.gets != 1 || revisions2.lastDigest != "abc" {
		t.Fatalf("revision store saw gets=%d lastDigest=%q, want 1 and abc", revisions2.gets, revisions2.lastDigest)
	}
}

func TestValidateByNameStillUsesNamedRegistry(t *testing.T) {
	controlEngine, registry := valControl(t, &valRevisions{revision: revisionFor("patient", "abc")})
	control := Control{Engine: controlEngine}
	_, err := control.Validate(context.Background(), "rows-a", recipe.RuntimeBindings{Project: "HTAN_INT"})
	if !errors.Is(err, recipeexec.ErrRecipeNotFound) {
		t.Fatalf("name-only validation must keep the named-registry lane, got %v", err)
	}
	if registry.loads != 1 {
		t.Fatalf("named registry loads = %d, want 1", registry.loads)
	}
}

func TestValidateDigestBlankProjectGuardedFirst(t *testing.T) {
	revisions := &valRevisions{revision: revisionFor("patient", "abc")}
	controlEngine, _ := valControl(t, revisions)
	control := Control{Engine: controlEngine}
	_, err := control.Validate(context.Background(), "rows-a", recipe.RuntimeBindings{Project: "  ", RecipeDigest: "abc"})
	if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodeProjectRequired) {
		t.Fatalf("blank project must stay PROJECT_REQUIRED pre-store: ok=%v err=%v", ok, err)
	}
	if revisions.gets != 0 {
		t.Fatalf("revision store reached before project guard: %d gets", revisions.gets)
	}
}

// Wave-3 boundary extended to validate: now that digest-addressed revisions
// resolve here, structural user faults stay typed INVALID_REQUEST through
// Validate (and therefore Explain, which delegates to it).
func TestValidateDigestAddressedStructuralFaultStaysTyped(t *testing.T) {
	controlEngine, _ := valControl(t, &valRevisions{revision: revisionFor("Patient", "abc")})
	control := Control{Engine: controlEngine}
	_, err := control.Validate(context.Background(), "rows-a", recipe.RuntimeBindings{Project: "HTAN_INT", RecipeDigest: "abc"})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) || userErr.Retryable() {
		t.Fatalf("structural user fault through validate escaped classification: ok=%v err=%v", ok, err)
	}
	if userErr.Error() != `unsupported row grain "Patient"` {
		t.Fatalf("message drifted: %q", userErr.Error())
	}
}

// A wrong digest must stay the resolver-classifiable sentinel (mapped to
// RECIPE_NOT_FOUND by recipeGraphQLError), not be wrapped into
// BACKEND_UNAVAILABLE by recipeControlBackend — the sibling preview lane
// (Engine.Resolve, unwrapped) returns the sentinel raw, and both lanes must
// give the boundary the same signal.
func TestValidateUnknownDigestStaysResolverClassifiable(t *testing.T) {
	controlEngine, _ := valControl(t, &valRevisions{revision: revisionFor("patient", "abc")})
	control := Control{Engine: controlEngine}
	_, err := control.Validate(context.Background(), "rows-a", recipe.RuntimeBindings{Project: "HTAN_INT", RecipeDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"})
	if !errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
		t.Fatalf("wrong digest lost its sentinel: %v", err)
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		t.Fatalf("wrong digest pre-typed at control layer (%s), boundary cannot map it to RECIPE_NOT_FOUND", err)
	}
}
