package resolver

import (
	"encoding/json"
	"strings"

	"github.com/calypr/loom/generated/graphqlapi/model"
	queryapi "github.com/calypr/loom/internal/graphqlapi/query"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func traversalHints(in []catalog.PopulatedReference) []*model.DataframeTraversalHint {
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

func fieldHints(in []queryapi.FieldHint) []*model.DataframeFieldHint {
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
			family := model.FhirPivotFamily(item.PivotFamily)
			pivotFamily = &family
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
			ResourceType: item.ResourceType,
			FieldRef:     item.FieldRef,
			Label:        item.Label,
			Path:         item.Path,
			Selector: &model.DataframeFieldSelector{
				SourcePath: optionalString(item.Selector.SourcePath),
				Where:      predicate,
				ValuePath:  item.Selector.ValuePath,
			},
			Kind:                       item.Kind,
			DocCount:                   int(item.DocCount),
			SampleCount:                item.SampleCount,
			DistinctValues:             cloneStrings(item.DistinctValues),
			DistinctTruncated:          item.DistinctTruncated,
			PivotCandidate:             item.PivotCandidate,
			PivotKind:                  pivotKindPtr,
			PivotColumns:               cloneStrings(item.PivotColumns),
			PivotFamily:                pivotFamily,
			DefaultPivotColumnSelector: selectorModelFromExpression(item.PivotColumnSelect),
			DefaultPivotValueSelector:  selectorModelFromExpression(item.PivotValueSelect),
		})
	}
	return out
}

func resourceHints(in queryapi.ResourceHints) *model.DataframeResourceHints {
	return &model.DataframeResourceHints{
		ResourceType: in.ResourceType,
		Fields:       fieldHints(in.Fields),
		PivotFields:  fieldHints(in.PivotFields),
		Traversals:   traversalHints(in.Traversals),
	}
}

func relatedResourceHints(in []queryapi.RelatedResourceHints) []*model.DataframeRelatedResourceHints {
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

func selectorModelFromExpression(expression string) *model.DataframeFieldSelector {
	if strings.TrimSpace(expression) == "" {
		return nil
	}
	parts := queryapi.DecomposeSelector(expression)

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

func graphqlRows(in []map[string]any) (json.RawMessage, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return json.RawMessage(encoded), nil
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func optionalString(in string) *string {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil
	}
	return &in
}
