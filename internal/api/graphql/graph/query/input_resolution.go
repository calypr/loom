package queryapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func (s *Service) PrepareRunInput(ctx context.Context, input model.FhirDataframeInput) (model.FhirDataframeInput, error) {
	prepared, _, _, err := s.prepareRunInput(ctx, input)
	return prepared, err
}

// prepareRunInput resolves field references and returns the effective scope
// and selected generation alongside the GraphQL-shaped input. The public
// PrepareRunInput keeps the GraphQL transport shape while Run carries the
// resolved scope and generation into recipe.RuntimeBindings.
func (s *Service) prepareRunInput(ctx context.Context, input model.FhirDataframeInput) (model.FhirDataframeInput, authscope.ReadScope, string, error) {
	if input.Project == "" {
		return input, authscope.ReadScope{}, "", dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if input.RootResourceType == "" {
		return input, authscope.ReadScope{}, "", dataframeerrors.NewError(dataframeerrors.CodeRootResourceTypeRequired, "")
	}

	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, input.Project, s.scopeResolver != nil); err != nil {
		return input, authscope.ReadScope{}, "", classifyError(err)
	}
	generation, err := s.resolveActiveGeneration(ctx, input.Project)
	if err != nil {
		return input, authscope.ReadScope{}, "", classifyError(err)
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, input.Project, generation, input.AuthResourcePaths)
	if err != nil {
		return input, authscope.ReadScope{}, "", classifyError(err)
	}

	input.AuthResourcePaths = cloneStrings(scope.AuthResourcePaths)
	if len(input.AuthResourcePaths) == 0 {
		input.AuthResourcePaths = nil
	}
	if err := s.resolveNodeInputRefs(ctx, input.Project, generation, scope, input.RootResourceType, input.RootFields, input.RootFilters, input.RootPivots, input.RootAggregates, input.RootSlices); err != nil {
		return input, authscope.ReadScope{}, "", classifyError(err)
	}
	for _, step := range input.Traverse {
		if err := s.resolveTraversalInputRefs(ctx, input.Project, generation, scope, step); err != nil {
			return input, authscope.ReadScope{}, "", classifyError(err)
		}
	}
	return input, scope.Clone(), generation, nil
}

func (s *Service) resolveTraversalInputRefs(ctx context.Context, project, datasetGeneration string, scope authscope.ReadScope, step *model.FhirTraversalStepInput) error {
	if step == nil {
		return nil
	}
	if err := s.resolveNodeInputRefs(ctx, project, datasetGeneration, scope, step.ToResourceType, step.Fields, step.Filters, step.Pivots, step.Aggregates, step.Slices); err != nil {
		return err
	}
	for _, child := range step.Traverse {
		if err := s.resolveTraversalInputRefs(ctx, project, datasetGeneration, scope, child); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resolveNodeInputRefs(ctx context.Context, project, datasetGeneration string, scope authscope.ReadScope, resourceType string, fields []*model.FhirFieldSelectInput, filters []*model.FhirFilterInput, pivots []*model.FhirPivotInput, aggregates []*model.FhirAggregateInput, slices []*model.FhirRepresentativeSliceInput) error {
	discovered, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             datasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
		ResourceType:                  resourceType,
	})
	if err != nil {
		return queryBackend(err)
	}

	for _, field := range fields {
		if field == nil {
			continue
		}
		if strings.TrimSpace(derefString(field.FieldRef)) != "" {
			selectorText, err := resolveFieldRef(resourceType, discovered, derefString(field.FieldRef))
			if err != nil {
				return classifyError(err)
			}
			field.Selector = selectorInputFromExpression(selectorText)
		}
		if len(field.FallbackFieldRefs) > 0 {
			fallbacks := make([]*model.FhirFieldSelectorInput, 0, len(field.FallbackFieldRefs))
			for _, ref := range field.FallbackFieldRefs {
				selectorText, err := resolveFieldRef(resourceType, discovered, ref)
				if err != nil {
					return err
				}
				fallbacks = append(fallbacks, selectorInputFromExpression(selectorText))
			}
			field.FallbackSelectors = fallbacks
		}
	}

	for _, pivot := range pivots {
		if pivot == nil {
			continue
		}
		if strings.TrimSpace(derefString(pivot.FieldRef)) != "" {
			hint, err := resolvePivotFieldRef(resourceType, discovered, derefString(pivot.FieldRef))
			if err != nil {
				return err
			}
			if pivot.ColumnSelector == nil {
				pivot.ColumnSelector = selectorInputFromExpression(hint.PivotColumnSelect)
			}
			if pivot.ValueSelector == nil {
				pivot.ValueSelector = selectorInputFromExpression(hint.PivotValueSelect)
			}
		}
	}

	for _, filter := range filters {
		if filter == nil || strings.TrimSpace(derefString(filter.FieldRef)) == "" {
			continue
		}
		selector, err := resolveFieldRef(resourceType, discovered, derefString(filter.FieldRef))
		if err != nil {
			return err
		}
		filter.Select = selector
	}

	for _, aggregate := range aggregates {
		if aggregate == nil {
			continue
		}
		if strings.TrimSpace(derefString(aggregate.FieldRef)) != "" {
			selector, err := resolveFieldRef(resourceType, discovered, derefString(aggregate.FieldRef))
			if err != nil {
				return err
			}
			aggregate.FhirPath = &selector
		}
		if strings.TrimSpace(derefString(aggregate.PredicateFieldRef)) != "" {
			selector, err := resolveFieldRef(resourceType, discovered, derefString(aggregate.PredicateFieldRef))
			if err != nil {
				return err
			}
			aggregate.PredicatePath = &selector
		}
	}

	for _, slice := range slices {
		if slice == nil {
			continue
		}
		if strings.TrimSpace(derefString(slice.WhereFieldRef)) != "" {
			selector, err := resolveFieldRef(resourceType, discovered, derefString(slice.WhereFieldRef))
			if err != nil {
				return err
			}
			slice.WherePath = &selector
		}
		for _, field := range slice.Fields {
			if field == nil {
				continue
			}
			if strings.TrimSpace(derefString(field.FieldRef)) != "" {
				selectorText, err := resolveFieldRef(resourceType, discovered, derefString(field.FieldRef))
				if err != nil {
					return err
				}
				field.Selector = selectorInputFromExpression(selectorText)
			}
			if len(field.FallbackFieldRefs) > 0 {
				fallbacks := make([]*model.FhirFieldSelectorInput, 0, len(field.FallbackFieldRefs))
				for _, ref := range field.FallbackFieldRefs {
					selectorText, err := resolveFieldRef(resourceType, discovered, ref)
					if err != nil {
						return err
					}
					fallbacks = append(fallbacks, selectorInputFromExpression(selectorText))
				}
				field.FallbackSelectors = fallbacks
			}
		}
	}

	return nil
}

func resolvePivotFieldRef(resourceType string, discovered []catalog.PopulatedField, fieldRef string) (catalog.PopulatedField, error) {
	fieldRef = strings.TrimSpace(fieldRef)
	if fieldRef == "" {
		return catalog.PopulatedField{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	for _, field := range discovered {
		if defaultFieldRef(resourceType, field.Path) == fieldRef {
			return field, nil
		}
	}
	return catalog.PopulatedField{}, dataframeerrors.NewError(dataframeerrors.CodeUnknownField, fmt.Sprintf("unknown pivot fieldRef %q for resourceType %q", fieldRef, resourceType))
}

func (s *Service) resolveReadScopeForGeneration(ctx context.Context, principal *authscope.Principal, project, datasetGeneration string, requested []string) (authscope.ReadScope, error) {
	if s.scopeResolver != nil {
		returnScope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, requested)
		return returnScope, queryBackend(err)
	}
	if len(requested) == 0 {
		if principal == nil || len(principal.AuthResourcePaths) == 0 {
			return authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil
		}
		return authscope.ReadScope{
			AuthResourcePaths: append([]string(nil), principal.AuthResourcePaths...),
			Mode:              authscope.ReadScopeRestricted,
		}, nil
	}
	if principal == nil || len(principal.AuthResourcePaths) == 0 {
		return authscope.ReadScope{
			AuthResourcePaths: append([]string(nil), requested...),
			Mode:              authscope.ReadScopeRestricted,
		}, nil
	}
	for _, path := range requested {
		found := false
		for _, candidate := range principal.AuthResourcePaths {
			if candidate == path {
				found = true
				break
			}
		}
		if !found {
			return authscope.ReadScope{}, dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
		}
	}
	return authscope.ReadScope{
		AuthResourcePaths: append([]string(nil), requested...),
		Mode:              authscope.ReadScopeRestricted,
	}, nil
}

func selectorInputFromExpression(expression string) *model.FhirFieldSelectorInput {
	parts := decomposeSelector(expression)
	var where *model.FhirFieldPredicateInput
	if parts.Where != nil {
		where = &model.FhirFieldPredicateInput{
			Path:  parts.Where.Path,
			Op:    model.FhirFieldPredicateOperation(parts.Where.Op),
			Value: parts.Where.Value,
		}
	}

	var sourcePath *string
	if trimmed := strings.TrimSpace(parts.SourcePath); trimmed != "" {
		sourcePath = &trimmed
	}

	return &model.FhirFieldSelectorInput{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  parts.ValuePath,
	}
}
