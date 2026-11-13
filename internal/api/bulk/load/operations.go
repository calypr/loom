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
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/gofiber/fiber/v3"
)

func (h *Handler) GetDatasetGenerationStatus(ctx context.Context, project, generation, authResourcePath string, principal *authscope.Principal) (*GenerationStatusResult, error) {
	project, generation, authResourcePath = strings.TrimSpace(project), strings.TrimSpace(generation), strings.TrimSpace(authResourcePath)
	if project == "" || generation == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	status, err := h.service.generationStatus(ctx, project, generation)
	if err != nil {
		if errors.Is(err, publication.ErrManifestNotFound) {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeDatasetNotFound, "")
		}
		return nil, normalizeError(err)
	}
	return status, nil
}

func (h *Handler) CreateDatasetGeneration(ctx context.Context, project, generation string, body *multipart.Reader, principal *authscope.Principal) (*GenerationLoadResult, error) {
	bundle, err := stageGenerationBundle(body)
	if bundle != nil {
		defer bundle.Cleanup()
	}
	if body == nil {
		return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	if err != nil {
		if strings.Contains(err.Error(), "boundary is empty") {
			return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
		}
		var parseErr *multipartParseError
		if !errors.As(err, &parseErr) {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
	}
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	if project == "" || generation == "" || bundle.FileCount() == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	authResourcePath := strings.TrimSpace(firstFormValue(bundle.Values, "auth_resource_path"))
	if authResourcePath != "" && authscope.NormalizeAuthResourcePath(authResourcePath) == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	deferActivation := false
	if raw := strings.TrimSpace(firstFormValue(bundle.Values, "defer_activation")); raw != "" {
		deferActivation, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
		}
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	if err := bundle.ParseStagingError(); err != nil {
		switch {
		case errors.Is(err, os.ErrInvalid):
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidGenerationFile, "")
		case errors.Is(err, os.ErrExist):
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeDuplicateGenerationFile, "")
		default:
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)
	req := GenerationLoadRequest{Project: project, Generation: generation, AuthResourcePath: authResourcePath, StagedDir: bundle.StagedDir, DeferActivation: deferActivation}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := h.service.runGeneration(ctx, req)
	return result, normalizeError(err)
}

func (h *Handler) ActivateDatasetGeneration(ctx context.Context, project, generation, executionID, authResourcePath string, principal *authscope.Principal) (*GenerationActivationResult, error) {
	project, generation, executionID = strings.TrimSpace(project), strings.TrimSpace(generation), strings.TrimSpace(executionID)
	if project == "" || generation == "" || executionID == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, strings.TrimSpace(authResourcePath)); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	if err := h.service.activateGeneration(ctx, project, generation, executionID); err != nil {
		return nil, normalizeError(err)
	}
	return &GenerationActivationResult{Project: project, Generation: generation, DataframeExecutionID: executionID, Activated: true}, nil
}

func firstFormValue(values map[string][]string, key string) string {
	if items := values[key]; len(items) != 0 {
		return items[0]
	}
	return ""
}
