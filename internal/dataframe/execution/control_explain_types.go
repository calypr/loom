package execution

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/store/arango"
)

// PhysicalExplanation is the output-scoped physical half of recipe Explain.
// It contains compiler structure and an optional live Arango assessment; it
// never contains AQL text, bind values, auth paths, credentials, or connection
// information.
type PhysicalExplanation struct {
	Outputs []PhysicalOutputExplanation
}

type PhysicalOutputExplanation struct {
	Name string
	// PlanFingerprint identifies the exact compiled command the assessment
	// describes: the AQL-shape digest for AQL-tagged outputs and the
	// PostgreSQL command fingerprint for PostgreSQL-tagged ones, matching the
	// fingerprint the corresponding stream reports for the same output.
	PlanFingerprint string
	Columns         []string
	Diagnostics     ir.CompilerPlanDiagnostics
	// Live carries the Arango assessment for an AQL-tagged output and is nil
	// otherwise.
	Live *ExplainAssessment
	// LivePostgreSQL carries the sanitized PostgreSQL assessment for a
	// PostgreSQL-tagged output and is nil otherwise. Tag dispatch makes the
	// two live fields mutually exclusive; an output never carries both.
	LivePostgreSQL *PostgreSQLPlanAssessment
}

// PostgreSQLScanKind names the closed scan classifications a sanitized
// PostgreSQL plan may report.
const (
	PostgreSQLScanSequential = "sequential"
	PostgreSQLScanIndex      = "index"
)

// PostgreSQLPlanAssessment is a transport-neutral, sanitized summary of one
// PostgreSQL 18 EXPLAIN (FORMAT JSON) plan produced through the typed
// command boundary. It deliberately exposes only the agreed backend/node/
// estimate fields: the producing backend tag, plan node types with their
// ordinal structure, closed scan classifications, and numeric cost
// estimates. It never carries statement SQL, bound parameter values, plan
// text strings (relation names, index names, aliases, filters, join
// conditions, sort keys, output lists), DSN/host/schema internals, or any
// other identifier from user data; the sanitizer drops every JSON key it
// does not explicitly recognize and pattern-gates the node types it keeps.
type PostgreSQLPlanAssessment struct {
	// Backend is the compiled-command backend tag that produced this
	// assessment, always string(compiler.CompiledBackendPostgreSQL).
	Backend string
	// Nodes is the pre-order traversal of the plan tree. Ordinal is the
	// pre-order position (the tree identity this assessment synthesizes,
	// because plain EXPLAIN reports no Node ID), ParentOrdinal is -1 for the
	// root, and Depth counts edges below the root.
	Nodes        []PostgreSQLPlanNode
	FullScans    []PostgreSQLScanEvidence
	IndexScans   []PostgreSQLScanEvidence
	RootEstimate PostgreSQLPlanEstimate
}

// PostgreSQLPlanNode is one sanitized plan node: a pattern-validated
// PostgreSQL operator name plus its numeric estimates. Node types arrive
// from PostgreSQL's fixed operator vocabulary; anything outside the safe
// shape is rejected rather than rendered.
type PostgreSQLPlanNode struct {
	Ordinal       int
	ParentOrdinal int
	Depth         int
	NodeType      string
	Estimate      PostgreSQLPlanEstimate
}

// PostgreSQLPlanEstimate holds the four numeric estimates PostgreSQL reports
// for one plan node.
type PostgreSQLPlanEstimate struct {
	StartupCost float64
	TotalCost   float64
	PlanRows    float64
	PlanWidth   int
}

// PostgreSQLScanEvidence identifies one scan node of a closed classification
// by its traversal ordinal and operator name only; the scanned relation and
// index names are deliberately absent.
type PostgreSQLScanEvidence struct {
	Ordinal  int
	NodeType string
	ScanKind string
}

// ExplainAssessment is a transport-neutral, sanitized copy of Arango's
// ExplainAssessment. Keeping this DTO here lets GraphQL and HTTP adapters use
// one assessment shape without parsing Explain responses themselves.
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

// AssessmentFromArango converts the common store assessment to the sanitized
// control-plane DTO. It copies every slice so callers cannot mutate shared
// assessment state.
func AssessmentFromArango(input arango.ExplainAssessment) ExplainAssessment {
	out := ExplainAssessment{
		Plans:                 make([]ExplainPlanEstimate, 0, len(input.Plans)),
		FullCollectionScans:   make([]ExplainCollectionScan, 0, len(input.FullCollectionScans)),
		Indexes:               make([]ExplainIndexSummary, 0, len(input.Indexes)),
		Warnings:              make([]ExplainWarning, 0, len(input.Warnings)),
		AppliedOptimizerRules: append([]string(nil), input.AppliedOptimizerRules...),
	}
	for _, plan := range input.Plans {
		out.Plans = append(out.Plans, ExplainPlanEstimate{Plan: plan.Plan, EstimatedCost: plan.EstimatedCost, EstimatedNrItems: plan.EstimatedNrItems})
	}
	for _, scan := range input.FullCollectionScans {
		out.FullCollectionScans = append(out.FullCollectionScans, ExplainCollectionScan{Plan: scan.Plan, NodeID: scan.NodeID, Collection: scan.Collection})
	}
	for _, index := range input.Indexes {
		copyIndex := ExplainIndexSummary{Collection: index.Collection, ID: index.ID, Name: index.Name, Type: index.Type, Fields: append([]string(nil), index.Fields...), Uses: make([]ExplainIndexLocation, 0, len(index.Uses))}
		for _, use := range index.Uses {
			copyIndex.Uses = append(copyIndex.Uses, ExplainIndexLocation{Plan: use.Plan, NodeID: use.NodeID})
		}
		out.Indexes = append(out.Indexes, copyIndex)
	}
	for _, warning := range input.Warnings {
		out.Warnings = append(out.Warnings, ExplainWarning{Code: warning.Code, Message: warning.Message})
	}
	return out
}

// ClonePhysicalExplanation returns an ownership-safe copy suitable for a
// transport adapter. Compiler diagnostics are structural and contain no bind
// values; clone their nested slices before returning them to callers.
