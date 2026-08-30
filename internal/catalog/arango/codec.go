package arango

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/catalog"
)

func generation(value string) any {
	value = catalog.NormalizeDatasetGeneration(value)
	if value == "" {
		return nil
	}
	return value
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func decodeInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		return int64(typed), nil
	case float32:
		return int64(typed), nil
	case json.Number:
		n, err := typed.Int64()
		return n, err
	case string:
		n, err := strconv.ParseInt(typed, 10, 64)
		return n, err
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("unsupported numeric type %T", value)
}

func decodeBool(value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("unsupported boolean type %T", value)
	}
	return result, nil
}

func decodeStrings(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		result := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("unsupported slice item type %T at index %d", value, i)
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported string slice type %T", value)
	}
}

func decodeExtensionValues(value any) ([]catalog.ExtensionValueObservation, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			items = make([]any, len(typed))
			for i := range typed {
				items[i] = typed[i]
			}
		} else {
			return nil, fmt.Errorf("unsupported extension observation slice type %T", value)
		}
	}
	result := make([]catalog.ExtensionValueObservation, 0, len(items))
	for i, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unsupported extension observation type %T at index %d", item, i)
		}
		urlPath, err := decodeStrings(row["url_path"])
		if err != nil {
			return nil, fmt.Errorf("extension observation URL path: %w", err)
		}
		result = append(result, catalog.ExtensionValueObservation{URL: stringValue(row["url"]), SourcePath: stringValue(row["source_path"]), ValuePath: stringValue(row["value_path"]), ValueType: stringValue(row["value_type"]), URLPath: urlPath})
	}
	return result, nil
}

func decode(row map[string]any, out *catalog.PopulatedField) error {
	out.Project = stringValue(row["project"])
	out.DatasetGeneration = stringValue(row["dataset_generation"])
	out.AuthResourcePath = stringValue(row["auth_resource_path"])
	out.ResourceType = stringValue(row["resource_type"])
	out.Path = stringValue(row["path"])
	out.Kind = stringValue(row["kind"])
	var err error
	if out.DocCount, err = decodeInt64(row["doc_count"]); err != nil {
		return fmt.Errorf("decode field row %s/%s doc_count: %w", out.ResourceType, out.Path, err)
	}
	var sampleCount int64
	if sampleCount, err = decodeInt64(row["sample_count"]); err != nil {
		return fmt.Errorf("decode field row %s/%s sample_count: %w", out.ResourceType, out.Path, err)
	}
	out.SampleCount = int(sampleCount)
	if out.DistinctTruncated, err = decodeBool(row["distinct_truncated"]); err != nil {
		return fmt.Errorf("decode field row %s/%s distinct_truncated: %w", out.ResourceType, out.Path, err)
	}
	if out.PivotCandidate, err = decodeBool(row["pivot_candidate"]); err != nil {
		return fmt.Errorf("decode field row %s/%s pivot_candidate: %w", out.ResourceType, out.Path, err)
	}
	if out.DistinctValues, err = decodeStrings(row["distinct_values"]); err != nil {
		return fmt.Errorf("decode field row %s/%s distinct_values: %w", out.ResourceType, out.Path, err)
	}
	if out.PivotColumns, err = decodeStrings(row["pivot_columns"]); err != nil {
		return fmt.Errorf("decode field row %s/%s pivot_columns: %w", out.ResourceType, out.Path, err)
	}
	if out.PivotValueSelectors, err = decodeStrings(row["pivot_value_selectors"]); err != nil {
		return fmt.Errorf("decode field row %s/%s pivot_value_selectors: %w", out.ResourceType, out.Path, err)
	}
	if out.ExtensionValues, err = decodeExtensionValues(row["extension_values"]); err != nil {
		return fmt.Errorf("decode field row %s/%s extension_values: %w", out.ResourceType, out.Path, err)
	}
	out.PivotKind = stringValue(row["pivot_kind"])
	out.PivotFamily = stringValue(row["pivot_family"])
	out.PivotColumnSelect = stringValue(row["pivot_column_selector"])
	out.PivotValueSelect = stringValue(row["pivot_value_selector"])
	out.PivotItemSource = stringValue(row["pivot_item_source"])
	out.PivotItemResourceType = stringValue(row["pivot_item_resource_type"])
	return nil
}
