package httpapi

import (
	"mime"
	"os"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
)

const defaultRawProject = "default"

func (s *HTTPServer) dumpRaw(c fiber.Ctx) error {
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
	legacy := !s.disableSingleResourceImports && requestedGeneration == ""
	generation := requestedGeneration
	var err error
	if !legacy {
		generation, err = s.rawExporter.ResolveGeneration(c.Context(), project, requestedGeneration)
		if err != nil {
			return &apiError{Status: fiber.StatusBadRequest, Code: "generation_not_found", Message: err.Error()}
		}
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
	if err := s.rawExporter.ExportRawFiltered(c.Context(), RawDumpRequest{
		Project:      project,
		Generation:   generation,
		ResourceType: strings.TrimSpace(c.Query("resourceType")),
		Limit:        limit,
		Legacy:       legacy,
	}, scope, c); err != nil {
		resetExportResponse(c)
		return &apiError{Status: fiber.StatusBadRequest, Code: "EXPORT_FAILED", Message: "export failed", Cause: err}
	}
	return nil
}

func (s *HTTPServer) loadRaw(c fiber.Ctx) error {
	contentType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil || (contentType != "application/x-ndjson" && contentType != "application/ndjson") {
		return &apiError{Status: fiber.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "expected application/x-ndjson"}
	}
	project := strings.TrimSpace(c.Query("project"))
	if project == "" {
		project = defaultRawProject
	}
	authResourcePath := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: "the requested resource is not available", Cause: err}
	}

	dir, err := os.MkdirTemp("", "loom-raw-")
	if err != nil {
		return &apiError{Status: fiber.StatusInternalServerError, Code: "stage_failed", Message: err.Error()}
	}
	defer os.RemoveAll(dir)
	rows, err := ingest.PartitionNDJSON(c.Request().BodyStream(), dir)
	if err != nil {
		return &apiError{Status: fiber.StatusUnprocessableEntity, Code: "INVALID_DATA", Message: "uploaded NDJSON is invalid", Cause: err}
	}
	req := GenerationLoadRequest{
		Project:          project,
		Generation:       strings.TrimSpace(c.Query("generation")),
		AuthResourcePath: authResourcePath,
		StagedDir:        dir,
	}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	var result *GenerationLoadResult
	if s.disableSingleResourceImports {
		if req.Generation == "" {
			return &apiError{Status: fiber.StatusBadRequest, Code: "missing_generation", Message: "generation is required while dataset-generation mode is enabled"}
		}
		result, err = s.service.RunGeneration(c.Context(), req)
	} else {
		result, err = s.service.RunBundle(c.Context(), req)
	}
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "RAW_LOAD_FAILED", Message: "raw load failed", Cause: err}
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"rows": rows, "result": result})
}
