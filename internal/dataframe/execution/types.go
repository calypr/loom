package execution

import (
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type RowIdentity = spec.RowIdentity
type CompilerPlanDiagnostics = ir.CompilerPlanDiagnostics

type Result struct {
	Columns     []string
	Rows        []map[string]any
	RowCount    int
	Diagnostics QueryDiagnostics
}

// QueryDiagnostics separates the cost of turning a dataframe request into
// rows. SourceQuery is the source cursor interval for the selected backend,
// excluding Loom's per-row processing; RowMaterialization is the time spent
// flattening and delivering rows. The two never overlap: RowMaterialization
// is subtracted from the cursor interval so materialization is not
// double-counted, and one timer produces SourceQuery for either the AQL or
// PostgreSQL source. SourceBackend names which adapter served the cursor so
// the transport projection can report the legacy arangoQueryMs as actual
// Arango time on AQL and zero on PostgreSQL.
type QueryDiagnostics struct {
	InputResolution    time.Duration
	RequestPreparation time.Duration
	Compilation        time.Duration
	SourceQuery        time.Duration
	SourceBackend      command.CompiledBackend
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
