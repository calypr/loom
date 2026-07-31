package queryapi

// Graph mode is a deliberately small adapter over the dataframe semantic
// pipeline.  It owns the transport contract (required-by-default traversals,
// bounded lookahead pagination, and public resource sanitization); route
// validation, generation/scope resolution, and AQL lowering remain in the
// canonical compiler.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	dfruntime "github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

const (
	FhirGraphDefaultLimit = 100
	FhirGraphMaxLimit     = 10000
	FhirGraphMaxDepth     = 4
	FhirGraphMaxSteps     = 32
)

// FHIRGraphQuery is transport-neutral and intentionally mirrors only the
// explicit graph DSL. GraphQL resolvers can map generated gqlgen models into
// this type without exposing compiler or storage details.
type FHIRGraphQuery struct {
	Project           string
	AuthResourcePaths []string
	RootResourceType  string
	RootFilters       []*model.FhirFilterInput
	Traverse          []FHIRGraphTraversal
	Limit             int
}

type FHIRGraphTraversal struct {
	EdgeLabel      string
	ToResourceType string
	Alias          string
	MatchMode      model.FhirTraversalMatchMode
	Filters        []*model.FhirFilterInput
	Traverse       []FHIRGraphTraversal
}

type FHIRGraphNode struct {
	Alias        string
	ResourceType string
	ID           string
	Resource     map[string]any
}

type FHIRGraphRelationship struct {
	Alias            string
	Label            string
	FromResourceType string
	ToResourceType   string
}

type FHIRGraphPath struct {
	TerminalAlias string
	Nodes         []FHIRGraphNode
	Relationships []FHIRGraphRelationship
}

type FHIRGraphPageInfo struct{ HasMore bool }

type FHIRGraphResult struct {
	SourceGeneration string
	Paths            []FHIRGraphPath
	ReturnedCount    int
	PageInfo         FHIRGraphPageInfo
	Diagnostics      any
}

// FHIRGraphExplanation is intentionally sanitized. The AQL text, bind vars,
// collection names, and authorization predicates never cross this boundary.
type FHIRGraphExplanation struct {
	SourceGeneration string
	RootResourceType string
	TraversalCount   int
	MaxDepth         int
	Limit            int
	Live             bool
	Diagnostics      any
}

func normalizeFHIRGraphQuery(in FHIRGraphQuery) (FHIRGraphQuery, error) {
	in.Project = strings.TrimSpace(in.Project)
	in.RootResourceType = strings.TrimSpace(in.RootResourceType)
	if in.Project == "" || in.RootResourceType == "" {
		if in.Project == "" {
			return in, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
		}
		return in, dataframeerrors.NewError(dataframeerrors.CodeRootResourceTypeRequired, "")
	}
	if in.Limit == 0 {
		in.Limit = FhirGraphDefaultLimit
	}
	if in.Limit < 1 || in.Limit > FhirGraphMaxLimit {
		return in, dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "")
	}
	steps, _, err := normalizeFHIRGraphTraversals(in.Traverse, 1)
	if err != nil {
		return in, err
	}
	if steps > FhirGraphMaxSteps {
		return in, dataframeerrors.NewError(dataframeerrors.CodeInvalidTraversal, "")
	}
	return in, nil
}

func normalizeFHIRGraphTraversals(in []FHIRGraphTraversal, depth int) (int, int, error) {
	if depth > FhirGraphMaxDepth {
		return 0, depth, dataframeerrors.NewError(dataframeerrors.CodeQueryDepthExceeded, "")
	}
	total, maxDepth := 0, depth
	for i := range in {
		step := &in[i]
		step.EdgeLabel = strings.TrimSpace(step.EdgeLabel)
		step.ToResourceType = strings.TrimSpace(step.ToResourceType)
		step.Alias = strings.TrimSpace(step.Alias)
		if step.EdgeLabel == "" || step.ToResourceType == "" || step.Alias == "" {
			return 0, maxDepth, dataframeerrors.NewError(dataframeerrors.CodeInvalidTraversal, "")
		}
		if !step.MatchMode.IsValid() {
			step.MatchMode = model.FhirTraversalMatchModeRequired
		}
		childCount, childDepth, err := normalizeFHIRGraphTraversals(step.Traverse, depth+1)
		if err != nil {
			return 0, maxDepth, err
		}
		total += 1 + childCount
		if childDepth > maxDepth {
			maxDepth = childDepth
		}
	}
	return total, maxDepth, nil
}

func graphDataframeInput(in FHIRGraphQuery) model.FhirDataframeInput {
	return model.FhirDataframeInput{
		Project: in.Project, AuthResourcePaths: cloneStrings(in.AuthResourcePaths),
		RootResourceType: in.RootResourceType, RootFilters: in.RootFilters,
		Traverse: graphTraversalInputs(in.Traverse),
	}
}

func graphTraversalInputs(in []FHIRGraphTraversal) []*model.FhirTraversalStepInput {
	out := make([]*model.FhirTraversalStepInput, 0, len(in))
	for i := range in {
		step := in[i]
		mode := step.MatchMode
		if !mode.IsValid() {
			mode = model.FhirTraversalMatchModeRequired
		}
		children := graphTraversalInputs(step.Traverse)
		out = append(out, &model.FhirTraversalStepInput{
			EdgeLabel: step.EdgeLabel, ToResourceType: step.ToResourceType,
			Alias: step.Alias, MatchMode: &mode, Filters: step.Filters,
			Traverse: children,
		})
	}
	return out
}

// RunFHIRGraph compiles and executes one graph request. The compiler facade
// returns a single physical query whose RETURN operation performs the global
// path union/dedupe/sort and limit+1 lookahead.
func (s *Service) RunFHIRGraph(ctx context.Context, input FHIRGraphQuery) (*FHIRGraphResult, error) {
	normalized, err := normalizeFHIRGraphQuery(input)
	if err != nil {
		return nil, err
	}
	prepared, scope, generation, err := s.prepareRunInput(ctx, graphDataframeInput(normalized))
	if err != nil {
		return nil, err
	}
	bundle, err := RecipeBundleFromInput(prepared)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	bindings := recipe.RuntimeBindings{Project: normalized.Project, DatasetGeneration: generation,
		AuthResourcePaths: cloneStrings(scope.AuthResourcePaths), AuthScopeMode: scope.Mode,
		PreviewLimit: normalized.Limit + 1}
	bundle, err = s.resolveRecipeBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, err
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "", generation)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	compiled, err := compiler.CompileResolvedGraphQueryWithPolicy(resolved, normalized.Limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	runtimeResult, err := s.dataframes.RunCompiled(ctx, compiled)
	if err != nil {
		return nil, queryBackend(err)
	}
	paths := make([]FHIRGraphPath, 0, len(runtimeResult.Rows))
	for _, row := range runtimeResult.Rows {
		path, ok, err := decodeGraphPath(row)
		if err != nil {
			return nil, err
		}
		if ok {
			paths = append(paths, path)
		}
	}
	hasMore := len(paths) > normalized.Limit
	if hasMore {
		paths = paths[:normalized.Limit]
	}
	return &FHIRGraphResult{SourceGeneration: generation, Paths: paths, ReturnedCount: len(paths), PageInfo: FHIRGraphPageInfo{HasMore: hasMore}, Diagnostics: runtimeResult.Diagnostics}, nil
}

// ExplainFHIRGraph performs semantic/compiler validation. Live explain is an
// optional compiler operation and must only issue Arango EXPLAIN; the facade
// intentionally exposes no physical query text.
func (s *Service) ExplainFHIRGraph(ctx context.Context, input FHIRGraphQuery, live bool) (*FHIRGraphExplanation, error) {
	normalized, err := normalizeFHIRGraphQuery(input)
	if err != nil {
		return nil, err
	}
	prepared, scope, generation, err := s.prepareRunInput(ctx, graphDataframeInput(normalized))
	if err != nil {
		return nil, err
	}
	bundle, err := RecipeBundleFromInput(prepared)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	bindings := recipe.RuntimeBindings{Project: normalized.Project, DatasetGeneration: generation, AuthResourcePaths: cloneStrings(scope.AuthResourcePaths), AuthScopeMode: scope.Mode, PreviewLimit: normalized.Limit + 1}
	bundle, err = s.resolveRecipeBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, err
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "", generation)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	// The compiler facade performs semantic/physical validation without
	// executing the result query. When requested, the runtime issues only an
	// Arango EXPLAIN call; this transport contract never exposes AQL or bind
	// variables.
	compiled, err := compiler.CompileResolvedGraphQueryWithPolicy(resolved, normalized.Limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return nil, err
	}
	if live {
		// Explain only: this invokes Arango's EXPLAIN endpoint and never opens a
		// result cursor. The assessment is intentionally not returned here.
		if _, err := dfruntime.ExplainCompiledQueryAssessment(ctx, s.connOpts, compiled); err != nil {
			return nil, queryBackend(err)
		}
	}
	count, depth := graphTraversalStats(normalized.Traverse, 1)
	return &FHIRGraphExplanation{SourceGeneration: generation, RootResourceType: normalized.RootResourceType, TraversalCount: count, MaxDepth: depth, Limit: normalized.Limit, Live: live, Diagnostics: compiled.PlanDiagnostics}, nil
}

func graphTraversalStats(in []FHIRGraphTraversal, depth int) (int, int) {
	count, maxDepth := 0, depth
	for _, step := range in {
		count++
		childCount, childDepth := graphTraversalStats(step.Traverse, depth+1)
		count += childCount
		if childDepth > maxDepth {
			maxDepth = childDepth
		}
	}
	return count, maxDepth
}

func decodeGraphPath(row map[string]any) (FHIRGraphPath, bool, error) {
	value := any(row)
	for _, key := range []string{"path", "paths", "graph_path", "graphPath"} {
		if candidate, ok := row[key]; ok {
			value = candidate
			break
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return FHIRGraphPath{}, false, dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
	}
	var raw struct {
		TerminalAlias string            `json:"terminalAlias"`
		Nodes         []json.RawMessage `json:"nodes"`
		Relationships []json.RawMessage `json:"relationships"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return FHIRGraphPath{}, false, dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
	}
	if raw.Nodes == nil {
		return FHIRGraphPath{}, false, nil
	}
	path := FHIRGraphPath{TerminalAlias: raw.TerminalAlias, Nodes: make([]FHIRGraphNode, 0, len(raw.Nodes)), Relationships: make([]FHIRGraphRelationship, 0, len(raw.Relationships))}
	for _, encoded := range raw.Nodes {
		node, err := decodeGraphNode(encoded)
		if err != nil {
			return FHIRGraphPath{}, false, err
		}
		path.Nodes = append(path.Nodes, node)
	}
	for _, encoded := range raw.Relationships {
		var relationship FHIRGraphRelationship
		if err := json.Unmarshal(encoded, &relationship); err != nil {
			return FHIRGraphPath{}, false, dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
		}
		path.Relationships = append(path.Relationships, relationship)
	}
	if path.TerminalAlias == "" && len(path.Nodes) > 0 {
		path.TerminalAlias = path.Nodes[len(path.Nodes)-1].Alias
	}
	return path, true, nil
}

func decodeGraphNode(encoded json.RawMessage) (FHIRGraphNode, error) {
	var raw struct {
		Alias        string          `json:"alias"`
		ResourceType string          `json:"resourceType"`
		ID           string          `json:"id"`
		Resource     json.RawMessage `json:"resource"`
		Payload      json.RawMessage `json:"payload"`
		Key          string          `json:"key"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return FHIRGraphNode{}, dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
	}
	resourceBytes := raw.Resource
	if len(resourceBytes) == 0 || string(resourceBytes) == "null" {
		resourceBytes = raw.Payload
	}
	resource := map[string]any{}
	if len(resourceBytes) != 0 && string(resourceBytes) != "null" {
		if err := json.Unmarshal(resourceBytes, &resource); err != nil {
			return FHIRGraphNode{}, dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
		}
	}
	if raw.ID == "" {
		raw.ID = raw.Key
	}
	if raw.ID == "" {
		if value, ok := resource["id"].(string); ok {
			raw.ID = value
		}
	}
	if raw.ID == "" {
		return FHIRGraphNode{}, dataframeerrors.NewError(dataframeerrors.CodeResourceDecodeFailed, "")
	}
	delete(resource, "_key")
	delete(resource, "_id")
	delete(resource, "_rev")
	delete(resource, "_from")
	delete(resource, "_to")
	delete(resource, "label")
	delete(resource, "from_type")
	delete(resource, "to_type")
	delete(resource, "logical_key")
	delete(resource, "project")
	delete(resource, "dataset_generation")
	delete(resource, "generation")
	delete(resource, "auth_resource_path")
	if raw.ResourceType == "" {
		if value, ok := resource["resourceType"].(string); ok {
			raw.ResourceType = value
		}
	}
	if raw.ResourceType == "" {
		return FHIRGraphNode{}, dataframeerrors.NewError(dataframeerrors.CodeResourceDecodeFailed, "")
	}
	return FHIRGraphNode{Alias: raw.Alias, ResourceType: raw.ResourceType, ID: raw.ID, Resource: resource}, nil
}
