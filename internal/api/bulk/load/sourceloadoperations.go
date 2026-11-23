package load

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest/sourceload"
)

type sourceLoadOperations struct {
	loader     SourceLoader
	authorizer authscope.Authorizer
}

func (ops sourceLoadOperations) BeginSourceLoad(ctx context.Context, project, operationID string, plan sourceload.SourceLoadPlan, principal *authscope.Principal) (sourceload.SourceLoadStatus, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return sourceload.SourceLoadStatus{}, err
	}
	if ops.loader == nil {
		return sourceload.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	plan.AuthResourcePath, err = normalizeSourceLoadAuthPath(plan.AuthResourcePath)
	if err != nil {
		return sourceload.SourceLoadStatus{}, err
	}
	if plan.ExpectedRevision != nil && *plan.ExpectedRevision < 0 {
		return sourceload.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if len(plan.Files) == 0 {
		return sourceload.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if err := ops.authorizer.AuthorizeWrite(ctx, principal, project, plan.AuthResourcePath); err != nil {
		return sourceload.SourceLoadStatus{}, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	status, err := ops.loader.Begin(ctx, project, operationID, plan)
	if err != nil {
		return sourceload.SourceLoadStatus{}, normalizeError(err)
	}
	return status, nil
}

func (ops sourceLoadOperations) GetSourceLoadStatus(ctx context.Context, project, operationID string, principal *authscope.Principal) (sourceload.SourceLoadStatus, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return sourceload.SourceLoadStatus{}, err
	}
	status, err := ops.authorizedStatus(ctx, project, operationID, principal)
	if err != nil {
		return sourceload.SourceLoadStatus{}, err
	}
	return status, nil
}

func (ops sourceLoadOperations) UploadSourceLoadFile(ctx context.Context, project, operationID, resourceType string, stage func(string) (string, error), principal *authscope.Principal) (sourceload.SourceFileReceipt, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return sourceload.SourceFileReceipt{}, err
	}
	resourceType, ok := schema.ConcreteResourceType(resourceType)
	if !ok {
		return sourceload.SourceFileReceipt{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "")
	}
	if ops.loader == nil {
		return sourceload.SourceFileReceipt{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if _, err := ops.authorizedStatus(ctx, project, operationID, principal); err != nil {
		return sourceload.SourceFileReceipt{}, err
	}
	localPath, err := stage(resourceType)
	if err != nil {
		return sourceload.SourceFileReceipt{}, err
	}
	defer os.Remove(localPath)

	receipt, err := ops.loader.UploadFile(ctx, project, operationID, resourceType, localPath)
	if err != nil {
		return sourceload.SourceFileReceipt{}, normalizeError(err)
	}
	return receipt, nil
}

func (ops sourceLoadOperations) PublishSourceLoad(ctx context.Context, project, operationID string, principal *authscope.Principal) (sourceload.SourcePublishReceipt, error) {
	project, operationID, err := normalizeSourceLoadIdentity(project, operationID)
	if err != nil {
		return sourceload.SourcePublishReceipt{}, err
	}
	if _, err := ops.authorizedStatus(ctx, project, operationID, principal); err != nil {
		return sourceload.SourcePublishReceipt{}, err
	}
	receipt, err := ops.loader.Publish(ctx, project, operationID)
	if err != nil {
		return sourceload.SourcePublishReceipt{}, normalizeError(err)
	}
	return receipt, nil
}

func (ops sourceLoadOperations) authorizedStatus(ctx context.Context, project, operationID string, principal *authscope.Principal) (sourceload.SourceLoadStatus, error) {
	if ops.loader == nil {
		return sourceload.SourceLoadStatus{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	status, err := ops.loader.Status(ctx, project, operationID)
	if err != nil {
		return sourceload.SourceLoadStatus{}, normalizeError(err)
	}
	authResourcePath, err := normalizeSourceLoadAuthPath(status.AuthResourcePath)
	if err != nil {
		return sourceload.SourceLoadStatus{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInternalError, "")
	}
	if err := ops.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return sourceload.SourceLoadStatus{}, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
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
