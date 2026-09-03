package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

var (
	ErrBundleInFlight        = errors.New("identical bundle execution is already in flight")
	ErrBundleLeaseLost       = errors.New("bundle lease ownership was lost")
	ErrBundleCommitUncertain = errors.New("bundle publication commit outcome is uncertain")
)

// ClickHouseBundleStore publishes staged tables by advancing a durable logical
// pointer in publication.BundleCatalog. ClickHouse itself has no cross-table transaction;
// the pointer is therefore the visibility boundary.
type ClickHouseBundleStore struct {
	clickHouse         BundleClickHouseStore
	catalog            publication.BundleCatalog
	prefix             string
	mu                 sync.Mutex
	leaseTTL           time.Duration
	leaseRenewInterval time.Duration
}

type BundleClickHouseStore interface {
	CreateTable(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	VerifyOutput(context.Context, string, []clickhouse.Column, int64) error
	DropTable(context.Context, string) error
	DropColumns(context.Context, string, []string) error
}

func NewBundleStore(client BundleClickHouseStore, catalog publication.BundleCatalog) (*ClickHouseBundleStore, error) {
	if client == nil || catalog == nil {
		return nil, fmt.Errorf("ClickHouse client and bundle catalog are required")
	}
	return &ClickHouseBundleStore{clickHouse: client, catalog: catalog, prefix: "loom_bundle", leaseTTL: 2 * time.Minute, leaseRenewInterval: 30 * time.Second}, nil
}

var _ publication.Target = (*ClickHouseBundleStore)(nil)
var _ publication.Transaction = (*clickHouseBundleTx)(nil)

// SupportsObjectValues reports the native JSON support of this target.
func (s *ClickHouseBundleStore) SupportsObjectValues() bool { return true }

func (s *ClickHouseBundleStore) Begin(ctx context.Context, identity publication.PublicationIdentity, schemas []publication.OutputSchema) (publication.Transaction, error) {
	bundleIdentity := publication.BundleIdentity{
		Name: identity.Name, TranslationVersion: identity.TranslationVersion, OutputName: identity.OutputName,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest,
		SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
		AuthScopeMode: identity.AuthScopeMode, AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...),
	}
	tx, err := s.beginBundle(ctx, bundleIdentity)
	if err != nil {
		return nil, err
	}
	for _, schema := range schemas {
		columns, err := toColumns(schema.Columns)
		if err != nil {
			cause := fmt.Errorf("output %q schema: %w", schema.Name, err)
			cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
			abortErr := tx.Abort(cleanupCtx, cause)
			cancel()
			return nil, errors.Join(cause, abortErr)
		}
		if err := tx.CreateOutput(ctx, schema.Name, columns); err != nil {
			cause := fmt.Errorf("output %q create: %w", schema.Name, err)
			cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
			abortErr := tx.Abort(cleanupCtx, cause)
			cancel()
			return nil, errors.Join(cause, abortErr)
		}
		if err := tx.SetOutputMetadata(schema.Name, schema.Columns); err != nil {
			cause := fmt.Errorf("output %q metadata: %w", schema.Name, err)
			cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
			abortErr := tx.Abort(cleanupCtx, cause)
			cancel()
			return nil, errors.Join(cause, abortErr)
		}
	}
	return tx, nil
}

func (s *ClickHouseBundleStore) beginBundle(ctx context.Context, identity publication.BundleIdentity) (*clickHouseBundleTx, error) {
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
	if existing, err := s.catalog.FindExecutionByKey(ctx, key); err == nil {
		switch existing.State {
		case publication.BundlePublished, publication.BundleReady:
			return &clickHouseBundleTx{store: s, execution: existing, idempotent: true, columns: make(map[string][]clickhouse.Column)}, nil
		case publication.BundleQueued, publication.BundleRunning, publication.BundlePending, publication.BundlePreflight, publication.BundleLoading, publication.BundleValidating:
			return nil, dataframeerrors.Wrap(
				fmt.Errorf("%w: %s", ErrBundleInFlight, existing.ID),
				dataframeerrors.CodePublicationInProgress,
				"",
				dataframeerrors.WithDetails(map[string]any{"executionId": existing.ID}),
				dataframeerrors.WithRetryable(true),
			)
		}
	} else if !errors.Is(err, publication.ErrBundleNotFound) {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	leaseUntil := now.Add(s.leaseTTL)
	expectedPointer, err := s.pointer(ctx, identity.PointerName())
	if err != nil {
		return nil, err
	}
	acquired, err := s.catalog.AcquireBundleLease(ctx, key, id, leaseUntil)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if !acquired {
		return nil, dataframeerrors.Wrap(fmt.Errorf("%w: lease for %s", ErrBundleInFlight, key), dataframeerrors.CodePublicationInProgress, "", dataframeerrors.WithRetryable(true))
	}
	execution := publication.BundleExecution{ID: id, Key: key, BundleIdentity: identity, State: publication.BundleQueued, CreatedAt: now, UpdatedAt: now, OwnerID: id, LeaseExpiresAt: &leaseUntil}
	if err := s.catalog.SaveExecution(ctx, execution); err != nil {
		if releaseErr := s.releaseLease(ctx, key, id); releaseErr != nil {
			return nil, errors.Join(err, releaseErr)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	execution.State = publication.BundleRunning
	if err := s.catalog.SaveExecution(ctx, execution); err != nil {
		if releaseErr := s.releaseLease(ctx, key, id); releaseErr != nil {
			return nil, errors.Join(err, releaseErr)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	tx := &clickHouseBundleTx{store: s, execution: execution, expectedPointer: expectedPointer}
	tx.startLeaseRenewal(ctx)
	return tx, nil
}

func (s *ClickHouseBundleStore) pointer(ctx context.Context, name string) (string, error) {
	p, err := s.catalog.GetPointer(ctx, name)
	if err != nil {
		if errors.Is(err, publication.ErrBundleNotFound) {
			return "", nil
		}
		return "", dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return p.ExecutionID, nil
}

func (s *ClickHouseBundleStore) releaseLease(ctx context.Context, key, owner string) error {
	if err := s.catalog.ReleaseBundleLease(ctx, key, owner); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return nil
}

type clickHouseBundleTx struct {
	store           *ClickHouseBundleStore
	execution       publication.BundleExecution
	expectedPointer string
	columns         map[string][]clickhouse.Column
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

func (t *clickHouseBundleTx) Idempotent() bool { return t.idempotent }

func (t *clickHouseBundleTx) ExistingPublishedOutputs() []publication.PublishedOutput {
	result := make([]publication.PublishedOutput, 0, len(t.execution.Outputs))
	for _, output := range t.execution.Outputs {
		result = append(result, publication.PublishedOutput{Name: output.Name, PhysicalName: output.PhysicalTable, RowCount: output.RowCount, ByteCount: output.ByteCount})
	}
	return result
}

func (t *clickHouseBundleTx) WriteBatch(ctx context.Context, output string, rows []map[string]any) error {
	if t.closed {
		return fmt.Errorf("ClickHouse publication transaction is closed")
	}
	columns, ok := t.columns[output]
	if !ok {
		return fmt.Errorf("output %q was not declared", output)
	}
	return t.InsertRows(ctx, output, columns, rows)
}

const bundleCleanupTimeout = 10 * time.Second

func boundedBundleCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), bundleCleanupTimeout)
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
				acquired, err := t.store.catalog.RenewBundleLease(ctx, t.execution.Key, t.execution.OwnerID, until)
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
			timer := time.NewTimer(bundleCleanupTimeout)
			select {
			case <-t.leaseDone:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
				t.leaseStopErr = context.DeadlineExceeded
				return
			}
		}
		if t.execution.OwnerID != "" {
			cleanupCtx, cancel := boundedBundleCleanupContext(context.Background())
			defer cancel()
			t.leaseStopErr = t.store.releaseLease(cleanupCtx, t.execution.Key, t.execution.OwnerID)
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
	logicalColumns := append([]clickhouse.Column(nil), columns...)
	columns = withRowIdentityColumn(columns)
	for _, c := range columns {
		if c.Name == "__loom_row_id" {
			continue
		}
		if err := validateBundleColumn(publication.PhysicalColumn{Name: c.Name, ClickHouse: c.Type}); err != nil {
			return err
		}
	}
	table := t.tableName(name)
	if err := t.store.clickHouse.CreateTable(ctx, table, columns); err != nil {
		return err
	}
	converted := make([]publication.PhysicalColumn, len(columns))
	for i, c := range columns {
		converted[i] = publication.PhysicalColumn{Name: c.Name, ClickHouse: c.Type}
	}
	t.execution.Outputs = append(t.execution.Outputs, publication.BundleOutputRecord{Name: name, PhysicalTable: table, Selector: t.execution.Selector(name), Columns: converted, State: publication.BundleRunning})
	if t.columns == nil {
		t.columns = make(map[string][]clickhouse.Column)
	}
	// Keep the caller-facing schema separate from the physical row identity
	// column. InsertRows adds generated identities when it receives this view.
	t.columns[name] = logicalColumns
	return t.save(ctx)
}

// SetOutputMetadata persists semantic schema alongside the physical table
// definition.
func (t *clickHouseBundleTx) SetOutputMetadata(name string, columns []publication.LogicalColumn) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	idx := t.outputIndex(name)
	if idx < 0 {
		return fmt.Errorf("bundle output %q was not created", name)
	}
	record := &t.execution.Outputs[idx]
	if len(columns) == 0 {
		return fmt.Errorf("bundle output %q has no metadata columns", name)
	}
	physical := make(map[string]publication.LogicalColumn, len(columns))
	for _, column := range columns {
		physical[column.Name] = column
	}
	for index := range record.Columns {
		column := &record.Columns[index]
		if logical, ok := physical[column.Name]; ok {
			column.SemanticPath = logical.SemanticPath
			column.LogicalType = logical.Kind
			column.Nullable = logical.Nullable
			column.Repeated = logical.Repeated
			column.Provenance = logical.Provenance
			column.LoomOwned = logical.LoomOwned || logical.IsIdentity || column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id"
		}
	}
	return t.save(context.Background())
}

// FinalizeSchema removes discovered columns that were never populated and
// persists the retained logical/physical schema before verification.
func (t *clickHouseBundleTx) FinalizeSchema(ctx context.Context, schemas []publication.OutputSchema) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return err
	}
	for _, schema := range schemas {
		idx := t.outputIndex(schema.Name)
		if idx < 0 {
			return fmt.Errorf("bundle output %q was not created", schema.Name)
		}
		record := &t.execution.Outputs[idx]
		retained := make(map[string]publication.LogicalColumn, len(schema.Columns))
		for _, column := range schema.Columns {
			retained[column.Name] = column
		}
		var dropped []string
		seen := make(map[string]bool, len(record.Columns))
		kept := make([]publication.PhysicalColumn, 0, len(record.Columns))
		for _, column := range record.Columns {
			logical, ok := retained[column.Name]
			if !ok {
				if column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id" || strings.HasPrefix(column.Name, "__loom_") {
					seen[column.Name] = true
					kept = append(kept, column)
				} else if column.Provenance != publication.ColumnDiscovered {
					return fmt.Errorf("output %q attempted to remove explicit column %q", schema.Name, column.Name)
				} else {
					dropped = append(dropped, column.Name)
				}
				continue
			}
			seen[column.Name] = true
			column.SemanticPath, column.LogicalType = logical.SemanticPath, logical.Kind
			column.Nullable, column.Repeated = logical.Nullable, logical.Repeated
			column.Provenance, column.LoomOwned = logical.Provenance, logical.LoomOwned || logical.IsIdentity || strings.HasPrefix(column.Name, "__loom_") || column.Name == "auth_resource_path" || column.Name == "project_id"
			kept = append(kept, column)
		}
		if len(dropped) > 0 {
			if err := t.store.clickHouse.DropColumns(ctx, record.PhysicalTable, dropped); err != nil {
				return err
			}
		}
		record.Columns = kept
		for name := range retained {
			if !seen[name] {
				return fmt.Errorf("output %q retained unknown schema column %q", schema.Name, name)
			}
		}
	}
	identity := publication.PublicationIdentity{Name: t.execution.Name, TranslationVersion: t.execution.TranslationVersion, Project: t.execution.Project, DatasetGeneration: t.execution.DatasetGeneration, RecipeDigest: t.execution.RecipeDigest, ScopeDigest: t.execution.ScopeDigest, EngineVersion: t.execution.EngineVersion, AuthScopeMode: t.execution.AuthScopeMode, AuthResourcePaths: append([]string(nil), t.execution.AuthResourcePaths...)}
	t.execution.SchemaDigest = publication.FinalSchemaDigest(identity, schemas)
	return t.save(ctx)
}

func (t *clickHouseBundleTx) SetFinalSchemaDigest(digest string) error {
	if t.idempotent {
		return nil
	}
	if strings.TrimSpace(digest) == "" {
		return fmt.Errorf("final schema digest is required")
	}
	t.execution.SchemaDigest = digest
	return nil
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
	if err := t.store.clickHouse.InsertRows(ctx, record.PhysicalTable, effectiveColumns, rows); err != nil {
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

func (t *clickHouseBundleTx) Commit(ctx context.Context) ([]publication.PublishedOutput, error) {
	if t.idempotent {
		t.closed = true
		return t.ExistingPublishedOutputs(), nil
	}
	if t.closed {
		return nil, fmt.Errorf("bundle transaction is closed")
	}
	if err := t.ensureLease(); err != nil {
		return nil, err
	}
	if len(t.execution.Outputs) == 0 {
		return nil, t.fail(ctx, fmt.Errorf("bundle has no outputs"))
	}
	t.execution.State = publication.BundleValidating
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = publication.BundleValidating
	}
	if err := t.save(ctx); err != nil {
		return nil, err
	}
	for i := range t.execution.Outputs {
		output := &t.execution.Outputs[i]
		if output.RowCount < 0 {
			return nil, t.failPhase(ctx, "VERIFY_OUTPUT", output.Name, fmt.Errorf("output %q has invalid row count", output.Name))
		}
		columns := make([]clickhouse.Column, len(output.Columns))
		for columnIndex, column := range output.Columns {
			columns[columnIndex] = clickhouse.Column{Name: column.Name, Type: column.ClickHouse}
		}
		if err := t.store.clickHouse.VerifyOutput(ctx, output.PhysicalTable, columns, output.RowCount); err != nil {
			return nil, t.failPhase(ctx, "VERIFY_OUTPUT", output.Name, dataframeerrors.Wrap(err, dataframeerrors.CodePublicationFailed, "", dataframeerrors.WithRetryable(true)))
		}
		verified := time.Now().UTC()
		output.VerifiedAt = &verified
	}
	now := time.Now().UTC()
	t.execution.State, t.execution.PublishedAt, t.execution.UpdatedAt = publication.BundlePublished, &now, now
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = publication.BundlePublished
	}
	if err := t.store.catalog.PublishExecution(ctx, t.execution.PointerName(), t.expectedPointer, t.execution); err != nil {
		if errors.Is(err, publication.ErrBundlePointerConflict) {
			err = dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "", dataframeerrors.WithRetryable(true))
			return nil, t.failPhase(ctx, "COMMIT_POINTER", "", fmt.Errorf("publish bundle pointer: %w", err))
		}
		committed, conclusive, confirmErr := t.confirmCommit(ctx)
		if committed {
			t.closed = true
			return t.publishedOutputs(), t.stopLease()
		}
		if !conclusive {
			t.closed = true // Prevent the runner from deleting possibly published tables.
			uncertain := dataframeerrors.Wrap(errors.Join(ErrBundleCommitUncertain, err, confirmErr), dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
			return nil, publication.WithPhase(errors.Join(uncertain, t.stopLease()), "COMMIT_POINTER", "")
		}
		err = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		return nil, t.failPhase(ctx, "COMMIT_POINTER", "", fmt.Errorf("publish bundle pointer: %w", err))
	}
	t.closed = true
	return t.publishedOutputs(), t.stopLease()
}

func (t *clickHouseBundleTx) publishedOutputs() []publication.PublishedOutput {
	result := make([]publication.PublishedOutput, 0, len(t.execution.Outputs))
	for _, output := range t.execution.Outputs {
		result = append(result, publication.PublishedOutput{Name: output.Name, PhysicalName: output.PhysicalTable, RowCount: output.RowCount, ByteCount: output.ByteCount})
	}
	return result
}

// confirmCommit distinguishes a lost success response from a transaction that
// definitely did not publish. Inconsistent or unreadable metadata remains
// uncertain so callers never delete tables that a live pointer may reference.
func (t *clickHouseBundleTx) confirmCommit(ctx context.Context) (committed, conclusive bool, err error) {
	execution, executionErr := t.store.catalog.GetExecution(ctx, t.execution.ID)
	pointer, pointerErr := t.store.catalog.GetPointer(ctx, t.execution.PointerName())
	if errors.Is(pointerErr, publication.ErrBundleNotFound) {
		pointerErr = nil
		pointer = publication.BundlePointer{Name: t.execution.PointerName()}
	}
	if executionErr != nil || pointerErr != nil {
		return false, false, errors.Join(executionErr, pointerErr)
	}
	if execution.State.Successful() && pointer.ExecutionID == execution.ID && allOutputsQueryable(execution.Outputs) {
		t.execution = execution
		return true, true, nil
	}
	if !execution.State.Successful() && pointer.ExecutionID != execution.ID {
		return false, true, nil
	}
	return false, false, fmt.Errorf("publication metadata is inconsistent: execution state %q, pointer %q", execution.State, pointer.ExecutionID)
}

func (t *clickHouseBundleTx) Abort(ctx context.Context, cause error) error {
	if t.idempotent || t.closed {
		return nil
	}
	if err := t.ensureLease(); err != nil {
		t.closed = true
		return errors.Join(err, t.stopLease())
	}
	cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
	defer cancel()
	var cleanup error
	for _, output := range t.execution.Outputs {
		if err := t.store.clickHouse.DropTable(cleanupCtx, output.PhysicalTable); err != nil {
			cleanup = errors.Join(cleanup, err)
		}
	}
	if t.execution.State != publication.BundleFailed {
		if err := t.fail(cleanupCtx, cause); err != nil {
			cleanup = errors.Join(cleanup, err)
		}
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
	return t.store.prefix + "_" + strings.ReplaceAll(t.execution.ID, "-", "") + "_" + output
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
	if err := t.store.catalog.SaveExecution(ctx, snapshot); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return nil
}

func (t *clickHouseBundleTx) fail(ctx context.Context, err error) error {
	return t.failPhase(ctx, "PUBLICATION", "", err)
}

func (t *clickHouseBundleTx) failPhase(ctx context.Context, phase, output string, err error) error {
	slog.Error("dataframe bundle publication failed", "phase", phase, "output", output, "error", err)
	normalized := dataframeerrors.Normalize(err)
	t.execution.State, t.execution.Error = publication.BundleFailed, err.Error()
	t.execution.PublishedAt = nil
	t.execution.FailureCode, t.execution.FailureRetryable = normalized.Code(), normalized.Retryable()
	t.execution.FailurePhase, t.execution.FailureOutput, t.execution.FailureDetails = phase, output, err.Error()
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = publication.BundleFailed
		t.execution.Outputs[i].FailureCode = normalized.Code()
		t.execution.Outputs[i].FailureRetryable = normalized.Retryable()
		t.execution.Outputs[i].FailurePhase = phase
		t.execution.Outputs[i].FailureDetails = err.Error()
	}
	if persistenceErr := t.save(ctx); persistenceErr != nil {
		return errors.Join(err, persistenceErr)
	}
	return err
}

var bundleOutputRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var schemaIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var supportedSchemaScalarRE = regexp.MustCompile(`^(String|JSON|UUID|Bool|Int8|Int16|Int32|Int64|Int128|Int256|UInt8|UInt16|UInt32|UInt64|UInt128|UInt256|Float32|Float64|Date|Date32|DateTime|DateTime64(\([^)]*\))?)$`)

func validateBundleColumn(column publication.PhysicalColumn) error {
	if column.Name == "" || !schemaIdentifierRE.MatchString(column.Name) || column.Name == "__loom_row_id" {
		return fmt.Errorf("invalid dataframe schema column %q", column.Name)
	}
	typ := column.ClickHouse
	for strings.HasPrefix(typ, "Nullable(") || strings.HasPrefix(typ, "Array(") {
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, strings.SplitN(typ, "(", 2)[0]+"("), ")")
	}
	if typ == "" || !supportedSchemaScalarRE.MatchString(typ) {
		return fmt.Errorf("unsupported ClickHouse type %q for schema column %q", column.ClickHouse, column.Name)
	}
	return nil
}

func validBundleOutput(value string) bool { return bundleOutputRE.MatchString(value) }

func toColumns(columns []publication.LogicalColumn) ([]clickhouse.Column, error) {
	result := make([]clickhouse.Column, 0, len(columns))
	for _, column := range columns {
		kind := strings.ToLower(strings.TrimSpace(column.Kind))
		columnType := "String"
		switch kind {
		case "boolean":
			columnType = "Bool"
		case "integer":
			columnType = "Int64"
		case "decimal":
			columnType = "Float64"
		case "date":
			columnType = "Date"
		case "date-time":
			columnType = "DateTime64(3)"
		case "uuid":
			columnType = "UUID"
		case "code":
			columnType = "String"
		case "object":
			columnType = "JSON"
		}
		if column.Repeated {
			columnType = "Array(" + columnType + ")"
		} else if column.Nullable {
			columnType = "Nullable(" + columnType + ")"
		}
		result = append(result, clickhouse.Column{Name: column.Name, Type: columnType})
	}
	return result, nil
}

func allOutputsQueryable(outputs []publication.BundleOutputRecord) bool {
	if len(outputs) == 0 {
		return false
	}
	for _, output := range outputs {
		if !output.Queryable() {
			return false
		}
	}
	return true
}

// Reconcile removes abandoned staging tables and repairs persisted publication
// state left behind by an interrupted commit.
// It is safe to call repeatedly during startup; published pointers and queued
// commands that have not started are never touched.
func (s *ClickHouseBundleStore) Reconcile(ctx context.Context, olderThan time.Time) error {
	reconcilerID := "reconciler-" + uuid.NewString()
	for _, state := range []publication.BundleState{publication.BundleRunning, publication.BundleValidating, publication.BundlePreflight, publication.BundleLoading, publication.BundleFailed} {
		executions, err := s.catalog.ListExecutions(ctx, state, olderThan)
		if err != nil {
			return err
		}
		for _, execution := range executions {
			expires := time.Now().UTC().Add(s.leaseTTL)
			claimed, err := s.catalog.AcquireBundleLease(ctx, execution.Key, reconcilerID, expires)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			execution.OwnerID = reconcilerID
			cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
			pointer, pointerErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName())
			if pointerErr != nil && !errors.Is(pointerErr, publication.ErrBundleNotFound) {
				releaseErr := s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
				cancel()
				return errors.Join(pointerErr, releaseErr)
			}
			// A pointer is the visibility boundary. If this execution is already
			// visible, its tables are live even when a stale lifecycle snapshot
			// still reports a non-successful state; never clean those tables up.
			if pointerErr == nil && pointer.ExecutionID == execution.ID {
				releaseErr := s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
				cancel()
				if releaseErr != nil {
					return releaseErr
				}
				continue
			}
			var first error
			remaining := make([]publication.BundleOutputRecord, 0, len(execution.Outputs))
			for _, output := range execution.Outputs {
				if err := s.clickHouse.DropTable(cleanupCtx, output.PhysicalTable); err != nil {
					first = errors.Join(first, err)
					remaining = append(remaining, output)
				}
			}
			execution.State = publication.BundleFailed
			execution.Error = "stale execution reconciled"
			execution.FailureCode = string(dataframeerrors.CodePublicationLeaseLost)
			execution.FailureRetryable = true
			execution.UpdatedAt = time.Now().UTC()
			execution.Outputs = remaining
			execution.OwnerID = ""
			execution.LeaseExpiresAt = nil
			if err := s.catalog.SaveExecution(cleanupCtx, execution); err != nil {
				first = errors.Join(first, err)
			}
			first = errors.Join(first, s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID))
			cancel()
			if first != nil {
				return first
			}
		}
	}
	return nil
}
