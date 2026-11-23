package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestProposeConstructionGroupMeanRecalculatesDependentFilterWithRealCompiler(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	snapshot.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, Populated: true}}
	idCandidate := traceFieldCandidate(snapshot.Token, "candidate-id", "id", "string")
	numericCandidate := traceFieldCandidate(snapshot.Token, "candidate-birth-count", "multipleBirthInteger", "integer")
	snapshot.Candidates = []capability.Candidate{idCandidate, numericCandidate}
	service.config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }
	service.config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service.config.Capability.ForExecution = service.config.Capability.ForCompilation
	service.config.Capability.Catalog = func(got capability.Snapshot, explorerID string) ws.CatalogSnapshot {
		catalog := lifecycleInterpretationCatalog(got, explorerID)
		for _, candidate := range []capability.Candidate{numericCandidate} {
			choice, err := capability.NewFieldConstructionChoice(got.Token, candidate)
			if err != nil {
				t.Fatalf("make source choice for %s: %v", candidate.ID, err)
			}
			catalog.Candidates = append(catalog.Candidates, ws.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
			})
		}
		return catalog
	}

	workspace, baseDocument, baseConstruction := traceGroupMeanWorkspace(t, store)
	compileCount := 0
	service.config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCount++
		receipt, err := compileTraceReceipt(ctx, request, snapshot, service.catalog(snapshot, request.ExplorerID))
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	service.config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.ConstructionProposal == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview did not receive the proposal's exact compiled receipt")
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: emittedPublicColumns(receipt), Complete: true}, nil
	}

	candidate := cloneLifecycleTestConstruction(t, baseConstruction)
	candidate.Steps[0].Operation.Group.Aggregates[0] = ws.ConstructionGroupAggregate{
		Operation: ws.ConstructionGroupMean, InputColumnID: "birth-count", OutputColumnID: "row_count",
	}
	candidate.Steps[0].Outputs[1].Type = "decimal"

	acceptedBefore := append([]byte(nil), store.created.DraftConfig...)
	versionBefore, digestBefore := store.created.DraftVersion, store.created.DraftDigest
	analyzed, impact, err := baseDocument.AnalyzeConstructionCandidate(candidate, "group-records", nil)
	if err != nil {
		t.Fatalf("AnalyzeConstructionCandidate: %v", err)
	}
	if impact.HasMissingInputs() {
		t.Fatalf("MEAN edit unexpectedly needs repair: %#v", impact.MissingInputs)
	}
	if got := traceStageColumnType(analyzed.Construction.Steps[0], "row_count"); got != "decimal" {
		t.Fatalf("Document.AnalyzeConstructionCandidate Group output type = %q, want decimal", got)
	}
	if got := traceStageColumnType(analyzed.Construction.Steps[1], "row_count"); got != "decimal" {
		t.Fatalf("Document.AnalyzeConstructionCandidate dependent FILTER input type = %q, want decimal", got)
	}

	proposal, err := service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: versionBefore, ExpectedDraftDigest: digestBefore, OutputID: "patients",
		ChangedStepID: "group-records", CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatalf("ProposeConstruction: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("proposal status/id = %q/%q, want a compiled preview proposal", proposal.PreviewStatus, proposal.ProposalID)
	}
	if got := traceStageColumnType(proposal.CandidateConstruction.Steps[0], "row_count"); got != "decimal" {
		t.Fatalf("proposal Group output type = %q, want decimal", got)
	}
	if got := traceStageColumnType(proposal.CandidateConstruction.Steps[1], "row_count"); got != "decimal" {
		t.Fatalf("proposal dependent FILTER input type = %q, want decimal", got)
	}
	if got := traceReceiptStageColumnType(store.receipt, "group-records", "row_count"); got != "decimal" {
		t.Fatalf("actual compiler/lowering GROUP stage output type = %q, want decimal", got)
	}
	if compileCount < 2 {
		t.Fatalf("actual compiler ran %d times, want base and proposal receipt compilation", compileCount)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != versionBefore || store.created.DraftDigest != digestBefore || string(store.created.DraftConfig) != string(acceptedBefore) {
		t.Fatal("ProposeConstruction changed the accepted draft before Apply")
	}

	preview, err := service.Preview(context.Background(), PreviewRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, ReceiptID: proposal.ProposalID, OutputID: "patients",
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil || preview.Receipt == nil || preview.Receipt.ID != proposal.ProposalID {
		t.Fatalf("exact proposal preview receipt=%#v err=%v", preview.Receipt, err)
	}

	apply, err := service.ApplyCommands(context.Background(), store.created.Project, store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
		CommandID: "trace-apply-group-mean", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: versionBefore, ExpectedDraftDigest: digestBefore,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "trace-actor")
	if err != nil {
		t.Fatalf("ApplyCommands exact proposal: %v", err)
	}
	if apply.Workspace.Documents[0].Construction == nil || traceReceiptStageColumnType(store.receipt, "group-records", "row_count") != "decimal" {
		t.Fatalf("Apply acknowledgement omitted applied MEAN construction: %#v", apply.Workspace.Documents[0].Construction)
	}
	if store.created.DraftVersion != versionBefore+1 || store.created.DraftDigest == digestBefore || store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("Apply acknowledgement did not advance the accepted draft exactly once: version=%d digest=%q proposal=%q", store.created.DraftVersion, store.created.DraftDigest, proposal.CandidateWorkspaceDigest)
	}
	if store.saveDraftCalls != 1 {
		t.Fatalf("Apply acknowledgement saved %d times, want exactly once", store.saveDraftCalls)
	}
	appliedDocument := apply.Workspace.Documents[0]
	gotConstruction, err := json.Marshal(appliedDocument.Construction)
	if err != nil {
		t.Fatal(err)
	}
	wantConstruction, err := json.Marshal(analyzed.Construction)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotConstruction) != string(wantConstruction) {
		t.Fatalf("Apply acknowledgement construction = %#v, want analyzed construction %#v", appliedDocument.Construction, analyzed.Construction)
	}
	if appliedDocument.Output != baseDocument.Output || appliedDocument.RootResourceType != baseDocument.RootResourceType || !reflect.DeepEqual(appliedDocument.Columns, baseDocument.Columns) {
		t.Fatalf("Apply acknowledgement changed non-construction document fields: output=%#v root=%q columns=%#v", appliedDocument.Output, appliedDocument.RootResourceType, appliedDocument.Columns)
	}
	_ = workspace
}

func traceGroupMeanWorkspace(t *testing.T, store *fakeStore) (ws.Workspace, ws.Document, ws.Construction) {
	t.Helper()
	workspace := lifecycleCandidatePreviewWorkspace()
	workspace.SemanticsVersion = ws.CurrentSemanticsVersion
	workspace.Documents[0].Rows = ws.RecordsRowDefinition()
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Columns) == 0 {
		t.Fatal("trace source workspace has no source columns")
	}
	if document.Columns[0].ColumnID == "" {
		document.Columns[0].ColumnID = "patient-id"
	}
	patientIDColumnID := document.Columns[0].ColumnID
	document.Columns = append(document.Columns, ws.Column{
		ColumnID: "birth-count", Column: "birth_count", Label: "Birth count", LogicalType: "integer",
		OccurrenceID: ws.RootOccurrenceID,
		Source:       ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "multipleBirthInteger", ProjectionMode: "VALUE"}},
	})
	sort.SliceStable(document.Columns, func(left, right int) bool {
		return document.Columns[left].ColumnID < document.Columns[right].ColumnID
	})
	groupOutputs := []ws.StageColumn{
		{ID: "patient-key", Name: "patient_key", Label: "Patient", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Aggregate", Type: "integer"},
	}
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID: "group-records", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group-records", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
				Keys:       []ws.ConstructionGroupKey{{InputColumnID: patientIDColumnID, OutputColumnID: "patient-key"}},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: groupOutputs,
		},
		{
			ID: "dependent-filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group-records"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "row_count", Operator: ws.ConstructionFilterExists,
			}},
			Outputs: append([]ws.StageColumn(nil), groupOutputs...),
		},
	}}
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate trace workspace: %v", err)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	return workspace, document, construction
}
