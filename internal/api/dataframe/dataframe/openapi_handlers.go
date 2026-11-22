package dataframe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	httpapi "github.com/calypr/loom/internal/api/http"

	loomapi "github.com/calypr/loom/api/loomapi"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"

	queryapi "github.com/calypr/loom/internal/api/dataframe/query"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/errors/authpolicy"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/projectid"
)

type Handlers struct {
	published           *Service
	query               *queryapi.Service
	readRecipeExecution RecipeExecutionReader
}

type RecipeExecutionReader func(context.Context, string) (publication.BundleExecution, error)

func (r *Handlers) GetRecipeExecution(ctx context.Context, request loomapi.GetRecipeExecutionRequestObject) (loomapi.GetRecipeExecutionResponseObject, error) {
	if r.readRecipeExecution == nil {
		return recipeExecutionNotFoundResponse(), nil
	}
	execution, err := r.readRecipeExecution(ctx, request.Id)
	if err != nil {
		return recipeExecutionErrorResponse(ctx, err)
	}
	return loomapi.GetRecipeExecution200JSONResponse(recipeExecutionResponse(execution)), nil
}

func (r *Handlers) PostDataframeRows(ctx context.Context, request loomapi.PostDataframeRowsRequestObject) (loomapi.PostDataframeRowsResponseObject, error) {
	result, err := r.rows(ctx, request.Project, request.Body)
	if err != nil {
		status, body := httpapi.MapServiceError(err, ctx)
		return loomapi.NewPostDataframeRowsErrorResponse(status, body)
	}
	return loomapi.PostDataframeRows200JSONResponse(result), nil
}

func (r *Handlers) PostDataframeAggregations(ctx context.Context, request loomapi.PostDataframeAggregationsRequestObject) (loomapi.PostDataframeAggregationsResponseObject, error) {
	result, err := r.aggregations(ctx, request.Project, request.Body)
	if err != nil {
		status, body := httpapi.MapServiceError(err, ctx)
		return loomapi.NewPostDataframeAggregationsErrorResponse(status, body)
	}
	return loomapi.PostDataframeAggregations200JSONResponse(result), nil
}

func (r *Handlers) PostFhirDataframe(ctx context.Context, request loomapi.PostFhirDataframeRequestObject) (loomapi.PostFhirDataframeResponseObject, error) {
	result, err := r.run(ctx, request.Project, request.Body)
	if err != nil {
		status, body := httpapi.MapServiceError(err, ctx)
		return loomapi.NewPostFhirDataframeErrorResponse(status, body)
	}
	return loomapi.PostFhirDataframe200JSONResponse(result), nil
}

func (r *Handlers) rows(ctx context.Context, project string, input *dataframemodel.DataframeRowsInput) (dataframemodel.DataframeRowConnection, error) {
	if input == nil {
		return dataframemodel.DataframeRowConnection{}, missingDataframeInput()
	}
	if err := matchDataframeProject(project, input.ProjectID); err != nil {
		return dataframemodel.DataframeRowConnection{}, err
	}
	if r.published == nil {
		return dataframemodel.DataframeRowConnection{}, dataframeReadUnavailable()
	}
	query := *input
	query.ProjectID = project
	page, err := r.published.Rows(ctx, query)
	if err != nil {
		return dataframemodel.DataframeRowConnection{}, err
	}
	rows, err := json.Marshal(page.Rows)
	if err != nil {
		return dataframemodel.DataframeRowConnection{}, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	count := int(page.TotalCount)
	info := &dataframemodel.DataframePageInfo{HasNextPage: page.HasNext}
	if page.NextCursor != "" {
		info.EndCursor = &page.NextCursor
	}
	return dataframemodel.DataframeRowConnection{
		Materialization: Model(page.Materialization),
		Columns:         append([]string{}, page.Columns...), Rows: rows,
		RowIds: append([]string{}, page.RowIDs...), TotalCount: &count, PageInfo: info,
	}, nil
}

func (r *Handlers) aggregations(ctx context.Context, project string, input *dataframemodel.DataframeAggregationsInput) (dataframemodel.DataframeAggregationsResult, error) {
	if input == nil {
		return dataframemodel.DataframeAggregationsResult{}, missingDataframeInput()
	}
	if err := matchDataframeProject(project, input.ProjectID); err != nil {
		return dataframemodel.DataframeAggregationsResult{}, err
	}
	if r.published == nil {
		return dataframemodel.DataframeAggregationsResult{}, dataframeReadUnavailable()
	}
	query := *input
	query.ProjectID = project
	result, err := r.published.AggregationsInput(ctx, query)
	if err != nil {
		return dataframemodel.DataframeAggregationsResult{}, err
	}
	aggregations, err := AggregationsJSON(result)
	if err != nil {
		return dataframemodel.DataframeAggregationsResult{}, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return dataframemodel.DataframeAggregationsResult{Materialization: Model(result.Materialization), Aggregations: aggregations}, nil
}

func (r *Handlers) run(ctx context.Context, project string, body *loomapi.FhirDataframeRequest) (dataframemodel.FhirDataframeResult, error) {
	if body == nil {
		return dataframemodel.FhirDataframeResult{}, missingDataframeInput()
	}
	if err := matchDataframeProject(project, body.Input.Project); err != nil {
		return dataframemodel.FhirDataframeResult{}, err
	}
	if r.query == nil {
		return dataframemodel.FhirDataframeResult{}, dataframeReadUnavailable()
	}
	input := body.Input
	input.Project = project
	result, err := r.query.RunPreview(ctx, input, body.Limit)
	if err != nil {
		return dataframemodel.FhirDataframeResult{}, err
	}
	rows, err := json.Marshal(result.Rows)
	if err != nil {
		return dataframemodel.FhirDataframeResult{}, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return dataframemodel.FhirDataframeResult{
		Columns: append([]string{}, result.Columns...), Rows: rows,
		RowCount: result.RowCount, Diagnostics: dataframemodel.Diagnostics(result.Diagnostics),
	}, nil
}

func matchDataframeProject(path, body string) error {
	if body != "" && projectid.Canonical(path) != projectid.Canonical(body) {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "body project must match the path project")
	}
	return nil
}

func missingDataframeInput() error {
	return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "dataframe input is required")
}

func dataframeReadUnavailable() error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func NewHandlers(published *Service, query *queryapi.Service, readRecipeExecution RecipeExecutionReader) *Handlers {
	return &Handlers{published: published, query: query, readRecipeExecution: readRecipeExecution}
}

func recipeExecutionResponse(execution publication.BundleExecution) loomapi.RecipeExecutionResponse {
	outputs := make([]loomapi.RecipeExecutionOutput, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		columns := make([]loomapi.RecipeExecutionColumn, 0, len(output.Columns))
		for _, column := range output.Columns {
			capabilities := publication.ResolveColumnCapabilities(column.ClickHouse, column.LogicalType, column.Nullable, column.Repeated)
			columns = append(columns, loomapi.RecipeExecutionColumn{
				Name:           column.Name,
				SemanticPath:   column.SemanticPath,
				ClickhouseType: column.ClickHouse,
				LogicalType:    capabilities.Logical,
				Nullable:       capabilities.Nullable,
				Repeated:       capabilities.Repeated,
				Filterable:     capabilities.Filterable,
				Sortable:       capabilities.Sortable,
				Aggregatable:   capabilities.Aggregatable,
			})
		}
		item := loomapi.RecipeExecutionOutput{
			Name:     output.Name,
			State:    recipeExecutionHTTPState(output.State),
			RowCount: output.RowCount,
			Columns:  columns,
		}
		mirroredError := RecipeExecutionOutputMessage(execution.Error, output.State)
		item.Error, item.ErrorCode, item.ErrorRetryable = PersistedFailure(mirroredError, output.FailureCode, output.FailureRetryable)
		if output.FailurePhase != "" {
			item.Phase = &output.FailurePhase
		}
		outputs = append(outputs, item)
	}
	response := loomapi.RecipeExecutionResponse{
		Id:                   execution.ID,
		ProjectId:            execution.Project,
		DatasetGeneration:    execution.DatasetGeneration,
		RecipeDigest:         execution.RecipeDigest,
		SchemaDigest:         execution.SchemaDigest,
		ResolvedSchemaDigest: execution.SchemaDigest,
		State:                recipeExecutionHTTPState(execution.State),
		Outputs:              outputs,
	}
	response.Error, response.ErrorCode, response.ErrorRetryable = PersistedFailure(execution.Error, execution.FailureCode, execution.FailureRetryable)
	if execution.FailurePhase != "" {
		response.Phase = &execution.FailurePhase
	}
	return response
}

func recipeExecutionErrorResponse(ctx context.Context, err error) (loomapi.GetRecipeExecutionResponseObject, error) {
	var failure *RecipeExecutionReadFailure
	if errors.As(err, &failure) {
		if failure.Authorization {
			return recipeExecutionAuthorizationResponse(ctx, failure.Err)
		}
		return recipeExecutionLookupResponse(ctx, failure.Err)
	}
	return recipeExecutionLookupResponse(ctx, err)
}

func recipeExecutionLookupResponse(ctx context.Context, err error) (loomapi.GetRecipeExecutionResponseObject, error) {
	if err == nil || errors.Is(err, publication.ErrBundleNotFound) {
		return recipeExecutionNotFoundResponse(), nil
	}
	classified := authpolicy.Classify(err, authpolicy.OperationConcealedLookup)
	if userErr, ok := dataframeerrors.AsUserError(classified); ok {
		if dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
			return recipeExecutionNotFoundResponse(), nil
		}
	} else {
		classified = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	status, response := httpapi.MapServiceError(classified, ctx)
	if status != http.StatusServiceUnavailable {
		status, response = httpapi.MapServiceError(dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), ctx)
	}
	return recipeExecutionServiceResponse(status, response)
}

func recipeExecutionAuthorizationResponse(ctx context.Context, err error) (loomapi.GetRecipeExecutionResponseObject, error) {
	status := recipeExecutionAuthorizationStatus(err)
	if status == http.StatusUnauthorized {
		return loomapi.GetRecipeExecution401JSONResponse{"error": "recipe execution not found"}, nil
	}
	if status == http.StatusNotFound {
		return recipeExecutionNotFoundResponse(), nil
	}
	mappedStatus, response := httpapi.MapServiceError(err, ctx)
	if mappedStatus != http.StatusServiceUnavailable {
		_, response = httpapi.MapServiceError(dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), ctx)
	}
	return loomapi.NewGetRecipeExecutionErrorResponse(http.StatusServiceUnavailable, response)
}

func recipeExecutionAuthorizationStatus(err error) int {
	switch {
	case errors.Is(err, authscope.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, authscope.ErrForbidden):
		return http.StatusNotFound
	default:
		return http.StatusServiceUnavailable
	}
}

func recipeExecutionServiceResponse(status int, response loomapi.ServiceErrorResponse) (loomapi.GetRecipeExecutionResponseObject, error) {
	if status == http.StatusServiceUnavailable {
		return loomapi.NewGetRecipeExecutionErrorResponse(status, response)
	}
	properties := map[string]any{}
	body := response.Error
	if body.FieldPath != nil {
		properties["fieldPath"] = *body.FieldPath
	}
	if body.Details != nil {
		properties["details"] = *body.Details
	}
	if body.RequestId != nil {
		properties["requestId"] = *body.RequestId
	}
	if body.Retryable != nil {
		properties["retryable"] = *body.Retryable
	}
	if status == http.StatusUnauthorized {
		properties["code"], properties["message"] = body.Code, body.Message
		return loomapi.NewGetRecipeExecutionErrorResponse(status, loomapi.RawJSON{"error": properties})
	}
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return nil, errors.New("legacy error response is missing a string error")
	}
	return nil, httpapi.UnexpectedResponseStatus("getRecipeExecution", status)
}

func recipeExecutionNotFoundResponse() loomapi.GetRecipeExecution404JSONResponse {
	var response loomapi.LegacyErrorResponse
	_ = response.Error.FromLegacyErrorResponseError0("recipe execution not found")
	return loomapi.GetRecipeExecution404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(response)}
}

func recipeExecutionHTTPState(state publication.BundleState) string {
	if state == publication.BundlePublished {
		return string(publication.BundleReady)
	}
	return string(state)
}
