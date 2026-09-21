package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

func TestProposeTableShapeBindsCandidateReceiptWithoutMutatingDraft(t *testing.T) {
	service, store, snapshot, workspace := tableShapeProposalService(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	compileBindings := make([]*explorer.TableShapeProposalBinding, 0, 2)
	compile := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileBindings = append(compileBindings, cloneTableShapeProposalBinding(request.TableShapeProposal))
		return compile(ctx, request)
	}
	proposal, err := service.ProposeTableShape(context.Background(), request)
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
		t.Fatalf("compile proposal bindings = %#v, want one loaded base and one bound candidate", compileBindings)
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
	request.DerivedResolutionIDs[0] = "mutated_after_proposal"
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

func TestTableShapeProposalRequestRejectsRawAuthoringShape(t *testing.T) {
	var request TableShapeProposalRequest
	err := json.Unmarshal([]byte(`{"project":"project-a","explorerId":"patients","snapshotToken":"snapshot-a","expectedDraftVersion":1,"expectedDraftDigest":"draft-a","outputId":"patients","mode":"ADD","catalogId":"tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tableShape":{"derived":[]}}`), &request)
	if err == nil {
		t.Fatal("accepted raw authoring TableShape at the proposal boundary")
	}
}

func TestProposeTableShapeComposesPivotAndPostPivotDerived(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	request := tableShapeProposalBaseRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	repository := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository)
	catalog := repository.catalogs[request.CatalogID]
	pivot := resolveProposalPivot(t, service, request, catalog)
	derivedSelection := TableShapeDerivedSelection{
		OutputColumn: "bucket_plus_zero", OutputLabel: "Bucket plus zero", PivotResolutionID: pivot.ResolutionID,
		OperatorChoiceID:      catalogOperatorChoice(t, catalog, "ADD"),
		Left:                  TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: pivot.DerivedOperands[0].ID},
		Right:                 TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandLiteral, Literal: tableShapeTestPtr(tableshapecap.IntegerScalar(0))},
		MissingPolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDerivedMissing, "ERROR"),
	}
	derived, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: request.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &derivedSelection,
	})
	if err != nil {
		t.Fatal(err)
	}
	request.ReshapeResolutionID = pivot.ResolutionID
	request.DerivedResolutionIDs = []string{derived.ResolutionID}

	proposal, err := service.ProposeTableShape(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	shape := compiledProposalShape(t, store)
	if proposal.Mode != TableShapeProposalAdd || shape.Reshape == nil || shape.Reshape.Pivot == nil || len(shape.Derived) != 1 {
		t.Fatalf("proposal shape did not contain pivot and derived construction: proposal=%#v shape=%#v", proposal, shape)
	}
	if got := shape.Reshape.Pivot.Categories[0].Key; got.Kind != authoringv2.TableScalarString || got.String == nil || *got.String != "" {
		t.Fatalf("pivot category did not preserve its frozen typed empty-string key: %#v", got)
	}
	if got := shape.Derived[0].Left.Column; got != shape.Reshape.Pivot.Categories[0].Output.Column {
		t.Fatalf("derived pivot operand mapped to %q, want frozen output %q", got, shape.Reshape.Pivot.Categories[0].Output.Column)
	}
	if shape.Derived[0].Right.Literal == nil || shape.Derived[0].Right.Literal.Kind != authoringv2.TableScalarInteger {
		t.Fatalf("derived literal lost its resolved integer type: %#v", shape.Derived[0].Right)
	}
	first, err := service.composeTableShapeProposal(context.Background(), request, catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.composeTableShapeProposal(context.Background(), request, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.Reshape.Pivot.ConstructionID == "" || first.Derived[0].ConstructionID == "" {
		t.Fatalf("immutable receipts did not reconstruct stable construction IDs: first=%#v second=%#v", first, second)
	}
}

func TestProposeTableShapeComposesOrderedDerivedChainWithoutReshape(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	repository := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository)
	catalog := repository.catalogs[request.CatalogID]
	firstID := request.DerivedResolutionIDs[0]
	first := repository.resolutions[firstID].Derived
	secondSelection := TableShapeDerivedSelection{
		OutputColumn: "patient_id_copy_twice", OutputLabel: "Patient ID copy twice",
		OperatorChoiceID:      catalogOperatorChoice(t, catalog, "ADD"),
		Left:                  TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandResolution, ResolutionID: firstID},
		Right:                 TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandLiteral, Literal: tableShapeTestPtr(tableshapecap.IntegerScalar(1))},
		MissingPolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDerivedMissing, "PROPAGATE_NULL"),
	}
	if first == nil {
		t.Fatal("first derived receipt has no payload")
	}
	second, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: request.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &secondSelection,
	})
	if err != nil {
		t.Fatal(err)
	}
	request.DerivedResolutionIDs = append(request.DerivedResolutionIDs, second.ResolutionID)
	if _, err := service.ProposeTableShape(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	shape := compiledProposalShape(t, store)
	if shape.Reshape != nil || len(shape.Derived) != 2 || shape.Derived[1].Left.Column != shape.Derived[0].Output.Column {
		t.Fatalf("proposal did not map an earlier derived output without a reshape: %#v", shape)
	}

	request.DerivedResolutionIDs = []string{second.ResolutionID, firstID}
	if _, err := service.ProposeTableShape(context.Background(), request); !proposalErrorIs(err, ClassUnprocessable, "INVALID_TABLE_SHAPE") {
		t.Fatalf("out-of-order derived dependency error = %#v, want 422-class INVALID_TABLE_SHAPE", err)
	}
	request.DerivedResolutionIDs = []string{second.ResolutionID}
	if _, err := service.ProposeTableShape(context.Background(), request); !proposalErrorIs(err, ClassUnprocessable, "INVALID_TABLE_SHAPE") {
		t.Fatalf("missing derived dependency error = %#v, want 422-class INVALID_TABLE_SHAPE", err)
	}
	request.DerivedResolutionIDs = []string{firstID, firstID}
	if _, err := service.ProposeTableShape(context.Background(), request); !proposalErrorIs(err, ClassMalformed, "MALFORMED_REQUEST") {
		t.Fatalf("duplicate resolution error = %#v, want malformed request", err)
	}
}

func TestProposeTableShapeComposesUnpivotAndRejectsStaleResolution(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	request := tableShapeProposalBaseRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	repository := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository)
	catalog := repository.catalogs[request.CatalogID]
	unpivotSelection := TableShapeUnpivotSelection{
		InputColumnChoiceIDs: []string{
			catalogColumnChoice(t, catalog, tableshapecap.RoleUnpivotInput, "value"),
			catalogColumnChoice(t, catalog, tableshapecap.RoleUnpivotInput, "value_alt"),
		},
		NullPolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyUnpivotNull, "PRESERVE"),
		KeyOutputColumn:    "measure", KeyOutputLabel: "Measure", ValueOutputColumn: "amount", ValueOutputLabel: "Amount",
	}
	unpivot, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: request.CatalogID, Kind: tableshapecap.ResolutionUnpivot, Unpivot: &unpivotSelection,
	})
	if err != nil {
		t.Fatal(err)
	}
	request.ReshapeResolutionID = unpivot.ResolutionID
	if _, err := service.ProposeTableShape(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	shape := compiledProposalShape(t, store)
	if shape.Reshape == nil || shape.Reshape.Unpivot == nil || len(shape.Reshape.Unpivot.Inputs) != 2 ||
		shape.Reshape.Unpivot.Inputs[0].Column != "value" || shape.Reshape.Unpivot.Inputs[1].Column != "value_alt" ||
		shape.Reshape.Unpivot.KeyOutput.Column != "measure" || shape.Reshape.Unpivot.KeyOutput.Label != "Measure" ||
		shape.Reshape.Unpivot.ValueOutput.Column != "amount" || shape.Reshape.Unpivot.ValueOutput.Label != "Amount" {
		t.Fatalf("proposal did not use the exact resolved unpivot descriptors: %#v", shape.Reshape)
	}
	for index, expected := range []string{"value", "value_alt"} {
		key := shape.Reshape.Unpivot.Inputs[index].Key
		if key.Kind != authoringv2.TableScalarString || key.String == nil || *key.String != expected {
			t.Fatalf("unpivot input %d lost its server-resolved key: %#v, want %q", index, key, expected)
		}
	}

	stale := request
	stale.ReshapeResolutionID = unpivot.ResolutionID
	receipt := repository.resolutions[unpivot.ResolutionID]
	receipt.Binding.OutputID = "other-output"
	repository.resolutions[unpivot.ResolutionID] = receipt
	if _, err := service.ProposeTableShape(context.Background(), stale); !proposalErrorIs(err, ClassConflict, "STALE_TABLE_SHAPE_RESOLUTION") {
		t.Fatalf("cross-output resolution error = %#v, want conflict STALE_TABLE_SHAPE_RESOLUTION", err)
	}
}

func TestProposeTableShapeEnforcesModeAndSupportsReplaceAndRemove(t *testing.T) {
	saved := savedProposalPivotShape()
	for _, test := range []struct {
		name        string
		mode        TableShapeProposalMode
		shape       *authoringv2.TableShape
		wantFailure bool
	}{
		{name: "add with saved shape", mode: TableShapeProposalAdd, shape: saved, wantFailure: true},
		{name: "replace without saved shape", mode: TableShapeProposalReplace, wantFailure: true},
		{name: "remove without saved shape", mode: TableShapeProposalRemove, wantFailure: true},
		{name: "replace with semantic no-op", mode: TableShapeProposalReplace, shape: savedProposalDerivedShape(), wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot := tableShapeProposalServiceWithShape(t, test.shape)
			request := tableShapeProposalBaseRequest(t, service, store.created, snapshot, test.mode)
			if test.name == "replace with semantic no-op" {
				request = tableShapeProposalRequest(t, service, store.created, snapshot, test.mode)
			}
			_, err := service.ProposeTableShape(context.Background(), request)
			if test.wantFailure && !proposalErrorIs(err, ClassUnprocessable, "INVALID_TABLE_SHAPE") {
				t.Fatalf("mode mismatch error = %#v, want 422-class INVALID_TABLE_SHAPE", err)
			}
		})
	}

	for _, mode := range []TableShapeProposalMode{TableShapeProposalReplace, TableShapeProposalRemove} {
		t.Run(string(mode)+" succeeds", func(t *testing.T) {
			service, store, snapshot := tableShapeProposalServiceWithShape(t, savedProposalPivotShape())
			request := tableShapeProposalRequest(t, service, store.created, snapshot, mode)
			proposal, err := service.ProposeTableShape(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			shape := compiledProposalShape(t, store)
			if proposal.Mode != mode || mode == TableShapeProposalRemove && shape != nil || mode == TableShapeProposalReplace && shape == nil {
				t.Fatalf("%s proposal reconstructed wrong candidate: proposal=%#v shape=%#v", mode, proposal, shape)
			}
		})
	}
}

func TestDiscardingTableShapeProposalDoesNotApplyIt(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	if _, err := service.ProposeTableShape(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("discarding proposal changed the saved draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestApplyTableShapeProposalSavesCandidateOnceAndIsIdempotent(t *testing.T) {
	service, store, snapshot, workspace := tableShapeProposalService(t)
	proposalRequest := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	proposal, err := service.ProposeTableShape(context.Background(), proposalRequest)
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
	proposalRequest := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	proposal, err := service.ProposeTableShape(context.Background(), proposalRequest)
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
			proposalRequest := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
			proposal, err := service.ProposeTableShape(context.Background(), proposalRequest)
			if err != nil {
				t.Fatal(err)
			}
			request := tableShapeApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-table-shape")
			if err := test.mutate(store, &request); err != nil {
				t.Fatal(err)
			}
			beforeConfig := append([]byte(nil), store.created.DraftConfig...)
			beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
			if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); !proposalErrorIs(err, ClassConflict, "STALE_TABLE_SHAPE_PROPOSAL") {
				t.Fatalf("stale or tampered proposal error = %#v, want conflict STALE_TABLE_SHAPE_PROPOSAL", err)
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
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
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
	service, store, snapshot := tableShapeProposalServiceWithShape(t, nil)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, snapshot, workspace
}

func tableShapeProposalServiceWithShape(t *testing.T, saved *authoringv2.TableShape) (*Service, *fakeStore, capability.Snapshot) {
	t.Helper()
	service, store, snapshot, _, err := lifecycleTableShapeService(t, saved)
	if err != nil {
		t.Fatal(err)
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		receipt.TableShapeProposal = cloneTableShapeProposalBinding(request.TableShapeProposal)
		var err error
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
	return service, store, snapshot
}

func catalogOperandChoice(t *testing.T, catalog tableshapecap.CatalogReceipt, column string) string {
	t.Helper()
	for _, choice := range catalog.Choices.Operands {
		if choice.Operand.Kind == tableshapecap.OperandColumn && choice.Operand.ColumnKey == column {
			return choice.ID
		}
	}
	t.Fatalf("missing derived operand choice for %s", column)
	return ""
}

func tableShapeProposalRequest(t *testing.T, service *Service, owner *explorer.Explorer, snapshot capability.Snapshot, mode TableShapeProposalMode) TableShapeProposalRequest {
	t.Helper()
	request := tableShapeProposalBaseRequest(t, service, owner, snapshot, mode)
	if mode == TableShapeProposalRemove {
		return request
	}
	catalog := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository).catalogs[request.CatalogID]
	selection := TableShapeDerivedSelection{
		OutputColumn: "patient_id_copy", OutputLabel: "Patient ID copy",
		OperatorChoiceID: catalogOperatorChoice(t, catalog, "ADD"),
		Left: TableShapeOperandSelection{
			Kind:     tableshapecap.ResolvedOperandCatalogChoice,
			ChoiceID: catalogOperandChoice(t, catalog, "value"),
		},
		Right: TableShapeOperandSelection{
			Kind:    tableshapecap.ResolvedOperandLiteral,
			Literal: tableShapeTestPtr(tableshapecap.IntegerScalar(2)),
		},
		MissingPolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDerivedMissing, "PROPAGATE_NULL"),
	}
	resolved, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: request.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &selection,
	})
	if err != nil {
		t.Fatalf("resolve proposal test shape: %v", err)
	}
	request.DerivedResolutionIDs = []string{resolved.ResolutionID}
	return request
}

func tableShapeProposalBaseRequest(t *testing.T, service *Service, owner *explorer.Explorer, snapshot capability.Snapshot, mode TableShapeProposalMode) TableShapeProposalRequest {
	t.Helper()
	catalogResult, err := service.GetTableShapeCatalog(context.Background(), tableShapeCatalogRequest(owner, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	request := TableShapeProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", Mode: mode, CatalogID: catalogResult.CatalogID,
	}
	return request
}

func resolveProposalPivot(t *testing.T, service *Service, request TableShapeProposalRequest, catalog tableshapecap.CatalogReceipt) TableShapeResolutionResult {
	t.Helper()
	discovery, err := service.DiscoverTableShapeCategories(context.Background(), TableShapeCategoryDiscoveryRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: request.CatalogID,
		CategoryColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotCategory, "category"),
		ValueColumnChoiceID:    catalogColumnChoice(t, catalog, tableshapecap.RolePivotValue, "value"),
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := TableShapePivotSelection{
		CategoryDiscoveryID:    discovery.DiscoveryID,
		GroupColumnChoiceIDs:   []string{catalogColumnChoice(t, catalog, tableshapecap.RolePivotGroup, "patient_id")},
		CategoryColumnChoiceID: discovery.CategoryColumnChoiceID, ValueColumnChoiceID: discovery.ValueColumnChoiceID,
		DuplicatePolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDuplicate, "ERROR"),
		MissingPolicyChoiceID:   catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyMissing, "NULL"),
		UnlistedPolicyChoiceID:  catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyUnlisted, "ERROR"),
	}
	for index, category := range discovery.Categories[:2] {
		selection.Categories = append(selection.Categories, TableShapePivotCategorySelection{
			ChoiceID: category.ID, OutputColumn: fmt.Sprintf("bucket_%d", index+1), OutputLabel: fmt.Sprintf("Bucket %d", index+1),
		})
	}
	result, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: request.CatalogID, Kind: tableshapecap.ResolutionPivot, Pivot: &selection,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func compiledProposalShape(t *testing.T, store *fakeStore) *authoringv2.TableShape {
	t.Helper()
	if store.receipt == nil {
		t.Fatal("proposal did not compile a candidate receipt")
	}
	workspace, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	document := proposalDocument(workspace, "patients")
	if document == nil {
		t.Fatal("candidate receipt omitted the table output")
	}
	return document.TableShape
}

func proposalErrorIs(err error, class ErrorClass, code string) bool {
	var lifecycleErr *Error
	return errors.As(err, &lifecycleErr) && lifecycleErr.Class == class && lifecycleErr.Code == code
}

func savedProposalPivotShape() *authoringv2.TableShape {
	return &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{Kind: "PIVOT", Pivot: &authoringv2.PivotConstruction{
		ConstructionID: "pivot_saved", GroupKeys: []string{"patient_id"}, CategoryColumn: "category", ValueColumn: "value",
		Categories:      []authoringv2.PivotCategory{{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: tableShapeTestPtr("saved")}, Output: authoringv2.ColumnOutput{Column: "saved_bucket", Label: "Saved bucket"}}},
		DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
	}}}
}

func savedProposalDerivedShape() *authoringv2.TableShape {
	return &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{{
		ConstructionID: "derived_saved", Output: authoringv2.ColumnOutput{Column: "patient_id_copy", Label: "Patient ID copy"},
		Operation: "ADD", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "value"},
		Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: tableShapeTestPtr(authoringv2.TableScalar{Kind: authoringv2.TableScalarInteger, Integer: tableShapeTestPtr(int64(2))})},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
}

func tableShapeApplyRequest(owner *explorer.Explorer, snapshot capability.Snapshot, proposalID, commandID string) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: proposalID}},
	}
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
