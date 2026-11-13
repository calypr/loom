package ingest

import (
	"errors"
	"fmt"
	"os"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
)

type LoadOptions struct {
	ConnectionOptions
	Schema              string
	MetaDir             string
	Project             string
	AuthResourcePath    string
	BatchSize           int
	ProgressEvery       int
	WriterCount         int
	WorkerCount         int
	LineQueueSize       int
	WriteQueueSize      int
	CatalogLimits       catalog.ProfileLimits
	Truncate            bool
	FailFast            bool
	UseGeneric          bool
	WriteAPI            string
	EventSink           EventSink
	Dataset             *publication.Ref
	StageOnly           bool
	DeferActivation     bool
	PreflightSampleRows int
}

type RowErrorSample struct {
	File         string `json:"file"`
	Line         int    `json:"line"`
	ResourceType string `json:"resource_type"`
	Category     string `json:"category"`
	Message      string `json:"message"`
}

type LoadSummary struct {
	Files            int                         `json:"files"`
	VerticesInserted int                         `json:"vertices_inserted"`
	EdgesInserted    int                         `json:"edges_inserted"`
	ValidationErrors int                         `json:"validation_errors"`
	GenerationErrors int                         `json:"generation_errors"`
	EdgeErrors       int                         `json:"edge_errors"`
	BatchCounts      map[string]int              `json:"batch_counts,omitempty"`
	Resources        map[string]int              `json:"resources"`
	StageSeconds     map[string]float64          `json:"stage_seconds"`
	Preflight        PreflightReport             `json:"preflight"`
	RowErrors        []RowErrorSample            `json:"row_errors,omitempty"`
	SchemaIdentity   *publication.SchemaSnapshot `json:"schema_identity,omitempty"`
	Dataset          *publication.Ref            `json:"dataset,omitempty"`
}

func normalizeLoadOptions(opts LoadOptions) LoadOptions {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 50000
	}
	if opts.WriterCount <= 0 {
		opts.WriterCount = 8
	}
	if opts.WorkerCount <= 0 {
		opts.WorkerCount = opts.WriterCount
	}
	if opts.LineQueueSize <= 0 {
		opts.LineQueueSize = 1024
	}
	if opts.WriteQueueSize <= 0 {
		opts.WriteQueueSize = 16
	}
	opts.CatalogLimits = catalog.NormalizeProfileLimits(opts.CatalogLimits)
	if opts.WriteAPI == "" {
		opts.WriteAPI = "import"
	}
	return opts
}

var (
	ErrGenerationLoadRequiresDirectory     = errors.New("dataset generation load requires a directory")
	ErrGenerationLoadRequiresFiles         = errors.New("dataset generation load requires at least one NDJSON file")
	ErrGenerationLoadTruncateForbidden     = errors.New("dataset generation load cannot truncate collections")
	ErrGenerationDatasetProjectMismatch    = errors.New("dataset generation project does not match load project")
	ErrGenerationSingleResourceUnsupported = errors.New("single-resource imports cannot create a dataset generation")
	ErrGenerationDatasetRequired           = errors.New("dataset generation is required")
)

type ActivationOutcomeError struct {
	Dataset publication.Ref
	Err     error
}

func (e *ActivationOutcomeError) Error() string {
	if e == nil {
		return "dataset generation activation outcome is unknown"
	}
	return fmt.Sprintf("activate dataset generation %s/%s: %v", e.Dataset.Project, e.Dataset.Generation, e.Err)
}

func (e *ActivationOutcomeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type GenerationLoadIncompleteError struct {
	Dataset          publication.Ref
	ValidationErrors int
	GenerationErrors int
	EdgeErrors       int
}

func (e *GenerationLoadIncompleteError) Error() string {
	if e == nil {
		return "dataset generation load is incomplete"
	}
	return fmt.Sprintf(
		"dataset generation %s/%s is incomplete: validation_errors=%d generation_errors=%d edge_errors=%d",
		e.Dataset.Project,
		e.Dataset.Generation,
		e.ValidationErrors,
		e.GenerationErrors,
		e.EdgeErrors,
	)
}

type generationLoadPlan struct {
	Dataset  publication.Ref
	Manifest publication.Manifest
}

func newGenerationLoadPlan(opts LoadOptions, files []string, schemaSnapshot publication.SchemaSnapshot) (*generationLoadPlan, error) {
	if opts.Dataset == nil {
		return nil, ErrGenerationDatasetRequired
	}

	ref := *opts.Dataset
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Project != opts.Project {
		return nil, fmt.Errorf("%w: dataset project %q, load project %q", ErrGenerationDatasetProjectMismatch, ref.Project, opts.Project)
	}
	if opts.Truncate {
		return nil, ErrGenerationLoadTruncateForbidden
	}
	info, err := os.Stat(opts.MetaDir)
	if err != nil {
		return nil, fmt.Errorf("inspect dataset generation directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %q", ErrGenerationLoadRequiresDirectory, opts.MetaDir)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrGenerationLoadRequiresFiles, opts.MetaDir)
	}

	manifest, err := publication.NewManifest(ref, schemaSnapshot)
	if err != nil {
		return nil, fmt.Errorf("create dataset generation manifest: %w", err)
	}
	return &generationLoadPlan{Dataset: ref, Manifest: manifest}, nil
}
