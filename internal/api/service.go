package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/calypr/loom/internal/ingest"
)

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

type ServiceConfig struct {
	Runner    Runner
	Logger    *slog.Logger
	OnSuccess func(project string)
}

type Service struct {
	runner    Runner
	logger    *slog.Logger
	onSuccess func(project string)
}

func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Runner == nil {
		return nil, errors.New("runner is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{
		runner:    cfg.Runner,
		logger:    cfg.Logger,
		onSuccess: cfg.OnSuccess,
	}, nil
}

func (s *Service) Run(ctx context.Context, req ImportRequest) (*ImportResult, error) {
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
		s.logger.Error("import failed", "project", req.Project, "resource_type", req.ResourceType, "error", err.Error())
		return nil, err
	}
	if s.onSuccess != nil {
		s.onSuccess(req.Project)
	}
	s.logger.Info("import succeeded", "project", req.Project, "resource_type", req.ResourceType, "vertices", summary.VerticesInserted, "edges", summary.EdgesInserted)
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
