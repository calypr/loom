// Package control exposes the backend-neutral control-plane operations
// needed by GraphQL or HTTP adapters. It deliberately contains no transport
// types and no translation-specific output names.
package engine

import (
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type Validation struct {
	Entry exec.Entry
	Plan  semantic.RecipePlan
}

type Preview struct {
	Plan    semantic.ResolvedRecipePlan
	Outputs []OutputRows
}

type OutputRows struct {
	Name    string
	Columns []string
	Rows    []map[string]any
}

// ExplainPhysical returns compiler diagnostics and, when requested by the
// caller, a live Arango assessment for every resolved output. Semantic Explain
// remains available through Explain and is intentionally not replaced by this
// backend-facing view.
