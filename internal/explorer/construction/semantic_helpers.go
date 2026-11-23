package construction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

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
	document authoringv2.Document,
	outputID, contextToken, buildID string,
	entries []semantic.Entry,
	families []semantic.Family,
) (map[string]capability.ConstructionChoice, error) {
	choices := make(map[string]capability.ConstructionChoice)
	byNode := make(map[string][]semanticInventoryRouteCandidate)
	targets := make(map[string]struct{})
	for _, entry := range entries {
		if !authoringv2.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			continue
		}
		candidate, ok := semanticConstructionCandidate(snapshot, entry.Observation)
		if !ok || candidate.NodeID == "" {
			continue
		}
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		byNode[candidate.NodeID] = append(byNode[candidate.NodeID], semanticInventoryRouteCandidate{key: key, entry: entry, candidate: candidate})
		targets[candidate.NodeID] = struct{}{}
	}
	for index := range families {
		family := families[index]
		entry := semanticInventoryEntryForFamily(family)
		if !authoringv2.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			continue
		}
		candidate, ok := semanticConstructionCandidate(snapshot, entry.Observation)
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
			return nil, internal("catalog", "CONSTRUCTION_ROUTE_SEARCH_FAILED", "the table's semantic route scope could not be encoded", err)
		}
		sourceDigest := sha256.Sum256(keyBytes)
		sourceKey := "semantic-inventory:" + hex.EncodeToString(sourceDigest[:])
		cursor := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, unavailable("catalog", "CONSTRUCTION_ROUTE_SEARCH_INTERRUPTED", "the table's semantic route search was interrupted", err)
			}
			page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
				Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: targetNodeID,
				SourceKey: sourceKey, Cursor: cursor, Limit: capability.ConstructionRouteMaxLimit,
			})
			if err != nil {
				return nil, internal("catalog", "CONSTRUCTION_ROUTE_SEARCH_FAILED", "the table's semantic routes could not be planned", err)
			}
			for _, route := range page.Routes {
				for _, routeCandidate := range routeCandidates {
					if _, resolved := choices[routeCandidate.key]; resolved {
						continue
					}
					resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, route)
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
								return nil, unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the table's semantic routes was interrupted", err)
							}
						}
						proofCache[proofKey] = proof
					}
					if !proof.ok {
						continue
					}
					ownerRecordsProved := routeCandidate.family == nil && proveOwnerRecordsForRoute(ctx, authorized, document.RootResourceType, routeCandidate.entry.Observation, proof.route)
					if err := ctx.Err(); err != nil {
						return nil, unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the table's semantic routes was interrupted", err)
					}
					var choice capability.ConstructionChoice
					var choiceErr error
					if routeCandidate.family != nil {
						choice, choiceErr = semanticCategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, proof.route, *routeCandidate.family, proof.candidate)
					} else {
						choice, choiceErr = semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, proof.route, routeCandidate.entry, proof.candidate, ownerRecordsProved)
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

func semanticInventoryConstructionChoiceForRoute(snapshot capability.Snapshot, contextToken, buildID string, route []capability.ConstructionRouteStep, entry semantic.Entry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	observation := entry.Observation
	if observation.RuleHint == semantic.RuleHintCodedConceptV1 {

		ownerRecordsProved = false
	}
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

func semanticInventoryEntryForFamily(family semantic.Family) semantic.Entry {
	return semantic.Entry{
		ConceptID: family.FamilyID, BindingID: family.BindingID,
		Observation: semantic.Observation{
			SchemaVersion: family.SchemaVersion,
			Source:        family.Source,
			Key: semantic.Key{
				Selector: family.KeySelector, System: family.System, Version: family.Version,
			},
			Value:             family.Value,
			OwningScope:       family.OwningScope,
			LogicalType:       family.LogicalType,
			Completeness:      family.Completeness,
			Status:            family.Status,
			Population:        family.Population,
			Examples:          append([]string(nil), family.Examples...),
			ExamplesTruncated: family.ExamplesTruncated,
			RuleHint:          semantic.RuleHintCodedCategoryV1,
			RuleVersion:       family.RuleVersion,
		},
	}
}

func semanticCategoryConstructionChoiceForRoute(
	snapshot capability.Snapshot,
	contextToken, buildID string,
	route []capability.ConstructionRouteStep,
	family semantic.Family,
	candidate capability.Candidate,
) (capability.ConstructionChoice, error) {
	entry := semanticInventoryEntryForFamily(family)
	observation := entry.Observation
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return capability.ConstructionChoice{}, errors.New("coded category has no complete system-version compiler binding")
	}
	projectedCandidate, ok := semanticCandidateWithFallback(snapshot, observation, candidate)
	if !ok {
		return capability.ConstructionChoice{}, errors.New("coded category fallback value selectors have no exact compiler-proved candidates")
	}
	source := capability.SemanticBindingChoiceSource{
		ConceptID: family.FamilyID, BindingID: family.BindingID, SourceKind: family.SourceKind,
		ResourceType: family.Source.Type, SourcePath: family.Source.Path,
		SourceCanonical: family.Source.Canonical, SourceProfile: family.Source.Profile,
		FieldPath: candidate.FieldPath, OwningScope: family.OwningScope,
		KeySelector: family.KeySelector, System: family.System, Version: family.Version,
		ValueSelector:         family.Value.Selector,
		ValueFallbacks:        append([]string(nil), family.Value.FallbackSelectors...),
		FallbackOnBlankString: family.Value.FallbackOnBlankString,
		LogicalType:           firstNonEmpty(family.LogicalType, family.Value.Type),
		RuleHint:              semantic.RuleHintCodedCategoryV1, RuleVersion: family.RuleVersion,
		SchemaVersion: family.SchemaVersion,
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, route, source, projectedCandidate)
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

func semanticEntryMatchesFrame(entry semantic.Entry, frame authoringv2.FrameDefinition) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, frame.Source)
	return ok
}

func semanticEntryMatchesFrameFamily(entry semantic.Entry, family capability.SemanticFrameFamily) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, family)
	return ok
}

func semanticFrameFamilyMatchesPinnedPrior(current, pinned capability.SemanticFrameFamily, observation semantic.Observation) bool {
	return current.SchemaVersion == semantic.ObservationSchemaVersion &&
		current.RuleVersion == strconv.Itoa(semantic.SemanticObservationRuleVersion) &&
		pinned.SchemaVersion == semantic.SemanticObservationLegacyV1SchemaVersion &&
		pinned.RuleVersion == strconv.Itoa(semantic.SemanticObservationLegacyV1RuleVersion) &&
		len(pinned.ValueFallbacks) == 0 && !pinned.FallbackOnBlankString &&
		pinned.BindingID == semantic.LegacySemanticBindingIDV1(observation) &&
		semanticEntrySourceMatchesFrameFamily(observation, pinned) &&
		current.ResourceType == pinned.ResourceType &&
		current.SourcePath == pinned.SourcePath && current.SourceCanonical == pinned.SourceCanonical &&
		current.SourceProfile == pinned.SourceProfile && current.OwningScope == pinned.OwningScope &&
		current.KeyPath == pinned.KeyPath && current.ValuePath == pinned.ValuePath &&
		current.LogicalType == pinned.LogicalType && reflect.DeepEqual(current.ChoiceArms, pinned.ChoiceArms)
}

func semanticEntryForFrameFamily(
	entry semantic.Entry,
	family capability.SemanticFrameFamily,
) (semantic.Entry, authoringv2.SemanticSelectionPlan, bool) {
	observation := entry.Observation
	if observation.RuleHint != semantic.RuleHintCodedValueV1 || !semanticEntrySourceMatchesFrameFamily(observation, family) || observation.Key.Version != "" {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}

	currentRuleVersion := strconv.Itoa(semantic.SemanticObservationRuleVersion)
	if observation.SchemaVersion != semantic.ObservationSchemaVersion || observation.RuleVersion != currentRuleVersion {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	currentFamily := family.SchemaVersion == observation.SchemaVersion && family.RuleVersion == observation.RuleVersion && family.BindingID == entry.BindingID
	priorFamily := isPriorPrimaryOnlySemanticFrameFamily(family) &&
		family.BindingID == semantic.LegacySemanticBindingIDV1(observation)
	if !currentFamily && !priorFamily {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	if priorFamily {
		observation.Value.FallbackSelectors = nil
		observation.Value.FallbackOnBlankString = false
		entry.Observation = observation
	}

	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue ||
		plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	binding := plan.Source.Lookup.Binding
	if binding.OwnerPath != family.OwningScope || binding.KeyPath != family.KeyPath || binding.ValuePath != family.ValuePath ||
		!slices.Equal(binding.ValueFallback, family.ValueFallbacks) || binding.FallbackOnBlankString != family.FallbackOnBlankString ||
		binding.LogicalType != family.LogicalType || !reflect.DeepEqual(binding.ChoiceArms, family.ChoiceArms) {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
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
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
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

func semanticConstructionCandidate(snapshot capability.Snapshot, observation semantic.Observation) (capability.Candidate, bool) {
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

func semanticCandidateWithFallback(snapshot capability.Snapshot, observation semantic.Observation, primary capability.Candidate) (capability.Candidate, bool) {
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

func semanticObservationValuePath(observation semantic.Observation) string {
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
