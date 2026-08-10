package load

import (
	"fmt"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
	"github.com/gofiber/fiber/v3"
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

func (h *Handler) RegisterRoutes(router fiber.Router) {
	h.RegisterResourceRoute(router)
	router.Post("/api/v1/datasets/:project/generations/:generation", h.createGeneration)
	router.Post("/api/v1/datasets/:project/generations/:generation/activate", h.activateGeneration)
	router.Put("/api/v1/raw", h.loadRaw)
	h.RegisterSnapshotRoutes(router)
}
