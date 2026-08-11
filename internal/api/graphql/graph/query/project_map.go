package queryapi

import (
	"context"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

// ProjectMap returns one deterministic, populated-only graph for the active
// generation and effective reader scope. It intentionally reads the catalogs
// in bulk so browsers never need to guess which FHIR resource types to probe.
func (s *Service) ProjectMap(ctx context.Context, req ProjectMapRequest) (*ProjectMapResponse, error) {
	if req.Project == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, req.Project, s.scopeResolver != nil); err != nil {
		return nil, classifyError(err)
	}
	generation, err := s.resolveActiveGeneration(ctx, req.Project)
	if err != nil {
		return nil, queryBackend(err)
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, req.Project, generation, req.AuthResourcePaths)
	if err != nil {
		return nil, queryBackend(err)
	}

	fieldOpts := catalog.PopulatedFieldOptions{
		Project: req.Project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
	}
	fields, err := s.discoverFields(ctx, fieldOpts)
	if err != nil {
		return nil, queryBackend(err)
	}
	pivotFields := []catalog.PopulatedField{}
	if req.IncludePivotOnlyFields {
		fieldOpts.PivotOnly = true
		pivotFields, err = s.discoverFields(ctx, fieldOpts)
		if err != nil {
			return nil, queryBackend(err)
		}
	}
	relationships, err := s.discoverReferences(ctx, catalog.PopulatedReferenceOptions{
		Project: req.Project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
		AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths),
		Mode:                          catalog.TraversalModeBuilder,
	})
	if err != nil {
		return nil, queryBackend(err)
	}

	byType := map[string]*ResourceHints{}
	resource := func(resourceType string) *ResourceHints {
		if hints := byType[resourceType]; hints != nil {
			return hints
		}
		hints := &ResourceHints{ResourceType: resourceType, Fields: []FieldHint{}, PivotFields: []FieldHint{}, Traversals: []catalog.PopulatedReference{}}
		byType[resourceType] = hints
		return hints
	}
	for _, field := range aggregatePopulatedFields(fields) {
		hints := resource(field.ResourceType)
		hints.Fields = append(hints.Fields, discoveredFieldHints(field.ResourceType, []catalog.PopulatedField{field})...)
		if field.DocCount > hints.DocumentCount {
			hints.DocumentCount = field.DocCount
		}
	}
	for _, field := range aggregatePopulatedFields(pivotFields) {
		hints := resource(field.ResourceType)
		hints.PivotFields = append(hints.PivotFields, discoveredFieldHints(field.ResourceType, []catalog.PopulatedField{field})...)
		if field.DocCount > hints.DocumentCount {
			hints.DocumentCount = field.DocCount
		}
	}
	populatedRelationships := aggregatePopulatedReferences(relationships)
	for _, relationship := range populatedRelationships {
		resource(relationship.FromType).Traversals = append(resource(relationship.FromType).Traversals, relationship)
		resource(relationship.ToType)
	}

	resources := make([]ResourceHints, 0, len(byType))
	for _, hints := range byType {
		resources = append(resources, *hints)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ResourceType < resources[j].ResourceType })
	sort.Slice(populatedRelationships, func(i, j int) bool { return referenceLess(populatedRelationships[i], populatedRelationships[j]) })
	for i := range resources {
		sort.Slice(resources[i].Fields, func(left, right int) bool {
			return resources[i].Fields[left].FieldRef < resources[i].Fields[right].FieldRef
		})
		sort.Slice(resources[i].PivotFields, func(left, right int) bool {
			return resources[i].PivotFields[left].FieldRef < resources[i].PivotFields[right].FieldRef
		})
		sort.Slice(resources[i].Traversals, func(left, right int) bool {
			return referenceLess(resources[i].Traversals[left], resources[i].Traversals[right])
		})
	}
	return &ProjectMapResponse{Project: req.Project, SourceGeneration: generation, Resources: resources, Relationships: populatedRelationships}, nil
}

// aggregatePopulatedFields collapses the one-row-per-auth-resource catalog
// representation into one public field hint. It only sees rows returned by
// the already-resolved reader scope, then sums partition counts and unions
// sampled metadata. Keeping this boundary in the query service prevents auth
// catalog identities from leaking through duplicate GraphQL fields.
func aggregatePopulatedFields(in []catalog.PopulatedField) []catalog.PopulatedField {
	type key struct{ resourceType, path string }
	aggregates := make(map[key]catalog.PopulatedField, len(in))
	distinct := make(map[key]map[string]struct{}, len(in))
	pivotColumns := make(map[key]map[string]struct{}, len(in))
	pivotSelectors := make(map[key]map[string]struct{}, len(in))
	extensionValues := make(map[key]map[string]catalog.ExtensionValueObservation, len(in))
	for _, field := range in {
		if strings.TrimSpace(field.ResourceType) == "" || strings.TrimSpace(field.Path) == "" || field.DocCount <= 0 {
			continue
		}
		k := key{resourceType: field.ResourceType, path: field.Path}
		current, ok := aggregates[k]
		if !ok {
			current = field
			current.DocCount = 0
			current.DistinctValues = nil
			current.PivotColumns = nil
			current.PivotValueSelectors = nil
			current.ExtensionValues = nil
			aggregates[k] = current
			distinct[k] = make(map[string]struct{})
			pivotColumns[k] = make(map[string]struct{})
			pivotSelectors[k] = make(map[string]struct{})
			extensionValues[k] = make(map[string]catalog.ExtensionValueObservation)
		}
		current.DocCount += field.DocCount
		current.SampleCount = maxInt(current.SampleCount, field.SampleCount)
		current.DistinctTruncated = current.DistinctTruncated || field.DistinctTruncated
		current.PivotCandidate = current.PivotCandidate || field.PivotCandidate
		current.Kind = stableMetadata(current.Kind, field.Kind)
		current.PivotKind = stableMetadata(current.PivotKind, field.PivotKind)
		current.PivotFamily = stableMetadata(current.PivotFamily, field.PivotFamily)
		current.PivotColumnSelect = stableMetadata(current.PivotColumnSelect, field.PivotColumnSelect)
		current.PivotValueSelect = stableMetadata(current.PivotValueSelect, field.PivotValueSelect)
		current.PivotItemSource = stableMetadata(current.PivotItemSource, field.PivotItemSource)
		current.PivotItemResourceType = stableMetadata(current.PivotItemResourceType, field.PivotItemResourceType)
		for _, value := range field.DistinctValues {
			distinct[k][value] = struct{}{}
		}
		for _, value := range field.PivotColumns {
			pivotColumns[k][value] = struct{}{}
		}
		for _, value := range field.PivotValueSelectors {
			pivotSelectors[k][value] = struct{}{}
		}
		for _, observation := range field.ExtensionValues {
			observationKey := observation.URL + "\x00" + strings.Join(observation.URLPath, "\x00") + "\x00" + observation.SourcePath + "\x00" + observation.ValuePath + "\x00" + observation.ValueType
			extensionValues[k][observationKey] = observation
		}
		aggregates[k] = current
	}
	out := make([]catalog.PopulatedField, 0, len(aggregates))
	for k, field := range aggregates {
		for value := range distinct[k] {
			field.DistinctValues = append(field.DistinctValues, value)
		}
		for value := range pivotColumns[k] {
			field.PivotColumns = append(field.PivotColumns, value)
		}
		for value := range pivotSelectors[k] {
			field.PivotValueSelectors = append(field.PivotValueSelectors, value)
		}
		for _, value := range extensionValues[k] {
			field.ExtensionValues = append(field.ExtensionValues, value)
		}
		sort.Strings(field.DistinctValues)
		sort.Strings(field.PivotColumns)
		sort.Strings(field.PivotValueSelectors)
		sort.Slice(field.ExtensionValues, func(i, j int) bool {
			left, right := field.ExtensionValues[i], field.ExtensionValues[j]
			return left.URL+"\x00"+strings.Join(left.URLPath, "\x00")+"\x00"+left.SourcePath+"\x00"+left.ValuePath+"\x00"+left.ValueType < right.URL+"\x00"+strings.Join(right.URLPath, "\x00")+"\x00"+right.SourcePath+"\x00"+right.ValuePath+"\x00"+right.ValueType
		})
		field.SampleCount = maxInt(field.SampleCount, len(field.DistinctValues))
		out = append(out, field)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ResourceType != out[j].ResourceType {
			return out[i].ResourceType < out[j].ResourceType
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func aggregatePopulatedReferences(in []catalog.PopulatedReference) []catalog.PopulatedReference {
	type key struct{ fromType, label, toType string }
	aggregates := make(map[key]catalog.PopulatedReference, len(in))
	for _, reference := range in {
		if reference.EdgeCount <= 0 || strings.TrimSpace(reference.FromType) == "" || strings.TrimSpace(reference.Label) == "" || strings.TrimSpace(reference.ToType) == "" {
			continue
		}
		k := key{reference.FromType, reference.Label, reference.ToType}
		current := aggregates[k]
		current.FromType, current.Label, current.ToType = reference.FromType, reference.Label, reference.ToType
		current.DatasetGeneration = reference.DatasetGeneration
		current.EdgeCount += reference.EdgeCount
		aggregates[k] = current
	}
	out := make([]catalog.PopulatedReference, 0, len(aggregates))
	for _, reference := range aggregates {
		out = append(out, reference)
	}
	sort.Slice(out, func(i, j int) bool { return referenceLess(out[i], out[j]) })
	return out
}

func referenceLess(left, right catalog.PopulatedReference) bool {
	if left.FromType != right.FromType {
		return left.FromType < right.FromType
	}
	if left.Label != right.Label {
		return left.Label < right.Label
	}
	return left.ToType < right.ToType
}

func stableMetadata(left, right string) string {
	if left == "" {
		return right
	}
	if right == "" || left <= right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
