package server

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// explainCompiledQuery is an AQL policy surface: the EXPLAIN view is derived
// from the command through closed dispatch, so a PostgreSQL-tagged, untagged,
// or unknown-tag command is refused here and never sent to the Arango EXPLAIN
// endpoint.
func explainCompiledQuery(ctx context.Context, client *arangostore.Client, compiled compiler.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
	query, bindVars, dispatchErr := compiled.Command.AQLParameters()
	if dispatchErr != nil {
		return dataframeexecution.ExplainAssessment{}, dispatchErr
	}
	result, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
	if err != nil {
		return dataframeexecution.ExplainAssessment{}, err
	}
	return arangoAssessment(arangostore.AssessExplainResult(result)), nil
}

// arangoAssessment converts the Arango store's explain assessment to the
// sanitized control-plane DTO. It lives at this adapter boundary (not in
// shared execution) so the execution package never names a concrete Arango
// plan or index type, and it copies every slice so callers cannot mutate
// shared assessment state.
func arangoAssessment(input arangostore.ExplainAssessment) dataframeexecution.ExplainAssessment {
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

// PreviewCollectionScanEvidenceFromExplain decodes one Arango explain
// response into the neutral preview scan evidence consumed by shared
// execution. Vendor plan decoding lives here, at the Arango adapter
// boundary: only a single decodable plan (a lone Plan, or a lone alternative)
// yields evidence — any other shape yields the zero value, which certifies
// nothing. Nodes are filtered to the queried collection, a full collection
// enumeration is recorded as a blocking fact, and each index node keeps the
// names of the indexes attributed to that collection plus one coverage
// tri-state: both coverage flags true is covering, both false is not
// covering, and an incomplete or inconsistent pair is Unknown.
func PreviewCollectionScanEvidenceFromExplain(result arangostore.ExplainResult, collection string) dataframeexecution.PreviewCollectionScanEvidence {
	var plan *arangostore.ExplainPlan
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

func previewIndexNodeEvidence(node arangostore.ExplainNode, collection string) dataframeexecution.PreviewIndexNodeEvidence {
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

// previewCollectionIndexesFromStore converts the Arango store's filtered
// preview index listing to the neutral definition execution consumes.
func previewCollectionIndexesFromStore(indexes []arangostore.PreviewCoveringIndex) []dataframeexecution.PreviewCollectionIndex {
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

// explainPostgreSQLCompiledQuery is the PostgreSQL policy surface of live
// recipe Explain. It refuses a nil executor fail-closed — the PostgreSQL
// source runtime is wired by a later slice, and until then a PostgreSQL-
// tagged command must report a typed backend failure rather than fall back
// to the Arango EXPLAIN endpoint. With an executor, the sanitized assessment
// comes from execution.PostgreSQLExplainPlan, which reaches PostgreSQL only
// through the parent-tagged EXPLAIN command.
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
