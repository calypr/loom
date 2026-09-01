package dataframe

import (
	"encoding/json"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	materialization "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/projectid"
)

func Model(value materialization.Materialization) *model.DataframeMaterialization {
	revision := value.Revision
	if revision == "" {
		revision = value.ID
	}
	columns := make([]*model.DataframeColumn, 0, len(value.Columns))
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
	result := &model.DataframeMaterialization{
		ID: value.ID, Name: value.Name, Revision: revision,
		ProjectID: projectid.Canonical(value.Project), DatasetGeneration: value.DatasetGeneration,
		State: model.DataframeMaterializationState(value.State), Columns: columns,
		RowCount:  rowCount,
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
		ReadyAt:   readyAt, Error: failure, ErrorCode: failureCode, ErrorRetryable: failureRetryable,
	}
	if value.Selector.Valid() {
		result.Selector = &model.DataframeSelector{Recipe: value.Selector.Recipe, TranslationVersion: value.Selector.TranslationVersion, Output: value.Selector.Output}
	}
	return result
}

// ColumnFromPhysical adapts candidate execution metadata before activation.
// It intentionally mirrors the published materialization representation while
// retaining semantic identity for browser-side configuration.
func ColumnFromPhysical(column publication.PhysicalColumn) *model.DataframeColumn {
	return ColumnModel(materialization.Column{Name: column.Name, SemanticPath: column.SemanticPath, ClickHouse: column.ClickHouse, LogicalType: column.LogicalType, Nullable: column.Nullable, Repeated: column.Repeated})
}

func ColumnModel(column materialization.Column) *model.DataframeColumn {
	logical, nullable, repeated, filterable, sortable, aggregatable := columnCapabilities(column.ClickHouse)
	if column.LogicalType != "" {
		logical = column.LogicalType
	}
	if column.Nullable {
		nullable = true
	}
	if column.Repeated {
		repeated = true
	}
	return &model.DataframeColumn{SemanticPath: column.SemanticPath, Name: column.Name, ClickhouseType: column.ClickHouse, LogicalType: logical, Nullable: nullable, Repeated: repeated, Filterable: filterable, Sortable: sortable, Aggregatable: aggregatable}
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

func columnCapabilities(clickHouseType string) (logical string, nullable, repeated, filterable, sortable, aggregatable bool) {
	typ := clickHouseType
	if strings.HasPrefix(typ, "Nullable(") && strings.HasSuffix(typ, ")") {
		nullable = true
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Nullable("), ")")
	}
	if strings.HasPrefix(typ, "Array(") && strings.HasSuffix(typ, ")") {
		repeated = true
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Array("), ")")
	}
	switch {
	case strings.HasPrefix(typ, "Bool"):
		logical = "boolean"
	case strings.HasPrefix(typ, "Int") || strings.HasPrefix(typ, "UInt"):
		logical = "integer"
	case strings.HasPrefix(typ, "Float") || strings.HasPrefix(typ, "Decimal"):
		logical = "number"
	case strings.HasPrefix(typ, "Date"):
		logical = "date"
	case strings.HasPrefix(typ, "String") || strings.HasPrefix(typ, "FixedString") || strings.HasPrefix(typ, "UUID"):
		logical = "string"
	default:
		logical = "json"
	}
	filterable = true
	sortable = !repeated
	aggregatable = !repeated && logical != "json"
	return logical, nullable, repeated, filterable, sortable, aggregatable
}

func AggregateRowsResult(value []map[string]any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return data, nil
}
