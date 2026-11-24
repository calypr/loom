package explorerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"

	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestPopulationMemberRemovalProposalHTTPPreviewsAndAppliesExactScopedCAS(t *testing.T) {
	const project, explorerID = "project-a", "custom"
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/alpha"}}
	baseSnapshot := testAuthoringV2CapabilitySnapshot()
	identity := baseSnapshot.Identity
	identity.AuthorizationScopeDigest = capabilityresolver.ScopeDigest(scope)
	snapshot := capability.NewSnapshot(identity, baseSnapshot.Policy, baseSnapshot.Status, baseSnapshot.Complete, baseSnapshot.Truncated,
		baseSnapshot.Nodes, baseSnapshot.Edges, baseSnapshot.Candidates, baseSnapshot.Diagnostics)

	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = ws.CurrentSemanticsVersion
	workspace.Documents[0].Rows = ws.RecordsRowDefinition()
	workspace.Documents[0].Population = &ws.Population{SelectionRevisionID: "selection-base", Route: []ws.PopulationRouteStep{}}
	catalog := ws.CatalogFromCapability(snapshot, explorerID)
	workspace, err = ws.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	rawDraft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	draftDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	store := &explorerfixtures.SelectionStore{
		Store: explorerfixtures.NewStore(), Selections: map[string]explorerselection.SelectionRevision{}, Members: map[string][]explorerselection.SelectionMember{},
	}
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: project, ExplorerID: explorerID, Title: "Patients", DraftConfig: rawDraft, DraftVersion: 7, DraftDigest: draftDigest}); err != nil {
		t.Fatal(err)
	}
	members := []explorerselection.SelectionMember{
		{Ref: explorerselection.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-1"}},
		{Ref: explorerselection.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-2"}},
	}
	completedAt := time.Now().UTC()
	store.Selections["selection-base"] = explorerselection.SelectionRevision{
		ID: "selection-base", Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient",
		Rule: explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit}, Source: explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "base-rule", MembershipDigest: explorerselection.MembershipDigest(members),
		MemberCount: int64(len(members)), MemberBytes: 256, Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	store.Members["selection-base"] = append([]explorerselection.SelectionMember(nil), members...)

	domain, err := explorerfixtures.NewService(store)
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
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		SelectionReferenceValidator: func(_ context.Context, gotProject, generation string, gotScope authscope.ReadScope, refs []explorerselection.ResourceRef) error {
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
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, engine, store, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	registerGeneratedExplorerTestRoutes(t, app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config, store)
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
	if proposal.BaseSelection.MembershipDigest == proposal.CandidateSelection.MembershipDigest || proposal.CandidateSelection.MemberCount != 1 || store.SaveCalls != 0 {
		t.Fatalf("proposal did not produce an unattached exact removal: base=%#v candidate=%#v saves=%d", proposal.BaseSelection, proposal.CandidateSelection, store.SaveCalls)
	}
	var candidateIDs []string
	if _, err := store.VisitSelectionMembers(context.Background(), project, proposal.CandidateSelection.Id, "", 100, func(member explorerselection.SelectionMember) error {
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
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposal.ProposalId,
	))
	if staleApply.StatusCode != http.StatusConflict || store.SaveCalls != 0 {
		t.Fatalf("wrong-version Apply status=%d saves=%d body=%s", staleApply.StatusCode, store.SaveCalls, staleApply.Body)
	}
	staleDigestApply := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"population-remove-stale-digest","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":7,"expectedDraftDigest":"sha256:stale","commands":[{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, proposal.ProposalId,
	))
	if staleDigestApply.StatusCode != http.StatusConflict || store.SaveCalls != 0 {
		t.Fatalf("wrong-digest Apply status=%d saves=%d body=%s", staleDigestApply.StatusCode, store.SaveCalls, staleDigestApply.Body)
	}
	apply := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"population-remove-apply","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":7,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposal.ProposalId,
	))
	if apply.StatusCode != http.StatusOK {
		t.Fatalf("exact-CAS Apply status=%d body=%s", apply.StatusCode, apply.Body)
	}
	var applied loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(apply.Body), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.DraftVersion != 8 || applied.Workspace.Documents[0].Population == nil || applied.Workspace.Documents[0].Population.SelectionRevisionID != proposal.CandidateSelection.Id || store.SaveCalls != 1 {
		t.Fatalf("Apply did not attach the exact candidate under one CAS: version=%d workspace=%#v saves=%d", applied.DraftVersion, applied.Workspace, store.SaveCalls)
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
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposal.ProposalId,
	))
	if staleReplay.StatusCode != http.StatusConflict || store.SaveCalls != 1 {
		t.Fatalf("stale replay status=%d saves=%d body=%s", staleReplay.StatusCode, store.SaveCalls, staleReplay.Body)
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
