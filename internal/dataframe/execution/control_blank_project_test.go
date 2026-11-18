package execution_test

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

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
