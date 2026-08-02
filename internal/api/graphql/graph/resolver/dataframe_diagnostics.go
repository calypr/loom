package resolver

import (
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/runtime"
)

func dataframeDiagnostics(in runtime.QueryDiagnostics) *model.DataframeQueryDiagnostics {
	return &model.DataframeQueryDiagnostics{
		InputResolutionMs:    milliseconds(in.InputResolution),
		RequestPreparationMs: milliseconds(in.RequestPreparation),
		CompilationMs:        milliseconds(in.Compilation),
		ArangoQueryMs:        milliseconds(in.ArangoQuery),
		RowMaterializationMs: milliseconds(in.RowMaterialization),
		ResultAssemblyMs:     milliseconds(in.ResultAssembly),
		TotalMs:              milliseconds(in.Total),
		Plan:                 compilerPlanDiagnostics(in.Plan),
	}
}

func milliseconds(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func compilerPlanDiagnostics(in ir.CompilerPlanDiagnostics) *model.DataframeCompilerPlanDiagnostics {
	out := &model.DataframeCompilerPlanDiagnostics{
		TraversalSets:                     in.TraversalSets,
		SharedTraversalCount:              in.SharedTraversalCount,
		RequiredMatchReuseCount:           in.RequiredMatchReuseCount,
		ScopedSharingCandidateGroups:      in.ScopedSharingCandidateGroups,
		ScopedSharingCandidateSets:        in.ScopedSharingCandidateSets,
		PotentialSharingOpportunityGroups: in.PotentialSharingOpportunityGroups,
		PotentialSharingOpportunitySets:   in.PotentialSharingOpportunitySets,
		OptimizationPolicy:                optimizationPolicy(in.OptimizationPolicy),
		RichSourceReuse:                   make([]*model.DataframeRichSourceReuse, 0, len(in.RichSourceReuse)),
	}
	for _, reuse := range in.RichSourceReuse {
		out.RichSourceReuse = append(out.RichSourceReuse, &model.DataframeRichSourceReuse{
			SourceSet:          reuse.SourceSet,
			AggregateConsumers: reuse.AggregateConsumers,
			PivotConsumers:     reuse.PivotConsumers,
			SliceConsumers:     reuse.SliceConsumers,
			TotalConsumers:     reuse.TotalConsumers(),
		})
	}
	return out
}

func optimizationPolicy(in ir.PhysicalOptimizationReport) *model.DataframeOptimizationPolicy {
	out := &model.DataframeOptimizationPolicy{Name: in.Policy, Enabled: in.Enabled, MinimumSavings: in.MinimumSavings, Decisions: make([]*model.DataframeOptimizationDecision, 0, len(in.Decisions))}
	for _, decision := range in.Decisions {
		out.Decisions = append(out.Decisions, &model.DataframeOptimizationDecision{Rule: decision.Rule, Enabled: decision.Enabled, CandidateSets: decision.CandidateSets, EstimatedBaselineWork: decision.EstimatedBaselineWork, EstimatedOptimizedWork: decision.EstimatedOptimizedWork, EstimatedSavings: decision.EstimatedSavings, Reason: decision.Reason})
	}
	return out
}
