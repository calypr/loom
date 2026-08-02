package dump

import (
	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
	"strconv"
	"strings"
)

func (s *Handler) dumpRaw(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Query("project"))
	if project == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_project", Message: "project is required"}
	}
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_limit", Message: "limit must be a positive integer"}
		}
		limit = parsed
	}
	requestedGeneration := strings.TrimSpace(c.Query("generation"))
	generation, err := s.rawExporter.ResolveGeneration(c.Context(), project, requestedGeneration)
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "generation_not_found", Message: err.Error()}
	}
	principal, _ := c.Locals("principal").(*authscope.Principal)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	if s.scopeResolver != nil {
		scope, err = s.scopeResolver.ResolveReadScopeForGeneration(c.Context(), principal, project, generation, nil)
		if err != nil {
			return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: "the requested resource is not available", Cause: err}
		}
		if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
			return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: "caller has no read access to project"}
		}
	}
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	if err := s.rawExporter.ExportRawFiltered(c.Context(), RawDumpRequest{Project: project, Generation: generation, ResourceType: strings.TrimSpace(c.Query("resourceType")), Limit: limit}, scope, c); err != nil {
		resetExportResponse(c)
		return &apiError{Status: fiber.StatusBadRequest, Code: "EXPORT_FAILED", Message: "export failed", Cause: err}
	}
	return nil
}
