package execution

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type PhysicalExplanation struct {
	Outputs []PhysicalOutputExplanation
}

type PhysicalOutputExplanation struct {
	Name            string
	PlanFingerprint string
	Columns         []string
	Diagnostics     ir.CompilerPlanDiagnostics
	Live            *ExplainAssessment
	LivePostgreSQL  *PostgreSQLPlanAssessment
}

const (
	PostgreSQLScanSequential = "sequential"
	PostgreSQLScanIndex      = "index"
)

type PostgreSQLPlanAssessment struct {
	Backend      string
	Nodes        []PostgreSQLPlanNode
	FullScans    []PostgreSQLScanEvidence
	IndexScans   []PostgreSQLScanEvidence
	RootEstimate PostgreSQLPlanEstimate
}

type PostgreSQLPlanNode struct {
	Ordinal       int
	ParentOrdinal int
	Depth         int
	NodeType      string
	Estimate      PostgreSQLPlanEstimate
}

type PostgreSQLPlanEstimate struct {
	StartupCost float64
	TotalCost   float64
	PlanRows    float64
	PlanWidth   int
}

type PostgreSQLScanEvidence struct {
	Ordinal  int
	NodeType string
	ScanKind string
}

type ExplainAssessment struct {
	Plans                 []ExplainPlanEstimate
	FullCollectionScans   []ExplainCollectionScan
	Indexes               []ExplainIndexSummary
	Warnings              []ExplainWarning
	AppliedOptimizerRules []string
}

type ExplainPlanEstimate struct {
	Plan             int
	EstimatedCost    float64
	EstimatedNrItems float64
}

type ExplainCollectionScan struct {
	Plan       int
	NodeID     int64
	Collection string
}

type ExplainIndexSummary struct {
	Collection string
	ID         string
	Name       string
	Type       string
	Fields     []string
	Uses       []ExplainIndexLocation
}

type ExplainIndexLocation struct {
	Plan   int
	NodeID int64
}

type ExplainWarning struct {
	Code    int
	Message string
}
