package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// PreviewCoveringIndexSpec describes a bounded compiler-selected persistent
// index that may cover a preview source scan. Runtime owners may provision
// this index before executing the hinted query.
type PreviewCoveringIndexSpec struct {
	Collection string
	Name       string
	Fields     []string
	// Supersedes identifies the exact legacy category index that may be
	// replaced if the collection has reached its bounded preview-index cap.
	Supersedes *PreviewCoveringIndexReplacement
	// PrepareAfterPreview keeps Group index creation out of the query's critical
	// path. The query uses a non-forcing hint and remains valid without it.
	PrepareAfterPreview bool

	// pivotGroupKeyPaths enables the Pivot-only two-scan renderer. Other
	// preview covering indexes use a single hinted root scan.
	pivotGroupKeyPaths [][]string
}

// PreviewCoveringIndexReplacement is an exact compiler-owned index identity
// eligible for replacement by a more useful index on the same category path.
type PreviewCoveringIndexReplacement struct {
	Name   string
	Fields []string
}

// PreviewGroupScanSpec contains compiler-rendered alternatives for a narrow
// direct-source Group preview. Runtime may choose SequentialQuery only after
// verifying the current scan is non-covering and that the exact scoped source
// count is close to the collection cardinality.
type PreviewGroupScanSpec struct {
	Collection         string
	SequentialQuery    string
	SequentialBindVars map[string]any
	ScopeCountQuery    string
	ScopeCountBindVars map[string]any
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
	PreviewGroupScan     *PreviewGroupScanSpec
	PlanDiagnostics      ir.CompilerPlanDiagnostics
}
