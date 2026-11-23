package dataframe

type DataframeQueryDiagnostics struct {
	InputResolutionMs    float64                           `json:"inputResolutionMs"`
	RequestPreparationMs float64                           `json:"requestPreparationMs"`
	CompilationMs        float64                           `json:"compilationMs"`
	ArangoQueryMs        float64                           `json:"arangoQueryMs"`
	RowMaterializationMs float64                           `json:"rowMaterializationMs"`
	ResultAssemblyMs     float64                           `json:"resultAssemblyMs"`
	TotalMs              float64                           `json:"totalMs"`
	Plan                 *DataframeCompilerPlanDiagnostics `json:"plan"`
}

type DataframeCompilerPlanDiagnostics struct {
	TraversalSets                     int                          `json:"traversalSets"`
	SharedTraversalCount              int                          `json:"sharedTraversalCount"`
	RequiredMatchReuseCount           int                          `json:"requiredMatchReuseCount"`
	ScopedSharingCandidateGroups      int                          `json:"scopedSharingCandidateGroups"`
	ScopedSharingCandidateSets        int                          `json:"scopedSharingCandidateSets"`
	PotentialSharingOpportunityGroups int                          `json:"potentialSharingOpportunityGroups"`
	PotentialSharingOpportunitySets   int                          `json:"potentialSharingOpportunitySets"`
	OptimizationPolicy                *DataframeOptimizationPolicy `json:"optimizationPolicy"`
	RichSourceReuse                   []*DataframeRichSourceReuse  `json:"richSourceReuse"`
}

type DataframeOptimizationPolicy struct {
	Name           string                           `json:"name"`
	Enabled        bool                             `json:"enabled"`
	MinimumSavings int                              `json:"minimumSavings"`
	Decisions      []*DataframeOptimizationDecision `json:"decisions"`
}

type DataframeOptimizationDecision struct {
	Rule                   string `json:"rule"`
	Enabled                bool   `json:"enabled"`
	CandidateSets          int    `json:"candidateSets"`
	EstimatedBaselineWork  int    `json:"estimatedBaselineWork"`
	EstimatedOptimizedWork int    `json:"estimatedOptimizedWork"`
	EstimatedSavings       int    `json:"estimatedSavings"`
	Reason                 string `json:"reason"`
}

type DataframeRichSourceReuse struct {
	SourceSet          string `json:"sourceSet"`
	AggregateConsumers int    `json:"aggregateConsumers"`
	PivotConsumers     int    `json:"pivotConsumers"`
	SliceConsumers     int    `json:"sliceConsumers"`
	TotalConsumers     int    `json:"totalConsumers"`
}
