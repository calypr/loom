package publication

import "time"

type State string

const authResourcePathColumn = "auth_resource_path"

const (
	StateReady State = "READY"
)

type PhysicalColumn struct {
	Name       string `json:"name"`
	ClickHouse string `json:"clickhouseType"`
}

type Materialization struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Revision          string           `json:"revision,omitempty"`
	Project           string           `json:"project"`
	DatasetGeneration string           `json:"datasetGeneration"`
	State             State            `json:"state"`
	ScopeUnrestricted bool             `json:"scopeUnrestricted"`
	AuthResourcePaths []string         `json:"authResourcePaths,omitempty"`
	Columns           []PhysicalColumn `json:"columns"`
	PhysicalTable     string           `json:"physicalTable"`
	RowCount          int64            `json:"rowCount"`
	RowCountKnown     bool             `json:"-"`
	CreatedAt         time.Time        `json:"createdAt"`
	ReadyAt           *time.Time       `json:"readyAt,omitempty"`
	Error             string           `json:"error,omitempty"`
	FailureCode       string           `json:"failureCode,omitempty"`
	FailureRetryable  bool             `json:"failureRetryable,omitempty"`
}
