package population

import (
	"context"
	"fmt"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

func (s *Service) ProposePopulationMemberRemoval(ctx context.Context, request PopulationMemberRemovalProposalRequest) (PopulationMemberRemovalProposalResponse, error) {
	if err := request.Validate(); err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.Malformed("population-member-proposal", err.Error(), err)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	if s == nil || s.config.Store == nil || s.capability().ForCompilation == nil || s.capability().Catalog == nil {
		return PopulationMemberRemovalProposalResponse{}, support.Unavailable("population-member-proposal", "CAPABILITY_UNAVAILABLE", "authorized population proposal compilation is not configured", nil)
	}
	authorized, err := s.capability().ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.CapabilityFailure("population-member-proposal", "STALE_CATALOG_SNAPSHOT", "reload the catalog before changing the selected population", err)
	}
	if authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "STALE_CATALOG_SNAPSHOT", "reload the catalog before changing the selected population", nil, nil)
	}
	snapshot := authorized.Snapshot.Clone()
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "STALE_CATALOG_SNAPSHOT", "reload the catalog before changing the selected population", nil, nil)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	owner, err := s.config.GetOwner(ctx, projectid.Canonical(request.Project), request.ExplorerID)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.PersistenceFailure("population-member-proposal", "the Explorer workspace could not be loaded", err)
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return PopulationMemberRemovalProposalResponse{}, support.NotFound("population-member-proposal", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before removing a selected member", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be compiled", nil, err)
	}
	workspaceDigest, err := workspace.Digest()
	if err != nil || workspaceDigest != owner.DraftDigest {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	documentIndex := s.config.ConstructionDocumentIndex(workspace, request.OutputID)
	if documentIndex < 0 {
		return PopulationMemberRemovalProposalResponse{}, support.Unprocessable("population-member-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify one saved table", nil)
	}
	baseDocument := workspace.Documents[documentIndex]
	if baseDocument.Population == nil || baseDocument.Population.SelectionRevisionID != request.BaseSelectionID {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "STALE_POPULATION_SELECTION", "the requested immutable selection is not attached to this table", nil, nil)
	}
	baseDocumentDigest, err := s.config.DocumentDigest(baseDocument)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, fmt.Errorf("digest population proposal base document: %w", err)
	}
	baseSelection, err := s.getAuthorizedPopulationSelection(ctx, request.Project, request.BaseSelectionID, snapshot)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if baseSelection.ResourceType != request.RemovedMember.ResourceType {
		return PopulationMemberRemovalProposalResponse{}, support.Unprocessable("population-member-proposal", "INVALID_POPULATION_MEMBER", "the removed member does not match the selected resource type", nil)
	}
	contains, err := selectionContainsExactMember(ctx, s.config.Store, projectid.Canonical(request.Project), baseSelection.ID, request.RemovedMember)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.PersistenceFailure("population-member-proposal", "the selected resource set could not be inspected", err)
	}
	if !contains {
		return PopulationMemberRemovalProposalResponse{}, support.Unprocessable("population-member-proposal", "POPULATION_MEMBER_NOT_FOUND", "the exact resource reference is not a member of the attached selection", nil)
	}

	created, err := s.config.CreateSelection(ctx, selection.SelectionIntentCreateRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		IdempotencyKey: "population-member-removal-" + uuid.NewString(),
		Source:         selection.SelectionSourceIntent{Kind: selection.SelectionSourceSelectionRevision, SelectionRevisionID: baseSelection.ID},
		Exclusions:     []selection.ResourceRef{request.RemovedMember},
	})
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	candidateSelection := created.Header
	if err := ValidateRemovalSelection(baseSelection, candidateSelection, request.RemovedMember, snapshot); err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.Conflict("population-member-proposal", "INVALID_CANDIDATE_SELECTION", "the server-created candidate selection failed exact removal validation", nil, err)
	}
	routeChoiceID, err := s.PopulationRemovalRouteChoice(ctx, request, baseDocument, candidateSelection.ID)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	candidateWorkspace := workspace
	candidateWorkspace.Documents = append([]authoringv2.Document(nil), workspace.Documents...)
	candidateDocument := baseDocument
	candidatePopulation := *baseDocument.Population
	candidatePopulation.Route = clonePopulationRouteForProposal(baseDocument.Population.Route)
	candidatePopulation.SelectionRevisionID = candidateSelection.ID
	candidateDocument.Population = &candidatePopulation
	candidateWorkspace.Documents[documentIndex] = candidateDocument
	preparedCandidate, err := authoringv2.PrepareWorkspaceForCompilation(candidateWorkspace, s.config.Catalog(snapshot, request.ExplorerID))
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, fmt.Errorf("prepare population removal candidate workspace: %w", err)
	}
	candidateDigest, err := preparedCandidate.Digest()
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, fmt.Errorf("digest population removal candidate workspace: %w", err)
	}
	binding := &explorer.PopulationMemberRemovalProposalBinding{
		DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, OutputID: request.OutputID,
		BaseDocumentDigest: baseDocumentDigest, BaseSelectionRevisionID: baseSelection.ID,
		BaseMembershipDigest: baseSelection.MembershipDigest, BaseMemberCount: baseSelection.MemberCount,
		CandidateSelectionRevisionID: candidateSelection.ID, CandidateMembershipDigest: candidateSelection.MembershipDigest,
		CandidateMemberCount: candidateSelection.MemberCount, RemovedMember: request.RemovedMember,
		RouteChoiceID: routeChoiceID, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken, PreviewLimit: request.Limit,
	}
	receipt, err := s.config.Compile(ctx, CompileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "population-member-removal-candidate",
		PopulationMemberRemovalProposal: binding,
	})
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if _, err := s.config.VerifyProposalReceipt(ctx, "population-member-proposal", receipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &candidateWorkspace); err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if err := support.ValidateReceiptOutputContract(receipt, request.OutputID); err != nil {
		return PopulationMemberRemovalProposalResponse{}, support.Unprocessable("population-member-proposal", "OUTPUT_NOT_FOUND", "the candidate receipt does not contain the requested output", err)
	}
	return PopulationMemberRemovalProposalResponse{
		ProposalID: receipt.ID, OutputID: request.OutputID, SnapshotToken: request.SnapshotToken,
		DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, BaseDocumentDigest: baseDocumentDigest,
		CandidateWorkspaceDigest: candidateDigest, BaseSelection: cloneSelectionHeader(baseSelection),
		CandidateSelection: cloneSelectionHeader(candidateSelection), RemovedMember: request.RemovedMember,
		PreviewStatus: "PREVIEW_PENDING",
	}, nil
}

const selectionBatchSize = 256

type selectionMemberReader interface {
	VisitSelectionMembers(context.Context, string, string, string, int, func(selection.SelectionMember) error) (string, error)
}

func selectionContainsExactMember(ctx context.Context, store selectionMemberReader, project, selectionID string, target selection.ResourceRef) (bool, error) {
	target = target.Canonical()
	after := ""
	maxPages := int((selection.DefaultSelectionMaxRows+selectionBatchSize-1)/selectionBatchSize) + 1
	for page := 0; page < maxPages; page++ {
		found := false
		next, err := store.VisitSelectionMembers(ctx, project, selectionID, after, selectionBatchSize, func(member selection.SelectionMember) error {
			if member.Ref.Canonical() == target {
				found = true
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
		if next == "" {
			return false, nil
		}
		if next == after {
			return false, fmt.Errorf("selection member cursor did not advance")
		}
		after = next
	}
	return false, fmt.Errorf("selection member lookup exceeded the immutable selection size bound")
}

func cloneSelectionHeader(selected *selection.SelectionRevision) *selection.SelectionRevision {
	if selected == nil {
		return nil
	}
	cloned := selected.Canonical()
	return &cloned
}

func clonePopulationRouteForProposal(route []authoringv2.PopulationRouteStep) []authoringv2.PopulationRouteStep {
	if route == nil {
		return nil
	}
	cloned := make([]authoringv2.PopulationRouteStep, len(route))
	copy(cloned, route)
	return cloned
}

func (s *Service) PopulationRemovalRouteChoice(ctx context.Context, request PopulationMemberRemovalProposalRequest, document authoringv2.Document, candidateSelectionID string) (string, error) {
	cursor := ""
	seenCursors := map[string]bool{}
	for pageNumber := 0; pageNumber < 1024; pageNumber++ {
		page, err := s.SearchPopulationRoutes(ctx, PopulationRoutesRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
			OutputID: request.OutputID, SelectionRevisionID: candidateSelectionID, Limit: 50, Cursor: cursor,
		})
		if err != nil {
			return "", err
		}
		for _, choice := range page.Choices {
			if RemovalRouteMatches(document.Population.Route, choice.Route) {
				return choice.RouteChoiceID, nil
			}
		}
		if page.NextCursor == "" {
			break
		}
		if seenCursors[page.NextCursor] || page.NextCursor == cursor {
			return "", support.Conflict("population-member-proposal", "INVALID_POPULATION_ROUTE", "the route search did not advance", nil, nil)
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return "", support.Unprocessable("population-member-proposal", "INVALID_POPULATION_ROUTE", "the existing population route is no longer available for the candidate selection", nil)
}

func (s *Service) PreparePopulationMemberRemovalProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	snapshot := authorized.Snapshot
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyPopulationMemberProposal {
		return nil, nil, support.Malformed("commands", "APPLY_POPULATION_MEMBER_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.config.LookupReceipt(ctx, project, explorerID, command.ProposalID())
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID() || receipt.PopulationMemberRemovalProposal == nil {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the proposal ID does not identify a population member removal receipt", nil, nil)
	}
	binding := receipt.PopulationMemberRemovalProposal
	if binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, support.Conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil || currentDigest != binding.DraftDigest {
		return nil, nil, support.Conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the saved draft no longer matches the proposal base", nil, err)
	}
	baseIndex := s.config.ConstructionDocumentIndex(current, command.OutputID)
	if baseIndex < 0 {
		return nil, nil, support.Conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the proposal output is missing or duplicated in the saved draft", nil, nil)
	}
	baseDocument := current.Documents[baseIndex]
	baseDocumentSHA, err := s.config.DocumentDigest(baseDocument)
	if err != nil || baseDocumentSHA != binding.BaseDocumentDigest || baseDocument.Population == nil || baseDocument.Population.SelectionRevisionID != binding.BaseSelectionRevisionID {
		return nil, nil, support.Conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the target table or attached selection changed after proposal creation", nil, err)
	}
	baseSelection, err := s.getAuthorizedPopulationSelection(ctx, project, binding.BaseSelectionRevisionID, snapshot)
	if err != nil {
		return nil, nil, err
	}
	candidateSelection, err := s.getAuthorizedPopulationSelection(ctx, project, binding.CandidateSelectionRevisionID, snapshot)
	if err != nil {
		return nil, nil, err
	}
	containsRemovedMember, memberReadErr := selectionContainsExactMember(ctx, s.config.Store, projectid.Canonical(project), baseSelection.ID, binding.RemovedMember)
	if memberReadErr != nil {
		return nil, nil, support.PersistenceFailure("commands", "the selected resource set could not be inspected", memberReadErr)
	}
	if validationErr := ValidateRemovalSelection(baseSelection, candidateSelection, binding.RemovedMember, snapshot); validationErr != nil {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the immutable selection revisions no longer prove the exact removed member", nil, validationErr)
	}
	if baseSelection.MembershipDigest != binding.BaseMembershipDigest || baseSelection.MemberCount != binding.BaseMemberCount ||
		candidateSelection.MembershipDigest != binding.CandidateMembershipDigest || candidateSelection.MemberCount != binding.CandidateMemberCount ||
		!containsRemovedMember {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the immutable selection revisions no longer prove the exact removed member", nil, fmt.Errorf("the immutable selection revisions do not match the proposal binding"))
	}
	identity, decodeErr := capability.DecodePopulationRouteChoiceID(binding.RouteChoiceID)
	if decodeErr != nil || identity.SnapshotToken != request.SnapshotToken || identity.OutputID != command.OutputID || identity.SelectionRevisionID != candidateSelection.ID ||
		!RemovalRouteMatches(baseDocument.Population.Route, identity.Route) {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the receipt route is not bound to the exact candidate selection and retained table route", nil, decodeErr)
	}
	synthetic := authoringv2.Command{Type: authoringv2.CommandSetTablePopulation, OutputID: command.OutputID, Payload: &authoringv2.TablePayload{SelectionRevisionID: candidateSelection.ID, RouteChoiceID: binding.RouteChoiceID}}
	preparedRoute, err := s.PreparePopulationRouteChoices(ctx, project, authorized, []capability.PopulationRouteChoiceIdentity{identity}, current, []authoringv2.Command{synthetic})
	if err != nil {
		return nil, nil, err
	}
	if len(preparedRoute) != 1 || !RemovalRouteMatches(baseDocument.Population.Route, identity.Route) {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the retained population route could not be reauthorized exactly", nil, nil)
	}
	expectedWorkspace := current
	expectedWorkspace.Documents = append([]authoringv2.Document(nil), current.Documents...)
	expectedDocument := baseDocument
	expectedPopulation := *baseDocument.Population
	expectedPopulation.Route = clonePopulationRouteForProposal(baseDocument.Population.Route)
	expectedPopulation.SelectionRevisionID = candidateSelection.ID
	expectedDocument.Population = &expectedPopulation
	expectedWorkspace.Documents[baseIndex] = expectedDocument
	preparedExpected, err := authoringv2.PrepareWorkspaceForCompilation(expectedWorkspace, s.config.Catalog(snapshot, explorerID))
	if err != nil {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the expected candidate workspace cannot be normalized for compilation", nil, err)
	}
	expectedDigest, err := preparedExpected.Digest()
	if err != nil || expectedDigest != binding.CandidateWorkspaceDigest || expectedDigest != receipt.IntentDigest {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the receipt does not represent the exact base workspace with only its population revision replaced", nil, err)
	}
	if _, err := s.config.VerifyProposalReceipt(ctx, "population-member-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, &expectedWorkspace); err != nil {
		return nil, nil, err
	}
	if err := command.ResolvePopulationMemberProposal(candidateSelection.ID, baseDocument.Population.Route); err != nil {
		return nil, nil, support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the candidate population is invalid", nil, err)
	}
	return commands, receipt, nil
}

func CheckPopulationMemberRemovalProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
		return support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied population member workspace: %w", err)
	}
	binding := receipt.PopulationMemberRemovalProposal
	if digest != binding.CandidateWorkspaceDigest || digest != receipt.IntentDigest {
		return support.Conflict("commands", "POPULATION_MEMBER_PROPOSAL_MISMATCH", "the applied population does not match the exact candidate receipt workspace", nil, nil)
	}
	return nil
}

func (s *Service) PreviewPopulationMemberRemovalProposal(ctx context.Context, project, explorerID string, receipt *explorer.CompilationReceipt) error {
	if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
		return support.Conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	if err := s.config.PreviewMemberRemoval(ctx, receipt, project, explorerID); err != nil {
		return support.Conflict("commands", "POPULATION_MEMBER_PREVIEW_FAILED", "the exact candidate preview failed; the accepted draft remains unchanged", nil, err)
	}
	return nil
}
