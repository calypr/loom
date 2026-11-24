package clickhouse

import (
	"context"
	"fmt"
	"sync"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/lease"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type clickHouseBundleTx struct {
	store               *ClickHouseBundleStore
	execution           publication.BundleExecution
	expectedPointer     string
	columns             map[string][]clickhouse.Column
	idempotent          bool
	closed              bool
	leaseLost           bool
	leaseErr            error
	leaseRenewal        *lease.Renewal
	leaseMu             sync.RWMutex
	leaseStopOnce       sync.Once
	leaseStopErr        error
	checkpointUncertain bool
}

func (t *clickHouseBundleTx) Idempotent() bool { return t.idempotent }

func (t *clickHouseBundleTx) ExistingPublishedOutputs() []publication.PublishedOutput {
	result := make([]publication.PublishedOutput, 0, len(t.execution.Outputs))
	for _, output := range t.execution.Outputs {
		result = append(result, publication.PublishedOutput{Name: output.Name, PhysicalName: output.PhysicalTable, RowCount: output.RowCount, ByteCount: output.ByteCount})
	}
	return result
}

func (t *clickHouseBundleTx) ExistingQualityReports() []publication.QualityReport {
	return publication.CloneQualityReports(t.execution.QualityReports)
}

func (t *clickHouseBundleTx) SetQualityReports(ctx context.Context, reports []publication.QualityReport) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("ClickHouse publication transaction is closed")
	}
	if err := publication.ValidateQualityReportBindings(t.execution.BundleIdentity, t.execution.Outputs, reports); err != nil {
		return err
	}
	t.execution.QualityReports = publication.CloneQualityReports(reports)
	return t.save(ctx)
}

func (t *clickHouseBundleTx) WriteBatch(ctx context.Context, output string, rows []map[string]any) error {
	if t.checkpointUncertain {
		return ErrBundleCheckpointUncertain
	}
	if t.closed {
		return fmt.Errorf("ClickHouse publication transaction is closed")
	}
	columns, ok := t.columns[output]
	if !ok {
		return fmt.Errorf("output %q was not declared", output)
	}
	return t.InsertRows(ctx, output, columns, rows)
}
