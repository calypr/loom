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

type streamResult struct {
	Columns     []string
	RowCount    int
	Diagnostics QueryDiagnostics
}
