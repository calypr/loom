package dataframe

import (
	"context"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/fhirschema"
)

func (s *Service) expandPivotColumns(ctx context.Context, builder Builder) (Builder, error) {
	pivots, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           builder.Project,
		AuthResourcePaths: builder.AuthResourcePaths,
		ResourceType:      builder.RootResourceType,
		PivotOnly:         true,
	})
	if err != nil {
		return Builder{}, err
	}
	builder.Pivots = fillPivotColumns(builder.Pivots, pivots)
	for i := range builder.Traversals {
		if err := s.expandTraversalPivotColumns(ctx, builder.Project, builder.AuthResourcePaths, &builder.Traversals[i]); err != nil {
			return Builder{}, err
		}
	}
	return builder, nil
}

func (s *Service) expandTraversalPivotColumns(ctx context.Context, project string, authResourcePaths []string, step *TraversalStep) error {
	pivots, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		ResourceType:      step.ToResourceType,
		PivotOnly:         true,
	})
	if err != nil {
		return err
	}
	step.Pivots = fillPivotColumns(step.Pivots, pivots)
	for i := range step.Traversals {
		if err := s.expandTraversalPivotColumns(ctx, project, authResourcePaths, &step.Traversals[i]); err != nil {
			return err
		}
	}
	return nil
}

func fillPivotColumns(in []PivotSelect, discovered []catalog.PopulatedField) []PivotSelect {
	if len(in) == 0 {
		return []PivotSelect{}
	}
	out := make([]PivotSelect, 0, len(in))
	resourceType := resourceTypeFromDiscovered(discovered)
	for _, pivot := range in {
		if pivot.PivotFamily == "" {
			columnSel, colErr := ParseSelector(pivot.ColumnSelect)
			valueSel, valErr := ParseSelector(pivot.ValueSelect)
			if colErr == nil && valErr == nil {
				spec, err := fhirschema.ValidatePivotSelectors(resourceType, selectorSpecFromSelector(columnSel), selectorSpecFromSelector(valueSel))
				if err == nil {
					pivot.PivotFamily = spec.Family
				}
			}
		}
		out = append(out, pivot)
	}
	return out
}

func resourceTypeFromDiscovered(fields []catalog.PopulatedField) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0].ResourceType
}
