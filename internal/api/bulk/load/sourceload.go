package load

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest/sourceload"
	"github.com/gofiber/fiber/v3"
)

const defaultSourceLoadMaxFileBytes int64 = 1 << 30

var errSourceLoadFileTooLarge = errors.New("source file exceeds the configured per-file limit")

func (h *Handler) sourceLoads() sourceLoadOperations {
	return sourceLoadOperations{loader: h.sourceLoader, authorizer: h.authz}
}

func (h *Handler) BeginSourceLoad(ctx context.Context, project, operationID string, plan sourceload.SourceLoadPlan, principal *authscope.Principal) (sourceload.SourceLoadStatus, error) {
	return h.sourceLoads().BeginSourceLoad(ctx, project, operationID, plan, principal)
}

func (h *Handler) GetSourceLoadStatus(ctx context.Context, project, operationID string, principal *authscope.Principal) (sourceload.SourceLoadStatus, error) {
	return h.sourceLoads().GetSourceLoadStatus(ctx, project, operationID, principal)
}

func (h *Handler) UploadSourceLoadFile(ctx context.Context, project, operationID, resourceType string, body *multipart.Reader, principal *authscope.Principal) (sourceload.SourceFileReceipt, error) {
	return h.sourceLoads().UploadSourceLoadFile(ctx, project, operationID, resourceType, func(resourceType string) (string, error) {
		return h.stageRequestedSourceFile(body, resourceType)
	}, principal)
}

func (h *Handler) PublishSourceLoad(ctx context.Context, project, operationID string, principal *authscope.Principal) (sourceload.SourcePublishReceipt, error) {
	return h.sourceLoads().PublishSourceLoad(ctx, project, operationID, principal)
}

func (h *Handler) stageRequestedSourceFile(body *multipart.Reader, resourceType string) (string, error) {
	if body == nil {
		return "", fiber.NewError(fiber.StatusUnsupportedMediaType)
	}

	filename := "META/" + resourceType + ".ndjson"
	localPath, err := stageSourceLoadFile(body, filename, h.sourceLimits.MaxFileBytes)
	if err != nil {
		if errors.Is(err, errSourceLoadFileTooLarge) {
			return "", dataframeerrors.NewError(dataframeerrors.CodeExportLimitExceeded, "",
				dataframeerrors.WithPublicMessage(fmt.Sprintf("The resource file exceeds Loom's per-file limit of %d bytes.", h.sourceLimits.MaxFileBytes)),
				dataframeerrors.WithDetails(map[string]any{"limit": "resource file bytes", "maximum": h.sourceLimits.MaxFileBytes}))
		}
		var parseErr *multipartParseError
		if errors.As(err, &parseErr) || errors.Is(err, errInvalidSourceLoadMultipart) {
			return "", dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		return "", dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return localPath, nil
}

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
