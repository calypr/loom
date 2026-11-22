package load

import (
	"context"
	"encoding/json"
	"net/http"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest/sourceload"
)

func (r *StrictHandlers) BeginDatasetSourceLoad(ctx context.Context, request loomapi.BeginDatasetSourceLoadRequestObject) (loomapi.BeginDatasetSourceLoadResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	var status sourceload.SourceLoadStatus
	var err error
	if request.Body == nil {
		err = dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	} else {
		plan := sourceload.SourceLoadPlan{
			Repository: request.Body.Repository,
			Commit:     request.Body.Commit,
			Files:      make([]sourceload.SourceManifestFile, 0, len(request.Body.Files)),
		}
		if request.Body.AuthResourcePath != nil {
			plan.AuthResourcePath = *request.Body.AuthResourcePath
		}
		if request.Body.ExpectedRevision != nil {
			value := *request.Body.ExpectedRevision
			plan.ExpectedRevision = &value
		}
		for _, file := range request.Body.Files {
			item := sourceload.SourceManifestFile{Path: file.Path}
			if file.Sha256 != nil {
				item.SHA256 = *file.Sha256
			}
			plan.Files = append(plan.Files, item)
		}
		status, err = r.service.BeginSourceLoad(ctx, string(request.Project), request.Operation, plan, principal)
	}
	if err == nil {
		return loomapi.BeginDatasetSourceLoad200JSONResponse(sourceLoadStatusValue(status)), nil
	}
	code, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewBeginDatasetSourceLoadErrorResponse(code, body)
}

func (r *StrictHandlers) GetDatasetSourceLoadStatus(ctx context.Context, request loomapi.GetDatasetSourceLoadStatusRequestObject) (loomapi.GetDatasetSourceLoadStatusResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	status, err := r.service.GetSourceLoadStatus(ctx, string(request.Project), request.Operation, principal)
	if err == nil {
		return loomapi.GetDatasetSourceLoadStatus200JSONResponse(sourceLoadStatusValue(status)), nil
	}
	code, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewGetDatasetSourceLoadStatusErrorResponse(code, body)
}

func (r *StrictHandlers) UploadDatasetSourceLoadFile(ctx context.Context, request loomapi.UploadDatasetSourceLoadFileRequestObject) (loomapi.UploadDatasetSourceLoadFileResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	receipt, err := r.service.UploadSourceLoadFile(ctx, string(request.Project), request.Operation, request.ResourceType, request.Body, principal)
	if err == nil {
		return loomapi.UploadDatasetSourceLoadFile200JSONResponse(sourceFileReceiptValue(receipt)), nil
	}
	code, body := httpapi.MapServiceError(err, ctx)
	if code == http.StatusUnprocessableEntity {
		return sourceLoadValidationErrorResponse(body), nil
	}
	return loomapi.NewUploadDatasetSourceLoadFileErrorResponse(code, body)
}

func (r *StrictHandlers) PublishDatasetSourceLoad(ctx context.Context, request loomapi.PublishDatasetSourceLoadRequestObject) (loomapi.PublishDatasetSourceLoadResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	receipt, err := r.service.PublishSourceLoad(ctx, string(request.Project), request.Operation, principal)
	if err == nil {
		return loomapi.PublishDatasetSourceLoad200JSONResponse{
			OperationId: receipt.OperationID, SourceRevision: receipt.SourceRevision,
			ManifestHash: receipt.ManifestHash, ExecutionId: receipt.ExecutionID,
			State: loomapi.SourcePublishReceiptState(receipt.State), Reused: receipt.Reused,
		}, nil
	}
	code, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewPublishDatasetSourceLoadErrorResponse(code, body)
}

func sourceLoadStatusValue(status sourceload.SourceLoadStatus) loomapi.SourceLoadStatus {
	files := make([]loomapi.SourceFileReceipt, 0, len(status.Files))
	for _, file := range status.Files {
		files = append(files, sourceFileReceiptValue(file))
	}
	return loomapi.SourceLoadStatus{
		OperationId: status.OperationID, Generation: status.Generation,
		State: loomapi.SourceLoadStatusState(status.State), BaseRevision: status.BaseRevision,
		Files: files,
	}
}

func sourceFileReceiptValue(receipt sourceload.SourceFileReceipt) loomapi.SourceFileReceipt {
	return loomapi.SourceFileReceipt{
		Path: receipt.Path, ResourceType: receipt.ResourceType,
		Sha256: receipt.SHA256, Rows: receipt.Rows,
		State: loomapi.SourceFileReceiptState(receipt.State),
	}
}

func sourceLoadValidationErrorResponse(body loomapi.ServiceErrorResponse) loomapi.UploadDatasetSourceLoadFile422JSONResponse {
	response := loomapi.UploadDatasetSourceLoadFile422JSONResponse{}
	response.Error.Code = loomapi.SourceLoadValidationErrorResponseErrorCode(body.Error.Code)
	response.Error.Message = body.Error.Message
	response.Error.RequestId = body.Error.RequestId
	response.Error.Retryable = body.Error.Retryable
	response.Error.Details.Issues = []loomapi.SourceLoadValidationIssue{}
	if body.Error.Details != nil {
		encoded, err := json.Marshal(*body.Error.Details)
		if err == nil {
			_ = json.Unmarshal(encoded, &response.Error.Details)
		}
	}
	if response.Error.Details.Issues == nil {
		response.Error.Details.Issues = []loomapi.SourceLoadValidationIssue{}
	}
	return response
}
