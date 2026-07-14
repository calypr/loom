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
	parts := strings.SplitN(project, "-", 2)
	authResourcePath := ""
	if len(parts) == 2 {
		authResourcePath = "/programs/" + parts[0] + "/projects/" + parts[1]
	}
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: err.Error()}
	}
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	if err := s.rawExporter.ExportRaw(c.Context(), project, generation, c); err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "export_failed", Message: err.Error()}
	}
	return nil
}
