package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestProposeTableShapeBindsCandidateReceiptWithoutMutatingDraft(t *testing.T) {
	service, store, snapshot, workspace := tableShapeProposalService(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	compileBindings := make([]*explorer.TableShapeProposalBinding, 0, 2)
	compile := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileBindings = append(compileBindings, cloneTableShapeProposalBinding(request.TableShapeProposal))
		return compile(ctx, request)
	}
	shape := testTableShape()
	proposal, err := service.ProposeTableShape(context.Background(), tableShapeProposalRequest(store.created, snapshot, shape))
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.CandidateWorkspaceDigest == "" || proposal.Mode != TableShapeProposalAdd {
		t.Fatalf("proposal identity = %#v", proposal)
	}
	if proposal.Comparison.Status != TableShapeComparisonUnavailable || proposal.Comparison.ReasonCode != "PREVIEW_UNAVAILABLE" {
		t.Fatalf("comparison status = %#v", proposal.Comparison)
	}
	if len(compileBindings) != 2 || compileBindings[0] != nil || compileBindings[1] == nil {
		t.Fatalf("compile proposal bindings = %#v, want base unbound and candidate bound", compileBindings)
	}
	binding := store.receipt.TableShapeProposal
	if binding == nil || *binding != *compileBindings[1] || binding.DraftVersion != beforeVersion ||
		binding.DraftDigest != beforeDigest || binding.OutputID != "patients" || binding.SnapshotToken != snapshot.Token ||
		binding.CandidateWorkspaceDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("candidate receipt binding = %#v, proposal = %#v", binding, proposal)
	}
	if proposal.BaseDocumentDigest == "" || proposal.BaseReceiptID == "" || proposal.CandidateWorkspaceDigest == beforeDigest {
		t.Fatalf("proposal is missing bound identities: %#v", proposal)
	}
	shape.Derived[0].Output.Column = "mutated_after_proposal"
	candidate, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidate.Documents[0].TableShape.Derived[0].Output.Column; got != "patient_id_copy" {
		t.Fatalf("candidate receipt aliases caller-owned table shape: column=%q", got)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("proposal mutated the saved draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
	if err := tableShapeWorkspaceUnchanged(workspace, store.created); err != nil {
		t.Fatal(err)
	}
}

func TestDiscardingTableShapeProposalDoesNotApplyIt(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	if _, err := service.ProposeTableShape(context.Background(), tableShapeProposalRequest(store.created, snapshot, testTableShape())); err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("discarding proposal changed the saved draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestApplyTableShapeProposalSavesCandidateOnceAndIsIdempotent(t *testing.T) {
	service, store, snapshot, workspace := tableShapeProposalService(t)
	proposal, err := service.ProposeTableShape(context.Background(), tableShapeProposalRequest(store.created, snapshot, testTableShape()))
	if err != nil {
		t.Fatal(err)
	}
	baseVersion := store.created.DraftVersion
	request := tableShapeApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-table-shape")
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || store.saveDraftCalls != 1 || store.created.DraftVersion != baseVersion+1 || store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("apply response=%#v saves=%d owner=%#v", response, store.saveDraftCalls, store.created)
	}
	applied, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tableShapeWorkspaceChange(workspace, applied, "patients"); err != nil {
		t.Fatalf("applied workspace changed more than Document.TableShape: %v", err)
	}
	replayed, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if replayed == nil || replayed.DraftDigest != proposal.CandidateWorkspaceDigest || store.saveDraftCalls != 1 {
		t.Fatalf("replay=%#v saves=%d, want same committed result and no second write", replayed, store.saveDraftCalls)
	}
}

func TestApplyTableShapeProposalRejectsNonAtomicCommandBatch(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	proposal, err := service.ProposeTableShape(context.Background(), tableShapeProposalRequest(store.created, snapshot, testTableShape()))
	if err != nil {
		t.Fatal(err)
	}
	request := tableShapeApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-table-shape-with-extra")
	request.Commands = append(request.Commands, authoringv2.Command{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: "Changed"})
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("accepted a table shape proposal with another command")
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("rejected non-atomic apply saved the draft %d times", store.saveDraftCalls)
	}
}

func TestApplyTableShapeProposalRejectsStaleAndTamperedReceipts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeStore, *authoringv2.ApplyCommandsRequest) error
	}{
		{
			name: "tampered binding",
			mutate: func(store *fakeStore, _ *authoringv2.ApplyCommandsRequest) error {
				store.receipt.TableShapeProposal.BaseDocumentDigest = "sha256:tampered"
				return nil
			},
		},
		{
			name: "candidate also changes output title",
			mutate: func(store *fakeStore, request *authoringv2.ApplyCommandsRequest) error {
				candidate, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
				if err != nil {
					return err
				}
				candidate.Documents[0].Output.Title = "Tampered"
				store.receipt.NormalizedBundle, err = candidate.CanonicalJSON()
				if err != nil {
					return err
				}
				store.receipt.IntentDigest, err = candidate.Digest()
				if err != nil {
					return err
				}
				store.receipt.TableShapeProposal.CandidateWorkspaceDigest = store.receipt.IntentDigest
				store.receipt.CompilationKey, err = explorer.CompilationKey(*store.receipt)
				if err != nil {
					return err
				}
				store.receipt.ID, err = explorer.ReceiptID(*store.receipt)
				if err != nil {
					return err
				}
				request.Commands[0].ProposalID = store.receipt.ID
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := tableShapeProposalService(t)
			proposal, err := service.ProposeTableShape(context.Background(), tableShapeProposalRequest(store.created, snapshot, testTableShape()))
			if err != nil {
				t.Fatal(err)
			}
			request := tableShapeApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-table-shape")
			if err := test.mutate(store, &request); err != nil {
				t.Fatal(err)
			}
			beforeConfig := append([]byte(nil), store.created.DraftConfig...)
			beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
			if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
				t.Fatal("accepted a stale or tampered table shape proposal")
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
				t.Fatalf("rejected apply mutated current draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
			}
		})
	}
}

func TestProposeTableShapeComparesChangedColumnsAndRowsByStableIdentity(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	previewCalls := 0
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if bindings.PreviewLimit != 2 || !bindings.IncludeRowIdentity || !reflect.DeepEqual(bindings.OutputNames, []string{"patients"}) {
			t.Fatalf("preview bindings = %#v", bindings)
		}
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		shape := proposalDocument(workspace, "patients").TableShape
		if shape == nil {
			if err := visit(map[string]any{"__loom_row_id": "row-1", "patient_id": "p1", "value": 1}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			if err := visit(map[string]any{"__loom_row_id": "row-2", "patient_id": "p2", "value": 2}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Columns: []string{"patient_id", "value"}, RowCount: 100, Truncated: true}, nil
		}
		if err := visit(map[string]any{"__loom_row_id": "row-1", "patient_id": "p1", "patient_id_copy": 2}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		if err := visit(map[string]any{"__loom_row_id": "row-3", "patient_id": "p3", "patient_id_copy": 6}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"patient_id", "patient_id_copy"}, RowCount: 100, Truncated: true}, nil
	}
	request := tableShapeProposalRequest(store.created, snapshot, testTableShape())
	request.Limit = 2
	proposal, err := service.ProposeTableShape(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	comparison := proposal.Comparison
	if previewCalls != 2 || comparison.Status != TableShapeComparisonAvailable || !comparison.Base.Sampled || !comparison.Candidate.Sampled {
		t.Fatalf("preview comparison summary = %#v, calls=%d", comparison, previewCalls)
	}
	if !reflect.DeepEqual(comparison.ChangedColumns, []string{"patient_id", "patient_id_copy", "value"}) {
		t.Fatalf("changed columns = %#v", comparison.ChangedColumns)
	}
	if comparison.ChangedRowCount != 3 || len(comparison.ChangedRows) != 3 {
		t.Fatalf("changed row summary = count %d, rows %#v", comparison.ChangedRowCount, comparison.ChangedRows)
	}
	if got := comparison.ChangedRows[0]; got.RowIdentity != "row-1" || !got.BasePresent || !got.CandidatePresent || !reflect.DeepEqual(got.ChangedColumns, []string{"patient_id_copy", "value"}) {
		t.Fatalf("changed shared row = %#v", got)
	}
	if got := comparison.ChangedRows[1]; got.RowIdentity != "row-2" || !got.BasePresent || got.CandidatePresent {
		t.Fatalf("removed row = %#v", got)
	}
	if got := comparison.ChangedRows[2]; got.RowIdentity != "row-3" || got.BasePresent || !got.CandidatePresent {
		t.Fatalf("added row = %#v", got)
	}
}

func tableShapeProposalService(t *testing.T) (*Service, *fakeStore, capability.Snapshot, authoringv2.Workspace) {
	t.Helper()
	service, store, snapshot, workspace := rowProposalService(t)
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := nativeReceipt(snapshot)
		var err error
		receipt.IntentDigest, err = request.Workspace.Digest()
		if err != nil {
			return nil, err
		}
		receipt.NormalizedBundle, err = request.Workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		receipt.RowDefinitionProposal = cloneRowDefinitionProposalBinding(request.RowDefinitionProposal)
		receipt.TableShapeProposal = cloneTableShapeProposalBinding(request.TableShapeProposal)
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
	return service, store, snapshot, workspace
}

func tableShapeProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot, shape *authoringv2.TableShape) TableShapeProposalRequest {
	return TableShapeProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", TableShape: shape,
	}
}

func tableShapeApplyRequest(owner *explorer.Explorer, snapshot capability.Snapshot, proposalID, commandID string) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: proposalID}},
	}
}

func testTableShape() *authoringv2.TableShape {
	value := int64(2)
	return &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{{
		ConstructionID: "derived_copy", Output: authoringv2.ColumnOutput{Column: "patient_id_copy", Label: "Patient ID copy"}, Operation: "ADD",
		Left:               authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "patient_id"},
		Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "INTEGER", Integer: &value}},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
}

func tableShapeWorkspaceUnchanged(workspace authoringv2.Workspace, owner *explorer.Explorer) error {
	current, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, workspace) {
		return fmt.Errorf("saved workspace differs from proposal input")
	}
	return nil
}
