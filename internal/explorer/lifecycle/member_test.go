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

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/population"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	explorerfixtures "github.com/calypr/loom/internal/testkit/explorerfixtures"
)

type populationProposalTestStore struct {
	*fakeStore
	selections map[string]explorerselection.SelectionRevision
	members    map[string][]explorerselection.SelectionMember
}

func (s *populationProposalTestStore) GetSelection(ctx context.Context, project, id string) (*explorerselection.SelectionRevision, error) {
	if header, ok := s.selections[id]; ok {
		copy := header.Canonical()
		return &copy, nil
	}
	if s.fakeStore.selection != nil && s.fakeStore.selection.ID == id {
		return s.fakeStore.GetSelection(ctx, project, id)
	}
	return nil, explorerselection.ErrSelectionNotFound
}

func (s *populationProposalTestStore) VisitSelectionMembers(_ context.Context, project, id, after string, limit int, visit func(explorerselection.SelectionMember) error) (string, error) {
	members, ok := s.members[id]
	if !ok && s.fakeStore.selection != nil && s.fakeStore.selection.ID == id {
		members, ok = s.fakeStore.selectionMembers, true
	}
	if !ok {
		return "", explorerselection.ErrSelectionNotFound
	}
	ordered := append([]explorerselection.SelectionMember(nil), members...)
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

func (s *populationProposalTestStore) BeginSelection(_ context.Context, header explorerselection.SelectionRevision, _ string) (*explorerselection.SelectionRevision, error) {
	if current, ok := s.selections[header.ID]; ok {
		copy := current
		return &copy, nil
	}
	header = header.Canonical()
	s.selections[header.ID] = header
	s.members[header.ID] = []explorerselection.SelectionMember{}
	copy := header
	return &copy, nil
}

func (s *populationProposalTestStore) AppendSelectionMembers(_ context.Context, id, _ string, members []explorerselection.SelectionMember) ([]explorerselection.SelectionMember, error) {
	if _, ok := s.selections[id]; !ok {
		return nil, explorerselection.ErrSelectionNotFound
	}
	added := make([]explorerselection.SelectionMember, 0, len(members))
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
		return "", 0, 0, explorerselection.ErrSelectionNotFound
	}
	var bytes int64
	for _, member := range members {
		ref := member.Ref.Canonical()
		for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
			bytes += int64(8 + len(value))
		}
	}
	return explorerselection.MembershipDigest(members), int64(len(members)), bytes, nil
}

func (s *populationProposalTestStore) CompleteSelection(_ context.Context, id, _ string, digest string, count, bytes int64, completedAt time.Time) (*explorerselection.SelectionRevision, error) {
	header, ok := s.selections[id]
	if !ok {
		return nil, explorerselection.ErrSelectionNotFound
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
	ctx context.Context, project, id string, catalog ws.CatalogSnapshot, request authoringv2.ApplyCommandsRequest,
	actor string, prepare func(context.Context, ws.Workspace, []authoringv2.Command) ([]authoringv2.Command, error),
	checker func(ws.Workspace) error,
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
	workspace, err := ws.DecodeWorkspace(owner.DraftConfig)
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

func populationMemberProposalFixture(t *testing.T) (*Service, *populationProposalTestStore, capability.Snapshot, ws.Workspace) {
	t.Helper()
	domain, config, store, snapshot, workspace := populationMemberProposalSetup(t)
	return newPopulationMemberProposalService(t, domain, config), store, snapshot, workspace
}

func newPopulationMemberProposalService(t *testing.T, domain *explorer.Service, config Config) *Service {
	t.Helper()
	service, err := New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func populationMemberProposalSetup(t *testing.T) (*explorer.Service, Config, *populationProposalTestStore, capability.Snapshot, ws.Workspace) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	scopeBase := readySnapshot("project-a", "generation-a", "snapshot-base", scope)
	snapshot := capability.NewSnapshot(scopeBase.Identity, capability.Policy{Route: capability.RoutePolicy{Version: "route-v1", MaxHops: 8}}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	workspace := lifecycleCandidatePreviewWorkspace()
	workspace.SemanticsVersion = ws.CurrentSemanticsVersion
	workspace.Documents[0].Rows = ws.RecordsRowDefinition()
	workspace.Documents[0].Population = &ws.Population{SelectionRevisionID: "selection-base", Route: []ws.PopulationRouteStep{}}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	members := []explorerselection.SelectionMember{
		{Ref: explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-1"}},
		{Ref: explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}},
	}
	baseHeader := explorerselection.SelectionRevision{
		ID: "selection-base", Project: "project-a", Generation: "generation-a", ResourceType: "Patient",
		Rule: explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit}, Source: explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "base-rule", MembershipDigest: explorerselection.MembershipDigest(members),
		MemberCount: int64(len(members)), MemberBytes: 100, Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	fake := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: raw, DraftVersion: 7, DraftDigest: digest}, copyGet: true,
		selection: &baseHeader, selectionMembers: members}
	store := &populationProposalTestStore{fakeStore: fake, selections: map[string]explorerselection.SelectionRevision{}, members: map[string][]explorerselection.SelectionMember{}}
	domain, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(snapshot)
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	config.Capability.ForExecution = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) ws.CatalogSnapshot {
		return lifecycleInterpretationCatalog(snapshot, explorerID)
	}
	config.SelectionReferenceValidator = func(_ context.Context, project, generation string, _ authscope.ReadScope, refs []explorerselection.ResourceRef) error {
		for _, ref := range refs {
			if err := ref.Validate(project, generation, "Patient"); err != nil {
				return err
			}
		}
		return nil
	}
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
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
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.fakeStore.receipt = receipt
		return receipt, nil
	}
	config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
			return dataframeexecution.PreviewSummary{}, errors.New("candidate proposal receipt was not used for preview")
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	return domain, config, store, snapshot, workspace
}

func populationProposalRequest(store *populationProposalTestStore, snapshot capability.Snapshot, member explorerselection.ResourceRef) population.PopulationMemberRemovalProposalRequest {
	return population.PopulationMemberRemovalProposalRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", BaseSelectionID: "selection-base", RemovedMember: member,
	}
}

func TestPopulationMemberRemovalProposalCreatesExactRevisionAndAppliesOnlyPopulationChange(t *testing.T) {
	service, store, snapshot, before := populationMemberProposalFixture(t)
	removed := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
	proposal, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
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
	if _, err := store.VisitSelectionMembers(context.Background(), "project-a", proposal.CandidateSelection.ID, "", 100, func(member explorerselection.SelectionMember) error {
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
		CommandID: "apply-population-removal", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
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
	domain, config, store, _, _ := populationMemberProposalSetup(t)
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
	compileScope := scope
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: compileScope}, nil
	}
	config.Capability.ForExecution = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	compilerScope := constructionCompilerScope(ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope})
	if compilerScope.AuthScopeMode != scope.Mode || !reflect.DeepEqual(compilerScope.AuthResourcePaths, scope.AuthResourcePaths) {
		t.Fatalf("route proof compiler scope = %#v, want exact authorized scope %#v", compilerScope, scope)
	}
	var previewBindings recipe.RuntimeBindings
	config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.PopulationMemberRemovalProposal == nil {
			return dataframeexecution.PreviewSummary{}, errors.New("preview did not receive the population removal receipt")
		}
		previewBindings = bindings
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
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
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.fakeStore.receipt = receipt
		return receipt, nil
	}
	config.Capability.Catalog = func(_ capability.Snapshot, explorerID string) ws.CatalogSnapshot {
		return ws.CatalogSnapshot{
			APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: explorerID,
			SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
			SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
			Nodes: []ws.CatalogNode{
				{ID: "specimen-node", ResourceType: "Specimen", RowRootEligible: true},
				{ID: "patient-node", ResourceType: "Patient"},
				{ID: "observation-node", ResourceType: "Observation"},
			},
			Edges: []ws.CatalogEdge{
				{ID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "patient-node", Label: "subject_Patient", StorageDirection: "OUTBOUND"},
				{ID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", Label: "subject_Patient", StorageDirection: "INBOUND"},
			},
		}
	}
	config.SelectionReferenceValidator = func(_ context.Context, project, generation string, _ authscope.ReadScope, refs []explorerselection.ResourceRef) error {
		for _, ref := range refs {
			if err := ref.Validate(project, generation, "Observation"); err != nil {
				return err
			}
		}
		return nil
	}

	members := []explorerselection.SelectionMember{
		{Ref: explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Observation", ID: "observation-1"}},
		{Ref: explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Observation", ID: "observation-2"}},
	}
	completedAt := time.Now().UTC()
	baseHeader := explorerselection.SelectionRevision{
		ID: "selection-base", Project: "project-a", Generation: "generation-a", ResourceType: "Observation",
		Rule: explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit}, Source: explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "base-route-rule", MembershipDigest: explorerselection.MembershipDigest(members),
		MemberCount: int64(len(members)), MemberBytes: 100, Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	store.fakeStore.selection, store.fakeStore.selectionMembers = &baseHeader, members
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].RootResourceType = "Specimen"
	workspace.Documents[0].Route.ResourceType = "Specimen"
	workspace.Documents[0].Population = &ws.Population{SelectionRevisionID: "selection-base"}
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	service := newPopulationMemberProposalService(t, domain, config)
	baseRoutes, err := service.OwnerServices().Population.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-base",
	})
	if err != nil || !baseRoutes.Complete || len(baseRoutes.Choices) != 1 || len(baseRoutes.Choices[0].Route) != 2 {
		t.Fatalf("base Specimen → Patient → Observation route = %#v, %v", baseRoutes, err)
	}
	baseRoute := make([]ws.PopulationRouteStep, 0, 2)
	for _, hop := range baseRoutes.Choices[0].Route {
		baseRoute = append(baseRoute, ws.PopulationRouteStep{
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
	proposal, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
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
	currentWorkspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.preparePopulationRouteChoices(context.Background(), "project-a", ports.AuthorizedCapability{Snapshot: snapshot},
		[]capability.PopulationRouteChoiceIdentity{choice}, currentWorkspace,
		[]authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: proposal.CandidateSelection.ID, RouteChoiceID: binding.RouteChoiceID}}}); err == nil {
		t.Fatal("population route proof accepted a snapshot without its restricted authorized scope")
	}

	request := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-routed-population-removal", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}
	beforeRejectedApply := append([]byte(nil), store.created.DraftConfig...)
	beforeRejectedDigest, beforeRejectedVersion := store.created.DraftDigest, store.created.DraftVersion
	wrongScope := scope
	wrongScope.AuthResourcePaths = []string{"/programs/beta"}
	compileScope = wrongScope
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("proposal Apply accepted a changed authorization scope")
	}
	if store.fakeStore.saveDraftCalls != 0 || store.created.DraftVersion != beforeRejectedVersion || store.created.DraftDigest != beforeRejectedDigest || string(store.created.DraftConfig) != string(beforeRejectedApply) {
		t.Fatalf("changed-scope rejection mutated the saved draft: saves=%d version=%d/%d digest=%q/%q configEqual=%t", store.fakeStore.saveDraftCalls, store.created.DraftVersion, beforeRejectedVersion, store.created.DraftDigest, beforeRejectedDigest, string(store.created.DraftConfig) == string(beforeRejectedApply))
	}
	compileScope = scope
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
		change func(*population.PopulationMemberRemovalProposalRequest)
	}{
		{"stale version", func(r *population.PopulationMemberRemovalProposalRequest) { r.ExpectedDraftVersion-- }},
		{"stale digest", func(r *population.PopulationMemberRemovalProposalRequest) { r.ExpectedDraftDigest = "sha256:stale" }},
		{"wrong project", func(r *population.PopulationMemberRemovalProposalRequest) { r.RemovedMember.Project = "other-project" }},
		{"wrong generation", func(r *population.PopulationMemberRemovalProposalRequest) {
			r.RemovedMember.Generation = "old-generation"
		}},
		{"foreign member", func(r *population.PopulationMemberRemovalProposalRequest) { r.RemovedMember.ID = "patient-foreign" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := populationMemberProposalFixture(t)
			member := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
			request := populationProposalRequest(store, snapshot, member)
			test.change(&request)
			if _, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), request); err == nil {
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
		domain, config, store, snapshot, _ := populationMemberProposalSetup(t)
		preview := config.PreviewReceipt
		failPreview := false
		config.PreviewReceipt = func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, sink func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if failPreview {
				return dataframeexecution.PreviewSummary{}, errors.New("candidate preview rejected")
			}
			return preview(ctx, receipt, bindings, sink)
		}
		service := newPopulationMemberProposalService(t, domain, config)
		removed := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
		proposal, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
		if err != nil {
			t.Fatal(err)
		}
		failPreview = true
		beforeDigest, beforeVersion, saveCalls := store.created.DraftDigest, store.created.DraftVersion, store.fakeStore.saveDraftCalls
		request := authoringv2.ApplyCommandsRequest{
			CommandID: "apply-preview-fail", SemanticsVersion: ws.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
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
		removed := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
		proposal, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
		if err != nil {
			t.Fatal(err)
		}
		request := authoringv2.ApplyCommandsRequest{
			CommandID: "apply-population-once", SemanticsVersion: ws.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
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
	removed := explorerselection.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Patient", ID: "patient-2"}
	_, err := service.OwnerServices().Population.ProposePopulationMemberRemoval(context.Background(), populationProposalRequest(store, snapshot, removed))
	if err != nil {
		t.Fatal(err)
	}
	forged := *store.fakeStore.receipt
	binding := *forged.PopulationMemberRemovalProposal
	binding.RouteChoiceID = "route-substituted"
	forged.PopulationMemberRemovalProposal = &binding
	forged.CompilationKey, err = receiptmodel.CompilationKey(forged)
	if err != nil {
		t.Fatal(err)
	}
	forged.ID, err = receiptmodel.ReceiptID(forged)
	if err != nil {
		t.Fatal(err)
	}
	store.fakeStore.receipt = &forged
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-route-substitution", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: forged.ID}}},
	}
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("substituted route choice was accepted")
	}
	if store.fakeStore.saveDraftCalls != 0 {
		t.Fatal("route substitution changed the saved draft")
	}
}
