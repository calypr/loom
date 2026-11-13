package server

import (
	"context"
	"errors"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

func explainCompiledQuery(ctx context.Context, client *arangofactory.Client, compiled compiler.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
	query, bindVars, dispatchErr := compiled.Command.AQLParameters()
	if dispatchErr != nil {
		return dataframeexecution.ExplainAssessment{}, dispatchErr
	}
	result, err := client.Explain(ctx, arangofactory.ExplainRequest{Query: query, BindVars: bindVars})
	if err != nil {
		return dataframeexecution.ExplainAssessment{}, err
	}
	return arangoAssessment(arangofactory.AssessExplainResult(result)), nil
}

func arangoAssessment(input arangofactory.ExplainAssessment) dataframeexecution.ExplainAssessment {
	out := dataframeexecution.ExplainAssessment{
		Plans:                 make([]dataframeexecution.ExplainPlanEstimate, 0, len(input.Plans)),
		FullCollectionScans:   make([]dataframeexecution.ExplainCollectionScan, 0, len(input.FullCollectionScans)),
		Indexes:               make([]dataframeexecution.ExplainIndexSummary, 0, len(input.Indexes)),
		Warnings:              make([]dataframeexecution.ExplainWarning, 0, len(input.Warnings)),
		AppliedOptimizerRules: append([]string(nil), input.AppliedOptimizerRules...),
	}
	for _, plan := range input.Plans {
		out.Plans = append(out.Plans, dataframeexecution.ExplainPlanEstimate{Plan: plan.Plan, EstimatedCost: plan.EstimatedCost, EstimatedNrItems: plan.EstimatedNrItems})
	}
	for _, scan := range input.FullCollectionScans {
		out.FullCollectionScans = append(out.FullCollectionScans, dataframeexecution.ExplainCollectionScan{Plan: scan.Plan, NodeID: scan.NodeID, Collection: scan.Collection})
	}
	for _, index := range input.Indexes {
		copyIndex := dataframeexecution.ExplainIndexSummary{Collection: index.Collection, ID: index.ID, Name: index.Name, Type: index.Type, Fields: append([]string(nil), index.Fields...), Uses: make([]dataframeexecution.ExplainIndexLocation, 0, len(index.Uses))}
		for _, use := range index.Uses {
			copyIndex.Uses = append(copyIndex.Uses, dataframeexecution.ExplainIndexLocation{Plan: use.Plan, NodeID: use.NodeID})
		}
		out.Indexes = append(out.Indexes, copyIndex)
	}
	for _, warning := range input.Warnings {
		out.Warnings = append(out.Warnings, dataframeexecution.ExplainWarning{Code: warning.Code, Message: warning.Message})
	}
	return out
}

func PreviewCollectionScanEvidenceFromExplain(result arangofactory.ExplainResult, collection string) dataframeexecution.PreviewCollectionScanEvidence {
	var plan *arangofactory.ExplainPlan
	switch {
	case result.Plan != nil && len(result.Plans) == 0:
		plan = result.Plan
	case result.Plan == nil && len(result.Plans) == 1:
		plan = &result.Plans[0]
	default:
		return dataframeexecution.PreviewCollectionScanEvidence{}
	}
	evidence := dataframeexecution.PreviewCollectionScanEvidence{}
	for _, node := range plan.Nodes {
		if node.Collection != collection {
			continue
		}
		switch node.Type {
		case "EnumerateCollectionNode":
			evidence.FullCollectionEnumeration = true
		case "IndexNode":
			evidence.IndexNodes = append(evidence.IndexNodes, previewIndexNodeEvidence(node, collection))
		}
	}
	return evidence
}

func previewIndexNodeEvidence(node arangofactory.ExplainNode, collection string) dataframeexecution.PreviewIndexNodeEvidence {
	evidence := dataframeexecution.PreviewIndexNodeEvidence{Coverage: dataframeexecution.PreviewIndexCoverageUnknown}
	if node.IndexCoversProjections != nil && node.IndexCoversOutProjections != nil {
		switch {
		case *node.IndexCoversProjections && *node.IndexCoversOutProjections:
			evidence.Coverage = dataframeexecution.PreviewIndexCoverageCovering
		case !*node.IndexCoversProjections && !*node.IndexCoversOutProjections:
			evidence.Coverage = dataframeexecution.PreviewIndexCoverageNotCovering
		}
	}
	for _, index := range node.Indexes {
		if index.Collection == "" || index.Collection == collection {
			evidence.IndexNames = append(evidence.IndexNames, index.Name)
		}
	}
	return evidence
}

func previewCollectionIndexesFromStore(indexes []arangofactory.PreviewCoveringIndex) []dataframeexecution.PreviewCollectionIndex {
	if indexes == nil {
		return nil
	}
	out := make([]dataframeexecution.PreviewCollectionIndex, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, dataframeexecution.PreviewCollectionIndex{
			Collection:   index.Collection,
			Name:         index.Name,
			Fields:       index.Fields,
			StoredValues: index.StoredValues,
		})
	}
	return out
}

func explainPostgreSQLCompiledQuery(ctx context.Context, executor dataframeexecution.PostgreSQLPlanJSONExecutor, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
	if executor == nil {
		return dataframeexecution.PostgreSQLPlanAssessment{}, dataframeerrors.Wrap(
			errors.New("live PostgreSQL recipe Explain requires a PostgreSQL connection; the PostgreSQL source runtime is not wired"),
			dataframeerrors.CodeBackendUnavailable, "live PostgreSQL recipe Explain is unavailable",
			dataframeerrors.WithRetryable(false),
			dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "wiring"}),
		)
	}
	return dataframeexecution.PostgreSQLExplainPlan(ctx, executor, compiled)
}
