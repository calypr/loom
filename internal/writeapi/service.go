package writeapi

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"arangodb-proto/internal/proto"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

type Principal struct {
	Subject             string            `json:"subject"`
	Claims              map[string]string `json:"claims,omitempty"`
	Projects            []string          `json:"projects,omitempty"`
	AuthResourcePaths   []string          `json:"auth_resource_paths,omitempty"`
	AuthorizationHeader string            `json:"-"`
}

type principalContextKey struct{}

func ContextWithPrincipal(ctx context.Context, principal *Principal) context.Context {
	if principal == nil {
		return ctx
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(*Principal)
	return principal, ok
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

type Event struct {
	Time   time.Time      `json:"time"`
	Name   string         `json:"name"`
	Fields map[string]any `json:"fields,omitempty"`
}

type Operation struct {
	ID               string             `json:"id"`
	Status           Status             `json:"status"`
	Project          string             `json:"project"`
	ResourceType     string             `json:"resource_type"`
	AuthResourcePath string             `json:"auth_resource_path,omitempty"`
	OriginalFilename string             `json:"original_filename"`
	SubmittedBy      string             `json:"submitted_by,omitempty"`
	SubmittedAt      time.Time          `json:"submitted_at"`
	StartedAt        *time.Time         `json:"started_at,omitempty"`
	CompletedAt      *time.Time         `json:"completed_at,omitempty"`
	Error            string             `json:"error,omitempty"`
	Summary          *proto.LoadSummary `json:"summary,omitempty"`
	EventCount       int                `json:"event_count"`
	StatusURL        string             `json:"status_url"`
	EventsURL        string             `json:"events_url"`
}

type Runner interface {
	Run(ctx context.Context, req ImportRequest, sink proto.EventSink) (proto.LoadSummary, error)
}

type ProtoRunner struct {
	BaseOptions proto.LoadOptions
}

func (r ProtoRunner) Run(ctx context.Context, req ImportRequest, sink proto.EventSink) (proto.LoadSummary, error) {
	opts := r.BaseOptions
	opts.Project = req.Project
	opts.AuthResourcePath = req.AuthResourcePath
	opts.Truncate = req.Truncate
	opts.UseGeneric = req.UseGeneric
	opts.EventSink = sink
	return proto.LoadSingleResourceFile(ctx, opts, req.ResourceType, req.StagedFilePath)
}

type ServiceConfig struct {
	Runner        Runner
	MaxConcurrent int
	Logger        *slog.Logger
	OnSuccess     func(project string)
}

type Service struct {
	runner    Runner
	sem       chan struct{}
	logger    *slog.Logger
	onSuccess func(project string)

	mu  sync.RWMutex
	ops map[string]*operationState
}

type operationState struct {
	mu sync.RWMutex

	op     Operation
	events []Event
}

func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Runner == nil {
		return nil, errors.New("runner is required")
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{
		runner:    cfg.Runner,
		sem:       make(chan struct{}, cfg.MaxConcurrent),
		logger:    cfg.Logger,
		onSuccess: cfg.OnSuccess,
		ops:       make(map[string]*operationState),
	}, nil
}

func (s *Service) Submit(ctx context.Context, req ImportRequest) (Operation, error) {
	if req.Project == "" {
		return Operation{}, errors.New("project is required")
	}
	if req.ResourceType == "" {
		return Operation{}, errors.New("resource_type is required")
	}
	if req.StagedFilePath == "" {
		return Operation{}, errors.New("staged file path is required")
	}

	now := time.Now().UTC()
	id := uuid.NewString()
	state := &operationState{
		op: Operation{
			ID:               id,
			Status:           StatusPending,
			Project:          req.Project,
			ResourceType:     req.ResourceType,
			AuthResourcePath: req.AuthResourcePath,
			OriginalFilename: req.OriginalFilename,
			SubmittedBy:      req.SubmittedBy,
			SubmittedAt:      now,
			StatusURL:        "/api/v1/imports/" + id,
			EventsURL:        "/api/v1/imports/" + id + "/events",
		},
	}
	state.events = append(state.events, Event{
		Time: now,
		Name: "import_submitted",
		Fields: map[string]any{
			"project":            req.Project,
			"resource_type":      req.ResourceType,
			"auth_resource_path": req.AuthResourcePath,
			"truncate":           req.Truncate,
			"use_generic":        req.UseGeneric,
			"original_filename":  req.OriginalFilename,
		},
	})
	state.op.EventCount = len(state.events)

	s.mu.Lock()
	s.ops[id] = state
	s.mu.Unlock()

	go s.runOperation(context.Background(), state, req)

	return state.snapshot(), nil
}

func (s *Service) Get(id string) (Operation, bool) {
	s.mu.RLock()
	state, ok := s.ops[id]
	s.mu.RUnlock()
	if !ok {
		return Operation{}, false
	}
	return state.snapshot(), true
}

func (s *Service) Events(id string) ([]Event, bool) {
	s.mu.RLock()
	state, ok := s.ops[id]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return state.eventSnapshot(), true
}

func (s *Service) runOperation(ctx context.Context, state *operationState, req ImportRequest) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	defer os.Remove(req.StagedFilePath)

	startedAt := time.Now().UTC()
	state.mu.Lock()
	state.op.Status = StatusRunning
	state.op.StartedAt = &startedAt
	state.events = append(state.events, Event{
		Time:   startedAt,
		Name:   "import_started",
		Fields: map[string]any{"resource_type": req.ResourceType, "project": req.Project},
	})
	state.op.EventCount = len(state.events)
	state.mu.Unlock()

	sink := func(event string, fields map[string]any) {
		state.appendEvent(event, fields)
	}

	summary, err := s.runner.Run(ctx, req, sink)

	completedAt := time.Now().UTC()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.op.CompletedAt = &completedAt
	if err != nil {
		state.op.Status = StatusFailed
		state.op.Error = err.Error()
		state.events = append(state.events, Event{
			Time:   completedAt,
			Name:   "import_failed",
			Fields: map[string]any{"error": err.Error()},
		})
		s.logger.Error("import failed", "import_id", state.op.ID, "project", req.Project, "resource_type", req.ResourceType, "error", err.Error())
	} else {
		state.op.Status = StatusSucceeded
		summaryCopy := summary
		state.op.Summary = &summaryCopy
		state.events = append(state.events, Event{
			Time: completedAt,
			Name: "import_succeeded",
			Fields: map[string]any{
				"files":             summary.Files,
				"vertices_inserted": summary.VerticesInserted,
				"edges_inserted":    summary.EdgesInserted,
			},
		})
		s.logger.Info("import succeeded", "import_id", state.op.ID, "project", req.Project, "resource_type", req.ResourceType, "vertices", summary.VerticesInserted, "edges", summary.EdgesInserted)
		if s.onSuccess != nil {
			s.onSuccess(req.Project)
		}
	}
	state.op.EventCount = len(state.events)
}

func (o *operationState) appendEvent(name string, fields map[string]any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	copied := make(map[string]any, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	o.events = append(o.events, Event{
		Time:   time.Now().UTC(),
		Name:   name,
		Fields: copied,
	})
	o.op.EventCount = len(o.events)
}

func (o *operationState) snapshot() Operation {
	o.mu.RLock()
	defer o.mu.RUnlock()
	snapshot := o.op
	if o.op.Summary != nil {
		summaryCopy := *o.op.Summary
		snapshot.Summary = &summaryCopy
	}
	return snapshot
}

func (o *operationState) eventSnapshot() []Event {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]Event, 0, len(o.events))
	for _, event := range o.events {
		fields := make(map[string]any, len(event.Fields))
		for k, v := range event.Fields {
			fields[k] = v
		}
		out = append(out, Event{
			Time:   event.Time,
			Name:   event.Name,
			Fields: fields,
		})
	}
	return out
}
