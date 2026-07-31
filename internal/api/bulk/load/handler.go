package load

import (
	"fmt"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	service                      *Service
	authz                        authscope.Authorizer
	scopeResolver                *authscope.ScopeResolver
	disableSingleResourceImports bool
}

type Config struct {
	Service                      *Service
	Authorizer                   authscope.Authorizer
	ScopeResolver                *authscope.ScopeResolver
	DisableSingleResourceImports bool
}

func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("load service is required")
	}
	if cfg.Authorizer == nil {
		cfg.Authorizer = authscope.AllowAllAuthorizer{}
	}
	return &Handler{service: cfg.Service, authz: cfg.Authorizer, scopeResolver: cfg.ScopeResolver, disableSingleResourceImports: cfg.DisableSingleResourceImports}, nil
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	router.Put("/api/v1/projects/:project/resources/:resourceType", h.bulkResource)
	router.Post("/api/v1/datasets/:project/generations/:generation", h.createGeneration)
	router.Put("/api/v1/raw", h.loadRaw)
	api := router.Group("/api/v1")
	if h.disableSingleResourceImports {
		api.Post("/imports", func(c fiber.Ctx) error {
			return &httpapi.Error{Status: fiber.StatusConflict, Code: "legacy_import_disabled", Message: "single-resource imports are disabled while dataset-generation mode is enabled; load a complete dataset generation instead"}
		})
	} else {
		api.Post("/imports", h.createImport)
	}
}

type apiError = httpapi.Error
