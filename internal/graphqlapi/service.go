package graphqlapi

import (
	"context"
	"fmt"

	"arangodb-proto/internal/dataframe"
	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

type ServiceConfig struct {
	ConnectionOptions  proto.ConnectionOptions
	DiscoverReferences func(context.Context, proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error)
	DiscoverFields     func(context.Context, proto.PopulatedFieldOptions) ([]proto.PopulatedField, error)
	Dataframes         *dataframe.Service
	ScopeResolver      *writeapi.ScopeResolver
}

type Service struct {
	connOpts           proto.ConnectionOptions
	discoverReferences func(context.Context, proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error)
	discoverFields     func(context.Context, proto.PopulatedFieldOptions) ([]proto.PopulatedField, error)
	dataframes         *dataframe.Service
	scopeResolver      *writeapi.ScopeResolver
}

type IntrospectionRequest struct {
	Project                string
	RootResourceType       string
	AuthResourcePaths      []string
	IncludePivotOnlyFields bool
}

type IntrospectionResponse struct {
	Project           string
	RootResourceType  string
	AuthResourcePaths []string
	Root             ResourceHintsResponse
	RelatedResources []RelatedResourceHintsResponse
	Traversals        []proto.PopulatedReference
	Fields            []proto.PopulatedField
	PivotFields       []proto.PopulatedField
}

type ResourceHintsResponse struct {
	ResourceType string
	Fields       []proto.PopulatedField
	PivotFields  []proto.PopulatedField
	Traversals   []proto.PopulatedReference
}

type RelatedResourceHintsResponse struct {
	ViaLabel   string
	EdgeCount  int64
	Target     ResourceHintsResponse
}

func NewService(cfg ServiceConfig) *Service {
	svc := &Service{
		connOpts:      cfg.ConnectionOptions,
		scopeResolver: cfg.ScopeResolver,
	}
	if cfg.DiscoverReferences != nil {
		svc.discoverReferences = cfg.DiscoverReferences
	} else {
		svc.discoverReferences = proto.DiscoverPopulatedReferences
	}
	if cfg.DiscoverFields != nil {
		svc.discoverFields = cfg.DiscoverFields
	} else {
		svc.discoverFields = proto.DiscoverPopulatedFields
	}
	if cfg.Dataframes != nil {
		svc.dataframes = cfg.Dataframes
	} else {
		svc.dataframes = dataframe.NewService(dataframe.ServiceConfig{
			ConnectionOptions:  cfg.ConnectionOptions,
			DiscoverReferences: svc.discoverReferences,
			DiscoverFields:     svc.discoverFields,
			ScopeResolver:      cfg.ScopeResolver,
		})
	}
	return svc
}

func (s *Service) Introspect(ctx context.Context, req IntrospectionRequest) (*IntrospectionResponse, error) {
	if req.Project == "" {
		return nil, fmt.Errorf("project is required")
	}
	if req.RootResourceType == "" {
		return nil, fmt.Errorf("rootResourceType is required")
	}

	principal, _ := writeapi.PrincipalFromContext(ctx)
	resolvedPaths, err := s.resolveAuthResourcePaths(ctx, principal, req.Project, req.AuthResourcePaths)
	if err != nil {
		return nil, err
	}
	if err := authorizeProject(principal, req.Project, s.scopeResolver != nil); err != nil {
		return nil, err
	}

	traversals, err := s.discoverReferences(ctx, proto.PopulatedReferenceOptions{
		ConnectionOptions: s.connOpts,
		Project:           req.Project,
		AuthResourcePaths: resolvedPaths,
		NodeType:          req.RootResourceType,
		Mode:              proto.TraversalModeBuilder,
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
		Traversals:        normalizeTraversalSlice(rootHints.Traversals),
		Fields:            normalizeFieldSlice(rootHints.Fields),
		PivotFields:       normalizeFieldSlice(rootHints.PivotFields),
	}, nil
}

func (s *Service) buildResourceHints(ctx context.Context, project string, authResourcePaths []string, resourceType string, traversals []proto.PopulatedReference, includePivotOnlyFields bool) (ResourceHintsResponse, error) {
	fields, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		ResourceType:      resourceType,
		PivotOnly:         false,
	})
	if err != nil {
		return ResourceHintsResponse{}, err
	}
	pivotFields := []proto.PopulatedField{}
	if includePivotOnlyFields {
		pivotFields, err = s.discoverFields(ctx, proto.PopulatedFieldOptions{
			ConnectionOptions: s.connOpts,
			Project:           project,
			AuthResourcePaths: authResourcePaths,
			ResourceType:      resourceType,
			PivotOnly:         true,
		})
		if err != nil {
			return ResourceHintsResponse{}, err
		}
	}
	return ResourceHintsResponse{
		ResourceType: resourceType,
		Fields:       normalizeFieldSlice(fields),
		PivotFields:  normalizeFieldSlice(pivotFields),
		Traversals:   normalizeTraversalSlice(traversals),
	}, nil
}

func (s *Service) buildRelatedResourceHints(ctx context.Context, project string, authResourcePaths []string, traversals []proto.PopulatedReference, includePivotOnlyFields bool) ([]RelatedResourceHintsResponse, error) {
	if len(traversals) == 0 {
		return []RelatedResourceHintsResponse{}, nil
	}
	typeCache := map[string]ResourceHintsResponse{}
	out := make([]RelatedResourceHintsResponse, 0, len(traversals))
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
		out = append(out, RelatedResourceHintsResponse{
			ViaLabel:  ref.Label,
			EdgeCount: ref.EdgeCount,
			Target:    target,
		})
	}
	return out, nil
}

func normalizeTraversalSlice(in []proto.PopulatedReference) []proto.PopulatedReference {
	if len(in) == 0 {
		return []proto.PopulatedReference{}
	}
	return in
}

func normalizeFieldSlice(in []proto.PopulatedField) []proto.PopulatedField {
	if len(in) == 0 {
		return []proto.PopulatedField{}
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

func (s *Service) RunDataframe(ctx context.Context, req dataframe.RunRequest) (*dataframe.RunResult, error) {
	return s.dataframes.Run(ctx, req)
}

func authorizeProject(principal *writeapi.Principal, project string, ignorePrincipalProjects bool) error {
	if ignorePrincipalProjects {
		return nil
	}
	if principal == nil || len(principal.Projects) == 0 {
		return nil
	}
	for _, candidate := range principal.Projects {
		if candidate == project {
			return nil
		}
	}
	return fmt.Errorf("principal is not authorized for project %q", project)
}

func (s *Service) resolveAuthResourcePaths(ctx context.Context, principal *writeapi.Principal, project string, requested []string) ([]string, error) {
	if s.scopeResolver != nil {
		return s.scopeResolver.ResolveReadAuthResourcePaths(ctx, principal, project, requested)
	}
	if len(requested) == 0 {
		if principal == nil || len(principal.AuthResourcePaths) == 0 {
			return nil, nil
		}
		return append([]string(nil), principal.AuthResourcePaths...), nil
	}
	if principal == nil || len(principal.AuthResourcePaths) == 0 {
		return append([]string(nil), requested...), nil
	}
	for _, path := range requested {
		found := false
		for _, candidate := range principal.AuthResourcePaths {
			if candidate == path {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("authResourcePath %q is outside caller scope", path)
		}
	}
	return append([]string(nil), requested...), nil
}
