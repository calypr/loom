package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type PreviewCoveringIndexSpec struct {
	Collection          string
	Name                string
	Fields              []string
	StoredValues        []string
	Supersedes          *PreviewCoveringIndexReplacement
	PrepareAfterPreview bool

	pivotGroupKeyPaths [][]string
}

type PreviewCoveringIndexReplacement struct {
	Name         string
	Fields       []string
	StoredValues []string
}

type PreviewGroupScanSpec struct {
	Collection string
	Sequential command.CompiledCommand
	ScopeCount command.CompiledCommand
}

type PreviewGroupProjectionSpec struct {
	Collection    string
	ScopeCount    command.CompiledCommand
	ReadOnlyIndex *PreviewGroupProjectionIndexSpec
}

type PreviewGroupProjectionIndexSpec struct {
	RequiredFields []string
	ProjectionPath string
}

type CompiledQuery struct {
	PhysicalPlan           ir.PhysicalPlan
	Project                string
	DatasetGeneration      string
	RootResourceType       string
	TranslationVersion     string
	AuthResourcePaths      []string
	PlanMode               string
	PlanProfile            string
	TraversalCount         int
	OptimizationRules      []string
	RowIdentity            *spec.RowIdentity
	Command                command.CompiledCommand
	Columns                []string
	OutputSchema           []lower.CompiledOutputColumn
	PublicColumns          []string
	PivotFields            []string
	Limit                  int
	PartialValidation      bool
	PreviewCoveringIndex   *PreviewCoveringIndexSpec
	PreviewGroupScan       *PreviewGroupScanSpec
	PreviewGroupProjection *PreviewGroupProjectionSpec
	PlanDiagnostics        ir.CompilerPlanDiagnostics
}
