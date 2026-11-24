package semanticbrowse

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
	"unicode"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

type semanticInventoryItemIdentity interface {
	semanticInventoryItemIdentity()
}

type ExactSemanticInventoryItemIdentity struct {
	Kind      SemanticInventoryItemIdentityKind `json:"kind"`
	ConceptID string                            `json:"conceptId"`
	BindingID string                            `json:"bindingId"`
}

func (ExactSemanticInventoryItemIdentity) semanticInventoryItemIdentity() {}

type FamilySemanticInventoryItemIdentity struct {
	Kind      SemanticInventoryItemIdentityKind `json:"kind"`
	FamilyID  string                            `json:"familyId"`
	BindingID string                            `json:"bindingId"`
}

func (FamilySemanticInventoryItemIdentity) semanticInventoryItemIdentity() {}

type BrowseSemanticInventoryResponse struct {
	FrameID            string                  `json:"frameId,omitempty"`
	FrameSource        *ws.FrameDefinition     `json:"frameSource,omitempty"`
	ContextToken       string                  `json:"contextToken"`
	BuildID            string                  `json:"buildId"`
	State              semantic.State          `json:"state"`
	SourceAvailability string                  `json:"sourceAvailability"`
	Entries            []SemanticInventoryItem `json:"entries"`
	NextCursor         string                  `json:"nextCursor,omitempty"`
	FieldPresentation  FieldPresentationResult `json:"fieldPresentation,omitempty"`
}

type semanticBrowseCursor struct {
	Context        string `json:"context"`
	Page           string `json:"page"`
	SelectionMode  string `json:"selectionMode,omitempty"`
	FrameID        string `json:"frameId,omitempty"`
	SourceChoiceID string `json:"sourceChoiceId,omitempty"`
	OutputID       string `json:"outputId,omitempty"`
}

type semanticBrowseSourceSelection struct {
	frame         *ws.FrameDefinition
	codedFamily   *ports.FrameSourceFamilyCandidate
	codedRoute    []capability.ConstructionRouteStep
	bindingFilter string
	resourceType  string
}

func semanticBrowseCursorSelectionMode(mode string) string {
	if mode == "" {
		return string(semantic.ExactConcept)
	}
	return mode
}

func semanticInventoryChoicesForExactOccurrence(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType string,
	contextToken, buildID string,
	entries []semantic.Entry,
	families []semantic.Family,
	sourceNodeID string,
	route []capability.ConstructionRouteStep,
	proofCache map[string]semanticCandidateRouteProof,
) (map[string]capability.ConstructionChoice, error) {
	choices := make(map[string]capability.ConstructionChoice)
	add := func(entry semantic.Entry, family *semantic.Family) error {
		if !semanticauthoring.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			return nil
		}
		candidate, found := capability.SemanticConstructionCandidate(snapshot, entry.Observation)
		if !found || candidate.NodeID != sourceNodeID || !candidate.Populated {
			return nil
		}
		proof, err := proveSemanticCandidateForRoute(ctx, authorized, rootResourceType, candidate, route, proofCache)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return support.Unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the exact field-presentation route was interrupted", err)
			}
			return support.Internal("catalog", "CONSTRUCTION_ROUTE_PROOF_FAILED", "compiler proof for the exact field-presentation route failed", err)
		}
		fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, proof.candidate)
		if err != nil || len(fieldChoice.Options) == 0 {
			return nil
		}
		var choice capability.ConstructionChoice
		ownerRecordsProved := family == nil && proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, entry.Observation, route)
		if family != nil {
			choice, err = semanticauthoring.CategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, route, *family, proof.candidate)
		} else {
			choice, err = capability.SemanticConstructionChoiceForRoute(snapshot, contextToken, buildID, route, entry, proof.candidate, ownerRecordsProved)
		}
		if err == nil && len(choice.Options) > 0 && capability.ConstructionChoiceFieldOptionsMatch(choice, fieldChoice) {
			choices[semanticChoiceKey(entry.ConceptID, entry.BindingID)] = choice
		}
		return nil
	}
	for _, entry := range entries {
		if err := add(entry, nil); err != nil {
			return nil, err
		}
	}
	for index := range families {
		family := families[index]
		if err := add(semantic.EntryForFamily(family), &family); err != nil {
			return nil, err
		}
	}
	return choices, nil
}

func proveSemanticCandidateForRoute(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	rootResourceType string,
	candidate capability.Candidate,
	route []capability.ConstructionRouteStep,
	cache map[string]semanticCandidateRouteProof,
) (semanticCandidateRouteProof, error) {
	key := semanticCandidateRouteProofCacheKey(candidate, route)
	if proof, ok := cache[key]; ok {
		return proof, nil
	}
	proof := semanticCandidateRouteProof{candidate: candidate, route: capability.CloneConstructionRoute(route), ok: true}
	if len(route) > 0 || strings.HasPrefix(candidate.ID, "schema_field.") {
		proven, err := proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
		if err != nil {
			return semanticCandidateRouteProof{}, err
		}
		proof.candidate = proven
	}
	cache[key] = proof
	return proof, nil
}

func semanticCoverageCandidatePathKey(path semanticarango.SemanticCoverageCandidatePath) string {
	return path.ResourceType + "\x00" + path.CanonicalPath
}

func semanticInventoryItemForPresentation(
	entry semantic.Entry,
	family *semantic.Family,
	candidate capability.Candidate,
	choice capability.ConstructionChoice,
) SemanticInventoryItem {
	observation := entry.Observation
	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	item := SemanticInventoryItem{
		ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
		SourcePath: observation.Source.Path, System: observation.Key.System, Code: observation.Key.Code,
		CodingVersion: observation.Key.Version, Display: observation.Key.Display,
		ValueSelector: observation.Value.Selector, ValueType: observation.Value.Type,
		OwningScope: observation.OwningScope, Occurrences: observation.Population,
		Examples: append([]string(nil), observation.Examples...), ExamplesTruncated: observation.ExamplesTruncated,
		ObservedUnits: append([]string(nil), observation.ObservedUnits...), ObservedUnitsTruncated: observation.ObservedUnitsTruncated,
		Completeness: observation.Completeness, Readiness: plan.Readiness, ConstructionChoice: &choice,
	}
	if family != nil {
		item.SelectionMode = string(semantic.SystemVersionCategory)
		item.FamilyID = family.FamilyID
		item.Display = semanticCategoryLabel(*family, candidate)
		item.Code = ""
	} else if observation.RuleHint == "IDENTIFIER_SYSTEM_VALUE" {
		item.Display = semanticIdentifierLabel(observation.Key.System)
	}
	return item
}

type semanticInventoryRouteCandidate struct {
	key       string
	entry     semantic.Entry
	family    *semantic.Family
	candidate capability.Candidate
}

type semanticCandidateRouteProof struct {
	candidate capability.Candidate
	route     []capability.ConstructionRouteStep
	ok        bool
}

func semanticInventoryChoicesForOutput(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	document ws.Document,
	outputID, contextToken, buildID string,
	entries []semantic.Entry,
	families []semantic.Family,
) (map[string]capability.ConstructionChoice, error) {
	choices := make(map[string]capability.ConstructionChoice)
	byNode := make(map[string][]semanticInventoryRouteCandidate)
	targets := make(map[string]struct{})
	for _, entry := range entries {
		if !semanticauthoring.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			continue
		}
		candidate, ok := capability.SemanticConstructionCandidate(snapshot, entry.Observation)
		if !ok || candidate.NodeID == "" {
			continue
		}
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		byNode[candidate.NodeID] = append(byNode[candidate.NodeID], semanticInventoryRouteCandidate{key: key, entry: entry, candidate: candidate})
		targets[candidate.NodeID] = struct{}{}
	}
	for index := range families {
		family := families[index]
		entry := semantic.EntryForFamily(family)
		if !semanticauthoring.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			continue
		}
		candidate, ok := capability.SemanticConstructionCandidate(snapshot, entry.Observation)
		if !ok || candidate.NodeID == "" {
			continue
		}
		key := semanticChoiceKey(family.FamilyID, family.BindingID)
		familyCopy := family
		byNode[candidate.NodeID] = append(byNode[candidate.NodeID], semanticInventoryRouteCandidate{key: key, entry: entry, family: &familyCopy, candidate: candidate})
		targets[candidate.NodeID] = struct{}{}
	}
	if len(targets) == 0 {
		return choices, nil
	}
	targetNodeIDs := make([]string, 0, len(targets))
	for targetNodeID := range targets {
		targetNodeIDs = append(targetNodeIDs, targetNodeID)
	}
	sort.Strings(targetNodeIDs)
	reachable := semanticInventoryReachableNodes(snapshot, document.RootResourceType)
	reachableTargets := targetNodeIDs[:0]
	for _, targetNodeID := range targetNodeIDs {
		if reachable[targetNodeID] {
			reachableTargets = append(reachableTargets, targetNodeID)
		} else {
			delete(byNode, targetNodeID)
		}
	}
	targetNodeIDs = reachableTargets
	if len(targetNodeIDs) == 0 {
		return choices, nil
	}
	proofCache := make(map[string]semanticCandidateRouteProof)
	for _, targetNodeID := range targetNodeIDs {
		routeCandidates := byNode[targetNodeID]
		keyBytes, err := json.Marshal(struct {
			OutputID string
			Snapshot string
			Target   string
		}{OutputID: outputID, Snapshot: snapshot.Token, Target: targetNodeID})
		if err != nil {
			return nil, support.Internal("catalog", "CONSTRUCTION_ROUTE_SEARCH_FAILED", "the table's semantic route scope could not be encoded", err)
		}
		sourceDigest := sha256.Sum256(keyBytes)
		sourceKey := "semantic-inventory:" + hex.EncodeToString(sourceDigest[:])
		cursor := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, support.Unavailable("catalog", "CONSTRUCTION_ROUTE_SEARCH_INTERRUPTED", "the table's semantic route search was interrupted", err)
			}
			page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
				Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: targetNodeID,
				SourceKey: sourceKey, Cursor: cursor, Limit: capability.ConstructionRouteMaxLimit,
			})
			if err != nil {
				return nil, support.Internal("catalog", "CONSTRUCTION_ROUTE_SEARCH_FAILED", "the table's semantic routes could not be planned", err)
			}
			for _, route := range page.Routes {
				for _, routeCandidate := range routeCandidates {
					if _, resolved := choices[routeCandidate.key]; resolved {
						continue
					}
					resolvedRoute, routeErr := support.ReauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, route)
					if routeErr != nil {
						continue
					}
					proofKey := semanticCandidateRouteProofCacheKey(routeCandidate.candidate, resolvedRoute)
					proof, cached := proofCache[proofKey]
					if !cached {
						proof = semanticCandidateRouteProof{candidate: routeCandidate.candidate, route: resolvedRoute, ok: true}
						if len(resolvedRoute) > 0 || strings.HasPrefix(routeCandidate.candidate.ID, "schema_field.") {
							proof.candidate, err = proveConstructionCandidate(ctx, authorized, document.RootResourceType, routeCandidate.candidate, resolvedRoute)
							proof.ok = err == nil
							if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil) {
								return nil, support.Unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the table's semantic routes was interrupted", err)
							}
						}
						proofCache[proofKey] = proof
					}
					if !proof.ok {
						continue
					}
					ownerRecordsProved := routeCandidate.family == nil && proveOwnerRecordsForRoute(ctx, authorized, document.RootResourceType, routeCandidate.entry.Observation, proof.route)
					if err := ctx.Err(); err != nil {
						return nil, support.Unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the table's semantic routes was interrupted", err)
					}
					var choice capability.ConstructionChoice
					var choiceErr error
					if routeCandidate.family != nil {
						choice, choiceErr = semanticauthoring.CategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, proof.route, *routeCandidate.family, proof.candidate)
					} else {
						choice, choiceErr = capability.SemanticConstructionChoiceForRoute(snapshot, contextToken, buildID, proof.route, routeCandidate.entry, proof.candidate, ownerRecordsProved)
					}
					if choiceErr == nil {
						choices[routeCandidate.key] = choice
					}
				}
			}
			allResolved := true
			for _, candidate := range routeCandidates {
				if _, ok := choices[candidate.key]; !ok {
					allResolved = false
					break
				}
			}
			if allResolved || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	return choices, nil
}

func semanticInventoryReachableNodes(snapshot capability.Snapshot, rootResourceType string) map[string]bool {
	adjacency := make(map[string][]string)
	for _, edge := range snapshot.Edges {
		if edge.BlockedReason != "" || edge.ID == "" || edge.FromNodeID == "" || edge.ToNodeID == "" || edge.Label == "" {
			continue
		}
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		if fromOK && toOK && from.ResourceType == edge.SourceResourceType && to.ResourceType == edge.TargetResourceType {
			adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge.ToNodeID)
		}
	}
	queue := make([]string, 0)
	reachable := make(map[string]bool)
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType && !reachable[node.ID] {
			reachable[node.ID] = true
			queue = append(queue, node.ID)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, targetNodeID := range adjacency[queue[head]] {
			if !reachable[targetNodeID] {
				reachable[targetNodeID] = true
				queue = append(queue, targetNodeID)
			}
		}
	}
	return reachable
}

func semanticCandidateRouteProofCacheKey(candidate capability.Candidate, route []capability.ConstructionRouteStep) string {
	identity, _ := json.Marshal(struct {
		Candidate capability.Candidate
		Route     []capability.ConstructionRouteStep
	}{Candidate: candidate, Route: route})
	return string(identity)
}

func semanticCategoryLabel(family semantic.Family, candidate capability.Candidate) string {
	if label := semanticSystemTailLabel(family.System); label != "" {
		return label
	}
	path := strings.TrimSpace(family.Source.Path)
	resourcePrefix := strings.TrimSpace(family.Source.Type) + "."
	path = strings.TrimPrefix(path, resourcePrefix)
	path = strings.TrimSuffix(path, ".coding[]")
	path = strings.TrimSuffix(path, "coding[]")
	path = strings.Trim(path, ".[]")
	if path == "" {
		path = strings.TrimSpace(candidate.Label)
	}
	if path == "" {
		return family.Source.Type
	}
	parts := strings.Split(path, ".")
	field := semanticHumanizeLabel(parts[len(parts)-1])
	if field == "" {
		field = semanticHumanizeLabel(path)
	}
	if field == "" {
		return family.Source.Type
	}
	if field != "" {
		field = strings.ToLower(field[:1]) + field[1:]
	}
	return strings.TrimSpace(family.Source.Type + " " + field)
}

func semanticIdentifierLabel(system string) string {
	if strings.TrimSpace(system) == "" {
		return "Identifier"
	}
	return "Identifier · " + strings.TrimSpace(system)
}

func semanticSystemTailLabel(system string) string {
	path := strings.TrimRight(strings.TrimSpace(system), "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		path = path[index+1:]
	}
	if !strings.ContainsAny(path, "_-") {
		return ""
	}
	return semanticHumanizeLabel(path)
}

func semanticHumanizeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	words := make([]string, 0)
	for _, raw := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		if raw == "" {
			continue
		}
		if raw == strings.ToUpper(raw) {
			words = append(words, strings.ToLower(raw))
			continue
		}
		runes := []rune(raw)
		start := 0
		for index := 1; index < len(runes); index++ {
			previous, current := runes[index-1], runes[index]
			if !unicode.IsUpper(current) {
				continue
			}
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || (unicode.IsUpper(previous) && nextLower) {
				words = append(words, strings.ToLower(string(runes[start:index])))
				start = index
			}
		}
		words = append(words, strings.ToLower(string(runes[start:])))
	}
	if len(words) == 0 {
		return ""
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

func semanticEntryMatchesFrame(entry semantic.Entry, frame ws.FrameDefinition) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, frame.Source)
	return ok
}

func semanticEntryMatchesFrameFamily(entry semantic.Entry, family capability.SemanticFrameFamily) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, family)
	return ok
}

func semanticEntryForFrameFamily(
	entry semantic.Entry,
	family capability.SemanticFrameFamily,
) (semantic.Entry, semanticauthoring.SemanticSelectionPlan, bool) {
	observation := entry.Observation
	if observation.RuleHint != semantic.RuleHintCodedValueV1 || !semanticEntrySourceMatchesFrameFamily(observation, family) || observation.Key.Version != "" {
		return semantic.Entry{}, semanticauthoring.SemanticSelectionPlan{}, false
	}

	currentRuleVersion := strconv.Itoa(semantic.SemanticObservationRuleVersion)
	if observation.SchemaVersion != semantic.ObservationSchemaVersion || observation.RuleVersion != currentRuleVersion {
		return semantic.Entry{}, semanticauthoring.SemanticSelectionPlan{}, false
	}
	currentFamily := family.SchemaVersion == observation.SchemaVersion && family.RuleVersion == observation.RuleVersion && family.BindingID == entry.BindingID
	priorFamily := isPriorPrimaryOnlySemanticFrameFamily(family) &&
		family.BindingID == semantic.LegacySemanticBindingIDV1(observation)
	if !currentFamily && !priorFamily {
		return semantic.Entry{}, semanticauthoring.SemanticSelectionPlan{}, false
	}
	if priorFamily {
		observation.Value.FallbackSelectors = nil
		observation.Value.FallbackOnBlankString = false
		entry.Observation = observation
	}

	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != ws.SourceCodedValue ||
		plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return semantic.Entry{}, semanticauthoring.SemanticSelectionPlan{}, false
	}
	binding := plan.Source.Lookup.Binding
	if binding.OwnerPath != family.OwningScope || binding.KeyPath != family.KeyPath || binding.ValuePath != family.ValuePath ||
		!slices.Equal(binding.ValueFallback, family.ValueFallbacks) || binding.FallbackOnBlankString != family.FallbackOnBlankString ||
		binding.LogicalType != family.LogicalType || !reflect.DeepEqual(binding.ChoiceArms, family.ChoiceArms) {
		return semantic.Entry{}, semanticauthoring.SemanticSelectionPlan{}, false
	}
	return entry, plan, true
}

func isPriorPrimaryOnlySemanticFrameFamily(family capability.SemanticFrameFamily) bool {
	return family.SchemaVersion == semantic.SemanticObservationLegacyV1SchemaVersion &&
		family.RuleVersion == strconv.Itoa(semantic.SemanticObservationLegacyV1RuleVersion) &&
		len(family.ValueFallbacks) == 0 && !family.FallbackOnBlankString
}

func semanticEntrySourceMatchesFrameFamily(observation semantic.Observation, family capability.SemanticFrameFamily) bool {
	return observation.Source.Type == family.ResourceType &&
		observation.Source.Path == family.SourcePath && observation.Source.Canonical == family.SourceCanonical &&
		observation.Source.Profile == family.SourceProfile
}

func proveOwnerRecordsForRoute(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, observation semantic.Observation, route []capability.ConstructionRouteStep) bool {
	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != ws.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return false
	}
	compilerRoute := make([]facade.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		compilerRoute = append(compilerRoute, facade.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode})
	}
	_, err := facade.ProbeOwnerRecords(ctx, facade.OwnerRecordsRequest{
		Scope: facade.Scope{
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

func compilerProofUnavailable() semanticauthoring.SemanticSelectionReadiness {
	return semanticauthoring.SemanticSelectionReadiness{
		Status: semanticauthoring.SemanticReadinessUnsupported, Code: "COMPILER_PROOF_UNAVAILABLE",
		Message: "The exact semantic value path has no matching compiler-proved capability field.",
	}
}

func SemanticInventoryContextToken(snapshot capability.Snapshot, explorerID, rowRoot, buildID string) (string, error) {
	identity, err := json.Marshal([]string{"semantic-browse/v1", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, rowRoot, "all-authorized", buildID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) BrowseSemanticInventory(ctx context.Context, req BrowseSemanticInventoryRequest) (BrowseSemanticInventoryResponse, error) {

	var result BrowseSemanticInventoryResponse
	if req.FieldPresentation != nil {
		if err := req.FieldPresentation.Validate(); err != nil {
			return result, support.Malformed("catalog", err.Error(), err)
		}
		if req.OutputID == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return result, support.Malformed("catalog", "outputId is required and must be exact with fieldPresentation", nil)
		}
		if req.FrameID != "" || req.SourceChoiceID != "" {
			return result, support.Malformed("catalog", "fieldPresentation cannot be combined with frameId or sourceChoiceId", nil)
		}
	}
	if req.SelectionMode == "" {
		req.SelectionMode = string(semantic.ExactConcept)
	}
	if req.SelectionMode != string(semantic.ExactConcept) && req.SelectionMode != string(semantic.SystemVersionCategory) {
		return result, support.Malformed("catalog", "selectionMode must be EXACT_CONCEPT or SYSTEM_VERSION_CATEGORY", nil)
	}
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || req.SnapshotToken == "" || req.RowRoot == "" || req.Limit < 0 || req.Limit > semantic.PageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
		return result, support.Malformed("catalog", "project, explorer, snapshotToken, rowRoot, and a page limit of at most 50 are required", nil)
	}
	if s.semanticInventory == nil || s.capability.ForCompilation == nil {
		return result, support.Unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory browsing is not configured", nil)
	}
	authorized, err := s.capability.ForCompilation(ctx, req.Project, req.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, support.Conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, err)
	}
	if err != nil {
		return result, authoringcore.CapabilityResolutionFailure("catalog", "reload the catalog before browsing", err)
	}
	snapshot := authorized.Snapshot
	if snapshot.ValidateToken(req.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(req.Project) || snapshot.Identity.Generation == "" {
		return result, support.Conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, nil)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, support.Conflict("catalog", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	rootAllowed := false
	resourceAllowed := req.ResourceType == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == req.RowRoot && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == req.ResourceType
	}
	if !rootAllowed || !resourceAllowed {
		return result, support.Malformed("catalog", "the row root or resource filter is not available in this snapshot", nil)
	}
	if req.FrameID != "" && req.SourceChoiceID != "" {
		return result, support.Malformed("catalog", "frameId and sourceChoiceId are mutually exclusive", nil)
	}
	if req.SelectionMode == string(semantic.SystemVersionCategory) && (req.FrameID != "" || req.SourceChoiceID != "") {
		return result, support.Malformed("catalog", "system-version category browsing is available only outside a saved frame or coded source selection", nil)
	}
	sourceSelection, err := s.resolveSemanticBrowseSource(ctx, req, authorized, snapshot)
	if err != nil {
		return result, err
	}
	var outputDocument *ws.Document
	var presentationCurrent *fieldPresentationCurrentContext
	frame := sourceSelection.frame
	codedFamily := sourceSelection.codedFamily
	codedRoute := sourceSelection.codedRoute
	bindingFilter := sourceSelection.bindingFilter
	req.ResourceType = sourceSelection.resourceType
	if frame != nil {
		result.FrameID, result.FrameSource = frame.ID, frame
	}

	if req.OutputID != "" && req.OutputID != strings.TrimSpace(req.OutputID) {
		return result, support.Malformed("catalog", "outputId must be an exact identifier", nil)
	}
	if req.OutputID != "" && frame == nil && codedFamily == nil {
		if req.FieldPresentation != nil {
			current, currentErr := s.fieldPresentationCurrentContext(ctx, req, snapshot)
			if currentErr != nil {
				return result, currentErr
			}
			presentationCurrent = &current
			document := current.document
			outputDocument = &document
		} else {
			workspace, workspaceErr := s.core.CurrentWorkspace(ctx, "construction-choices", req.Project, req.ExplorerID)
			if workspaceErr != nil {
				return result, workspaceErr
			}
			document := workspace.FindOutputDocument(req.OutputID)
			if document == nil || document.RootResourceType != req.RowRoot {
				return result, support.NotFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
			}
			outputDocument = document
		}
	}
	var cursor semanticBrowseCursor
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Context == "" || cursor.Page == "" || semanticBrowseCursorSelectionMode(cursor.SelectionMode) != req.SelectionMode || cursor.FrameID != req.FrameID || cursor.SourceChoiceID != req.SourceChoiceID || cursor.OutputID != req.OutputID {
			return result, support.Conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, nil)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	page, err := s.semanticInventory(ctx, semantic.PageOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: authorized.Scope.AuthResourcePaths,
		ResourceType: req.ResourceType, BindingID: bindingFilter, Query: req.Query, Cursor: cursor.Page, Limit: req.Limit,
		SelectionMode: semantic.SelectionMode(req.SelectionMode),
	})
	if errors.Is(err, semantic.ErrSemanticInventoryCursorMismatch) {
		return result, support.Conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, err)
	}
	if err != nil {
		return result, support.PersistenceFailure("catalog", "semantic inventory could not be read", err)
	}
	result.ContextToken, err = SemanticInventoryContextToken(snapshot, req.ExplorerID, req.RowRoot, page.Build.BuildID)
	if err != nil {
		return result, err
	}
	if req.Cursor != "" && cursor.Context != result.ContextToken {
		return BrowseSemanticInventoryResponse{}, support.Conflict("catalog", "STALE_CATALOG_CURSOR", "catalog context changed; restart search", nil, nil)
	}
	result.BuildID, result.State = page.Build.BuildID, page.State
	result.SourceAvailability = page.Build.SourceAvailability
	if result.SourceAvailability == "" {
		result.SourceAvailability = semantic.SourceAvailabilityUnknown
	}
	categoryEntries := make([]semantic.Entry, 0, len(page.Families))
	familyByChoice := make(map[string]semantic.Family, len(page.Families))
	for _, family := range page.Families {
		entry := semantic.EntryForFamily(family)
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		categoryEntries = append(categoryEntries, entry)
		familyByChoice[key] = family
	}
	entries := make([]semantic.Entry, 0, len(page.Entries)+len(categoryEntries))
	entries = append(entries, page.Entries...)
	entries = append(entries, categoryEntries...)
	var outputChoices map[string]capability.ConstructionChoice
	if outputDocument != nil {
		if presentationCurrent != nil {
			outputChoices, result.FieldPresentation, err = s.semanticInventoryPresentationForOutput(
				semanticInventoryPresentationRequest{
					ctx: ctx, authorized: authorized, snapshot: snapshot, document: *outputDocument,
					outputID: req.OutputID, contextToken: result.ContextToken, build: page.Build,
					entries: page.Entries, families: page.Families, current: *presentationCurrent,
					request: *req.FieldPresentation,
				},
			)
		} else {
			outputChoices, err = semanticInventoryChoicesForOutput(ctx, authorized, snapshot, *outputDocument, req.OutputID, result.ContextToken, result.BuildID, page.Entries, page.Families)
		}
		if err != nil {
			return result, err
		}
	}
	result.Entries = make([]SemanticInventoryItem, 0, len(entries))
	for _, entry := range entries {
		observation := entry.Observation
		family, isCategory := familyByChoice[semanticChoiceKey(entry.ConceptID, entry.BindingID)]
		plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
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
		if observation.RuleHint == "IDENTIFIER_SYSTEM_VALUE" {
			item.Display = semanticIdentifierLabel(observation.Key.System)
		}
		if isCategory {
			item.SelectionMode = string(semantic.SystemVersionCategory)
			item.FamilyID = family.FamilyID
			item.Display = semanticCategoryLabel(family, capability.Candidate{})
			item.Code = ""
			candidate, ok := capability.SemanticConstructionCandidate(snapshot, observation)
			if !ok {
				item.Readiness = compilerProofUnavailable()
			} else {
				item.Display = semanticCategoryLabel(family, candidate)
			}
		}
		if outputDocument != nil {
			choice, ok := outputChoices[semanticChoiceKey(entry.ConceptID, entry.BindingID)]
			if !ok {
				if presentationCurrent == nil {
					continue
				}
				result.Entries = append(result.Entries, item)
				continue
			}
			item.ConstructionChoice = &choice
			result.Entries = append(result.Entries, item)
			continue
		}
		if plan.Readiness.Addable() {
			if candidate, ok := capability.SemanticConstructionCandidate(snapshot, observation); ok {
				if codedFamily != nil && (candidate.ID != codedFamily.Candidate.ID || candidate.NodeID != codedFamily.Candidate.NodeID || candidate.FieldPath != codedFamily.Candidate.FieldPath) {
					continue
				}
				var route []capability.ConstructionRouteStep
				if frame != nil {
					if !semanticEntryMatchesFrame(entry, *frame) {
						continue
					}
					resolvedRoute, routeErr := support.ReauthorizeConstructionRoute(snapshot, req.RowRoot, candidate.NodeID, frame.Route)
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
				ownerRecordsProved := !isCategory && frame == nil && proveOwnerRecordsForRoute(ctx, authorized, req.RowRoot, observation, route)
				var choice capability.ConstructionChoice
				var choiceErr error
				if isCategory {
					choice, choiceErr = semanticauthoring.CategoryConstructionChoiceForRoute(snapshot, result.ContextToken, result.BuildID, route, family, candidate)
				} else {
					choice, choiceErr = capability.SemanticConstructionChoiceForRoute(snapshot, result.ContextToken, result.BuildID, route, entry, candidate, ownerRecordsProved)
				}
				if choiceErr == nil {
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
		raw, err := json.Marshal(semanticBrowseCursor{Context: result.ContextToken, Page: page.NextCursor, SelectionMode: req.SelectionMode, FrameID: req.FrameID, SourceChoiceID: req.SourceChoiceID, OutputID: req.OutputID})
		if err != nil {
			return BrowseSemanticInventoryResponse{}, err
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}

func (s *Service) resolveSemanticBrowseSource(ctx context.Context, req BrowseSemanticInventoryRequest, authorized ports.AuthorizedCapability, snapshot capability.Snapshot) (semanticBrowseSourceSelection, error) {
	var frame *ws.FrameDefinition
	var codedFamily *ports.FrameSourceFamilyCandidate
	var codedRoute []capability.ConstructionRouteStep
	bindingFilter := ""
	if req.FrameID != "" {
		if strings.TrimSpace(req.OutputID) == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return semanticBrowseSourceSelection{}, support.Malformed("catalog", "outputId is required with frameId", nil)
		}
		workspace, workspaceErr := s.core.CurrentWorkspace(ctx, "construction-choices", req.Project, req.ExplorerID)
		if workspaceErr != nil {
			return semanticBrowseSourceSelection{}, workspaceErr
		}
		documentIndex := -1
		for index := range workspace.Documents {
			if workspace.Documents[index].Output.ID == req.OutputID {
				documentIndex = index
				break
			}
		}
		if documentIndex < 0 || workspace.Documents[documentIndex].RootResourceType != req.RowRoot {
			return semanticBrowseSourceSelection{}, support.NotFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
		}
		for _, candidate := range workspace.Documents[documentIndex].Frames {
			if candidate.ID == req.FrameID {
				copy := candidate
				copy.Route = capability.CloneConstructionRoute(candidate.Route)
				copy.Source.ChoiceArms = append([]string(nil), candidate.Source.ChoiceArms...)
				frame = &copy
			}
		}
		if frame == nil {
			return semanticBrowseSourceSelection{}, support.NotFound("catalog", "FRAME_NOT_FOUND", "the saved frame source was not found for this row root", nil)
		}
		if req.ResourceType != "" && req.ResourceType != frame.Source.ResourceType {
			return semanticBrowseSourceSelection{}, support.Malformed("catalog", "resourceType must match the saved frame source", nil)
		}
		req.ResourceType = frame.Source.ResourceType
		bindingFilter = frame.Source.BindingID
		if isPriorPrimaryOnlySemanticFrameFamily(frame.Source) {

			bindingFilter = ""
		}
	}
	if req.SourceChoiceID != "" {
		if strings.TrimSpace(req.OutputID) == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return semanticBrowseSourceSelection{}, support.Malformed("catalog", "outputId is required with sourceChoiceId", nil)
		}
		workspace, workspaceErr := s.core.CurrentWorkspace(ctx, "construction-choices", req.Project, req.ExplorerID)
		if workspaceErr != nil {
			return semanticBrowseSourceSelection{}, workspaceErr
		}
		document := workspace.FindOutputDocument(req.OutputID)
		if document == nil || document.Route.OccurrenceID != ws.RootOccurrenceID || document.RootResourceType != req.RowRoot {
			return semanticBrowseSourceSelection{}, support.NotFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
		}
		identity, decodeErr := capability.DecodeConstructionChoiceID(req.SourceChoiceID)
		if decodeErr != nil || identity.SnapshotToken != snapshot.Token {
			return semanticBrowseSourceSelection{}, support.Conflict("catalog", "STALE_CONSTRUCTION_CHOICE", "reload the coded source for the current authorized snapshot", nil, decodeErr)
		}
		family, route, verifyErr := s.reauthorizeFrameSourceChoice(ctx, req.Project, req.ExplorerID, req.RowRoot, req.OutputID, req.SourceChoiceID, authorized, identity)
		if verifyErr != nil {
			return semanticBrowseSourceSelection{}, verifyErr
		}
		if len(route) != 0 || family.Family.ResourceType != req.RowRoot {
			return semanticBrowseSourceSelection{}, support.Unprocessable("catalog", "CODED_PIVOT_SOURCE_UNSUPPORTED", "coded Pivot currently requires a direct root source", nil)
		}
		if req.ResourceType != "" && req.ResourceType != family.Family.ResourceType {
			return semanticBrowseSourceSelection{}, support.Malformed("catalog", "resourceType must match sourceChoiceId", nil)
		}
		req.ResourceType = family.Family.ResourceType
		bindingFilter = family.Family.BindingID
		codedFamily = &family
		codedRoute = route
	}

	return semanticBrowseSourceSelection{
		frame: frame, codedFamily: codedFamily, codedRoute: codedRoute,
		bindingFilter: bindingFilter, resourceType: req.ResourceType,
	}, nil
}

func (b *Service) resolveFieldPresentationReplacements(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType, semanticContextToken, buildID string,
	current fieldPresentationCurrentContext,
	references map[string]semanticPresentationReference,
	proofCache map[string]semanticCandidateRouteProof,
) (map[string]SemanticInventoryItem, bool, error) {
	items := make(map[string]SemanticInventoryItem, len(references))
	if len(references) == 0 {
		return items, true, nil
	}
	if b.resolveSelections == nil {
		return items, false, nil
	}
	keys := make([]string, 0, len(references))
	for key := range references {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	exactReferences := make([]semantic.Reference, 0, len(keys))
	familyReferences := make([]semantic.FamilyReference, 0, len(keys))
	for _, key := range keys {
		reference := references[key]
		if reference.exact != nil {
			exactReferences = append(exactReferences, *reference.exact)
		}
		if reference.family != nil {
			familyReferences = append(familyReferences, *reference.family)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := b.resolveSelections(ctx, semantic.ResolveOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References:                    exactReferences, FamilyReferences: familyReferences,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, false, support.Unavailable("catalog", "FIELD_PRESENTATION_REPLACEMENT_READ_INTERRUPTED", "semantic replacement resolution was interrupted", err)
		}
		return nil, false, support.PersistenceFailure("catalog", "authorized semantic replacements could not be read", err)
	}
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete || resolved.Build.BuildID != buildID {
		return items, false, nil
	}
	exactEntries := make(map[string][]semantic.Entry, len(exactReferences))
	for _, entry := range resolved.Entries {
		key := "EXACT\x00" + entry.BindingID + "\x00" + entry.ConceptID
		if _, requested := references[key]; requested {
			exactEntries[key] = append(exactEntries[key], entry)
		}
	}
	familyEntries := make(map[string][]semantic.Family, len(familyReferences))
	for _, family := range resolved.Families {
		key := "FAMILY\x00" + family.BindingID + "\x00" + family.FamilyID
		if _, requested := references[key]; requested {
			familyEntries[key] = append(familyEntries[key], family)
		}
	}
	for _, key := range keys {
		reference := references[key]
		var entry semantic.Entry
		var family *semantic.Family
		if reference.exact != nil {
			matches := exactEntries[key]
			if len(matches) != 1 {
				continue
			}
			entry = matches[0]
		} else if reference.family != nil {
			matches := familyEntries[key]
			if len(matches) != 1 {
				continue
			}
			copy := matches[0]
			family = &copy
			entry = semantic.EntryForFamily(copy)
		} else {
			continue
		}
		item, available, _, itemErr := b.semanticFieldPresentationReplacement(
			ctx, authorized, snapshot, rootResourceType, semanticContextToken, buildID,
			current, entry, family, proofCache,
		)
		if itemErr != nil {
			return nil, false, itemErr
		}
		if available {
			items[key] = item
		}
	}
	return items, true, nil
}

func (b *Service) semanticFieldPresentationReplacement(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType, semanticContextToken, buildID string,
	current fieldPresentationCurrentContext,
	entry semantic.Entry,
	family *semantic.Family,
	proofCache map[string]semanticCandidateRouteProof,
) (SemanticInventoryItem, bool, FieldPresentationReason, error) {
	observation := entry.Observation
	if !fieldPresentationRuleVisibleInCodedView(observation.RuleHint) {
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, nil
	}
	if observation.Population <= 0 {
		return SemanticInventoryItem{}, false, FieldPresentationCandidateNotPopulated, nil
	}
	if !semanticauthoring.ResolveSemanticSelectionPlan(observation).Readiness.Addable() {
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, nil
	}
	candidate, found := capability.SemanticConstructionCandidate(snapshot, observation)
	if !found || candidate.NodeID != current.sourceNodeID || !candidate.Populated {
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, nil
	}
	proof, err := proveSemanticCandidateForRoute(ctx, authorized, rootResourceType, candidate, current.route, proofCache)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, support.Unavailable("catalog", "FIELD_PRESENTATION_REPLACEMENT_PROOF_INTERRUPTED", "compiler proof for a semantic replacement was interrupted", err)
		}
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, support.Internal("catalog", "FIELD_PRESENTATION_REPLACEMENT_PROOF_FAILED", "compiler proof for a semantic replacement failed", err)
	}
	fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, current.route, proof.candidate)
	if err != nil || len(fieldChoice.Options) == 0 {
		return SemanticInventoryItem{}, false, FieldPresentationSourceFieldsInaccessible, nil
	}
	ownerRecordsProved := family == nil && proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, current.route)
	var choice capability.ConstructionChoice
	if family != nil {
		choice, err = semanticauthoring.CategoryConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, current.route, *family, proof.candidate)
	} else {
		choice, err = capability.SemanticConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, current.route, entry, proof.candidate, ownerRecordsProved)
	}
	if err != nil || !capability.ConstructionChoiceFieldOptionsMatch(choice, fieldChoice) || len(choice.Options) == 0 {
		return SemanticInventoryItem{}, false, FieldPresentationSourceFieldsInaccessible, nil
	}
	item := semanticInventoryItemForPresentation(entry, family, proof.candidate, choice)
	return item, true, "", nil
}
