package load

import (
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

type HTTPConfig struct {
	Service       *Service
	Authenticator authscope.Authenticator
	Authorizer    authscope.Authorizer
}

func NewHTTPServer(cfg HTTPConfig) (*httpapi.HTTPServer, error) {
	s, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: cfg.Authenticator, Authorizer: cfg.Authorizer})
	if err != nil {
		return nil, err
	}
	h, err := NewHandler(Config{Service: cfg.Service, Authorizer: cfg.Authorizer})
	if err != nil {
		return nil, err
	}
	registerTestRoutes(s.App(), h)
	return s, nil
}

// registerTestRoutes keeps package-level handler tests focused on transport
// behavior. Production route ownership lives exclusively in generated/loomapi.
func registerTestRoutes(router fiber.Router, h *Handler) {
	router.Put("/api/v1/projects/:project/resources/:resourceType", h.HandleResource)
	router.Post("/api/v1/datasets/:project/generations/:generation", h.HandleCreateGeneration)
	router.Post("/api/v1/datasets/:project/generations/:generation/activate", h.HandleActivateGeneration)
	registerTestSnapshotRoutes(router, h)
}

func registerTestSnapshotRoutes(router fiber.Router, h *Handler) {
	if h.snapshots != nil {
		router.Post("/api/v1/projects/:project/generations/:generation", h.HandleCreateSnapshot)
		router.Get("/api/v1/projects/:project/generations/:generation", h.HandleSnapshotStatus)
		router.Put("/api/v1/projects/:project/generations/:generation/resources/:resourceType", h.HandleUploadSnapshotResource)
		router.Post("/api/v1/projects/:project/generations/:generation/finalize", h.HandleFinalizeSnapshot)
		router.Delete("/api/v1/projects/:project/generations/:generation", h.HandleAbortSnapshot)
	}
	if h.releases != nil {
		router.Post("/api/v1/projects/:project/releases/activate", h.HandleActivateReleaseCompatibility)
		router.Post("/api/v1/projects/:project/releases", h.HandleCreateRelease)
		router.Post("/api/v1/projects/:project/releases/:release/activate", h.HandleActivateRelease)
		router.Get("/api/v1/projects/:project/releases/active", h.HandleActiveRelease)
		router.Get("/api/v1/projects/:project/releases/:release", h.HandleReleaseStatus)
	}
}
