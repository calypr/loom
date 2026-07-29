package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/authscope"
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
	AuthResourcePaths                []string `json:"authResourcePaths,omitempty"`
}

// PointerName is the visibility namespace for a published logical dataset.
// Project and generation are part of the key so two tenants can publish the
// same recipe/output name without racing a shared pointer.
func (i BundleIdentity) PointerName() string {
	return strings.Join([]string{i.Project, i.DatasetGeneration, i.Name}, "\x00")
}

func (i BundleIdentity) Key() string {
	b, _ := json.Marshal(i)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type BundleOutputRecord struct {
	Name, Alias, PhysicalTable string
	Columns                    []Column
	RowCount, ByteCount        int64
	State                      BundleState
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

// ResolvePublishedOutput resolves the current READY output for one logical
// dataset. The alias currently defaults to the output name; publication code
// may set BundleOutputRecord.Alias when an Explorer-facing alias differs.
func ResolvePublishedOutput(ctx context.Context, catalog BundleCatalog, project, generation, alias string) (Materialization, error) {
	if catalog == nil {
		return Materialization{}, fmt.Errorf("bundle catalog is required")
	}
	listed, ok := catalog.(StaleBundleCatalog)
	if !ok {
		return Materialization{}, fmt.Errorf("bundle catalog does not support dataset resolution")
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, time.Now().UTC().Add(time.Second))
	if err != nil {
		return Materialization{}, err
	}
	var newest *BundleExecution
	for index := range executions {
		execution := executions[index]
		if execution.Project != project || execution.DatasetGeneration != generation || execution.State != BundleReady {
			continue
		}
		pointer, pointerErr := catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil || pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			outputAlias := output.Alias
			if outputAlias == "" {
				outputAlias = output.Name
			}
			if outputAlias == alias && (newest == nil || execution.UpdatedAt.After(newest.UpdatedAt)) {
				copy := execution
				newest = &copy
				break
			}
		}
	}
	if newest != nil {
		for _, output := range newest.Outputs {
			outputAlias := output.Alias
			if outputAlias == "" {
				outputAlias = output.Name
			}
			if outputAlias == alias {
				return publishedMaterialization(*newest, output, outputAlias), nil
			}
		}
	}
	return Materialization{}, fmt.Errorf("published dataset %q was not found for project %q and generation %q", alias, project, generation)
}

// ListPublishedOutputs lists READY outputs for one project and generation when
// the catalog supports execution listing. Production catalogs do; lightweight
// test catalogs may only support direct resolution.
func ListPublishedOutputs(ctx context.Context, catalog BundleCatalog, project, generation string) ([]Materialization, error) {
	listed, ok := catalog.(StaleBundleCatalog)
	if !ok {
		return nil, fmt.Errorf("bundle catalog does not support dataset listing")
	}
	executions, err := listed.ListExecutions(ctx, BundleReady, time.Now().UTC().Add(time.Second))
	if err != nil {
		return nil, err
	}
	result := make([]Materialization, 0)
	for _, execution := range executions {
		if execution.Project != project || execution.DatasetGeneration != generation || execution.State != BundleReady {
			continue
		}
		pointer, pointerErr := catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil || pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			alias := output.Alias
			if alias == "" {
				alias = output.Name
			}
			result = append(result, publishedMaterialization(execution, output, alias))
		}
	}
	return result, nil
}

func publishedMaterialization(execution BundleExecution, output BundleOutputRecord, alias string) Materialization {
	return Materialization{ID: execution.ID + ":" + output.Name, Name: alias, Revision: execution.ID, Project: execution.Project, DatasetGeneration: execution.DatasetGeneration, State: StateReady, AuthScopeMode: authScopeMode(execution.AuthResourcePaths), AuthResourcePaths: append([]string(nil), execution.AuthResourcePaths...), Columns: output.Columns, PhysicalTable: output.PhysicalTable, RowCount: output.RowCount, CreatedAt: execution.CreatedAt, ReadyAt: execution.ReadyAt}
}

func authScopeMode(paths []string) authscope.ReadScopeMode {
	if len(paths) == 0 {
		return authscope.ReadScopeUnrestricted
	}
	return authscope.ReadScopeRestricted
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
