package load

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

type Handler struct {
	service      *Service
	authz        authscope.Authorizer
	sourceLoader SourceLoader
	sourceLimits SourceLoadLimits
}

type SourceLoader interface {
	Begin(context.Context, string, string, ingest.SourceLoadPlan) (ingest.SourceLoadStatus, error)
	Status(context.Context, string, string) (ingest.SourceLoadStatus, error)
	UploadFile(context.Context, string, string, string, string) (ingest.SourceFileReceipt, error)
	Publish(context.Context, string, string) (ingest.SourcePublishReceipt, error)
}

type SourceLoadLimits struct {
	MaxFileBytes int64
}

type Config struct {
	Service          *Service
	Authorizer       authscope.Authorizer
	SourceLoader     SourceLoader
	SourceLoadLimits SourceLoadLimits
}

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("load service is required")
	}
	if cfg.Authorizer == nil {
		return nil, fmt.Errorf("load authorizer is required")
	}
	if cfg.SourceLoadLimits.MaxFileBytes <= 0 {
		cfg.SourceLoadLimits.MaxFileBytes = defaultSourceLoadMaxFileBytes
	}
	return &Handler{service: cfg.Service, authz: cfg.Authorizer, sourceLoader: cfg.SourceLoader, sourceLimits: cfg.SourceLoadLimits}, nil
}
