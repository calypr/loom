package publication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

type DataframeSelector = dataset.DataframeSelector

type BundleState string

const (
	BundleQueued     BundleState = "QUEUED"
	BundleRunning    BundleState = "RUNNING"
	BundleValidating BundleState = "VALIDATING"
	BundlePublished  BundleState = "PUBLISHED"
	BundleFailed     BundleState = "FAILED"

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
	OutputName         string   `json:"outputName,omitempty"`
	Project            string   `json:"project"`
	DatasetGeneration  string   `json:"datasetGeneration"`
	RecipeDigest       string   `json:"recipeDigest"`
	SchemaDigest       string   `json:"schemaDigest"`
	ReceiptID          string   `json:"receiptId,omitempty"`
	ScopeDigest        string   `json:"scopeDigest"`
	EngineVersion      string   `json:"engineVersion"`
	AuthScopeMode      string   `json:"authScopeMode,omitempty"`
	AuthResourcePaths  []string `json:"authResourcePaths,omitempty"`
}

func (i BundleIdentity) Canonical() BundleIdentity {
	if i.AuthResourcePaths == nil {
		return i
	}
	paths := append([]string(nil), i.AuthResourcePaths...)
	sort.Strings(paths)
	unique := paths[:0]
	for _, path := range paths {
		if len(unique) == 0 || unique[len(unique)-1] != path {
			unique = append(unique, path)
		}
	}
	i.AuthResourcePaths = unique
	return i
}

func (i BundleIdentity) PointerName() string {
	parts := []string{i.Project, i.DatasetGeneration, i.Name}
	if strings.TrimSpace(i.TranslationVersion) != "" {
		parts = append(parts, i.TranslationVersion)
	}
	if strings.TrimSpace(i.OutputName) != "" {
		parts = append(parts, i.OutputName)
	}
	var name strings.Builder
	for _, part := range parts {
		name.WriteString(strconv.Itoa(len(part)))
		name.WriteByte(':')
		name.WriteString(part)
	}
	return name.String()
}

func (i BundleIdentity) Key() string {
	i = i.Canonical()
	b, _ := json.Marshal(struct {
		Name, Project, DatasetGeneration string
		TranslationVersion               string `json:"TranslationVersion,omitempty"`
		OutputName                       string `json:"OutputName,omitempty"`
		RecipeDigest, SchemaDigest       string
		ReceiptID                        string `json:"ReceiptID,omitempty"`
		ScopeDigest, EngineVersion       string
		AuthScopeMode                    string   `json:"AuthScopeMode,omitempty"`
		AuthResourcePaths                []string `json:"AuthResourcePaths,omitempty"`
	}{i.Name, i.Project, i.DatasetGeneration, i.TranslationVersion, i.OutputName, i.RecipeDigest, i.SchemaDigest, i.ReceiptID, i.ScopeDigest, i.EngineVersion, i.AuthScopeMode, i.AuthResourcePaths})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type BundleOutputRecord struct {
	Name             string             `json:"name"`
	PhysicalTable    string             `json:"physicalTable"`
	Selector         DataframeSelector  `json:"selector"`
	Columns          []PhysicalColumn   `json:"columns,omitempty"`
	RowCount         int64              `json:"rowCount"`
	ByteCount        int64              `json:"byteCount"`
	State            BundleState        `json:"state"`
	FailureCode      string             `json:"failureCode,omitempty"`
	FailureRetryable bool               `json:"failureRetryable,omitempty"`
	VerifiedAt       *time.Time         `json:"verifiedAt,omitempty"`
	FailurePhase     string             `json:"failurePhase,omitempty"`
	FailureDetails   string             `json:"failureDetails,omitempty"`
	SourceRow        *SourceRowMetadata `json:"sourceRow,omitempty"`
}

type SourceRowMetadata struct {
	ResourceType string `json:"resourceType"`
	IDColumn     string `json:"idColumn"`
}

func (m *SourceRowMetadata) Valid() bool {
	return m != nil && strings.TrimSpace(m.ResourceType) != "" && strings.TrimSpace(m.IDColumn) != ""
}

func (e BundleExecution) Selector(output string) DataframeSelector {
	return DataframeSelector{Recipe: e.Name, TranslationVersion: e.TranslationVersion, Output: output}
}

func (o BundleOutputRecord) Queryable() bool {
	return o.State.Successful() && o.VerifiedAt != nil && strings.TrimSpace(o.PhysicalTable) != ""
}

type PhysicalColumn struct {
	ID           string           `json:"id,omitempty"`
	Name         string           `json:"name"`
	SemanticPath string           `json:"semanticPath,omitempty"`
	ClickHouse   string           `json:"clickhouseType"`
	LogicalType  string           `json:"logicalType,omitempty"`
	Nullable     bool             `json:"nullable,omitempty"`
	Repeated     bool             `json:"repeated,omitempty"`
	Provenance   ColumnProvenance `json:"provenance,omitempty"`
	LoomOwned    bool             `json:"loomOwned,omitempty"`
}

type BundleExecution struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	BundleIdentity
	State            BundleState          `json:"state"`
	Outputs          []BundleOutputRecord `json:"outputs,omitempty"`
	QualityReports   []QualityReport      `json:"qualityReports,omitempty"`
	CreatedAt        time.Time            `json:"createdAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
	ReadyAt          *time.Time           `json:"readyAt,omitempty"`
	PublishedAt      *time.Time           `json:"publishedAt,omitempty"`
	Error            string               `json:"error,omitempty"`
	FailureCode      string               `json:"failureCode,omitempty"`
	FailureRetryable bool                 `json:"failureRetryable,omitempty"`
	OwnerID          string               `json:"ownerId,omitempty"`
	LeaseExpiresAt   *time.Time           `json:"leaseExpiresAt,omitempty"`
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

type BundleCatalog interface {
	SaveExecution(context.Context, BundleExecution, string) error
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

type BundleExecutionPageFunc func([]BundleExecution) error

type PagedBundleCatalog interface {
	VisitExecutionPages(context.Context, BundleState, time.Time, int, BundleExecutionPageFunc) error
}

type ExactExecutionCatalog interface {
	FindExecutionBySelector(context.Context, string, string, DataframeSelector) (BundleExecution, BundleOutputRecord, error)
}

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
var ErrBundleLeaseLost = fmt.Errorf("bundle lease ownership was lost")

var ErrExecutionReadPinLost = fmt.Errorf("published execution read pin was lost")
var ErrExecutionReadPinActive = fmt.Errorf("published execution has active readers")
var ErrSelectionSourceNotAddressable = fmt.Errorf("published output is not addressable to source resources")
var ErrSelectionSourceIdentityChanged = fmt.Errorf("published selection source identity changed")

type ExecutionReadPinCatalog interface {
	AcquireExecutionReadPin(context.Context, string, string, time.Time) (bool, error)
	RenewExecutionReadPin(context.Context, string, string, time.Time) (bool, error)
	ReleaseExecutionReadPin(context.Context, string, string) error
	ClaimExecutionCleanup(context.Context, string, string) (bool, error)
	RenewExecutionCleanup(context.Context, string, string, time.Time) (bool, error)
	ReleaseExecutionCleanup(context.Context, string, string) error
}

type SourceRowMetadataWriter interface {
	SetSourceRowMetadata(context.Context, string, SourceRowMetadata) error
}
