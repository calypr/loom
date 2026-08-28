package load

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
	"github.com/gofiber/fiber/v3"
)

type createSnapshotRequest struct {
	GitCommit             string   `json:"gitCommit"`
	ExpectedResourceTypes []string `json:"expectedResourceTypes"`
	AuthResourcePath      string   `json:"authResourcePath,omitempty"`
}

func (h *Handler) HandleCreateSnapshot(c fiber.Ctx) error {
	project, generation := strings.TrimSpace(c.Params("project")), strings.TrimSpace(c.Params("generation"))
	var request createSnapshotRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return writeSnapshotError(c, err)
	}
	if request.GitCommit == "" {
		request.GitCommit = generation
	}
	if request.GitCommit != generation {
		return writeSnapshotError(c, dataset.ErrSnapshotConflict)
	}
	if err := h.authorizeSnapshot(c, project, request.AuthResourcePath); err != nil {
		return err
	}
	result, err := h.snapshots.CreateOrResume(c.Context(), project, generation, request.AuthResourcePath, request.ExpectedResourceTypes)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(result)
}

func (h *Handler) HandleSnapshotStatus(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	result, err := h.snapshots.Status(c.Context(), project, strings.TrimSpace(c.Params("generation")))
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeSnapshot(c, project, result.AuthResourcePath); err != nil {
		return err
	}
	return c.Status(http.StatusOK).JSON(result)
}

func (h *Handler) HandleUploadSnapshotResource(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	generation := strings.TrimSpace(c.Params("generation"))
	status, err := h.snapshots.Status(c.Context(), project, generation)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeSnapshot(c, project, status.AuthResourcePath); err != nil {
		return err
	}
	result, err := h.snapshots.Upload(c.Context(), project, generation, strings.TrimSpace(c.Params("resourceType")), c.Get("X-Content-SHA256"), c.Body())
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(result)
}

func (h *Handler) HandleFinalizeSnapshot(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	generationID := strings.TrimSpace(c.Params("generation"))
	status, err := h.snapshots.Status(c.Context(), project, generationID)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeSnapshot(c, project, status.AuthResourcePath); err != nil {
		return err
	}
	principal, _ := c.Locals("principal").(*authscope.Principal)
	submittedBy := ""
	if principal != nil {
		submittedBy = principal.Subject
	}
	generation, load, err := h.snapshots.Finalize(c.Context(), project, generationID, submittedBy)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"generation": generation, "load": load})
}

func (h *Handler) HandleAbortSnapshot(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	generationID := strings.TrimSpace(c.Params("generation"))
	status, err := h.snapshots.Status(c.Context(), project, generationID)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeSnapshot(c, project, status.AuthResourcePath); err != nil {
		return err
	}
	result, err := h.snapshots.Abort(c.Context(), project, generationID)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(result)
}

func (h *Handler) HandleActivateReleaseCompatibility(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	var request dataset.ActivationRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return writeSnapshotError(c, err)
	}
	request.Project = project
	if err := h.authorizeReleaseGeneration(c, project, request.Generation); err != nil {
		return err
	}
	result, err := h.releases.Activate(c.Context(), request)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(result)
}

func (h *Handler) HandleCreateRelease(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	var request dataset.ActivationRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return writeSnapshotError(c, err)
	}
	request.Project = project
	if err := h.authorizeReleaseGeneration(c, project, request.Generation); err != nil {
		return err
	}
	release, err := h.releases.Create(c.Context(), request)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(release)
}

func (h *Handler) HandleActivateRelease(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	releaseID := strings.TrimSpace(c.Params("release"))
	release, err := h.releases.Releases.ReadRelease(c.Context(), project, releaseID)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeReleaseGeneration(c, project, release.Generation); err != nil {
		return err
	}
	var request struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return writeSnapshotError(c, err)
	}
	active, err := h.releases.ActivateExisting(c.Context(), project, releaseID, request.ExpectedRevision)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return c.Status(http.StatusOK).JSON(active)
}

func (h *Handler) HandleActiveRelease(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	active, err := h.releases.Active(c.Context(), project)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeReleaseGeneration(c, project, active.Release.Generation); err != nil {
		return err
	}
	return c.Status(http.StatusOK).JSON(active)
}

func (h *Handler) HandleReleaseStatus(c fiber.Ctx) error {
	project := strings.TrimSpace(c.Params("project"))
	release, err := h.releases.Releases.ReadRelease(c.Context(), project, strings.TrimSpace(c.Params("release")))
	if err != nil {
		return writeSnapshotError(c, err)
	}
	if err := h.authorizeReleaseGeneration(c, project, release.Generation); err != nil {
		return err
	}
	return c.Status(http.StatusOK).JSON(release)
}

func (h *Handler) authorizeSnapshot(c fiber.Ctx, project, authResourcePath string) error {
	principal, _ := c.Locals("principal").(*authscope.Principal)
	return h.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath)
}

func (h *Handler) authorizeReleaseGeneration(c fiber.Ctx, project, generation string) error {
	status, err := h.snapshots.Status(c.Context(), project, generation)
	if err != nil {
		return writeSnapshotError(c, err)
	}
	return h.authorizeSnapshot(c, project, status.AuthResourcePath)
}

func writeSnapshotError(c fiber.Ctx, err error) error {
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
		if err == nil {
			return nil
		}
	}
	details := map[string]any(nil)
	var requirements *dataset.ReleaseRequirementsError
	if errors.As(err, &requirements) {
		details = map[string]any{"verifications": requirements.Verifications}
	}
	return c.Status(status).JSON(httpapi.ErrorResponse{Error: httpapi.HTTPErrorBody{Code: code, Message: message, Details: details, Retryable: retryable, RequestID: httpapi.RequestIDFromContext(c.Context())}})
}
