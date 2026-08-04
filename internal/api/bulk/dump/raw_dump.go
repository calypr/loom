package dump

import (
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
	"strconv"
	"strings"
)

func (s *Handler) dumpRaw(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Query("project"))
	if project == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "")
		}
		limit = parsed
	}
	requestedGeneration := strings.TrimSpace(c.Query("generation"))
	generation, err := s.rawExporter.ResolveGeneration(c.Context(), project, requestedGeneration)
	if err != nil {
		return err
	}
	principal, _ := c.Locals("principal").(*authscope.Principal)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	if s.scopeResolver != nil {
		scope, err = s.scopeResolver.ResolveReadScopeForGeneration(c.Context(), principal, project, generation, nil)
		if err != nil {
			return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
		}
		if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
			return dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
		}
	}
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	if err := s.rawExporter.ExportRawFiltered(c.Context(), RawDumpRequest{Project: project, Generation: generation, ResourceType: strings.TrimSpace(c.Query("resourceType")), Limit: limit}, scope, c); err != nil {
		resetExportResponse(c)
		return err
	}
	return nil
}
