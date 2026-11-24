package sourceload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/ingest/source"
	"strings"

	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	SourceLoadUploading      = "UPLOADING"
	SourceLoadReady          = "READY"
	SourceLoadPublishPending = "PUBLISH_PENDING"
	SourceLoadPublished      = "PUBLISHED"
	SourceLoadSuperseded     = "SUPERSEDED"
)

type SourceManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
}

type SourceLoadPlan struct {
	Repository       string               `json:"repository"`
	Commit           string               `json:"commit"`
	Files            []SourceManifestFile `json:"files"`
	AuthResourcePath string               `json:"auth_resource_path"`
	ExpectedRevision *int64               `json:"expected_revision,omitempty"`
}

type SourceFileReceipt struct {
	Path         string `json:"path"`
	ResourceType string `json:"resource_type"`
	SHA256       string `json:"sha256"`
	Rows         int64  `json:"rows"`
	State        string `json:"state"`
}

type SourceLoadStatus struct {
	OperationID      string              `json:"operation_id"`
	Generation       string              `json:"generation"`
	State            string              `json:"state"`
	BaseRevision     int64               `json:"base_revision"`
	Files            []SourceFileReceipt `json:"files"`
	AuthResourcePath string              `json:"-"`
}

type SourceFileValidationError struct {
	Issues      []row.RowErrorSample `json:"issues"`
	TotalErrors int64                `json:"total_errors"`
	Truncated   bool                 `json:"truncated"`
}

func (e *SourceFileValidationError) Error() string {
	if e == nil {
		return "source file validation failed"
	}
	return fmt.Sprintf("source file validation failed for %d row(s)", e.TotalErrors)
}

type SourceLoadMaterializer func(context.Context, string, string, string, string, int64) (executionID string, sourceRevision int64, err error)

type SourceLoadPublisherConfig struct {
	Client               *arangostore.Client
	DatasetStore         *datasetarango.Store
	BatchSize            int
	WriteAPI             string
	ValidateProject      func(context.Context, string) error
	MaterializeAndSelect SourceLoadMaterializer
}

type SourceLoadStore interface {
	Begin(context.Context, string, string, SourceLoadPlan) (SourceLoadStatus, error)
	Status(context.Context, string, string) (SourceLoadStatus, error)
	UploadFile(context.Context, string, string, string, string) (SourceFileReceipt, error)
	Publish(context.Context, string, string) (SourcePublishReceipt, error)
}

var (
	ErrSourceLoadNotFound   = errors.New("source load not found")
	ErrSourceLoadIncomplete = errors.New("source load is missing selected files")
	ErrSourceLoadInProgress = errors.New("source load operation is already in progress")
)

const sourceLoadCollection = "loom_source_loads"

type RepositorySourceLoader struct {
	client          *arangostore.Client
	datasets        *datasetarango.Store
	schema          *source.RuntimeFHIRSchema
	batchSize       int
	writeAPI        string
	validateProject func(context.Context, string) error
	materialize     SourceLoadMaterializer
}

var _ SourceLoadStore = (*RepositorySourceLoader)(nil)

func NewRepositorySourceLoader(cfg SourceLoadPublisherConfig) (*RepositorySourceLoader, error) {
	if cfg.Client == nil || cfg.DatasetStore == nil || cfg.MaterializeAndSelect == nil {
		return nil, fmt.Errorf("source load publisher requires an Arango client, dataset store, and release callback")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.BatchSize > 10000 {
		return nil, fmt.Errorf("source load batch size must not exceed 10000")
	}
	if cfg.WriteAPI == "" {
		cfg.WriteAPI = "import"
	}
	return &RepositorySourceLoader{
		client: cfg.Client, datasets: cfg.DatasetStore, schema: source.LoadRuntimeFHIRSchema(),
		batchSize: cfg.BatchSize, writeAPI: cfg.WriteAPI, validateProject: cfg.ValidateProject,
		materialize: cfg.MaterializeAndSelect,
	}, nil
}

func canonicalizeSourceLoadPlan(plan SourceLoadPlan) (SourceLoadPlan, SourceManifest, string, error) {
	manifest := SourceManifest{Repository: plan.Repository, Commit: plan.Commit, Files: make([]SourceFile, len(plan.Files))}
	for i, file := range plan.Files {
		manifest.Files[i] = SourceFile(file)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return SourceLoadPlan{}, SourceManifest{}, "", fmt.Errorf("encode source-load plan: %w", err)
	}
	canonical, hash, err := ParseSourceManifest(encoded)
	if err != nil {
		return SourceLoadPlan{}, SourceManifest{}, "", err
	}
	if plan.ExpectedRevision != nil && *plan.ExpectedRevision < 0 {
		return SourceLoadPlan{}, SourceManifest{}, "", fmt.Errorf("expected revision must be non-negative")
	}
	plan.Repository, plan.Commit = canonical.Repository, canonical.Commit
	plan.Files = make([]SourceManifestFile, len(canonical.Files))
	for i, file := range canonical.Files {
		plan.Files[i] = SourceManifestFile(file)
	}
	plan.AuthResourcePath = strings.TrimSpace(plan.AuthResourcePath)
	return plan, canonical, hash, nil
}

func sourceLoadStatus(record sourceLoadRecord) SourceLoadStatus {
	files := make([]SourceFileReceipt, 0, len(record.Receipts))
	for _, planned := range record.Plan.Files {
		if receipt, ok := record.Receipts[planned.Path]; ok {
			files = append(files, receipt)
		}
	}
	return SourceLoadStatus{
		OperationID: record.OperationID, Generation: record.Generation, State: record.State,
		BaseRevision: record.BaseRevision, Files: files, AuthResourcePath: record.Plan.AuthResourcePath,
	}
}
