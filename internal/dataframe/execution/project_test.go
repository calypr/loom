package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type blankProjectRegistryFailures struct{ loads int }

func (r *blankProjectRegistryFailures) LoadRecipe(context.Context, string) (recipe.Entry, error) {
	r.loads++
	return recipe.Entry{}, errors.New("recipe registry consulted for blank project")
}

func (r *blankProjectRegistryFailures) LoadRecipeVersion(context.Context, string, string) (recipe.Entry, error) {
	r.loads++
	return recipe.Entry{}, errors.New("recipe registry consulted for blank project")
}

func blankProjectEngine(t *testing.T) (*Engine, *blankProjectRegistryFailures) {
	t.Helper()
	registry := &blankProjectRegistryFailures{}
	engine := newTestEngine(t, registry, "query executor consulted for blank project", "create recipe engine")
	return engine, registry
}

func TestRecipeControlBlankProjectGuardTypesThrowsAtEscapeSite(t *testing.T) {
	engine, registry := blankProjectEngine(t)
	for _, leg := range []struct {
		name string
		call func(context.Context, string, recipe.RuntimeBindings) error
	}{
		{"run", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			resolved, err := engine.Resolve(ctx, name, b)
			if err != nil {
				return err
			}
			_, err = engine.Run(ctx, resolved)
			return err
		}},
		{"preview", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			resolved, err := engine.Resolve(ctx, name, b)
			if err != nil {
				return err
			}
			_, err = engine.Preview(ctx, resolved, b.PreviewLimit)
			return err
		}},
		{"resolve", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := engine.Resolve(ctx, name, b)
			return err
		}},
		{"validate", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := engine.entry(ctx, name, b)
			return err
		}},
		{"explain", func(ctx context.Context, name string, b recipe.RuntimeBindings) error {
			_, err := engine.Resolve(ctx, name, b)
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
