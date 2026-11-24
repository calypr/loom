package semanticbrowse

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/semanticauthoring"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
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
	ZeroPolicy ws.FrameZeroPolicy                `json:"zeroPolicy"`
	ManyPolicy ws.FrameManyPolicy                `json:"manyPolicy"`
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

func (browse *Service) BrowseFrameSourceOptions(ctx context.Context, request BrowseFrameSourceOptionsRequest) (BrowseFrameSourceOptionsResponse, error) {
	result := BrowseFrameSourceOptionsResponse{
		SnapshotToken: request.SnapshotToken, OutputID: request.OutputID, Sources: []FrameSourceOption{},
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" ||
		strings.TrimSpace(request.SnapshotToken) == "" || strings.TrimSpace(request.OutputID) == "" ||
		len(request.Query) > 256 || len(request.Cursor) > 4096 || request.Limit < 0 || request.Limit > capability.ConstructionRouteMaxLimit {
		return result, support.Malformed("frame-source-options", "project, explorerId, snapshotToken, outputId, and a page limit of at most 50 are required", nil)
	}
	if browse.semanticInventory == nil || browse.capability.ForCompilation == nil {
		return result, support.Unavailable("frame-source-options", "CATALOG_UNAVAILABLE", "authorized semantic inventory is not configured", nil)
	}
	if request.Limit == 0 {
		request.Limit = frameSourceDefaultLimit
	}
	authorized, err := browse.capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, support.Conflict("frame-source-options", "STALE_CATALOG_SNAPSHOT", "reload the catalog before choosing a frame source", nil, err)
	}
	if err != nil {
		return result, authoringcore.CapabilityResolutionFailure("frame-source-options", "reload the catalog before choosing a frame source", err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return result, support.Conflict("frame-source-options", "STALE_CATALOG_SNAPSHOT", "reload the catalog before choosing a frame source", nil, nil)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, support.Conflict("frame-source-options", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	workspace, err := browse.core.CurrentWorkspace(ctx, "construction-choices", request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	document := workspace.FindOutputDocument(request.OutputID)
	if document == nil || document.Route.OccurrenceID != ws.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, support.Malformed("frame-source-options", "outputId does not identify a valid row-rooted table", nil)
	}
	rootAllowed, resourceAllowed := false, request.ResourceType == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == document.RootResourceType && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == request.ResourceType
	}
	if !rootAllowed || !resourceAllowed {
		return result, support.Malformed("frame-source-options", "the row root or resource filter is not available in this snapshot", nil)
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
			return result, support.Conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, decodeErr)
		}
	}
	result.Sources = make([]FrameSourceOption, 0, request.Limit)
	truncated := false
	for len(result.Sources) < request.Limit {
		page, pageErr := browse.semanticInventory(ctx, semantic.PageOptions{
			Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
			AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
			ResourceType: request.ResourceType, Query: request.Query, Cursor: state.Page, Limit: semantic.PageLimit,
		})
		if errors.Is(pageErr, semantic.ErrSemanticInventoryCursorMismatch) {
			return BrowseFrameSourceOptionsResponse{}, support.Conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, pageErr)
		}
		if pageErr != nil {
			return BrowseFrameSourceOptionsResponse{}, support.PersistenceFailure("frame-source-options", "frame source options could not be read", pageErr)
		}
		contextToken, tokenErr := SemanticInventoryContextToken(snapshot, request.ExplorerID, document.RootResourceType, page.Build.BuildID)
		if tokenErr != nil {
			return BrowseFrameSourceOptionsResponse{}, tokenErr
		}
		if page.Build.BuildID != semantic.BuildID(projectid.Legacy(snapshot.Identity.Project), snapshot.Identity.Generation) || page.State != semantic.Complete {
			return BrowseFrameSourceOptionsResponse{}, support.Conflict("frame-source-options", "SEMANTIC_INVENTORY_UNAVAILABLE", "the current generation does not have a complete semantic inventory", nil, nil)
		}
		families := frameSourceFamilies(snapshot, page.Entries)
		if state.FamilyIndex > len(families) || state.Phase == 1 && state.FamilyIndex == len(families) && state.Route != "" {
			return BrowseFrameSourceOptionsResponse{}, support.Conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, nil)
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
				return BrowseFrameSourceOptionsResponse{}, support.Conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, routeErr)
			}
			truncated = truncated || routes.Truncated && routes.NextCursor == ""
			if len(routes.Routes) == 1 {
				option, found := browse.frameSourceOptionForRoute(ctx, authorized, snapshot, document.RootResourceType, contextToken, page.Build.BuildID, family, routes.Routes[0])
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
				return BrowseFrameSourceOptionsResponse{}, support.Conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, routeErr)
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
			return BrowseFrameSourceOptionsResponse{}, support.Conflict("frame-source-options", "STALE_FRAME_SOURCE_CURSOR", "restart frame source search with an empty cursor", nil, routeErr)
		}
		truncated = truncated || pageRoutes.Truncated && pageRoutes.NextCursor == ""
		for _, route := range pageRoutes.Routes {
			option, found := browse.frameSourceOptionForRoute(ctx, authorized, snapshot, document.RootResourceType, contextToken, page.Build.BuildID, family, route)
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

func (browse *Service) frameSourceOptionForRoute(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType, contextToken, buildID string,
	family ports.FrameSourceFamilyCandidate,
	route []capability.ConstructionRouteStep,
) (FrameSourceOption, bool) {
	resolvedRoute, err := support.ReauthorizeConstructionRoute(snapshot, rootResourceType, family.Candidate.NodeID, route)
	if err != nil {
		return FrameSourceOption{}, false
	}
	provenCandidate, err := proveConstructionCandidate(ctx, authorized, rootResourceType, family.Candidate, resolvedRoute)
	if err != nil {
		return FrameSourceOption{}, false
	}
	provenCandidate, ok := capability.SemanticCandidateWithFallback(snapshot, family.Entry.Observation, provenCandidate)
	if !ok {
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
		ChoiceID: choice.ChoiceID, Title: FrameSourceTitle(family.Family),
		Description:  FrameSourceDescription(family.Family, resolvedRoute),
		ResourceType: family.Family.ResourceType, SourcePath: family.Family.SourcePath,
		SourceCanonical: family.Family.SourceCanonical, SourceProfile: family.Family.SourceProfile,
		BindingID: family.Family.BindingID, OwningScope: family.Family.OwningScope,
		KeyPath: family.Family.KeyPath, ValuePath: family.Family.ValuePath,
		LogicalType: family.Family.LogicalType, ExampleConcept: example,
		ObservedOccurrences: family.Entry.Observation.Population, Route: capability.CloneConstructionRoute(resolvedRoute),
		Forms: forms, DefaultForm: defaultForm,
	}, true
}

func frameSourceFamilies(snapshot capability.Snapshot, entries []semantic.Entry) []ports.FrameSourceFamilyCandidate {
	byFamily := make(map[string]ports.FrameSourceFamilyCandidate)
	for _, entry := range entries {
		observation := entry.Observation
		if observation.RuleHint != semantic.RuleHintCodedValueV1 {

			continue
		}
		plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
		if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != ws.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
			continue
		}
		binding := plan.Source.Lookup.Binding
		candidate, ok := capability.SemanticConstructionCandidate(snapshot, observation)
		if !ok {
			continue
		}
		family := capability.SemanticFrameFamily{
			BindingID: entry.BindingID, ResourceType: observation.Source.Type,
			SourcePath: observation.Source.Path, SourceCanonical: observation.Source.Canonical,
			SourceProfile: observation.Source.Profile, OwningScope: binding.OwnerPath,
			KeyPath: binding.KeyPath, ValuePath: binding.ValuePath,
			ValueFallbacks: append([]string(nil), binding.ValueFallback...), FallbackOnBlankString: binding.FallbackOnBlankString,
			ChoiceArms: append([]string(nil), binding.ChoiceArms...), LogicalType: binding.LogicalType,
			RuleVersion: observation.RuleVersion, SchemaVersion: observation.SchemaVersion,
		}
		keyBytes, _ := json.Marshal(family)
		key := string(keyBytes)
		current, found := byFamily[key]
		if !found || observation.Population > current.Entry.Observation.Population ||
			observation.Population == current.Entry.Observation.Population && entry.ConceptID < current.Entry.ConceptID {
			byFamily[key] = ports.FrameSourceFamilyCandidate{Family: family, Entry: entry, Candidate: candidate}
		}
	}
	families := make([]ports.FrameSourceFamilyCandidate, 0, len(byFamily))
	for _, family := range byFamily {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool {
		if families[i].Entry.Observation.Population != families[j].Entry.Observation.Population {
			return families[i].Entry.Observation.Population > families[j].Entry.Observation.Population
		}
		leftTitle, rightTitle := FrameSourceTitle(families[i].Family), FrameSourceTitle(families[j].Family)
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
		var zero ws.FrameZeroPolicy
		var many ws.FrameManyPolicy
		switch option.Form {
		case capability.ConstructionChoiceValue:
			zero, many = ws.FrameZeroNull, ws.FrameManyInvalidMultipleValues
		case capability.ConstructionChoiceFirst:
			zero, many = ws.FrameZeroNull, ws.FrameManyFirst
		case capability.ConstructionChoiceAll:
			zero, many = ws.FrameZeroEmptyList, ws.FrameManyAll
		case capability.ConstructionChoiceDistinct:
			zero, many = ws.FrameZeroEmptyList, ws.FrameManyDistinct
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

func FrameSourceTitle(family capability.SemanticFrameFamily) string {
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

func FrameSourceDescription(family capability.SemanticFrameFamily, route []capability.ConstructionRouteStep) string {
	where := family.ResourceType + " records"
	if len(route) > 0 {
		where = family.ResourceType + " records reached from " + route[0].FromResourceType
		for _, step := range route {
			where += " through " + step.Relationship
		}
	}
	return "Codes and their paired values on " + where + "."
}

func FrameSourceFamilies(snapshot capability.Snapshot, entries []semantic.Entry) []ports.FrameSourceFamilyCandidate {
	return frameSourceFamilies(snapshot, entries)
}
