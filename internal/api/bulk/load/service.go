package load

import (
	"context"
	"errors"
	"log/slog"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

type GenerationLoadRequest struct {
	Project          string
	Generation       string
	AuthResourcePath string
	StagedDir        string
	SubmittedBy      string
}

type ImportRequest struct {
	Project          string `json:"project"`
	ResourceType     string `json:"resource_type"`
	AuthResourcePath string `json:"auth_resource_path,omitempty"`
	Truncate         bool   `json:"truncate"`
	UseGeneric       bool   `json:"use_generic"`
	StagedFilePath   string `json:"-"`
	OriginalFilename string `json:"original_filename"`
	SubmittedBy      string `json:"submitted_by,omitempty"`
}

type ImportResult struct {
	Project          string              `json:"project"`
	ResourceType     string              `json:"resource_type"`
	AuthResourcePath string              `json:"auth_resource_path,omitempty"`
	OriginalFilename string              `json:"original_filename"`
	SubmittedBy      string              `json:"submitted_by,omitempty"`
	Summary          *ingest.LoadSummary `json:"summary,omitempty"`
}

type GenerationLoadResult struct {
	Project          string              `json:"project"`
	Generation       string              `json:"generation"`
	AuthResourcePath string              `json:"auth_resource_path,omitempty"`
	SubmittedBy      string              `json:"submitted_by,omitempty"`
	Summary          *ingest.LoadSummary `json:"summary,omitempty"`
}

type GenerationRunner interface {
	RunGeneration(ctx context.Context, req GenerationLoadRequest, sink ingest.EventSink) (ingest.LoadSummary, error)
}

type Runner interface {
	Run(ctx context.Context, req ImportRequest, sink ingest.EventSink) (ingest.LoadSummary, error)
}

type IngestRunner struct {
	BaseOptions ingest.LoadOptions
}

func (r IngestRunner) Run(ctx context.Context, req ImportRequest, sink ingest.EventSink) (ingest.LoadSummary, error) {
	opts := r.BaseOptions
	opts.Project = req.Project
	opts.AuthResourcePath = req.AuthResourcePath
	opts.Truncate = req.Truncate
	opts.UseGeneric = req.UseGeneric
	opts.EventSink = sink
	return ingest.LoadSingleResourceFile(ctx, opts, req.ResourceType, req.StagedFilePath)
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
	opts.Truncate = false
	opts.EventSink = sink
	return ingest.Load(ctx, opts)
}

type ServiceConfig struct {
	Runner           Runner
	GenerationRunner GenerationRunner
	Logger           *slog.Logger
	OnSuccess        func(project string)
}

type Service struct {
	runner           Runner
	generationRunner GenerationRunner
	logger           *slog.Logger
	onSuccess        func(project string)
}

func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Runner == nil && cfg.GenerationRunner == nil {
		return nil, errors.New("load runner is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{
		runner:           cfg.Runner,
		generationRunner: cfg.GenerationRunner,
		logger:           cfg.Logger,
		onSuccess:        cfg.OnSuccess,
	}, nil
}

func (s *Service) Run(ctx context.Context, req ImportRequest) (*ImportResult, error) {
	if s.runner == nil {
		return nil, errors.New("resource runner is not configured")
	}
	if req.Project == "" {
		return nil, errors.New("project is required")
	}
	if req.ResourceType == "" {
		return nil, errors.New("resource_type is required")
	}
	if req.StagedFilePath == "" {
		return nil, errors.New("staged file path is required")
	}

	summary, err := s.runner.Run(ctx, req, nil)
	if err != nil {
		s.logger.Error("resource load failed", "project", req.Project, "resource_type", req.ResourceType, "error", err.Error())
		return nil, err
	}
	if s.onSuccess != nil {
		s.onSuccess(req.Project)
	}
	s.logger.Info("resource load succeeded", "project", req.Project, "resource_type", req.ResourceType, "vertices", summary.VerticesInserted, "edges", summary.EdgesInserted)
	summaryCopy := summary
	return &ImportResult{
		Project:          req.Project,
		ResourceType:     req.ResourceType,
		AuthResourcePath: req.AuthResourcePath,
		OriginalFilename: req.OriginalFilename,
		SubmittedBy:      req.SubmittedBy,
		Summary:          &summaryCopy,
	}, nil
}

func (s *Service) RunGeneration(ctx context.Context, req GenerationLoadRequest) (*GenerationLoadResult, error) {
	if s.generationRunner == nil {
		return nil, errors.New("generation runner is not configured")
	}
	if req.Project == "" || req.Generation == "" || req.StagedDir == "" {
		return nil, errors.New("project, generation, and staged directory are required")
	}
	summary, err := s.generationRunner.RunGeneration(ctx, req, nil)
	if err != nil {
		s.logger.Error("generation load failed", "project", req.Project, "generation", req.Generation, "error", err.Error())
		return nil, err
	}
	if s.onSuccess != nil {
		s.onSuccess(req.Project)
	}
	s.logger.Info("generation load succeeded", "project", req.Project, "generation", req.Generation, "vertices", summary.VerticesInserted, "edges", summary.EdgesInserted)
	return &GenerationLoadResult{Project: req.Project, Generation: req.Generation, AuthResourcePath: req.AuthResourcePath, SubmittedBy: req.SubmittedBy, Summary: &summary}, nil
}
