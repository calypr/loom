package catalog

import (
	"context"
	"fmt"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

const populatedFieldsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @resource_type == null OR d.resource_type == @resource_type
  FILTER @pivot_only == false OR d.pivot_candidate == true
  SORT d.resource_type, d.doc_count DESC, d.path
  RETURN {
    project: d.project,
    dataset_generation: d.dataset_generation,
    auth_resource_path: d.auth_resource_path,
    resource_type: d.resource_type,
    path: d.path,
    kind: d.kind,
    doc_count: d.doc_count,
    sample_count: d.sample_count,
    distinct_values: d.distinct_values,
    distinct_truncated: d.distinct_truncated,
    pivot_candidate: d.pivot_candidate,
    pivot_kind: d.pivot_kind,
    pivot_columns: d.pivot_columns,
    pivot_family: d.pivot_family,
    pivot_column_selector: d.pivot_column_selector,
    pivot_value_selector: d.pivot_value_selector,
    pivot_item_source: d.pivot_item_source,
    pivot_item_resource_type: d.pivot_item_resource_type,
    pivot_value_selectors: d.pivot_value_selectors
  }
`

func DiscoverPopulatedFields(ctx context.Context, opts PopulatedFieldOptions) ([]PopulatedField, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return nil, err
	}
	defer client.Close(ctx)

	start := time.Now()
	emit("go_discovery_start", map[string]any{
		"database":            opts.Database,
		"project":             opts.Project,
		"dataset_generation":  DatasetGenerationBindValue(opts.DatasetGeneration),
		"resource_type":       opts.ResourceType,
		"pivot_only":          opts.PivotOnly,
		"auth_resource_paths": opts.AuthResourcePaths,
		"cursor_batch_size":   opts.CursorBatch,
		"query":               "populated_fields",
	})

	bindVars := populatedFieldsBindVars(opts)

	results := make([]PopulatedField, 0, 64)
	err = client.QueryRows(ctx, populatedFieldsAQL, opts.CursorBatch, bindVars, func(row map[string]any) error {
		field, err := populatedFieldFromRow(row)
		if err != nil {
			return err
		}
		results = append(results, field)
		return nil
	})
	if err != nil {
		return nil, err
	}

	emit("go_discovery_complete", map[string]any{
		"database":            opts.Database,
		"project":             opts.Project,
		"dataset_generation":  DatasetGenerationBindValue(opts.DatasetGeneration),
		"resource_type":       opts.ResourceType,
		"pivot_only":          opts.PivotOnly,
		"auth_resource_paths": opts.AuthResourcePaths,
		"rows":                len(results),
		"seconds":             secondsSince(start),
	})
	return results, nil
}

func populatedFieldFromRow(row map[string]any) (PopulatedField, error) {
	field := PopulatedField{
		Project:               stringValue(row["project"]),
		DatasetGeneration:     stringValue(row["dataset_generation"]),
		AuthResourcePath:      stringValue(row["auth_resource_path"]),
		ResourceType:          stringValue(row["resource_type"]),
		Path:                  stringValue(row["path"]),
		Kind:                  stringValue(row["kind"]),
		PivotKind:             stringValue(row["pivot_kind"]),
		PivotFamily:           stringValue(row["pivot_family"]),
		PivotColumnSelect:     stringValue(row["pivot_column_selector"]),
		PivotValueSelect:      stringValue(row["pivot_value_selector"]),
		PivotItemSource:       stringValue(row["pivot_item_source"]),
		PivotItemResourceType: stringValue(row["pivot_item_resource_type"]),
	}
	decode := func(name string, fn func() error) error {
		if err := fn(); err != nil {
			return fmt.Errorf("decode field row %s/%s %s: %w", field.ResourceType, field.Path, name, err)
		}
		return nil
	}
	if err := decode("doc_count", func() error {
		var err error
		field.DocCount, err = strictInt64Value(row["doc_count"])
		return err
	}); err != nil {
		return PopulatedField{}, err
	}
	if err := decode("sample_count", func() error {
		value, err := strictInt64Value(row["sample_count"])
		field.SampleCount = int(value)
		return err
	}); err != nil {
		return PopulatedField{}, err
	}
	for name, target := range map[string]*[]string{
		"distinct_values":       &field.DistinctValues,
		"pivot_columns":         &field.PivotColumns,
		"pivot_value_selectors": &field.PivotValueSelectors,
	} {
		name, target := name, target
		if err := decode(name, func() error {
			var err error
			*target, err = strictStringSliceValue(row[name])
			return err
		}); err != nil {
			return PopulatedField{}, err
		}
	}
	for name, target := range map[string]*bool{
		"distinct_truncated": &field.DistinctTruncated,
		"pivot_candidate":    &field.PivotCandidate,
	} {
		name, target := name, target
		if err := decode(name, func() error {
			var err error
			*target, err = strictBoolValue(row[name])
			return err
		}); err != nil {
			return PopulatedField{}, err
		}
	}
	return field, nil
}

func populatedFieldsBindVars(opts PopulatedFieldOptions) map[string]any {
	bindVars := map[string]any{
		"project":                          opts.Project,
		"dataset_generation":               DatasetGenerationBindValue(opts.DatasetGeneration),
		"pivot_only":                       opts.PivotOnly,
		"auth_resource_paths":              cloneStrings(opts.AuthResourcePaths),
		"auth_resource_paths_unrestricted": EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted),
	}
	if opts.ResourceType != "" {
		bindVars["resource_type"] = opts.ResourceType
	} else {
		bindVars["resource_type"] = nil
	}
	return bindVars
}
