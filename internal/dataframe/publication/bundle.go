package publication

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
	Columns                    []PhysicalColumn
	RowCount, ByteCount        int64
	State                      BundleState
	FailureCode                string `json:"failureCode,omitempty"`
	FailureRetryable           bool   `json:"failureRetryable,omitempty"`
}

type PhysicalColumn struct {
	Name       string `json:"name"`
	ClickHouse string `json:"clickhouseType"`
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

var ErrBundleNotFound = fmt.Errorf("bundle execution not found")
var ErrBundlePointerConflict = fmt.Errorf("bundle pointer compare-and-swap conflict")
