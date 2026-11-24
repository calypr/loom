package construction

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

func (s *Service) ReauthorizeFrameSourceChoice(
	ctx context.Context,
	project, explorerID, rowRoot, outputID, choiceID string,
	authorized ports.AuthorizedCapability,
	identity capability.ConstructionChoiceIdentity,
) (FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
	if identity.SnapshotToken != authorized.Snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemanticFrame {
		return FrameSourceFamilyCandidate{}, nil, support.Conflict("catalog", "STALE_CONSTRUCTION_CHOICE", "the coded source belongs to a different authorized snapshot", nil, nil)
	}
	source, ok := identity.Source.(capability.SemanticFrameChoiceSource)
	if !ok || strings.TrimSpace(outputID) == "" {
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source does not identify a correlated code/value family")
	}
	if len(identity.Route) != 0 || source.Family.ResourceType != rowRoot {
		return FrameSourceFamilyCandidate{}, nil, support.Unprocessable("catalog", "CODED_PIVOT_SOURCE_UNSUPPORTED", "coded Pivot currently requires a direct root source", nil)
	}
	entry, err := s.ResolveFrameSourceAnchor(ctx, project, explorerID, rowRoot, authorized, identity, source)
	if err != nil {
		return FrameSourceFamilyCandidate{}, nil, err
	}
	families := frameSourceFamilies(authorized.Snapshot, []semantic.Entry{entry})
	if len(families) != 1 {
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source is no longer a complete coded value family")
	}
	family := families[0]
	if !reflect.DeepEqual(family.Family, source.Family) || family.Candidate.ID != source.CandidateID ||
		family.Candidate.NodeID != source.NodeID || family.Candidate.FieldPath != source.FieldPath {
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source metadata or compiler candidate changed")
	}
	route, err := support.ReauthorizeConstructionRoute(authorized.Snapshot, rowRoot, family.Candidate.NodeID, identity.Route)
	if err != nil || len(route) != 0 {
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source route is no longer an authorized direct-root route")
	}
	proven, err := proveConstructionCandidate(ctx, authorized, rowRoot, family.Candidate, route)
	if err != nil {
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source candidate no longer compiles")
	}
	proven, ok = semanticCandidateWithFallback(authorized.Snapshot, family.Entry.Observation, proven)
	if !ok {
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source fallback selectors no longer have exact compiler-proved candidates")
	}
	contextToken, err := semanticInventoryContextToken(authorized.Snapshot, explorerID, rowRoot, identity.BuildID)
	if err != nil {
		return FrameSourceFamilyCandidate{}, nil, err
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
		return FrameSourceFamilyCandidate{}, nil, invalidFrameSource("the coded source token does not match current semantic and compiler evidence")
	}
	family.Candidate = proven
	return family, route, nil
}

func (s *Service) ResolveFrameSourceAnchor(
	ctx context.Context,
	project, explorerID, rowRoot string,
	authorized ports.AuthorizedCapability,
	identity capability.ConstructionChoiceIdentity,
	source capability.SemanticFrameChoiceSource,
) (semantic.Entry, error) {
	if s.config.ResolveSemanticInventorySelections == nil {
		return semantic.Entry{}, support.Unavailable("commands", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References: []semantic.Reference{{ConceptID: source.AnchorConceptID, BindingID: source.Family.BindingID}},
	})
	if err != nil {
		return semantic.Entry{}, support.Unavailable("catalog", "CATALOG_UNAVAILABLE", "the frame source evidence could not be resolved", err)
	}
	expectedBuildID := semantic.BuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete ||
		resolved.Build.BuildID != expectedBuildID || identity.BuildID != expectedBuildID {
		return semantic.Entry{}, support.Conflict("commands", "STALE_SEMANTIC_CONTEXT", "the frame source belongs to a different semantic inventory build", nil, nil)
	}
	if len(resolved.Entries) != 1 || resolved.Entries[0].ConceptID != source.AnchorConceptID || resolved.Entries[0].BindingID != source.Family.BindingID {
		return semantic.Entry{}, invalidFrameSource("the frame source anchor is unavailable in the current authorized inventory")
	}
	contextToken, err := semanticInventoryContextToken(authorized.Snapshot, explorerID, rowRoot, expectedBuildID)
	if err != nil {
		return semantic.Entry{}, err
	}
	if identity.SemanticContextToken != contextToken {
		return semantic.Entry{}, support.Conflict("commands", "STALE_SEMANTIC_CONTEXT", "reload the frame source for the current table root and inventory build", nil, nil)
	}
	return resolved.Entries[0], nil
}

func frameSourceFamilies(snapshot capability.Snapshot, entries []semantic.Entry) []FrameSourceFamilyCandidate {
	byFamily := make(map[string]FrameSourceFamilyCandidate)
	for _, entry := range entries {
		observation := entry.Observation
		if observation.RuleHint != semantic.RuleHintCodedValueV1 {
			continue
		}
		plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
		if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != ws.SourceCodedValue ||
			plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
			continue
		}
		binding := plan.Source.Lookup.Binding
		candidate, ok := semanticConstructionCandidate(snapshot, observation)
		if !ok || candidate.ResourceType != observation.Source.Type || canonicalFramePath(candidate.FieldPath) != semanticFrameValuePath(observation) {
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
			byFamily[key] = FrameSourceFamilyCandidate{Family: family, Entry: entry, Candidate: candidate}
		}
	}
	families := make([]FrameSourceFamilyCandidate, 0, len(byFamily))
	for _, family := range byFamily {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool {
		if families[i].Entry.Observation.Population != families[j].Entry.Observation.Population {
			return families[i].Entry.Observation.Population > families[j].Entry.Observation.Population
		}
		return families[i].Family.SourcePath < families[j].Family.SourcePath
	})
	return families
}

func semanticFrameValuePath(observation semantic.Observation) string {
	valuePath := canonicalFramePath(observation.Value.Selector)
	ownerPath := canonicalFramePath(observation.OwningScope)
	if valuePath == "" || ownerPath == "" || valuePath == ownerPath || strings.HasPrefix(valuePath, ownerPath+".") {
		return valuePath
	}
	return ownerPath + "." + valuePath
}

func canonicalFramePath(raw string) string {
	path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "root."))
	if path == "" {
		return ""
	}
	parts := strings.Split(path, ".")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ".")
}

func invalidFrameSource(message string) error {
	return support.Unprocessable("frame-source", "INVALID_FRAME_SOURCE", message, nil)
}
