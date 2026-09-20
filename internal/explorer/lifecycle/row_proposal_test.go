package lifecycle

import (
	"context"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type rowProposalChoiceResolver struct{}

func (rowProposalChoiceResolver) ResolveRowChoiceID(_ context.Context, request RowChoiceResolveRequest) (ResolvedRowChoice, error) {
	if request.Route.OccurrenceID != authoringv2.RootOccurrenceID {
		return ResolvedRowChoice{}, context.Canceled
	}
	switch request.ExpectedKind {
	case RowChoiceFieldGroup:
		return ResolvedRowChoice{Kind: RowChoiceFieldGroup, OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: "id"}, nil
	case RowChoiceExpanded:
		return ResolvedRowChoice{Kind: RowChoiceExpanded, OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "component[]"}, nil
	default:
		return ResolvedRowChoice{}, context.Canceled
	}
}

type rowProposalExplicitGroupResolver struct {
	proof       ExplicitGroupRevisionProof
	resolveReqs []ExplicitGroupRevisionResolveRequest
	receipts    []*explorer.CompilationReceipt
}

func (r *rowProposalExplicitGroupResolver) ResolveExplicitGroupRevision(_ context.Context, request ExplicitGroupRevisionResolveRequest) (ExplicitGroupRevisionProof, error) {
	r.resolveReqs = append(r.resolveReqs, request)
	proof := r.proof
	if proof.RevisionID == "" {
		proof = ExplicitGroupRevisionProof{
			RevisionID: request.RevisionID, Project: request.Project,
			SourceGeneration:         request.Snapshot.Identity.Generation,
			AuthorizationScopeDigest: request.Snapshot.Identity.AuthorizationScopeDigest,
			RootResourceType:         request.RootResourceType, SelectionRevisionID: "selection-1",
			SelectionMembershipDigest: "sha256:selection-members", DefinitionDigest: "sha256:definition",
			MembershipDigest: "sha256:membership", Complete: true,
		}
	}
	return proof, nil
}

func (r *rowProposalExplicitGroupResolver) ValidateCompilationReceipt(_ context.Context, proof ExplicitGroupRevisionProof, receipt *explorer.CompilationReceipt) error {
	r.receipts = append(r.receipts, receipt)
	if proof.RevisionID == "" || receipt == nil {
		return fmt.Errorf("explicit group receipt binding is missing")
	}
	workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return err
	}
	document := proposalDocument(workspace, "patients")
	if document == nil || document.Rows.Kind != authoringv2.RowDefinitionGroups || document.Rows.Groups == nil ||
		document.Rows.Groups.Source.Kind != authoringv2.GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil ||
		document.Rows.Groups.Source.Explicit.RevisionID != proof.RevisionID {
		return fmt.Errorf("compiled workspace does not contain the resolved explicit group revision")
	}
	return nil
}

func TestProposeExplicitGroupRequiresInjectedRevisionAndReceiptProof(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	resolver := &rowProposalExplicitGroupResolver{}
	service.config.ExplicitGroupResolver = resolver
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberError,
	}}
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolver.resolveReqs) != 1 || len(resolver.receipts) != 0 {
		t.Fatalf("resolver calls: resolve=%d receipt=%d", len(resolver.resolveReqs), len(resolver.receipts))
	}
	resolved := resolver.resolveReqs[0]
	if resolved.Project != request.Project || resolved.ExplorerID != request.ExplorerID || resolved.OutputID != request.OutputID || resolved.Snapshot.Token != snapshot.Token || resolved.RevisionID != "group-revision-1" || resolved.RootResourceType != "Patient" {
		t.Fatalf("explicit group resolver received unbound request: %#v", resolved)
	}
	if proposal.ProposalID != "" || proposal.Comparison.Status != RowDefinitionComparisonUnavailable || proposal.Comparison.ReasonCode != "GROUPED_ROW_COMPILER_UNAVAILABLE" {
		t.Fatalf("grouped proposal incorrectly advertised execution: %#v", proposal)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("proposal persisted a draft %d times", store.saveDraftCalls)
	}
}

func TestProposeFieldGroupReportsGroupedExecutionUnavailable(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionFieldGroup, FieldGroup: &FieldGroupSelection{
		RowChoiceID: "opaque-choice-1", MissingKeyPolicy: authoringv2.MissingKeyError,
	}}
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID != "" || proposal.Comparison.Status != RowDefinitionComparisonUnavailable || proposal.Comparison.ReasonCode != "GROUPED_ROW_COMPILER_UNAVAILABLE" {
		t.Fatalf("field-group proposal advertised unsupported grouped execution: %#v", proposal)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("field-group proposal mutated the draft %d times", store.saveDraftCalls)
	}
}

func TestProposeExplicitGroupRejectsStaleRevisionProof(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	service.config.ExplicitGroupResolver = &rowProposalExplicitGroupResolver{proof: ExplicitGroupRevisionProof{
		RevisionID: "group-revision-1", Project: "project-a", SourceGeneration: "generation-stale",
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RootResourceType: "Patient",
		SelectionRevisionID: "selection-1", SelectionMembershipDigest: "sha256:selection-members",
		DefinitionDigest: "sha256:definition", MembershipDigest: "sha256:membership", Complete: true,
	}}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{
		RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberError,
	}}
	if _, err := service.ProposeRowDefinition(context.Background(), request); err == nil {
		t.Fatal("accepted explicit group revision from a stale source generation")
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("stale explicit group rejection persisted a draft %d times", store.saveDraftCalls)
	}
}

func TestProposeRowDefinitionBindsCandidateReceiptWithoutSavingDraft(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	compileBindings := make([]*explorer.RowDefinitionProposalBinding, 0, 2)
	compile := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileBindings = append(compileBindings, cloneRowDefinitionProposalBinding(request.RowDefinitionProposal))
		return compile(ctx, request)
	}
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.CandidateWorkspaceDigest == "" || proposal.Mode != RowDefinitionSelectionExpanded {
		t.Fatalf("proposal identity = %#v", proposal)
	}
	if proposal.Comparison.Status != RowDefinitionComparisonUnavailable || proposal.Comparison.ReasonCode != "PREVIEW_UNAVAILABLE" {
		t.Fatalf("comparison status = %#v", proposal.Comparison)
	}
	if len(compileBindings) != 2 || compileBindings[0] != nil || compileBindings[1] == nil {
		t.Fatalf("compile proposal bindings = %#v, want base unbound and candidate bound", compileBindings)
	}
	binding := store.receipt.RowDefinitionProposal
	if binding == nil || *binding != *compileBindings[1] || binding.DraftVersion != beforeVersion || binding.DraftDigest != beforeDigest || binding.OutputID != "patients" || binding.SnapshotToken != snapshot.Token || binding.CandidateWorkspaceDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("candidate receipt binding = %#v, proposal = %#v", binding, proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("proposal mutated the saved draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestApplyRowDefinitionProposalSavesOnceAndReplaysIdempotently(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	baseVersion := store.created.DraftVersion
	request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-row-definition")
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || store.saveDraftCalls != 1 || store.created.DraftVersion != baseVersion+1 || store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("apply response=%#v saves=%d owner=%#v", response, store.saveDraftCalls, store.created)
	}
	if got := store.created.DraftConfig; string(got) == string(mustCanonical(t, workspace)) {
		t.Fatal("apply did not change the saved row definition")
	}
	replayed, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if replayed == nil || replayed.DraftDigest != proposal.CandidateWorkspaceDigest || store.saveDraftCalls != 1 {
		t.Fatalf("replay=%#v saves=%d, want same committed result and no second write", replayed, store.saveDraftCalls)
	}
}

func TestApplyRowDefinitionProposalRejectsTamperingAndNewCASReplay(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeStore, *authoringv2.Workspace, *authoringv2.ApplyCommandsRequest) error
	}{
		{
			name: "tampered receipt binding",
			mutate: func(store *fakeStore, _ *authoringv2.Workspace, _ *authoringv2.ApplyCommandsRequest) error {
				store.receipt.RowDefinitionProposal.BaseDocumentDigest = "sha256:tampered"
				return nil
			},
		},
		{
			name: "replay with new CAS after row-only write",
			mutate: func(store *fakeStore, workspace *authoringv2.Workspace, request *authoringv2.ApplyCommandsRequest) error {
				workspace.Documents[0].Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
					OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "component", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
				}}
				canonical, err := workspace.CanonicalJSON()
				if err != nil {
					return err
				}
				digest, err := workspace.Digest()
				if err != nil {
					return err
				}
				store.created.DraftConfig = canonical
				store.created.DraftDigest = digest
				store.created.DraftVersion++
				request.ExpectedDraftVersion = store.created.DraftVersion
				request.ExpectedDraftDigest = store.created.DraftDigest
				request.CommandID = "apply-after-row-only-write"
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, workspace := rowProposalService(t)
			proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
			if err != nil {
				t.Fatal(err)
			}
			request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-row-definition")
			beforeConfig := append([]byte(nil), store.created.DraftConfig...)
			beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
			if err := test.mutate(store, &workspace, &request); err != nil {
				t.Fatal(err)
			}
			conflictConfig := append([]byte(nil), store.created.DraftConfig...)
			conflictVersion, conflictDigest := store.created.DraftVersion, store.created.DraftDigest
			if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
				t.Fatal("accepted stale or tampered proposal")
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != conflictVersion || store.created.DraftDigest != conflictDigest || string(store.created.DraftConfig) != string(conflictConfig) {
				t.Fatalf("rejected apply mutated current draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
			}
			if test.name == "tampered receipt binding" && (store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig)) {
				t.Fatalf("tampered receipt rejection changed draft: %#v", store.created)
			}
		})
	}
}

func TestApplyRowDefinitionProposalLeavesDraftUntouchedOnCASConflict(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	proposal, err := service.ProposeRowDefinition(context.Background(), rowProposalRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	store.applyErr = explorer.ErrDraftConflict
	request := rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-conflict")
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("accepted compare-and-swap conflict")
	}
	if store.saveDraftCalls != 1 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("conflicted apply mutated draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func rowProposalService(t *testing.T) (*Service, *fakeStore, capability.Snapshot, authoringv2.Workspace) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-row-proposal", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := lifecycleCandidatePreviewWorkspace()
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{
		Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: draft,
		DraftVersion: 7, DraftDigest: digest,
	}, copyGet: true}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return lifecycleInterpretationCatalog(snapshot, explorerID)
	}
	config.RowChoiceResolver = rowProposalChoiceResolver{}
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
		receipt.RowDefinitionProposal = cloneRowDefinitionProposalBinding(request.RowDefinitionProposal)
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	return newTestService(t, store, config), store, snapshot, workspace
}

func rowProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot) RowDefinitionProposalRequest {
	return RowDefinitionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest, OutputID: "patients",
		Selection: RowDefinitionSelection{Kind: RowDefinitionSelectionExpanded, Expanded: &ExpandedSelection{
			RowChoiceID: "opaque-choice-1", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
		}},
	}
}

func rowProposalApplyRequest(owner *explorer.Explorer, snapshot capability.Snapshot, proposalID, commandID string) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: proposalID}},
	}
}

func mustCanonical(t *testing.T, workspace authoringv2.Workspace) []byte {
	t.Helper()
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
