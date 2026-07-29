// Package control exposes the backend-neutral control-plane operations
// needed by GraphQL or HTTP adapters. It deliberately contains no transport
// types and no translation-specific output names.
package control

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type Registry interface {
	Get(string) (exec.Entry, bool)
}

// ContextRegistry is the durable registry shape. Durable adapters keep
// storage errors distinct from a missing recipe; the control-plane adapter
// exposes the small transport-neutral Registry interface used by this
// package's existing in-memory tests.
type ContextRegistry interface {
	LoadRecipe(context.Context, string) (exec.Entry, error)
}

type DurableRegistry struct{ Store ContextRegistry }

type Service struct {
	Registry    Registry
	ScopeDigest func(recipe.RuntimeBindings) string
	// ExplainPhysicalFn is injected by the production canonical compiler
	// boundary. Keeping it as a callback prevents this control package from
	// owning recipe execution or a second physical renderer.
	ExplainPhysicalFn ExplainPhysicalFunc
}

type Validation struct {
	Entry exec.Entry
	Plan  semantic.RecipePlan
}

type Preview struct {
	Plan semantic.ResolvedRecipePlan
	Rows map[string][]map[string]any
	// Outputs preserves recipe order and carries the compiler-owned public
	// schema alongside rows. Rows is retained for non-transport callers during
	// the migration, but adapters must use Outputs when present.
	Outputs []OutputRows
}

type OutputRows struct {
	Name    string
	Columns []string
	Rows    []map[string]any
}

type ExecuteFunc func(context.Context, semantic.ResolvedRecipePlan, int) (map[string][]map[string]any, error)

// ExplainPhysical returns compiler diagnostics and, when requested by the
// caller, a live Arango assessment for every resolved output. Semantic Explain
// remains available through Explain and is intentionally not replaced by this
// backend-facing view.
