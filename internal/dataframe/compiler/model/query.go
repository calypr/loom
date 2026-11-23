package model

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type PreviewCoveringIndexSpec struct {
	Collection string
	Name       string
	Fields     []string

	StoredValues []string

	PrepareAfterPreview bool
}

type PreviewGroupScanSpec struct {
	Collection         string
	SequentialQuery    string
	SequentialBindVars map[string]any
	ScopeCountQuery    string
	ScopeCountBindVars map[string]any
}

type PreviewGroupProjectionSpec struct {
	Collection         string
	ScopeCountQuery    string
	ScopeCountBindVars map[string]any
	ReadOnlyIndex      *PreviewGroupProjectionIndexSpec
}

type PreviewGroupProjectionIndexSpec struct {
	RequiredFields       []string
	RequiredStoredValues []string
	ProjectionPath       string
}

type CompiledQuery struct {
	ArtifactPlanDigest string
	PhysicalPlan       ir.PhysicalPlan
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

	OutputSchema  []lower.CompiledOutputColumn
	PublicColumns []string
	PivotFields   []string
	Limit         int

	PartialValidation      bool
	PreviewCoveringIndex   *PreviewCoveringIndexSpec
	PreviewGroupScan       *PreviewGroupScanSpec
	PreviewGroupProjection *PreviewGroupProjectionSpec
	PlanDiagnostics        ir.CompilerPlanDiagnostics
}
