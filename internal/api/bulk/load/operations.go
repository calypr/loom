package load

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataset"
	"github.com/gofiber/fiber/v3"
)

// UploadProjectResource parses the generated multipart request and performs
// the resource import without constructing a Fiber context. The generated
// OpenAPI strict handler owns multipart framing; this method owns the load
// operation and its authorization policy.
func (h *Handler) UploadProjectResource(ctx context.Context, project, resourceType string, body *multipart.Reader, principal *authscope.Principal) (*ImportResult, error) {
	if body == nil {
		return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	project = strings.TrimSpace(project)
	resourceType = strings.TrimSpace(resourceType)
	if project == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if resourceType == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "")
	}
	form, err := body.ReadForm(32 << 20)
	if err != nil {
		if strings.Contains(err.Error(), "boundary is empty") {
			return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
		}
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if form == nil || len(form.File["file"]) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	authResourcePath := strings.TrimSpace(firstFormValue(form.Value, "auth_resource_path"))
	if err := h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)
	fileHeader := form.File["file"][0]
	stagedPath, err := stageUploadedFile(fileHeader)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	defer os.Remove(stagedPath)

	req := ImportRequest{Project: project, ResourceType: resourceType, AuthResourcePath: authResourcePath, StagedFilePath: stagedPath, OriginalFilename: fileHeader.Filename}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := h.service.Run(ctx, req)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "")
	}
	return result, nil
}

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
	if project == "" || generation == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	files := form.File["file"]
	if len(files) == 0 {
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
	stagedDir, err := stageGenerationFiles(files)
	if err != nil {
		if errors.Is(err, os.ErrInvalid) {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidGenerationFile, "")
		}
		if errors.Is(err, os.ErrExist) {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeDuplicateGenerationFile, "")
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	defer os.RemoveAll(stagedDir)
	req := GenerationLoadRequest{Project: project, Generation: generation, AuthResourcePath: authResourcePath, StagedDir: stagedDir, DeferActivation: deferActivation}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	return h.service.RunGeneration(ctx, req)
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
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "")
	}
	return map[string]any{"project": project, "generation": generation, "dataframeExecutionId": executionID, "activated": true}, nil
}

type SnapshotCreateRequest struct {
	GitCommit             string   `json:"gitCommit"`
	ExpectedResourceTypes []string `json:"expectedResourceTypes"`
	AuthResourcePath      string   `json:"authResourcePath,omitempty"`
}

func (h *Handler) CreateSnapshot(ctx context.Context, project, generation string, body []byte, principal *authscope.Principal) (dataset.SnapshotGeneration, error) {
	var request SnapshotCreateRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	if request.GitCommit == "" {
		request.GitCommit = generation
	}
	if request.GitCommit != generation {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotConflict
	}
	if err := h.authorizeSnapshotContext(ctx, principal, project, request.AuthResourcePath); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if h.snapshots == nil {
		return dataset.SnapshotGeneration{}, errors.New("snapshot service dependencies are required")
	}
	return h.snapshots.CreateOrResume(ctx, project, generation, request.AuthResourcePath, request.ExpectedResourceTypes)
}

func (h *Handler) SnapshotStatus(ctx context.Context, project, generation string, principal *authscope.Principal) (dataset.SnapshotGeneration, error) {
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	result, err := h.snapshots.Status(ctx, project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if err := h.authorizeSnapshotContext(ctx, principal, project, result.AuthResourcePath); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return result, nil
}

func (h *Handler) UploadSnapshotResource(ctx context.Context, project, generation, resourceType, checksum string, body io.Reader, principal *authscope.Principal) (dataset.SnapshotGeneration, error) {
	project, generation, resourceType = strings.TrimSpace(project), strings.TrimSpace(generation), strings.TrimSpace(resourceType)
	status, err := h.snapshots.Status(ctx, project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if err := h.authorizeSnapshotContext(ctx, principal, project, status.AuthResourcePath); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	content, err := io.ReadAll(body)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return h.snapshots.Upload(ctx, project, generation, resourceType, checksum, content)
}

func (h *Handler) FinalizeSnapshot(ctx context.Context, project, generation string, principal *authscope.Principal) (dataset.SnapshotGeneration, *GenerationLoadResult, error) {
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	status, err := h.snapshots.Status(ctx, project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, nil, err
	}
	if err := h.authorizeSnapshotContext(ctx, principal, project, status.AuthResourcePath); err != nil {
		return dataset.SnapshotGeneration{}, nil, err
	}
	submittedBy := ""
	if principal != nil {
		submittedBy = principal.Subject
	}
	return h.snapshots.Finalize(ctx, project, generation, submittedBy)
}

func (h *Handler) AbortSnapshot(ctx context.Context, project, generation string, principal *authscope.Principal) (dataset.SnapshotGeneration, error) {
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	status, err := h.snapshots.Status(ctx, project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if err := h.authorizeSnapshotContext(ctx, principal, project, status.AuthResourcePath); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return h.snapshots.Abort(ctx, project, generation)
}

func (h *Handler) CreateRelease(ctx context.Context, project string, body []byte, principal *authscope.Principal) (dataset.ProjectRelease, error) {
	var request dataset.ActivationRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return dataset.ProjectRelease{}, err
	}
	request.Project = strings.TrimSpace(project)
	if err := h.authorizeReleaseGenerationContext(ctx, request.Project, request.Generation, principal); err != nil {
		return dataset.ProjectRelease{}, err
	}
	return h.releases.Create(ctx, request)
}

func (h *Handler) ActivateReleaseCompatibility(ctx context.Context, project string, body []byte, principal *authscope.Principal) (dataset.ActiveRelease, error) {
	var request dataset.ActivationRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return dataset.ActiveRelease{}, err
	}
	request.Project = strings.TrimSpace(project)
	if err := h.authorizeReleaseGenerationContext(ctx, request.Project, request.Generation, principal); err != nil {
		return dataset.ActiveRelease{}, err
	}
	return h.releases.Activate(ctx, request)
}

func (h *Handler) ActiveRelease(ctx context.Context, project string, principal *authscope.Principal) (dataset.ActiveRelease, error) {
	project = strings.TrimSpace(project)
	active, err := h.releases.Active(ctx, project)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	if err := h.authorizeReleaseGenerationContext(ctx, project, active.Release.Generation, principal); err != nil {
		return dataset.ActiveRelease{}, err
	}
	return active, nil
}

func (h *Handler) ReleaseStatus(ctx context.Context, project, release string, principal *authscope.Principal) (dataset.ProjectRelease, error) {
	project, release = strings.TrimSpace(project), strings.TrimSpace(release)
	value, err := h.releases.Releases.ReadRelease(ctx, project, release)
	if err != nil {
		return dataset.ProjectRelease{}, err
	}
	if err := h.authorizeReleaseGenerationContext(ctx, project, value.Generation, principal); err != nil {
		return dataset.ProjectRelease{}, err
	}
	return value, nil
}

func (h *Handler) ActivateRelease(ctx context.Context, project, release string, body []byte, principal *authscope.Principal) (dataset.ActiveRelease, error) {
	project, release = strings.TrimSpace(project), strings.TrimSpace(release)
	value, err := h.releases.Releases.ReadRelease(ctx, project, release)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	if err := h.authorizeReleaseGenerationContext(ctx, project, value.Generation, principal); err != nil {
		return dataset.ActiveRelease{}, err
	}
	var request struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return dataset.ActiveRelease{}, err
	}
	return h.releases.ActivateExisting(ctx, project, release, request.ExpectedRevision)
}

func (h *Handler) authorizeSnapshotContext(ctx context.Context, principal *authscope.Principal, project, authResourcePath string) error {
	return h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath)
}

func (h *Handler) authorizeReleaseGenerationContext(ctx context.Context, project, generation string, principal *authscope.Principal) error {
	status, err := h.snapshots.Status(ctx, project, generation)
	if err != nil {
		return err
	}
	return h.authorizeSnapshotContext(ctx, principal, project, status.AuthResourcePath)
}

func firstFormValue(values map[string][]string, key string) string {
	if items := values[key]; len(items) != 0 {
		return items[0]
	}
	return ""
}

// MapSnapshotError preserves the status and safe snapshot error envelope used
// by the legacy Fiber routes for generated OpenAPI callers.
func MapSnapshotError(err error, requestID string) (int, httpapi.ErrorResponse) {
	status, code, message, retryable := http.StatusBadRequest, "INVALID_REQUEST", "the request is invalid", false
	switch {
	case errors.Is(err, dataset.ErrSnapshotNotFound), errors.Is(err, dataset.ErrReleaseNotFound), errors.Is(err, dataset.ErrNoActiveRelease):
		status, code, message = http.StatusNotFound, "DATASET_NOT_FOUND", "the requested dataset was not found"
	case errors.Is(err, dataset.ErrChecksumConflict), errors.Is(err, dataset.ErrSnapshotConflict):
		status, code, message = http.StatusConflict, "CHECKSUM_CONFLICT", "immutable snapshot content conflicts with an existing upload"
	case errors.Is(err, dataset.ErrGenerationIncomplete):
		status, code, message = http.StatusUnprocessableEntity, "GENERATION_INCOMPLETE", "the generation is missing required resource uploads"
	case errors.Is(err, dataset.ErrSnapshotFinalized), errors.Is(err, dataset.ErrSnapshotAborted):
		status, code, message = http.StatusConflict, "CHECKSUM_CONFLICT", "the immutable generation can no longer be changed"
	case errors.Is(err, dataset.ErrReleaseRequirementsUnmet):
		status, code, message = http.StatusUnprocessableEntity, "RELEASE_REQUIREMENTS_UNMET", "required dataframe publications are not ready for activation"
	case errors.Is(err, dataset.ErrReleaseActivationConflict):
		status, code, message = http.StatusConflict, "RELEASE_ACTIVATION_CONFLICT", "the active project release changed; reload it before retrying"
	default:
		mapped := httpapi.MapDataframeError(err, requestID)
		return mapped.Status, mapped.Body
	}
	details := map[string]any(nil)
	var requirements *dataset.ReleaseRequirementsError
	if errors.As(err, &requirements) {
		details = map[string]any{"verifications": requirements.Verifications}
	}
	return status, httpapi.ErrorResponse{Error: httpapi.HTTPErrorBody{Code: code, Message: message, Details: details, Retryable: retryable, RequestID: requestID}}
}
