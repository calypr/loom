package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/store/clickhouse"
)

// BundleOutput is a fully resolved output stream ready for publication. The
// producer is responsible for consuming one resolved physical plan per
// output; this package only owns the all-or-nothing publication boundary.
type BundleOutput struct {
	Name    string
	Columns []Column
	Rows    []map[string]any
}

// StreamOutput is the bounded-memory input to PublishStreamBundle. The
// callback must call visit once for each logical row and stop when it returns
// an error.
type StreamOutput struct {
	Name    string
	Columns []clickhouse.Column
	Stream  func(context.Context, func(map[string]any) error) error
}

// StreamPublishConfig controls row and byte buffering during publication.
type StreamPublishConfig struct {
	BatchRows  int
	BatchBytes int
}

func (c StreamPublishConfig) normalized() StreamPublishConfig {
	if c.BatchRows <= 0 {
		c.BatchRows = 1000
	}
	if c.BatchBytes <= 0 {
		c.BatchBytes = 4 << 20
	}
	return c
}

type AtomicBundleStore interface {
	BeginBundle(context.Context) (AtomicBundleTx, error)
}

type AtomicBundleTx interface {
	CreateOutput(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

// BundleState is the durable lifecycle of a multi-output publication.  The
// physical ClickHouse tables are never reader-visible until the logical
// pointer is advanced to READY.
type BundleState string

const (
	BundlePending    BundleState = "PENDING"
	BundlePreflight  BundleState = "PREFLIGHT"
	BundleLoading    BundleState = "LOADING"
	BundleValidating BundleState = "VALIDATING"
	BundleReady      BundleState = "READY"
	BundleFailed     BundleState = "FAILED"
)

type BundleIdentity struct {
	Name, Project, DatasetGeneration string
	RecipeDigest, SchemaDigest       string
	ScopeDigest, EngineVersion       string
}

func (i BundleIdentity) Key() string {
	b, _ := json.Marshal(i)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type BundleOutputRecord struct {
	Name, PhysicalTable string
	Columns             []Column
	RowCount, ByteCount int64
	State               BundleState
}

type BundleExecution struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	BundleIdentity
	State     BundleState          `json:"state"`
	Outputs   []BundleOutputRecord `json:"outputs,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
	ReadyAt   *time.Time           `json:"readyAt,omitempty"`
	Error     string               `json:"error,omitempty"`
}

type BundlePointer struct {
	Name        string    `json:"name"`
	ExecutionID string    `json:"executionId"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// StaleBundleCatalog is optional so existing lightweight registries remain
// source-compatible. Production catalogs implement it for startup recovery.
type StaleBundleCatalog interface {
	ListExecutions(context.Context, BundleState, time.Time) ([]BundleExecution, error)
}

// BundleCatalog is the durable metadata/pointer boundary. Implementations
// should make CompareAndSwapPointer atomic in their backing store.
type BundleCatalog interface {
	SaveExecution(context.Context, BundleExecution) error
	GetExecution(context.Context, string) (BundleExecution, error)
	FindExecutionByKey(context.Context, string) (BundleExecution, error)
	GetPointer(context.Context, string) (BundlePointer, error)
	CompareAndSwapPointer(context.Context, string, string, string) error
}

var ErrBundleNotFound = fmt.Errorf("bundle execution not found")
var ErrBundlePointerConflict = fmt.Errorf("bundle pointer compare-and-swap conflict")

// PublishBundle stages every output in one backend transaction and commits
// only after all outputs have been created and loaded. A failed output rolls
// back the entire bundle, so no partial READY set can be observed.
func PublishBundle(ctx context.Context, store AtomicBundleStore, outputs []BundleOutput) error {
	if store == nil {
		return fmt.Errorf("atomic bundle store is required")
	}
	if len(outputs) == 0 {
		return fmt.Errorf("bundle must contain at least one output")
	}
	tx, err := store.BeginBundle(ctx)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			return fmt.Errorf("%w (bundle rollback failed: %v)", cause, rollbackErr)
		}
		return cause
	}
	seen := map[string]struct{}{}
	for _, output := range outputs {
		if strings.TrimSpace(output.Name) == "" {
			return rollback(fmt.Errorf("bundle output name is required"))
		}
		if _, ok := seen[output.Name]; ok {
			return rollback(fmt.Errorf("bundle output %q is duplicated", output.Name))
		}
		seen[output.Name] = struct{}{}
		columns := toClickHouseColumns(output.Columns)
		if err := tx.CreateOutput(ctx, output.Name, columns); err != nil {
			return rollback(fmt.Errorf("output %q create: %w", output.Name, err))
		}
		if len(output.Rows) > 0 {
			if err := tx.InsertRows(ctx, output.Name, columns, output.Rows); err != nil {
				return rollback(fmt.Errorf("output %q insert: %w", output.Name, err))
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return rollback(fmt.Errorf("bundle commit: %w", err))
	}
	return nil
}

// PublishStreamBundle stages each output while consuming it once. Its memory
// use is bounded by StreamPublishConfig and it advances the logical pointer
// only after every output stream has completed.
func PublishStreamBundle(ctx context.Context, store IdentityBundleStore, identity BundleIdentity, outputs []StreamOutput, config StreamPublishConfig) error {
	if store == nil {
		return fmt.Errorf("identity bundle store is required")
	}
	if len(outputs) == 0 {
		return fmt.Errorf("bundle must contain at least one output")
	}
	config = config.normalized()
	tx, err := store.BeginBundleFor(ctx, identity)
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			return fmt.Errorf("%w (bundle rollback failed: %v)", cause, rollbackErr)
		}
		return cause
	}
	for _, output := range outputs {
		if strings.TrimSpace(output.Name) == "" || output.Stream == nil {
			return fail(fmt.Errorf("stream output name and callback are required"))
		}
		if err := tx.CreateOutput(ctx, output.Name, output.Columns); err != nil {
			return fail(fmt.Errorf("output %q create: %w", output.Name, err))
		}
		batch := make([]map[string]any, 0, config.BatchRows)
		batchBytes := 0
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := tx.InsertRows(ctx, output.Name, output.Columns, batch); err != nil {
				return err
			}
			batch = batch[:0]
			batchBytes = 0
			return nil
		}
		err := output.Stream(ctx, func(row map[string]any) error {
			encoded, err := json.Marshal(row)
			if err != nil {
				return fmt.Errorf("output %q encode row: %w", output.Name, err)
			}
			if len(encoded) > config.BatchBytes {
				return fmt.Errorf("output %q row exceeds batch byte limit %d", output.Name, config.BatchBytes)
			}
			batch = append(batch, row)
			batchBytes += len(encoded)
			if len(batch) >= config.BatchRows || batchBytes >= config.BatchBytes {
				return flush()
			}
			return nil
		})
		if err != nil {
			return fail(fmt.Errorf("output %q stream: %w", output.Name, err))
		}
		if err := flush(); err != nil {
			return fail(fmt.Errorf("output %q final batch: %w", output.Name, err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fail(fmt.Errorf("bundle commit: %w", err))
	}
	return nil
}
