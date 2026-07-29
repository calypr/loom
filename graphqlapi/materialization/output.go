package materializationapi

import (
	"encoding/json"
	"strings"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe/materialization"
)

func Model(value materialization.Materialization) *model.DataframeMaterialization {
	revision := value.Revision
	if revision == "" {
		revision = value.ID
	}
	columns := make([]*model.DataframeColumn, 0, len(value.Columns))
	for _, column := range value.Columns {
		logical, nullable, repeated, filterable, sortable, aggregatable := columnCapabilities(column.ClickHouse)
		columns = append(columns, &model.DataframeColumn{Name: column.Name, ClickhouseType: column.ClickHouse, LogicalType: logical, Nullable: nullable, Repeated: repeated, Filterable: filterable, Sortable: sortable, Aggregatable: aggregatable})
	}
	var readyAt *string
	if value.ReadyAt != nil {
		formatted := value.ReadyAt.UTC().Format("2006-01-02T15:04:05.999Z07:00")
		readyAt = &formatted
	}
	var failure *string
	if value.Error != "" {
		failure = &value.Error
	}
	return &model.DataframeMaterialization{
		ID: value.ID, Name: value.Name, Revision: revision,
		State: model.DataframeMaterializationState(value.State), Columns: columns,
		RowCount:  int(value.RowCount),
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
		ReadyAt:   readyAt, Error: failure,
	}
}

func FederatedMaterialization(dataset materialization.FederatedDataset) *model.DataframeMaterialization {
	return Model(materialization.Materialization{
		ID: "federated:" + dataset.Name, Name: dataset.Name, Revision: dataset.Revision,
		DatasetGeneration: "federated:" + dataset.Revision, State: materialization.StateReady,
		Columns: dataset.Columns, RowCount: dataset.RowCount,
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

func AggregateRows(value []map[string]any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
