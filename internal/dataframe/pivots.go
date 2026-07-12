package dataframe

import (
	"context"
	"fmt"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/catalog"
)

func (s *Service) expandPivotColumns(ctx context.Context, builder Builder) (Builder, error) {
	pivots, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             s.connOpts,
		Project:                       builder.Project,
		DatasetGeneration:             builder.DatasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(builderAuthScopeUnrestricted(builder)),
		AuthResourcePaths:             builder.AuthResourcePaths,
		ResourceType:                  builder.RootResourceType,
		PivotOnly:                     true,
	})
	if err != nil {
		return Builder{}, err
	}
	resolved, err := fillPivotColumns(builder.Pivots, pivots)
	if err != nil {
		return Builder{}, err
	}
	builder.Pivots = resolved
	for i := range builder.Traversals {
		if err := s.expandTraversalPivotColumns(ctx, builder.Project, builder.DatasetGeneration, builder.AuthResourcePaths, builderAuthScopeUnrestricted(builder), &builder.Traversals[i]); err != nil {
			return Builder{}, err
		}
	}
	return builder, nil
}

func (s *Service) expandTraversalPivotColumns(ctx context.Context, project, datasetGeneration string, authResourcePaths []string, authResourcePathsUnrestricted bool, step *TraversalStep) error {
	pivots, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
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
	resolved, err := fillPivotColumns(step.Pivots, pivots)
	if err != nil {
		return err
	}
	step.Pivots = resolved
	for i := range step.Traversals {
		if err := s.expandTraversalPivotColumns(ctx, project, datasetGeneration, authResourcePaths, authResourcePathsUnrestricted, &step.Traversals[i]); err != nil {
			return err
		}
	}
	return nil
}

// fillPivotColumns resolves an omitted user column list to the bounded,
// scope-aware catalog values computed at ingest time. Rendering an empty list
// would materialize every observed pivot key and is not a safe fallback.
func fillPivotColumns(in []PivotSelect, discovered []catalog.PopulatedField) ([]PivotSelect, error) {
	if len(in) == 0 {
		return []PivotSelect{}, nil
	}
	out := make([]PivotSelect, 0, len(in))
	resourceType := resourceTypeFromDiscovered(discovered)
	for _, pivot := range in {
		columnSel, err := ParseSelector(pivot.ColumnSelect)
		if err != nil {
			return nil, fmt.Errorf("pivot %q column selector: %w", pivot.Name, err)
		}
		valueSel, err := ParseSelector(pivot.ValueSelect)
		if err != nil {
			return nil, fmt.Errorf("pivot %q value selector: %w", pivot.Name, err)
		}
		spec, err := fhirschema.ValidatePivotSelectors(resourceType, selectorSpecFromSelector(columnSel), selectorSpecFromSelector(valueSel))
		if err != nil {
			return nil, fmt.Errorf("pivot %q: %w", pivot.Name, err)
		}
		if pivot.PivotFamily == "" {
			pivot.PivotFamily = spec.Family
		}
		if len(pivot.Columns) == 0 {
			match := findFieldByPath(discovered, spec.CatalogRootPath)
			if match == nil || len(match.PivotColumns) == 0 {
				return nil, fmt.Errorf("pivot %q has no bounded catalog columns", pivot.Name)
			}
			pivot.Columns = cloneStrings(match.PivotColumns)
		}
		out = append(out, pivot)
	}
	return out, nil
}

func resourceTypeFromDiscovered(fields []catalog.PopulatedField) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0].ResourceType
}
