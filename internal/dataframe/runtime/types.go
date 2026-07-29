package runtime

import (
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type RunRequest struct {
	Recipe   recipe.Bundle
	Bindings recipe.RuntimeBindings
	Limit    int
}

type CompiledQuery = compiler.CompiledQuery
type RowIdentity = compiler.RowIdentity
type CompilerPlanDiagnostics = compiler.CompilerPlanDiagnostics

type Result struct {
	Columns     []string
	Rows        []map[string]any
	RowCount    int
	Diagnostics QueryDiagnostics
}

// QueryDiagnostics separates the cost of turning a dataframe request into
// rows. ArangoQuery is cursor time excluding Loom's per-row processing;
// RowMaterialization is the time spent flattening and delivering rows.
type QueryDiagnostics struct {
	InputResolution    time.Duration
	RequestPreparation time.Duration
	Compilation        time.Duration
	ArangoQuery        time.Duration
	RowMaterialization time.Duration
	ResultAssembly     time.Duration
	Total              time.Duration
	Plan               compiler.CompilerPlanDiagnostics
}

// StreamResult describes rows delivered to a streaming caller. Columns are
// finalized only after iteration because flattened pivots can add bounded,
// data-dependent output keys.
type StreamResult struct {
	Columns     []string
	RowCount    int
	Diagnostics QueryDiagnostics
}
