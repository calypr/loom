package graphqlapi

import (
	"arangodb-proto/internal/dataframe"
	"arangodb-proto/internal/graphqlapi/model"
	"arangodb-proto/internal/proto"
	"encoding/json"
	"strings"
)

func traversalHints(in []proto.PopulatedReference) []*model.DataframeTraversalHint {
	if len(in) == 0 {
		return []*model.DataframeTraversalHint{}
	}
	out := make([]*model.DataframeTraversalHint, 0, len(in))
	for _, item := range in {
		out = append(out, &model.DataframeTraversalHint{
			FromType:  item.FromType,
			Label:     item.Label,
			ToType:    item.ToType,
			EdgeCount: int(item.EdgeCount),
		})
	}
	return out
}

func fieldHints(in []FieldHintResponse) []*model.DataframeFieldHint {
	if len(in) == 0 {
		return []*model.DataframeFieldHint{}
	}
	out := make([]*model.DataframeFieldHint, 0, len(in))
	for _, item := range in {
		pivotKind := item.PivotKind
		var pivotKindPtr *string
		if pivotKind != "" {
			pivotKindPtr = &pivotKind
		}
		var pivotFamily *model.FhirPivotFamily
		if item.PivotFamily != "" {
			pf := model.FhirPivotFamily(item.PivotFamily)
			pivotFamily = &pf
		}
		var predicate *model.DataframeFieldPredicate
		if item.Selector.Where != nil {
			predicate = &model.DataframeFieldPredicate{
				Path:  item.Selector.Where.Path,
				Op:    model.FhirFieldPredicateOperation(item.Selector.Where.Op),
				Value: item.Selector.Where.Value,
			}
		}
		out = append(out, &model.DataframeFieldHint{
			ResourceType:      item.ResourceType,
			FieldRef:          item.FieldRef,
			Label:             item.Label,
			Path:              item.Path,
			Selector: &model.DataframeFieldSelector{
				SourcePath: optionalString(item.Selector.SourcePath),
				Where:      predicate,
				ValuePath:  item.Selector.ValuePath,
			},
			Kind:              item.Kind,
			DocCount:          int(item.DocCount),
			SampleCount:       item.SampleCount,
			DistinctValues:    cloneStrings(item.DistinctValues),
			DistinctTruncated: item.DistinctTruncated,
			PivotCandidate:    item.PivotCandidate,
			PivotKind:         pivotKindPtr,
			PivotColumns:      cloneStrings(item.PivotColumns),
			PivotFamily:       pivotFamily,
			DefaultPivotColumnSelector: selectorModelFromExpression(item.PivotColumnSelect),
			DefaultPivotValueSelector:  selectorModelFromExpression(item.PivotValueSelect),
		})
	}
	return out
}

func resourceHints(in ResourceHintsResponse) *model.DataframeResourceHints {
	return &model.DataframeResourceHints{
		ResourceType: in.ResourceType,
		Fields:       fieldHints(in.Fields),
		PivotFields:  fieldHints(in.PivotFields),
		Traversals:   traversalHints(in.Traversals),
	}
}

func relatedResourceHints(in []RelatedResourceHintsResponse) []*model.DataframeRelatedResourceHints {
	if len(in) == 0 {
		return []*model.DataframeRelatedResourceHints{}
	}
	out := make([]*model.DataframeRelatedResourceHints, 0, len(in))
	for _, item := range in {
		out = append(out, &model.DataframeRelatedResourceHints{
			ViaLabel:  item.ViaLabel,
			EdgeCount: int(item.EdgeCount),
			Target:    resourceHints(item.Target),
		})
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func cloneRows(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(in))
	for _, row := range in {
		cloned := make(map[string]any, len(row))
		for k, v := range row {
			cloned[k] = v
		}
		out = append(out, cloned)
	}
	return out
}

func graphqlRows(in []map[string]any) json.RawMessage {
	rows := cloneRows(in)
	if len(rows) == 0 {
		return json.RawMessage("[]")
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return json.RawMessage("[]")
	}
	return json.RawMessage(encoded)
}

func builderFromInput(in model.FhirDataframeInput) dataframe.Builder {
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

func selectorModelFromExpression(expression string) *model.DataframeFieldSelector {
	if strings.TrimSpace(expression) == "" {
		return nil
	}
	parts := decomposeSelector(expression)
	var predicate *model.DataframeFieldPredicate
	if parts.Where != nil {
		predicate = &model.DataframeFieldPredicate{
			Path:  parts.Where.Path,
			Op:    model.FhirFieldPredicateOperation(parts.Where.Op),
			Value: parts.Where.Value,
		}
	}
	return &model.DataframeFieldSelector{
		SourcePath: optionalString(parts.SourcePath),
		Where:      predicate,
		ValuePath:  parts.ValuePath,
	}
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
		operation := item.Operation.String()
		out = append(out, dataframe.AggregateSelect{
			Name:            item.Name,
			Operation:       operation,
			FieldRef:        strings.TrimSpace(derefString(item.FieldRef)),
			Select:          strings.TrimSpace(derefString(item.FhirPath)),
			PredicateFieldRef: strings.TrimSpace(derefString(item.PredicateFieldRef)),
			PredicatePath:   strings.TrimSpace(derefString(item.PredicatePath)),
			PredicateEquals: derefString(item.PredicateEquals),
			ValueMode:       item.ValueMode.String(),
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
			Name:            item.Name,
			Limit:           item.Limit,
			PredicateFieldRef: strings.TrimSpace(derefString(item.WhereFieldRef)),
			PredicatePath:   strings.TrimSpace(derefString(item.WherePath)),
			PredicateEquals: derefString(item.WhereEquals),
			Fields:          fieldSelectsFromModel(item.Fields),
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

func derefBool(in *bool) bool {
	return in != nil && *in
}

func optionalString(in string) *string {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil
	}
	return &in
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
