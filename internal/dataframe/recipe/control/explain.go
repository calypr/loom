package control

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
	Name            string
	PlanFingerprint string
	Columns         []string
	Diagnostics     ir.CompilerPlanDiagnostics
	Live            *ExplainAssessment
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
