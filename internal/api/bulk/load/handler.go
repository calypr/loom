package load

import (
	"fmt"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
)

type Handler struct {
	service   *Service
	authz     authscope.Authorizer
	snapshots *SnapshotService
	releases  *dataset.ReleaseService
}

type Config struct {
	Service    *Service
	Authorizer authscope.Authorizer
	Snapshots  *SnapshotService
	Releases   *dataset.ReleaseService
}

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("load service is required")
	}
	if cfg.Authorizer == nil {
		return nil, fmt.Errorf("load authorizer is required")
	}
	return &Handler{service: cfg.Service, authz: cfg.Authorizer, snapshots: cfg.Snapshots, releases: cfg.Releases}, nil
}
