package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

// explorerLegacyOperationError carries the response contract used by the
// legacy Explorer endpoints. The old Fiber handlers encoded either a
// structured {code,message} value or a plain error string depending on the
// route; keeping that distinction here lets generated OpenAPI responses use
// the same wire shape without executing a second handler.
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
	if r == nil || r.explorers == nil || r.authorizeRead == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizeRead(ctx, principal, project); err != nil {
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	values, err := r.explorers.List(ctx, project)
	if err != nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error(), err)
	}
	summaries := make([]explorer.ExplorerSummaryV1, 0, len(values))
	for _, value := range values {
		summaries = append(summaries, explorer.ExplorerSummaryV1{
			Project: project, ExplorerID: value.ExplorerID, Title: value.Title,
			Management: value.ManagementMode, ActiveRevisionID: value.ActiveRevisionID,
			UpdatedAt: value.UpdatedAt,
		})
	}
	raw, err := json.Marshal(summaries)
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
	if r == nil || r.explorers == nil || r.authorizeRead == nil || r.authorizer == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, ""); err != nil {
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	var request struct {
		Name             string `json:"name"`
		Title            string `json:"title,omitempty"`
		SourceExplorerID string `json:"sourceExplorerId,omitempty"`
	}
	if body == nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", nil)
	}
	raw, err := json.Marshal(*body)
	if err != nil || decodeStrict(raw, &request) != nil || strings.TrimSpace(request.Name) == "" {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", err)
	}
	id := explorer.StableExplorerID(request.Name)
	if id == "default" {
		return result, legacyOperationFailure(http.StatusConflict, "EXPLORER_EXISTS", "the repository default already exists", nil)
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = strings.TrimSpace(request.Name)
	}
	value, err := r.explorers.CreateInteractiveFrom(ctx, project, id, title, strings.TrimSpace(request.SourceExplorerID), subjectFromContext(ctx))
	if errors.Is(err, explorer.ErrDraftConflict) {
		return result, legacyOperationFailure(http.StatusConflict, "EXPLORER_EXISTS", "an Explorer with this name already exists", err)
	}
	if err != nil {
		return result, legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_EXPLORER", err.Error(), err)
	}
	summary := explorer.ExplorerSummaryV1{Project: project, ExplorerID: value.ExplorerID, Title: value.Title, Management: value.ManagementMode, UpdatedAt: value.UpdatedAt}
	raw, err = json.Marshal(summary)
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
	if r == nil || r.explorers == nil || r.authorizeRead == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	id := strings.TrimSpace(rawID)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizeRead(ctx, principal, project); err != nil {
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	state, err := r.explorers.LoadExplorerState(ctx, project, id)
	if errors.Is(err, explorer.ErrNotFound) {
		return result, legacyOperationFailure(http.StatusNotFound, "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "EXPLORER_READ_FAILED", err.Error(), err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func subjectFromContext(ctx context.Context) string {
	principal, _ := authscope.PrincipalFromContext(ctx)
	if principal == nil {
		return ""
	}
	return principal.Subject
}

func (r *explorerHTTPHandlers) publishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfig200JSONResponse, error) {
	var result loomapi.PublishRepositoryExplorerConfig200JSONResponse
	if r == nil || r.explorers == nil || r.authorizer == nil {
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
	config := r.lifecycleConfig
	if config.Capability == nil || config.AuthorizedCapabilityCompile == nil || config.CompileReceipt == nil {
		return result, legacyStringFailure(http.StatusServiceUnavailable, "repository V2 compilation is not configured", nil)
	}
	snapshot, err := config.Capability(ctx, project, "default", generation)
	if err != nil || !snapshot.Usable() || snapshot.Identity.Generation != generation {
		if err == nil {
			err = fmt.Errorf("capability generation %q does not match deployment generation %q", snapshot.Identity.Generation, generation)
		}
		return result, legacyStringFailure(http.StatusConflict, fmt.Sprintf("resolve repository V2 capability: %v", err), err)
	}
	authorized, err := config.AuthorizedCapabilityCompile(ctx, project, snapshot.Token)
	if err != nil {
		return result, legacyStringFailure(http.StatusForbidden, fmt.Sprintf("authorize repository V2 compilation: %v", err), err)
	}
	receipt, err := config.CompileReceipt(ctx, ExplorerV2ReceiptCompileRequest{Project: project, ExplorerID: "default", Workspace: workspace, SnapshotToken: snapshot.Token, RequestID: requestIDFromContext(ctx), Authorized: authorized})
	if err != nil || receipt == nil {
		if err == nil {
			err = fmt.Errorf("compiler returned no receipt")
		}
		return result, legacyStringFailure(http.StatusUnprocessableEntity, fmt.Sprintf("compile repository V2 workspace: %v", err), err)
	}
	if receipt.SourceGeneration != generation {
		return result, legacyStringFailure(http.StatusConflict, "compiled receipt generation does not match deployment generation", nil)
	}
	if config.AuthorizedCapabilityExecution != nil {
		authorized, err = config.AuthorizedCapabilityExecution(ctx, project, receipt.SnapshotToken)
		if err != nil {
			return result, legacyStringFailure(http.StatusForbidden, fmt.Sprintf("authorize repository V2 materialization: %v", err), err)
		}
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(project), DatasetGeneration: generation}
	applyAuthorizedScope(&bindings, authorized, true)
	var execution graphresolver.RecipeExecution
	if config.MaterializeReceipt != nil {
		execution, err = config.MaterializeReceipt(ctx, receipt, bindings)
	} else if r.materialize != nil {
		execution, err = r.materialize(ctx, receipt.Bundle, bindings)
	} else {
		err = fmt.Errorf("repository V2 materialization is not configured")
	}
	if err != nil {
		return result, legacyStringFailure(http.StatusUnprocessableEntity, fmt.Sprintf("materialize repository V2 workspace: %v", err), err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return result, legacyStringFailure(http.StatusUnprocessableEntity, err.Error(), err)
	}
	materializations := explorerMaterializations(receipt.Bundle, execution)
	datasetMetadata := datasetMetadataFromExecution(receipt.Bundle, generation, receipt.ResolvedSchemaDigest, execution)
	publication := explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: generation, ExecutionID: execution.ID, UpdatedAt: nowUTC()}
	owner, revision, err := r.explorers.UpsertRepositoryV2(ctx, *receipt, commit, subjectFromContext(ctx), materializations, datasetMetadata, publication)
	if err != nil {
		return result, legacyStringFailure(http.StatusInternalServerError, fmt.Sprintf("persist Explorer lifecycle V2: %v", err), err)
	}
	if owner.ManagementMode != explorer.ManagementRepository {
		return result, legacyStringFailure(http.StatusConflict, "default Explorer has invalid management mode", nil)
	}
	if config.ActivateRelease == nil {
		return result, legacyStringFailure(http.StatusServiceUnavailable, "release activation is not configured", nil)
	}
	if err := config.ActivateRelease(ctx, projectid.Legacy(project), generation, selectorsForBundle(receipt.Bundle)); err != nil {
		_, _ = r.explorers.FailRevision(ctx, revision.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "RELEASE_ACTIVATION_FAILED", Message: err.Error(), Retryable: true}})
		return result, legacyStringFailure(http.StatusConflict, fmt.Sprintf("activate published ExplorerConfigV2: %v", err), err)
	}
	if err := r.explorers.ActivateRepositoryGeneration(ctx, project, generation, revision.ID); err != nil {
		return result, legacyStringFailure(http.StatusConflict, fmt.Sprintf("activate ExplorerConfigV2: %v", err), err)
	}
	publication.State = string(explorer.RevisionActive)
	publication.RevisionID = revision.ID
	publication.UpdatedAt = nowUTC()
	if _, err := r.explorers.SaveRepositoryConfig(ctx, explorer.RepositoryConfig{
		Project: project, Config: append([]byte(nil), receipt.CompiledConfig...), Workspace: append([]byte(nil), receipt.NormalizedBundle...),
		ConfigDigest: receipt.IntentDigest, IntentDigest: receipt.IntentDigest, CompilationReceiptID: receipt.ID,
		PublicOutputContract: append([]byte(nil), receipt.PublicOutputContract...), ActiveRevisionID: revision.ID,
		DraftVersion: owner.DraftVersion, SourceGeneration: generation, SourceCommit: commit, ExecutionID: execution.ID,
		Materializations: materializations, Dataset: datasetMetadata, Publication: publication,
	}); err != nil {
		return result, legacyStringFailure(http.StatusInternalServerError, fmt.Sprintf("persist ExplorerConfigV2: %v", err), err)
	}
	raw := loomapi.RawJSON{"project": project, "generation": generation, "explorerId": "default", "receiptId": receipt.ID, "revisionId": revision.ID, "executionId": execution.ID, "recipe": receipt.Bundle.Name, "translationVersion": receipt.Bundle.TranslationVersion, "activated": true}
	result = loomapi.PublishRepositoryExplorerConfig200JSONResponse(raw)
	return result, nil
}

func requestIDFromContext(ctx context.Context) string {
	return httpapi.RequestIDFromContext(ctx)
}

func nowUTC() (now time.Time) {
	return time.Now().UTC()
}
