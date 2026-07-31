package httpapi

import (
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

func (s *HTTPServer) createGeneration(c fiber.Ctx) error {
	if !c.IsMultipart() {
		return &apiError{Status: fiber.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "expected multipart/form-data"}
	}
	form, err := c.Req().MultipartForm()
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_multipart_form", Message: err.Error()}
	}
	project := strings.TrimSpace(c.Req().FormValue("project"))
	generation := strings.TrimSpace(c.Req().FormValue("generation"))
	if project == "" || generation == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_generation_identity", Message: "project and generation are required"}
	}
	files := form.File["file"]
	if len(files) == 0 {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_file", Message: "at least one file is required"}
	}
	authResourcePath := strings.TrimSpace(c.Req().FormValue("auth_resource_path"))
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: "the requested resource is not available", Cause: err}
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)
	stagedDir, err := stageGenerationFiles(files)
	if err != nil {
		return &apiError{Status: fiber.StatusInternalServerError, Code: "stage_failed", Message: err.Error()}
	}
	defer os.RemoveAll(stagedDir)
	req := GenerationLoadRequest{Project: project, Generation: generation, AuthResourcePath: authResourcePath, StagedDir: stagedDir}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := s.service.RunGeneration(c.Context(), req)
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "GENERATION_LOAD_FAILED", Message: "generation load failed", Cause: err}
	}
	return c.Status(fiber.StatusOK).JSON(result)
}

func stageGenerationFiles(headers []*multipart.FileHeader) (string, error) {
	dir, err := os.MkdirTemp("", "loom-generation-")
	if err != nil {
		return "", err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := filepath.Base(header.Filename)
		if name == "." || name == "" || name != header.Filename || filepath.Ext(name) != ".ndjson" {
			cleanup()
			return "", os.ErrInvalid
		}
		if _, ok := seen[name]; ok {
			cleanup()
			return "", os.ErrExist
		}
		seen[name] = struct{}{}
		src, err := header.Open()
		if err != nil {
			cleanup()
			return "", err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err == nil {
			_, err = io.Copy(dst, src)
		}
		_ = src.Close()
		if dst != nil {
			if closeErr := dst.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			cleanup()
			return "", err
		}
	}
	return dir, nil
}
