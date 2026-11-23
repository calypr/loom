package recipetest

import (
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func StageFixture(outputID string, source []ws.StageColumn) ws.Document {
	columns := make([]ws.Column, len(source))
	for i, c := range source {
		columns[i] = ws.Column{ColumnID: c.ID, Column: c.Name, Label: c.Label, LogicalType: c.Type, OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}
	}
	return ws.Document{Kind: ws.Kind, Output: ws.Output{ID: outputID, Title: outputID}, RootResourceType: "Patient", Rows: ws.RecordsRowDefinition(), Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: columns, Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{}}}
}
func StageColumns(document ws.Document) []ws.StageColumn {
	if len(document.Construction.Steps) > 0 {
		return append([]ws.StageColumn(nil), document.Construction.Steps[len(document.Construction.Steps)-1].Outputs...)
	}
	columns := make([]ws.StageColumn, len(document.Columns))
	for i, c := range document.Columns {
		columns[i] = ws.StageColumn{ID: c.ColumnID, Name: c.Column, Label: c.Label, Type: c.LogicalType, Table: c.Table}
	}
	return columns
}
func AppendDeriveFixture(document *ws.Document, id string, output ws.StageColumn, operation ws.ConstructionDerivedOperation, left, right ws.ConstructionOperand, missing ws.ConstructionMissingInputPolicy) {
	prior := StageColumns(*document)
	input := ws.ConstructionInputRef{Kind: ws.ConstructionInputSourceProjection}
	if len(document.Construction.Steps) > 0 {
		input = ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: document.Construction.Steps[len(document.Construction.Steps)-1].ID}
	}
	step := ws.ConstructionStep{ID: id, Inputs: []ws.ConstructionInputRef{input}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{ConstructionID: id, OutputColumnID: output.ID, Operation: operation, Left: left, Right: right, MissingInputPolicy: missing}}, Outputs: append(prior, output)}
	document.Construction.Steps = append(document.Construction.Steps, step)
}
func ColumnOperand(id string) ws.ConstructionOperand {
	return ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: id}
}
func IntegerOperand(value *int64) ws.ConstructionOperand {
	return ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: value}}
}
func DecimalOperand(value *float64) ws.ConstructionOperand {
	return ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericDecimal, Decimal: value}}
}

func StageWorkspace(documents ...ws.Document) ws.Workspace {
	workspace := ws.NewWorkspace("Receipt metadata fixture")
	workspace.Documents = documents
	for i, document := range documents {
		workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: i, Visible: true})
	}
	return workspace
}

func EmittedPublicColumnNames(values []viewer.EmittedColumn) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.PublicColumn)
	}
	return names
}
