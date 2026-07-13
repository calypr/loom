package queryapi

import (
	"strings"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe"
)

func BuilderFromInput(in model.FhirDataframeInput) dataframe.Builder {
	authResourcePaths := cloneStrings(in.AuthResourcePaths)
	if len(authResourcePaths) == 0 && strings.TrimSpace(derefString(in.AuthResourcePath)) != "" {
		authResourcePaths = []string{strings.TrimSpace(derefString(in.AuthResourcePath))}
	}
	return dataframe.Builder{
		Project:           in.Project,
		AuthResourcePaths: authResourcePaths,
		RootResourceType:  in.RootResourceType,
		Fields:            fieldSelectsFromModel(in.RootFields),
		Pivots:            pivotSelectsFromModel(in.RootPivots),
		Aggregates:        aggregateSelectsFromModel(in.RootAggregates),
		Slices:            sliceSelectsFromModel(in.RootSlices),
		Traversals:        traversalStepsFromModel(in.Traverse),
	}
}

func fieldSelectsFromModel(in []*model.FhirFieldSelectInput) []dataframe.FieldSelect {
	if len(in) == 0 {
		return []dataframe.FieldSelect{}
	}
	out := make([]dataframe.FieldSelect, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		selectText := ""
		if item.Selector != nil {
			selectText = composeSelector(
				derefString(item.Selector.SourcePath),
				predicatePathFromInput(item.Selector.Where),
				predicateOpFromInput(item.Selector.Where),
				predicateValueFromInput(item.Selector.Where),
				item.Selector.ValuePath,
			)
		}
		fallbackSelectors := make([]string, 0, len(item.FallbackSelectors))
		for _, fallback := range item.FallbackSelectors {
			if fallback == nil {
				continue
			}
			fallbackSelectors = append(fallbackSelectors, composeSelector(
				derefString(fallback.SourcePath),
				predicatePathFromInput(fallback.Where),
				predicateOpFromInput(fallback.Where),
				predicateValueFromInput(fallback.Where),
				fallback.ValuePath,
			))
		}
		out = append(out, dataframe.FieldSelect{
			Name:              item.Name,
			FieldRef:          derefString(item.FieldRef),
			Select:            selectText,
			FallbackFieldRefs: cloneStrings(item.FallbackFieldRefs),
			FallbackSelects:   fallbackSelectors,
			ValueMode:         item.ValueMode.String(),
		})
	}
	return out
}

func pivotSelectsFromModel(in []*model.FhirPivotInput) []dataframe.PivotSelect {
	if len(in) == 0 {
		return []dataframe.PivotSelect{}
	}
	out := make([]dataframe.PivotSelect, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		out = append(out, dataframe.PivotSelect{
			Name:         item.Name,
			FieldRef:     derefString(item.FieldRef),
			ColumnSelect: composeSelectorFromInput(item.ColumnSelector),
			ValueSelect:  composeSelectorFromInput(item.ValueSelector),
			Columns:      cloneStrings(item.Columns),
		})
	}
	return out
}

func composeSelectorFromInput(in *model.FhirFieldSelectorInput) string {
	if in == nil {
		return ""
	}
	return composeSelector(
		derefString(in.SourcePath),
		predicatePathFromInput(in.Where),
		predicateOpFromInput(in.Where),
		predicateValueFromInput(in.Where),
		in.ValuePath,
	)
}

func aggregateSelectsFromModel(in []*model.FhirAggregateInput) []dataframe.AggregateSelect {
	if len(in) == 0 {
		return []dataframe.AggregateSelect{}
	}
	out := make([]dataframe.AggregateSelect, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		out = append(out, dataframe.AggregateSelect{
			Name:              item.Name,
			Operation:         item.Operation.String(),
			FieldRef:          strings.TrimSpace(derefString(item.FieldRef)),
			Select:            strings.TrimSpace(derefString(item.FhirPath)),
			PredicateFieldRef: strings.TrimSpace(derefString(item.PredicateFieldRef)),
			PredicatePath:     strings.TrimSpace(derefString(item.PredicatePath)),
			PredicateEquals:   derefString(item.PredicateEquals),
			ValueMode:         item.ValueMode.String(),
		})
	}
	return out
}

func sliceSelectsFromModel(in []*model.FhirRepresentativeSliceInput) []dataframe.RepresentativeSlice {
	if len(in) == 0 {
		return []dataframe.RepresentativeSlice{}
	}
	out := make([]dataframe.RepresentativeSlice, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		out = append(out, dataframe.RepresentativeSlice{
			Name:              item.Name,
			Limit:             item.Limit,
			PredicateFieldRef: strings.TrimSpace(derefString(item.WhereFieldRef)),
			PredicatePath:     strings.TrimSpace(derefString(item.WherePath)),
			PredicateEquals:   derefString(item.WhereEquals),
			Fields:            fieldSelectsFromModel(item.Fields),
		})
	}
	return out
}

func traversalStepsFromModel(in []*model.FhirTraversalStepInput) []dataframe.TraversalStep {
	if len(in) == 0 {
		return []dataframe.TraversalStep{}
	}
	out := make([]dataframe.TraversalStep, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		out = append(out, dataframe.TraversalStep{
			Label:          item.EdgeLabel,
			ToResourceType: item.ToResourceType,
			Alias:          item.Alias,
			Fields:         fieldSelectsFromModel(item.Fields),
			Pivots:         pivotSelectsFromModel(item.Pivots),
			Aggregates:     aggregateSelectsFromModel(item.Aggregates),
			Slices:         sliceSelectsFromModel(item.Slices),
			Traversals:     traversalStepsFromModel(item.Traverse),
		})
	}
	return out
}

func derefString(in *string) string {
	if in == nil {
		return ""
	}
	return *in
}

func predicatePathFromInput(in *model.FhirFieldPredicateInput) string {
	if in == nil {
		return ""
	}
	return in.Path
}

func predicateOpFromInput(in *model.FhirFieldPredicateInput) string {
	if in == nil {
		return ""
	}
	return in.Op.String()
}

func predicateValueFromInput(in *model.FhirFieldPredicateInput) string {
	if in == nil {
		return ""
	}
	return in.Value
}
