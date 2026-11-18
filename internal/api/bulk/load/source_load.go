package load

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
)

const defaultSourceLoadMaxFileBytes int64 = 1 << 30

var errSourceLoadFileTooLarge = errors.New("source file exceeds the configured per-file limit")

func (h *Handler) BeginSourceLoad(ctx context.Context, project, operationID string, plan ingest.SourceLoadPlan, principal *authscope.Principal) (ingest.SourceLoadStatus, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return ingest.SourceLoadStatus{}, err
	}
	if h.sourceLoader == nil {
		return ingest.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	plan.AuthResourcePath, err = normalizeSourceLoadAuthPath(plan.AuthResourcePath)
	if err != nil {
		return ingest.SourceLoadStatus{}, err
	}
	if plan.ExpectedRevision != nil && *plan.ExpectedRevision < 0 {
		return ingest.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if len(plan.Files) == 0 {
		return ingest.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, plan.AuthResourcePath); err != nil {
		return ingest.SourceLoadStatus{}, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	status, err := h.sourceLoader.Begin(ctx, project, operationID, plan)
	if err != nil {
		return ingest.SourceLoadStatus{}, normalizeError(err)
	}
	return status, nil
}

func (h *Handler) GetSourceLoadStatus(ctx context.Context, project, operationID string, principal *authscope.Principal) (ingest.SourceLoadStatus, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return ingest.SourceLoadStatus{}, err
	}
	status, err := h.sourceLoadStatusForRequest(ctx, project, operationID, principal)
	if err != nil {
		return ingest.SourceLoadStatus{}, err
	}
	return status, nil
}

func (h *Handler) UploadSourceLoadFile(ctx context.Context, project, operationID, resourceType string, body *multipart.Reader, principal *authscope.Principal) (ingest.SourceFileReceipt, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return ingest.SourceFileReceipt{}, err
	}
	resourceType, ok := schema.ConcreteResourceType(resourceType)
	if !ok {
		return ingest.SourceFileReceipt{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "")
	}
	if h.sourceLoader == nil {
		return ingest.SourceFileReceipt{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if _, err := h.sourceLoadStatusForRequest(ctx, project, operationID, principal); err != nil {
		return ingest.SourceFileReceipt{}, err
	}
	if body == nil {
		return ingest.SourceFileReceipt{}, fiber.NewError(fiber.StatusUnsupportedMediaType)
	}

	filename := "META/" + resourceType + ".ndjson"
	localPath, err := stageSourceLoadFile(body, filename, h.sourceLimits.MaxFileBytes)
	if err != nil {
		if errors.Is(err, errSourceLoadFileTooLarge) {
			return ingest.SourceFileReceipt{}, dataframeerrors.NewError(dataframeerrors.CodeExportLimitExceeded, "",
				dataframeerrors.WithPublicMessage(fmt.Sprintf("The resource file exceeds Loom's per-file limit of %d bytes.", h.sourceLimits.MaxFileBytes)),
				dataframeerrors.WithDetails(map[string]any{"limit": "resource file bytes", "maximum": h.sourceLimits.MaxFileBytes}))
		}
		var parseErr *multipartParseError
		if errors.As(err, &parseErr) || errors.Is(err, errInvalidSourceLoadMultipart) {
			return ingest.SourceFileReceipt{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		return ingest.SourceFileReceipt{}, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	defer os.Remove(localPath)

	receipt, err := h.sourceLoader.UploadFile(ctx, project, operationID, resourceType, localPath)
	if err != nil {
		return ingest.SourceFileReceipt{}, normalizeError(err)
	}
	return receipt, nil
}

func (h *Handler) PublishSourceLoad(ctx context.Context, project, operationID string, principal *authscope.Principal) (ingest.SourcePublishReceipt, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return ingest.SourcePublishReceipt{}, err
	}
	if _, err := h.sourceLoadStatusForRequest(ctx, project, operationID, principal); err != nil {
		return ingest.SourcePublishReceipt{}, err
	}
	receipt, err := h.sourceLoader.Publish(ctx, project, operationID)
	if err != nil {
		return ingest.SourcePublishReceipt{}, normalizeError(err)
	}
	return receipt, nil
}

func (h *Handler) sourceLoadStatusForRequest(ctx context.Context, project, operationID string, principal *authscope.Principal) (ingest.SourceLoadStatus, error) {
	if h.sourceLoader == nil {
		return ingest.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	status, err := h.sourceLoader.Status(ctx, project, operationID)
	if err != nil {
		return ingest.SourceLoadStatus{}, normalizeError(err)
	}
	authResourcePath, err := normalizeSourceLoadAuthPath(status.AuthResourcePath)
	if err != nil {
		return ingest.SourceLoadStatus{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInternalError, "")
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return ingest.SourceLoadStatus{}, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	return status, nil
}

func normalizeSourceLoadIdentity(project, operationID string) (string, string, error) {
	project = strings.TrimSpace(project)
	operationID = strings.TrimSpace(operationID)
	if project == "" || strings.ContainsAny(project, "\r\n\x00") || operationID == "" || len(operationID) > 200 ||
		strings.ContainsAny(operationID, "/\\\r\n\x00") {
		return "", "", dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	return project, operationID, nil
}

func normalizeSourceLoadAuthPath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	normalized := authscope.NormalizeAuthResourcePath(trimmed)
	if normalized == "" {
		return "", dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	return normalized, nil
}

var errInvalidSourceLoadMultipart = errors.New("source file upload must contain exactly one canonical file part")

type multipartReadTracker struct {
	reader io.Reader
	err    error
}

func (r *multipartReadTracker) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

func stageSourceLoadFile(body *multipart.Reader, filename string, maxBytes int64) (string, error) {
	part, err := body.NextPart()
	if err != nil {
		return "", &multipartParseError{err: err}
	}
	if part == nil {
		return "", errInvalidSourceLoadMultipart
	}
	if err := validateSourceLoadPart(part, filename); err != nil {
		return "", err
	}

	file, err := os.CreateTemp("", "loom-source-load-*.ndjson")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if maxBytes <= 0 {
		maxBytes = defaultSourceLoadMaxFileBytes
	}
	if maxBytes == int64(^uint64(0)>>1) {
		_ = file.Close()
		cleanup()
		return "", fmt.Errorf("source file size limit is too large")
	}
	reader := &multipartReadTracker{reader: part}
	count, copyErr := io.Copy(file, io.LimitReader(reader, maxBytes+1))
	fileCloseErr := file.Close()
	if count > maxBytes {
		cleanup()
		return "", errSourceLoadFileTooLarge
	}
	if copyErr != nil {
		cleanup()
		if reader.err != nil {
			return "", &multipartParseError{err: reader.err}
		}
		return "", copyErr
	}
	if fileCloseErr != nil {
		cleanup()
		return "", fileCloseErr
	}
	second, err := body.NextPart()
	if second != nil {
		cleanup()
		return "", errInvalidSourceLoadMultipart
	}
	if err != nil && !errors.Is(err, io.EOF) {
		cleanup()
		return "", &multipartParseError{err: err}
	}
	if !errors.Is(err, io.EOF) {
		cleanup()
		return "", errInvalidSourceLoadMultipart
	}
	return path, nil
}

func validateSourceLoadPart(part *multipart.Part, filename string) error {
	if part.FormName() != "file" {
		return errInvalidSourceLoadMultipart
	}
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return &multipartParseError{err: err}
	}
	if params["name"] != "file" || params["filename"] != filename {
		return errInvalidSourceLoadMultipart
	}
	return nil
}
