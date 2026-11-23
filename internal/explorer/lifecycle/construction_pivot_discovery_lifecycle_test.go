package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/explorer/construction"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestProposeConstructionResolvesPivotDiscoveryToConcreteCategories(t *testing.T) {
	service, store, snapshot, categoryColumnID, valueColumnID := constructionProposalPivotFixture(t)
	scans := 0
	service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		scans++
		if receipt == nil || scan.Output != "patients" || scan.StageID != recipe.ConstructionSourceProjectionID ||
			scan.ColumnID != categoryColumnID || scan.ValueColumnID != valueColumnID || !scan.RequireValue {
			t.Fatalf("discovery scan request = %#v", scan)
		}
		var category explorer.ReceiptConstructionStageColumn
		for _, stage := range receipt.ConstructionStages["patients"] {
			if stage.ID != scan.StageID {
				continue
			}
			for _, column := range stage.Columns {
				if column.ID == scan.ColumnID {
					category = column
				}
			}
		}
		proof := compilerCategoryScanProof(construction.ConstructionCategoryDiscoveryRequest{
			OutputID: scan.Output, StageID: scan.StageID,
			CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID,
		}, category)
		return dataframeexecution.CategoryScanResult{
			Values: []dataframeexecution.CategoryValue{
				{Present: true, Value: "final"},
				{Present: true, Value: "preliminary"},
			},
			Complete: true, Proof: proof,
		}, nil
	}
	request := constructionProposalPivotRequest(store.created, snapshot, nil)
	request.CandidateConstruction.Steps[0].Operation.Pivot.Discovery = &authoringv2.ConstructionPivotDiscovery{MaxCategories: 4}
	request.CandidateConstruction.Steps[0].Outputs = []authoringv2.StageColumn{
		{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
	}

	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatalf("propose discovery Pivot: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("discovery proposal response = %#v", proposal)
	}
	if scans != 1 {
		t.Fatalf("discovery scans = %d, want exactly one value-bearing scan", scans)
	}
	pivot := proposal.CandidateConstruction.Steps[0].Operation.Pivot
	if pivot == nil || pivot.Discovery != nil {
		t.Fatalf("resolved Pivot retained its discovery declaration: %#v", pivot)
	}
	if len(pivot.Categories) != 2 {
		t.Fatalf("resolved categories = %#v, want two observed categories", pivot.Categories)
	}
	if pivot.Categories[0].Key.String == nil || *pivot.Categories[0].Key.String != "final" ||
		pivot.Categories[1].Key.String == nil || *pivot.Categories[1].Key.String != "preliminary" {
		t.Fatalf("resolved category keys = %#v", pivot.Categories)
	}
	if len(proposal.CandidateConstruction.Steps[0].Outputs) != 3 {
		t.Fatalf("resolved Pivot outputs = %#v, want group key plus two categories", proposal.CandidateConstruction.Steps[0].Outputs)
	}
}

func TestProposeConstructionRejectsUnverifiablePivotDiscovery(t *testing.T) {
	service, store, snapshot, categoryColumnID, valueColumnID := constructionProposalPivotFixture(t)
	service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		var category explorer.ReceiptConstructionStageColumn
		for _, stage := range receipt.ConstructionStages["patients"] {
			if stage.ID != scan.StageID {
				continue
			}
			for _, column := range stage.Columns {
				if column.ID == scan.ColumnID {
					category = column
				}
			}
		}
		proof := compilerCategoryScanProof(construction.ConstructionCategoryDiscoveryRequest{
			OutputID: scan.Output, StageID: scan.StageID,
			CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID,
		}, category)
		proof.OverflowWitnessFingerprint = "witness"
		return dataframeexecution.CategoryScanResult{
			Overflow: true, ConclusiveMissing: true, Proof: proof,
		}, nil
	}
	request := constructionProposalPivotRequest(store.created, snapshot, nil)
	request.CandidateConstruction.Steps[0].Operation.Pivot.Discovery = &authoringv2.ConstructionPivotDiscovery{MaxCategories: 4}
	request.CandidateConstruction.Steps[0].Outputs = []authoringv2.StageColumn{
		{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
	}

	proposal, err := service.ProposeConstruction(context.Background(), request)
	if got := lifecycleErrorCode(err); got != "PIVOT_CATEGORIES_UNVERIFIABLE" {
		t.Fatalf("unverifiable discovery error code = %q, want PIVOT_CATEGORIES_UNVERIFIABLE; error = %v", got, err)
	}
	if proposal.ProposalID != "" || store.saveDraftCalls != 0 {
		t.Fatalf("rejected discovery proposal returned %#v or saved a draft", proposal)
	}
	if categoryColumnID == valueColumnID {
		t.Fatal("fixture category and value columns must differ")
	}
}

func TestLoadConstructionBaseRejectsSavedUnresolvedPivotDiscovery(t *testing.T) {
	service, store, snapshot, _, _ := constructionProposalPivotFixture(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	pivot := workspace.Documents[0].Construction.Steps[0].Operation.Pivot
	pivot.Discovery = &authoringv2.ConstructionPivotDiscovery{MaxCategories: 4}
	pivot.Categories = nil
	workspace.Documents[0].Construction.Steps[0].Outputs = []authoringv2.StageColumn{
		{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
	}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate saved discovery workspace: %v", err)
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

	_, err = service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if got := lifecycleErrorCode(err); got != "PIVOT_DISCOVERY_UNRESOLVED" {
		t.Fatalf("saved discovery load error code = %q, want PIVOT_DISCOVERY_UNRESOLVED; error = %v", got, err)
	}
}
