package execution

import (
	"time"

	model "github.com/calypr/loom/internal/dataframe/compiler/model"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type CompiledQuery = model.CompiledQuery

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
	ArangoQuery        time.Duration
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
