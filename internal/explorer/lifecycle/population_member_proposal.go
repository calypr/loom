package lifecycle

import (
	"context"
	"fmt"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

// PopulationMemberRemovalProposalRequest identifies one exact member in the
// currently attached immutable selection. The caller never supplies candidate
// membership or a population route.
type PopulationMemberRemovalProposalRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	BaseSelectionID      string
	RemovedMember        explorer.ResourceRef
	Limit                int
}

func (r PopulationMemberRemovalProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
		"baseSelectionId": r.BaseSelectionID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.RemovedMember != r.RemovedMember.Canonical() {
		return fmt.Errorf("removedMember must contain exact canonical scope and identity fields")
	}
	if err := r.RemovedMember.Validate(r.Project, "", ""); err != nil {
		return fmt.Errorf("removedMember is invalid: %w", err)
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit must be between 1 and %d", dataframeexecution.MaxPreviewLimit)
	}
	return nil
}

// PopulationMemberRemovalProposalResponse exposes immutable headers for
// paginated inspection; member arrays are never downloaded by this operation.
type PopulationMemberRemovalProposalResponse struct {
	ProposalID               string                      `json:"proposalId,omitempty"`
	OutputID                 string                      `json:"outputId"`
	SnapshotToken            string                      `json:"snapshotToken"`
	DraftVersion             int64                       `json:"draftVersion"`
	DraftDigest              string                      `json:"draftDigest"`
	BaseDocumentDigest       string                      `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                      `json:"candidateWorkspaceDigest"`
	BaseSelection            *explorer.SelectionRevision `json:"baseSelection,omitempty"`
	CandidateSelection       *explorer.SelectionRevision `json:"candidateSelection,omitempty"`
	RemovedMember            explorer.ResourceRef        `json:"removedMember"`
	PreviewStatus            string                      `json:"previewStatus"`
}

// ProposePopulationMemberRemoval creates an unattached immutable candidate
// selection by excluding one exact member from the current attached selection,
// reissues the same authorized population route for that candidate, and stores
// a purpose-bound compilation receipt. Apply is the only operation that saves
// the changed population on the draft.
func (s *Service) ProposePopulationMemberRemoval(ctx context.Context, request PopulationMemberRemovalProposalRequest) (PopulationMemberRemovalProposalResponse, error) {
	if err := request.Validate(); err != nil {
		return PopulationMemberRemovalProposalResponse{}, malformed("population-member-proposal", err.Error(), err)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	if s == nil || s.store == nil || s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return PopulationMemberRemovalProposalResponse{}, unavailable("population-member-proposal", "CAPABILITY_UNAVAILABLE", "authorized population proposal compilation is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "STALE_CATALOG_SNAPSHOT", "reload the catalog before changing the selected population", nil, err)
	}
	snapshot := authorized.Snapshot.Clone()
	if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) || snapshot.Identity.Generation == "" {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "STALE_CATALOG_SNAPSHOT", "reload the catalog before changing the selected population", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	owner, err := s.store.Get(ctx, projectid.Canonical(request.Project), request.ExplorerID)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return PopulationMemberRemovalProposalResponse{}, notFound("population-member-proposal", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before removing a selected member", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be compiled", nil, err)
	}
	workspaceDigest, err := workspace.Digest()
	if err != nil || workspaceDigest != owner.DraftDigest {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	documentIndex := constructionDocumentIndex(workspace, request.OutputID)
	if documentIndex < 0 {
		return PopulationMemberRemovalProposalResponse{}, unprocessable("population-member-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify one saved table", nil)
	}
	baseDocument := workspace.Documents[documentIndex]
	if baseDocument.Population == nil || baseDocument.Population.SelectionRevisionID != request.BaseSelectionID {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "STALE_POPULATION_SELECTION", "the requested immutable selection is not attached to this table", nil, nil)
	}
	baseDocumentDigest, err := documentDigest(baseDocument)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, fmt.Errorf("digest population proposal base document: %w", err)
	}
	baseSelection, err := s.getAuthorizedPopulationSelection(ctx, request.Project, request.BaseSelectionID, snapshot)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if baseSelection.ResourceType != request.RemovedMember.ResourceType {
		return PopulationMemberRemovalProposalResponse{}, unprocessable("population-member-proposal", "INVALID_POPULATION_MEMBER", "the removed member does not match the selected resource type", nil)
	}
	contains, err := selectionContainsExactMember(ctx, s.store, projectid.Canonical(request.Project), baseSelection.ID, request.RemovedMember)
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, internal("population-member-proposal", "SELECTION_READ_FAILED", "the selected resource set could not be inspected", err)
	}
	if !contains {
		return PopulationMemberRemovalProposalResponse{}, unprocessable("population-member-proposal", "POPULATION_MEMBER_NOT_FOUND", "the exact resource reference is not a member of the attached selection", nil)
	}

	created, err := s.CreateSelection(ctx, SelectionIntentCreateRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		IdempotencyKey: "population-member-removal-" + uuid.NewString(),
		Source:         SelectionSourceIntent{Kind: SelectionSourceSelectionRevision, SelectionRevisionID: baseSelection.ID},
		Exclusions:     []explorer.ResourceRef{request.RemovedMember},
	})
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	candidateSelection := created.Header
	if err := validatePopulationRemovalSelection(baseSelection, candidateSelection, request.RemovedMember, snapshot); err != nil {
		return PopulationMemberRemovalProposalResponse{}, conflict("population-member-proposal", "INVALID_CANDIDATE_SELECTION", "the server-created candidate selection failed exact removal validation", nil, err)
	}
	routeChoiceID, err := s.populationRemovalRouteChoice(ctx, request, baseDocument, candidateSelection.ID)
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
	candidateDigest, err := candidateWorkspace.Digest()
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
	receipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "population-member-removal-candidate",
		PopulationMemberRemovalProposal: binding,
	})
	if err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "population-member-proposal", receipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &candidateWorkspace); err != nil {
		return PopulationMemberRemovalProposalResponse{}, err
	}
	if err := validateReceiptOutputContract(receipt, request.OutputID); err != nil {
		return PopulationMemberRemovalProposalResponse{}, unprocessable("population-member-proposal", "OUTPUT_NOT_FOUND", "the candidate receipt does not contain the requested output", err)
	}
	return PopulationMemberRemovalProposalResponse{
		ProposalID: receipt.ID, OutputID: request.OutputID, SnapshotToken: request.SnapshotToken,
		DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, BaseDocumentDigest: baseDocumentDigest,
		CandidateWorkspaceDigest: candidateDigest, BaseSelection: cloneSelectionHeader(baseSelection),
		CandidateSelection: cloneSelectionHeader(candidateSelection), RemovedMember: request.RemovedMember,
		PreviewStatus: "PREVIEW_PENDING",
	}, nil
}

type selectionMemberReader interface {
	VisitSelectionMembers(context.Context, string, string, string, int, func(explorer.SelectionMember) error) (string, error)
}

func selectionContainsExactMember(ctx context.Context, store selectionMemberReader, project, selectionID string, target explorer.ResourceRef) (bool, error) {
	target = target.Canonical()
	after := ""
	maxPages := int((DefaultSelectionMaxRows+selectionBatchSize-1)/selectionBatchSize) + 1
	for page := 0; page < maxPages; page++ {
		found := false
		next, err := store.VisitSelectionMembers(ctx, project, selectionID, after, selectionBatchSize, func(member explorer.SelectionMember) error {
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

func validatePopulationRemovalSelection(base, candidate *explorer.SelectionRevision, removed explorer.ResourceRef, snapshot capability.Snapshot) error {
	if base == nil || candidate == nil {
		return fmt.Errorf("base and candidate selection headers are required")
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	project, generation, scope := projectid.Canonical(snapshot.Identity.Project), snapshot.Identity.Generation, snapshot.Identity.AuthorizationScopeDigest
	for name, selection := range map[string]*explorer.SelectionRevision{"base": base, "candidate": candidate} {
		if !selection.Complete || projectid.Canonical(selection.Project) != project || selection.Generation != generation || selection.ScopeDigest != scope {
			return fmt.Errorf("%s selection is incomplete or outside the authorized project, generation, or scope", name)
		}
	}
	if base.ID == candidate.ID || base.MembershipDigest == candidate.MembershipDigest ||
		base.Project != candidate.Project || base.Generation != candidate.Generation || base.ScopeDigest != candidate.ScopeDigest || base.ResourceType != candidate.ResourceType {
		return fmt.Errorf("candidate selection does not retain the exact base selection scope")
	}
	removed = removed.Canonical()
	if err := removed.Validate(project, generation, base.ResourceType); err != nil {
		return err
	}
	if base.MemberCount < 1 || candidate.MemberCount != base.MemberCount-1 {
		return fmt.Errorf("candidate selection does not remove exactly one member")
	}
	if candidate.Source.Kind != explorer.SelectionSourceRevision || candidate.Source.RevisionID != base.ID || candidate.Source.Generation != base.Generation ||
		candidate.Source.ResourceType != base.ResourceType || candidate.Source.MembershipDigest != base.MembershipDigest ||
		candidate.Rule.Kind != explorer.SelectionRuleExplicit || len(candidate.Exclusions) != 1 || candidate.Exclusions[0].Canonical() != removed {
		return fmt.Errorf("candidate selection provenance does not identify the exact base-minus-member derivation")
	}
	return nil
}

func cloneSelectionHeader(selection *explorer.SelectionRevision) *explorer.SelectionRevision {
	if selection == nil {
		return nil
	}
	cloned := selection.Canonical()
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

func populationRemovalRouteMatches(populationRoute []authoringv2.PopulationRouteStep, signedRoute []capability.ConstructionRouteStep) bool {
	if len(populationRoute) != len(signedRoute) {
		return false
	}
	for index, step := range populationRoute {
		signed := signedRoute[index]
		if step.ResourceType != signed.ToResourceType || step.Relationship != signed.Relationship ||
			(step.CatalogEdgeID != "" && step.CatalogEdgeID != signed.EdgeID) ||
			(step.StorageDirection != "" && step.StorageDirection != signed.StorageDirection) {
			return false
		}
	}
	return true
}

func (s *Service) populationRemovalRouteChoice(ctx context.Context, request PopulationMemberRemovalProposalRequest, document authoringv2.Document, candidateSelectionID string) (string, error) {
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
			if populationRemovalRouteMatches(document.Population.Route, choice.Route) {
				return choice.RouteChoiceID, nil
			}
		}
		if page.NextCursor == "" {
			break
		}
		if seenCursors[page.NextCursor] || page.NextCursor == cursor {
			return "", conflict("population-member-proposal", "INVALID_POPULATION_ROUTE", "the route search did not advance", nil, nil)
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return "", unprocessable("population-member-proposal", "INVALID_POPULATION_ROUTE", "the existing population route is no longer available for the candidate selection", nil)
}

func (s *Service) preparePopulationMemberRemovalProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	snapshot := authorized.Snapshot
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyPopulationMemberProposal {
		return nil, nil, malformed("commands", "APPLY_POPULATION_MEMBER_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID || receipt.PopulationMemberRemovalProposal == nil {
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the proposal ID does not identify a population member removal receipt", nil, nil)
	}
	binding := receipt.PopulationMemberRemovalProposal
	if binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil || currentDigest != binding.DraftDigest {
		return nil, nil, conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the saved draft no longer matches the proposal base", nil, err)
	}
	baseIndex := constructionDocumentIndex(current, command.OutputID)
	if baseIndex < 0 {
		return nil, nil, conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the proposal output is missing or duplicated in the saved draft", nil, nil)
	}
	baseDocument := current.Documents[baseIndex]
	baseDocumentSHA, err := documentDigest(baseDocument)
	if err != nil || baseDocumentSHA != binding.BaseDocumentDigest || baseDocument.Population == nil || baseDocument.Population.SelectionRevisionID != binding.BaseSelectionRevisionID {
		return nil, nil, conflict("commands", "STALE_POPULATION_MEMBER_PROPOSAL", "the target table or attached selection changed after proposal creation", nil, err)
	}
	baseSelection, err := s.getAuthorizedPopulationSelection(ctx, project, binding.BaseSelectionRevisionID, snapshot)
	if err != nil {
		return nil, nil, err
	}
	candidateSelection, err := s.getAuthorizedPopulationSelection(ctx, project, binding.CandidateSelectionRevisionID, snapshot)
	if err != nil {
		return nil, nil, err
	}
	containsRemovedMember, memberReadErr := selectionContainsExactMember(ctx, s.store, projectid.Canonical(project), baseSelection.ID, binding.RemovedMember)
	if validationErr := validatePopulationRemovalSelection(baseSelection, candidateSelection, binding.RemovedMember, snapshot); validationErr != nil {
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the immutable selection revisions no longer prove the exact removed member", nil, validationErr)
	}
	if baseSelection.MembershipDigest != binding.BaseMembershipDigest || baseSelection.MemberCount != binding.BaseMemberCount ||
		candidateSelection.MembershipDigest != binding.CandidateMembershipDigest || candidateSelection.MemberCount != binding.CandidateMemberCount ||
		memberReadErr != nil || !containsRemovedMember {
		cause := memberReadErr
		if cause == nil {
			cause = fmt.Errorf("the immutable selection revisions do not match the proposal binding")
		}
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the immutable selection revisions no longer prove the exact removed member", nil, cause)
	}
	identity, decodeErr := capability.DecodePopulationRouteChoiceID(binding.RouteChoiceID)
	if decodeErr != nil || identity.SnapshotToken != request.SnapshotToken || identity.OutputID != command.OutputID || identity.SelectionRevisionID != candidateSelection.ID ||
		!populationRemovalRouteMatches(baseDocument.Population.Route, identity.Route) {
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the receipt route is not bound to the exact candidate selection and retained table route", nil, decodeErr)
	}
	synthetic := authoringv2.Command{Type: authoringv2.CommandSetTablePopulation, OutputID: command.OutputID, SelectionRevisionID: candidateSelection.ID, RouteChoiceID: binding.RouteChoiceID}
	preparedRoute, err := s.preparePopulationRouteChoices(ctx, project, authorized, []capability.PopulationRouteChoiceIdentity{identity}, current, []authoringv2.Command{synthetic})
	if err != nil {
		return nil, nil, err
	}
	if len(preparedRoute) != 1 || !populationRemovalRouteMatches(baseDocument.Population.Route, identity.Route) {
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the retained population route could not be reauthorized exactly", nil, nil)
	}
	expectedWorkspace := current
	expectedWorkspace.Documents = append([]authoringv2.Document(nil), current.Documents...)
	expectedDocument := baseDocument
	expectedPopulation := *baseDocument.Population
	expectedPopulation.Route = clonePopulationRouteForProposal(baseDocument.Population.Route)
	expectedPopulation.SelectionRevisionID = candidateSelection.ID
	expectedDocument.Population = &expectedPopulation
	expectedWorkspace.Documents[baseIndex] = expectedDocument
	expectedDigest, err := expectedWorkspace.Digest()
	if err != nil || expectedDigest != binding.CandidateWorkspaceDigest || expectedDigest != receipt.IntentDigest {
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the receipt does not represent the exact base workspace with only its population revision replaced", nil, err)
	}
	if _, err := s.verifyProposalReceipt(ctx, "population-member-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, &expectedWorkspace); err != nil {
		return nil, nil, err
	}
	if err := command.ResolvePopulationMemberProposal(candidateSelection.ID, baseDocument.Population.Route); err != nil {
		return nil, nil, conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the candidate population is invalid", nil, err)
	}
	return commands, receipt, nil
}

func checkPopulationMemberRemovalProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
		return conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied population member workspace: %w", err)
	}
	binding := receipt.PopulationMemberRemovalProposal
	if digest != binding.CandidateWorkspaceDigest || digest != receipt.IntentDigest {
		return conflict("commands", "POPULATION_MEMBER_PROPOSAL_MISMATCH", "the applied population does not match the exact candidate receipt workspace", nil, nil)
	}
	return nil
}

func previewPopulationMemberRemovalProposal(ctx context.Context, service *Service, project, explorerID string, receipt *explorer.CompilationReceipt) error {
	if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
		return conflict("commands", "INVALID_POPULATION_MEMBER_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	binding := receipt.PopulationMemberRemovalProposal
	if _, err := service.Preview(ctx, PreviewRequest{
		Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: binding.OutputID,
		Limit: binding.PreviewLimit,
		SinkFactory: func(_ *explorer.CompilationReceipt, _ []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		return conflict("commands", "POPULATION_MEMBER_PREVIEW_FAILED", "the exact candidate preview failed; the accepted draft remains unchanged", nil, err)
	}
	return nil
}
