package dataframe

import (
	"encoding/json"

	"github.com/calypr/loom/internal/api/columncapabilities"
	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	materialization "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/projectid"
)

func Model(value materialization.Materialization) *dataframemodel.DataframeMaterialization {
	revision := value.Revision
	if revision == "" {
		revision = value.ID
	}
	columns := make([]*dataframemodel.DataframeColumn, 0, len(value.Columns))
	for _, column := range value.Columns {
		if column.Name == "auth_resource_path" || column.Name == "__loom_row_id" {
			continue
		}
		columns = append(columns, ColumnModel(column))
	}
	var readyAt *string
	if value.ReadyAt != nil {
		formatted := value.ReadyAt.UTC().Format("2006-01-02T15:04:05.999Z07:00")
		readyAt = &formatted
	}
	failure, failureCode, failureRetryable := PersistedFailure(value.Error, value.FailureCode, value.FailureRetryable)
	var rowCount *int
	if value.RowCountKnown || value.Project != "" {
		count := int(value.RowCount)
		rowCount = &count
	}
	result := &dataframemodel.DataframeMaterialization{
		ID: value.ID, Name: value.Name, Revision: revision,
		ProjectID: projectid.Canonical(value.Project), DatasetGeneration: value.DatasetGeneration,
		State: dataframemodel.DataframeMaterializationState(value.State), Columns: columns,
		RowCount:  rowCount,
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
		ReadyAt:   readyAt, Error: failure, ErrorCode: failureCode, ErrorRetryable: failureRetryable,
	}
	if value.Selector.Valid() {
		result.Selector = &dataframemodel.DataframeSelector{Recipe: value.Selector.Recipe, TranslationVersion: value.Selector.TranslationVersion, Output: value.Selector.Output}
	}
	return result
}

func ColumnFromPhysical(column publication.PhysicalColumn) *dataframemodel.DataframeColumn {
	return ColumnModel(materialization.Column{Name: column.Name, SemanticPath: column.SemanticPath, ClickHouse: column.ClickHouse, LogicalType: column.LogicalType, Nullable: column.Nullable, Repeated: column.Repeated})
}

func ColumnModel(column materialization.Column) *dataframemodel.DataframeColumn {
	capabilities := columncapabilities.Resolve(column.ClickHouse, column.LogicalType, column.Nullable, column.Repeated)
	return &dataframemodel.DataframeColumn{SemanticPath: column.SemanticPath, Name: column.Name, ClickhouseType: column.ClickHouse, LogicalType: capabilities.Logical, Nullable: capabilities.Nullable, Repeated: capabilities.Repeated, Filterable: capabilities.Filterable, Sortable: capabilities.Sortable, Aggregatable: capabilities.Aggregatable}
}

func PersistedFailure(raw, code string, retryable bool) (message, failureCode *string, failureRetryable *bool) {
	if code == "" && raw == "" {
		return nil, nil, nil
	}
	if code == "" {
		publicMessage, publicCode, publicRetryable := dataframeerrors.SanitizePersistedFailure(raw)
		return &publicMessage, &publicCode, &publicRetryable
	}
	publicCode := code
	publicMessage := dataframeerrors.PublicMessage(dataframeerrors.NewError(dataframeerrors.ErrorCode(code), "", dataframeerrors.WithRetryable(retryable)))
	return &publicMessage, &publicCode, &retryable
}

func AggregateRowsResult(value []map[string]any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return data, nil
}
