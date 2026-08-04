package dump

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	rawExporter       RawExporter
	dataframeExporter DataframeExporter
	scopeResolver     *authscope.ScopeResolver
}
type Config struct {
	RawExporter       RawExporter
	DataframeExporter DataframeExporter
	ScopeResolver     *authscope.ScopeResolver
}

func NewHandler(cfg Config) *Handler {
	return &Handler{rawExporter: cfg.RawExporter, dataframeExporter: cfg.DataframeExporter, scopeResolver: cfg.ScopeResolver}
}
func (h *Handler) RegisterRoutes(router fiber.Router) {
	if h.rawExporter != nil {
		router.Get("/api/v1/raw", h.dumpRaw)
		router.Get("/api/v1/datasets/:project/generations/:generation/export", h.exportGeneration)
	}
	if h.dataframeExporter != nil {
		router.Post("/loom/api/v1/dataframe/export", h.exportDataframe)
	}
}
