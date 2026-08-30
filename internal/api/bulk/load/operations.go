package load

import (
	"context"
	"errors"
	"mime/multipart"
	"os"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
)

// CreateDatasetGeneration parses and stages a generated multipart request,
// then runs the generation import operation.
func (h *Handler) CreateDatasetGeneration(ctx context.Context, project, generation string, body *multipart.Reader, principal *authscope.Principal) (*GenerationLoadResult, error) {
	if body == nil {
		return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	form, err := body.ReadForm(32 << 20)
	if err != nil {
		if strings.Contains(err.Error(), "boundary is empty") {
			return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
	}
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	if project == "" || generation == "" || len(form.File["file"]) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	authResourcePath := strings.TrimSpace(firstFormValue(form.Value, "auth_resource_path"))
	deferActivation := false
	if raw := strings.TrimSpace(firstFormValue(form.Value, "defer_activation")); raw != "" {
		deferActivation, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
		}
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)
	stagedDir, err := stageGenerationFiles(form.File["file"])
	if err != nil {
		switch {
		case errors.Is(err, os.ErrInvalid):
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidGenerationFile, "")
		case errors.Is(err, os.ErrExist):
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeDuplicateGenerationFile, "")
		default:
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
	}
	defer os.RemoveAll(stagedDir)
	req := GenerationLoadRequest{Project: project, Generation: generation, AuthResourcePath: authResourcePath, StagedDir: stagedDir, DeferActivation: deferActivation}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := h.service.RunGeneration(ctx, req)
	return result, NormalizeError(err)
}

func (h *Handler) ActivateDatasetGeneration(ctx context.Context, project, generation, executionID, authResourcePath string, principal *authscope.Principal) (map[string]any, error) {
	project, generation, executionID = strings.TrimSpace(project), strings.TrimSpace(generation), strings.TrimSpace(executionID)
	if project == "" || generation == "" || executionID == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, strings.TrimSpace(authResourcePath)); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	if err := h.service.ActivateGeneration(ctx, project, generation, executionID); err != nil {
		return nil, NormalizeError(err)
	}
	return map[string]any{"project": project, "generation": generation, "dataframeExecutionId": executionID, "activated": true}, nil
}

func firstFormValue(values map[string][]string, key string) string {
	if items := values[key]; len(items) != 0 {
		return items[0]
	}
	return ""
}
