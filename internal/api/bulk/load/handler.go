package load

import (
	"fmt"
	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	service *Service
	authz   authscope.Authorizer
}

type Config struct {
	Service    *Service
	Authorizer authscope.Authorizer
}

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("load service is required")
	}
	if cfg.Authorizer == nil {
		return nil, fmt.Errorf("load authorizer is required")
	}
	return &Handler{service: cfg.Service, authz: cfg.Authorizer}, nil
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	router.Post("/api/v1/datasets/:project/generations/:generation", h.createGeneration)
	router.Put("/api/v1/raw", h.loadRaw)
}
