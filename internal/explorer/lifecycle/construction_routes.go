package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type ConstructionChoiceSearchSource struct {
	Kind         capability.ConstructionChoiceSourceKind `json:"kind"`
	CandidateID  string                                  `json:"candidateId,omitempty"`
	ContextToken string                                  `json:"contextToken,omitempty"`
	BuildID      string                                  `json:"buildId,omitempty"`
	ConceptID    string                                  `json:"conceptId,omitempty"`
	BindingID    string                                  `json:"bindingId,omitempty"`
}

type ConstructionChoiceSearchRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	OutputID      string
	OccurrenceID  string
	Source        ConstructionChoiceSearchSource
	Limit         int
	Cursor        string
}

type ConstructionChoiceSearchResponse struct {
	SnapshotToken string                          `json:"snapshotToken"`
	OutputID      string                          `json:"outputId"`
	Complete      bool                            `json:"complete"`
	Truncated     bool                            `json:"truncated"`
	NextCursor    string                          `json:"nextCursor,omitempty"`
	Choices       []capability.ConstructionChoice `json:"choices"`
}

type ResolvedPopulationRouteChoice struct {
	Route       []capability.ConstructionRouteStep
	SelectionID string
}

func resolveCapabilityRouteEdge(snapshot capability.Snapshot, parentNodeID string, parent, child authoringv2.RouteNode) (capability.Edge, error) {
	valid := func(edge capability.Edge) bool {
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		return edge.ID != "" && edge.BlockedReason == "" && edge.FromNodeID == parentNodeID && edge.Label == child.Relationship &&
			fromOK && toOK && from.ResourceType == parent.ResourceType && to.ResourceType == child.ResourceType &&
			(edge.SourceResourceType == "" || edge.SourceResourceType == parent.ResourceType) && (edge.TargetResourceType == "" || edge.TargetResourceType == child.ResourceType) &&
			(direction == "" || direction == "INBOUND" || direction == "OUTBOUND")
	}
	if child.CatalogEdgeID != "" {
		edge, found := snapshot.Edge(child.CatalogEdgeID)
		if !found || !valid(edge) {
			return capability.Edge{}, fmt.Errorf("catalog edge %q no longer identifies the saved route step", child.CatalogEdgeID)
		}
		return edge, nil
	}
	var match capability.Edge
	for _, edge := range snapshot.Edges {
		if !valid(edge) {
			continue
		}
		if match.ID != "" {
			return capability.Edge{}, fmt.Errorf("saved route step resolves to multiple capability edges")
		}
		match = edge
	}
	if match.ID == "" {
		return capability.Edge{}, fmt.Errorf("saved route step no longer resolves to a capability edge")
	}
	return match, nil
}

func reauthorizeConstructionRoute(snapshot capability.Snapshot, rootResourceType, targetNodeID string, route []capability.ConstructionRouteStep) ([]capability.ConstructionRouteStep, error) {
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType {
			if root.ID != "" {
				return nil, fmt.Errorf("row root is ambiguous")
			}
			root = node
		}
	}
	if root.ID == "" {
		return nil, fmt.Errorf("row root is unavailable")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(route) > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("route exceeds the current policy")
	}
	current := root
	seenEdges := make(map[string]bool, len(route))
	resolved := make([]capability.ConstructionRouteStep, 0, len(route))
	for index, step := range route {
		if strings.TrimSpace(step.EdgeID) == "" || step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED" {
			return nil, fmt.Errorf("route step %d is incomplete", index)
		}
		edge, found := snapshot.Edge(step.EdgeID)
		direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
		if !found || edge.BlockedReason != "" || edge.FromNodeID != current.ID ||
			edge.Label != step.Relationship || direction != strings.ToUpper(strings.TrimSpace(step.StorageDirection)) ||
			(edge.SourceResourceType != "" && edge.SourceResourceType != step.FromResourceType) || (edge.TargetResourceType != "" && edge.TargetResourceType != step.ToResourceType) ||
			edge.FromNodeID != step.FromNodeID || edge.ToNodeID != step.ToNodeID {
			return nil, fmt.Errorf("route step %d no longer identifies the authorized capability edge", index)
		}
		if current.ResourceType != step.FromResourceType {
			return nil, fmt.Errorf("route step %d does not extend its parent", index)
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && seenEdges[edge.ID] {
			return nil, fmt.Errorf("route repeats an edge forbidden by current policy")
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return nil, fmt.Errorf("route contains a self-loop forbidden by current policy")
		}
		if direction != "" && direction != "INBOUND" && direction != "OUTBOUND" {
			return nil, fmt.Errorf("route has an unsupported storage direction")
		}
		to, found := snapshot.Node(edge.ToNodeID)
		if !found || to.ResourceType != step.ToResourceType {
			return nil, fmt.Errorf("route target is unavailable")
		}
		seenEdges[edge.ID] = true
		resolved = append(resolved, step)
		current = to
	}
	if current.ID != targetNodeID {
		return nil, fmt.Errorf("route does not end at the exact source node")
	}
	return resolved, nil
}

func proveConstructionCandidate(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	compilerRoute := make([]compilerprobe.Traversal, 0, len(route))
	for index, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		traversal := compilerprobe.Traversal{
			FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship,
			ToResourceType: step.ToResourceType, MatchMode: matchMode,
		}
		provenEdge, err := compilerprobe.ProbeTraversal(ctx, compilerprobe.TraversalRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal,
		})
		if err != nil || provenEdge.Traversal == nil || string(provenEdge.Traversal.StorageDirection) != step.StorageDirection {
			return capability.Candidate{}, fmt.Errorf("route step %d compiler proof changed: %w", index, err)
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	proof, err := compilerprobe.ProbeCandidate(ctx, compilerprobe.CandidateRequest{
		Scope: constructionCompilerScope(authorized), RootResourceType: rootResourceType,
		ResourceType: candidate.ResourceType, FieldRef: candidate.ResourceType + "." + candidate.FieldPath,
		Selector: candidate.FieldPath, Route: compilerRoute,
	})
	if err != nil || proof.Candidate == nil {
		if err == nil {
			err = fmt.Errorf("compiler returned no candidate proof")
		}
		return capability.Candidate{}, err
	}
	updated := candidate
	updated.LogicalType = string(proof.Candidate.Primitive)
	updated.Cardinality = string(proof.Candidate.Cardinality)
	updated.ProjectionModes = make([]capability.ProjectionMode, 0, len(proof.Candidate.ProjectionModes))
	for _, mode := range proof.Candidate.ProjectionModes {
		updated.ProjectionModes = append(updated.ProjectionModes, capability.ProjectionMode(strings.ToUpper(string(mode))))
	}
	return updated, nil
}

func constructionCompilerScope(authorized AuthorizedCapability) compilerprobe.Scope {
	return compilerprobe.Scope{
		Project: authorized.Snapshot.Identity.Project, DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode,
	}
}

func constructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	if choice.ChoiceID == submittedID {
		return true
	}
	return identity.Version == "construction-choice/v1" && len(identity.Route) == 0 && reflect.DeepEqual(choice.Source, identity.Source)
}

func constructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot authoringv2.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	path, found := routePathToOccurrence(authoredRoot, occurrenceID)
	if !found || len(path) == 0 || path[0].OccurrenceID != authoringv2.RootOccurrenceID || path[0].ResourceType != rootResourceType {
		return nil, fmt.Errorf("occurrence is not in this output's saved route")
	}
	var root capability.Node
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		if root.ID != "" {
			return nil, fmt.Errorf("authorized row root is ambiguous")
		}
		root = node
	}
	if root.ID == "" {
		return nil, fmt.Errorf("authorized row root is unavailable")
	}
	if snapshot.Policy.Route.MaxHops > 0 && len(path)-1 > snapshot.Policy.Route.MaxHops {
		return nil, fmt.Errorf("saved route exceeds the current route policy")
	}
	if len(path) == 1 {
		if root.ID != targetNodeID {
			return nil, fmt.Errorf("root occurrence does not match the candidate terminal")
		}
		return []capability.ConstructionRouteStep{}, nil
	}

	var matches [][]capability.ConstructionRouteStep
	var visit func(capability.Node, int, []capability.ConstructionRouteStep, map[string]bool) error
	visit = func(current capability.Node, pathIndex int, steps []capability.ConstructionRouteStep, usedEdges map[string]bool) error {
		if len(matches) > 1 {
			return nil
		}
		if pathIndex == len(path) {
			if current.ID == targetNodeID {
				matches = append(matches, cloneConstructionRoute(steps))
			}
			return nil
		}
		parent := path[pathIndex-1]
		authored := path[pathIndex]
		matchMode := string(authored.MatchMode.Normalized())
		edge, err := resolveCapabilityRouteEdge(snapshot, current.ID, parent, authored)
		if err != nil {
			return err
		}
		if !snapshot.Policy.Route.AllowsRepeatedEdges && usedEdges[edge.ID] {
			return fmt.Errorf("saved route repeats an edge forbidden by policy")
		}
		if !snapshot.Policy.Route.AllowsSelfLoops && edge.FromNodeID == edge.ToNodeID {
			return fmt.Errorf("saved route contains a self-loop forbidden by policy")
		}
		to, _ := snapshot.Node(edge.ToNodeID)
		next := append(cloneConstructionRoute(steps), capability.ConstructionRouteStep{
			EdgeID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
			FromResourceType: edge.SourceResourceType, ToResourceType: edge.TargetResourceType,
			Relationship: edge.Label, StorageDirection: strings.ToUpper(strings.TrimSpace(edge.StorageDirection)), MatchMode: matchMode,
		})
		nextUsed := make(map[string]bool, len(usedEdges)+1)
		for id, used := range usedEdges {
			nextUsed[id] = used
		}
		nextUsed[edge.ID] = true
		if err := visit(to, pathIndex+1, next, nextUsed); err != nil {
			return err
		}
		return nil
	}
	if err := visit(root, 1, nil, map[string]bool{}); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("saved route does not resolve to the exact candidate terminal")
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("saved route has multiple authorized capability edge resolutions")
	}
	return reauthorizeConstructionRoute(snapshot, rootResourceType, targetNodeID, matches[0])
}

func routePathToOccurrence(root authoringv2.RouteNode, occurrenceID string) ([]authoringv2.RouteNode, bool) {
	if root.OccurrenceID == occurrenceID {
		return []authoringv2.RouteNode{root}, true
	}
	for _, child := range root.Children {
		path, found := routePathToOccurrence(child, occurrenceID)
		if !found {
			continue
		}
		return append([]authoringv2.RouteNode{root}, path...), true
	}
	return nil, false
}

// SearchConstructionChoices resolves one exact field or semantic identity for
// the current table output, enumerates its distinct directed routes, and only
// returns choices that compile with that full route.
func (s *Service) SearchConstructionChoices(ctx context.Context, request ConstructionChoiceSearchRequest) (ConstructionChoiceSearchResponse, error) {
	result := ConstructionChoiceSearchResponse{SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Choices: []capability.ConstructionChoice{}}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" || len(request.Cursor) > 4096 {
		return result, malformed("construction-choices", "project, explorerId, snapshotToken, and outputId are required", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return result, unavailable("construction-choices", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	identityKey, err := constructionSearchSourceKey(request.Source)
	if err != nil {
		return result, err
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return result, conflict("construction-choices", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching routes", nil, err)
	}
	snapshot := authorized.Snapshot
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("construction-choices", "STALE_CATALOG_SNAPSHOT", "reload the catalog before searching routes", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("construction-choices", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, malformed("construction-choices", "outputId does not identify a valid row-rooted table", nil)
	}
	var candidate capability.Candidate
	var semanticEntry catalog.SemanticInventoryEntry
	semanticContext := ""
	buildID := ""
	if request.Source.Kind == capability.ConstructionChoiceSourceSemantic {
		semanticEntry, buildID, err = s.resolveConstructionSearchSemanticEntry(ctx, authorized, request)
		if err != nil {
			return result, err
		}
		semanticContext, err = semanticInventoryContextToken(snapshot, request.ExplorerID, document.RootResourceType, buildID)
		if err != nil || request.Source.ContextToken != semanticContext {
			return result, conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "reload the semantic choice for the current table root and inventory build", nil, err)
		}
		var found bool
		candidate, found = semanticConstructionCandidate(snapshot, semanticEntry.Observation)
		if !found || semanticEntry.Observation.Source.Type != candidate.ResourceType {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "semantic binding and candidate resource do not match", nil)
		}
		plan := authoringv2.ResolveSemanticSelectionPlan(semanticEntry.Observation)
		if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "semantic source has no supported typed compiler binding", nil)
		}
	} else {
		var found bool
		candidate, found = uniqueCapabilityCandidate(snapshot, request.Source.CandidateID)
		if !found {
			return result, unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the exact field candidate is unavailable in this authorized snapshot", nil)
		}
	}
	var routes [][]capability.ConstructionRouteStep
	if occurrenceID := strings.TrimSpace(request.OccurrenceID); occurrenceID != "" {
		if strings.TrimSpace(request.Cursor) != "" {
			return result, malformed("construction-choices", "cursor cannot be combined with occurrenceId", nil)
		}
		route, routeErr := constructionRouteForOccurrence(snapshot, document.RootResourceType, candidate.NodeID, document.Route, occurrenceID)
		if routeErr != nil {
			return result, unprocessable("construction-choices", "INVALID_OCCURRENCE_ROUTE", "the selected occurrence does not resolve to this source through one authorized route", routeErr)
		}
		routes = [][]capability.ConstructionRouteStep{route}
		result.Complete = true
	} else {
		page, planErr := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
			Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: candidate.NodeID,
			SourceKey: identityKey, Cursor: request.Cursor, Limit: request.Limit,
		})
		if planErr != nil {
			return result, conflict("construction-choices", "STALE_OR_INVALID_ROUTE_CURSOR", "restart route search for this exact source", nil, planErr)
		}
		result.Complete, result.Truncated, result.NextCursor = page.Complete, page.Truncated, page.NextCursor
		routes = page.Routes
	}
	for _, route := range routes {
		resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, document.RootResourceType, candidate.NodeID, route)
		if routeErr != nil {
			if strings.TrimSpace(request.OccurrenceID) != "" {
				return result, unprocessable("construction-choices", "INVALID_OCCURRENCE_ROUTE", "the selected occurrence route is no longer authorized", routeErr)
			}
			continue
		}
		provenCandidate := candidate
		if len(resolvedRoute) > 0 {
			provenCandidate, err = proveConstructionCandidate(ctx, authorized, document.RootResourceType, candidate, resolvedRoute)
			if err != nil {
				if strings.TrimSpace(request.OccurrenceID) != "" {
					return result, unprocessable("construction-choices", "UNPROVEN_OCCURRENCE_ROUTE", "the complete selected route and source do not compile", err)
				}
				continue
			}
		}
		var choice capability.ConstructionChoice
		if request.Source.Kind == capability.ConstructionChoiceSourceField {
			choice, err = capability.NewFieldConstructionChoiceForRoute(snapshot.Token, resolvedRoute, provenCandidate)
		} else {
			ownerRecords := proveOwnerRecordsForRoute(ctx, authorized, document.RootResourceType, semanticEntry.Observation, resolvedRoute)
			choice, err = semanticInventoryConstructionChoiceForRoute(snapshot, semanticContext, buildID, resolvedRoute, semanticEntry, provenCandidate, ownerRecords)
		}
		if err != nil {
			if strings.TrimSpace(request.OccurrenceID) != "" {
				return result, unprocessable("construction-choices", "INVALID_OCCURRENCE_ROUTE", "the selected source could not be bound to its exact route", err)
			}
			continue
		}
		result.Choices = append(result.Choices, choice)
	}
	return result, nil
}

func constructionSearchSourceKey(source ConstructionChoiceSearchSource) (string, error) {
	switch source.Kind {
	case capability.ConstructionChoiceSourceField:
		if strings.TrimSpace(source.CandidateID) == "" || source.ContextToken != "" || source.BuildID != "" || source.ConceptID != "" || source.BindingID != "" {
			return "", malformed("construction-choices", "FIELD source requires only candidateId", nil)
		}
		return "field:" + source.CandidateID, nil
	case capability.ConstructionChoiceSourceSemantic:
		if strings.TrimSpace(source.ContextToken) == "" || strings.TrimSpace(source.BuildID) == "" || strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" || source.CandidateID != "" {
			return "", malformed("construction-choices", "SEMANTIC source requires contextToken, buildId, conceptId, and bindingId", nil)
		}
		return "semantic:" + source.ContextToken + "\x00" + source.BuildID + "\x00" + source.BindingID + "\x00" + source.ConceptID, nil
	default:
		return "", malformed("construction-choices", "source kind must be FIELD or SEMANTIC", nil)
	}
}

func (s *Service) currentWorkspace(ctx context.Context, project, explorerID string) (authoringv2.Workspace, error) {
	project = projectid.Canonical(project)
	owner, err := s.store.Get(ctx, project, explorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return authoringv2.Workspace{}, notFound("construction-choices", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return authoringv2.Workspace{}, internal("construction-choices", "EXPLORER_READ_FAILED", "the current Explorer workspace could not be loaded", err)
	}
	if len(owner.DraftConfig) != 0 {
		workspace, decodeErr := authoringv2.DecodeWorkspace(owner.DraftConfig)
		if decodeErr != nil {
			return authoringv2.Workspace{}, conflict("construction-choices", "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	if owner.ActiveRevisionID != "" {
		active, activeErr := s.store.ActiveRevision(ctx, project, explorerID)
		if activeErr != nil {
			return authoringv2.Workspace{}, conflict("construction-choices", "AUTHORING_STATE_MISSING", "the active Explorer workspace cannot be loaded", nil, activeErr)
		}
		workspace, decodeErr := authoringv2.DecodeWorkspace(active.AuthoringBundle)
		if decodeErr != nil {
			return authoringv2.Workspace{}, conflict("construction-choices", "AUTHORING_STATE_MISSING", "the active Explorer workspace is invalid", nil, decodeErr)
		}
		return workspace, nil
	}
	return authoringv2.Workspace{}, conflict("construction-choices", "AUTHORING_STATE_MISSING", "the Explorer has no V2 authoring workspace", nil, nil)
}

func (s *Service) resolveConstructionSearchSemanticEntry(ctx context.Context, authorized AuthorizedCapability, request ConstructionChoiceSearchRequest) (catalog.SemanticInventoryEntry, string, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return catalog.SemanticInventoryEntry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References: []catalog.SemanticInventoryReference{{ConceptID: request.Source.ConceptID, BindingID: request.Source.BindingID}},
	})
	if err != nil {
		return catalog.SemanticInventoryEntry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "the selected semantic source could not be resolved", err)
	}
	expectedBuild := catalog.SemanticInventoryBuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete || resolved.Build.BuildID != expectedBuild || request.Source.BuildID != expectedBuild {
		return catalog.SemanticInventoryEntry{}, "", conflict("construction-choices", "STALE_SEMANTIC_CONTEXT", "the semantic source belongs to a different or incomplete inventory build", nil, nil)
	}
	var match catalog.SemanticInventoryEntry
	for _, entry := range resolved.Entries {
		if entry.ConceptID != request.Source.ConceptID || entry.BindingID != request.Source.BindingID || match.ConceptID != "" {
			return catalog.SemanticInventoryEntry{}, "", unavailable("construction-choices", "CATALOG_UNAVAILABLE", "semantic inventory returned an unrelated or duplicate source", nil)
		}
		match = entry
	}
	if match.ConceptID == "" {
		return catalog.SemanticInventoryEntry{}, "", unprocessable("construction-choices", "INVALID_CONSTRUCTION_SOURCE", "the exact semantic source is unavailable in this authorized inventory", nil)
	}
	return match, expectedBuild, nil
}
