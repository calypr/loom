package load

import (
	"mime"
	"os"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
)

const defaultRawProject = "default"

func (s *Handler) HandleLoadRaw(c fiber.Ctx) error {
	contentType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil || (contentType != "application/x-ndjson" && contentType != "application/ndjson") {
		return fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	project := strings.TrimSpace(c.Query("project"))
	if project == "" {
		project = defaultRawProject
	}
	authResourcePath := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
	principal, _ := c.Locals("principal").(*authscope.Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}

	dir, err := os.MkdirTemp("", "loom-raw-")
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	defer os.RemoveAll(dir)
	rows, err := ingest.PartitionNDJSON(c.Request().BodyStream(), dir)
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
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
	if req.Generation == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	result, err := s.service.RunGeneration(c.Context(), req)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"rows": rows, "result": result})
}
