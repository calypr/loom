package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type BrowseSemanticInventoryRequest struct {
	Project        string
	ExplorerID     string
	SnapshotToken  string
	RowRoot        string
	FrameID        string
	SourceChoiceID string
	OutputID       string
	ResourceType   string
	Query          string
	Cursor         string
	Limit          int
}

type SemanticInventoryItem struct {
	ConceptID              string                                  `json:"conceptId"`
	BindingID              string                                  `json:"bindingId"`
	ResourceType           string                                  `json:"resourceType"`
	SourcePath             string                                  `json:"sourcePath"`
	System                 string                                  `json:"system"`
	Code                   string                                  `json:"code"`
	CodingVersion          string                                  `json:"codingVersion"`
	Display                string                                  `json:"display"`
	ValueSelector          string                                  `json:"valueSelector"`
	ValueType              string                                  `json:"valueType"`
	OwningScope            string                                  `json:"owningScope"`
	Occurrences            int64                                   `json:"occurrences"`
	Examples               []string                                `json:"examples,omitempty"`
	ExamplesTruncated      bool                                    `json:"examplesTruncated"`
	ObservedUnits          []string                                `json:"observedUnits,omitempty"`
	ObservedUnitsTruncated bool                                    `json:"observedUnitsTruncated"`
	Completeness           catalog.SemanticObservationCompleteness `json:"completeness,omitempty"`
	Readiness              authoringv2.SemanticSelectionReadiness  `json:"readiness"`
	ConstructionChoice     *capability.ConstructionChoice          `json:"constructionChoice,omitempty"`
}

type BrowseSemanticInventoryResponse struct {
	FrameID            string                         `json:"frameId,omitempty"`
	FrameSource        *authoringv2.FrameDefinition   `json:"frameSource,omitempty"`
	ContextToken       string                         `json:"contextToken"`
	BuildID            string                         `json:"buildId"`
	State              catalog.SemanticInventoryState `json:"state"`
	SourceAvailability string                         `json:"sourceAvailability"`
	Entries            []SemanticInventoryItem        `json:"entries"`
	NextCursor         string                         `json:"nextCursor,omitempty"`
}

type semanticBrowseCursor struct {
	Context        string `json:"context"`
	Page           string `json:"page"`
	FrameID        string `json:"frameId,omitempty"`
	SourceChoiceID string `json:"sourceChoiceId,omitempty"`
	OutputID       string `json:"outputId,omitempty"`
}

func (s *Service) BrowseSemanticInventory(ctx context.Context, req BrowseSemanticInventoryRequest) (BrowseSemanticInventoryResponse, error) {
	var result BrowseSemanticInventoryResponse
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || req.SnapshotToken == "" || req.RowRoot == "" || req.Limit < 0 || req.Limit > catalog.SemanticInventoryPageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
		return result, malformed("catalog", "project, explorer, snapshotToken, rowRoot, and a page limit of at most 50 are required", nil)
	}
	if s.config.SemanticInventory == nil || s.config.Capability.ForCompilation == nil {
		return result, unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory browsing is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, req.Project, req.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, err)
	}
	if err != nil {
		return result, err
	}
	snapshot := authorized.Snapshot
	if snapshot.ValidateToken(req.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(req.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("catalog", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	rootAllowed := false
	resourceAllowed := req.ResourceType == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == req.RowRoot && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == req.ResourceType
	}
	if !rootAllowed || !resourceAllowed {
		return result, malformed("catalog", "the row root or resource filter is not available in this snapshot", nil)
	}
	if req.FrameID != "" && req.SourceChoiceID != "" {
		return result, malformed("catalog", "frameId and sourceChoiceId are mutually exclusive", nil)
	}
	var frame *authoringv2.FrameDefinition
	var codedFamily *frameSourceFamilyCandidate
	var codedRoute []capability.ConstructionRouteStep
	bindingFilter := ""
	if req.FrameID != "" {
		if strings.TrimSpace(req.OutputID) == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return result, malformed("catalog", "outputId is required with frameId", nil)
		}
		workspace, workspaceErr := s.currentWorkspace(ctx, req.Project, req.ExplorerID)
		if workspaceErr != nil {
			return result, workspaceErr
		}
		documentIndex := -1
		for index := range workspace.Documents {
			if workspace.Documents[index].Output.ID == req.OutputID {
				documentIndex = index
				break
			}
		}
		if documentIndex < 0 || workspace.Documents[documentIndex].RootResourceType != req.RowRoot {
			return result, notFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
		}
		for _, candidate := range workspace.Documents[documentIndex].Frames {
			if candidate.ID == req.FrameID {
				copy := candidate
				copy.Route = cloneConstructionRoute(candidate.Route)
				copy.Source.ChoiceArms = append([]string(nil), candidate.Source.ChoiceArms...)
				frame = &copy
			}
		}
		if frame == nil {
			return result, notFound("catalog", "FRAME_NOT_FOUND", "the saved frame source was not found for this row root", nil)
		}
		if req.ResourceType != "" && req.ResourceType != frame.Source.ResourceType {
			return result, malformed("catalog", "resourceType must match the saved frame source", nil)
		}
		req.ResourceType = frame.Source.ResourceType
		bindingFilter = frame.Source.BindingID
		if isPriorPrimaryOnlySemanticFrameFamily(frame.Source) {
			// The current inventory uses semantic-binding/v2 IDs. Query the
			// authorized resource page without the obsolete v1 filter, then require
			// an exact reconstructed v1 identity in semanticEntryForFrameFamily.
			bindingFilter = ""
		}
		result.FrameID, result.FrameSource = frame.ID, frame
	}
	if req.SourceChoiceID != "" {
		if strings.TrimSpace(req.OutputID) == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return result, malformed("catalog", "outputId is required with sourceChoiceId", nil)
		}
		workspace, workspaceErr := s.currentWorkspace(ctx, req.Project, req.ExplorerID)
		if workspaceErr != nil {
			return result, workspaceErr
		}
		document := findSemanticOutput(workspace, req.OutputID)
		if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.RootResourceType != req.RowRoot {
			return result, notFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
		}
		identity, decodeErr := capability.DecodeConstructionChoiceID(req.SourceChoiceID)
		if decodeErr != nil || identity.SnapshotToken != snapshot.Token {
			return result, conflict("catalog", "STALE_CONSTRUCTION_CHOICE", "reload the coded source for the current authorized snapshot", nil, decodeErr)
		}
		family, route, verifyErr := s.reauthorizeFrameSourceChoice(ctx, req.Project, req.ExplorerID, req.RowRoot, req.OutputID, req.SourceChoiceID, authorized, identity)
		if verifyErr != nil {
			return result, verifyErr
		}
		if len(route) != 0 || family.Family.ResourceType != req.RowRoot {
			return result, unprocessable("catalog", "CODED_PIVOT_SOURCE_UNSUPPORTED", "coded Pivot currently requires a direct root source", nil)
		}
		if req.ResourceType != "" && req.ResourceType != family.Family.ResourceType {
			return result, malformed("catalog", "resourceType must match sourceChoiceId", nil)
		}
		req.ResourceType = family.Family.ResourceType
		bindingFilter = family.Family.BindingID
		codedFamily = &family
		codedRoute = route
	}
	var cursor semanticBrowseCursor
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Context == "" || cursor.Page == "" || cursor.FrameID != req.FrameID || cursor.SourceChoiceID != req.SourceChoiceID || cursor.OutputID != req.OutputID {
			return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, nil)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	page, err := s.config.SemanticInventory(ctx, catalog.SemanticInventoryPageOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: authorized.Scope.AuthResourcePaths,
		ResourceType: req.ResourceType, BindingID: bindingFilter, Query: req.Query, Cursor: cursor.Page, Limit: req.Limit,
	})
	if errors.Is(err, catalog.ErrSemanticInventoryCursorMismatch) {
		return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, err)
	}
	if err != nil {
		return result, err
	}
	result.ContextToken, err = semanticInventoryContextToken(snapshot, req.ExplorerID, req.RowRoot, page.Build.BuildID)
	if err != nil {
		return result, err
	}
	if req.Cursor != "" && cursor.Context != result.ContextToken {
		return BrowseSemanticInventoryResponse{}, conflict("catalog", "STALE_CATALOG_CURSOR", "catalog context changed; restart search", nil, nil)
	}
	result.BuildID, result.State = page.Build.BuildID, page.State
	result.SourceAvailability = page.Build.SourceAvailability
	if result.SourceAvailability == "" {
		result.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnknown
	}
	result.Entries = make([]SemanticInventoryItem, 0, len(page.Entries))
	for _, entry := range page.Entries {
		observation := entry.Observation
		plan := authoringv2.ResolveSemanticSelectionPlan(observation)
		if frame != nil {
			projected, projectedPlan, ok := semanticEntryForFrameFamily(entry, frame.Source)
			if !ok {
				continue
			}
			entry, observation, plan = projected, projected.Observation, projectedPlan
		}
		item := SemanticInventoryItem{
			ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
			SourcePath: observation.Source.Path, System: observation.Key.System, Code: observation.Key.Code,
			CodingVersion: observation.Key.Version, Display: observation.Key.Display,
			ValueSelector: observation.Value.Selector, ValueType: observation.Value.Type,
			OwningScope: observation.OwningScope, Occurrences: observation.Population,
			Examples: append([]string(nil), observation.Examples...), ExamplesTruncated: observation.ExamplesTruncated,
			ObservedUnits: append([]string(nil), observation.ObservedUnits...), ObservedUnitsTruncated: observation.ObservedUnitsTruncated,
			Completeness: observation.Completeness,
			Readiness:    plan.Readiness,
		}
		if plan.Readiness.Addable() {
			if candidate, ok := semanticConstructionCandidate(snapshot, observation); ok {
				if codedFamily != nil && (candidate.ID != codedFamily.Candidate.ID || candidate.NodeID != codedFamily.Candidate.NodeID || candidate.FieldPath != codedFamily.Candidate.FieldPath) {
					continue
				}
				var route []capability.ConstructionRouteStep
				if frame != nil {
					if !semanticEntryMatchesFrame(entry, *frame) {
						continue
					}
					resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, req.RowRoot, candidate.NodeID, frame.Route)
					if routeErr != nil {
						continue
					}
					route = resolvedRoute
				} else if codedFamily != nil {
					if observation.Key.Version != "" || !semanticEntryMatchesFrameFamily(entry, codedFamily.Family) {
						continue
					}
					route = codedRoute
				}
				ownerRecordsProved := frame == nil && proveOwnerRecordsForRoute(ctx, authorized, req.RowRoot, observation, route)
				if choice, err := semanticInventoryConstructionChoiceForRoute(snapshot, result.ContextToken, result.BuildID, route, entry, candidate, ownerRecordsProved); err == nil {
					if frame != nil && !constructionChoiceSupports(choice, frame.Form) {
						continue
					}
					item.ConstructionChoice = &choice
				} else {
					if frame != nil {
						continue
					}
					item.Readiness = compilerProofUnavailable()
				}
			} else {
				if frame != nil {
					continue
				}
				item.Readiness = compilerProofUnavailable()
			}
		} else if frame != nil {
			continue
		}
		result.Entries = append(result.Entries, item)
	}
	if page.NextCursor != "" {
		raw, err := json.Marshal(semanticBrowseCursor{Context: result.ContextToken, Page: page.NextCursor, FrameID: req.FrameID, SourceChoiceID: req.SourceChoiceID, OutputID: req.OutputID})
		if err != nil {
			return BrowseSemanticInventoryResponse{}, err
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}

func semanticInventoryConstructionChoice(snapshot capability.Snapshot, contextToken, buildID string, entry catalog.SemanticInventoryEntry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	return semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, entry, candidate, ownerRecordsProved)
}

func semanticInventoryConstructionChoiceForRoute(snapshot capability.Snapshot, contextToken, buildID string, route []capability.ConstructionRouteStep, entry catalog.SemanticInventoryEntry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	observation := entry.Observation
	logicalType := observation.LogicalType
	if logicalType == "" {
		logicalType = observation.Value.Type
	}
	source := capability.SemanticBindingChoiceSource{
		ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
		SourcePath: observation.Source.Path, SourceCanonical: observation.Source.Canonical,
		SourceProfile: observation.Source.Profile, FieldPath: candidate.FieldPath,
		OwningScope: observation.OwningScope, ExtensionURLPath: observation.ExtensionURLPath,
		KeySelector: observation.Key.Selector, System: observation.Key.System, Version: observation.Key.Version,
		Code: observation.Key.Code, ValueSelector: observation.Value.Selector,
		ValueFallbacks:        append([]string(nil), observation.Value.FallbackSelectors...),
		FallbackOnBlankString: observation.Value.FallbackOnBlankString, ChoiceArm: observation.ChoiceArm,
		LogicalType: logicalType, RuleHint: observation.RuleHint, RuleVersion: observation.RuleVersion,
		SchemaVersion: observation.SchemaVersion,
	}
	projectedCandidate, ok := semanticCandidateWithFallback(snapshot, observation, candidate)
	if !ok {
		return capability.ConstructionChoice{}, errors.New("semantic fallback value selectors have no exact compiler-proved candidates")
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, route, source, projectedCandidate, ownerRecordsProved)
}

func semanticEntryMatchesFrame(entry catalog.SemanticInventoryEntry, frame authoringv2.FrameDefinition) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, frame.Source)
	return ok
}

func semanticEntryMatchesFrameFamily(entry catalog.SemanticInventoryEntry, family capability.SemanticFrameFamily) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, family)
	return ok
}

func semanticFrameFamilyMatchesPinnedPrior(current, pinned capability.SemanticFrameFamily, observation catalog.SemanticObservation) bool {
	return current.SchemaVersion == catalog.SemanticObservationSchemaVersion &&
		current.RuleVersion == strconv.Itoa(catalog.SemanticObservationRuleVersion) &&
		pinned.SchemaVersion == catalog.SemanticObservationLegacyV1SchemaVersion &&
		pinned.RuleVersion == strconv.Itoa(catalog.SemanticObservationLegacyV1RuleVersion) &&
		len(pinned.ValueFallbacks) == 0 && !pinned.FallbackOnBlankString &&
		pinned.BindingID == catalog.LegacySemanticBindingIDV1(observation) &&
		semanticEntrySourceMatchesFrameFamily(observation, pinned) &&
		current.ResourceType == pinned.ResourceType &&
		current.SourcePath == pinned.SourcePath && current.SourceCanonical == pinned.SourceCanonical &&
		current.SourceProfile == pinned.SourceProfile && current.OwningScope == pinned.OwningScope &&
		current.KeyPath == pinned.KeyPath && current.ValuePath == pinned.ValuePath &&
		current.LogicalType == pinned.LogicalType && reflect.DeepEqual(current.ChoiceArms, pinned.ChoiceArms)
}

// semanticEntryForFrameFamily projects a current inventory observation back
// to the exact value binding pinned by a saved frame. The only supported
// transition is the immediately preceding semantic build, whose new behavior
// adds fallback selectors. A legacy frame without fallbacks keeps its primary
// selector and cannot silently acquire the new fallback behavior.
func semanticEntryForFrameFamily(
	entry catalog.SemanticInventoryEntry,
	family capability.SemanticFrameFamily,
) (catalog.SemanticInventoryEntry, authoringv2.SemanticSelectionPlan, bool) {
	observation := entry.Observation
	if !semanticEntrySourceMatchesFrameFamily(observation, family) || observation.Key.Version != "" {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}

	currentRuleVersion := strconv.Itoa(catalog.SemanticObservationRuleVersion)
	if observation.SchemaVersion != catalog.SemanticObservationSchemaVersion || observation.RuleVersion != currentRuleVersion {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	currentFamily := family.SchemaVersion == observation.SchemaVersion && family.RuleVersion == observation.RuleVersion && family.BindingID == entry.BindingID
	priorFamily := isPriorPrimaryOnlySemanticFrameFamily(family) &&
		family.BindingID == catalog.LegacySemanticBindingIDV1(observation)
	if !currentFamily && !priorFamily {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	if priorFamily {
		observation.Value.FallbackSelectors = nil
		observation.Value.FallbackOnBlankString = false
		entry.Observation = observation
	}

	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue ||
		plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	binding := plan.Source.Lookup.Binding
	if binding.OwnerPath != family.OwningScope || binding.KeyPath != family.KeyPath || binding.ValuePath != family.ValuePath ||
		!slices.Equal(binding.ValueFallback, family.ValueFallbacks) || binding.FallbackOnBlankString != family.FallbackOnBlankString ||
		binding.LogicalType != family.LogicalType || !reflect.DeepEqual(binding.ChoiceArms, family.ChoiceArms) {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	return entry, plan, true
}

func isPriorPrimaryOnlySemanticFrameFamily(family capability.SemanticFrameFamily) bool {
	return family.SchemaVersion == catalog.SemanticObservationLegacyV1SchemaVersion &&
		family.RuleVersion == strconv.Itoa(catalog.SemanticObservationLegacyV1RuleVersion) &&
		len(family.ValueFallbacks) == 0 && !family.FallbackOnBlankString
}

func semanticEntrySourceMatchesFrameFamily(observation catalog.SemanticObservation, family capability.SemanticFrameFamily) bool {
	return observation.Source.Type == family.ResourceType &&
		observation.Source.Path == family.SourcePath && observation.Source.Canonical == family.SourceCanonical &&
		observation.Source.Profile == family.SourceProfile
}

func proveOwnerRecords(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, observation catalog.SemanticObservation) bool {
	return proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, nil)
}

func proveOwnerRecordsForRoute(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, observation catalog.SemanticObservation, route []capability.ConstructionRouteStep) bool {
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return false
	}
	compilerRoute := make([]compilerprobe.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		compilerRoute = append(compilerRoute, compilerprobe.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode})
	}
	_, err := compilerprobe.ProbeOwnerRecords(ctx, compilerprobe.OwnerRecordsRequest{
		Scope: compilerprobe.Scope{
			Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
			AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode,
		},
		RootResourceType: rootResourceType,
		ResourceType:     observation.Source.Type,
		Route:            compilerRoute,
		Binding:          *plan.Source.Lookup.Binding,
		Key:              *plan.Source.Lookup.Key,
	})
	return err == nil
}

func semanticConstructionCandidate(snapshot capability.Snapshot, observation catalog.SemanticObservation) (capability.Candidate, bool) {
	resourceType := strings.TrimSpace(observation.Source.Type)
	valuePath := semanticObservationValuePath(observation)
	if resourceType == "" || valuePath == "" {
		return capability.Candidate{}, false
	}
	var match capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ResourceType == resourceType && canonicalInventoryPath(candidate.FieldPath) == valuePath {
			if found {
				return capability.Candidate{}, false
			}
			match, found = candidate, true
		}
	}
	return match, found
}

func semanticCandidateWithFallback(snapshot capability.Snapshot, observation catalog.SemanticObservation, primary capability.Candidate) (capability.Candidate, bool) {
	if len(observation.Value.FallbackSelectors) == 0 {
		return primary, !observation.Value.FallbackOnBlankString
	}
	if primary.LogicalType != observation.Value.Type {
		return capability.Candidate{}, false
	}
	combined := primary
	unknownCardinality := strings.EqualFold(strings.TrimSpace(primary.Cardinality), "unknown_observed_many")
	repeatedCardinality := capability.IsRepeatedCardinality(primary.Cardinality)
	projectionModes := make(map[capability.ProjectionMode]struct{}, len(primary.ProjectionModes))
	for _, mode := range primary.ProjectionModes {
		projectionModes[mode] = struct{}{}
	}
	boundaries := make(map[string]capability.RepeatedBoundary, len(primary.RepeatedBoundaries))
	for _, boundary := range primary.RepeatedBoundaries {
		boundaries[boundary.Path] = boundary
	}
	for _, selector := range observation.Value.FallbackSelectors {
		path := canonicalInventoryPath(selector)
		ownerPath := canonicalInventoryPath(observation.OwningScope)
		if ownerPath != "" && path != ownerPath && !strings.HasPrefix(path, ownerPath+".") {
			path = ownerPath + "." + path
		}
		var fallback capability.Candidate
		found := false
		for _, candidate := range snapshot.Candidates {
			if candidate.ResourceType != observation.Source.Type || canonicalInventoryPath(candidate.FieldPath) != path {
				continue
			}
			if found || candidate.NodeID != primary.NodeID || candidate.LogicalType != primary.LogicalType {
				return capability.Candidate{}, false
			}
			fallback, found = candidate, true
		}
		if !found || len(fallback.ProjectionModes) == 0 {
			return capability.Candidate{}, false
		}
		unknownCardinality = unknownCardinality || strings.EqualFold(strings.TrimSpace(fallback.Cardinality), "unknown_observed_many")
		repeatedCardinality = repeatedCardinality || capability.IsRepeatedCardinality(fallback.Cardinality)
		for _, boundary := range fallback.RepeatedBoundaries {
			if existing, ok := boundaries[boundary.Path]; !ok || boundary.MaxItems > existing.MaxItems {
				boundaries[boundary.Path] = boundary
			}
		}
		for _, mode := range fallback.ProjectionModes {
			projectionModes[mode] = struct{}{}
		}
	}
	if unknownCardinality {
		combined.Cardinality = "unknown_observed_many"
	} else if repeatedCardinality {
		combined.Cardinality = "many"
	}
	combined.RepeatedBoundaries = make([]capability.RepeatedBoundary, 0, len(boundaries))
	for _, boundary := range boundaries {
		combined.RepeatedBoundaries = append(combined.RepeatedBoundaries, boundary)
	}
	sort.Slice(combined.RepeatedBoundaries, func(i, j int) bool { return combined.RepeatedBoundaries[i].Path < combined.RepeatedBoundaries[j].Path })
	combined.ProjectionModes = make([]capability.ProjectionMode, 0, len(projectionModes))
	for _, mode := range []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray} {
		if _, ok := projectionModes[mode]; ok {
			combined.ProjectionModes = append(combined.ProjectionModes, mode)
		}
	}
	return combined, true
}

// Coded-value observations keep key and value selectors relative to the object
// that owns their correlation. Capability candidates address the scalar from
// the resource root, so the owning scope is restored only at this boundary.
func semanticObservationValuePath(observation catalog.SemanticObservation) string {
	valuePath := canonicalInventoryPath(observation.Value.Selector)
	ownerPath := canonicalInventoryPath(observation.OwningScope)
	if valuePath == "" || ownerPath == "" || valuePath == ownerPath || strings.HasPrefix(valuePath, ownerPath+".") {
		return valuePath
	}
	return ownerPath + "." + valuePath
}

func canonicalInventoryPath(raw string) string {
	path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "root."))
	if path == "" {
		return ""
	}
	segments := strings.Split(path, ".")
	for index := range segments {
		segments[index] = strings.TrimSpace(segments[index])
		if segments[index] == "" {
			return ""
		}
	}
	return strings.Join(segments, ".")
}

func compilerProofUnavailable() authoringv2.SemanticSelectionReadiness {
	return authoringv2.SemanticSelectionReadiness{
		Status: authoringv2.SemanticReadinessUnsupported, Code: "COMPILER_PROOF_UNAVAILABLE",
		Message: "The exact semantic value path has no matching compiler-proved capability field.",
	}
}

func semanticInventoryContextToken(snapshot capability.Snapshot, explorerID, rowRoot, buildID string) (string, error) {
	identity, err := json.Marshal([]string{"semantic-browse/v1", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, rowRoot, "all-authorized", buildID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}
