package graphqlapi

import (
	"arangodb-proto/internal/dataframe"
	"arangodb-proto/internal/graphqlapi/model"
	"arangodb-proto/internal/proto"
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

func fieldHints(in []proto.PopulatedField) []*model.DataframeFieldHint {
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
		out = append(out, &model.DataframeFieldHint{
			ResourceType:      item.ResourceType,
			Path:              item.Path,
			Selector:          item.Path,
			Kind:              item.Kind,
			DocCount:          int(item.DocCount),
			SampleCount:       item.SampleCount,
			DistinctValues:    cloneStrings(item.DistinctValues),
			DistinctTruncated: item.DistinctTruncated,
			PivotCandidate:    item.PivotCandidate,
			PivotKind:         pivotKindPtr,
			PivotColumns:      cloneStrings(item.PivotColumns),
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

func graphqlRows(in []map[string]any) []*model.FhirDataframeRow {
	if len(in) == 0 {
		return []*model.FhirDataframeRow{}
	}
	out := make([]*model.FhirDataframeRow, 0, len(in))
	for _, row := range cloneRows(in) {
		out = append(out, &model.FhirDataframeRow{Data: row})
	}
	return out
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
		out = append(out, dataframe.FieldSelect{Name: item.Name, Select: item.FhirPath})
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
		pivotKind := ""
		if item.PivotKind != nil {
			pivotKind = item.PivotKind.String()
		}
		out = append(out, dataframe.PivotSelect{
			Name:      item.Name,
			Select:    item.FhirPath,
			PivotKind: pivotKind,
			Columns:   cloneStrings(item.SelectedColumns),
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
