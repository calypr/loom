package load

import (
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
)

// HandleResource implements the project resource upload transport operation.
func (h *Handler) HandleResource(c fiber.Ctx) error {
	if !c.IsMultipart() {
		return fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	project := strings.TrimSpace(c.Params("project"))
	resourceType := strings.TrimSpace(c.Params("resourceType"))
	if project == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if resourceType == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "")
	}
	authResourcePath := strings.TrimSpace(c.Req().FormValue("auth_resource_path"))
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := h.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)

	fileHeader, err := c.Req().FormFile("file")
	if err != nil {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	stagedPath, err := stageUploadedFile(fileHeader)
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
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
	result, err := h.service.Run(c.Context(), req)
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
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
		_ = dst.Close()
		_ = os.Remove(dst.Name())
		return "", err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}
