package server

import (
	"context"
	"encoding/json"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
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
	switch code {
	case http.StatusBadRequest:
		return loomapi.BeginDatasetSourceLoad400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.BeginDatasetSourceLoad401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.BeginDatasetSourceLoad403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusNotFound:
		return loomapi.BeginDatasetSourceLoad404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(body)}, nil
	case http.StatusConflict:
		return loomapi.BeginDatasetSourceLoad409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(body)}, nil
	case http.StatusRequestEntityTooLarge:
		return loomapi.BeginDatasetSourceLoad413JSONResponse{ServicePayloadTooLargeJSONResponse: loomapi.ServicePayloadTooLargeJSONResponse(body)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.BeginDatasetSourceLoad415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(body)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.BeginDatasetSourceLoad422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(body)}, nil
	case http.StatusInternalServerError:
		return loomapi.BeginDatasetSourceLoad500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.BeginDatasetSourceLoad503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("beginDatasetSourceLoad", code)
	}
}

func (r *HTTPRoutes) GetDatasetSourceLoadStatus(ctx context.Context, request loomapi.GetDatasetSourceLoadStatusRequestObject) (loomapi.GetDatasetSourceLoadStatusResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	status, err := r.load.GetSourceLoadStatus(ctx, string(request.Project), request.Operation, principal)
	if err == nil {
		return loomapi.GetDatasetSourceLoadStatus200JSONResponse(sourceLoadStatusValue(status)), nil
	}
	code, body := mapServiceError(err, ctx)
	switch code {
	case http.StatusBadRequest:
		return loomapi.GetDatasetSourceLoadStatus400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.GetDatasetSourceLoadStatus401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.GetDatasetSourceLoadStatus403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusNotFound:
		return loomapi.GetDatasetSourceLoadStatus404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(body)}, nil
	case http.StatusConflict:
		return loomapi.GetDatasetSourceLoadStatus409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(body)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetDatasetSourceLoadStatus500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetDatasetSourceLoadStatus503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("getDatasetSourceLoadStatus", code)
	}
}

func (r *HTTPRoutes) UploadDatasetSourceLoadFile(ctx context.Context, request loomapi.UploadDatasetSourceLoadFileRequestObject) (loomapi.UploadDatasetSourceLoadFileResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	receipt, err := r.load.UploadSourceLoadFile(ctx, string(request.Project), request.Operation, request.ResourceType, request.Body, principal)
	if err == nil {
		return loomapi.UploadDatasetSourceLoadFile200JSONResponse(sourceFileReceiptValue(receipt)), nil
	}
	code, body := mapServiceError(err, ctx)
	switch code {
	case http.StatusBadRequest:
		return loomapi.UploadDatasetSourceLoadFile400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.UploadDatasetSourceLoadFile401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.UploadDatasetSourceLoadFile403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusNotFound:
		return loomapi.UploadDatasetSourceLoadFile404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(body)}, nil
	case http.StatusConflict:
		return loomapi.UploadDatasetSourceLoadFile409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(body)}, nil
	case http.StatusRequestEntityTooLarge:
		return loomapi.UploadDatasetSourceLoadFile413JSONResponse{ServicePayloadTooLargeJSONResponse: loomapi.ServicePayloadTooLargeJSONResponse(body)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.UploadDatasetSourceLoadFile415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(body)}, nil
	case http.StatusUnprocessableEntity:
		return sourceLoadValidationErrorResponse(body), nil
	case http.StatusInternalServerError:
		return loomapi.UploadDatasetSourceLoadFile500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.UploadDatasetSourceLoadFile503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("uploadDatasetSourceLoadFile", code)
	}
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
	switch code {
	case http.StatusBadRequest:
		return loomapi.PublishDatasetSourceLoad400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.PublishDatasetSourceLoad401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.PublishDatasetSourceLoad403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusNotFound:
		return loomapi.PublishDatasetSourceLoad404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(body)}, nil
	case http.StatusConflict:
		return loomapi.PublishDatasetSourceLoad409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(body)}, nil
	case http.StatusRequestEntityTooLarge:
		return loomapi.PublishDatasetSourceLoad413JSONResponse{ServicePayloadTooLargeJSONResponse: loomapi.ServicePayloadTooLargeJSONResponse(body)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.PublishDatasetSourceLoad415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(body)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.PublishDatasetSourceLoad422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(body)}, nil
	case http.StatusInternalServerError:
		return loomapi.PublishDatasetSourceLoad500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.PublishDatasetSourceLoad503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("publishDatasetSourceLoad", code)
	}
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
