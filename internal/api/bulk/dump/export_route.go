package dump

import (
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
)

func (s *Handler) exportGeneration(c fiber.Ctx) error {
	if s.rawExporter == nil {
		return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	project := strings.TrimSpace(c.Params("project"))
	generation := strings.TrimSpace(c.Params("generation"))
	if project == "" || generation == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	principal, _ := c.Locals("principal").(*authscope.Principal)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	if s.scopeResolver != nil {
		resolved, err := s.scopeResolver.ResolveReadScopeForGeneration(c.Context(), principal, project, generation, nil)
		if err != nil {
			return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
		}
		if resolved.Mode == authscope.ReadScopeRestricted && len(resolved.AuthResourcePaths) == 0 {
			return dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
		}
		scope = resolved
	}
	c.Set(fiber.HeaderContentType, "application/x-ndjson")
	if err := s.rawExporter.ExportRaw(c.Context(), project, generation, scope, c); err != nil {
		resetExportResponse(c)
		return err
	}
	return nil
}
