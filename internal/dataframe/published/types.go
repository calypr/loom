package published

import (
	"time"

	publication "github.com/calypr/loom/internal/dataframe/publication"
	dataset "github.com/calypr/loom/internal/dataset"
)

type State string

const authResourcePathColumn = "auth_resource_path"
const projectIDColumn = "project_id"

const StateReady State = "READY"

type Column struct {
	Name         string `json:"name"`
	SemanticPath string `json:"semanticPath,omitempty"`
	ClickHouse   string `json:"clickhouseType"`
	LogicalType  string `json:"logicalType,omitempty"`
	Nullable     bool   `json:"nullable,omitempty"`
	Repeated     bool   `json:"repeated,omitempty"`
}

type DataframeSelector = dataset.DataframeSelector

type Materialization struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Revision          string            `json:"revision,omitempty"`
	Project           string            `json:"project"`
	DatasetGeneration string            `json:"datasetGeneration"`
	State             State             `json:"state"`
	ScopeUnrestricted bool              `json:"scopeUnrestricted"`
	AuthResourcePaths []string          `json:"authResourcePaths,omitempty"`
	Columns           []Column          `json:"columns"`
	PhysicalTable     string            `json:"physicalTable"`
	RowCount          int64             `json:"rowCount"`
	RowCountKnown     bool              `json:"-"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	ReadyAt           *time.Time        `json:"readyAt,omitempty"`
	Error             string            `json:"error,omitempty"`
	FailureCode       string            `json:"failureCode,omitempty"`
	FailureRetryable  bool              `json:"failureRetryable,omitempty"`
	Selector          DataframeSelector `json:"selector"`
}

func publishedMaterialization(execution publication.BundleExecution, output publication.BundleOutputRecord, resourceType string) Materialization {
	return Materialization{
		ID:                execution.ID + ":" + output.Name,
		Name:              resourceType,
		Revision:          execution.ID,
		Project:           execution.Project,
		DatasetGeneration: execution.DatasetGeneration,
		State:             StateReady,
		ScopeUnrestricted: len(execution.AuthResourcePaths) == 0,
		AuthResourcePaths: append([]string(nil), execution.AuthResourcePaths...),
		Columns:           publishedColumns(output.Columns),
		PhysicalTable:     output.PhysicalTable,
		RowCount:          output.RowCount,
		CreatedAt:         execution.CreatedAt,
		UpdatedAt:         execution.UpdatedAt,
		ReadyAt:           execution.ReadyAt,
	}
}

func publishedColumns(columns []publication.PhysicalColumn) []Column {
	result := make([]Column, len(columns))
	for index, column := range columns {
		result[index] = Column{Name: column.Name, SemanticPath: column.SemanticPath, ClickHouse: column.ClickHouse, LogicalType: column.LogicalType, Nullable: column.Nullable, Repeated: column.Repeated}
	}
	return result
}
