package published

import "sort"

type AggregationSpec struct {
	Name              string
	Kind              string
	Column            string
	Size              int
	Interval          float64
	DateInterval      int
	ExcludeSelfFilter bool
}

type AggregationsRequest struct {
	Filters         []Filter
	Specs           []AggregationSpec
	AccessByProject map[string]SourceAccess
}

type AggregationResult struct {
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	Columns      []string         `json:"columns"`
	Rows         []map[string]any `json:"rows"`
	MissingCount int64            `json:"missingCount,omitempty"`
	Truncated    bool             `json:"truncated,omitempty"`
}

type AggregationsResult struct {
	Dataset      FederatedDataset
	Aggregations []AggregationResult
}

const (
	defaultAggregationSize = 100
	maxAggregationSize     = 10_000
	maxAggregationSpecs    = 50
	maxGroupingSets        = 256
)

type AggregateResponseMode string

const (
	AggregateResponseLegacy        AggregateResponseMode = "LEGACY"
	AggregateResponseTerms         AggregateResponseMode = "TERMS"
	AggregateResponseHistogram     AggregateResponseMode = "HISTOGRAM"
	AggregateResponseDateHistogram AggregateResponseMode = "DATE_HISTOGRAM"
	AggregateResponseStats         AggregateResponseMode = "STATS"
	AggregateResponseMissing       AggregateResponseMode = "MISSING"
)

type AggregateJob struct {
	ID           int
	ResponseMode AggregateResponseMode
	Filters      []Filter
	GroupBy      []string
	Operation    string
	Column       string
	Size         int
	Interval     float64
	DateInterval int
}

type AggregateJobResult struct {
	ID           int
	Columns      []string
	Rows         []map[string]any
	MissingCount int64
	Truncated    bool
	Err          error
}

type AggregateBatchRequest struct {
	Jobs            []AggregateJob
	AccessByProject map[string]SourceAccess
}

type AggregateBatchResult struct {
	Jobs               []AggregateJobResult
	LogicalJobs        int
	DeduplicatedJobs   int
	Statements         int
	GroupingStatements int
	ScalarStatements   int
	FilterGroups       int
}

func NormalizeAggregationResults(results []AggregationResult) []AggregationResult {
	copyResults := append([]AggregationResult(nil), results...)
	sort.Slice(copyResults, func(i, j int) bool { return copyResults[i].Name < copyResults[j].Name })
	return copyResults
}
