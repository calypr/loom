package recipe

import (
	"fmt"
	"strings"
)

func validateDynamicColumns(items []DynamicColumn, path string, budget *int) error {
	seen := map[string]bool{}
	for index, dynamic := range items {
		dp := fmt.Sprintf("%s[%d]", path, index)
		if err := validateRecipeName(dynamic.Name, dp+".name"); err != nil {
			return err
		}
		if dynamic.ColumnPrefix != nil && *dynamic.ColumnPrefix != "" {
			if err := validateRecipeName(*dynamic.ColumnPrefix, dp+".columnPrefix"); err != nil {
				return err
			}
		}
		if seen[dynamic.Name] {
			return validationError("duplicate_name", dp+".name", "duplicate dynamic column name")
		}
		seen[dynamic.Name] = true
		if dynamic.MaxColumns < 0 {
			return validationError("invalid_limit", dp+".maxColumns", "must not be negative")
		}
		seenColumns := map[string]bool{}
		for columnIndex, column := range dynamic.Columns {
			if strings.TrimSpace(column) == "" {
				return validationError("required", fmt.Sprintf("%s.columns[%d]", dp, columnIndex), "column name is required")
			}
			if seenColumns[column] {
				return validationError("duplicate_name", fmt.Sprintf("%s.columns[%d]", dp, columnIndex), "duplicate dynamic column")
			}
			seenColumns[column] = true
		}
		if err := validateExpressionBudget(dynamic.Source, dp+".source", budget); err != nil {
			return err
		}
		if dynamic.Key != nil {
			if err := validateExpressionBudget(*dynamic.Key, dp+".key", budget); err != nil {
				return err
			}
		}
		if dynamic.Value != nil {
			if err := validateExpressionBudget(*dynamic.Value, dp+".value", budget); err != nil {
				return err
			}
		}
	}
	return nil
}
