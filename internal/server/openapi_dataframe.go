package server

import (
	"context"
	"encoding/json"

	httpapi "github.com/calypr/loom/internal/api/http"

	loomapi "github.com/calypr/loom/api/loomapi"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"

	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"

	queryapi "github.com/calypr/loom/internal/api/dataframe/query"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/projectid"
)

type dataframeReadRoutes struct {
	published *dataframeapi.Service
	query     *queryapi.Service
}

func (r *HTTPRoutes) PostDataframeRows(ctx context.Context, request loomapi.PostDataframeRowsRequestObject) (loomapi.PostDataframeRowsResponseObject, error) {
	result, err := r.dataframes.rows(ctx, request.Project, request.Body)
	if err != nil {
		status, body := httpapi.MapServiceError(err, ctx)
		return loomapi.NewPostDataframeRowsErrorResponse(status, body)
	}
	return loomapi.PostDataframeRows200JSONResponse(result), nil
}

func (r *HTTPRoutes) PostDataframeAggregations(ctx context.Context, request loomapi.PostDataframeAggregationsRequestObject) (loomapi.PostDataframeAggregationsResponseObject, error) {
	result, err := r.dataframes.aggregations(ctx, request.Project, request.Body)
	if err != nil {
		status, body := httpapi.MapServiceError(err, ctx)
		return loomapi.NewPostDataframeAggregationsErrorResponse(status, body)
	}
	return loomapi.PostDataframeAggregations200JSONResponse(result), nil
}

func (r *HTTPRoutes) PostFhirDataframe(ctx context.Context, request loomapi.PostFhirDataframeRequestObject) (loomapi.PostFhirDataframeResponseObject, error) {
	result, err := r.dataframes.run(ctx, request.Project, request.Body)
	if err != nil {
		status, body := httpapi.MapServiceError(err, ctx)
		return loomapi.NewPostFhirDataframeErrorResponse(status, body)
	}
	return loomapi.PostFhirDataframe200JSONResponse(result), nil
}

func (r dataframeReadRoutes) rows(ctx context.Context, project string, input *dataframemodel.DataframeRowsInput) (dataframemodel.DataframeRowConnection, error) {
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
		Materialization: dataframeapi.Model(page.Materialization),
		Columns:         append([]string{}, page.Columns...), Rows: rows,
		RowIds: append([]string{}, page.RowIDs...), TotalCount: &count, PageInfo: info,
	}, nil
}

func (r dataframeReadRoutes) aggregations(ctx context.Context, project string, input *dataframemodel.DataframeAggregationsInput) (dataframemodel.DataframeAggregationsResult, error) {
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
	aggregations, err := dataframeapi.AggregationsJSON(result)
	if err != nil {
		return dataframemodel.DataframeAggregationsResult{}, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return dataframemodel.DataframeAggregationsResult{Materialization: dataframeapi.Model(result.Materialization), Aggregations: aggregations}, nil
}

func (r dataframeReadRoutes) run(ctx context.Context, project string, body *loomapi.FhirDataframeRequest) (dataframemodel.FhirDataframeResult, error) {
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
	limit := body.Limit
	if limit == nil {
		defaultLimit := 25
		limit = &defaultLimit
	}
	result, err := r.query.Run(ctx, input, limit)
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
