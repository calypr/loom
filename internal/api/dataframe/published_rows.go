package dataframe

import (
	"encoding/json"
	"fmt"
)

type DataframeAggregateInput struct {
	ProjectID string                  `json:"projectId"`
	Selector  *DataframeSelectorInput `json:"selector"`
	GroupBy   []string                `json:"groupBy,omitempty"`
	Filters   []*DataframeFilterInput `json:"filters,omitempty"`
	Operation string                  `json:"operation"`
	Column    *string                 `json:"column,omitempty"`
}

type DataframeAggregationSpecInput struct {
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	Column            string   `json:"column"`
	Size              *int     `json:"size,omitempty"`
	Interval          *float64 `json:"interval,omitempty"`
	DateInterval      *int     `json:"dateInterval,omitempty"`
	ExcludeSelfFilter *bool    `json:"excludeSelfFilter,omitempty"`
}

type DataframeAggregationsInput struct {
	ProjectID string                           `json:"projectId"`
	Selector  *DataframeSelectorInput          `json:"selector"`
	Filters   []*DataframeFilterInput          `json:"filters,omitempty"`
	Specs     []*DataframeAggregationSpecInput `json:"specs"`
}

type DataframeColumn struct {
	SemanticPath   string `json:"semanticPath"`
	Name           string `json:"name"`
	ClickhouseType string `json:"clickhouseType"`
	LogicalType    string `json:"logicalType"`
	Nullable       bool   `json:"nullable"`
	Repeated       bool   `json:"repeated"`
	Filterable     bool   `json:"filterable"`
	Sortable       bool   `json:"sortable"`
	Aggregatable   bool   `json:"aggregatable"`
}

type DataframeDatasetInput struct {
	ProjectID string                  `json:"projectId"`
	Selector  *DataframeSelectorInput `json:"selector"`
}

type DataframeFilterInput struct {
	Column string          `json:"column"`
	Op     string          `json:"op"`
	Value  json.RawMessage `json:"value"`
}

type DataframeMaterialization struct {
	ID                string                        `json:"id"`
	Name              string                        `json:"name"`
	Revision          string                        `json:"revision"`
	ProjectID         string                        `json:"projectId"`
	DatasetGeneration string                        `json:"datasetGeneration"`
	State             DataframeMaterializationState `json:"state"`
	Columns           []*DataframeColumn            `json:"columns"`
	RowCount          *int                          `json:"rowCount"`
	CreatedAt         string                        `json:"createdAt"`
	ReadyAt           *string                       `json:"readyAt"`
	Error             *string                       `json:"error"`
	ErrorCode         *string                       `json:"errorCode"`
	ErrorRetryable    *bool                         `json:"errorRetryable"`
	Selector          *DataframeSelector            `json:"selector"`
}

type DataframeMaterializationState string

const (
	DataframeMaterializationStatePending DataframeMaterializationState = "PENDING"
	DataframeMaterializationStateLoading DataframeMaterializationState = "LOADING"
	DataframeMaterializationStateReady   DataframeMaterializationState = "READY"
	DataframeMaterializationStateFailed  DataframeMaterializationState = "FAILED"
)

var AllDataframeMaterializationState = []DataframeMaterializationState{
	DataframeMaterializationStatePending,
	DataframeMaterializationStateLoading,
	DataframeMaterializationStateReady,
	DataframeMaterializationStateFailed,
}

func (e DataframeMaterializationState) IsValid() bool {
	switch e {
	case DataframeMaterializationStatePending, DataframeMaterializationStateLoading, DataframeMaterializationStateReady, DataframeMaterializationStateFailed:
		return true
	}
	return false
}

func (e DataframeMaterializationState) String() string {
	return string(e)
}

func (e *DataframeMaterializationState) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := DataframeMaterializationState(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid DataframeMaterializationState", value)
	}
	*e = decoded
	return nil
}

type DataframeRowsInput struct {
	ProjectID string                  `json:"projectId"`
	Selector  *DataframeSelectorInput `json:"selector"`
	Columns   []string                `json:"columns,omitempty"`
	Filters   []*DataframeFilterInput `json:"filters,omitempty"`
	Sort      *DataframeSortInput     `json:"sort,omitempty"`
	First     *int                    `json:"first,omitempty"`
	After     *string                 `json:"after,omitempty"`
}

type DataframeSelector struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

type DataframeSelectorInput struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

type DataframeSortInput struct {
	Column string `json:"column"`
	Desc   *bool  `json:"desc,omitempty"`
}

type DataframeRowConnection struct {
	Materialization *DataframeMaterialization `json:"materialization"`
	Columns         []string                  `json:"columns"`
	Rows            json.RawMessage           `json:"rows"`
	RowIds          []string                  `json:"rowIds"`
	TotalCount      *int                      `json:"totalCount"`
	PageInfo        *DataframePageInfo        `json:"pageInfo"`
}

type DataframePageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type DataframeAggregationsResult struct {
	Materialization *DataframeMaterialization `json:"materialization"`
	Aggregations    json.RawMessage           `json:"aggregations"`
}
