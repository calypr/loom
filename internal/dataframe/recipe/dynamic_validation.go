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
		if !dynamic.ColumnMode.Valid() {
			return validationError("invalid_column_mode", dp+".columnMode", "must be DISCOVER or SELECTED")
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

func validateExtensionColumns(items []ExtensionColumn, path string, budget *int) error {
	seen := map[string]bool{}
	for index, item := range items {
		p := fmt.Sprintf("%s[%d]", path, index)
		if err := validateRecipeName(item.Name, p+".name"); err != nil {
			return err
		}
		if seen[item.Name] {
			return validationError("duplicate_name", p+".name", "duplicate extension column name")
		}
		seen[item.Name] = true
		if item.ColumnPrefix != nil && *item.ColumnPrefix != "" {
			if err := validateRecipeName(*item.ColumnPrefix, p+".columnPrefix"); err != nil {
				return err
			}
		}
		if item.MaxColumns <= 0 {
			return validationError("invalid_limit", p+".maxColumns", "must be greater than zero")
		}
		if !item.ColumnMode.Valid() {
			return validationError("invalid_column_mode", p+".columnMode", "must be DISCOVER or SELECTED")
		}
		if err := validateExpressionBudget(item.Source, p+".source", budget); err != nil {
			return err
		}
		seenColumns := map[string]bool{}
		for columnIndex, column := range item.Columns {
			cp := fmt.Sprintf("%s.columns[%d]", p, columnIndex)
			if strings.TrimSpace(column.Name) == "" || strings.TrimSpace(column.URL) == "" {
				return validationError("required", cp, "name and url are required")
			}
			if seenColumns[column.Name] {
				return validationError("duplicate_name", cp+".name", "duplicate extension column")
			}
			seenColumns[column.Name] = true
			if strings.TrimSpace(column.ValueType) == "" {
				return validationError("required", cp+".valueType", "valueType is required")
			}
		}
	}
	return nil
}
