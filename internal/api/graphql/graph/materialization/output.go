package materializationapi

import (
	"encoding/json"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/published"
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
		logical, nullable, repeated, filterable, sortable, aggregatable := columnCapabilities(column.ClickHouse)
		columns = append(columns, &model.DataframeColumn{Name: column.Name, ClickhouseType: column.ClickHouse, LogicalType: logical, Nullable: nullable, Repeated: repeated, Filterable: filterable, Sortable: sortable, Aggregatable: aggregatable})
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
		State: model.DataframeMaterializationState(value.State), Columns: columns,
		RowCount:  rowCount,
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
		ReadyAt:   readyAt, Error: failure, ErrorCode: failureCode, ErrorRetryable: failureRetryable,
		ProjectStatuses: []*model.DataframeProjectStatus{},
	}
	if value.Selector.Valid() {
		result.Selector = &model.DataframeSelector{Recipe: value.Selector.Recipe, TranslationVersion: value.Selector.TranslationVersion, Output: value.Selector.Output}
		activeVersion := value.ActiveContractVersion
		if activeVersion == "" {
			activeVersion = value.Selector.TranslationVersion
		}
		result.ActiveContractVersion = &activeVersion
	}
	if value.Availability != "" {
		availability := model.DataframeAvailability(value.Availability)
		result.Availability = &availability
		included, expected := value.IncludedProjects, value.ExpectedProjects
		result.IncludedProjectCount, result.ExpectedProjectCount = &included, &expected
		completeness := 0.0
		if expected > 0 {
			completeness = float64(included) / float64(expected)
		}
		result.Completeness = &completeness
		result.ProjectStatuses = projectStatusModels(value.ProjectStatuses)
	}
	return result
}

func projectStatusModels(values []materialization.ProjectStatus) []*model.DataframeProjectStatus {
	result := make([]*model.DataframeProjectStatus, 0, len(values))
	for _, value := range values {
		item := &model.DataframeProjectStatus{Project: value.ProjectID, State: model.DataframeProjectState(value.State), Retryable: value.Retryable}
		if value.Generation != "" {
			item.Generation = &value.Generation
		}
		if value.ExecutionID != "" {
			item.ExecutionID = &value.ExecutionID
		}
		if !value.CreatedAt.IsZero() {
			formatted := value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00")
			item.CreatedAt = &formatted
		}
		if !value.UpdatedAt.IsZero() {
			formatted := value.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00")
			item.UpdatedAt = &formatted
		}
		if value.ErrorCode != "" {
			item.ErrorCode = &value.ErrorCode
		}
		result = append(result, item)
	}
	return result
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

func FederatedMaterialization(dataset materialization.FederatedDataset) *model.DataframeMaterialization {
	return Model(materialization.Materialization{
		ID: "federated:" + dataset.Selector.Key(), Name: dataset.Name, Revision: dataset.Revision,
		DatasetGeneration: "federated:" + dataset.Revision, State: materialization.StateReady,
		Columns: dataset.Columns, RowCount: dataset.RowCount, RowCountKnown: dataset.RowCountComplete,
		Selector: dataset.Selector, Availability: dataset.Availability, ExpectedProjects: dataset.ExpectedProjects, ProjectStatuses: dataset.ProjectStatuses, ActiveContractVersion: dataset.ActiveContractVersion,
	})
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
