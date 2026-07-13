package httpapi

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/authscope"

	"github.com/gofiber/fiber/v3"
)

func (s *HTTPServer) createImport(c fiber.Ctx) error {
	if !c.IsMultipart() {
		return &apiError{Status: fiber.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "expected multipart/form-data"}
	}
	form, err := c.Req().MultipartForm()
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_multipart_form", Message: err.Error()}
	}
	fileCount := 0
	for _, files := range form.File {
		fileCount += len(files)
	}
	if fileCount != 1 {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_file_count", Message: "exactly one uploaded file is required"}
	}

	project := strings.TrimSpace(c.Req().FormValue("project"))
	if project == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_project", Message: "project is required"}
	}
	resourceType := strings.TrimSpace(c.Req().FormValue("resource_type"))
	if resourceType == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_resource_type", Message: "resource_type is required"}
	}
	authResourcePath := strings.TrimSpace(c.Req().FormValue("auth_resource_path"))
	truncate, err := parseOptionalBool(c.Req().FormValue("truncate"))
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_truncate", Message: err.Error()}
	}
	useGeneric, err := parseOptionalBool(c.Req().FormValue("use_generic"))
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_use_generic", Message: err.Error()}
	}

	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: err.Error()}
	}

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
		Truncate:         truncate,
		UseGeneric:       useGeneric,
		StagedFilePath:   stagedPath,
		OriginalFilename: fileHeader.Filename,
	}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := s.service.Run(c.Context(), req)
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_import_request", Message: err.Error()}
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

func parseOptionalBool(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q", raw)
	}
	return value, nil
}

func stageUploadedFile(fileHeader *multipart.FileHeader) (string, error) {
	src, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	ext := filepath.Ext(fileHeader.Filename)
	if ext == "" {
		ext = ".ndjson"
	}
	dst, err := os.CreateTemp("", "arango-fhir-upload-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		return "", err
	}
	if err := dst.Close(); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}
