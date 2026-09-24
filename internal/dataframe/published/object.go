package published

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/objectvalue"
)

func decodeObjectColumns(columns []Column, row map[string]any) error {
	if err := validateObjectColumns(columns); err != nil {
		return err
	}
	for _, column := range columns {
		if !strings.EqualFold(strings.TrimSpace(column.LogicalType), "object") {
			continue
		}
		typ := strings.TrimSpace(column.ClickHouse)
		switch typ {
		case "String", "Nullable(String)":
			value, present := row[column.Name]
			if !present || value == nil {
				delete(row, column.Name)
				continue
			}
			text, err := objectText(value)
			if err != nil {
				return fmt.Errorf("logical object column %q has invalid %s value: %w", column.Name, typ, err)
			}
			decoded, err := objectvalue.Decode(objectShape(column), text)
			if err != nil {
				return fmt.Errorf("logical object column %q: %w", column.Name, err)
			}
			row[column.Name] = decoded
		case "JSON", "Nullable(JSON)", "Array(JSON)":
			if value, present := row[column.Name]; !present || value == nil {
				delete(row, column.Name)
			}
		default:
			return fmt.Errorf("logical object column %q has unsupported physical type %q", column.Name, typ)
		}
	}
	return nil
}

func validateObjectColumns(columns []Column) error {
	for _, column := range columns {
		if !isObjectColumn(column) {
			continue
		}
		switch strings.TrimSpace(column.ClickHouse) {
		case "String", "Nullable(String)", "JSON", "Nullable(JSON)", "Array(JSON)":
		default:
			return fmt.Errorf("logical object column %q has unsupported physical type %q", column.Name, column.ClickHouse)
		}
	}
	return nil
}

func objectShape(column Column) objectvalue.Shape {
	if column.Repeated {
		return objectvalue.RepeatedObjects
	}
	return objectvalue.ScalarObject
}

func objectText(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", fmt.Errorf("expected JSON text, got %T", value)
	}
}

func isObjectColumn(column Column) bool {
	return strings.EqualFold(strings.TrimSpace(column.LogicalType), "object")
}
