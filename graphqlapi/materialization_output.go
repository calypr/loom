package graphqlapi

import (
	"encoding/json"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe/materialization"
)

func materializationModel(value materialization.Materialization) *model.DataframeMaterialization {
	columns := make([]*model.DataframeColumn, 0, len(value.Columns))
	for _, column := range value.Columns {
		columns = append(columns, &model.DataframeColumn{Name: column.Name, ClickhouseType: column.ClickHouse})
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
		ID: value.ID, Name: value.Name, Project: value.Project,
		DatasetGeneration: value.DatasetGeneration,
		State:             model.DataframeMaterializationState(value.State), Columns: columns,
		RowCount: int(value.RowCount),
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
		ReadyAt:   readyAt, Error: failure,
	}
}

func aggregateRows(value []map[string]any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
