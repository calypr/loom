package httpapi

import (
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

func (s *HTTPServer) exportGeneration(c fiber.Ctx) error {
	if s.rawExporter == nil {
		return &apiError{Status: fiber.StatusNotImplemented, Code: "export_not_configured", Message: "generation export is not configured"}
	}
	project := strings.TrimSpace(c.Params("project"))
	generation := strings.TrimSpace(c.Params("generation"))
	if project == "" || generation == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_generation_identity", Message: "project and generation are required"}
	}
	principal, _ := c.Locals("principal").(*authscope.Principal)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	if s.scopeResolver != nil {
		resolved, err := s.scopeResolver.ResolveReadScopeForGeneration(c.Context(), principal, project, generation, nil)
		if err != nil {
			return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: err.Error()}
		}
		if resolved.Mode == authscope.ReadScopeRestricted && len(resolved.AuthResourcePaths) == 0 {
			return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: "caller has no read access to project"}
		}
		scope = resolved
	}
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	if err := s.rawExporter.ExportRaw(c.Context(), project, generation, scope, c); err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "export_failed", Message: err.Error()}
	}
	return nil
}
