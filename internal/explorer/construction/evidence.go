package construction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

func semanticCategoryConstructionChoiceForRoute(
	snapshot capability.Snapshot,
	contextToken, buildID string,
	route []capability.ConstructionRouteStep,
	family semantic.Family,
	candidate capability.Candidate,
) (capability.ConstructionChoice, error) {
	entry := semantic.EntryForFamily(family)
	observation := entry.Observation
	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return capability.ConstructionChoice{}, errors.New("coded category has no complete system-version compiler binding")
	}
	projectedCandidate, ok := capability.SemanticCandidateWithFallback(snapshot, observation, candidate)
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
		LogicalType:           loomtext.FirstTrimmed(family.LogicalType, family.Value.Type),
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
	_, ok := semanticEntryForFrameFamily(entry, frame.Source)
	return ok
}

func semanticEntryMatchesFrameFamily(entry semantic.Entry, family capability.SemanticFrameFamily) bool {
	_, ok := semanticEntryForFrameFamily(entry, family)
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
) (semantic.Entry, bool) {
	observation := entry.Observation
	if observation.RuleHint != semantic.RuleHintCodedValueV1 || !semanticEntrySourceMatchesFrameFamily(observation, family) || observation.Key.Version != "" {
		return semantic.Entry{}, false
	}

	currentRuleVersion := strconv.Itoa(semantic.SemanticObservationRuleVersion)
	if observation.SchemaVersion != semantic.ObservationSchemaVersion || observation.RuleVersion != currentRuleVersion {
		return semantic.Entry{}, false
	}
	currentFamily := family.SchemaVersion == observation.SchemaVersion && family.RuleVersion == observation.RuleVersion && family.BindingID == entry.BindingID
	priorFamily := isPriorPrimaryOnlySemanticFrameFamily(family) &&
		family.BindingID == semantic.LegacySemanticBindingIDV1(observation)
	if !currentFamily && !priorFamily {
		return semantic.Entry{}, false
	}
	if priorFamily {
		observation.Value.FallbackSelectors = nil
		observation.Value.FallbackOnBlankString = false
		entry.Observation = observation
	}

	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != ws.SourceCodedValue ||
		plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return semantic.Entry{}, false
	}
	binding := plan.Source.Lookup.Binding
	if binding.OwnerPath != family.OwningScope || binding.KeyPath != family.KeyPath || binding.ValuePath != family.ValuePath ||
		!slices.Equal(binding.ValueFallback, family.ValueFallbacks) || binding.FallbackOnBlankString != family.FallbackOnBlankString ||
		binding.LogicalType != family.LogicalType || !reflect.DeepEqual(binding.ChoiceArms, family.ChoiceArms) {
		return semantic.Entry{}, false
	}
	return entry, true
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

func semanticInventoryContextToken(snapshot capability.Snapshot, explorerID, rowRoot, buildID string) (string, error) {
	identity, err := json.Marshal([]string{"semantic-browse/v1", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, rowRoot, "all-authorized", buildID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}
