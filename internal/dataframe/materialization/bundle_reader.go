package materialization

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/store/clickhouse"
)

// BundleQueryStore is the read-only subset required to resolve logical bundle
// outputs. Physical table names never enter this API from a caller.
type BundleQueryStore interface {
	QueryRows(context.Context, string, []string) ([]map[string]any, error)
}

type BundleReader struct {
	ClickHouse BundleQueryStore
	Catalog    BundleCatalog
}

func (r *BundleReader) ResolveOutput(ctx context.Context, bundleName, outputName string) (BundleExecution, BundleOutputRecord, error) {
	if r.Catalog == nil {
		return BundleExecution{}, BundleOutputRecord{}, fmt.Errorf("bundle catalog is required")
	}
	pointer, err := r.Catalog.GetPointer(ctx, bundleName)
	if err != nil {
		return BundleExecution{}, BundleOutputRecord{}, err
	}
	execution, err := r.Catalog.GetExecution(ctx, pointer.ExecutionID)
	if err != nil {
		return BundleExecution{}, BundleOutputRecord{}, err
	}
	if execution.State != BundleReady {
		return BundleExecution{}, BundleOutputRecord{}, fmt.Errorf("bundle %q is not ready", bundleName)
	}
	for _, output := range execution.Outputs {
		if output.Name == outputName {
			if output.State != BundleReady {
				return BundleExecution{}, BundleOutputRecord{}, fmt.Errorf("bundle output %q is not ready", outputName)
			}
			return execution, output, nil
		}
	}
	return BundleExecution{}, BundleOutputRecord{}, fmt.Errorf("bundle output %q not found", outputName)
}

func (r *BundleReader) Rows(ctx context.Context, bundleName, outputName string, columns []string) ([]map[string]any, error) {
	if r.ClickHouse == nil {
		return nil, fmt.Errorf("ClickHouse reader is required")
	}
	_, output, err := r.ResolveOutput(ctx, bundleName, outputName)
	if err != nil {
		return nil, err
	}
	if !validBundleOutput(output.PhysicalTable) {
		return nil, fmt.Errorf("invalid persisted bundle table name")
	}
	allowed := make(map[string]struct{}, len(output.Columns))
	for _, column := range output.Columns {
		allowed[column.Name] = struct{}{}
	}
	if len(columns) == 0 {
		columns = make([]string, 0, len(output.Columns))
		for _, column := range output.Columns {
			columns = append(columns, column.Name)
		}
	}
	quoted := make([]string, len(columns))
	for i, column := range columns {
		if _, ok := allowed[column]; !ok || !validBundleOutput(column) {
			return nil, fmt.Errorf("column %q is not in bundle output schema", column)
		}
		quoted[i] = "`" + column + "`"
	}
	query := fmt.Sprintf("SELECT %s FROM `%s`", strings.Join(quoted, ", "), output.PhysicalTable)
	return r.ClickHouse.QueryRows(ctx, query, columns)
}

var _ BundleQueryStore = (*clickhouse.Client)(nil)
