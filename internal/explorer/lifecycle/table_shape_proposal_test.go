package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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
	if proposal.Comparison.Status != TableShapeComparisonAvailable {
		t.Fatalf("comparison status = %#v", proposal.Comparison)
	}
	if len(compileBindings) != 1 || compileBindings[0] == nil {
		t.Fatalf("compile proposal bindings = %#v, want only the bound candidate after reusing the persisted base", compileBindings)
	}
	binding := store.receipt.TableShapeProposal
	if binding == nil || *binding != *compileBindings[0] || binding.DraftVersion != beforeVersion ||
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

func TestProposeTableShapeRequiresPreviewConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Service)
	}{
		{name: "preview executor is absent", configure: func(service *Service) {
			service.config.PreviewReceipt = nil
		}},
		{name: "execution authorization is absent", configure: func(service *Service) {
			service.config.Capability.ForExecution = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := tableShapeProposalService(t)
			request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
			test.configure(service)
			compileCalls := 0
			compile := service.config.CompileReceipt
			service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
				compileCalls++
				return compile(ctx, request)
			}
			before := append([]byte(nil), store.created.DraftConfig...)
			version, digest := store.created.DraftVersion, store.created.DraftDigest
			_, err := service.ProposeTableShape(context.Background(), request)
			if !proposalErrorIs(err, ClassUnavailable, "PREVIEW_UNAVAILABLE") {
				t.Fatalf("missing table-shape preview configuration error = %v", err)
			}
			if compileCalls != 0 || store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
				t.Fatalf("missing preview configuration compiled or mutated state: compiles=%d saves=%d owner=%#v", compileCalls, store.saveDraftCalls, store.created)
			}
		})
	}
}

func TestCompareTableShapeReceiptsRejectsMissingOutputContract(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	request := tableShapeProposalBaseRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	request.OutputID = "missing-output"
	if store.receipt == nil {
		t.Fatal("base table-shape receipt was not loaded")
	}
	_, err := service.compareTableShapeReceipts(context.Background(), request, snapshot, store.receipt, store.receipt, 25, &tableShapeProposalTimings{})
	if !proposalErrorIs(err, ClassInternal, "PREVIEW_OUTPUT_INVALID") {
		t.Fatalf("missing output contract error = %v, want internal receipt failure", err)
	}
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Cause == nil {
		t.Fatalf("missing output contract did not preserve its cause: %#v", err)
	}
}

func TestProposeTableShapeCandidateFeatureErrorReturnsValidationError(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	featureFailure := dataframeerrors.NewError(dataframeerrors.CodeTablePivotCellCardinality, "")
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt.TableShapeProposal != nil {
			return dataframeexecution.PreviewSummary{}, featureFailure
		}
		if err := visit(map[string]any{"__loom_row_id": "row-1", "value": int64(1)}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"value"}, RowCount: 1, Complete: true}, nil
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	proposal, err := service.ProposeTableShape(context.Background(), request)
	var lifecycleErr *Error
	if proposal.ProposalID != "" || !proposalErrorIs(err, ClassUnprocessable, string(dataframeerrors.CodeTablePivotCellCardinality)) || !errors.As(err, &lifecycleErr) || lifecycleErr.Message != dataframeerrors.PublicMessage(featureFailure) || !errors.Is(err, featureFailure) {
		t.Fatalf("candidate table-shape feature error = proposal %#v, error %v; want caused unprocessable feature error", proposal, err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("candidate feature error mutated the draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestProposeTableShapePreviewFailuresPropagate(t *testing.T) {
	for _, test := range []struct {
		name         string
		failAt       string
		identityFail bool
		duplicateID  bool
		wantCode     string
		wantCause    bool
	}{
		{name: "base executor failure", failAt: "base", wantCode: "BASE_PREVIEW_FAILED", wantCause: true},
		{name: "candidate executor failure", failAt: "candidate", wantCode: "CANDIDATE_PREVIEW_FAILED", wantCause: true},
		{name: "candidate identity failure", failAt: "candidate", identityFail: true, wantCode: "CANDIDATE_PREVIEW_FAILED"},
		{name: "candidate duplicate identity", failAt: "candidate", duplicateID: true, wantCode: "CANDIDATE_PREVIEW_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _ := tableShapeProposalService(t)
			request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
			executorFailure := errors.New("preview backend failed")
			service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
				stage := "base"
				if receipt.TableShapeProposal != nil {
					stage = "candidate"
				}
				summary := dataframeexecution.PreviewSummary{Columns: []string{"value"}, RowCount: 1, Complete: true}
				if stage == test.failAt {
					if test.identityFail {
						return summary, visit(map[string]any{"value": int64(1)})
					}
					if test.duplicateID {
						row := map[string]any{"__loom_row_id": "row-1", "value": int64(1)}
						if err := visit(row); err != nil {
							return summary, err
						}
						return summary, visit(row)
					}
					return summary, fmt.Errorf("preview execution failed: %w", executorFailure)
				}
				if err := visit(map[string]any{"__loom_row_id": "row-1", "value": int64(1)}); err != nil {
					return summary, err
				}
				return summary, nil
			}
			before := append([]byte(nil), store.created.DraftConfig...)
			version, digest := store.created.DraftVersion, store.created.DraftDigest
			proposal, err := service.ProposeTableShape(context.Background(), request)
			if !proposalErrorIs(err, ClassInternal, test.wantCode) {
				t.Fatalf("table-shape preview error = %v, want internal %s", err, test.wantCode)
			}
			if proposal.ProposalID != "" {
				t.Fatalf("failed table-shape preview issued proposal %q", proposal.ProposalID)
			}
			var lifecycleErr *Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassInternal || lifecycleErr.Cause == nil {
				t.Fatalf("table-shape preview error did not preserve its cause: %#v", err)
			}
			if test.wantCause && !errors.Is(err, executorFailure) {
				t.Fatalf("table-shape preview error lost executor cause: %v", err)
			}
			if test.identityFail && !strings.Contains(lifecycleErr.Cause.Error(), "stable identity") {
				t.Fatalf("table-shape identity error cause = %v", lifecycleErr.Cause)
			}
			if test.duplicateID && !strings.Contains(lifecycleErr.Cause.Error(), "duplicate stable row identity") {
				t.Fatalf("table-shape duplicate-identity cause = %v", lifecycleErr.Cause)
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
				t.Fatalf("table-shape preview failure mutated the draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
			}
		})
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

func TestApplyTableShapeProposalMigratesV9CandidateAndKeepsBaseCAS(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	baseDigest := persistV9TableShapeDraft(t, store)
	compile := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		prepared, err := authoringv2.PrepareWorkspaceForCompilation(request.Workspace, service.catalog(snapshot, "patients"))
		if err != nil {
			return nil, err
		}
		request.Workspace = prepared
		return compile(ctx, request)
	}
	baseBytes := append([]byte(nil), store.created.DraftConfig...)
	baseVersion := store.created.DraftVersion
	proposalRequest := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	proposal, err := service.ProposeTableShape(context.Background(), proposalRequest)
	if err != nil {
		t.Fatalf("propose table shape from v9 draft: %v", err)
	}
	if proposal.DraftDigest != baseDigest || proposal.CandidateWorkspaceDigest == baseDigest {
		t.Fatalf("proposal base/candidate digests = %q/%q, want raw v9 base %q and distinct normalized candidate", proposal.DraftDigest, proposal.CandidateWorkspaceDigest, baseDigest)
	}
	if store.receipt.TableShapeProposal == nil || store.receipt.TableShapeProposal.DraftDigest != baseDigest || store.receipt.TableShapeProposal.CandidateWorkspaceDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("receipt proposal binding = %#v, want raw v9 base and normalized candidate digest", store.receipt.TableShapeProposal)
	}
	if store.created.DraftDigest != baseDigest || store.created.DraftVersion != baseVersion || string(store.created.DraftConfig) != string(baseBytes) {
		t.Fatal("proposal changed the persisted v9 base draft")
	}
	candidate, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.SemanticsVersion != authoringv2.CurrentSemanticsVersion {
		t.Fatalf("candidate semantics version = %d, want %d", candidate.SemanticsVersion, authoringv2.CurrentSemanticsVersion)
	}
	candidateDigest, err := candidate.Digest()
	if err != nil || candidateDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("normalized candidate digest=%q, err=%v, want proposal digest %q", candidateDigest, err, proposal.CandidateWorkspaceDigest)
	}
	request := tableShapeApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-v9-table-shape")
	if request.ExpectedDraftDigest != baseDigest {
		t.Fatalf("apply base CAS=%q, want persisted v9 digest %q", request.ExpectedDraftDigest, baseDigest)
	}
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err != nil {
		t.Fatalf("apply table shape against v9 base: %v", err)
	}
	if store.created.DraftVersion != baseVersion+1 || store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("applied owner version/digest=%d/%q, want %d/%q", store.created.DraftVersion, store.created.DraftDigest, baseVersion+1, proposal.CandidateWorkspaceDigest)
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
	traceCalls := 0
	service.config.CellTrace = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		traceCalls++
		if receipt.TableShapeProposal == nil || receipt.TableShapeProposal.OutputID != "patients" ||
			bindings.DatasetGeneration != snapshot.Identity.Generation || request.Limit != compiler.MaxCellTraceContributions || request.Offset != 0 {
			t.Fatalf("cell trace was not bound to the candidate receipt: receipt=%#v bindings=%#v request=%#v", receipt, bindings, request)
		}
		var contributions []dataframeexecution.CellTraceContribution
		switch request.RowID + "/" + request.Column {
		case "row-1/patient_id_copy":
			contributions = []dataframeexecution.CellTraceContribution{
				{ResourceType: "Patient", ResourceID: "p1", Value: "p1"},
				{ResourceType: "Observation", ResourceID: "obs-2", Value: "red"},
				{ResourceType: "Observation", ResourceID: "obs-2", Value: "red"},
				{ResourceType: "Observation", ResourceID: "obs-2", Value: "green"},
			}
		case "row-3/patient_id":
			contributions = []dataframeexecution.CellTraceContribution{{ResourceType: "Patient", ResourceID: "p3", Value: "p3"}}
		case "row-3/patient_id_copy":
			contributions = []dataframeexecution.CellTraceContribution{{ResourceType: "Observation", ResourceID: "obs-3", Value: "sample"}}
		default:
			t.Fatalf("unexpected cell trace request: %#v", request)
		}
		return dataframeexecution.CellTraceResult{
			RowID: request.RowID, Column: request.Column, Status: dataframeexecution.CellTraceValue,
			Contributions: contributions, Complete: true,
		}, nil
	}
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
			if err := visit(map[string]any{"__loom_row_id": "row-1", "patient_id": "p1", "value": 1, "__loom_reshape_unlisted_count": 10}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			if err := visit(map[string]any{"__loom_row_id": "row-2", "patient_id": "p2", "value": 2, "__loom_reshape_unlisted_count": 20}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Columns: []string{"patient_id", "value", "__loom_reshape_unlisted_count"}, RowCount: 100, Truncated: true}, nil
		}
		if err := visit(map[string]any{"__loom_row_id": "row-1", "patient_id": "p1", "patient_id_copy": 2, "__loom_reshape_unlisted_count": 1}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		if err := visit(map[string]any{"__loom_row_id": "row-3", "patient_id": "p3", "patient_id_copy": 6, "__loom_reshape_unlisted_count": 2}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"patient_id", "patient_id_copy", "__loom_reshape_unlisted_count"}, RowCount: 100, Truncated: true}, nil
	}
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	request.Limit = 2
	proposal, err := service.ProposeTableShape(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	comparison := proposal.Comparison
	if previewCalls != 2 || traceCalls != 3 || comparison.Status != TableShapeComparisonAvailable || !comparison.Base.Sampled || !comparison.Candidate.Sampled || !comparison.ChangedRowsSampled {
		t.Fatalf("preview comparison summary = %#v, preview calls=%d trace calls=%d", comparison, previewCalls, traceCalls)
	}
	if !reflect.DeepEqual(comparison.ChangedColumns, []string{"patient_id", "patient_id_copy", "value"}) {
		t.Fatalf("changed columns = %#v", comparison.ChangedColumns)
	}
	for _, row := range comparison.ChangedRows {
		for _, column := range row.ChangedColumns {
			if column == "__loom_reshape_unlisted_count" {
				t.Fatalf("physical reshape evidence leaked as a changed column: %#v", row)
			}
		}
		for _, cell := range row.ChangedCells {
			if cell.Column == "__loom_reshape_unlisted_count" {
				t.Fatalf("physical reshape evidence leaked as a changed cell: %#v", cell)
			}
		}
	}
	if comparison.ChangedRowCount != 3 || len(comparison.ChangedRows) != 3 {
		t.Fatalf("changed row summary = count %d, rows %#v", comparison.ChangedRowCount, comparison.ChangedRows)
	}
	if got := comparison.ChangedRows[0]; got.RowIdentity != "row-1" || !got.BasePresent || !got.CandidatePresent || !reflect.DeepEqual(got.ChangedColumns, []string{"patient_id_copy", "value"}) {
		t.Fatalf("changed shared row = %#v", got)
	}
	changedCopy := tableShapeChangedCell(t, comparison.ChangedRows[0], "patient_id_copy")
	if changedCopy.Before.Present || changedCopy.Before.Value != nil || !changedCopy.After.Present || changedCopy.After.Value != 2 {
		t.Fatalf("changed column cell values = %#v, want missing before and integer 2 after", changedCopy)
	}
	if changedCopy.Trace.State != TableShapeCellTraceAvailable || !changedCopy.Trace.Complete ||
		!reflect.DeepEqual(changedCopy.Trace.Contributors, []TableShapeCellContributor{
			{ResourceType: "Observation", ResourceID: "obs-2", Value: "green"},
			{ResourceType: "Observation", ResourceID: "obs-2", Value: "red"},
			{ResourceType: "Patient", ResourceID: "p1", Value: "p1"},
		}) {
		t.Fatalf("candidate cell lineage = %#v", changedCopy.Trace)
	}
	encodedContributors, err := json.Marshal(changedCopy.Trace.Contributors)
	if err != nil {
		t.Fatal(err)
	}
	var wireContributors []struct {
		ResourceType string          `json:"resourceType"`
		ResourceID   string          `json:"resourceId"`
		Value        json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(encodedContributors, &wireContributors); err != nil {
		t.Fatal(err)
	}
	if len(wireContributors) != 3 || string(wireContributors[0].Value) != `"green"` || string(wireContributors[1].Value) != `"red"` {
		t.Fatalf("serialized source contribution values = %#v", wireContributors)
	}
	removedValue := tableShapeChangedCell(t, comparison.ChangedRows[0], "value")
	if !removedValue.Before.Present || removedValue.Before.Value != 1 || removedValue.After.Present || removedValue.Trace.State != TableShapeCellTraceNotApplicable {
		t.Fatalf("removed cell evidence = %#v", removedValue)
	}
	if got := comparison.ChangedRows[1]; got.RowIdentity != "row-2" || !got.BasePresent || got.CandidatePresent {
		t.Fatalf("removed row = %#v", got)
	}
	if got := tableShapeChangedCell(t, comparison.ChangedRows[1], "patient_id"); !got.Before.Present || got.Before.Value != "p2" || got.After.Present {
		t.Fatalf("removed row cell evidence = %#v", got)
	}
	if got := comparison.ChangedRows[2]; got.RowIdentity != "row-3" || got.BasePresent || !got.CandidatePresent {
		t.Fatalf("added row = %#v", got)
	}
	if got := tableShapeChangedCell(t, comparison.ChangedRows[2], "patient_id_copy"); got.Before.Present || !got.After.Present || got.After.Value != 6 {
		t.Fatalf("added row cell evidence = %#v", got)
	}
	wantContributors := []TableShapeContributor{
		{ResourceType: "Observation", ResourceID: "obs-2"},
		{ResourceType: "Observation", ResourceID: "obs-3"},
		{ResourceType: "Patient", ResourceID: "p1"},
		{ResourceType: "Patient", ResourceID: "p3"},
	}
	if !reflect.DeepEqual(comparison.Contributors, wantContributors) || !comparison.ContributorsSampled {
		t.Fatalf("comparison contributor union = %#v, sampled=%t", comparison.Contributors, comparison.ContributorsSampled)
	}
	if len(comparison.EvidenceLimitations) != 1 || comparison.EvidenceLimitations[0].Code != "TABLE_SHAPE_EXCLUSION_EXECUTOR_UNAVAILABLE" {
		t.Fatalf("comparison evidence limitations = %#v", comparison.EvidenceLimitations)
	}
}

func tableShapeChangedCell(t *testing.T, row TableShapeChangedRow, column string) TableShapeChangedCell {
	t.Helper()
	for _, cell := range row.ChangedCells {
		if cell.Column == column {
			return cell
		}
	}
	t.Fatalf("changed cell %q not found in %#v", column, row.ChangedCells)
	return TableShapeChangedCell{}
}

func setSingleValueTableShapePreview(service *Service) {
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		value := int64(1)
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		if workspace.Documents[0].TableShape != nil {
			value = 2
		}
		if err := visit(map[string]any{"__loom_row_id": "row-1", "value": value}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"value"}, RowCount: 1, Complete: true}, nil
	}
}

func TestTableShapeComparisonPreservesCellPresenceAndDegradesTraceFailures(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	traceCalls := 0
	service.config.CellTrace = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		traceCalls++
		if request.Limit != compiler.MaxCellTraceContributions || request.Offset != 0 {
			t.Fatalf("trace request bounds = %#v", request)
		}
		if request.Column == "zero_value" {
			return dataframeexecution.CellTraceResult{}, errors.New("trace database unavailable")
		}
		result := dataframeexecution.CellTraceResult{
			RowID: request.RowID, Column: request.Column, Status: dataframeexecution.CellTraceValue, Complete: true,
			Contributions: []dataframeexecution.CellTraceContribution{},
		}
		if request.Column == "explicit_null" {
			result.Status = dataframeexecution.CellTraceRecordedNull
			result.OmissionCode = "TABLE_SHAPE_SOURCE_LINEAGE_UNAVAILABLE"
		}
		return result, nil
	}
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		row := map[string]any{"__loom_row_id": "row-1"}
		columns := []string{"explicit_null", "false_value", "zero_value", "empty_value", "removed_null"}
		if workspace.Documents[0].TableShape == nil {
			row["removed_null"] = nil
			row["existing"] = int64(1)
			columns = append(columns, "existing")
		} else {
			row["explicit_null"] = nil
			row["false_value"] = false
			row["zero_value"] = int64(0)
			row["empty_value"] = ""
			row["existing"] = int64(2)
			columns = append(columns, "existing")
		}
		if err := visit(row); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Columns: columns, RowCount: 1, Complete: true}, nil
	}
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	request.Limit = 1
	proposal, err := service.ProposeTableShape(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	comparison := proposal.Comparison
	if comparison.Status != TableShapeComparisonAvailable || len(comparison.ChangedRows) != 1 || traceCalls != 5 {
		t.Fatalf("comparison or trace calls = %#v, calls=%d", comparison, traceCalls)
	}
	row := comparison.ChangedRows[0]
	if cell := tableShapeChangedCell(t, row, "explicit_null"); cell.Before.Present || !cell.After.Present || cell.After.Value != nil ||
		cell.Trace.CellStatus != string(dataframeexecution.CellTraceRecordedNull) || cell.Trace.Complete || cell.Trace.OmissionCode != "TABLE_SHAPE_SOURCE_LINEAGE_UNAVAILABLE" {
		t.Fatalf("missing-to-recorded-null evidence = %#v", cell)
	}
	if cell := tableShapeChangedCell(t, row, "false_value"); cell.Before.Present || !cell.After.Present || cell.After.Value != false {
		t.Fatalf("false value was not retained = %#v", cell)
	}
	if cell := tableShapeChangedCell(t, row, "zero_value"); cell.Before.Present || !cell.After.Present || cell.After.Value != int64(0) ||
		cell.Trace.State != TableShapeCellTraceFailed || cell.Trace.FailureCode != "CELL_TRACE_FAILED" || !cell.Trace.Sampled {
		t.Fatalf("zero value or failed trace evidence = %#v", cell)
	}
	if cell := tableShapeChangedCell(t, row, "empty_value"); cell.Before.Present || !cell.After.Present || cell.After.Value != "" {
		t.Fatalf("empty string was not retained = %#v", cell)
	}
	if cell := tableShapeChangedCell(t, row, "removed_null"); !cell.Before.Present || cell.Before.Value != nil || cell.After.Present {
		t.Fatalf("recorded-null-to-missing evidence = %#v", cell)
	}
	if !comparison.ContributorsSampled || len(comparison.EvidenceLimitations) != 1 || comparison.EvidenceLimitations[0].Code != "TABLE_SHAPE_EXCLUSION_EXECUTOR_UNAVAILABLE" {
		t.Fatalf("incomplete lineage or fixed comparison limitations were hidden: %#v", comparison)
	}
	encoded, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ChangedRows []struct {
			ChangedCells []struct {
				Column string `json:"column"`
				Before struct {
					Present bool            `json:"present"`
					Value   json.RawMessage `json:"value"`
				} `json:"before"`
				After struct {
					Present bool            `json:"present"`
					Value   json.RawMessage `json:"value"`
				} `json:"after"`
			} `json:"changedCells"`
		} `json:"changedRows"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	wireCells := make(map[string]struct {
		beforePresent bool
		beforeValue   string
		afterPresent  bool
		afterValue    string
	})
	for _, changedRow := range wire.ChangedRows {
		for _, cell := range changedRow.ChangedCells {
			wireCells[cell.Column] = struct {
				beforePresent bool
				beforeValue   string
				afterPresent  bool
				afterValue    string
			}{cell.Before.Present, string(cell.Before.Value), cell.After.Present, string(cell.After.Value)}
		}
	}
	for column, want := range map[string]struct {
		beforePresent bool
		beforeValue   string
		afterPresent  bool
		afterValue    string
	}{
		"explicit_null": {false, "null", true, "null"},
		"false_value":   {false, "null", true, "false"},
		"zero_value":    {false, "null", true, "0"},
		"empty_value":   {false, "null", true, `""`},
		"removed_null":  {true, "null", false, "null"},
	} {
		if got := wireCells[column]; got != want {
			t.Fatalf("serialized %s cell = %#v, want %#v", column, got, want)
		}
	}
}

func TestTableShapeComparisonBoundsTracesAndGlobalContributors(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	columns := make([]string, 8)
	for index := range columns {
		columns[index] = fmt.Sprintf("value_%02d", index)
	}
	traceCalls := make(map[string]int)
	service.config.CellTrace = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		traceCalls[request.RowID]++
		if request.Limit != compiler.MaxCellTraceContributions || request.Offset != 0 {
			t.Fatalf("trace request bounds = %#v", request)
		}
		contributions := make([]dataframeexecution.CellTraceContribution, 0, 10)
		for index := 0; index < 10; index++ {
			contributions = append(contributions, dataframeexecution.CellTraceContribution{
				ResourceType: "Patient", ResourceID: fmt.Sprintf("%s-%s-%02d", request.RowID, request.Column, index),
			})
		}
		return dataframeexecution.CellTraceResult{
			RowID: request.RowID, Column: request.Column, Status: dataframeexecution.CellTraceValue,
			Contributions: contributions, Complete: true,
		}, nil
	}
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		for _, id := range []string{"row-1", "row-2"} {
			row := map[string]any{"__loom_row_id": id}
			for _, column := range columns {
				value := int64(0)
				if workspace.Documents[0].TableShape != nil {
					value = 1
				}
				row[column] = value
			}
			if err := visit(row); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
		}
		return dataframeexecution.PreviewSummary{Columns: columns, RowCount: 2, Complete: true}, nil
	}
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	request.Limit = 2
	proposal, err := service.ProposeTableShape(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	comparison := proposal.Comparison
	if len(comparison.ChangedRows) != 2 || traceCalls["row-1"] != maxProposalComparisonTraceColumns || traceCalls["row-2"] != maxProposalComparisonTraceColumns {
		t.Fatalf("trace calls did not honor per-example bound: rows=%#v calls=%#v", comparison.ChangedRows, traceCalls)
	}
	for _, row := range comparison.ChangedRows {
		for index, cell := range row.ChangedCells {
			if index < maxProposalComparisonTraceColumns && cell.Trace.State != TableShapeCellTraceAvailable {
				t.Fatalf("trace %s/%s unexpectedly skipped: %#v", row.RowIdentity, cell.Column, cell.Trace)
			}
			if index >= maxProposalComparisonTraceColumns &&
				(cell.Trace.State != TableShapeCellTraceNotRequested || cell.Trace.OmissionCode != "TABLE_SHAPE_TRACE_COLUMN_LIMIT" || !cell.Trace.Sampled) {
				t.Fatalf("trace %s/%s did not disclose the per-example limit: %#v", row.RowIdentity, cell.Column, cell.Trace)
			}
		}
	}
	if len(comparison.Contributors) != maxProposalComparisonContributors || !comparison.ContributorsSampled {
		t.Fatalf("global contributor list is not bounded and marked sampled: count=%d sampled=%t", len(comparison.Contributors), comparison.ContributorsSampled)
	}
	globalLimitNotice := false
	cellLimitNotice := false
	for _, notice := range comparison.Notices {
		globalLimitNotice = globalLimitNotice || notice == "The global contributor list is limited to 100 unique resource identities."
		cellLimitNotice = cellLimitNotice || notice == "Cell contributor evidence is limited to 6 changed candidate columns per example."
	}
	if !globalLimitNotice || !cellLimitNotice {
		t.Fatalf("bounded evidence lacks explicit notices: %#v", comparison.Notices)
	}
	all := make([]TableShapeContributor, 0, 120)
	for _, rowID := range []string{"row-1", "row-2"} {
		for _, column := range columns[:maxProposalComparisonTraceColumns] {
			for index := 0; index < 10; index++ {
				all = append(all, TableShapeContributor{
					ResourceType: "Patient", ResourceID: fmt.Sprintf("%s-%s-%02d", rowID, column, index),
				})
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return tableShapeContributorKey(all[i]) < tableShapeContributorKey(all[j]) })
	if !reflect.DeepEqual(comparison.Contributors, all[:maxProposalComparisonContributors]) {
		t.Fatalf("global contributors are not the deterministic bounded union: got first=%#v want first=%#v", comparison.Contributors[:3], all[:3])
	}
}

func TestTableShapeComparisonTraceCancellationDoesNotMutateDraft(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	setSingleValueTableShapePreview(service)
	ctx, cancel := context.WithCancel(context.Background())
	service.config.CellTrace = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		cancel()
		return dataframeexecution.CellTraceResult{}, context.Canceled
	}
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	request.Limit = 1
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	if _, err := service.ProposeTableShape(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cell trace error = %v", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("cancelled proposal mutated draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestTableShapeComparisonPropagatesStaleTraceAuthorization(t *testing.T) {
	service, store, snapshot, _ := tableShapeProposalService(t)
	setSingleValueTableShapePreview(service)
	service.config.CellTrace = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		return dataframeexecution.CellTraceResult{}, conflict("cellTrace", "RECEIPT_STALE", "authorization changed during comparison", nil, nil)
	}
	request := tableShapeProposalRequest(t, service, store.created, snapshot, TableShapeProposalAdd)
	request.Limit = 1
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	if _, err := service.ProposeTableShape(context.Background(), request); !proposalErrorIs(err, ClassConflict, "RECEIPT_STALE") {
		t.Fatalf("stale cell trace error = %v", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeConfig) {
		t.Fatalf("stale proposal mutated draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
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
	setSingleValueTableShapePreview(service)
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
