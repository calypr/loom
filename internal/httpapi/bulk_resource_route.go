package httpapi

import (
	"os"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

// bulkResource accepts one resource-type NDJSON upload. The project and
// resource type are part of the URL so the multipart body only carries the
// data file (plus optional authorization metadata). The legacy non-generation
// ingest path performs document overwrite/upsert semantics for this route.
func (s *HTTPServer) bulkResource(c fiber.Ctx) error {
	if !c.IsMultipart() {
		return &apiError{Status: fiber.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "expected multipart/form-data"}
	}
	project := strings.TrimSpace(c.Params("project"))
	resourceType := strings.TrimSpace(c.Params("resourceType"))
	if project == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_project", Message: "project is required"}
	}
	if resourceType == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_resource_type", Message: "resource type is required"}
	}
	authResourcePath := strings.TrimSpace(c.Req().FormValue("auth_resource_path"))
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: "the requested resource is not available", Cause: err}
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)

	fileHeader, err := c.Req().FormFile("file")
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_file", Message: "file upload is required"}
	}
	stagedPath, err := stageUploadedFile(fileHeader)
	if err != nil {
		return &apiError{Status: fiber.StatusInternalServerError, Code: "stage_failed", Message: err.Error()}
	}
	defer os.Remove(stagedPath)

	req := ImportRequest{
		Project:          project,
		ResourceType:     resourceType,
		AuthResourcePath: authResourcePath,
		StagedFilePath:   stagedPath,
		OriginalFilename: fileHeader.Filename,
	}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := s.service.Run(c.Context(), req)
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "BULK_LOAD_FAILED", Message: "bulk load failed", Cause: err}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"project":            result.Project,
		"resource_type":      result.ResourceType,
		"auth_resource_path": result.AuthResourcePath,
		"original_filename":  result.OriginalFilename,
		"submitted_by":       result.SubmittedBy,
		"summary":            result.Summary,
	})
}
