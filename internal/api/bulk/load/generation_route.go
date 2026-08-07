package load

import (
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
)

func (s *Handler) createGeneration(c fiber.Ctx) error {
	if !c.IsMultipart() {
		return fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	form, err := c.Req().MultipartForm()
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
	}
	project := strings.TrimSpace(c.Req().FormValue("project"))
	generation := strings.TrimSpace(c.Req().FormValue("generation"))
	if project == "" || generation == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	files := form.File["file"]
	if len(files) == 0 {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	authResourcePath := strings.TrimSpace(c.Req().FormValue("auth_resource_path"))
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)
	stagedDir, err := stageGenerationFiles(files)
	if err != nil {
		if errors.Is(err, os.ErrInvalid) {
			return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidGenerationFile, "")
		}
		if errors.Is(err, os.ErrExist) {
			return dataframeerrors.Wrap(err, dataframeerrors.CodeDuplicateGenerationFile, "")
		}
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	defer os.RemoveAll(stagedDir)
	req := GenerationLoadRequest{Project: project, Generation: generation, AuthResourcePath: authResourcePath, StagedDir: stagedDir}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := s.service.RunGeneration(c.Context(), req)
	if err != nil {
		return err
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
