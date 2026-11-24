package construction

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/semanticauthoring"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

func frameForConstructionChoice(
	document *ws.Document,
	frameID string,
	identity capability.ConstructionChoiceIdentity,
	entries map[string]semantic.Entry,
) (ws.FrameDefinition, bool) {
	frame, found := frameDefinitionForID(document, frameID)
	if !found {
		return ws.FrameDefinition{}, false
	}
	semanticIdentity, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || !reflect.DeepEqual(identity.Route, frame.Route) {
		return ws.FrameDefinition{}, false
	}
	entry, found := entries[semanticChoiceKey(semanticIdentity.ConceptID, semanticIdentity.BindingID)]
	if !found {
		return ws.FrameDefinition{}, false
	}
	if !semanticEntryMatchesFrame(entry, frame) {
		return ws.FrameDefinition{}, false
	}
	return frame, true
}

func frameDefinitionForID(document *ws.Document, frameID string) (ws.FrameDefinition, bool) {
	if document == nil || strings.TrimSpace(frameID) == "" {
		return ws.FrameDefinition{}, false
	}
	for _, frame := range document.Frames {
		if frame.ID == frameID {
			return frame, true
		}
	}
	return ws.FrameDefinition{}, false
}

type resolveSemanticConstructionChoiceRequest struct {
	ctx                  context.Context
	authorized           ports.AuthorizedCapability
	snapshot             capability.Snapshot
	catalogSnapshot      ws.CatalogSnapshot
	explorerID           string
	rootResourceType     string
	semanticContextToken string
	tokenBuildID         string
	buildID              string
	entries              map[string]semantic.Entry
	families             map[string]semantic.Family
	selection            authoringv2.ConstructionChoiceSelection
	choiceIdentity       capability.ConstructionChoiceIdentity
	identity             capability.SemanticBindingChoiceSource
}

func resolveSemanticConstructionChoice(request resolveSemanticConstructionChoiceRequest) (authoringv2.ResolvedConstructionChoice, string, error) {
	ctx := request.ctx
	authorized := request.authorized
	snapshot := request.snapshot
	catalogSnapshot := request.catalogSnapshot
	explorerID := request.explorerID
	rootResourceType := request.rootResourceType
	semanticContextToken := request.semanticContextToken
	tokenBuildID := request.tokenBuildID
	buildID := request.buildID
	entries := request.entries
	families := request.families
	selection := request.selection
	choiceIdentity := request.choiceIdentity
	identity := request.identity
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
	key := semanticChoiceKey(identity.ConceptID, identity.BindingID)
	var entry semantic.Entry
	var selectedFamily *semantic.Family
	if identity.RuleHint == semantic.RuleHintCodedCategoryV1 {
		family, found := families[key]
		if !found || family.FamilyID != identity.ConceptID || family.BindingID != identity.BindingID ||
			family.SourceKind != identity.SourceKind || family.ResourceType != identity.ResourceType || family.SourcePath != identity.SourcePath ||
			family.System != identity.System || family.Version != identity.Version || identity.Code != "" {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("coded category is unavailable in the current authorized inventory")
		}
		selectedFamily = &family
		entry = semantic.EntryForFamily(family)
	} else {
		var found bool
		entry, found = entries[key]
		if !found || entry.ConceptID != identity.ConceptID || entry.BindingID != identity.BindingID {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic binding is unavailable in the current authorized inventory")
		}
	}
	observation := entry.Observation
	candidate, found := capability.SemanticConstructionCandidate(snapshot, observation)
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
	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || strings.TrimSpace(plan.LogicalType) == "" {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic observation no longer has a supported compiler source")
	}
	ownerRecordsProved := selectedFamily == nil && proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, route)
	var choice capability.ConstructionChoice
	if selectedFamily != nil {
		choice, err = semanticauthoring.CategoryConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, route, *selectedFamily, provenCandidate)
	} else {
		choice, err = capability.SemanticConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, route, entry, provenCandidate, ownerRecordsProved)
	}
	if err != nil || !capability.ConstructionChoiceIdentityMatches(choice, selection.ChoiceID, choiceIdentity) || !reflect.DeepEqual(choice.Source, identity) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic choice identity does not match the current observation and compiler candidate")
	}
	catalogCandidate, found := uniqueCatalogCandidate(catalogSnapshot, provenCandidate.ID)
	if !found {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic choice options do not match the current compiler candidate")
	}
	if len(route) == 0 {
		primaryFieldChoice, choiceErr := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, provenCandidate)
		if choiceErr != nil || catalogCandidate.ConstructionChoice == nil || !reflect.DeepEqual(*catalogCandidate.ConstructionChoice, primaryFieldChoice) {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic choice options do not match the current compiler candidate")
		}
	}
	projectedCandidate, ok := capability.SemanticCandidateWithFallback(snapshot, observation, provenCandidate)
	if !ok {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("semantic compiler candidate has no current supported options")
	}
	fieldChoice, choiceErr := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, projectedCandidate)
	if choiceErr != nil || !capability.ConstructionChoiceFieldOptionsMatch(choice, fieldChoice) {
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
		source = ws.ColumnSource{Kind: ws.SourceOwnerRecords, OwnerRecords: &ws.OwnerRecordsSource{
			Binding: *source.Lookup.Binding,
			Key:     *source.Lookup.Key,
		}}
		logicalType = "object"
	} else {
		source.Lookup.ProjectionMode = string(selection.Form)
	}
	label := strings.TrimSpace(observation.Key.Display)
	if selectedFamily != nil {
		label = semanticCategoryLabel(*selectedFamily, provenCandidate)
	} else if label == "" {
		label = strings.TrimSpace(observation.Key.Code)
	}
	if label == "" {
		label = candidate.Label
	}
	return authoringv2.ResolvedConstructionChoice{CandidateID: provenCandidate.ID, Source: source, LogicalType: logicalType, Route: route}, label, nil
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

func resolveFieldConstructionChoice(ctx context.Context, authorized ports.AuthorizedCapability, snapshot capability.Snapshot, catalogSnapshot ws.CatalogSnapshot, rootResourceType string, selection authoringv2.ConstructionChoiceSelection, choiceIdentity capability.ConstructionChoiceIdentity, identity capability.FieldChoiceSource) (authoringv2.ResolvedConstructionChoice, string, error) {
	candidate, found := capability.UniqueCapabilityCandidate(snapshot, identity.CandidateID)
	if !found || !fieldCandidateMatchesIdentity(candidate, identity) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate identity is unavailable or changed")
	}
	route, err := reauthorizeConstructionRoute(snapshot, rootResourceType, candidate.NodeID, choiceIdentity.Route)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field construction route is unavailable or changed")
	}
	provenCandidate := candidate
	if len(route) > 0 || strings.HasPrefix(candidate.ID, "schema_field.") {
		provenCandidate, err = proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
		if err != nil {
			return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("the complete field route and source no longer compile")
		}
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, provenCandidate)
	if err != nil {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate no longer has a supported construction choice")
	}
	return resolveFieldConstructionChoiceFromProvenCandidate(
		catalogSnapshot, route, selection, choiceIdentity, identity, candidate, provenCandidate, choice,
	)
}

func resolveFieldConstructionChoiceFromProvenCandidate(catalogSnapshot ws.CatalogSnapshot, route []capability.ConstructionRouteStep, selection authoringv2.ConstructionChoiceSelection, choiceIdentity capability.ConstructionChoiceIdentity, identity capability.FieldChoiceSource, candidate, provenCandidate capability.Candidate, choice capability.ConstructionChoice) (authoringv2.ResolvedConstructionChoice, string, error) {
	if !fieldCandidateMatchesIdentity(candidate, identity) ||
		provenCandidate.ID != candidate.ID || provenCandidate.NodeID != candidate.NodeID || provenCandidate.ResourceType != candidate.ResourceType || provenCandidate.FieldPath != candidate.FieldPath {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate identity is unavailable or changed")
	}
	if !reflect.DeepEqual(choice.Route, route) || !capability.ConstructionChoiceIdentityMatches(choice, selection.ChoiceID, choiceIdentity) || !reflect.DeepEqual(choice.Source, identity) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field choice identity does not match the current candidate")
	}
	_, found := uniqueCatalogCandidate(catalogSnapshot, candidate.ID)
	if !found && !strings.HasPrefix(candidate.ID, "schema_field.") {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field choice options do not match the current catalog")
	}
	if !constructionChoiceSupports(choice, selection.Form) {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("selected form is not supported for the exact field candidate")
	}
	if strings.TrimSpace(candidate.LogicalType) == "" {
		return authoringv2.ResolvedConstructionChoice{}, "", invalidConstructionChoice("field candidate has no logical type")
	}
	source := ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{
		Path: strings.TrimPrefix(candidate.FieldPath, "root."), ProjectionMode: string(selection.Form),
	}}
	return authoringv2.ResolvedConstructionChoice{CandidateID: candidate.ID, Source: source, LogicalType: provenCandidate.LogicalType, Route: route}, candidate.Label, nil
}

func fieldCandidateMatchesIdentity(candidate capability.Candidate, identity capability.FieldChoiceSource) bool {
	return candidate.ID == identity.CandidateID && candidate.NodeID == identity.NodeID && candidate.ResourceType == identity.ResourceType &&
		candidate.FieldPath == identity.Path && candidate.Cardinality == identity.Cardinality &&
		reflect.DeepEqual(candidate.RepeatedBoundaries, identity.RepeatedBoundaries)
}

func uniqueCatalogCandidate(snapshot ws.CatalogSnapshot, candidateID string) (ws.CatalogCandidate, bool) {
	var match ws.CatalogCandidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ID != candidateID {
			continue
		}
		if found {
			return ws.CatalogCandidate{}, false
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

func (s *Service) PrepareConstructionChoice(ctx context.Context, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace ws.Workspace, catalogSnapshot ws.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.PreparedCommand, error) {
	if len(commands) == 0 || len(commands) > 100 || len(commands) != len(identities) {
		return nil, malformed("commands", "APPLY_CONSTRUCTION_CHOICE requires between 1 and 100 resolved choices", nil)
	}
	snapshot := authorized.Snapshot
	if !constructionChoicesMatchSnapshot(identities, snapshot.Token) || catalogSnapshot.SnapshotToken != snapshot.Token {
		return nil, conflict("commands", "STALE_CONSTRUCTION_CHOICE", "the construction choice does not match the current authorized snapshot", nil, nil)
	}
	semanticEntries, semanticFamilies, buildID, err := s.resolveConstructionChoiceSemantics(ctx, authorized, identities)
	if err != nil {
		return nil, err
	}
	prepared := make([]authoringv2.PreparedCommand, len(commands))
	for index := range commands {
		command := commands[index]
		if command.Type != authoringv2.CommandApplyConstructionChoice || command.ConstructionChoice() == nil {
			return nil, malformed("commands", "construction choice requests cannot include unrelated commands", nil)
		}
		document := workspace.FindOutputDocument(command.OutputID)
		if document == nil || strings.TrimSpace(document.RootResourceType) == "" ||
			document.Route.OccurrenceID != ws.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
			return nil, malformed("commands", "outputId does not identify a valid row-rooted table", nil)
		}
		identity := identities[index]
		choiceEntries := semanticEntries
		choiceFamilies := semanticFamilies
		if command.ConstructionChoice().FrameID != "" {
			frame, found := frameDefinitionForID(document, command.ConstructionChoice().FrameID)
			if !found || !reflect.DeepEqual(identity.Route, frame.Route) {
				return nil, invalidConstructionChoice("the semantic category does not match its saved frame source or route")
			}
			semanticIdentity, ok := identity.Source.(capability.SemanticBindingChoiceSource)
			if !ok {
				return nil, invalidConstructionChoice("the semantic category does not match its saved frame source or route")
			}
			key := semanticChoiceKey(semanticIdentity.ConceptID, semanticIdentity.BindingID)
			entry, found := semanticEntries[key]
			if !found {
				return nil, invalidConstructionChoice("the semantic category is unavailable in its saved frame source")
			}
			projected, ok := semanticEntryForFrameFamily(entry, frame.Source)
			if !ok {
				return nil, invalidConstructionChoice("the semantic category no longer matches its saved frame source")
			}
			choiceEntries = map[string]semantic.Entry{key: projected}
		}
		var resolution authoringv2.ResolvedConstructionChoice
		var title string
		switch source := identity.Source.(type) {
		case capability.FieldChoiceSource:
			resolution, title, err = resolveFieldConstructionChoice(ctx, authorized, snapshot, catalogSnapshot, document.RootResourceType, *command.ConstructionChoice(), identity, source)
		case capability.SemanticBindingChoiceSource:
			resolution, title, err = resolveSemanticConstructionChoice(resolveSemanticConstructionChoiceRequest{
				ctx: ctx, authorized: authorized, snapshot: snapshot, catalogSnapshot: catalogSnapshot,
				explorerID: explorerID, rootResourceType: document.RootResourceType,
				semanticContextToken: identity.SemanticContextToken, tokenBuildID: identity.BuildID,
				buildID: buildID, entries: choiceEntries, families: choiceFamilies,
				selection: *command.ConstructionChoice(), choiceIdentity: identity, identity: source,
			})
		default:
			return nil, unprocessable("commands", "INVALID_CONSTRUCTION_CHOICE", "construction choice source kind is unsupported", nil)
		}
		if err != nil {
			return nil, err
		}
		if command.ConstructionChoice().FrameID != "" {
			frame, found := frameForConstructionChoice(document, command.ConstructionChoice().FrameID, identity, semanticEntries)
			if !found || !reflect.DeepEqual(resolution.Route, identity.Route) || command.ConstructionChoice().Form != frame.Form {
				return nil, invalidConstructionChoice("the semantic category does not match its saved frame source, route, or value policy")
			}
			resolution.FrameID = frame.ID
		}
		prepared[index] = authoringv2.PreparedCommand{Command: command, Resolution: authoringv2.ConstructionChoiceResolution{Choice: &resolution, Title: title}}
	}
	return prepared, nil
}

func (s *Service) resolveConstructionChoiceSemantics(ctx context.Context, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity) (map[string]semantic.Entry, map[string]semantic.Family, string, error) {
	references := make([]semantic.Reference, 0, len(identities))
	familyReferences := make([]semantic.FamilyReference, 0, len(identities))
	seen := make(map[string]struct{}, len(identities))
	seenFamilies := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
		if !ok {
			continue
		}
		key := semanticChoiceKey(source.ConceptID, source.BindingID)
		if source.RuleHint == semantic.RuleHintCodedCategoryV1 {
			if _, exists := seenFamilies[key]; exists {
				continue
			}
			seenFamilies[key] = struct{}{}
			familyReferences = append(familyReferences, semantic.FamilyReference{
				FamilyID: source.ConceptID, BindingID: source.BindingID, SourceKind: source.SourceKind,
				ResourceType: source.ResourceType, SourcePath: source.SourcePath,
				System: source.System, Version: source.Version,
			})
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		references = append(references, semantic.Reference{ConceptID: source.ConceptID, BindingID: source.BindingID})
	}
	if len(references) == 0 && len(familyReferences) == 0 {
		return nil, nil, "", nil
	}
	if s.config.ResolveSemanticInventorySelections == nil {
		return nil, nil, "", unavailable("commands", "CATALOG_UNAVAILABLE", "semantic inventory resolution is not configured", nil)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := s.config.ResolveSemanticInventorySelections(ctx, semantic.ResolveOptions{
		Project:                       projectid.Legacy(authorized.Snapshot.Identity.Project),
		DatasetGeneration:             authorized.Snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References:                    references, FamilyReferences: familyReferences,
	})
	if err != nil {
		return nil, nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "the selected semantic inventory could not be resolved", err)
	}
	expectedBuildID := semantic.BuildID(projectid.Legacy(authorized.Snapshot.Identity.Project), authorized.Snapshot.Identity.Generation)
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete || resolved.Build.BuildID != expectedBuildID {
		return nil, nil, "", conflict("catalog", "SEMANTIC_INVENTORY_UNAVAILABLE", "the current generation does not have a complete semantic inventory", nil, nil)
	}
	requested := make(map[string]struct{}, len(references))
	for _, reference := range references {
		requested[semanticChoiceKey(reference.ConceptID, reference.BindingID)] = struct{}{}
	}
	entries := make(map[string]semantic.Entry, len(resolved.Entries))
	for _, entry := range resolved.Entries {
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		if _, ok := requested[key]; !ok {
			return nil, nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned an unrelated selection identity", nil)
		}
		if _, exists := entries[key]; exists {
			return nil, nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned duplicate selection identities", nil)
		}
		entries[key] = entry
	}
	requestedFamilies := make(map[string]semantic.FamilyReference, len(familyReferences))
	for _, reference := range familyReferences {
		requestedFamilies[semanticChoiceKey(reference.FamilyID, reference.BindingID)] = reference
	}
	families := make(map[string]semantic.Family, len(resolved.Families))
	for _, family := range resolved.Families {
		key := semanticChoiceKey(family.FamilyID, family.BindingID)
		reference, ok := requestedFamilies[key]
		if !ok || family.FamilyReference != reference {
			return nil, nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned an unrelated category family", nil)
		}
		if _, exists := families[key]; exists {
			return nil, nil, "", unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory returned duplicate category families", nil)
		}
		families[key] = family
	}
	if len(entries) != len(requested) || len(families) != len(requestedFamilies) {
		return nil, nil, "", conflict("catalog", "STALE_CONSTRUCTION_CHOICE", "a selected semantic family is unavailable in the current authorized inventory", nil, nil)
	}
	return entries, families, expectedBuildID, nil
}
