package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

var (
	ErrBundleInFlight            = errors.New("identical bundle execution is already in flight")
	ErrBundleLeaseLost           = publication.ErrBundleLeaseLost
	ErrBundleCommitUncertain     = errors.New("bundle publication commit outcome is uncertain")
	ErrBundleCheckpointUncertain = errors.New("bundle checkpoint outcome is uncertain")
)

type ClickHouseBundleStore struct {
	clickHouse         BundleClickHouseStore
	catalog            publication.BundleCatalog
	prefix             string
	keyLocksMu         sync.Mutex
	keyLocks           map[string]*bundleKeyLock
	leaseTTL           time.Duration
	leaseRenewInterval time.Duration
	reconcilePageSize  int
	activeReleases     dataset.ActiveReleaseResolver
}

type bundleKeyLock struct {
	ready chan struct{}
	refs  int
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
	return &ClickHouseBundleStore{
		clickHouse:         client,
		catalog:            catalog,
		prefix:             "loom_bundle",
		keyLocks:           make(map[string]*bundleKeyLock),
		leaseTTL:           2 * time.Minute,
		leaseRenewInterval: 30 * time.Second,
		reconcilePageSize:  32,
	}, nil
}

func (s *ClickHouseBundleStore) SetActiveReleaseReader(reader dataset.ActiveReleaseResolver) {
	s.activeReleases = reader
}

var _ publication.Target = (*ClickHouseBundleStore)(nil)

var _ publication.Transaction = (*clickHouseBundleTx)(nil)

func (s *ClickHouseBundleStore) SupportsObjectValues() bool { return true }

func (s *ClickHouseBundleStore) Begin(ctx context.Context, identity publication.PublicationIdentity, schemas []publication.OutputSchema) (publication.Transaction, error) {
	bundleIdentity := publication.BundleIdentity{
		Name: identity.Name, TranslationVersion: identity.TranslationVersion, OutputName: identity.OutputName,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest,
		SchemaDigest: identity.SchemaDigest, ReceiptID: identity.ReceiptID, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
		AuthScopeMode: identity.AuthScopeMode, AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...),
	}
	tx, err := s.beginBundle(ctx, bundleIdentity)
	if err != nil {
		return nil, err
	}
	for _, schema := range schemas {
		columns := toColumns(schema.Columns)
		if err := tx.CreateOutput(ctx, schema.Name, columns); err != nil {
			cause := fmt.Errorf("output %q create: %w", schema.Name, err)
			cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
			abortErr := tx.Abort(cleanupCtx, cause)
			cancel()
			return nil, errors.Join(cause, abortErr)
		}
		if err := tx.SetOutputMetadata(ctx, schema.Name, schema.Columns); err != nil {
			cause := fmt.Errorf("output %q metadata: %w", schema.Name, err)
			cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
			abortErr := tx.Abort(cleanupCtx, cause)
			cancel()
			return nil, errors.Join(cause, abortErr)
		}
		if schema.SourceRow != nil {
			if err := tx.SetSourceRowMetadata(ctx, schema.Name, *schema.SourceRow); err != nil {
				cause := fmt.Errorf("output %q source identity: %w", schema.Name, err)
				cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
				abortErr := tx.Abort(cleanupCtx, cause)
				cancel()
				return nil, errors.Join(cause, abortErr)
			}
		}
	}
	return tx, nil
}

func (s *ClickHouseBundleStore) beginBundle(ctx context.Context, identity publication.BundleIdentity) (*clickHouseBundleTx, error) {
	identity = identity.Canonical()
	if strings.TrimSpace(identity.Name) == "" {
		return nil, fmt.Errorf("bundle name is required")
	}
	if identity.EngineVersion == "" {
		identity.EngineVersion = "loom"
	}
	key := identity.Key()
	unlock, err := s.lockBundleKey(ctx, key)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if existing, err := s.catalog.FindExecutionByKey(ctx, key); err == nil {
		switch {
		case existing.State.Successful():
			return &clickHouseBundleTx{store: s, execution: existing, idempotent: true, columns: make(map[string][]clickhouse.Column)}, nil
		case existing.State.InFlight():
			if existing.LeaseExpiresAt == nil || existing.LeaseExpiresAt.After(time.Now().UTC()) {
				return nil, dataframeerrors.Wrap(
					fmt.Errorf("%w: %s", ErrBundleInFlight, existing.ID),
					dataframeerrors.CodePublicationInProgress,
					"",
					dataframeerrors.WithDetails(map[string]any{"executionId": existing.ID}),
					dataframeerrors.WithRetryable(true),
				)
			}

			reconcilerID := "retry-reconciler-" + uuid.NewString()
			if err := s.reconcileExecution(ctx, existing, reconcilerID); err != nil {
				return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
			}
			refreshed, refreshErr := s.catalog.FindExecutionByKey(ctx, key)
			if errors.Is(refreshErr, publication.ErrBundleNotFound) {
				break
			}
			if refreshErr != nil {
				return nil, dataframeerrors.Wrap(refreshErr, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
			}
			switch {
			case refreshed.State.Successful():
				return &clickHouseBundleTx{store: s, execution: refreshed, idempotent: true, columns: make(map[string][]clickhouse.Column)}, nil
			case refreshed.State.InFlight():
				return nil, dataframeerrors.Wrap(
					fmt.Errorf("%w: %s", ErrBundleInFlight, refreshed.ID),
					dataframeerrors.CodePublicationInProgress,
					"",
					dataframeerrors.WithDetails(map[string]any{"executionId": refreshed.ID}),
					dataframeerrors.WithRetryable(true),
				)
			}
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
	if err := s.catalog.SaveExecution(ctx, execution, id); err != nil {
		if releaseErr := s.releaseLease(ctx, key, id); releaseErr != nil {
			return nil, errors.Join(err, releaseErr)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	execution.State = publication.BundleRunning
	if err := s.catalog.SaveExecution(ctx, execution, id); err != nil {
		if releaseErr := s.releaseLease(ctx, key, id); releaseErr != nil {
			return nil, errors.Join(err, releaseErr)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	tx := &clickHouseBundleTx{store: s, execution: execution, expectedPointer: expectedPointer}
	tx.startLeaseRenewal(ctx)
	return tx, nil
}

func (s *ClickHouseBundleStore) lockBundleKey(ctx context.Context, key string) (func(), error) {
	s.keyLocksMu.Lock()
	lock := s.keyLocks[key]
	if lock == nil {
		lock = &bundleKeyLock{ready: make(chan struct{}, 1)}
		lock.ready <- struct{}{}
		s.keyLocks[key] = lock
	}
	lock.refs++
	s.keyLocksMu.Unlock()

	select {
	case <-lock.ready:
		return func() {
			lock.ready <- struct{}{}
			s.releaseBundleKeyRef(key, lock)
		}, nil
	case <-ctx.Done():
		s.releaseBundleKeyRef(key, lock)
		return nil, ctx.Err()
	}
}

func (s *ClickHouseBundleStore) releaseBundleKeyRef(key string, lock *bundleKeyLock) {
	s.keyLocksMu.Lock()
	defer s.keyLocksMu.Unlock()
	lock.refs--
	if lock.refs == 0 && s.keyLocks[key] == lock {
		delete(s.keyLocks, key)
	}
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
