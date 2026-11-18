package importedsource

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func importedSchemaFromDocument(document authoringv2.Document) []recipe.StageColumn {
	var schema []recipe.StageColumn
	for _, column := range document.Columns {
		if !isImportedRecipeColumn(column) {
			continue
		}
		schema = append(schema, recipe.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	return schema
}

func rebuildSourceOutput(t *testing.T, fixture unifiedAuthoringFixture) recipe.Output {
	t.Helper()
	output, ok := findUnifiedRecipeOutput(fixture.bundle, importedOutputID)
	if !ok {
		t.Fatalf("fixture has no %q output", importedOutputID)
	}
	return output
}

func rebuildSourceCatalog(fixture unifiedAuthoringFixture) authoringv2.CatalogSnapshot {
	return authoringv2.CatalogFromCapability(fixture.authorized.Snapshot, "")
}

func TestRebuildImportedDocumentPreservesAuthoredColumnsAcrossSchemaChanges(t *testing.T) {
	fixture := newUnifiedAuthoringFixture(t)
	document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
	pinned := importedSchemaFromDocument(document)
	if len(pinned) < 4 {
		t.Fatalf("fixture has only %d pinned columns", len(pinned))
	}
	authoredDirect, authoredCoded := document.Columns[len(document.Columns)-2], document.Columns[len(document.Columns)-1]
	visible, tableOrder, filterOrder, chartOrder := false, 7, 3, 2
	authoredDirect.Table = &authoringv2.TablePresentation{Visible: &visible, Order: &tableOrder, Pinned: true, CellRenderer: "fileActions"}
	authoredDirect.Filter = &authoringv2.FilterPresentation{Label: "Dose filter", Order: &filterOrder}
	authoredDirect.Chart = &authoringv2.ChartPresentation{Type: "bar", Title: "Dose units", Order: &chartOrder}
	authoredDirect.LogicalType = "string"
	authoredCoded.Table = &authoringv2.TablePresentation{Visible: &visible, Pinned: true}
	authoredCoded.Label = "Authored medication category"
	document.Columns = []authoringv2.Column{
		document.Columns[0], authoredDirect, document.Columns[1], document.Columns[2],
		authoredCoded, document.Columns[3], document.Columns[4],
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("interleaved source and authored columns are invalid: %v", err)
	}
	wantAuthored := []authoringv2.Column{authoredDirect, authoredCoded}

	schema := []recipe.StageColumn{
		{ID: pinned[0].ID, Name: "renamed_pinned_column", Label: "Renamed source field", Type: "string"},
		{ID: "new_pinned_column_id", Name: "new_pinned_column", Label: "New pinned field", Type: "boolean"},
		pinned[1], pinned[3], pinned[4],
	}
	updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
	if err != nil {
		t.Fatalf("rebuild changed imported schema: %v", err)
	}
	gotIDs := unifiedColumnIDs(updated.Columns)
	wantIDs := []string{pinned[0].ID, authoredDirect.ColumnID, "new_pinned_column_id", pinned[1].ID, authoredCoded.ColumnID, pinned[3].ID, pinned[4].ID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("merged column order = %v, want %v", gotIDs, wantIDs)
	}
	var gotAuthored []authoringv2.Column
	for _, column := range updated.Columns {
		if column.ColumnID == authoredDirect.ColumnID || column.ColumnID == authoredCoded.ColumnID {
			gotAuthored = append(gotAuthored, column)
		}
	}
	if !reflect.DeepEqual(gotAuthored, wantAuthored) {
		t.Fatalf("authored column metadata changed during schema refresh:\n got: %#v\nwant: %#v", gotAuthored, wantAuthored)
	}
	if got := updated.Columns[0]; got.Column != "renamed_pinned_column" || got.Label != "Renamed source field" || got.Source.Imported == nil || got.Source.Imported.OutputColumn != got.Column {
		t.Fatalf("stable pinned column did not refresh its schema identity: %#v", got)
	}
	for _, column := range updated.Columns {
		if column.ColumnID == pinned[2].ID {
			t.Fatalf("removed pinned schema column %q remained in the candidate", pinned[2].ID)
		}
	}
}

func TestRebuildImportedDocumentRejectsAuthoredIdentityAndNameCollisions(t *testing.T) {
	t.Run("stable id collision", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		document.Columns[len(document.Columns)-2].ColumnID = "new_pinned_column_id"
		inputColumns := append([]authoringv2.Column(nil), document.Columns...)
		schema := append(importedSchemaFromDocument(document), recipe.StageColumn{ID: "new_pinned_column_id", Name: "new_pinned", Label: "New pinned"})
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "collides with an authored column") {
			t.Fatalf("stable ID collision returned document=%#v err=%v", updated, err)
		}
		if !reflect.DeepEqual(document.Columns, inputColumns) {
			t.Fatal("rejected source rebuild mutated the caller's document")
		}
	})

	t.Run("public name collision", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		schema := append(importedSchemaFromDocument(document), recipe.StageColumn{ID: "new_pinned_name_collision", Name: "dose_unit", Label: "Conflicting source name"})
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "duplicate column") {
			t.Fatalf("public name collision returned document=%#v err=%v", updated, err)
		}
	})
}

func TestRebuildImportedDocumentRejectsRemovedAuthoredDependencies(t *testing.T) {
	t.Run("removed route", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		document.Route.Children = []authoringv2.RouteNode{{OccurrenceID: "pinned_child", ResourceType: "Patient", Relationship: "subject"}}
		document.Columns[len(document.Columns)-2].OccurrenceID = "pinned_child"
		if err := document.Validate(); err != nil {
			t.Fatalf("route dependency fixture is invalid: %v", err)
		}
		output := rebuildSourceOutput(t, fixture)
		output.Traversals = nil
		updated, err := rebuildImportedDocument(document, output, importedSchemaFromDocument(document), rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "unknown occurrence") {
			t.Fatalf("removed route returned document=%#v err=%v", updated, err)
		}
	})

	t.Run("removed fixed filter output", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := fixture.workspace.Documents[0]
		if len(document.Columns) == 0 {
			t.Fatal("fixture has no imported columns")
		}
		removed := document.Columns[0]
		document.FixedFilters = []authoringv2.FixedFilter{{Column: removed.Column, Values: []string{"active"}}}
		if err := document.Validate(); err != nil {
			t.Fatalf("fixed filter dependency fixture is invalid: %v", err)
		}
		schema := importedSchemaFromDocument(document)[1:]
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "fixedFilters") {
			t.Fatalf("removed fixed-filter column returned document=%#v err=%v", updated, err)
		}
	})

	t.Run("removed construction input", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		input := document.Columns[0]
		outputs := make([]authoringv2.StageColumn, len(document.Columns))
		for index, column := range document.Columns {
			outputs[index] = authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType}
		}
		document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "retain_source", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
				ColumnID: input.ColumnID, Operator: authoringv2.ConstructionFilterExists,
			}},
			Outputs: outputs,
		}}}
		if err := document.Validate(); err != nil {
			t.Fatalf("construction dependency fixture is invalid: %v", err)
		}
		schema := importedSchemaFromDocument(document)[1:]
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "construction") {
			t.Fatalf("removed construction input returned document=%#v err=%v", updated, err)
		}
	})
}
