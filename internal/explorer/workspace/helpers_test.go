package workspace

import (
	"encoding/json"
)

func workspaceDocument(id string) Document {
	visible := true
	return Document{Kind: Kind, Output: Output{ID: id, Title: id}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Rows: RecordsRowDefinition(), Columns: []Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &TablePresentation{Visible: &visible}}}}
}

func stagedConstructionDocument() Document {
	document := workspaceDocument("patients")
	document.Columns = []Column{
		constructionSourceColumn("person_id", "person_id", "Person ID", "integer"),
		constructionSourceColumn("group_id", "group_id", "Group", "string"),
		constructionSourceColumn("category_id", "category", "Category", "string"),
		constructionSourceColumn("value_id", "value", "Value", "integer"),
	}
	group := StageColumn{ID: "group_id", Name: "group_id", Label: "Group", Type: "string"}
	categoryA := StageColumn{ID: "pivot_a", Name: "amount_a", Label: "Amount A", Type: "integer"}
	categoryB := StageColumn{ID: "pivot_b", Name: "amount_b", Label: "Amount B", Type: "integer"}
	pivotOutputs := []StageColumn{group, categoryA, categoryB}
	quotient := StageColumn{ID: "ratio_id", Name: "ratio", Label: "Ratio", Type: "decimal"}
	derivedOutputs := append(append([]StageColumn(nil), pivotOutputs...), quotient)
	filterValue := int64(1)
	filterOutputs := append([]StageColumn(nil), derivedOutputs...)
	finalOutputs := []StageColumn{
		group,
		quotient,
		{ID: "measure_key", Name: "measure", Label: "Measure"},
		{ID: "measure_value", Name: "measure_value", Label: "Measure value", Type: "integer"},
	}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{
		{
			ID:     "pivot_step",
			Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
				ConstructionID: "pivot_step", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "value_id",
				Categories: []ConstructionPivotCategory{
					{Key: stringScalar("A"), OutputColumnID: "pivot_a"},
					{Key: stringScalar("B"), OutputColumnID: "pivot_b"},
				},
				DuplicatePolicy: ConstructionPivotDuplicateSum, MissingCellPolicy: ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ConstructionPivotUnlistedError,
			}},
			Outputs: pivotOutputs,
		},
		{
			ID: "derive_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "pivot_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{
				ConstructionID: "derive_step", OutputColumnID: "ratio_id", Operation: ConstructionDerivedDivide,
				Left:               ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: "pivot_a"},
				Right:              ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: "pivot_b"},
				MissingInputPolicy: ConstructionMissingInputPropagateNull, DivisionByZeroPolicy: ConstructionDivisionByZeroNull,
			}}, Outputs: derivedOutputs,
		},
		{
			ID: "filter_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{
				ColumnID: "ratio_id", Operator: ConstructionFilterGreaterEq,
				Values: []FilterValue{{Kind: ConstructionFilterInteger, Integer: &filterValue}},
			}}, Outputs: filterOutputs,
		},
		{
			ID: "unpivot_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "filter_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationUnpivot, Unpivot: &ConstructionUnpivot{
				ConstructionID: "unpivot_step",
				Inputs: []ConstructionUnpivotInput{
					{ColumnID: "pivot_a", Key: stringScalar("A")},
					{ColumnID: "pivot_b", Key: stringScalar("B")},
				},
				KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ConstructionUnpivotPreserve,
			}}, Outputs: finalOutputs,
		},
	}}
	return document
}

func constructionSourceColumn(id, name, label, logicalType string) Column {
	return Column{ColumnID: id, Column: name, Label: label, LogicalType: logicalType, OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}}
}

func stringScalar(value string) TableScalar {
	return TableScalar{Kind: TableScalarString, String: &value}
}

func constructionWorkspace(document Document) Workspace {
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion,
		Explorer:  ExplorerMetadata{Title: "Construction test"},
		Documents: []Document{document},
		Tabs:      []Tab{{ID: "tab-patients", Title: "Patients", OutputID: document.Output.ID, Order: 0, Visible: true}},
	}
}

func legacyPivotDocument() Document {
	document := stagedConstructionDocument()
	for i := range document.Columns {
		document.Columns[i].ColumnID = ""
	}
	document.Construction = nil
	document.ArchiveRetiredShape = &storedTableShape{
		Reshape: &storedTableReshape{Kind: "PIVOT", Pivot: &storedPivotConstruction{
			ConstructionID: "legacy_pivot", GroupKeys: []string{"group_id"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []storedPivotCategory{
				{Key: stringScalar("A"), Output: ColumnOutput{Column: "amount_a", Label: "Amount A"}},
				{Key: stringScalar("B"), Output: ColumnOutput{Column: "amount_b", Label: "Amount B"}},
			},
			DuplicatePolicy: "MAX", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
		}},
		Derived: []storedDerivedConstruction{
			{
				ConstructionID: "legacy_ratio", Output: ColumnOutput{Column: "ratio", Label: "Ratio"}, Operation: "ADD",
				Left: storedArithmeticOperand{Kind: "COLUMN", Column: "twice_a"}, Right: storedArithmeticOperand{Kind: "COLUMN", Column: "amount_b"},
				MissingInputPolicy: "PROPAGATE_NULL",
			},
			{
				ConstructionID: "legacy_twice", Output: ColumnOutput{Column: "twice_a", Label: "Twice A"}, Operation: "MULTIPLY",
				Left:               storedArithmeticOperand{Kind: "COLUMN", Column: "amount_a"},
				Right:              storedArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: TableScalarInteger, Integer: int64Pointer(2)}},
				MissingInputPolicy: "PROPAGATE_NULL",
			},
		},
	}
	return document
}

func commandCatalog() CatalogSnapshot {
	catalog := testCatalog()
	for index := range catalog.Candidates {
		catalog.Candidates[index].ProjectionModes = []string{"VALUE"}
		catalog.Candidates[index].DefaultProjectionMode = "VALUE"
		catalog.Candidates[index].FieldPath = "id"
	}
	return catalog
}

func stringPtr(value string) *string { return &value }

func int64Pointer(value int64) *int64 { return &value }

func migrateStoredDocumentFixture(document Document) (Document, error) {
	if document.ArchiveRetiredShape == nil {
		return StartDocumentConstruction(document)
	}
	raw, err := json.Marshal(constructionWorkspace(document))
	if err != nil {
		return Document{}, err
	}
	result, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		return Document{}, err
	}
	return result.Workspace.Documents[0], nil
}

func cloneStoredShapeFixture(shape *storedTableShape) (*storedTableShape, error) {
	raw, err := json.Marshal(shape)
	if err != nil {
		return nil, err
	}
	var out *storedTableShape
	err = json.Unmarshal(raw, &out)
	return out, err
}

func storedDigestFixture(workspace Workspace) (string, error) {
	raw, err := CanonicalStoredWorkspaceForMigration(workspace)
	if err != nil {
		return "", err
	}
	return workspaceDigest(raw), nil
}
