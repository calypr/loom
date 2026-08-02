package ingest

import (
	"errors"
	"fmt"
	"os"

	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type LoadOptions struct {
	arangostore.ConnectionOptions
	Schema           string
	MetaDir          string
	Project          string
	AuthResourcePath string
	BatchSize        int
	ProgressEvery    int
	WriterCount      int
	Truncate         bool
	FailFast         bool
	UseGeneric       bool
	WriteAPI         string
	EventSink        EventSink
	// Dataset identifies the complete immutable generation being loaded. The
	// generation activates only after every graph file and catalog finalization
	// succeeds.
	Dataset *publication.Ref
	// PreflightSampleRows bounds the number of payloads inspected from every
	// staged file before Loom opens or mutates Arango. Zero uses the safe
	// default; full row validation still happens in the loader.
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
	Files            int                `json:"files"`
	VerticesInserted int                `json:"vertices_inserted"`
	EdgesInserted    int                `json:"edges_inserted"`
	ValidationErrors int                `json:"validation_errors"`
	GenerationErrors int                `json:"generation_errors"`
	EdgeErrors       int                `json:"edge_errors"`
	BatchCounts      map[string]int     `json:"batch_counts,omitempty"`
	Resources        map[string]int     `json:"resources"`
	StageSeconds     map[string]float64 `json:"stage_seconds"`
	Preflight        PreflightReport    `json:"preflight"`
	RowErrors        []RowErrorSample   `json:"row_errors,omitempty"`
	// SchemaIdentity is the exact configured graph-schema evidence used for
	// this load. It remains nil when Loom cannot load the configured schema, so
	// an early failure never looks like a successful schema observation.
	SchemaIdentity *publication.SchemaSnapshot `json:"schema_identity,omitempty"`
	// Dataset is the immutable target when this was a generation load. It is
	// present even on a failed generation load so callers can identify the
	// inactive manifest that needs operational inspection.
	Dataset *publication.Ref `json:"dataset,omitempty"`
}

// normalizeLoadOptions applies the operational defaults shared by both loader
// modes. The immutable-generation loader deliberately has a different write
// lifecycle, but both modes must agree on batching, progress, concurrency, and
// the Arango write API before any input is examined.
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
	if opts.WriteAPI == "" {
		opts.WriteAPI = "import"
	}
	return opts
}

var (
	// ErrGenerationLoadRequiresDirectory prevents a one-file or arbitrary-file
	// load from being mistaken for a complete immutable dataset snapshot.
	ErrGenerationLoadRequiresDirectory = errors.New("dataset generation load requires a directory")
	// ErrGenerationLoadRequiresFiles prevents an empty staged directory from
	// becoming an active but meaningless generation.
	ErrGenerationLoadRequiresFiles = errors.New("dataset generation load requires at least one NDJSON file")
	// ErrGenerationLoadTruncateForbidden prevents a new generation from
	// deleting active or historical graph data.
	ErrGenerationLoadTruncateForbidden = errors.New("dataset generation load cannot truncate collections")
	// ErrGenerationDatasetProjectMismatch prevents graph documents and their
	// lifecycle manifest from being scoped to different projects.
	ErrGenerationDatasetProjectMismatch = errors.New("dataset generation project does not match load project")
	ErrGenerationDatasetRequired        = errors.New("dataset generation is required")
)

// ActivationOutcomeError means the generation reached READY but Loom could
// not prove that the active-generation pointer was updated. READY is kept for
// an operator to reconcile; it must never be downgraded to FAILED because the
// activation request may have committed before its error reached the caller.
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

// GenerationLoadIncompleteError means row-level validation, generation, or
// edge construction failures were observed while FailFast was disabled. The
// graph may contain partial immutable documents, but the lifecycle manifest is
// deliberately left FAILED and cannot be selected for reads.
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

// newGenerationLoadPlan validates and snapshots all immutable information
// after input preflight and before a database connection is opened.
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
