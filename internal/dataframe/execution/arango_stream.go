package execution

import (
	"context"
	"fmt"
	"reflect"

	"github.com/calypr/loom/internal/store/arango"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

type QueryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error

type WithPreviewReadSnapshot func(context.Context, func(context.Context, QueryRows) error) error

type arangoSource struct {
	query                   string
	bindVars                map[string]any
	stream                  QueryRows
	batchSize               int
	rootPageRows            int
	page                    *compiler.CompiledOutputPage
	withPreviewReadSnapshot WithPreviewReadSnapshot
	queryMemoryLimit        int64
	sampledAggregate        bool
	projectionDecisionKey   *groupPreviewRowsKey
	projectionDecision      *groupPreviewProjectionDecision
}

func (s *arangoSource) identityBinds() map[string]any {
	return s.bindVars
}

func (s *arangoSource) rows(ctx context.Context, _ OutputStream, visit func(map[string]any) error) error {
	if s.queryMemoryLimit > 0 {
		ctx = arango.WithQueryMemoryLimit(ctx, s.queryMemoryLimit)
	}
	if s.page == nil || s.rootPageRows == 0 {
		return s.stream(ctx, s.query, s.batchSize, s.bindVars, visit)
	}
	if len(s.page.RowsPartitions) > 0 && s.withPreviewReadSnapshot != nil {
		return s.withPreviewReadSnapshot(ctx, func(snapshotCtx context.Context, query QueryRows) error {
			return s.streamRootPages(snapshotCtx, query, true, visit)
		})
	}
	return s.streamRootPages(ctx, s.stream, false, visit)
}

func (s *arangoSource) streamRootPages(ctx context.Context, query QueryRows, partitioned bool, visit func(map[string]any) error) error {
	after := ""
	pageRows := s.rootPageRows
	for {
		keys := make([]string, 0, pageRows)
		keyBinds := cloneBindVars(s.page.RootKeysBindVars)
		keyBinds[compiler.RootPageAfterKeyBind] = after
		keyBinds[compiler.RootPageSizeBind] = pageRows
		if err := query(ctx, s.page.RootKeysQuery, s.batchSize, keyBinds, func(row map[string]any) error {
			key, ok := row["_key"].(string)
			if !ok || key == "" {
				return fmt.Errorf("root-key page returned an invalid _key")
			}
			if len(keys) != 0 && key <= keys[len(keys)-1] {
				return fmt.Errorf("root-key page is not strictly ordered")
			}
			keys = append(keys, key)
			return nil
		}); err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		if partitioned {
			rows, err := previewProjectionRows(ctx, query, s.page.RowsPartitions, keys, s.batchSize)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := visit(row); err != nil {
					return err
				}
			}
		} else {
			rowBinds := cloneBindVars(s.page.RowsBindVars)
			rowBinds[compiler.RootPageKeysBind] = keys
			if err := query(ctx, s.page.RowsQuery, s.batchSize, rowBinds, visit); err != nil {
				return err
			}
		}
		if s.sampledAggregate {
			return nil
		}
		after = keys[len(keys)-1]
		if len(keys) < pageRows {
			return nil
		}
	}
}

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
