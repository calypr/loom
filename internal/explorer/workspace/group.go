package workspace

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/columntransform"
)

func validateExplicitGroupRowValues(document Document, values []ExplicitGroupRowValue) error {
	if len(values) == 0 {
		return nil
	}
	columnsByID := make(map[string][]Column, len(document.Columns))
	for _, column := range document.Columns {
		if column.ColumnID != "" {
			columnsByID[column.ColumnID] = append(columnsByID[column.ColumnID], column)
		}
	}
	for index, value := range values {
		columns := columnsByID[value.ColumnID]
		if len(columns) != 1 {
			return fmt.Errorf("rows.groups.rowValues[%d].columnId %q must identify exactly one source column", index, value.ColumnID)
		}
		column := columns[0]
		if !supportsExplicitGroupRowValueColumn(column) {
			return fmt.Errorf("rows.groups.rowValues[%d] supports only a root FHIR field with no transformation or a scalar string exact-category recode", index)
		}
	}
	return nil
}

func supportsExplicitGroupRowValueColumn(column Column) bool {
	if column.OccurrenceID != RootOccurrenceID || column.Source.Kind != SourceField || column.Source.Field == nil {
		return false
	}
	transformation := column.ValueTransformation
	if transformation == nil {
		return true
	}
	if transformation.Kind != columntransform.KindExactCategoryRecode || transformation.Validate() != nil {
		return false
	}
	if logicalType := strings.TrimSpace(column.LogicalType); logicalType != "" && !strings.EqualFold(logicalType, "string") {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(column.Source.Field.ProjectionMode)) {
	case "", "VALUE", "FIRST":
		return true
	default:
		return false
	}
}
