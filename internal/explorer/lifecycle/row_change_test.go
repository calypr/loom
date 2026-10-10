package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestAssessRowChangeCompilesExactCandidateWithoutSavingDraft(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.WorkspaceKind,
		Explorer:   authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}}
	config := testConfig(snapshot)
	var compileCalls int
	var previewCalls int
	var previewReceiptID string
	var compiledWorkspace authoringv2.Workspace
	config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if receipt.ID != previewReceiptID {
			t.Fatalf("preview receipt ID=%q, want %q", receipt.ID, previewReceiptID)
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileCalls++
		compiledWorkspace = request.Workspace
		receipt := nativeReceipt(snapshot)
		var receiptErr error
		receipt.IntentDigest, receiptErr = request.Workspace.Digest()
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.NormalizedBundle, receiptErr = request.Workspace.CanonicalJSON()
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.CompilationKey, receiptErr = explorer.CompilationKey(*receipt)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.ID, receiptErr = explorer.ReceiptID(*receipt)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		if err := receipt.Validate(); err != nil {
			t.Fatal(err)
		}
		store.receipt = receipt
		return receipt, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
		return authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients",
			SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
			RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes: []authoringv2.CatalogNode{
				{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
				{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
			},
			Edges: []authoringv2.CatalogEdge{
				{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
				{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"},
			},
		}
	}
	service := newTestService(t, store, config)
	result, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DraftVersion != 7 || result.DraftDigest != digest || result.Assessment.Status != authoringv2.RowChangeReady || result.Assessment.Proposal == nil || result.CandidateReceiptID == "" {
		t.Fatalf("row change result=%#v", result)
	}
	previewReceiptID = result.CandidateReceiptID
	if compileCalls != 1 || compiledWorkspace.Documents[0].RootResourceType != "Encounter" || compiledWorkspace.Documents[0].Route.ResourceType != "Encounter" {
		t.Fatalf("candidate compile calls=%d workspace=%#v", compileCalls, compiledWorkspace)
	}
	preview, err := service.Preview(context.Background(), PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: result.CandidateReceiptID, OutputID: "patients",
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil {
		t.Fatalf("preview compiled row-change candidate: %v", err)
	}
	if previewCalls != 1 || preview.Summary.RowCount != 1 {
		t.Fatalf("preview calls=%d summary=%#v", previewCalls, preview.Summary)
	}
	if store.created.DraftVersion != 7 || store.created.DraftDigest != digest || string(store.created.DraftConfig) != string(draft) {
		t.Fatalf("assessment wrote draft=%#v", store.created)
	}
	blocked, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter", RouteRebase: []authoringv2.RouteRebaseChoice{{OccurrenceID: authoringv2.RootOccurrenceID, EdgeID: "missing-edge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Assessment.Status != authoringv2.RowChangeBlocked || blocked.CandidateReceiptID != "" || compileCalls != 1 {
		t.Fatalf("blocked assessment compiled a receipt: result=%#v calls=%d", blocked, compileCalls)
	}
	noChange, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if noChange.Assessment.Status != authoringv2.RowChangeNoChange || noChange.CandidateReceiptID != "" || compileCalls != 1 {
		t.Fatalf("no-change assessment compiled a receipt: result=%#v calls=%d", noChange, compileCalls)
	}

	_, err = service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 6, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft error=%v", err)
	}
}
