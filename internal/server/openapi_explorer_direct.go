package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

// explorerLegacyOperationError carries the legacy Explorer response contract.
// It is a server-side transport value; lifecycle errors are mapped into it at
// the generated OpenAPI boundary.
type explorerLegacyOperationError struct {
	status      int
	code        string
	message     string
	stringValue bool
	cause       error
}

func (e *explorerLegacyOperationError) Error() string {
	if e == nil {
		return "Explorer operation failed"
	}
	return e.message
}

func (e *explorerLegacyOperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func legacyOperationFailure(status int, code, message string, cause error) error {
	return &explorerLegacyOperationError{status: status, code: code, message: message, cause: cause}
}

func legacyStringFailure(status int, message string, cause error) error {
	return &explorerLegacyOperationError{status: status, message: message, stringValue: true, cause: cause}
}

func lifecycleLegacyFailure(err error) error {
	if err == nil {
		return nil
	}
	var value *lifecycle.Error
	if !errors.As(err, &value) {
		return err
	}
	status := http.StatusInternalServerError
	switch value.Class {
	case lifecycle.ClassMalformed:
		status = http.StatusBadRequest
	case lifecycle.ClassForbidden:
		status = http.StatusForbidden
	case lifecycle.ClassNotFound:
		status = http.StatusNotFound
	case lifecycle.ClassConflict:
		status = http.StatusConflict
	case lifecycle.ClassUnprocessable:
		status = http.StatusUnprocessableEntity
	case lifecycle.ClassUnavailable:
		status = http.StatusServiceUnavailable
	}
	return legacyOperationFailure(status, value.Code, value.Message, err)
}

func explorerLegacyResponse(err error) (int, loomapi.LegacyErrorResponse) {
	var operation *explorerLegacyOperationError
	if errors.As(err, &operation) {
		if operation.stringValue {
			var response loomapi.LegacyErrorResponse
			_ = response.Error.FromLegacyErrorResponseError0(operation.message)
			return operation.status, response
		}
		var response loomapi.LegacyErrorResponse
		_ = response.Error.FromLegacyErrorBody(loomapi.LegacyErrorBody{Code: operation.code, Message: operation.message})
		return operation.status, response
	}
	var response loomapi.LegacyErrorResponse
	_ = response.Error.FromLegacyErrorBody(loomapi.LegacyErrorBody{Code: "INTERNAL_ERROR", Message: "internal server error"})
	return http.StatusInternalServerError, response
}

func (r *explorerHTTPHandlers) listExplorers(ctx context.Context, rawProject string) (loomapi.ListExplorers200JSONResponse, error) {
	var result loomapi.ListExplorers200JSONResponse
	if r == nil || r.application == nil || r.authorizeRead == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizeRead(ctx, principal, project); err != nil {
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	value, err := r.application.List(ctx, project)
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	raw, err := json.Marshal(value.Summaries)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *explorerHTTPHandlers) createExplorer(ctx context.Context, rawProject string, body *loomapi.CreateExplorerJSONRequestBody) (loomapi.CreateExplorer201JSONResponse, error) {
	var result loomapi.CreateExplorer201JSONResponse
	if r == nil || r.application == nil || r.authorizer == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, ""); err != nil {
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	if body == nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", nil)
	}
	var request struct {
		Name             string `json:"name"`
		Title            string `json:"title,omitempty"`
		SourceExplorerID string `json:"sourceExplorerId,omitempty"`
	}
	raw, err := json.Marshal(*body)
	if err != nil || decodeStrict(raw, &request) != nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", err)
	}
	value, err := r.application.Create(ctx, lifecycle.CreateRequest{Project: project, Name: request.Name, Title: request.Title, SourceExplorerID: request.SourceExplorerID, Actor: subjectFromContext(ctx)})
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *explorerHTTPHandlers) getExplorer(ctx context.Context, rawProject, rawID string) (loomapi.GetExplorer200JSONResponse, error) {
	var result loomapi.GetExplorer200JSONResponse
	if r == nil || r.application == nil || r.authorizeRead == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizeRead(ctx, principal, project); err != nil {
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	value, err := r.application.Get(ctx, project, rawID)
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *explorerHTTPHandlers) publishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfig200JSONResponse, error) {
	var result loomapi.PublishRepositoryExplorerConfig200JSONResponse
	if r == nil || r.application == nil || r.authorizer == nil {
		return result, legacyStringFailure(http.StatusInternalServerError, "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(string(request.Project))
	generation := strings.TrimSpace(string(request.Generation))
	if project == "" || generation == "" {
		return result, legacyStringFailure(http.StatusBadRequest, "project and generation are required", nil)
	}
	authResourcePath := ""
	if request.Params.AuthResourcePath != nil {
		authResourcePath = authscope.NormalizeAuthResourcePath(strings.TrimSpace(*request.Params.AuthResourcePath))
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return result, legacyStringFailure(http.StatusForbidden, "forbidden", err)
	}
	if request.Body == nil {
		return result, legacyStringFailure(http.StatusUnprocessableEntity, "workspace is required", nil)
	}
	workspace := authoringv2.Workspace(*request.Body)
	if err := workspace.ValidateForPublication(); err != nil {
		return result, legacyStringFailure(http.StatusUnprocessableEntity, err.Error(), err)
	}
	commit := strings.TrimSpace(request.Params.XLoomSourceCommit)
	if commit == "" {
		return result, legacyStringFailure(http.StatusBadRequest, "X-Loom-Source-Commit is required", nil)
	}
	value, err := r.application.PublishRepository(ctx, lifecycle.RepositoryPublishRequest{Project: project, Generation: generation, Workspace: workspace, Commit: commit, Actor: subjectFromContext(ctx)})
	if err != nil {
		status, legacy := explorerLegacyResponse(lifecycleLegacyFailure(err))
		return result, legacyStringFailure(status, legacyErrorMessage(legacy), err)
	}
	return loomapi.PublishRepositoryExplorerConfig200JSONResponse(loomapi.RawJSON{"project": project, "generation": generation, "explorerId": "default", "receiptId": value.Receipt.ID, "revisionId": value.Revision.ID, "executionId": value.Execution.ID, "recipe": value.Receipt.Bundle.Name, "translationVersion": value.Receipt.Bundle.TranslationVersion, "activated": true}), nil
}

func legacyErrorMessage(value loomapi.LegacyErrorResponse) string {
	if message, err := value.Error.AsLegacyErrorResponseError0(); err == nil {
		return message
	}
	if body, err := value.Error.AsLegacyErrorBody(); err == nil && strings.TrimSpace(body.Message) != "" {
		return body.Message
	}
	return "Explorer lifecycle failed"
}
