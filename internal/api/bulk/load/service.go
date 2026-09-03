package load

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

type GenerationLoadRequest struct {
	Project          string
	Generation       string
	AuthResourcePath string
	StagedDir        string
	SubmittedBy      string
	DeferActivation  bool
}

type GenerationLoadResult struct {
	Project          string              `json:"project"`
	Generation       string              `json:"generation"`
	AuthResourcePath string              `json:"auth_resource_path,omitempty"`
	SubmittedBy      string              `json:"submitted_by,omitempty"`
	Summary          *ingest.LoadSummary `json:"summary,omitempty"`
	Activated        bool                `json:"activated"`
	Reused           bool                `json:"reused,omitempty"`
}

type GenerationStatusResult struct {
	Project    string            `json:"project"`
	Generation string            `json:"generation"`
	State      publication.State `json:"state"`
	Reusable   bool              `json:"reusable"`
}

type GenerationActivator interface {
	ReadManifest(context.Context, publication.Ref) (publication.Manifest, error)
	Activate(context.Context, publication.Manifest) error
}

type DataframeReleaseStore interface {
	GetExecution(context.Context, string) (dataframepublication.BundleExecution, error)
	GetPointer(context.Context, string) (dataframepublication.BundlePointer, error)
}

type GenerationLoader func(context.Context, GenerationLoadRequest, ingest.EventSink) (ingest.LoadSummary, error)

type IngestRunner struct {
	BaseOptions ingest.LoadOptions
}

func (r IngestRunner) RunGeneration(ctx context.Context, req GenerationLoadRequest, sink ingest.EventSink) (ingest.LoadSummary, error) {
	ref, err := publication.NewRef(req.Project, req.Generation)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	opts := r.BaseOptions
	opts.Project = req.Project
	opts.AuthResourcePath = req.AuthResourcePath
	opts.MetaDir = req.StagedDir
	opts.Dataset = &ref
	opts.DeferActivation = req.DeferActivation
	opts.Truncate = false
	opts.EventSink = sink
	// StageOnly is a CLI mode. The HTTP API stages through DeferActivation.
	opts.StageOnly = false
	return ingest.Load(ctx, opts)
}

type ServiceConfig struct {
	LoadGeneration      GenerationLoader
	Logger              *slog.Logger
	OnSuccess           func(project string)
	GenerationActivator GenerationActivator
	DataframeReleases   DataframeReleaseStore
}

type Service struct {
	loadGeneration      GenerationLoader
	logger              *slog.Logger
	onSuccess           func(project string)
	generationActivator GenerationActivator
	dataframeReleases   DataframeReleaseStore
}

func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.LoadGeneration == nil {
		return nil, errors.New("load runner is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{
		loadGeneration:      cfg.LoadGeneration,
		logger:              cfg.Logger,
		onSuccess:           cfg.OnSuccess,
		generationActivator: cfg.GenerationActivator,
		dataframeReleases:   cfg.DataframeReleases,
	}, nil
}

func (s *Service) generationStatus(ctx context.Context, project, generation string) (*GenerationStatusResult, error) {
	if project == "" || generation == "" {
		return nil, errors.New("project and generation are required")
	}
	if s.generationActivator == nil {
		return nil, errors.New("generation manifest reader is not configured")
	}
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		return nil, err
	}
	manifest, err := s.generationActivator.ReadManifest(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &GenerationStatusResult{
		Project: ref.Project, Generation: ref.Generation,
		State: manifest.State, Reusable: manifest.IsStaged(),
	}, nil
}

func (s *Service) runGeneration(ctx context.Context, req GenerationLoadRequest) (*GenerationLoadResult, error) {
	if req.Project == "" || req.Generation == "" || req.StagedDir == "" {
		return nil, errors.New("project, generation, and staged directory are required")
	}
	if s.generationActivator != nil {
		ref, err := publication.NewRef(req.Project, req.Generation)
		if err != nil {
			return nil, err
		}
		manifest, err := s.generationActivator.ReadManifest(ctx, ref)
		switch {
		case err == nil && manifest.IsStaged():
			s.logger.Info("reusing ready generation", "project", req.Project, "generation", req.Generation)
			return &GenerationLoadResult{
				Project: req.Project, Generation: req.Generation,
				AuthResourcePath: req.AuthResourcePath, SubmittedBy: req.SubmittedBy,
				Activated: false, Reused: true,
			}, nil
		case err == nil:
			return nil, fmt.Errorf("generation %s/%s already exists in state %s", req.Project, req.Generation, manifest.State)
		case !errors.Is(err, publication.ErrManifestNotFound):
			return nil, fmt.Errorf("inspect existing generation: %w", err)
		}
	}
	summary, err := s.loadGeneration(ctx, req, nil)
	if err != nil {
		s.logger.Error("generation load failed", "project", req.Project, "generation", req.Generation, "error", err.Error())
		return nil, err
	}
	if s.onSuccess != nil {
		s.onSuccess(req.Project)
	}
	s.logger.Info("generation load succeeded", "project", req.Project, "generation", req.Generation, "vertices", summary.VerticesInserted, "edges", summary.EdgesInserted)
	return &GenerationLoadResult{Project: req.Project, Generation: req.Generation, AuthResourcePath: req.AuthResourcePath, SubmittedBy: req.SubmittedBy, Summary: &summary, Activated: !req.DeferActivation}, nil
}

// activateGeneration performs the release switch only after the exact
// dataframe bundle supplied by the caller is durably successful and remains
// the active pointer for its recipe/generation namespace.
func (s *Service) activateGeneration(ctx context.Context, project, generation, executionID string) error {
	fail := func(err error) error {
		if err != nil {
			s.logger.Error("generation release activation failed", "project", project, "generation", generation, "dataframe_execution_id", executionID, "error", err)
		}
		return err
	}
	if s.generationActivator == nil || s.dataframeReleases == nil {
		return fail(errors.New("generation release activation is not configured"))
	}
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		return fail(err)
	}
	execution, err := s.dataframeReleases.GetExecution(ctx, executionID)
	if err != nil {
		return fail(fmt.Errorf("load dataframe release %q: %w", executionID, err))
	}
	if execution.Project != project || execution.DatasetGeneration != generation || !execution.State.Successful() || len(execution.Outputs) == 0 {
		return fail(fmt.Errorf("dataframe release %q is not a published bundle for %s/%s", executionID, project, generation))
	}
	for _, output := range execution.Outputs {
		if !output.State.Successful() {
			return fail(fmt.Errorf("dataframe release %q output %q is not published", executionID, output.Name))
		}
	}
	pointer, err := s.dataframeReleases.GetPointer(ctx, execution.PointerName())
	if err != nil {
		return fail(fmt.Errorf("resolve dataframe release pointer: %w", err))
	}
	if pointer.ExecutionID != execution.ID {
		return fail(fmt.Errorf("dataframe release %q is no longer active", executionID))
	}
	manifest, err := s.generationActivator.ReadManifest(ctx, ref)
	if err != nil {
		return fail(fmt.Errorf("read generation release: %w", err))
	}
	if !manifest.IsStaged() {
		return fail(fmt.Errorf("generation %s/%s is not READY", project, generation))
	}
	if err := s.generationActivator.Activate(ctx, manifest); err != nil {
		return fail(fmt.Errorf("activate generation release: %w", err))
	}
	if s.onSuccess != nil {
		s.onSuccess(project)
	}
	return nil
}
