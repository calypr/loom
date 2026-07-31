package materialization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

var (
	ErrBundleInFlight  = errors.New("identical bundle execution is already in flight")
	ErrBundleLeaseLost = errors.New("bundle lease ownership was lost")
)

type IdentityBundleStore interface {
	BeginBundleFor(context.Context, BundleIdentity) (AtomicBundleTx, error)
}

// ClickHouseBundleStore publishes staged tables by advancing a durable logical
// pointer in BundleCatalog. ClickHouse itself has no cross-table transaction;
// the pointer is therefore the visibility boundary.
type ClickHouseBundleStore struct {
	ClickHouse         BundleClickHouseStore
	Catalog            BundleCatalog
	Prefix             string
	mu                 sync.Mutex
	leaseTTL           time.Duration
	leaseRenewInterval time.Duration
}

type BundleClickHouseStore interface {
	CreateTable(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	DropTable(context.Context, string) error
}

func NewClickHouseBundleStore(client BundleClickHouseStore, catalog BundleCatalog) (*ClickHouseBundleStore, error) {
	if client == nil || catalog == nil {
		return nil, fmt.Errorf("ClickHouse client and bundle catalog are required")
	}
	return &ClickHouseBundleStore{ClickHouse: client, Catalog: catalog, Prefix: "loom_bundle", leaseTTL: 2 * time.Minute, leaseRenewInterval: 30 * time.Second}, nil
}

func (s *ClickHouseBundleStore) BeginBundleFor(ctx context.Context, identity BundleIdentity) (AtomicBundleTx, error) {
	// ponytail: one process-wide begin lock; catalog leases handle cross-process races, per-key locks if throughput matters.
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(identity.Name) == "" {
		return nil, fmt.Errorf("bundle name is required")
	}
	if identity.EngineVersion == "" {
		identity.EngineVersion = "loom"
	}
	key := identity.Key()
	if existing, err := s.Catalog.FindExecutionByKey(ctx, key); err == nil {
		switch existing.State {
		case BundleReady:
			return &clickHouseBundleTx{store: s, execution: existing, idempotent: true}, nil
		case BundlePending, BundlePreflight, BundleLoading, BundleValidating:
			return nil, dataframeerrors.Wrap(fmt.Errorf("%w: %s", ErrBundleInFlight, existing.ID), dataframeerrors.CodePublicationInProgress, "", dataframeerrors.WithRetryable(true))
		}
	} else if !errors.Is(err, ErrBundleNotFound) {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	leaseUntil := now.Add(s.leaseTTL)
	expectedPointer, err := s.pointer(ctx, identity.PointerName())
	if err != nil {
		return nil, err
	}
	if leaseCatalog, ok := s.Catalog.(BundleLeaseCatalog); ok {
		acquired, err := leaseCatalog.AcquireBundleLease(ctx, key, id, leaseUntil)
		if err != nil {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		if !acquired {
			return nil, dataframeerrors.Wrap(fmt.Errorf("%w: lease for %s", ErrBundleInFlight, key), dataframeerrors.CodePublicationInProgress, "", dataframeerrors.WithRetryable(true))
		}
	}
	execution := BundleExecution{ID: id, Key: key, BundleIdentity: identity, State: BundleLoading, CreatedAt: now, UpdatedAt: now, OwnerID: id, LeaseExpiresAt: &leaseUntil}
	if err := s.Catalog.SaveExecution(ctx, execution); err != nil {
		if releaseErr := s.releaseLease(ctx, key, id); releaseErr != nil {
			return nil, errors.Join(err, releaseErr)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	tx := &clickHouseBundleTx{store: s, execution: execution, expectedPointer: expectedPointer}
	if _, ok := s.Catalog.(BundleLeaseCatalog); ok {
		tx.startLeaseRenewal(ctx)
	}
	return tx, nil
}

func (s *ClickHouseBundleStore) pointer(ctx context.Context, name string) (string, error) {
	p, err := s.Catalog.GetPointer(ctx, name)
	if err != nil {
		if errors.Is(err, ErrBundleNotFound) {
			return "", nil
		}
		return "", dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return p.ExecutionID, nil
}

func (s *ClickHouseBundleStore) releaseLease(ctx context.Context, key, owner string) error {
	lease, ok := s.Catalog.(BundleLeaseCatalog)
	if !ok {
		return nil
	}
	if err := lease.ReleaseBundleLease(ctx, key, owner); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return nil
}

type clickHouseBundleTx struct {
	store           *ClickHouseBundleStore
	execution       BundleExecution
	expectedPointer string
	idempotent      bool
	closed          bool
	leaseLost       bool
	leaseErr        error
	leaseCancel     context.CancelFunc
	leaseDone       chan struct{}
	leaseMu         sync.RWMutex
	leaseStopOnce   sync.Once
	leaseStopErr    error
}

func (t *clickHouseBundleTx) startLeaseRenewal(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	t.leaseCancel, t.leaseDone = cancel, make(chan struct{})
	go func() {
		defer close(t.leaseDone)
		interval := t.store.leaseRenewInterval
		if interval <= 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				until := time.Now().UTC().Add(t.store.leaseTTL)
				lease, ok := t.store.Catalog.(BundleLeaseCatalog)
				if !ok {
					return
				}
				acquired, err := lease.RenewBundleLease(context.Background(), t.execution.Key, t.execution.OwnerID, until)
				t.leaseMu.Lock()
				if err != nil || !acquired {
					t.leaseLost = true
					if err != nil {
						t.leaseErr = err
					} else {
						t.leaseErr = ErrBundleLeaseLost
					}
					t.leaseMu.Unlock()
					return
				}
				t.leaseMu.Lock()
				t.execution.LeaseExpiresAt = &until
				t.leaseMu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (t *clickHouseBundleTx) ensureLease() error {
	t.leaseMu.Lock()
	defer t.leaseMu.Unlock()
	if t.leaseLost {
		if t.leaseErr != nil {
			return dataframeerrors.Wrap(errors.Join(ErrBundleLeaseLost, t.leaseErr), dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true))
		}
		return dataframeerrors.NewError(dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true), dataframeerrors.WithCause(ErrBundleLeaseLost))
	}
	if t.execution.OwnerID == "" {
		return nil
	}
	if expires := t.execution.LeaseExpiresAt; expires != nil && !time.Now().UTC().Before(*expires) {
		t.leaseLost = true
		return dataframeerrors.NewError(dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true), dataframeerrors.WithCause(ErrBundleLeaseLost))
	}
	return nil
}

func (t *clickHouseBundleTx) stopLease() error {
	t.leaseStopOnce.Do(func() {
		if t.leaseCancel != nil {
			t.leaseCancel()
			<-t.leaseDone
		}
		if t.execution.OwnerID != "" {
			t.leaseStopErr = t.store.releaseLease(context.Background(), t.execution.Key, t.execution.OwnerID)
		}
	})
	return t.leaseStopErr
}

func (t *clickHouseBundleTx) CreateOutput(ctx context.Context, name string, columns []clickhouse.Column) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	if !validBundleOutput(name) {
		return fmt.Errorf("invalid bundle output name %q", name)
	}
	for _, output := range t.execution.Outputs {
		if output.Name == name {
			return fmt.Errorf("bundle output %q is duplicated", name)
		}
	}
	if len(columns) == 0 {
		return fmt.Errorf("bundle output %q has no columns", name)
	}
	columns = withRowIdentityColumn(columns)
	for _, c := range columns {
		if c.Name == "__loom_row_id" {
			continue
		}
		if err := validateSchemaColumn(Column{Name: c.Name, ClickHouse: c.Type}); err != nil {
			return err
		}
	}
	table := t.tableName(name)
	if err := t.store.ClickHouse.CreateTable(ctx, table, columns); err != nil {
		return err
	}
	converted := make([]Column, len(columns))
	for i, c := range columns {
		converted[i] = Column{Name: c.Name, ClickHouse: c.Type}
	}
	t.execution.Outputs = append(t.execution.Outputs, BundleOutputRecord{Name: name, PhysicalTable: table, Columns: converted, State: BundleLoading})
	return t.save(ctx)
}

func (t *clickHouseBundleTx) InsertRows(ctx context.Context, name string, columns []clickhouse.Column, rows []map[string]any) error {
	if t.idempotent || len(rows) == 0 {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	idx := t.outputIndex(name)
	if idx < 0 {
		return fmt.Errorf("bundle output %q was not created", name)
	}
	record := &t.execution.Outputs[idx]
	effectiveColumns := columns
	if len(columns)+1 == len(record.Columns) && record.Columns[0].Name == "__loom_row_id" {
		effectiveColumns = make([]clickhouse.Column, 0, len(columns)+1)
		effectiveColumns = append(effectiveColumns, clickhouse.Column{Name: "__loom_row_id", Type: record.Columns[0].ClickHouse})
		effectiveColumns = append(effectiveColumns, columns...)
		rows = addGeneratedRowIdentities(rows, record.RowCount)
	}
	if len(effectiveColumns) != len(record.Columns) {
		return fmt.Errorf("output %q schema changed after preflight", name)
	}
	for i, c := range effectiveColumns {
		if record.Columns[i].Name != c.Name || record.Columns[i].ClickHouse != c.Type {
			return fmt.Errorf("output %q schema changed after preflight", name)
		}
	}
	if err := t.store.ClickHouse.InsertRows(ctx, record.PhysicalTable, effectiveColumns, rows); err != nil {
		return err
	}
	record.RowCount += int64(len(rows))
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		record.ByteCount += int64(len(encoded))
	}
	return t.save(ctx)
}

func withRowIdentityColumn(columns []clickhouse.Column) []clickhouse.Column {
	for _, column := range columns {
		if column.Name == "__loom_row_id" {
			return append([]clickhouse.Column(nil), columns...)
		}
	}
	result := make([]clickhouse.Column, 0, len(columns)+1)
	result = append(result, clickhouse.Column{Name: "__loom_row_id", Type: "String"})
	return append(result, columns...)
}

func addGeneratedRowIdentities(rows []map[string]any, start int64) []map[string]any {
	result := make([]map[string]any, len(rows))
	for index, row := range rows {
		copy := cloneBundleRow(row)
		if _, ok := copy["__loom_row_id"]; !ok {
			copy["__loom_row_id"] = fmt.Sprintf("%d", start+int64(index))
		}
		result[index] = copy
	}
	return result
}

func cloneBundleRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}

func (t *clickHouseBundleTx) Commit(ctx context.Context) error {
	if t.idempotent {
		t.closed = true
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	if len(t.execution.Outputs) == 0 {
		return t.fail(ctx, fmt.Errorf("bundle has no outputs"))
	}
	t.execution.State = BundleValidating
	if err := t.save(ctx); err != nil {
		return err
	}
	for i := range t.execution.Outputs {
		if t.execution.Outputs[i].RowCount < 0 {
			return t.fail(ctx, fmt.Errorf("output %q has invalid row count", t.execution.Outputs[i].Name))
		}
		t.execution.Outputs[i].State = BundleReady
	}
	if err := t.save(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	t.execution.State, t.execution.ReadyAt, t.execution.UpdatedAt = BundleReady, &now, now
	if err := t.save(ctx); err != nil {
		return err
	}
	if err := t.store.Catalog.CompareAndSwapPointer(ctx, t.execution.PointerName(), t.expectedPointer, t.execution.ID); err != nil {
		if errors.Is(err, ErrBundlePointerConflict) {
			err = dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "", dataframeerrors.WithRetryable(true))
		} else {
			err = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		return t.fail(ctx, fmt.Errorf("publish bundle pointer: %w", err))
	}
	t.closed = true
	return t.stopLease()
}

func (t *clickHouseBundleTx) Rollback(ctx context.Context) error {
	return t.Abort(ctx, fmt.Errorf("bundle rolled back"))
}

func (t *clickHouseBundleTx) Abort(ctx context.Context, cause error) error {
	if t.idempotent || t.closed {
		return nil
	}
	if err := t.ensureLease(); err != nil {
		t.closed = true
		return errors.Join(err, t.stopLease())
	}
	var cleanup error
	for _, output := range t.execution.Outputs {
		if err := t.store.ClickHouse.DropTable(context.Background(), output.PhysicalTable); err != nil {
			cleanup = errors.Join(cleanup, err)
		}
	}
	if err := t.fail(ctx, cause); err != nil {
		cleanup = errors.Join(cleanup, err)
	}
	t.closed = true
	return errors.Join(cleanup, t.stopLease())
}

func (t *clickHouseBundleTx) outputIndex(name string) int {
	for i := range t.execution.Outputs {
		if t.execution.Outputs[i].Name == name {
			return i
		}
	}
	return -1
}

func (t *clickHouseBundleTx) tableName(output string) string {
	return t.store.Prefix + "_" + strings.ReplaceAll(t.execution.ID, "-", "") + "_" + output
}

func (t *clickHouseBundleTx) save(ctx context.Context) error {
	if err := t.ensureLease(); err != nil {
		return err
	}
	t.leaseMu.Lock()
	t.execution.UpdatedAt = time.Now().UTC()
	snapshot := t.execution
	if t.execution.LeaseExpiresAt != nil {
		expires := *t.execution.LeaseExpiresAt
		snapshot.LeaseExpiresAt = &expires
	}
	t.leaseMu.Unlock()
	if err := t.store.Catalog.SaveExecution(ctx, snapshot); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return nil
}

func (t *clickHouseBundleTx) fail(ctx context.Context, err error) error {
	normalized := dataframeerrors.Normalize(err)
	t.execution.State, t.execution.Error = BundleFailed, err.Error()
	t.execution.FailureCode, t.execution.FailureRetryable = normalized.Code(), normalized.Retryable()
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = BundleFailed
		t.execution.Outputs[i].FailureCode = normalized.Code()
		t.execution.Outputs[i].FailureRetryable = normalized.Retryable()
	}
	if persistenceErr := t.save(ctx); persistenceErr != nil {
		return errors.Join(err, persistenceErr)
	}
	return err
}

var bundleOutputRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validBundleOutput(value string) bool { return bundleOutputRE.MatchString(value) }

// Reconcile marks abandoned executions failed and removes their staging
// tables. It is safe to call repeatedly during server startup; READY pointers
// are never touched.
func (s *ClickHouseBundleStore) Reconcile(ctx context.Context, olderThan time.Time) error {
	catalog, ok := s.Catalog.(StaleBundleCatalog)
	if !ok {
		return fmt.Errorf("bundle catalog does not support stale execution listing")
	}
	reconcilerID := "reconciler-" + uuid.NewString()
	for _, state := range []BundleState{BundlePending, BundlePreflight, BundleLoading, BundleValidating} {
		executions, err := catalog.ListExecutions(ctx, state, olderThan)
		if err != nil {
			return err
		}
		for _, execution := range executions {
			if leases, ok := s.Catalog.(BundleLeaseCatalog); ok {
				expires := time.Now().UTC().Add(s.leaseTTL)
				claimed, err := leases.AcquireBundleLease(ctx, execution.Key, reconcilerID, expires)
				if err != nil {
					return err
				}
				if !claimed {
					continue
				}
				execution.OwnerID = reconcilerID
			}
			var first error
			for _, output := range execution.Outputs {
				if err := s.ClickHouse.DropTable(context.Background(), output.PhysicalTable); err != nil {
					first = errors.Join(first, err)
				}
			}
			execution.State = BundleFailed
			execution.Error = "stale execution reconciled"
			execution.FailureCode = string(dataframeerrors.CodePublicationLeaseLost)
			execution.FailureRetryable = true
			execution.UpdatedAt = time.Now().UTC()
			execution.OwnerID = ""
			execution.LeaseExpiresAt = nil
			if err := s.Catalog.SaveExecution(ctx, execution); err != nil {
				first = errors.Join(first, err)
			}
			if leases, ok := s.Catalog.(BundleLeaseCatalog); ok {
				first = errors.Join(first, leases.ReleaseBundleLease(context.Background(), execution.Key, reconcilerID))
			}
			if first != nil {
				return first
			}
		}
	}
	return nil
}
