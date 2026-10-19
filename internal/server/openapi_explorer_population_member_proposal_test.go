package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type populationProposalHTTPStore struct {
	*testExplorerStore
	selectionMu sync.Mutex
	selections  map[string]explorer.SelectionRevision
	members     map[string][]explorer.SelectionMember
	saveCalls   int
}

func (s *populationProposalHTTPStore) SaveDraft(ctx context.Context, value explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	s.saveCalls++
	return s.testExplorerStore.SaveDraft(ctx, value, expected, expectedDigest...)
}

func (s *populationProposalHTTPStore) GetSelection(_ context.Context, project, id string) (*explorer.SelectionRevision, error) {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	header, ok := s.selections[id]
	if !ok || header.Project != project {
		return nil, explorer.ErrSelectionNotFound
	}
	copy := header.Canonical()
	return &copy, nil
}

func (s *populationProposalHTTPStore) VisitSelectionMembers(_ context.Context, project, id, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	s.selectionMu.Lock()
	members, ok := s.members[id]
	if !ok || s.selections[id].Project != project {
		s.selectionMu.Unlock()
		return "", explorer.ErrSelectionNotFound
	}
	ordered := append([]explorer.SelectionMember(nil), members...)
	s.selectionMu.Unlock()
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

func (s *populationProposalHTTPStore) BeginSelection(_ context.Context, header explorer.SelectionRevision, _ string) (*explorer.SelectionRevision, error) {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	if current, ok := s.selections[header.ID]; ok && current.Complete {
		copy := current.Canonical()
		return &copy, nil
	}
	header = header.Canonical()
	s.selections[header.ID] = header
	s.members[header.ID] = []explorer.SelectionMember{}
	copy := header.Canonical()
	return &copy, nil
}

func (s *populationProposalHTTPStore) AppendSelectionMembers(_ context.Context, id, _ string, members []explorer.SelectionMember) ([]explorer.SelectionMember, error) {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	if _, ok := s.selections[id]; !ok {
		return nil, explorer.ErrSelectionNotFound
	}
	added := make([]explorer.SelectionMember, 0, len(members))
	for _, member := range members {
		member = member.Canonical()
		duplicate := false
		for _, prior := range s.members[id] {
			if prior.Ref == member.Ref {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.members[id] = append(s.members[id], member)
			added = append(added, member)
		}
	}
	return added, nil
}

func (s *populationProposalHTTPStore) DigestSelectionMembers(_ context.Context, project, id string) (string, int64, int64, error) {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	members, ok := s.members[id]
	if !ok || s.selections[id].Project != project {
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

func (s *populationProposalHTTPStore) CompleteSelection(_ context.Context, id, _ string, digest string, count, bytes int64, completedAt time.Time) (*explorer.SelectionRevision, error) {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	header, ok := s.selections[id]
	if !ok {
		return nil, explorer.ErrSelectionNotFound
	}
	header.MembershipDigest, header.MemberCount, header.MemberBytes = digest, count, bytes
	header.Complete, header.CompletedAt = true, &completedAt
	s.selections[id] = header
	copy := header.Canonical()
	return &copy, nil
}

func (s *populationProposalHTTPStore) AbortSelection(_ context.Context, id, _ string) error {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	delete(s.selections, id)
	delete(s.members, id)
	return nil
}

func (s *populationProposalHTTPStore) CleanupSelectionStaging(context.Context, time.Time, int) error {
	return nil
}

func TestPopulationMemberRemovalProposalHTTPPreviewsAndAppliesExactScopedCAS(t *testing.T) {
	const project, explorerID = "project-a", "custom"
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/alpha"}}
	baseSnapshot := testAuthoringV2CapabilitySnapshot()
	identity := baseSnapshot.Identity
	identity.AuthorizationScopeDigest = explorerScopeDigest(scope)
	snapshot := capability.NewSnapshot(identity, baseSnapshot.Policy, baseSnapshot.Status, baseSnapshot.Complete, baseSnapshot.Truncated,
		baseSnapshot.Nodes, baseSnapshot.Edges, baseSnapshot.Candidates, baseSnapshot.Diagnostics)

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	workspace.Documents[0].Population = &authoringv2.Population{SelectionRevisionID: "selection-base", Route: []authoringv2.PopulationRouteStep{}}
	catalog := authoringV2Catalog(snapshot, explorerID)
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	rawDraft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	draftDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	store := &populationProposalHTTPStore{
		testExplorerStore: newTestExplorerStore(), selections: map[string]explorer.SelectionRevision{}, members: map[string][]explorer.SelectionMember{},
	}
	if _, err := store.create(explorer.Explorer{Project: project, ExplorerID: explorerID, Title: "Patients", DraftConfig: rawDraft, DraftVersion: 7, DraftDigest: draftDigest}); err != nil {
		t.Fatal(err)
	}
	members := []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-1"}},
		{Ref: explorer.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-2"}},
	}
	completedAt := time.Now().UTC()
	store.selections["selection-base"] = explorer.SelectionRevision{
		ID: "selection-base", Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "base-rule", MembershipDigest: explorer.MembershipDigest(members),
		MemberCount: int64(len(members)), MemberBytes: 256, Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	store.members["selection-base"] = append([]explorer.SelectionMember(nil), members...)

	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		SelectionMembersCollection: "loom_explorer_selection_members",
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		SelectionReferenceValidator: func(_ context.Context, gotProject, generation string, gotScope authscope.ReadScope, refs []explorer.ResourceRef) error {
			if gotProject != project || generation != snapshot.Identity.Generation || gotScope.Mode != scope.Mode || !reflect.DeepEqual(gotScope.AuthResourcePaths, scope.AuthResourcePaths) {
				t.Fatalf("selection validation lost authorized scope: project=%q generation=%q scope=%#v", gotProject, generation, gotScope)
			}
			for _, ref := range refs {
				if err := ref.Validate(project, generation, "Patient"); err != nil {
					return err
				}
			}
			return nil
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.PopulationMemberRemovalProposal == nil || receipt.PopulationMemberRemovalProposal.CandidateSelectionRevisionID == "" {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview did not use the candidate population-removal receipt")
			}
			if bindings.AuthScopeMode != scope.Mode || !reflect.DeepEqual(bindings.AuthResourcePaths, scope.AuthResourcePaths) || bindings.DatasetGeneration != snapshot.Identity.Generation || bindings.SelectionProject != project || bindings.PreviewLimit != dataframeexecution.DefaultPreviewLimit {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("candidate preview lost authorized scope, generation, or default limit: %#v", bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, Complete: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/population-member-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":7,"expectedDraftDigest":%q,"outputId":"patients","baseSelectionRevisionId":"selection-base","removedMember":{"project":%q,"generation":%q,"resourceType":"Patient","id":"patient-2"}}`,
		snapshot.Token, draftDigest, project, snapshot.Identity.Generation,
	))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("population member proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposal loomapi.PopulationMemberRemovalProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == "" || proposal.BaseSelection.Id != "selection-base" || proposal.BaseSelection.ScopeDigest != snapshot.Identity.AuthorizationScopeDigest || proposal.CandidateSelection.Id == "selection-base" || proposal.CandidateSelection.ScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		t.Fatalf("proposal lost exact selection or scope headers: %#v", proposal)
	}
	if proposal.DraftVersion != 7 || proposal.DraftDigest != draftDigest || proposal.RemovedMember.Id != "patient-2" || proposal.PreviewStatus != loomapi.PopulationMemberRemovalProposalResponsePreviewStatusREADY || proposal.Preview.ReceiptId != proposal.ProposalId || proposal.Preview.OutputId != "patients" || proposal.Preview.RowCount != 1 {
		t.Fatalf("proposal response is not exact-draft/receipt preview: %#v", proposal)
	}
	if proposal.BaseSelection.MembershipDigest == proposal.CandidateSelection.MembershipDigest || proposal.CandidateSelection.MemberCount != 1 || store.saveCalls != 0 {
		t.Fatalf("proposal did not produce an unattached exact removal: base=%#v candidate=%#v saves=%d", proposal.BaseSelection, proposal.CandidateSelection, store.saveCalls)
	}
	var candidateIDs []string
	if _, err := store.VisitSelectionMembers(context.Background(), project, proposal.CandidateSelection.Id, "", 100, func(member explorer.SelectionMember) error {
		candidateIDs = append(candidateIDs, member.Ref.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(candidateIDs, []string{"patient-1"}) {
		t.Fatalf("candidate membership = %v, want exact base-minus-member [patient-1]", candidateIDs)
	}

	staleApply := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"population-remove-stale","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":8,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposal.ProposalId,
	))
	if staleApply.StatusCode != http.StatusConflict || store.saveCalls != 0 {
		t.Fatalf("wrong-version Apply status=%d saves=%d body=%s", staleApply.StatusCode, store.saveCalls, staleApply.Body)
	}
	staleDigestApply := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"population-remove-stale-digest","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":7,"expectedDraftDigest":"sha256:stale","commands":[{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, proposal.ProposalId,
	))
	if staleDigestApply.StatusCode != http.StatusConflict || store.saveCalls != 0 {
		t.Fatalf("wrong-digest Apply status=%d saves=%d body=%s", staleDigestApply.StatusCode, store.saveCalls, staleDigestApply.Body)
	}
	apply := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"population-remove-apply","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":7,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposal.ProposalId,
	))
	if apply.StatusCode != http.StatusOK {
		t.Fatalf("exact-CAS Apply status=%d body=%s", apply.StatusCode, apply.Body)
	}
	var applied loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(apply.Body), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.DraftVersion != 8 || applied.Workspace.Documents[0].Population == nil || applied.Workspace.Documents[0].Population.SelectionRevisionID != proposal.CandidateSelection.Id || store.saveCalls != 1 {
		t.Fatalf("Apply did not attach the exact candidate under one CAS: version=%d workspace=%#v saves=%d", applied.DraftVersion, applied.Workspace, store.saveCalls)
	}
	beforeDocument := workspace.Documents[0]
	beforeDocument.Population = nil
	afterDocument := applied.Workspace.Documents[0]
	afterDocument.Population = nil
	if !reflect.DeepEqual(beforeDocument, afterDocument) {
		t.Fatal("population Apply changed authoring fields outside the attached immutable selection")
	}
	staleReplay := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"population-remove-replay-stale","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":7,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposal.ProposalId,
	))
	if staleReplay.StatusCode != http.StatusConflict || store.saveCalls != 1 {
		t.Fatalf("stale replay status=%d saves=%d body=%s", staleReplay.StatusCode, store.saveCalls, staleReplay.Body)
	}
	persisted, err := domain.Get(context.Background(), project, explorerID)
	if err != nil {
		t.Fatalf("persisted draft lookup failed: %v", err)
	}
	if persisted == nil {
		t.Fatal("persisted draft lookup returned nil without an error")
	}
	if persisted.DraftVersion != 8 || persisted.DraftDigest != applied.DraftDigest {
		t.Fatalf("persisted CAS owner version=%d digest=%q, want version=8 digest=%q", persisted.DraftVersion, persisted.DraftDigest, applied.DraftDigest)
	}
}

func TestPopulationMemberRemovalProposalRequestRejectsUnknownNestedFields(t *testing.T) {
	for _, raw := range []string{
		`{"snapshotToken":"s","expectedDraftVersion":1,"expectedDraftDigest":"d","outputId":"patients","baseSelectionRevisionId":"selection","removedMember":{"project":"p","generation":"g","resourceType":"Patient","id":"1","scope":"forged"}}`,
		`{"snapshotToken":"s","expectedDraftVersion":1,"expectedDraftDigest":"d","outputId":"patients","baseSelectionRevisionId":"selection","removedMember":{"project":"p","generation":"g","resourceType":"Patient","id":"1"},"routeChoiceId":"forged"}`,
	} {
		var request loomapi.PopulationMemberRemovalProposalRequest
		if err := json.Unmarshal([]byte(raw), &request); err == nil {
			t.Fatalf("accepted unknown population proposal member: %s", raw)
		}
	}
}
