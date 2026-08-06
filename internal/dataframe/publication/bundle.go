package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type DataframeSelector = dataset.DataframeSelector

type AtomicBundleTx interface {
	CreateOutput(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	Commit(context.Context) error
	Abort(context.Context, error) error
	Rollback(context.Context) error
}

// BundleState is the durable lifecycle of a multi-output publication. The
// physical ClickHouse tables are never reader-visible until the logical
// pointer and PUBLISHED execution are committed atomically.
type BundleState string

const (
	BundleQueued     BundleState = "QUEUED"
	BundleRunning    BundleState = "RUNNING"
	BundleValidating BundleState = "VALIDATING"
	BundlePublished  BundleState = "PUBLISHED"
	BundleFailed     BundleState = "FAILED"

	// Compatibility-only stored values. New workflows never write these.
	BundlePending   BundleState = "PENDING"
	BundlePreflight BundleState = "PREFLIGHT"
	BundleLoading   BundleState = "LOADING"
	BundleReady     BundleState = "READY"
)

func (s BundleState) Canonical() BundleState {
	switch s {
	case BundlePending:
		return BundleQueued
	case BundlePreflight, BundleLoading:
		return BundleRunning
	case BundleReady:
		return BundlePublished
	default:
		return s
	}
}

func (s BundleState) Successful() bool { return s.Canonical() == BundlePublished }

type BundleIdentity struct {
	Name               string   `json:"name"`
	TranslationVersion string   `json:"translationVersion,omitempty"`
	Project            string   `json:"project"`
	DatasetGeneration  string   `json:"datasetGeneration"`
	RecipeDigest       string   `json:"recipeDigest"`
	SchemaDigest       string   `json:"schemaDigest"`
	ScopeDigest        string   `json:"scopeDigest"`
	EngineVersion      string   `json:"engineVersion"`
	AuthResourcePaths  []string `json:"authResourcePaths,omitempty"`
}

// PointerName is the visibility namespace for a published logical dataset.
// Project and generation are part of the key so two tenants can publish the
// same recipe/output name without racing a shared pointer.
func (i BundleIdentity) PointerName() string {
	return strings.Join([]string{i.Project, i.DatasetGeneration, i.Name, i.TranslationVersion}, "\x00")
}

func (i BundleIdentity) Key() string {
	b, _ := json.Marshal(struct {
		Name, Project, DatasetGeneration string
		TranslationVersion               string `json:"TranslationVersion,omitempty"`
		RecipeDigest, SchemaDigest       string
		ScopeDigest, EngineVersion       string
		AuthResourcePaths                []string `json:"AuthResourcePaths,omitempty"`
	}{i.Name, i.Project, i.DatasetGeneration, i.TranslationVersion, i.RecipeDigest, i.SchemaDigest, i.ScopeDigest, i.EngineVersion, i.AuthResourcePaths})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type BundleOutputRecord struct {
	Name             string            `json:"name"`
	PhysicalTable    string            `json:"physicalTable"`
	Selector         DataframeSelector `json:"selector"`
	Columns          []PhysicalColumn  `json:"columns,omitempty"`
	RowCount         int64             `json:"rowCount"`
	ByteCount        int64             `json:"byteCount"`
	State            BundleState       `json:"state"`
	FailureCode      string            `json:"failureCode,omitempty"`
	FailureRetryable bool              `json:"failureRetryable,omitempty"`
	VerifiedAt       *time.Time        `json:"verifiedAt,omitempty"`
	FailurePhase     string            `json:"failurePhase,omitempty"`
	FailureDetails   string            `json:"failureDetails,omitempty"`
}

func (e BundleExecution) Selector(output string) DataframeSelector {
	return DataframeSelector{Recipe: e.Name, TranslationVersion: e.TranslationVersion, Output: output}
}

func (o BundleOutputRecord) Queryable() bool {
	return o.State.Successful() && o.VerifiedAt != nil && strings.TrimSpace(o.PhysicalTable) != ""
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
	PublishedAt      *time.Time           `json:"publishedAt,omitempty"`
	Error            string               `json:"error,omitempty"`
	FailureCode      string               `json:"failureCode,omitempty"`
	FailureRetryable bool                 `json:"failureRetryable,omitempty"`
	OwnerID          string               `json:"ownerId,omitempty"`
	LeaseExpiresAt   *time.Time           `json:"leaseExpiresAt,omitempty"`
	Attempt          int                  `json:"attempt,omitempty"`
	MaxAttempts      int                  `json:"maxAttempts,omitempty"`
	NextAttemptAt    *time.Time           `json:"nextAttemptAt,omitempty"`
	FailurePhase     string               `json:"failurePhase,omitempty"`
	FailureOutput    string               `json:"failureOutput,omitempty"`
	FailureDetails   string               `json:"failureDetails,omitempty"`
}

func (e BundleExecution) CanonicalizeLegacy() BundleExecution {
	e.State = e.State.Canonical()
	for i := range e.Outputs {
		e.Outputs[i].State = e.Outputs[i].State.Canonical()
		if e.Outputs[i].Selector.Recipe == "" && e.TranslationVersion != "" {
			e.Outputs[i].Selector = e.Selector(e.Outputs[i].Name)
		}
		if e.Outputs[i].VerifiedAt == nil && e.Outputs[i].State == BundlePublished {
			verified := e.ReadyAt
			if verified == nil {
				stamp := e.UpdatedAt
				verified = &stamp
			}
			e.Outputs[i].VerifiedAt = verified
		}
	}
	if e.PublishedAt == nil && e.State == BundlePublished {
		e.PublishedAt = e.ReadyAt
	}
	return e
}

type BundlePointer struct {
	Name        string    `json:"name"`
	ExecutionID string    `json:"executionId"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// BundleCatalog is the durable metadata/pointer boundary. Implementations
// make pointer updates and lease acquisition atomic in their backing store.
type BundleCatalog interface {
	SaveExecution(context.Context, BundleExecution) error
	GetExecution(context.Context, string) (BundleExecution, error)
	FindExecutionByKey(context.Context, string) (BundleExecution, error)
	GetPointer(context.Context, string) (BundlePointer, error)
	CompareAndSwapPointer(context.Context, string, string, string) error
	PublishExecution(context.Context, string, string, BundleExecution) error
	ListExecutions(context.Context, BundleState, time.Time) ([]BundleExecution, error)
	AcquireBundleLease(context.Context, string, string, time.Time) (bool, error)
	RenewBundleLease(context.Context, string, string, time.Time) (bool, error)
	ReleaseBundleLease(context.Context, string, string) error
}

// ExactExecutionCatalog is consumed by project release verification. It never
// falls back to latest-by-output or a name-only recipe.
type ExactExecutionCatalog interface {
	FindExecutionBySelector(context.Context, string, string, DataframeSelector) (BundleExecution, BundleOutputRecord, error)
}

// PhaseError preserves operational context without making clients parse text.
type PhaseError struct {
	Phase  string
	Output string
	Err    error
}

func (e *PhaseError) Error() string {
	if e == nil || e.Err == nil {
		return "publication phase failed"
	}
	return e.Err.Error()
}

func (e *PhaseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func WithPhase(err error, phase, output string) error {
	if err == nil {
		return nil
	}
	return &PhaseError{Phase: phase, Output: output, Err: err}
}

var ErrBundleNotFound = fmt.Errorf("bundle execution not found")
var ErrBundlePointerConflict = fmt.Errorf("bundle pointer compare-and-swap conflict")
