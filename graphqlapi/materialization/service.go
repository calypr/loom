// Package materializationapi adapts published dataframe materializations to
// GraphQL. It owns read authorization and ClickHouse-backed row/aggregate
// access; dataframe compilation remains in internal/dataframe.
package materializationapi

import (
	"context"
	"fmt"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/materialization"
)

type Service struct {
	reader        *materialization.Reader
	scopeResolver *authscope.ScopeResolver
}

type Config struct {
	Reader        *materialization.Reader
	ScopeResolver *authscope.ScopeResolver
}

func NewService(cfg Config) *Service {
	return &Service{reader: cfg.Reader, scopeResolver: cfg.ScopeResolver}
}

func (s *Service) Get(ctx context.Context, id string) (*materialization.Materialization, error) {
	if s.reader == nil {
		return nil, fmt.Errorf("dataframe materialization reads are not configured")
	}
	value, err := s.reader.Registry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Service) Rows(ctx context.Context, input model.DataframeRowsInput) (materialization.Page, error) {
	if s.reader == nil {
		return materialization.Page{}, fmt.Errorf("dataframe materialization reads are not configured")
	}
	value, err := s.reader.Registry.Get(ctx, input.MaterializationID)
	if err != nil {
		return materialization.Page{}, err
	}
	if err := s.authorize(ctx, value); err != nil {
		return materialization.Page{}, err
	}
	filters := make([]materialization.Filter, 0, len(input.Filters))
	for _, filter := range input.Filters {
		if filter != nil {
			filters = append(filters, materialization.Filter{Column: filter.Column, Op: filter.Op, Value: filter.Value})
		}
	}
	var sortInput *materialization.Sort
	if input.Sort != nil {
		sortInput = &materialization.Sort{Column: input.Sort.Column, Desc: input.Sort.Desc != nil && *input.Sort.Desc}
	}
	first := 0
	if input.First != nil {
		first = *input.First
	}
	after := ""
	if input.After != nil {
		after = *input.After
	}
	return s.reader.Page(ctx, materialization.PageRequest{
		MaterializationID: input.MaterializationID,
		Columns:           input.Columns,
		Filters:           filters,
		Sort:              sortInput,
		First:             first,
		After:             after,
	})
}

func (s *Service) Aggregate(ctx context.Context, id string, groupBy []string, filters []*model.DataframeFilterInput, operation, column string) (materialization.AggregateResult, error) {
	if s.reader == nil {
		return materialization.AggregateResult{}, fmt.Errorf("dataframe materialization reads are not configured")
	}
	value, err := s.reader.Registry.Get(ctx, id)
	if err != nil {
		return materialization.AggregateResult{}, err
	}
	if err := s.authorize(ctx, value); err != nil {
		return materialization.AggregateResult{}, err
	}
	converted := make([]materialization.Filter, 0, len(filters))
	for _, filter := range filters {
		if filter != nil {
			converted = append(converted, materialization.Filter{Column: filter.Column, Op: filter.Op, Value: filter.Value})
		}
	}
	return s.reader.Aggregate(ctx, materialization.AggregateRequest{
		MaterializationID: id,
		GroupBy:           groupBy,
		Filters:           converted,
		Operation:         operation,
		Column:            column,
	})
}

func (s *Service) authorize(ctx context.Context, value materialization.Materialization) error {
	principal, _ := authscope.PrincipalFromContext(ctx)
	if principal != nil && len(principal.Projects) > 0 {
		allowed := false
		for _, project := range principal.Projects {
			if project == value.Project {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("principal is not authorized for project %q", value.Project)
		}
	}
	if value.AuthScopeMode == authscope.ReadScopeUnrestricted {
		if s.scopeResolver != nil {
			scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, nil)
			if err != nil {
				return err
			}
			if !scope.Unrestricted() {
				return fmt.Errorf("materialization %q requires unrestricted authorization", value.ID)
			}
		} else if principal != nil && len(principal.AuthResourcePaths) > 0 {
			return fmt.Errorf("materialization %q requires unrestricted authorization", value.ID)
		}
		return nil
	}
	if s.scopeResolver != nil {
		scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, value.AuthResourcePaths)
		if err != nil {
			return err
		}
		if len(scope.AuthResourcePaths) != len(value.AuthResourcePaths) {
			return fmt.Errorf("materialization %q is outside caller scope", value.ID)
		}
		return nil
	}
	if principal == nil {
		return fmt.Errorf("materialization %q requires an authorized principal", value.ID)
	}
	for _, requested := range value.AuthResourcePaths {
		found := false
		for _, allowed := range principal.AuthResourcePaths {
			if requested == allowed {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("materialization %q is outside caller scope", value.ID)
		}
	}
	return nil
}
