package lifecycle

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestProposeConstructionChoiceCompilesExactBatchWithoutSavingDraft(t *testing.T) {
	store, service, snapshot, candidates := constructionChoiceFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	choiceBatch, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	choiceID := "command-choice-proposal"
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
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
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	beforeWorkspace, err := authoringv2.DecodeWorkspace(beforeConfig)
	if err != nil {
		t.Fatal(err)
	}

	request := ConstructionChoiceProposalRequest{
		CommandID: choiceID, Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest, OutputID: "patients",
		ConstructionChoices: []ConstructionChoiceProposalSelection{
			{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue},
			{ChoiceID: choiceBatch.ChoiceID, Form: capability.ConstructionChoiceFirst},
		},
	}
	proposal, err := service.ProposeConstructionChoice(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.CommandID != choiceID || proposal.SnapshotToken != snapshot.Token || proposal.DraftVersion != beforeVersion || proposal.DraftDigest != beforeDigest || proposal.OutputID != request.OutputID {
		t.Fatalf("proposal identity echo = %#v", proposal)
	}
	if !reflect.DeepEqual(proposal.ConstructionChoices, request.ConstructionChoices) || len(proposal.CandidateColumnIDs) != 2 || proposal.CandidateWorkspaceDigest == "" || proposal.PreviewReceiptID == "" {
		t.Fatalf("proposal candidate identity = %#v", proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, beforeConfig) {
		t.Fatalf("proposal mutated saved draft: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
	}
	if store.receipt == nil || store.receipt.IntentDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("compiled receipt does not match candidate digest: receipt=%#v proposal=%#v", store.receipt, proposal)
	}

	apply, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: choiceID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest,
		Commands: []authoringv2.Command{
			{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}},
			{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: choiceBatch.ChoiceID, Form: capability.ConstructionChoiceFirst}},
		},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	applyWorkspaceDigest, err := apply.Workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if apply.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("ApplyCommands response digest %q differs from preview candidate %q", apply.DraftDigest, proposal.CandidateWorkspaceDigest)
	}
	if applyWorkspaceDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("ApplyCommands workspace digest %q differs from preview candidate %q", applyWorkspaceDigest, proposal.CandidateWorkspaceDigest)
	}
	columns := apply.Workspace.Documents[0].Columns
	if len(columns) != len(beforeWorkspace.Documents[0].Columns)+len(proposal.CandidateColumnIDs) {
		t.Fatalf("ApplyCommands columns=%d base columns=%d candidate columns=%d", len(columns), len(beforeWorkspace.Documents[0].Columns), len(proposal.CandidateColumnIDs))
	}
	for index, candidateColumnID := range proposal.CandidateColumnIDs {
		column := columns[len(beforeWorkspace.Documents[0].Columns)+index]
		if column.Column != candidateColumnID {
			t.Fatalf("candidate column %d = %q, ApplyCommands column = %q", index, candidateColumnID, column.Column)
		}
	}
}

func TestProposeConstructionChoiceRejectsStaleDraftAndSnapshotChoice(t *testing.T) {
	store, service, snapshot, candidates := constructionChoiceFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		store.receipt = receipt
		return receipt, nil
	}
	request := ConstructionChoiceProposalRequest{
		CommandID: "command-choice-stale", Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest, OutputID: "patients",
		ConstructionChoices: []ConstructionChoiceProposalSelection{{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}},
	}
	request.ExpectedDraftVersion++
	if _, err := service.ProposeConstructionChoice(context.Background(), request); !proposalErrorIs(err, ClassConflict, "DRAFT_CONFLICT") {
		t.Fatalf("stale draft error = %v", err)
	}
	request.ExpectedDraftVersion = store.created.DraftVersion
	staleChoice, err := capability.NewFieldConstructionChoice("different-snapshot", candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	request.ConstructionChoices[0].ChoiceID = staleChoice.ChoiceID
	if _, err := service.ProposeConstructionChoice(context.Background(), request); !proposalErrorIs(err, ClassConflict, "STALE_CONSTRUCTION_CHOICE") {
		t.Fatalf("choice from different snapshot error = %v", err)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("invalid proposal saved the draft %d times", store.saveDraftCalls)
	}
}

func TestProposeSemanticConstructionChoiceUsesAuthorizedInventoryWithoutSavingDraft(t *testing.T) {
	store, service, snapshot, _, choice := constructionSemanticChoiceFixture(t, nil, nil, "Observation")
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		var compileErr error
		receipt.CompilationKey, compileErr = explorer.CompilationKey(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.ID, compileErr = explorer.ReceiptID(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		store.receipt = receipt
		return receipt, nil
	}
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	proposal, err := service.ProposeConstructionChoice(context.Background(), ConstructionChoiceProposalRequest{
		CommandID: "semantic-choice-preview", Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest, OutputID: "patients",
		ConstructionChoices: []ConstructionChoiceProposalSelection{{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.CandidateWorkspaceDigest == "" || len(proposal.CandidateColumnIDs) != 1 || proposal.PreviewReceiptID == "" {
		t.Fatalf("semantic choice proposal = %#v", proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, beforeConfig) {
		t.Fatalf("semantic proposal mutated saved draft: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
	}
}
