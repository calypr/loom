package graphqlapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/graphqlapi/model"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	"github.com/calypr/loom/internal/dataframe/runtime"
)

func (r *queryResolver) resolveFHIRGraph(ctx context.Context, input model.FhirGraphQueryInput) (*model.FhirGraphQueryResult, error) {
	result, err := r.query.RunFHIRGraph(ctx, toFHIRGraphQuery(input))
	if err != nil {
		return nil, err
	}
	paths := make([]*model.FhirGraphPath, 0, len(result.Paths))
	for _, path := range result.Paths {
		mapped := &model.FhirGraphPath{TerminalAlias: path.TerminalAlias, Nodes: make([]*model.FhirGraphNode, 0, len(path.Nodes)), Relationships: make([]*model.FhirGraphRelationship, 0, len(path.Relationships))}
		for _, node := range path.Nodes {
			resource, err := json.Marshal(node.Resource)
			if err != nil {
				return nil, fmt.Errorf("encode graph resource: %w", err)
			}
			mapped.Nodes = append(mapped.Nodes, &model.FhirGraphNode{Alias: node.Alias, ResourceType: node.ResourceType, ID: node.ID, Resource: resource})
		}
		for _, rel := range path.Relationships {
			mapped.Relationships = append(mapped.Relationships, &model.FhirGraphRelationship{Alias: rel.Alias, Label: rel.Label, FromResourceType: rel.FromResourceType, ToResourceType: rel.ToResourceType})
		}
		paths = append(paths, mapped)
	}
	diagnostics := (*model.DataframeQueryDiagnostics)(nil)
	if value, ok := result.Diagnostics.(runtime.QueryDiagnostics); ok {
		diagnostics = dataframeDiagnostics(value)
	}
	return &model.FhirGraphQueryResult{SourceGeneration: result.SourceGeneration, Paths: paths, ReturnedCount: result.ReturnedCount, PageInfo: &model.FhirGraphPageInfo{HasMore: result.PageInfo.HasMore}, Diagnostics: diagnostics}, nil
}

func (r *queryResolver) resolveExplainFHIRGraph(ctx context.Context, input model.FhirGraphQueryInput, live *bool) (*model.FhirGraphQueryExplanation, error) {
	requestedLive := live != nil && *live
	result, err := r.query.ExplainFHIRGraph(ctx, toFHIRGraphQuery(input), requestedLive)
	if err != nil {
		return nil, err
	}
	return &model.FhirGraphQueryExplanation{SourceGeneration: result.SourceGeneration, RootResourceType: result.RootResourceType, TraversalCount: result.TraversalCount, MaxDepth: result.MaxDepth, Limit: result.Limit, Live: result.Live}, nil
}

func toFHIRGraphQuery(input model.FhirGraphQueryInput) queryapi.FHIRGraphQuery {
	return queryapi.FHIRGraphQuery{Project: input.Project, AuthResourcePaths: append([]string(nil), input.AuthResourcePaths...), RootResourceType: input.RootResourceType, RootFilters: input.RootFilters, Traverse: toFHIRGraphTraversals(input.Traverse), Limit: input.Limit}
}

func toFHIRGraphTraversals(in []*model.FhirGraphTraversalInput) []queryapi.FHIRGraphTraversal {
	out := make([]queryapi.FHIRGraphTraversal, 0, len(in))
	for _, step := range in {
		if step == nil {
			continue
		}
		out = append(out, queryapi.FHIRGraphTraversal{EdgeLabel: step.EdgeLabel, ToResourceType: step.ToResourceType, Alias: step.Alias, MatchMode: step.MatchMode, Filters: step.Filters, Traverse: toFHIRGraphTraversals(step.Traverse)})
	}
	return out
}
