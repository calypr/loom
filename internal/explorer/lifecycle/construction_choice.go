package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) prepareConstructionChoice(ctx context.Context, project, explorerID string, authorized AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace authoringv2.Workspace, catalogSnapshot authoringv2.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if len(commands) == 0 || len(commands) > 100 || len(commands) != len(identities) {
		return nil, malformed("commands", "APPLY_CONSTRUCTION_CHOICE requires between 1 and 100 resolved choices", nil)
	}
	snapshot := authorized.Snapshot
	if !constructionChoicesMatchSnapshot(identities, snapshot.Token) || catalogSnapshot.SnapshotToken != snapshot.Token {
		return nil, conflict("commands", "STALE_CONSTRUCTION_CHOICE", "the construction choice does not match the current authorized snapshot", nil, nil)
	}
	semanticEntries, buildID, err := s.resolveConstructionChoiceSemantics(ctx, project, authorized, identities)
	if err != nil {
		return nil, err
	}
	for index := range commands {
		command := &commands[index]
		if command.Type != authoringv2.CommandApplyConstructionChoice || command.ConstructionChoice == nil {
			return nil, malformed("commands", "construction choice requests cannot include unrelated commands", nil)
		}
		document := findSemanticOutput(workspace, command.OutputID)
		if document == nil || strings.TrimSpace(document.RootResourceType) == "" ||
			document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
			return nil, malformed("commands", "outputId does not identify a valid row-rooted table", nil)
		}
		identity := identities[index]
		var resolution authoringv2.ResolvedConstructionChoice
		var title string
		switch source := identity.Source.(type) {
		case capability.FieldChoiceSource:
			resolution, title, err = resolveFieldConstructionChoice(ctx, authorized, snapshot, catalogSnapshot, document.RootResourceType, *command.ConstructionChoice, identity, source)
		case capability.SemanticBindingChoiceSource:
			resolution, title, err = resolveSemanticConstructionChoice(ctx, authorized, snapshot, catalogSnapshot, explorerID, document.RootResourceType, identity.SemanticContextToken, identity.BuildID, buildID, semanticEntries, *command.ConstructionChoice, identity, source)
		default:
			return nil, unprocessable("commands", "INVALID_CONSTRUCTION_CHOICE", "construction choice source kind is unsupported", nil)
		}
		if err != nil {
			return nil, err
		}
		if command.ConstructionChoice.FrameID != "" {
			frame, found := frameForConstructionChoice(document, command.ConstructionChoice.FrameID, identity, semanticEntries)
			if !found || !reflect.DeepEqual(resolution.Route, identity.Route) || command.ConstructionChoice.Form != frame.Form {
				return nil, invalidConstructionChoice("the semantic category does not match its saved frame source, route, or value policy")
			}
			resolution.FrameID = frame.ID
		}
		command.ResolvedChoice = &resolution
		if strings.TrimSpace(command.Title) == "" {
			command.Title = title
		}
	}
	return commands, nil
}

func frameForConstructionChoice(
	document *authoringv2.Document,
	frameID string,
	identity capability.ConstructionChoiceIdentity,
	entries map[string]catalog.SemanticInventoryEntry,
) (authoringv2.FrameDefinition, bool) {
	if document == nil {
		return authoringv2.FrameDefinition{}, false
	}
	var frame *authoringv2.FrameDefinition
	for index := range document.Frames {
		if document.Frames[index].ID == frameID {
			frame = &document.Frames[index]
			break
		}
	}
	if frame == nil {
		return authoringv2.FrameDefinition{}, false
	}
	semanticIdentity, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || !reflect.DeepEqual(identity.Route, frame.Route) {
		return authoringv2.FrameDefinition{}, false
	}
	entry, found := entries[semanticChoiceKey(semanticIdentity.ConceptID, semanticIdentity.BindingID)]
	if !found {
		return authoringv2.FrameDefinition{}, false
	}
	plan := authoringv2.ResolveSemanticSelectionPlan(entry.Observation)
	if !semanticEntryMatchesFrame(entry, entry.Observation, plan, *frame) {
		return authoringv2.FrameDefinition{}, false
	}
	return *frame, true
}

func (s *Service) resolveConstructionChoiceSemantics(ctx context.Context, project string, authorized AuthorizedCapability, identities []capability.ConstructionChoiceIdentity) (map[string]catalog.SemanticInventoryEntry, string, error) {
	references := make([]catalog.SemanticInventoryReference, 0, len(identities))
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
		if !ok {
			continue
		}
		key := semanticChoiceKey(source.ConceptID, source.BindingID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		references = append(references, catalog.SemanticInventoryReference{ConceptID: source.ConceptID, BindingID: source.BindingID})
	}
	if len(references) == 0 {
		return nil, "", nil
	}
	if s.config.ResolveSemanticInventorySelections == nil {
		return nil, "", unavailable("commands", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, catalog.SemanticInventoryResolveOptions{
		Project:                       projectid.Legacy(authorized.Snapshot.Identity.Project),
		DatasetGeneration:             authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References:                    references,
	})
	if err != nil {
		return nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "the selected semantic inventory could not be resolved", err)
	}
	expectedBuildID := catalog.SemanticInventoryBuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != catalog.SemanticInventoryComplete || resolved.Build.State != catalog.SemanticInventoryComplete || resolved.Build.BuildID != expectedBuildID {
		return nil, "", conflict("catalog", "SEMANTIC_INVENTORY_UNAVAILABLE", "the current generation does not have a complete semantic inventory", nil, nil)
	}
	requested := make(map[string]struct{}, len(references))
	for _, reference := range references {
		requested[semanticChoiceKey(reference.ConceptID, reference.BindingID)] = struct{}{}
	}
	entries := make(map[string]catalog.SemanticInventoryEntry, len(resolved.Entries))
	for _, entry := range resolved.Entries {
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		if _, ok := requested[key]; !ok {
			return nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned an unrelated selection identity", nil)
		}
		if _, exists := entries[key]; exists {
			return nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned duplicate selection identities", nil)
		}
		entries[key] = entry
	}
	return entries, expectedBuildID, nil
}

func resolveSemanticConstructionChoice(ctx context.Context, authorized AuthorizedCapability, snapshot capability.Snapshot, catalogSnapshot authoringv2.CatalogSnapshot, explorerID, rootResourceType, semanticContextToken, tokenBuildID, buildID string, entries map[string]catalog.SemanticInventoryEntry, selection authoringv2.ConstructionChoiceSelection, choiceIdentity capability.ConstructionChoiceIdentity, identity capability.SemanticBindingChoiceSource) (authoringv2.ResolvedConstructionChoice, string, error) {
	if strings.TrimSpace(buildID) == "" || tokenBuildID != buildID {
		return authoringv2.ResolvedConstructionChoice{}, "", conflict("commands", "STALE_SEMANTIC_CONTEXT", "the semantic choice belongs to a different inventory build", nil, nil)
	}
	contextToken, err := semanticInventoryContextToken(snapshot, explorerID, rootResourceType, buildID)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", err
	}
	if semanticContextToken != contextToken {
		return authoringv2.ResolvedConstructionChoice{}, "", conflict("commands", "STALE_SEMANTIC_CONTEXT", "reload the semantic choice for the current table root and inventory build", nil, nil)
	}
	entry, found := entries[semanticChoiceKey(identity.ConceptID, identity.BindingID)]
	if !found || entry.ConceptID != identity.ConceptID || entry.BindingID != identity.BindingID {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic binding is unavailable in the current authorized inventory")
	}
	observation := entry.Observation
	candidate, found := semanticConstructionCandidate(snapshot, observation)
	if !found || candidate.ResourceType != observation.Source.Type {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic value path has no unique compiler candidate at the current source resource")
	}
	route, err := reauthorizeConstructionRoute(snapshot, rootResourceType, candidate.NodeID, choiceIdentity.Route)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic construction route is unavailable or changed")
	}
	provenCandidate := candidate
	if len(route) > 0 {
		provenCandidate, err = proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
		if err != nil {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("the complete semantic route and value no longer compile")
		}
	}
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || strings.TrimSpace(plan.LogicalType) == "" {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic observation no longer has a supported compiler source")
	}
	ownerRecordsProved := proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, route)
	choice, err := semanticInventoryConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, route, entry, provenCandidate, ownerRecordsProved)
	if err != nil || !constructionChoiceIdentityMatches(choice, selection.ChoiceID, choiceIdentity) || !reflect.DeepEqual(choice.Source, identity) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic choice identity does not match the current observation and compiler candidate")
	}
	fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, provenCandidate)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic compiler candidate has no current supported options")
	}
	catalogCandidate, found := uniqueCatalogCandidate(catalogSnapshot, provenCandidate.ID)
	if !found || (len(route) == 0 && (catalogCandidate.ConstructionChoice == nil || !reflect.DeepEqual(*catalogCandidate.ConstructionChoice, fieldChoice))) || !semanticChoiceFieldOptionsMatch(choice, fieldChoice) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic choice options do not match the current compiler candidate")
	}
	if !constructionChoiceSupports(choice, selection.Form) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("selected form is not supported for the exact semantic choice")
	}
	source := plan.Source.Normalized()
	logicalType := plan.LogicalType
	if selection.Form == capability.ConstructionChoiceOwnerRecords {
		if !ownerRecordsProved || source.Lookup == nil || source.Lookup.Binding == nil || source.Lookup.Key == nil {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("owner-record construction is not compiler-proved for the current semantic binding")
		}
		source = authoringv2.ColumnSource{Kind: authoringv2.SourceOwnerRecords, OwnerRecords: &authoringv2.OwnerRecordsSource{
			Binding: *source.Lookup.Binding,
			Key:     *source.Lookup.Key,
		}}
		logicalType = "object"
	} else {
		source.Lookup.ProjectionMode = string(selection.Form)
	}
	label := strings.TrimSpace(observation.Key.Display)
	if label == "" {
		label = strings.TrimSpace(observation.Key.Code)
	}
	if label == "" {
		label = candidate.Label
	}
	return authoringv2.ResolvedConstructionChoice{CandidateID: provenCandidate.ID, Source: source, LogicalType: logicalType, Route: route}, label, nil
}

func semanticChoiceFieldOptionsMatch(semanticChoice, fieldChoice capability.ConstructionChoice) bool {
	fieldOptions := make([]capability.ConstructionChoiceOption, 0, len(semanticChoice.Options))
	for _, option := range semanticChoice.Options {
		if option.Form != capability.ConstructionChoiceOwnerRecords {
			fieldOptions = append(fieldOptions, option)
		}
	}
	return reflect.DeepEqual(fieldOptions, fieldChoice.Options)
}

func semanticChoiceKey(conceptID, bindingID string) string {
	return bindingID + "\x00" + conceptID
}

func constructionChoicesMatchSnapshot(identities []capability.ConstructionChoiceIdentity, snapshotToken string) bool {
	if len(identities) == 0 || strings.TrimSpace(snapshotToken) == "" {
		return false
	}
	for _, identity := range identities {
		if identity.SnapshotToken != snapshotToken {
			return false
		}
	}
	return true
}

func resolveFieldConstructionChoice(ctx context.Context, authorized AuthorizedCapability, snapshot capability.Snapshot, catalogSnapshot authoringv2.CatalogSnapshot, rootResourceType string, selection authoringv2.ConstructionChoiceSelection, choiceIdentity capability.ConstructionChoiceIdentity, identity capability.FieldChoiceSource) (authoringv2.ResolvedConstructionChoice, string, error) {
	candidate, found := uniqueCapabilityCandidate(snapshot, identity.CandidateID)
	if !found || candidate.NodeID != identity.NodeID || candidate.ResourceType != identity.ResourceType || candidate.FieldPath != identity.Path || candidate.Cardinality != identity.Cardinality {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate identity is unavailable or changed")
	}
	route, err := reauthorizeConstructionRoute(snapshot, rootResourceType, candidate.NodeID, choiceIdentity.Route)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field construction route is unavailable or changed")
	}
	provenCandidate := candidate
	if len(route) > 0 {
		provenCandidate, err = proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
		if err != nil {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("the complete field route and source no longer compile")
		}
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, provenCandidate)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate no longer has a supported construction choice")
	}
	if !constructionChoiceIdentityMatches(choice, selection.ChoiceID, choiceIdentity) || !reflect.DeepEqual(choice.Source, identity) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field choice identity does not match the current candidate")
	}
	_, found = uniqueCatalogCandidate(catalogSnapshot, candidate.ID)
	if !found {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field choice options do not match the current catalog")
	}
	if !constructionChoiceSupports(choice, selection.Form) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("selected form is not supported for the exact field candidate")
	}
	if strings.TrimSpace(candidate.LogicalType) == "" {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate has no logical type")
	}
	source := authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{
		Path: strings.TrimPrefix(candidate.FieldPath, "root."), ProjectionMode: string(selection.Form),
	}}
	return authoringv2.ResolvedConstructionChoice{CandidateID: candidate.ID, Source: source, LogicalType: provenCandidate.LogicalType, Route: route}, candidate.Label, nil
}

func uniqueCapabilityCandidate(snapshot capability.Snapshot, candidateID string) (capability.Candidate, bool) {
	var match capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ID != candidateID {
			continue
		}
		if found {
			return capability.Candidate{}, false
		}
		match, found = candidate, true
	}
	return match, found
}

func capabilityCandidateBelongsToRoot(snapshot capability.Snapshot, candidate capability.Candidate, rootResourceType string) bool {
	var match capability.Node
	found := false
	for _, node := range snapshot.Nodes {
		if node.ID != candidate.NodeID {
			continue
		}
		if found {
			return false
		}
		match, found = node, true
	}
	return found && match.RowRootEligible && match.ResourceType == rootResourceType && candidate.ResourceType == rootResourceType
}

func uniqueCatalogCandidate(snapshot authoringv2.CatalogSnapshot, candidateID string) (authoringv2.CatalogCandidate, bool) {
	var match authoringv2.CatalogCandidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ID != candidateID {
			continue
		}
		if found {
			return authoringv2.CatalogCandidate{}, false
		}
		match, found = candidate, true
	}
	return match, found
}

func constructionChoiceSupports(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	for _, option := range choice.Options {
		if option.Form == form && option.Support == capability.ConstructionChoiceSupported {
			return true
		}
	}
	return false
}

func invalidConstructionChoice(message string) error {
	return unprocessable("commands", "INVALID_CONSTRUCTION_CHOICE", message, fmt.Errorf("%s", message))
}
