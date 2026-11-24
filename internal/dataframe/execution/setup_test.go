package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func newTestEngine(t *testing.T, registry recipe.Reader, queryError, failureContext string) *Engine {
	t.Helper()
	engine, err := New(Config{
		Registry: registry,
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return errors.New(queryError)
		},
	})
	if err != nil {
		t.Fatalf("%s: %v", failureContext, err)
	}
	return engine
}
