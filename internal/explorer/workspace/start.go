package workspace

import "fmt"

func StartDocumentConstruction(document Document) (Document, error) {
	if document.ArchiveRetiredShape != nil {
		return document, fmt.Errorf("tableShape requires explicit stored-data migration")
	}
	started := document
	if document.Columns != nil {
		started.Columns = append([]Column{}, document.Columns...)
	}
	if document.Construction != nil {
		return started, nil
	}
	for index := range started.Columns {
		if started.Columns[index].ColumnID == "" {
			started.Columns[index].ColumnID = stableConstructionID("source", document.Output.ID, started.Columns[index].Column)
		}
	}
	if _, err := sourceStageColumns(started.Columns); err != nil {
		return document, fmt.Errorf("prepare source projection: %w", err)
	}
	started.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{}}
	if err := started.Validate(); err != nil {
		return document, fmt.Errorf("initialize construction: %w", err)
	}
	return started, nil
}
