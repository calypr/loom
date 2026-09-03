package execution

import (
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type CompiledQuery = compiler.CompiledQuery
type RowIdentity = spec.RowIdentity
type CompilerPlanDiagnostics = ir.CompilerPlanDiagnostics

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
	Plan               ir.CompilerPlanDiagnostics
}

// streamResult is the internal result of generic compiled-query iteration.
// The public recipe-facing StreamResult remains in engine.go and carries the
// output name and publication-safe columns.
type streamResult struct {
	Columns     []string
	RowCount    int
	Diagnostics QueryDiagnostics
}
