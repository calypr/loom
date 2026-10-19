package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type populationProposalTestStore struct {
	*fakeStore
	selections map[string]explorer.SelectionRevision
	members    map[string][]explorer.SelectionMember
}

func (s *populationProposalTestStore) GetSelection(ctx context.Context, project, id string) (*explorer.SelectionRevision, error) {
	if header, ok := s.selections[id]; ok {
		copy := header.Canonical()
		return &copy, nil
	}
	if s.fakeStore.selection != nil && s.fakeStore.selection.ID == id {
		return s.fakeStore.GetSelection(ctx, project, id)
	}
	return nil, explorer.ErrSelectionNotFound
}

func (s *populationProposalTestStore) VisitSelectionMembers(_ context.Context, project, id, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	members, ok := s.members[id]
	if !ok && s.fakeStore.selection != nil && s.fakeStore.selection.ID == id {
		members, ok = s.fakeStore.selectionMembers, true
	}
	if !ok {
		return "", explorer.ErrSelectionNotFound
	}
	ordered := append([]explorer.SelectionMember(nil), members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ref.ID < ordered[j].Ref.ID })
	start := 0
	for start < len(ordered) && ordered[start].Ref.ID <= after {
		start++
	}
	if limit <= 0 {
		limit = len(ordered)
	}
	end := start + limit
	if end > len(ordered) {
		end = len(ordered)
	}
	for _, member := range ordered[start:end] {
		if err := visit(member.Canonical()); err != nil {
			return "", err
		}
	}
	if end == start || end == len(ordered) {
		return "", nil
	}
	return ordered[end-1].Ref.ID, nil
}

func (s *populationProposalTestStore) BeginSelection(_ context.Context, header explorer.SelectionRevision, _ string) (*explorer.SelectionRevision, error) {
	if current, ok := s.selections[header.ID]; ok {
		copy := current
		return &copy, nil
	}
	header = header.Canonical()
	s.selections[header.ID] = header
	s.members[header.ID] = []explorer.SelectionMember{}
	copy := header
	return &copy, nil
}

func (s *populationProposalTestStore) AppendSelectionMembers(_ context.Context, id, _ string, members []explorer.SelectionMember) ([]explorer.SelectionMember, error) {
	if _, ok := s.selections[id]; !ok {
		return nil, explorer.ErrSelectionNotFound
	}
	added := make([]explorer.SelectionMember, 0, len(members))
	for _, member := range members {
		member = member.Canonical()
		found := false
		for _, prior := range s.members[id] {
			if prior.Ref == member.Ref {
				found = true
				break
			}
		}
		if !found {
			s.members[id] = append(s.members[id], member)
			added = append(added, member)
		}
	}
	return added, nil
}

func (s *populationProposalTestStore) DigestSelectionMembers(_ context.Context, project, id string) (string, int64, int64, error) {
	members, ok := s.members[id]
	if !ok {
		return "", 0, 0, explorer.ErrSelectionNotFound
	}
	var bytes int64
	for _, member := range members {
		ref := member.Ref.Canonical()
		for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
			bytes += int64(8 + len(value))
		}
	}
	return explorer.MembershipDigest(members), int64(len(members)), bytes, nil
}

func (s *populationProposalTestStore) CompleteSelection(_ context.Context, id, _ string, digest string, count, bytes int64, completedAt time.Time) (*explorer.SelectionRevision, error) {
	header, ok := s.selections[id]
	if !ok {
		return nil, explorer.ErrSelectionNotFound
	}
	header.MembershipDigest, header.MemberCount, header.MemberBytes = digest, count, bytes
	header.Complete, header.CompletedAt = true, &completedAt
	s.selections[id] = header
	copy := header
	return &copy, nil
}

func (s *populationProposalTestStore) AbortSelection(_ context.Context, id, _ string) error {
	delete(s.selections, id)
	delete(s.members, id)
	return nil
}

func (s *populationProposalTestStore) CleanupSelectionStaging(context.Context, time.Time, int) error {
	return nil
}

func (s *populationProposalTestStore) ApplyWorkspaceCommandsChecked(
	ctx context.Context, project, id string, catalog authoringv2.CatalogSnapshot, request authoringv2.ApplyCommandsRequest,
	actor string, prepare func(context.Context, authoringv2.Workspace, []authoringv2.Command) ([]authoringv2.Command, error),
	checker func(authoringv2.Workspace) error,
) (*authoringv2.ApplyCommandsResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	owner, err := s.fakeStore.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return nil, explorer.ErrDraftConflict
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return nil, err
	}
	commands := append([]authoringv2.Command(nil), request.Commands...)
	if prepare != nil {
		commands, err = prepare(ctx, workspace, commands)
		if err != nil {
			return nil, err
		}
	}
	updated, results, err := authoringv2.ApplyCommands(workspace, catalog, request.CommandID, commands)
	if err != nil {
		return nil, err
	}
	if checker != nil {
		if err := checker(updated); err != nil {
			return nil, err
		}
	}
	owner.DraftConfig, err = updated.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	owner.DraftDigest, err = updated.Digest()
	if err != nil {
		return nil, err
	}
	owner.LastAuthoringCommandID = request.CommandID
	owner.LastAuthoringCommandDigest, err = request.Digest()
	if err != nil {
		return nil, err
	}
	owner.UpdatedBy = actor
	stored, err := s.fakeStore.SaveDraft(ctx, *owner, request.ExpectedDraftVersion, request.ExpectedDraftDigest)
	if err != nil {
		return nil, err
	}
	return &authoringv2.ApplyCommandsResponse{CommandID: request.CommandID, Workspace: updated, DraftVersion: stored.DraftVersion, DraftDigest: stored.DraftDigest, Results: results, Diagnostics: []any{}}, nil
}

func populationMemberProposalFixture(t *testing.T) (*Service, *populationProposalTestStore, capability.Snapshot, authoringv2.Workspace) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	scopeBase := readySnapshot("project-a", "generation-a", "snapshot-base", scope)
	snapshot := capability.NewSnapshot(scopeBase.Identity, capability.Policy{Route: capability.RoutePolicy{Version: "route-v1", MaxHops: 8}}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	workspace := lifecycleCandidatePreviewWorkspace()
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	workspace.Documents[0].Population = &authoringv2.Population{SelectionRevisionID: "selection-base", Route: []authoringv2.PopulationRouteStep{}}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	members := []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-1"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}},
	}
	baseHeader := explorer.SelectionRevision{
		ID: "selection-base", Project: "project-a", Generation: "generation-a", ResourceType: "Patient",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "base-rule", MembershipDigest: explorer.MembershipDigest(members),
		MemberCount: int64(len(members)), MemberBytes: 100, Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	fake := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: raw, DraftVersion: 7, DraftDigest: digest}, copyGet: true,
		selection: &baseHeader, selectionMembers: members}
	store := &populationProposalTestStore{fakeStore: fake, selections: map[string]explorer.SelectionRevision{}, members: map[string][]explorer.SelectionMember{}}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(snapshot)
	config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	config.Capability.ForExecution = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return lifecycleInterpretationCatalog(snapshot, explorerID)
	}
	config.SelectionReferenceValidator = func(_ context.Context, project, generation string, _ authscope.ReadScope, refs []explorer.ResourceRef) error {
		for _, ref := range refs {
			if err := ref.Validate(project, generation, "Patient"); err != nil {
				return err
			}
		}
		return nil
	}
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := nativeReceipt(snapshot)
		receipt.IntentDigest, err = request.Workspace.Digest()
		if err != nil {
			return nil, err
		}
		receipt.NormalizedBundle, err = request.Workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		if request.PopulationMemberRemovalProposal != nil {
			binding := *request.PopulationMemberRemovalProposal
			receipt.PopulationMemberRemovalProposal = &binding
		}
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.fakeStore.receipt = receipt
		return receipt, nil
	}
	config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
			return dataframeexecution.PreviewSummary{}, errors.New("candidate proposal receipt was not used for preview")
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	service, err := New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, snapshot, workspace
}

func populationProposalRequest(store *populationProposalTestStore, snapshot capability.Snapshot, member explorer.ResourceRef) PopulationMemberRemovalProposalRequest {
	return PopulationMemberRemovalProposalRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", BaseSelectionID: "selection-base", RemovedMember: member,
	}
}

func TestPopulationMemberRemovalProposalCreatesExactRevisionAndAppliesOnlyPopulationChange(t *testing.T) {
	service, store, snapshot, before := populationMemberProposalFixture(t)
	removed := explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
	proposal, err := service.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
	if err != nil {
		t.Fatal(err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" || proposal.BaseSelection == nil || proposal.CandidateSelection == nil {
		t.Fatalf("proposal = %#v", proposal)
	}
	if proposal.BaseSelection.MemberCount != 2 || proposal.CandidateSelection.MemberCount != 1 || proposal.CandidateSelection.ID == proposal.BaseSelection.ID {
		t.Fatalf("selection headers = base %#v candidate %#v", proposal.BaseSelection, proposal.CandidateSelection)
	}
	var candidateIDs []string
	if _, err := store.VisitSelectionMembers(context.Background(), "project-a", proposal.CandidateSelection.ID, "", 100, func(member explorer.SelectionMember) error {
		candidateIDs = append(candidateIDs, member.Ref.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(candidateIDs) != "[patient-1]" {
		t.Fatalf("candidate membership = %v, want exact base-minus-member [patient-1]", candidateIDs)
	}
	if store.fakeStore.saveDraftCalls != 0 {
		t.Fatalf("proposal mutated the saved draft %d times", store.fakeStore.saveDraftCalls)
	}

	request := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-population-removal", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response.DraftVersion != 8 || response.Workspace.Documents[0].Population == nil || response.Workspace.Documents[0].Population.SelectionRevisionID != proposal.CandidateSelection.ID {
		t.Fatalf("applied response = %#v", response)
	}
	beforeOther := before.Documents[0]
	beforeOther.Population = nil
	afterOther := response.Workspace.Documents[0]
	afterOther.Population = nil
	if !jsonEqual(beforeOther, afterOther) {
		t.Fatal("population member proposal changed authoring fields outside Population")
	}
	if store.fakeStore.selection == nil || store.fakeStore.selection.ID != "selection-base" {
		t.Fatal("base immutable selection was changed")
	}
	if store.fakeStore.saveDraftCalls != 1 {
		t.Fatalf("draft was saved %d times, want one checked apply", store.fakeStore.saveDraftCalls)
	}
}

func TestPopulationMemberRemovalProposalReissuesAndRetainsNonemptyRoute(t *testing.T) {
	service, store, _, _ := populationMemberProposalFixture(t)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/alpha"}}
	snapshotBase := readySnapshot("project-a", "generation-a", "population-removal-route", scope)
	snapshot := capability.NewSnapshot(snapshotBase.Identity,
		capability.Policy{Route: capability.RoutePolicy{Version: "route-v1", MaxHops: 8}},
		capability.StatusReady, true, false,
		[]capability.Node{
			{ID: "specimen-node", ResourceType: "Specimen", RowRootEligible: true},
			{ID: "patient-node", ResourceType: "Patient"},
			{ID: "observation-node", ResourceType: "Observation"},
		},
		[]capability.Edge{
			{ID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "patient-node", SourceResourceType: "Specimen", TargetResourceType: "Patient", Label: "subject_Patient", StorageDirection: "OUTBOUND"},
			{ID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND"},
		}, nil, nil)
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service.config.Capability.ForExecution = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	compilerScope := constructionCompilerScope(AuthorizedCapability{Snapshot: snapshot, Scope: scope})
	if compilerScope.AuthScopeMode != scope.Mode || !reflect.DeepEqual(compilerScope.AuthResourcePaths, scope.AuthResourcePaths) {
		t.Fatalf("route proof compiler scope = %#v, want exact authorized scope %#v", compilerScope, scope)
	}
	var previewBindings recipe.RuntimeBindings
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
			return dataframeexecution.PreviewSummary{}, errors.New("preview did not receive the population removal receipt")
		}
		previewBindings = bindings
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := nativeReceipt(snapshot)
		intentDigest, err := request.Workspace.Digest()
		if err != nil {
			return nil, err
		}
		receipt.IntentDigest = intentDigest
		receipt.NormalizedBundle, err = request.Workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		if request.PopulationMemberRemovalProposal != nil {
			binding := *request.PopulationMemberRemovalProposal
			receipt.PopulationMemberRemovalProposal = &binding
		}
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.fakeStore.receipt = receipt
		return receipt, nil
	}
	service.config.Capability.Catalog = func(_ capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: explorerID,
			SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
			SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes: []authoringv2.CatalogNode{
				{ID: "specimen-node", ResourceType: "Specimen", RowRootEligible: true},
				{ID: "patient-node", ResourceType: "Patient"},
				{ID: "observation-node", ResourceType: "Observation"},
			},
			Edges: []authoringv2.CatalogEdge{
				{ID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "patient-node", Label: "subject_Patient", StorageDirection: "OUTBOUND"},
				{ID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", Label: "subject_Patient", StorageDirection: "INBOUND"},
			},
		}
	}
	service.config.SelectionReferenceValidator = func(_ context.Context, project, generation string, _ authscope.ReadScope, refs []explorer.ResourceRef) error {
		for _, ref := range refs {
			if err := ref.Validate(project, generation, "Observation"); err != nil {
				return err
			}
		}
		return nil
	}

	members := []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Observation", ID: "observation-1"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Observation", ID: "observation-2"}},
	}
	completedAt := time.Now().UTC()
	baseHeader := explorer.SelectionRevision{
		ID: "selection-base", Project: "project-a", Generation: "generation-a", ResourceType: "Observation",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "base-route-rule", MembershipDigest: explorer.MembershipDigest(members),
		MemberCount: int64(len(members)), MemberBytes: 100, Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	store.fakeStore.selection, store.fakeStore.selectionMembers = &baseHeader, members
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].RootResourceType = "Specimen"
	workspace.Documents[0].Route.ResourceType = "Specimen"
	workspace.Documents[0].Population = &authoringv2.Population{SelectionRevisionID: "selection-base"}
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	baseRoutes, err := service.SearchPopulationRoutes(context.Background(), PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-base",
	})
	if err != nil || !baseRoutes.Complete || len(baseRoutes.Choices) != 1 || len(baseRoutes.Choices[0].Route) != 2 {
		t.Fatalf("base Specimen → Patient → Observation route = %#v, %v", baseRoutes, err)
	}
	baseRoute := make([]authoringv2.PopulationRouteStep, 0, 2)
	for _, hop := range baseRoutes.Choices[0].Route {
		baseRoute = append(baseRoute, authoringv2.PopulationRouteStep{
			ResourceType: hop.ToResourceType, Relationship: hop.Relationship,
			CatalogEdgeID: hop.EdgeID, StorageDirection: hop.StorageDirection,
		})
	}
	workspace.Documents[0].Population.Route = baseRoute
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	removed := members[1].Ref
	proposal, err := service.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
	if err != nil {
		t.Fatal(err)
	}
	binding := store.fakeStore.receipt.PopulationMemberRemovalProposal
	if binding == nil || binding.RouteChoiceID == "" {
		t.Fatalf("candidate receipt omitted the reissued route choice: %#v", binding)
	}
	choice, err := capability.DecodePopulationRouteChoiceID(binding.RouteChoiceID)
	if err != nil || choice.SelectionRevisionID != proposal.CandidateSelection.ID || len(choice.Route) != 2 {
		t.Fatalf("candidate signed route identity = %#v, %v", choice, err)
	}
	for index, want := range []struct{ edge, from, to, direction string }{
		{"specimen-patient", "Specimen", "Patient", "OUTBOUND"},
		{"patient-observation", "Patient", "Observation", "INBOUND"},
	} {
		got := choice.Route[index]
		if got.EdgeID != want.edge || got.FromResourceType != want.from || got.ToResourceType != want.to || got.StorageDirection != want.direction {
			t.Fatalf("candidate route hop %d = %#v, want %#v", index, got, want)
		}
	}
	currentWorkspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.preparePopulationRouteChoices(context.Background(), "project-a", AuthorizedCapability{Snapshot: snapshot},
		[]capability.PopulationRouteChoiceIdentity{choice}, currentWorkspace,
		[]authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", SelectionRevisionID: proposal.CandidateSelection.ID, RouteChoiceID: binding.RouteChoiceID}}); err == nil {
		t.Fatal("population route proof accepted a snapshot without its restricted authorized scope")
	}

	request := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-routed-population-removal", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}
	beforeRejectedApply := append([]byte(nil), store.created.DraftConfig...)
	beforeRejectedDigest, beforeRejectedVersion := store.created.DraftDigest, store.created.DraftVersion
	wrongScope := scope
	wrongScope.AuthResourcePaths = []string{"/programs/beta"}
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: wrongScope}, nil
	}
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("proposal Apply accepted a changed authorization scope")
	}
	if store.fakeStore.saveDraftCalls != 0 || store.created.DraftVersion != beforeRejectedVersion || store.created.DraftDigest != beforeRejectedDigest || string(store.created.DraftConfig) != string(beforeRejectedApply) {
		t.Fatalf("changed-scope rejection mutated the saved draft: saves=%d version=%d/%d digest=%q/%q configEqual=%t", store.fakeStore.saveDraftCalls, store.created.DraftVersion, beforeRejectedVersion, store.created.DraftDigest, beforeRejectedDigest, string(store.created.DraftConfig) == string(beforeRejectedApply))
	}
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if previewBindings.AuthScopeMode != scope.Mode || !reflect.DeepEqual(previewBindings.AuthResourcePaths, scope.AuthResourcePaths) {
		t.Fatalf("candidate preview bindings = %#v, want exact authorized scope %#v", previewBindings, scope)
	}
	applied := response.Workspace.Documents[0].Population
	if applied == nil || applied.SelectionRevisionID != proposal.CandidateSelection.ID || !reflect.DeepEqual(applied.Route, baseRoute) {
		t.Fatalf("applied population did not preserve the exact routed base-minus-one selection: %#v", applied)
	}
}

func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func TestPopulationMemberRemovalProposalRejectsStaleScopeAndForeignMember(t *testing.T) {
	cases := []struct {
		name   string
		change func(*PopulationMemberRemovalProposalRequest)
	}{
		{"stale version", func(r *PopulationMemberRemovalProposalRequest) { r.ExpectedDraftVersion-- }},
		{"stale digest", func(r *PopulationMemberRemovalProposalRequest) { r.ExpectedDraftDigest = "sha256:stale" }},
		{"wrong project", func(r *PopulationMemberRemovalProposalRequest) { r.RemovedMember.Project = "other-project" }},
		{"wrong generation", func(r *PopulationMemberRemovalProposalRequest) { r.RemovedMember.Generation = "old-generation" }},
		{"foreign member", func(r *PopulationMemberRemovalProposalRequest) { r.RemovedMember.ID = "patient-foreign" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := populationMemberProposalFixture(t)
			member := explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
			request := populationProposalRequest(store, snapshot, member)
			test.change(&request)
			if _, err := service.ProposePopulationMemberRemoval(context.Background(), request); err == nil {
				t.Fatal("invalid proposal request was accepted")
			}
			if store.fakeStore.saveDraftCalls != 0 {
				t.Fatal("invalid proposal request changed the saved draft")
			}
		})
	}
}

func TestPopulationMemberRemovalPreviewFailureLeavesDraftUnchangedAndApplyReplaysFailCAS(t *testing.T) {
	t.Run("preview error", func(t *testing.T) {
		service, store, snapshot, _ := populationMemberProposalFixture(t)
		removed := explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
		proposal, err := service.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
		if err != nil {
			t.Fatal(err)
		}
		service.config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			return dataframeexecution.PreviewSummary{}, errors.New("candidate preview rejected")
		}
		beforeDigest, beforeVersion, saveCalls := store.created.DraftDigest, store.created.DraftVersion, store.fakeStore.saveDraftCalls
		request := authoringv2.ApplyCommandsRequest{
			CommandID: "apply-preview-fail", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
		}
		if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
			t.Fatal("candidate preview failure was accepted")
		}
		if store.created.DraftDigest != beforeDigest || store.created.DraftVersion != beforeVersion || store.fakeStore.saveDraftCalls != saveCalls {
			t.Fatal("preview failure changed the saved draft")
		}
	})
	t.Run("replay after successful apply is stale CAS", func(t *testing.T) {
		service, store, snapshot, _ := populationMemberProposalFixture(t)
		removed := explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
		proposal, err := service.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
		if err != nil {
			t.Fatal(err)
		}
		request := authoringv2.ApplyCommandsRequest{
			CommandID: "apply-population-once", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
		}
		if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err != nil {
			t.Fatal(err)
		}
		replay := request
		replay.CommandID = "apply-population-replay"
		if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", replay, "alice"); err == nil {
			t.Fatal("stale proposal replay with a new command ID was accepted")
		}
		if store.fakeStore.saveDraftCalls != 1 {
			t.Fatalf("replayed apply saved %d times", store.fakeStore.saveDraftCalls)
		}
	})
}

func TestPopulationMemberRemovalProposalRejectsRouteSubstitution(t *testing.T) {
	service, store, snapshot, _ := populationMemberProposalFixture(t)
	removed := explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
	_, err := service.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
	if err != nil {
		t.Fatal(err)
	}
	forged := *store.fakeStore.receipt
	binding := *forged.PopulationMemberRemovalProposal
	binding.RouteChoiceID = "route-substituted"
	forged.PopulationMemberRemovalProposal = &binding
	forged.CompilationKey, err = explorer.CompilationKey(forged)
	if err != nil {
		t.Fatal(err)
	}
	forged.ID, err = explorer.ReceiptID(forged)
	if err != nil {
		t.Fatal(err)
	}
	store.fakeStore.receipt = &forged
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-route-substitution", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: forged.ID}},
	}
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("substituted route choice was accepted")
	}
	if store.fakeStore.saveDraftCalls != 0 {
		t.Fatal("route substitution changed the saved draft")
	}
}
