package lifecycle

import (
	"context"
	"reflect"

	"github.com/calypr/loom/internal/semantic"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func (s *Service) prepareFrameSourceCommand(
	ctx context.Context,
	project, explorerID string,
	authorized ports.AuthorizedCapability,
	workspace ws.Workspace,
	command authoringv2.Command,
	identity capability.ConstructionChoiceIdentity,
) ([]authoringv2.PreparedCommand, error) {
	if command.Type != authoringv2.CommandSetFrameSource && command.Type != authoringv2.CommandReplaceFrameSource {
		return nil, support.Malformed("commands", "unsupported frame source command", nil)
	}
	document := workspace.FindOutputDocument(command.OutputID)
	if document == nil || document.Route.OccurrenceID != ws.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return nil, support.Malformed("commands", "outputId does not identify a valid row-rooted table", nil)
	}
	if identity.SnapshotToken != authorized.Snapshot.Token || identity.Kind != capability.ConstructionChoiceSourceSemanticFrame {
		return nil, support.Conflict("commands", "STALE_FRAME_SOURCE", "the frame source belongs to a different authorized snapshot", nil, nil)
	}
	sourceIdentity, ok := identity.Source.(capability.SemanticFrameChoiceSource)
	if !ok {
		return nil, invalidFrameSource("the frame source does not identify a correlated code/value family")
	}
	if len(identity.Route) > 0 && identity.Route[0].FromResourceType != document.RootResourceType || len(identity.Route) == 0 && sourceIdentity.Family.ResourceType != document.RootResourceType {
		return nil, invalidFrameSource("the frame source route does not start at this table's row root")
	}
	entry, err := s.constructionService.ResolveFrameSourceAnchor(ctx, project, explorerID, document.RootResourceType, authorized, identity, sourceIdentity)
	if err != nil {
		return nil, err
	}
	familyCandidates := semanticbrowse.FrameSourceFamilies(authorized.Snapshot, []semantic.Entry{entry})
	if len(familyCandidates) != 1 {
		return nil, invalidFrameSource("the frame source is no longer a complete coded value family")
	}
	family := familyCandidates[0]
	if !reflect.DeepEqual(family.Family, sourceIdentity.Family) || family.Candidate.ID != sourceIdentity.CandidateID ||
		family.Candidate.NodeID != sourceIdentity.NodeID || family.Candidate.FieldPath != sourceIdentity.FieldPath {
		return nil, invalidFrameSource("the frame source metadata or compiler candidate changed")
	}
	resolvedRoute, err := support.ReauthorizeConstructionRoute(authorized.Snapshot, document.RootResourceType, family.Candidate.NodeID, identity.Route)
	if err != nil {
		return nil, invalidFrameSource("the frame source route is no longer authorized")
	}
	provenCandidate, err := proveConstructionCandidate(ctx, authorized, document.RootResourceType, family.Candidate, resolvedRoute)
	if err != nil {
		return nil, invalidFrameSource("the frame source route and value no longer compile")
	}
	provenCandidate, ok = capability.SemanticCandidateWithFallback(authorized.Snapshot, family.Entry.Observation, provenCandidate)
	if !ok {
		return nil, invalidFrameSource("the frame source fallback selectors no longer match compiler-proved candidates")
	}
	contextToken, err := semanticbrowse.SemanticInventoryContextToken(authorized.Snapshot, explorerID, document.RootResourceType, identity.BuildID)
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
	if err != nil || !construction.ConstructionChoiceIdentityMatches(expectedChoice, command.FrameChoiceID(), identity) ||
		!reflect.DeepEqual(expectedChoice.Source, sourceIdentity) || !reflect.DeepEqual(expectedChoice.Route, identity.Route) {
		return nil, invalidFrameSource("the frame source token does not match current semantic and route evidence")
	}
	if !construction.ConstructionChoiceSupports(expectedChoice, command.FrameForm()) {
		return nil, invalidFrameSource("the selected form is not supported for this exact frame source")
	}
	if command.FrameForm() != capability.ConstructionChoiceValue && command.FrameForm() != capability.ConstructionChoiceFirst &&
		command.FrameForm() != capability.ConstructionChoiceAll && command.FrameForm() != capability.ConstructionChoiceDistinct {
		return nil, invalidFrameSource("frame sources support only VALUE, FIRST, ALL, or DISTINCT forms")
	}
	frame, err := ws.NewFrameDefinition(
		semanticbrowse.FrameSourceTitle(family.Family), semanticbrowse.FrameSourceDescription(family.Family, resolvedRoute),
		family.Family, resolvedRoute, command.FrameForm(),
	)
	if err != nil {
		return nil, invalidFrameSource("the selected frame definition is invalid")
	}
	resolution, err := authoringv2.ResolveFrameSource(command, frame)
	if err != nil {
		return nil, invalidFrameSource("the resolved frame definition is invalid")
	}
	return []authoringv2.PreparedCommand{{Command: command, Resolution: resolution}}, nil
}

func invalidFrameSource(message string) error {
	return support.Unprocessable("frame-source", "INVALID_FRAME_SOURCE", message, nil)
}
