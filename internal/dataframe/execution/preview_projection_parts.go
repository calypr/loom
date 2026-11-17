package execution

import (
	"context"
	"fmt"
	"reflect"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

func previewProjectionRows(ctx context.Context, query QueryRows, partitions []compiler.CompiledOutputPagePartition, keys []string, batchSize int) ([]map[string]any, error) {
	var rows []map[string]any
	var identities []string
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	for partIndex, part := range partitions {
		binds := cloneBindVars(part.RowsBindVars)
		binds[compiler.RootPageKeysBind] = keys
		expected := make(map[string]bool, len(part.ProjectionNames))
		for _, name := range part.ProjectionNames {
			expected[name] = true
		}
		var current []map[string]any
		seen := make(map[string]bool)
		err := query(ctx, part.RowsQuery, batchSize, binds, func(row map[string]any) error {
			if len(expected) != 0 {
				if len(row) != len(expected) {
					return fmt.Errorf("preview projection returned an incomplete row")
				}
				for name := range row {
					if !expected[name] {
						return fmt.Errorf("preview projection returned an unexpected column %q", name)
					}
				}
			}
			key, ok := row["_key"].(string)
			if !ok || key == "" || !allowed[key] || seen[key] {
				return fmt.Errorf("preview projection returned missing or duplicate root identity")
			}
			seen[key] = true
			current = append(current, row)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if partIndex == 0 {
			rows = current
			identities = make([]string, len(rows))
			for i, row := range rows {
				identities[i] = row["_key"].(string)
			}
			continue
		}
		if len(current) != len(rows) {
			return nil, fmt.Errorf("preview projections returned different row counts")
		}
		for i, row := range current {
			if row["_key"] != identities[i] {
				return nil, fmt.Errorf("preview projections returned different row identities or order")
			}
			for name, value := range row {
				if old, exists := rows[i][name]; exists {
					if !reflect.DeepEqual(old, value) {
						return nil, fmt.Errorf("preview projections returned conflicting values for %q", name)
					}
					continue
				}
				rows[i][name] = value
			}
		}
	}
	return rows, nil
}
