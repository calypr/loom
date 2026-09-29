package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

const frameSourceDefaultLimit = 20

type BrowseFrameSourceOptionsRequest struct {
	Project       string `json:"-"`
	ExplorerID    string `json:"-"`
	SnapshotToken string `json:"snapshotToken"`
	OutputID      string `json:"outputId"`
	ResourceType  string `json:"resourceType,omitempty"`
	Query         string `json:"query,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type FrameSourceForm struct {
	Form       capability.ConstructionChoiceForm `json:"form"`
	ZeroPolicy authoringv2.FrameZeroPolicy       `json:"zeroPolicy"`
	ManyPolicy authoringv2.FrameManyPolicy       `json:"manyPolicy"`
	Decision   string                            `json:"decision"`
}

type FrameSourceOption struct {
	ChoiceID            string                             `json:"choiceId"`
	Title               string                             `json:"title"`
	Description         string                             `json:"description"`
	ResourceType        string                             `json:"resourceType"`
	SourcePath          string                             `json:"sourcePath"`
	SourceCanonical     string                             `json:"sourceCanonical,omitempty"`
	SourceProfile       string                             `json:"sourceProfile,omitempty"`
	BindingID           string                             `json:"bindingId"`
	OwningScope         string                             `json:"owningScope"`
	KeyPath             string                             `json:"keyPath"`
	ValuePath           string                             `json:"valuePath"`
	LogicalType         string                             `json:"logicalType"`
	ExampleConcept      string                             `json:"exampleConcept"`
	ObservedOccurrences int64                              `json:"observedOccurrences"`
	Route               []capability.ConstructionRouteStep `json:"route"`
	Forms               []FrameSourceForm                  `json:"forms"`
	DefaultForm         capability.ConstructionChoiceForm  `json:"defaultForm"`
}

type BrowseFrameSourceOptionsResponse struct {
	SnapshotToken string              `json:"snapshotToken"`
	OutputID      string              `json:"outputId"`
	Complete      bool                `json:"complete"`
	Truncated     bool                `json:"truncated"`
	NextCursor    string              `json:"nextCursor,omitempty"`
	Sources       []FrameSourceOption `json:"sources"`
}

type frameSourceCursor struct {
	Version     int    `json:"version"`
	Context     string `json:"context"`
	Page        string `json:"page,omitempty"`
	FamilyIndex int    `json:"familyIndex"`
	Phase       int    `json:"phase"`
	Route       string `json:"route,omitempty"`
}

type frameSourceFamilyCandidate struct {
	Family    capability.SemanticFrameFamily
	Entry     catalog.SemanticInventoryEntry
	Candidate capability.Candidate
}

func (s *Service) BrowseFrameSourceOptions(ctx context.Context, request BrowseFrameSourceOptionsRequest) (BrowseFrameSourceOptionsResponse, error) {
	result := BrowseFrameSourceOptionsResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Sources: []FrameSourceOption{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		len(request.Query) > 256 || len(request.Cursor) > 4096 || request.Limit < 0 || request.Limit > capability.ConstructionRouteMaxLimit {
		return result, malformed("frame-source-options", "project, explorerId, snapshotToken, outputId, and a page limit of at most 50 are required", nil)
	}
	if s.config.SemanticInventory == nil || s.config.Capability.ForCompilation == nil {
		return result, unavailable("frame-source-options", "CATALOG_UNAVAILABLE", "authorized semantic inventory is not configured", nil)
	}
	if request.Limit == 0 {
		request.Limit = frameSourceDefaultLimit
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, conflict("frame-source-options", "STALE_CATALOG_SNAPSHOT", "reload the catalog before choosing a frame source", nil, err)
	}
	if err != nil {
		return result, err
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("frame-source-options", "STALE_CATALOG_SNAPSHOT", "reload the catalog before choosing a frame source", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("frame-source-options", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := s.currentWorkspace(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, malformed("frame-source-options", "outputId does not identify a valid row-rooted table", nil)
	}
	rootAllowed, resourceAllowed := false, request.ResourceType == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == document.RootResourceType && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == request.ResourceType
	}
	if !rootAllowed || !resourceAllowed {
		return result, malformed("frame-source-options", "the row root or resource filter is not available in this snapshot", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	contextKey, err := frameSourceContext(snapshot, request.ExplorerID, request.OutputID, document.RootResourceType, request.ResourceType, request.Query)
	if err != nil {
		return result, err
	}
	state := frameSourceCursor{Version: 2, Context: contextKey}
	if request.Cursor != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(request.Cursor)
		if decodeErr != nil || json.Unmarshal(raw, &state) != nil || state.Version != 2 || state.Context != contextKey || state.FamilyIndex < 0 || state.Phase < 0 || state.Phase > 1 || state.Phase == 0 && state.Route != "" {
			return result, conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, decodeErr)
		}
	}
	result.Sources = make([]FrameSourceOption, 0, request.Limit)
	truncated := false
	for len(result.Sources) < request.Limit {
		page, pageErr := s.config.SemanticInventory(ctx, catalog.SemanticInventoryPageOptions{
			Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
			AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
			ResourceType: request.ResourceType, Query: request.Query, Cursor: state.Page, Limit: catalog.SemanticInventoryPageLimit,
		})
		if errors.Is(pageErr, catalog.ErrSemanticInventoryCursorMismatch) {
			return BrowseFrameSourceOptionsResponse{}, conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, pageErr)
		}
		if pageErr != nil {
			return BrowseFrameSourceOptionsResponse{}, pageErr
		}
		contextToken, tokenErr := semanticInventoryContextToken(snapshot, request.ExplorerID, document.RootResourceType, page.Build.BuildID)
		if tokenErr != nil {
			return BrowseFrameSourceOptionsResponse{}, tokenErr
		}
		if page.Build.BuildID != catalog.SemanticInventoryBuildID(projectid.Legacy(snapshot.Identity.Project), snapshot.Identity.Generation) || page.State != catalog.SemanticInventoryComplete {
			return BrowseFrameSourceOptionsResponse{}, conflict("frame-source-options", "SEMANTIC_INVENTORY_UNAVAILABLE", "the current generation does not have a complete semantic inventory", nil, nil)
		}
		families := frameSourceFamilies(snapshot, page.Entries)
		if state.FamilyIndex > len(families) || state.Phase == 1 && state.FamilyIndex == len(families) && state.Route != "" {
			return BrowseFrameSourceOptionsResponse{}, conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, nil)
		}
		if state.Phase == 0 {
			if state.FamilyIndex >= len(families) {
				state.Phase, state.FamilyIndex = 1, 0
				continue
			}
			family := families[state.FamilyIndex]
			routes, routeErr := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
				Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: family.Candidate.NodeID,
				SourceKey: frameSourceRouteKey(request.OutputID, family.Family, family.Candidate.ID), Limit: 1,
			})
			if routeErr != nil {
				return BrowseFrameSourceOptionsResponse{}, conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, routeErr)
			}
			truncated = truncated || routes.Truncated && routes.NextCursor == ""
			if len(routes.Routes) == 1 {
				option, found := s.frameSourceOptionForRoute(ctx, authorized, snapshot, document.RootResourceType, contextToken, page.Build.BuildID, family, routes.Routes[0])
				if found {
					result.Sources = append(result.Sources, option)
				}
			}
			state.FamilyIndex++
			continue
		}
		if state.FamilyIndex >= len(families) {
			if page.NextCursor == "" {
				result.Complete = true
				break
			}
			state = frameSourceCursor{Version: 2, Context: contextKey, Page: page.NextCursor}
			continue
		}
		family := families[state.FamilyIndex]
		if state.Route == "" {
			firstRoute, routeErr := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
				Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: family.Candidate.NodeID,
				SourceKey: frameSourceRouteKey(request.OutputID, family.Family, family.Candidate.ID), Limit: 1,
			})
			if routeErr != nil {
				return BrowseFrameSourceOptionsResponse{}, conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, routeErr)
			}
			if firstRoute.NextCursor == "" {
				state.FamilyIndex++
				continue
			}
			state.Route = firstRoute.NextCursor
		}
		pageRoutes, routeErr := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
			Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: family.Candidate.NodeID,
			SourceKey: frameSourceRouteKey(request.OutputID, family.Family, family.Candidate.ID), Cursor: state.Route,
			Limit: request.Limit - len(result.Sources),
		})
		if routeErr != nil {
			return BrowseFrameSourceOptionsResponse{}, conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, routeErr)
		}
		truncated = truncated || pageRoutes.Truncated && pageRoutes.NextCursor == ""
		for _, route := range pageRoutes.Routes {
			option, found := s.frameSourceOptionForRoute(ctx, authorized, snapshot, document.RootResourceType, contextToken, page.Build.BuildID, family, route)
			if found {
				result.Sources = append(result.Sources, option)
			}
		}
		state.Route = pageRoutes.NextCursor
		if state.Route == "" {
			state.FamilyIndex++
		}
	}
	result.Truncated = truncated
	if !result.Complete {
		raw, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			return BrowseFrameSourceOptionsResponse{}, marshalErr
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}

func (s *Service) frameSourceOptionForRoute(
	ctx context.Context,
	authorized AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType, contextToken, buildID string,
	family frameSourceFamilyCandidate,
	route []capability.ConstructionRouteStep,
) (FrameSourceOption, bool) {
	resolvedRoute, err := reauthorizeConstructionRoute(snapshot, rootResourceType, family.Candidate.NodeID, route)
	if err != nil {
		return FrameSourceOption{}, false
	}
	provenCandidate, err := proveConstructionCandidate(ctx, authorized, rootResourceType, family.Candidate, resolvedRoute)
	if err != nil {
		return FrameSourceOption{}, false
	}
	frameSource := capability.SemanticFrameChoiceSource{
		Kind: capability.ConstructionChoiceSourceSemanticFrame, AnchorConceptID: family.Entry.ConceptID,
		Family: family.Family, CandidateID: provenCandidate.ID, NodeID: provenCandidate.NodeID,
		FieldPath: provenCandidate.FieldPath,
	}
	choice, err := capability.NewSemanticFrameConstructionChoice(snapshot.Token, contextToken, buildID, frameSource, resolvedRoute, provenCandidate)
	if err != nil {
		return FrameSourceOption{}, false
	}
	forms := supportedFrameForms(choice.Options)
	if len(forms) == 0 {
		return FrameSourceOption{}, false
	}
	defaultForm, hasDefault := preservingFrameDefault(forms)
	if !hasDefault {
		return FrameSourceOption{}, false
	}
	example := strings.TrimSpace(family.Entry.Observation.Key.Display)
	if example == "" {
		example = family.Entry.Observation.Key.Code
	}
	return FrameSourceOption{
		ChoiceID: choice.ChoiceID, Title: frameSourceTitle(family.Family),
		Description:  frameSourceDescription(family.Family, resolvedRoute),
		ResourceType: family.Family.ResourceType, SourcePath: family.Family.SourcePath,
		SourceCanonical: family.Family.SourceCanonical, SourceProfile: family.Family.SourceProfile,
		BindingID: family.Family.BindingID, OwningScope: family.Family.OwningScope,
		KeyPath: family.Family.KeyPath, ValuePath: family.Family.ValuePath,
		LogicalType: family.Family.LogicalType, ExampleConcept: example,
		ObservedOccurrences: family.Entry.Observation.Population, Route: cloneConstructionRoute(resolvedRoute),
		Forms: forms, DefaultForm: defaultForm,
	}, true
}

func frameSourceCommand(commands []authoringv2.Command) bool {
	return len(commands) == 1 && (commands[0].Type == authoringv2.CommandSetFrameSource || commands[0].Type == authoringv2.CommandReplaceFrameSource)
}

func (s *Service) prepareFrameSourceCommand(
	ctx context.Context,
	project, explorerID string,
	authorized AuthorizedCapability,
	workspace authoringv2.Workspace,
	catalogSnapshot authoringv2.CatalogSnapshot,
	command authoringv2.Command,
	identity capability.ConstructionChoiceIdentity,
) ([]authoringv2.Command, error) {
	if command.Type != authoringv2.CommandSetFrameSource && command.Type != authoringv2.CommandReplaceFrameSource {
		return nil, malformed("commands", "unsupported frame source command", nil)
	}
	document := findSemanticOutput(workspace, command.OutputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return nil, malformed("commands", "outputId does not identify a valid row-rooted table", nil)
	}
	if identity.SnapshotToken != authorized.Snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemanticFrame {
		return nil, conflict("commands", "STALE_FRAME_SOURCE", "the frame source belongs to a different authorized snapshot", nil, nil)
	}
	sourceIdentity, ok := identity.Source.(capability.SemanticFrameChoiceSource)
	if !ok {
		return nil, invalidFrameSource("the frame source does not identify a correlated code/value family")
	}
	if len(identity.Route) > 0 && identity.Route[0].FromResourceType != document.RootResourceType || len(identity.Route) == 0 && sourceIdentity.Family.ResourceType != document.RootResourceType {
		return nil, invalidFrameSource("the frame source route does not start at this table's row root")
	}
	entry, err := s.resolveFrameSourceAnchor(ctx, project, explorerID, document.RootResourceType, authorized, identity, sourceIdentity)
	if err != nil {
		return nil, err
	}
	familyCandidates := frameSourceFamilies(authorized.Snapshot, []catalog.SemanticInventoryEntry{entry})
	if len(familyCandidates) != 1 {
		return nil, invalidFrameSource("the frame source is no longer a complete coded value family")
	}
	family := familyCandidates[0]
	if !reflect.DeepEqual(family.Family, sourceIdentity.Family) || family.Candidate.ID != sourceIdentity.CandidateID ||
		family.Candidate.NodeID != sourceIdentity.NodeID || family.Candidate.FieldPath != sourceIdentity.FieldPath {
		return nil, invalidFrameSource("the frame source metadata or compiler candidate changed")
	}
	resolvedRoute, err := reauthorizeConstructionRoute(authorized.Snapshot, document.RootResourceType, family.Candidate.NodeID, identity.Route)
	if err != nil {
		return nil, invalidFrameSource("the frame source route is no longer authorized")
	}
	provenCandidate, err := proveConstructionCandidate(ctx, authorized, document.RootResourceType, family.Candidate, resolvedRoute)
	if err != nil {
		return nil, invalidFrameSource("the frame source route and value no longer compile")
	}
	contextToken, err := semanticInventoryContextToken(authorized.Snapshot, explorerID, document.RootResourceType, identity.BuildID)
	if err != nil {
		return nil, err
	}
	expectedChoice, err := capability.NewSemanticFrameConstructionChoice(
		authorized.Snapshot.Token, contextToken, identity.BuildID,
		capability.SemanticFrameChoiceSource{
			Kind: capability.ConstructionChoiceSourceSemanticFrame, AnchorConceptID: sourceIdentity.AnchorConceptID,
			Family: family.Family, CandidateID: provenCandidate.ID, NodeID: provenCandidate.NodeID, FieldPath: provenCandidate.FieldPath,
		}, resolvedRoute, provenCandidate,
	)
	if err != nil || !constructionChoiceIdentityMatches(expectedChoice, command.FrameChoiceID, identity) ||
		!reflect.DeepEqual(expectedChoice.Source, sourceIdentity) || !reflect.DeepEqual(expectedChoice.Route, identity.Route) {
		return nil, invalidFrameSource("the frame source token does not match current semantic and route evidence")
	}
	if !constructionChoiceSupports(expectedChoice, command.FrameForm) {
		return nil, invalidFrameSource("the selected form is not supported for this exact frame source")
	}
	if command.FrameForm != capability.ConstructionChoiceValue && command.FrameForm != capability.ConstructionChoiceFirst &&
		command.FrameForm != capability.ConstructionChoiceAll && command.FrameForm != capability.ConstructionChoiceDistinct {
		return nil, invalidFrameSource("frame sources support only VALUE, FIRST, ALL, or DISTINCT forms")
	}
	frame, err := authoringv2.NewFrameDefinition(
		frameSourceTitle(family.Family), frameSourceDescription(family.Family, resolvedRoute),
		family.Family, resolvedRoute, command.FrameForm,
	)
	if err != nil {
		return nil, invalidFrameSource("the selected frame definition is invalid")
	}
	commands := []authoringv2.Command{command}
	if err := commands[0].ResolveFrameSource(frame); err != nil {
		return nil, invalidFrameSource("the resolved frame definition is invalid")
	}
	return commands, nil
}

func (s *Service) resolveFrameSourceAnchor(
	ctx context.Context,
	project, explorerID, rowRoot string,
	authorized AuthorizedCapability,
	identity capability.ConstructionChoiceIdentity,
	source capability.SemanticFrameChoiceSource,
) (catalog.SemanticInventoryEntry, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return catalog.SemanticInventoryEntry{}, unavailable("commands", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References: []catalog.SemanticInventoryReference{{ConceptID: source.AnchorConceptID, BindingID: source.Family.BindingID}},
	})
	if err != nil {
		return catalog.SemanticInventoryEntry{}, unavailable("catalog", "CATALOG_UNAVAILABLE", "the frame source evidence could not be resolved", err)
	}
	expectedBuildID := catalog.SemanticInventoryBuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete ||
		resolved.Build.BuildID != expectedBuildID || identity.BuildID != expectedBuildID {
		return catalog.SemanticInventoryEntry{}, conflict("commands", "STALE_SEMANTIC_CONTEXT", "the frame source belongs to a different semantic inventory build", nil, nil)
	}
	if len(resolved.Entries) != 1 || resolved.Entries[0].ConceptID != source.AnchorConceptID || resolved.Entries[0].BindingID != source.Family.BindingID {
		return catalog.SemanticInventoryEntry{}, invalidFrameSource("the frame source anchor is unavailable in the current authorized inventory")
	}
	contextToken, err := semanticInventoryContextToken(authorized.Snapshot, explorerID, rowRoot, expectedBuildID)
	if err != nil {
		return catalog.SemanticInventoryEntry{}, err
	}
	if identity.SemanticContextToken != contextToken {
		return catalog.SemanticInventoryEntry{}, conflict("commands", "STALE_SEMANTIC_CONTEXT", "reload the frame source for the current table root and inventory build", nil, nil)
	}
	return resolved.Entries[0], nil
}

func (s *Service) reauthorizeFrameSourceChoice(
	ctx context.Context,
	project, explorerID, rowRoot, outputID, choiceID string,
	authorized AuthorizedCapability,
	identity capability.ConstructionChoiceIdentity,
) (frameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
	if identity.SnapshotToken != authorized.Snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemanticFrame {
		return frameSourceFamilyCandidate{}, nil, conflict("catalog", "STALE_CONSTRUCTION_CHOICE", "the coded source belongs to a different authorized snapshot", nil, nil)
	}
	source, ok := identity.Source.(capability.SemanticFrameChoiceSource)
	if !ok || strings.TrimSpace(outputID) == "" {
		return frameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source does not identify a correlated code/value family")
	}
	if len(identity.Route) != 0 || source.Family.ResourceType != rowRoot {
		return frameSourceFamilyCandidate{}, nil, unprocessable("catalog", "CODED_PIVOT_SOURCE_UNSUPPORTED", "coded Pivot currently requires a direct root source", nil)
	}
	entry, err := s.resolveFrameSourceAnchor(ctx, project, explorerID, rowRoot, authorized, identity, source)
	if err != nil {
		return frameSourceFamilyCandidate{}, nil, err
	}
	families := frameSourceFamilies(authorized.Snapshot, []catalog.SemanticInventoryEntry{entry})
	if len(families) != 1 {
		return frameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source is no longer a complete coded value family")
	}
	family := families[0]
	if !reflect.DeepEqual(family.Family, source.Family) || family.Candidate.ID != source.CandidateID ||
		family.Candidate.NodeID != source.NodeID || family.Candidate.FieldPath != source.FieldPath {
		return frameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source metadata or compiler candidate changed")
	}
	route, err := reauthorizeConstructionRoute(authorized.Snapshot, rowRoot, family.Candidate.NodeID, identity.Route)
	if err != nil || len(route) != 0 {
		return frameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source route is no longer an authorized direct-root route")
	}
	proven, err := proveConstructionCandidate(ctx, authorized, rowRoot, family.Candidate, route)
	if err != nil {
		return frameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source candidate no longer compiles")
	}
	contextToken, err := semanticInventoryContextToken(authorized.Snapshot, explorerID, rowRoot, identity.BuildID)
	if err != nil {
		return frameSourceFamilyCandidate{}, nil, err
	}
	expected, err := capability.NewSemanticFrameConstructionChoice(
		authorized.Snapshot.Token, contextToken, identity.BuildID,
		capability.SemanticFrameChoiceSource{
			Kind: capability.ConstructionChoiceSourceSemanticFrame, AnchorConceptID: source.AnchorConceptID,
			Family: family.Family, CandidateID: proven.ID, NodeID: proven.NodeID, FieldPath: proven.FieldPath,
		}, route, proven,
	)
	if err != nil || !constructionChoiceIdentityMatches(expected, choiceID, identity) ||
		!reflect.DeepEqual(expected.Source, source) || !reflect.DeepEqual(expected.Route, identity.Route) {
		return frameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source token does not match current semantic and compiler evidence")
	}
	family.Candidate = proven
	return family, route, nil
}

func invalidFrameSource(message string) error {
	return unprocessable("frame-source", "INVALID_FRAME_SOURCE", message, nil)
}

func frameSourceFamilies(snapshot capability.Snapshot, entries []catalog.SemanticInventoryEntry) []frameSourceFamilyCandidate {
	byFamily := make(map[string]frameSourceFamilyCandidate)
	for _, entry := range entries {
		observation := entry.Observation
		plan := authoringv2.ResolveSemanticSelectionPlan(observation)
		if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
			continue
		}
		binding := plan.Source.Lookup.Binding
		candidate, ok := semanticConstructionCandidate(snapshot, observation)
		if !ok || candidate.ResourceType != observation.Source.Type || canonicalInventoryPath(candidate.FieldPath) != semanticObservationValuePath(observation) {
			continue
		}
		family := capability.SemanticFrameFamily{
			BindingID: entry.BindingID, ResourceType: observation.Source.Type,
			SourcePath: observation.Source.Path, SourceCanonical: observation.Source.Canonical,
			SourceProfile: observation.Source.Profile, OwningScope: binding.OwnerPath,
			KeyPath: binding.KeyPath, ValuePath: binding.ValuePath,
			ChoiceArms: append([]string(nil), binding.ChoiceArms...), LogicalType: binding.LogicalType,
			RuleVersion: observation.RuleVersion, SchemaVersion: observation.SchemaVersion,
		}
		keyBytes, _ := json.Marshal(family)
		key := string(keyBytes)
		current, found := byFamily[key]
		if !found || observation.Population > current.Entry.Observation.Population ||
			observation.Population == current.Entry.Observation.Population && entry.ConceptID < current.Entry.ConceptID {
			byFamily[key] = frameSourceFamilyCandidate{Family: family, Entry: entry, Candidate: candidate}
		}
	}
	families := make([]frameSourceFamilyCandidate, 0, len(byFamily))
	for _, family := range byFamily {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool {
		if families[i].Entry.Observation.Population != families[j].Entry.Observation.Population {
			return families[i].Entry.Observation.Population > families[j].Entry.Observation.Population
		}
		leftTitle, rightTitle := frameSourceTitle(families[i].Family), frameSourceTitle(families[j].Family)
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		left, right := frameSourceStableKey(families[i].Family), frameSourceStableKey(families[j].Family)
		return left < right
	})
	return families
}

func frameSourceStableKey(family capability.SemanticFrameFamily) string {
	value, _ := json.Marshal(family)
	return string(value)
}

func frameSourceRouteKey(outputID string, family capability.SemanticFrameFamily, candidateID string) string {
	value, _ := json.Marshal(struct {
		OutputID    string
		Family      capability.SemanticFrameFamily
		CandidateID string
	}{outputID, family, candidateID})
	digest := sha256.Sum256(value)
	return "frame-source:" + hex.EncodeToString(digest[:])
}

func frameSourceContext(snapshot capability.Snapshot, explorerID, outputID, rootResource, resourceType, query string) (string, error) {
	value, err := json.Marshal(struct {
		Snapshot string
		Explorer string
		Output   string
		Root     string
		Resource string
		Query    string
	}{snapshot.Token, explorerID, outputID, rootResource, resourceType, query})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

func supportedFrameForms(options []capability.ConstructionChoiceOption) []FrameSourceForm {
	forms := make([]FrameSourceForm, 0, 4)
	for _, option := range options {
		if option.Support != capability.ConstructionChoiceSupported {
			continue
		}
		var zero authoringv2.FrameZeroPolicy
		var many authoringv2.FrameManyPolicy
		switch option.Form {
		case capability.ConstructionChoiceValue:
			zero, many = authoringv2.FrameZeroNull, authoringv2.FrameManyInvalidMultipleValues
		case capability.ConstructionChoiceFirst:
			zero, many = authoringv2.FrameZeroNull, authoringv2.FrameManyFirst
		case capability.ConstructionChoiceAll:
			zero, many = authoringv2.FrameZeroEmptyList, authoringv2.FrameManyAll
		case capability.ConstructionChoiceDistinct:
			zero, many = authoringv2.FrameZeroEmptyList, authoringv2.FrameManyDistinct
		default:
			continue
		}
		forms = append(forms, FrameSourceForm{Form: option.Form, ZeroPolicy: zero, ManyPolicy: many, Decision: string(option.Decision)})
	}
	return forms
}

func preservingFrameDefault(forms []FrameSourceForm) (capability.ConstructionChoiceForm, bool) {
	for _, form := range forms {
		if form.Decision != string(capability.ConstructionChoiceDefault) {
			continue
		}
		switch form.Form {
		case capability.ConstructionChoiceValue, capability.ConstructionChoiceAll:
			return form.Form, true
		}
	}
	return "", false
}

func frameSourceTitle(family capability.SemanticFrameFamily) string {
	path := strings.Trim(family.SourcePath, ".")
	if index := strings.Index(path, ".code"); index >= 0 {
		path = path[:index]
	}
	path = strings.ReplaceAll(path, "[]", "")
	path = strings.ReplaceAll(path, ".", " ")
	path = strings.TrimSpace(path)
	if path == "" {
		return family.ResourceType + " values (" + family.LogicalType + ")"
	}
	return family.ResourceType + " " + path + " values (" + family.LogicalType + ")"
}

func frameSourceDescription(family capability.SemanticFrameFamily, route []capability.ConstructionRouteStep) string {
	where := family.ResourceType + " records"
	if len(route) > 0 {
		where = family.ResourceType + " records reached from " + route[0].FromResourceType
		for _, step := range route {
			where += " through " + step.Relationship
		}
	}
	return "Codes and their paired values on " + where + "."
}
