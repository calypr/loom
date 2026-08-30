package dataframe

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

// ProjectDatasets exposes the current output dataframes for one authorized
// project. It is the only dataset-discovery surface.
func (s *Service) ProjectDatasets(ctx context.Context, projectID string) ([]dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	projectID = strings.TrimSpace(projectID)
	if _, err := s.authorizeProject(ctx, principal, projectID); err != nil {
		return nil, err
	}
	values, err := s.reader.CurrentProjectDatasets(ctx, projectID)
	if err != nil {
		return nil, mapReaderError(err)
	}
	result := make([]dfmaterialization.Materialization, 0, len(values))
	for _, value := range values {
		if _, scopeErr := s.authorizeMaterialization(ctx, principal, value); scopeErr == nil {
			result = append(result, value)
		} else if normalized := dataframeerrors.Normalize(scopeErr); normalized.Code() != string(dataframeerrors.CodeDatasetNotFound) {
			return nil, scopeErr
		}
	}
	if len(result) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	return result, nil
}

func (s *Service) Dataset(ctx context.Context, input model.DataframeDatasetInput) (*dfmaterialization.Materialization, error) {
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return nil, err
	}
	value, _, err := s.currentProjectDataset(ctx, input.ProjectID, selector)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Service) Rows(ctx context.Context, input model.DataframeRowsInput) (dfmaterialization.Page, error) {
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.Page{}, err
	}
	value, access, err := s.currentProjectDataset(ctx, input.ProjectID, selector)
	if err != nil {
		s.logReadFailure(ctx, "published_dataset_resolution", selector.Output, err, "project", input.ProjectID)
		return dfmaterialization.Page{}, err
	}
	page, err := s.reader.Page(ctx, value, dfmaterialization.PageRequest{
		Columns: input.Columns, Filters: convertFilters(input.Filters), Sort: convertSort(input), First: intValue(input.First), After: stringValue(input.After),
		AuthResourcePaths: access.authResourcePaths, Unrestricted: access.unrestricted,
	})
	if err != nil {
		s.logReadFailure(ctx, "clickhouse_rows", selector.Output, err, "project", input.ProjectID)
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	return page, nil
}

func resolveSelector(input *model.DataframeSelectorInput) (dfmaterialization.DataframeSelector, error) {
	if input == nil {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input", "selector"))
	}
	selector := dfmaterialization.DataframeSelector{Recipe: strings.TrimSpace(input.Recipe), TranslationVersion: strings.TrimSpace(input.TranslationVersion), Output: strings.TrimSpace(input.Output)}
	if !selector.Valid() {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input", "selector"))
	}
	return selector, nil
}

func resolvePublishedSelector(input *dfmaterialization.DataframeSelector) (dfmaterialization.DataframeSelector, error) {
	if input == nil {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	selector := *input
	selector.Recipe = strings.TrimSpace(selector.Recipe)
	selector.TranslationVersion = strings.TrimSpace(selector.TranslationVersion)
	selector.Output = strings.TrimSpace(selector.Output)
	if !selector.Valid() {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	return selector, nil
}

func convertFilters(filters []*model.DataframeFilterInput) []dfmaterialization.Filter {
	converted := make([]dfmaterialization.Filter, 0, len(filters))
	for _, filter := range filters {
		if filter == nil {
			continue
		}
		var value any
		if len(filter.Value) > 0 {
			if err := json.Unmarshal(filter.Value, &value); err != nil {
				value = filter.Value
			}
		}
		converted = append(converted, dfmaterialization.Filter{Column: filter.Column, Op: filter.Op, Value: value})
	}
	return converted
}

func convertSort(input model.DataframeRowsInput) *dfmaterialization.Sort {
	if input.Sort == nil {
		return nil
	}
	return &dfmaterialization.Sort{Column: input.Sort.Column, Desc: input.Sort.Desc != nil && *input.Sort.Desc}
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
