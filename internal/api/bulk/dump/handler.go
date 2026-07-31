package dump

import (
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

type apiError = httpapi.Error

type Handler struct {
	rawExporter                  RawExporter
	dataframeExporter            DataframeExporter
	scopeResolver                *authscope.ScopeResolver
	disableSingleResourceImports bool
}
type Config struct {
	RawExporter                  RawExporter
	DataframeExporter            DataframeExporter
	ScopeResolver                *authscope.ScopeResolver
	DisableSingleResourceImports bool
}

func NewHandler(cfg Config) *Handler {
	return &Handler{rawExporter: cfg.RawExporter, dataframeExporter: cfg.DataframeExporter, scopeResolver: cfg.ScopeResolver, disableSingleResourceImports: cfg.DisableSingleResourceImports}
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
