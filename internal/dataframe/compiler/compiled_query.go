package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// PreviewCoveringIndexSpec describes a bounded compiler-selected persistent
// index that can cover one terminal nonunique Pivot preview source scan.
// Runtime owners may provision this index before executing the hinted query.
type PreviewCoveringIndexSpec struct {
	Collection         string
	Name               string
	Fields             []string
	pivotGroupKeyPaths [][]string
}

// CompiledQuery is the executable result of the canonical recipe compiler.
// It contains parameterized AQL plus stable metadata for execution, export,
// and diagnostics; it does not expose a transport-specific request builder.
type CompiledQuery struct {
	Project            string
	DatasetGeneration  string
	RootResourceType   string
	TranslationVersion string
	AuthResourcePaths  []string
	PlanMode           string
	PlanProfile        string
	TraversalCount     int
	OptimizationRules  []string
	RowIdentity        *spec.RowIdentity
	Query              string
	BindVars           map[string]any
	Columns            []string
	// OutputSchema is the compiler-owned ordered schema for the finalized
	// physical RETURN projections. PublicColumns is the transport-safe view;
	// Columns remains the legacy execution metadata used by generic runtime
	// callers and may include the stable physical row identity.
	OutputSchema  []lower.CompiledOutputColumn
	PublicColumns []string
	PivotFields   []string
	Limit         int
	// PartialValidation is true when a preview-only source or terminal Pivot
	// group window bounds validation to a deterministic subset of output rows.
	PartialValidation    bool
	PreviewCoveringIndex *PreviewCoveringIndexSpec
	PlanDiagnostics      ir.CompilerPlanDiagnostics
}
