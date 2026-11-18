package server

import (
	"context"
	"encoding/json"
	"net/http"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
)

func (r *HTTPRoutes) BeginDatasetSourceLoad(ctx context.Context, request loomapi.BeginDatasetSourceLoadRequestObject) (loomapi.BeginDatasetSourceLoadResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	var status ingest.SourceLoadStatus
	var err error
	if request.Body == nil {
		err = dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	} else {
		plan := ingest.SourceLoadPlan{
			Repository: request.Body.Repository,
			Commit:     request.Body.Commit,
			Files:      make([]ingest.SourceManifestFile, 0, len(request.Body.Files)),
		}
		if request.Body.AuthResourcePath != nil {
			plan.AuthResourcePath = *request.Body.AuthResourcePath
		}
		if request.Body.ExpectedRevision != nil {
			value := *request.Body.ExpectedRevision
			plan.ExpectedRevision = &value
		}
		for _, file := range request.Body.Files {
			item := ingest.SourceManifestFile{Path: file.Path}
			if file.Sha256 != nil {
				item.SHA256 = *file.Sha256
			}
			plan.Files = append(plan.Files, item)
		}
		status, err = r.load.BeginSourceLoad(ctx, string(request.Project), request.Operation, plan, principal)
	}
	if err == nil {
		return loomapi.BeginDatasetSourceLoad200JSONResponse(sourceLoadStatusValue(status)), nil
	}
	code, body := mapServiceError(err, ctx)
	return loomapi.NewBeginDatasetSourceLoadErrorResponse(code, body)
}

func (r *HTTPRoutes) GetDatasetSourceLoadStatus(ctx context.Context, request loomapi.GetDatasetSourceLoadStatusRequestObject) (loomapi.GetDatasetSourceLoadStatusResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	status, err := r.load.GetSourceLoadStatus(ctx, string(request.Project), request.Operation, principal)
	if err == nil {
		return loomapi.GetDatasetSourceLoadStatus200JSONResponse(sourceLoadStatusValue(status)), nil
	}
	code, body := mapServiceError(err, ctx)
	return loomapi.NewGetDatasetSourceLoadStatusErrorResponse(code, body)
}

func (r *HTTPRoutes) UploadDatasetSourceLoadFile(ctx context.Context, request loomapi.UploadDatasetSourceLoadFileRequestObject) (loomapi.UploadDatasetSourceLoadFileResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	receipt, err := r.load.UploadSourceLoadFile(ctx, string(request.Project), request.Operation, request.ResourceType, request.Body, principal)
	if err == nil {
		return loomapi.UploadDatasetSourceLoadFile200JSONResponse(sourceFileReceiptValue(receipt)), nil
	}
	code, body := mapServiceError(err, ctx)
	if code == http.StatusUnprocessableEntity {
		return sourceLoadValidationErrorResponse(body), nil
	}
	return loomapi.NewUploadDatasetSourceLoadFileErrorResponse(code, body)
}

func (r *HTTPRoutes) PublishDatasetSourceLoad(ctx context.Context, request loomapi.PublishDatasetSourceLoadRequestObject) (loomapi.PublishDatasetSourceLoadResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	receipt, err := r.load.PublishSourceLoad(ctx, string(request.Project), request.Operation, principal)
	if err == nil {
		return loomapi.PublishDatasetSourceLoad200JSONResponse{
			OperationId: receipt.OperationID, SourceRevision: receipt.SourceRevision,
			ManifestHash: receipt.ManifestHash, ExecutionId: receipt.ExecutionID,
			State: loomapi.SourcePublishReceiptState(receipt.State), Reused: receipt.Reused,
		}, nil
	}
	code, body := mapServiceError(err, ctx)
	return loomapi.NewPublishDatasetSourceLoadErrorResponse(code, body)
}

func sourceLoadStatusValue(status ingest.SourceLoadStatus) loomapi.SourceLoadStatus {
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

func sourceFileReceiptValue(receipt ingest.SourceFileReceipt) loomapi.SourceFileReceipt {
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
