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

type AtomicBundleTx interface {
	CreateOutput(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	Commit(context.Context) error
	Abort(context.Context, error) error
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
	FailureCode                string `json:"failureCode,omitempty"`
	FailureRetryable           bool   `json:"failureRetryable,omitempty"`
}

type BundleExecution struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	BundleIdentity
	State            BundleState          `json:"state"`
	Outputs          []BundleOutputRecord `json:"outputs,omitempty"`
	CreatedAt        time.Time            `json:"createdAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
	ReadyAt          *time.Time           `json:"readyAt,omitempty"`
	Error            string               `json:"error,omitempty"`
	FailureCode      string               `json:"failureCode,omitempty"`
	FailureRetryable bool                 `json:"failureRetryable,omitempty"`
	OwnerID          string               `json:"ownerId,omitempty"`
	LeaseExpiresAt   *time.Time           `json:"leaseExpiresAt,omitempty"`
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

type BundleLeaseCatalog interface {
	AcquireBundleLease(context.Context, string, string, time.Time) (bool, error)
	RenewBundleLease(context.Context, string, string, time.Time) (bool, error)
	ReleaseBundleLease(context.Context, string, string) error
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
		if pointerErr != nil {
			return Materialization{}, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID != execution.ID {
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
		if pointerErr != nil {
			return nil, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID != execution.ID {
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
