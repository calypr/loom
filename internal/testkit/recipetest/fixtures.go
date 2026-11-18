package recipetest

import (
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func StageFixture(outputID string, source []authoringv2.StageColumn) authoringv2.Document {
	columns := make([]authoringv2.Column, len(source))
	for i, c := range source {
		columns[i] = authoringv2.Column{ColumnID: c.ID, Column: c.Name, Label: c.Label, LogicalType: c.Type, OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}
	}
	return authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: outputID, Title: outputID}, RootResourceType: "Patient", Rows: authoringv2.RecordsRowDefinition(), Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: columns, Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{}}}
}
func StageColumns(document authoringv2.Document) []authoringv2.StageColumn {
	if len(document.Construction.Steps) > 0 {
		return append([]authoringv2.StageColumn(nil), document.Construction.Steps[len(document.Construction.Steps)-1].Outputs...)
	}
	columns := make([]authoringv2.StageColumn, len(document.Columns))
	for i, c := range document.Columns {
		columns[i] = authoringv2.StageColumn{ID: c.ColumnID, Name: c.Column, Label: c.Label, Type: c.LogicalType, Table: c.Table}
	}
	return columns
}
func AppendDeriveFixture(document *authoringv2.Document, id string, output authoringv2.StageColumn, operation authoringv2.ConstructionDerivedOperation, left, right authoringv2.ConstructionOperand, missing authoringv2.ConstructionMissingInputPolicy) {
	prior := StageColumns(*document)
	input := authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
	if len(document.Construction.Steps) > 0 {
		input = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: document.Construction.Steps[len(document.Construction.Steps)-1].ID}
	}
	step := authoringv2.ConstructionStep{ID: id, Inputs: []authoringv2.ConstructionInputRef{input}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{ConstructionID: id, OutputColumnID: output.ID, Operation: operation, Left: left, Right: right, MissingInputPolicy: missing}}, Outputs: append(prior, output)}
	document.Construction.Steps = append(document.Construction.Steps, step)
}
func ColumnOperand(id string) authoringv2.ConstructionOperand {
	return authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: id}
}
func IntegerOperand(value *int64) authoringv2.ConstructionOperand {
	return authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: value}}
}
func DecimalOperand(value *float64) authoringv2.ConstructionOperand {
	return authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericDecimal, Decimal: value}}
}

func StageWorkspace(documents ...authoringv2.Document) authoringv2.Workspace {
	workspace := authoringv2.NewWorkspace("Receipt metadata fixture")
	workspace.Documents = documents
	for i, document := range documents {
		workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: i, Visible: true})
	}
	return workspace
}

func EmittedPublicColumnNames(values []explorer.EmittedColumn) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.PublicColumn)
	}
	return names
}
