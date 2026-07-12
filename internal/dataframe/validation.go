package dataframe

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/catalog"
)

func (s *Service) validateBuilder(ctx context.Context, builder Builder) error {
	seenAliases := map[string]struct{}{}
	authResourcePathsUnrestricted := builderAuthScopeUnrestricted(builder)
	rootFields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       builder.Project,
		DatasetGeneration:             builder.DatasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authResourcePathsUnrestricted),
		AuthResourcePaths:             builder.AuthResourcePaths,
		ResourceType:                  builder.RootResourceType,
	})
	if err != nil {
		return err
	}
	rootPivots, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       builder.Project,
		DatasetGeneration:             builder.DatasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authResourcePathsUnrestricted),
		AuthResourcePaths:             builder.AuthResourcePaths,
		ResourceType:                  builder.RootResourceType,
		PivotOnly:                     true,
	})
	if err != nil {
		return err
	}
	if err := validateNodeSelections(builder.Fields, builder.Filters, builder.Pivots, builder.Aggregates, builder.Slices, rootFields, rootPivots); err != nil {
		return err
	}
	for _, step := range builder.Traversals {
		if err := s.validateTraversal(ctx, builder.Project, builder.DatasetGeneration, builder.AuthResourcePaths, authResourcePathsUnrestricted, builder.RootResourceType, step, seenAliases); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateTraversal(ctx context.Context, project, datasetGeneration string, authResourcePaths []string, authResourcePathsUnrestricted bool, sourceType string, step TraversalStep, seenAliases map[string]struct{}) error {
	if err := step.MatchMode.Validate(); err != nil {
		return fmt.Errorf("traversal %s -> %s (%s): %w", sourceType, step.ToResourceType, step.Label, err)
	}
	if step.Alias == "" {
		return fmt.Errorf("traversal alias is required")
	}
	if _, ok := seenAliases[step.Alias]; ok {
		return fmt.Errorf("traversal alias %q is duplicated", step.Alias)
	}
	seenAliases[step.Alias] = struct{}{}

	refs, err := s.discoverReferences(ctx, catalog.PopulatedReferenceOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             datasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authResourcePathsUnrestricted),
		AuthResourcePaths:             authResourcePaths,
		NodeType:                      sourceType,
		Mode:                          catalog.TraversalModeBuilder,
	})
	if err != nil {
		return err
	}
	found := false
	for _, ref := range refs {
		if ref.Label == step.Label && ref.ToType == step.ToResourceType {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("traversal %s -> %s (%s) is not populated", sourceType, step.ToResourceType, step.Label)
	}

	fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             datasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authResourcePathsUnrestricted),
		AuthResourcePaths:             authResourcePaths,
		ResourceType:                  step.ToResourceType,
	})
	if err != nil {
		return err
	}
	pivotFields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       project,
		DatasetGeneration:             datasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authResourcePathsUnrestricted),
		AuthResourcePaths:             authResourcePaths,
		ResourceType:                  step.ToResourceType,
		PivotOnly:                     true,
	})
	if err != nil {
		return err
	}
	if err := validateNodeSelections(step.Fields, step.Filters, step.Pivots, step.Aggregates, step.Slices, fields, pivotFields); err != nil {
		return fmt.Errorf("alias %s: %w", step.Alias, err)
	}
	for _, child := range step.Traversals {
		if err := s.validateTraversal(ctx, project, datasetGeneration, authResourcePaths, authResourcePathsUnrestricted, step.ToResourceType, child, seenAliases); err != nil {
			return err
		}
	}
	return nil
}

func validateNodeSelections(fields []FieldSelect, filters []TypedFilter, pivots []PivotSelect, aggregates []AggregateSelect, slices []RepresentativeSlice, discovered []catalog.PopulatedField, pivotable []catalog.PopulatedField) error {
	seenFields := map[string]struct{}{}
	for _, field := range fields {
		if field.Name == "" || field.Select == "" {
			return fmt.Errorf("field selections require name and select")
		}
		if _, ok := seenFields[field.Name]; ok {
			return fmt.Errorf("field name %q is duplicated", field.Name)
		}
		seenFields[field.Name] = struct{}{}
		if _, err := ParseSelector(field.Select); err != nil {
			return fmt.Errorf("invalid selector for field %q: %w", field.Name, err)
		}
		for _, fallback := range field.FallbackSelects {
			if _, err := ParseSelector(fallback); err != nil {
				return fmt.Errorf("invalid fallback selector for field %q: %w", field.Name, err)
			}
		}
	}

	for _, filter := range filters {
		if err := ValidateTypedFilterForResource(resourceTypeFromDiscovered(discovered), filter); err != nil {
			return fmt.Errorf("invalid filter %q: %w", filter.FieldRef, err)
		}
		sel, err := ParseSelector(filter.Selector)
		if err != nil {
			return fmt.Errorf("invalid filter selector for %q: %w", filter.FieldRef, err)
		}
		if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
			return fmt.Errorf("filter selector %q is not present in populated fields", filter.Selector)
		}
	}

	seenPivots := map[string]struct{}{}
	for _, pivot := range pivots {
		if pivot.Name == "" || pivot.ColumnSelect == "" || pivot.ValueSelect == "" {
			return fmt.Errorf("pivot selections require name, column selector, and value selector")
		}
		if _, ok := seenPivots[pivot.Name]; ok {
			return fmt.Errorf("pivot name %q is duplicated", pivot.Name)
		}
		seenPivots[pivot.Name] = struct{}{}

		columnSel, err := ParseSelector(pivot.ColumnSelect)
		if err != nil {
			return fmt.Errorf("invalid column selector for pivot %q: %w", pivot.Name, err)
		}
		valueSel, err := ParseSelector(pivot.ValueSelect)
		if err != nil {
			return fmt.Errorf("invalid value selector for pivot %q: %w", pivot.Name, err)
		}

		pivotSpec, err := fhirschema.ValidatePivotSelectors(resourceTypeFromDiscovered(discovered), selectorSpecFromSelector(columnSel), selectorSpecFromSelector(valueSel))
		if err != nil {
			return fmt.Errorf("pivot %q: %w", pivot.Name, err)
		}
		match := findFieldByPath(pivotable, pivotSpec.CatalogRootPath)
		if match == nil || !match.PivotCandidate {
			return fmt.Errorf("pivot selector %q is not pivotable", pivot.ColumnSelect)
		}
		if len(pivot.Columns) == 0 && len(match.PivotColumns) == 0 {
			return fmt.Errorf("pivot %q has no available pivot columns", pivot.Name)
		}
		pivot.PivotFamily = pivotSpec.Family
	}

	seenAggregates := map[string]struct{}{}
	for _, agg := range aggregates {
		if strings.TrimSpace(agg.Name) == "" {
			return fmt.Errorf("aggregate selections require name")
		}
		if _, ok := seenAggregates[agg.Name]; ok {
			return fmt.Errorf("aggregate name %q is duplicated", agg.Name)
		}
		seenAggregates[agg.Name] = struct{}{}
		switch strings.ToUpper(strings.TrimSpace(agg.Operation)) {
		case "COUNT", "COUNT_DISTINCT", "EXISTS", "DISTINCT_VALUES", "MIN", "MAX":
		default:
			return fmt.Errorf("aggregate %q uses unsupported operation %q", agg.Name, agg.Operation)
		}
		if strings.TrimSpace(agg.Select) != "" {
			sel, err := ParseSelector(agg.Select)
			if err != nil {
				return fmt.Errorf("invalid aggregate selector for %q: %w", agg.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("aggregate selector %q is not present in populated fields", agg.Select)
			}
		}
		if aggregateOperationRequiresSelector(strings.ToUpper(strings.TrimSpace(agg.Operation))) && strings.TrimSpace(agg.Select) == "" {
			return fmt.Errorf("aggregate %q operation %s requires a selector", agg.Name, agg.Operation)
		}
		if strings.TrimSpace(agg.PredicatePath) != "" {
			sel, err := ParseSelector(agg.PredicatePath)
			if err != nil {
				return fmt.Errorf("invalid aggregate predicate selector for %q: %w", agg.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("aggregate predicate selector %q is not present in populated fields", agg.PredicatePath)
			}
		}
	}

	seenSlices := map[string]struct{}{}
	for _, slice := range slices {
		if strings.TrimSpace(slice.Name) == "" {
			return fmt.Errorf("representative slices require name")
		}
		if _, ok := seenSlices[slice.Name]; ok {
			return fmt.Errorf("representative slice name %q is duplicated", slice.Name)
		}
		seenSlices[slice.Name] = struct{}{}
		if slice.Limit <= 0 {
			return fmt.Errorf("representative slice %q requires positive limit", slice.Name)
		}
		if strings.TrimSpace(slice.PredicatePath) != "" {
			sel, err := ParseSelector(slice.PredicatePath)
			if err != nil {
				return fmt.Errorf("invalid representative slice predicate for %q: %w", slice.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("representative slice predicate %q is not present in populated fields", slice.PredicatePath)
			}
		}
		for _, field := range slice.Fields {
			if strings.TrimSpace(field.Name) == "" || strings.TrimSpace(field.Select) == "" {
				return fmt.Errorf("representative slice %q requires fields with name and select", slice.Name)
			}
			sel, err := ParseSelector(field.Select)
			if err != nil {
				return fmt.Errorf("invalid representative slice field for %q: %w", slice.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("representative slice selector %q is not present in populated fields", field.Select)
			}
			for _, fallback := range field.FallbackSelects {
				fallbackSel, err := ParseSelector(fallback)
				if err != nil {
					return fmt.Errorf("invalid representative slice fallback selector for %q: %w", slice.Name, err)
				}
				if findFieldByPath(discovered, fallbackSel.CanonicalPath()) == nil {
					return fmt.Errorf("representative slice fallback selector %q is not present in populated fields", fallback)
				}
			}
		}
	}

	for _, field := range fields {
		sel, _ := ParseSelector(field.Select)
		if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
			return fmt.Errorf("selector %q is not present in populated fields", field.Select)
		}
		for _, fallback := range field.FallbackSelects {
			fallbackSel, _ := ParseSelector(fallback)
			if findFieldByPath(discovered, fallbackSel.CanonicalPath()) == nil {
				return fmt.Errorf("fallback selector %q is not present in populated fields", fallback)
			}
		}
	}
	return nil
}

func selectorSpecFromSelector(sel Selector) fhirschema.FieldSelectorSpec {
	sourcePath := ""
	valuePath := ""
	if len(sel.Steps) > 0 {
		last := len(sel.Steps) - 1
		valuePath = selectorStepText(sel.Steps[last])
		if last > 0 {
			parts := make([]string, 0, last)
			for _, step := range sel.Steps[:last] {
				parts = append(parts, selectorStepText(step))
			}
			sourcePath = strings.Join(parts, ".")
		}
	}
	var where *fhirschema.FieldPredicateSpec
	if sel.Filter != nil {
		where = &fhirschema.FieldPredicateSpec{
			Path:  sel.Filter.Field,
			Op:    fhirschema.PredicateContains,
			Value: sel.Filter.Needle,
		}
	}
	return fhirschema.FieldSelectorSpec{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  valuePath,
	}
}

func findFieldByPath(fields []catalog.PopulatedField, path string) *catalog.PopulatedField {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
}
