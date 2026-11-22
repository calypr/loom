package queryapi

import (
	"context"
	"fmt"
	"strings"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) prepareRunInput(ctx context.Context, input dataframemodel.FhirDataframeInput) (dataframemodel.FhirDataframeInput, authscope.ReadScope, string, error) {
	input.Project = projectid.Canonical(input.Project)
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
	resolver := scopedInputResolver{
		project: input.Project, datasetGeneration: generation,
		scope: scope, discoverFields: s.discoverFields,
	}
	if err := resolver.resolveNodeInputRefs(ctx, nodeInput{
		resourceType: input.RootResourceType, fields: input.RootFields,
		filters: input.RootFilters, pivots: input.RootPivots,
		aggregates: input.RootAggregates, slices: input.RootSlices,
	}); err != nil {
		return input, authscope.ReadScope{}, "", classifyError(err)
	}
	for _, step := range input.Traverse {
		if err := resolver.resolveTraversalInputRefs(ctx, step); err != nil {
			return input, authscope.ReadScope{}, "", classifyError(err)
		}
	}
	return input, scope.Clone(), generation, nil
}

type scopedInputResolver struct {
	project           string
	datasetGeneration string
	scope             authscope.ReadScope
	discoverFields    func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error)
}

type nodeInput struct {
	resourceType string
	fields       []*dataframemodel.FhirFieldSelectInput
	filters      []*dataframemodel.FhirFilterInput
	pivots       []*dataframemodel.FhirPivotInput
	aggregates   []*dataframemodel.FhirAggregateInput
	slices       []*dataframemodel.FhirRepresentativeSliceInput
}

func (r scopedInputResolver) resolveTraversalInputRefs(ctx context.Context, step *dataframemodel.FhirTraversalStepInput) error {
	if step == nil {
		return nil
	}
	if err := r.resolveNodeInputRefs(ctx, nodeInput{
		resourceType: step.ToResourceType, fields: step.Fields,
		filters: step.Filters, pivots: step.Pivots,
		aggregates: step.Aggregates, slices: step.Slices,
	}); err != nil {
		return err
	}
	for _, child := range step.Traverse {
		if err := r.resolveTraversalInputRefs(ctx, child); err != nil {
			return err
		}
	}
	return nil
}

func (r scopedInputResolver) resolveNodeInputRefs(ctx context.Context, input nodeInput) error {
	discovered, err := r.discoverFields(ctx, fieldprofile.PopulatedFieldOptions{
		Project:                       r.project,
		DatasetGeneration:             r.datasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(r.scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(r.scope.AuthResourcePaths),
		ResourceType:                  input.resourceType,
	})
	if err != nil {
		return queryBackend(err)
	}

	for _, field := range input.fields {
		if field == nil {
			continue
		}
		if strings.TrimSpace(derefString(field.FieldRef)) != "" {
			selectorText, err := resolveFieldRef(input.resourceType, discovered, derefString(field.FieldRef))
			if err != nil {
				return classifyError(err)
			}
			field.Selector = selectorInputFromExpression(selectorText)
		}
		if len(field.FallbackFieldRefs) > 0 {
			fallbacks := make([]*dataframemodel.FhirFieldSelectorInput, 0, len(field.FallbackFieldRefs))
			for _, ref := range field.FallbackFieldRefs {
				selectorText, err := resolveFieldRef(input.resourceType, discovered, ref)
				if err != nil {
					return err
				}
				fallbacks = append(fallbacks, selectorInputFromExpression(selectorText))
			}
			field.FallbackSelectors = fallbacks
		}
	}

	for _, pivot := range input.pivots {
		if pivot == nil {
			continue
		}
		if strings.TrimSpace(derefString(pivot.FieldRef)) != "" {
			hint, err := resolvePivotFieldRef(input.resourceType, discovered, derefString(pivot.FieldRef))
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

	for _, filter := range input.filters {
		if filter == nil || strings.TrimSpace(derefString(filter.FieldRef)) == "" {
			continue
		}
		selector, err := resolveFieldRef(input.resourceType, discovered, derefString(filter.FieldRef))
		if err != nil {
			return err
		}
		filter.Select = selector
	}

	for _, aggregate := range input.aggregates {
		if aggregate == nil {
			continue
		}
		if strings.TrimSpace(derefString(aggregate.FieldRef)) != "" {
			selector, err := resolveFieldRef(input.resourceType, discovered, derefString(aggregate.FieldRef))
			if err != nil {
				return err
			}
			aggregate.FhirPath = &selector
		}
		if strings.TrimSpace(derefString(aggregate.PredicateFieldRef)) != "" {
			selector, err := resolveFieldRef(input.resourceType, discovered, derefString(aggregate.PredicateFieldRef))
			if err != nil {
				return err
			}
			aggregate.PredicatePath = &selector
		}
	}

	for _, slice := range input.slices {
		if slice == nil {
			continue
		}
		if strings.TrimSpace(derefString(slice.WhereFieldRef)) != "" {
			selector, err := resolveFieldRef(input.resourceType, discovered, derefString(slice.WhereFieldRef))
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
				selectorText, err := resolveFieldRef(input.resourceType, discovered, derefString(field.FieldRef))
				if err != nil {
					return err
				}
				field.Selector = selectorInputFromExpression(selectorText)
			}
			if len(field.FallbackFieldRefs) > 0 {
				fallbacks := make([]*dataframemodel.FhirFieldSelectorInput, 0, len(field.FallbackFieldRefs))
				for _, ref := range field.FallbackFieldRefs {
					selectorText, err := resolveFieldRef(input.resourceType, discovered, ref)
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

func resolvePivotFieldRef(resourceType string, discovered []fieldprofile.PopulatedField, fieldRef string) (fieldprofile.PopulatedField, error) {
	fieldRef = strings.TrimSpace(fieldRef)
	if fieldRef == "" {
		return fieldprofile.PopulatedField{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	for _, field := range discovered {
		if defaultFieldRef(resourceType, field.Path) == fieldRef {
			return field, nil
		}
	}
	return fieldprofile.PopulatedField{}, dataframeerrors.NewError(dataframeerrors.CodeUnknownField, fmt.Sprintf("unknown pivot fieldRef %q for resourceType %q", fieldRef, resourceType))
}

func (s *Service) resolveReadScopeForGeneration(ctx context.Context, principal *authscope.Principal, project, datasetGeneration string, requested []string) (authscope.ReadScope, error) {
	if s.scopeResolver != nil {
		returnScope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, requested)
		return returnScope, queryBackend(err)
	}

	normalizedRequested := authscope.NormalizeAuthResourcePaths(requested)
	if len(requested) > 0 && len(normalizedRequested) == 0 {
		return authscope.ReadScope{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if len(normalizedRequested) == 0 {
		if principal == nil || len(principal.AuthResourcePaths) == 0 {
			return authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil
		}
		principalPaths := authscope.NormalizeAuthResourcePaths(principal.AuthResourcePaths)
		if len(principalPaths) == 0 {
			return authscope.ReadScope{}, dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
		}
		return authscope.ReadScope{
			AuthResourcePaths: principalPaths,
			Mode:              authscope.ReadScopeRestricted,
		}, nil
	}
	if principal == nil || len(principal.AuthResourcePaths) == 0 {
		return authscope.ReadScope{
			AuthResourcePaths: normalizedRequested,
			Mode:              authscope.ReadScopeRestricted,
		}, nil
	}
	principalPaths := authscope.NormalizeAuthResourcePaths(principal.AuthResourcePaths)
	if len(principalPaths) == 0 {
		return authscope.ReadScope{}, dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
	}
	for _, path := range normalizedRequested {
		found := false
		for _, candidate := range principalPaths {
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
		AuthResourcePaths: normalizedRequested,
		Mode:              authscope.ReadScopeRestricted,
	}, nil
}

func selectorInputFromExpression(expression string) *dataframemodel.FhirFieldSelectorInput {
	parts := decomposeSelector(expression)
	var where *dataframemodel.FhirFieldPredicateInput
	if parts.Where != nil {
		where = &dataframemodel.FhirFieldPredicateInput{
			Path:  parts.Where.Path,
			Op:    dataframemodel.FhirFieldPredicateOperation(parts.Where.Op),
			Value: parts.Where.Value,
		}
	}

	var sourcePath *string
	if trimmed := strings.TrimSpace(parts.SourcePath); trimmed != "" {
		sourcePath = &trimmed
	}

	return &dataframemodel.FhirFieldSelectorInput{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  parts.ValuePath,
	}
}
