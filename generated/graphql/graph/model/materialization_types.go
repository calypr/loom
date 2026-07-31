package model

// DataframeAggregationsInput/Spec are shared with the optional ClickHouse
// materialization service. They remain transport models even though the
// primary GraphQL schema does not expose that federated field.
type DataframeAggregationSpecInput struct {
	Name              string  `json:"name"`
	Kind              string  `json:"kind"`
	Column            string  `json:"column"`
	Size              *int    `json:"size,omitempty"`
	Interval          *float64 `json:"interval,omitempty"`
	DateInterval      *int    `json:"dateInterval,omitempty"`
	ExcludeSelfFilter *bool   `json:"excludeSelfFilter,omitempty"`
}

type DataframeAggregationsInput struct {
	DataType string                           `json:"dataType"`
	Filters  []*DataframeFilterInput          `json:"filters,omitempty"`
	Specs    []*DataframeAggregationSpecInput `json:"specs"`
}

type DataframeAggregationsResult struct {
	Materialization *DataframeMaterialization `json:"materialization"`
	Aggregations    interface{}                `json:"aggregations"`
}
