package dataframebuilder

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

func (s *Service) Introspect(ctx context.Context, req IntrospectionRequest) (*IntrospectionResponse, error) {
	if req.Project == "" {
		return nil, fmt.Errorf("project is required")
	}
	if req.RootResourceType == "" {
		return nil, fmt.Errorf("rootResourceType is required")
	}

	principal, _ := authscope.PrincipalFromContext(ctx)
	resolvedPaths, err := s.resolveAuthResourcePaths(ctx, principal, req.Project, req.AuthResourcePaths)
	if err != nil {
		return nil, err
	}
	if err := authorizeProject(principal, req.Project, s.scopeResolver != nil); err != nil {
		return nil, err
	}

	traversals, err := s.discoverReferences(ctx, catalog.PopulatedReferenceOptions{
		ConnectionOptions: s.connOpts,
		Project:           req.Project,
		AuthResourcePaths: resolvedPaths,
		NodeType:          req.RootResourceType,
		Mode:              catalog.TraversalModeBuilder,
	})
	if err != nil {
		return nil, err
	}

	rootHints, err := s.buildResourceHints(ctx, req.Project, resolvedPaths, req.RootResourceType, traversals, req.IncludePivotOnlyFields)
	if err != nil {
		return nil, err
	}
	relatedHints, err := s.buildRelatedResourceHints(ctx, req.Project, resolvedPaths, traversals, req.IncludePivotOnlyFields)
	if err != nil {
		return nil, err
	}

	return &IntrospectionResponse{
		Project:           req.Project,
		RootResourceType:  req.RootResourceType,
		AuthResourcePaths: resolvedPaths,
		Root:              rootHints,
		RelatedResources:  relatedHints,
		Traversals:        rootHints.Traversals,
		Fields:            rootHints.Fields,
		PivotFields:       rootHints.PivotFields,
	}, nil
}

func (s *Service) buildResourceHints(ctx context.Context, project string, authResourcePaths []string, resourceType string, traversals []catalog.PopulatedReference, includePivotOnlyFields bool) (ResourceHints, error) {
	fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		ResourceType:      resourceType,
		PivotOnly:         false,
	})
	if err != nil {
		return ResourceHints{}, err
	}

	pivotFields := []catalog.PopulatedField{}
	if includePivotOnlyFields {
		pivotFields, err = s.discoverFields(ctx, catalog.PopulatedFieldOptions{
			ConnectionOptions: s.connOpts,
			Project:           project,
			AuthResourcePaths: authResourcePaths,
			ResourceType:      resourceType,
			PivotOnly:         true,
		})
		if err != nil {
			return ResourceHints{}, err
		}
	}

	return ResourceHints{
		ResourceType: resourceType,
		Fields:       discoveredFieldHints(resourceType, normalizeFieldSlice(fields)),
		PivotFields:  discoveredFieldHints(resourceType, normalizeFieldSlice(pivotFields)),
		Traversals:   cloneTraversals(traversals),
	}, nil
}

func (s *Service) buildRelatedResourceHints(ctx context.Context, project string, authResourcePaths []string, traversals []catalog.PopulatedReference, includePivotOnlyFields bool) ([]RelatedResourceHints, error) {
	if len(traversals) == 0 {
		return []RelatedResourceHints{}, nil
	}

	typeCache := map[string]ResourceHints{}
	out := make([]RelatedResourceHints, 0, len(traversals))
	for _, ref := range traversals {
		target, ok := typeCache[ref.ToType]
		if !ok {
			hints, err := s.buildResourceHints(ctx, project, authResourcePaths, ref.ToType, nil, includePivotOnlyFields)
			if err != nil {
				return nil, err
			}
			typeCache[ref.ToType] = hints
			target = hints
		}
		out = append(out, RelatedResourceHints{
			ViaLabel:  ref.Label,
			EdgeCount: ref.EdgeCount,
			Target:    target,
		})
	}
	return out, nil
}

func normalizeFieldSlice(in []catalog.PopulatedField) []catalog.PopulatedField {
	if len(in) == 0 {
		return []catalog.PopulatedField{}
	}
	for i := range in {
		if in[i].DistinctValues == nil {
			in[i].DistinctValues = []string{}
		}
		if in[i].PivotColumns == nil {
			in[i].PivotColumns = []string{}
		}
	}
	return in
}
