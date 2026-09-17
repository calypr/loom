package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

// Keep the vertical contract test close to the lifecycle boundary. The
// receipt and membership are intentionally assembled from public domain
// values; the mapping callback stands in for the configured execution engine.
func TestPopulationMappingReportLiteralResult(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := populationTestReceipt(snapshot)
	selection := completedTestSelection(snapshot, "Specimen")
	selection.MemberCount = 3
	selection.MembershipDigest = "membership-digest"
	store := &fakeStore{receipt: receipt, selection: selection, selectionMembers: []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-001"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-002"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-004"}},
	}}
	config := testConfig(snapshot)
	config.PopulationMapping = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, output string, ids []string, _ string, _ int) (dataframeexecution.PopulationMappingResult, error) {
		if output != "patients" || len(ids) != 3 {
			t.Fatalf("mapping callback arguments = %q/%v", output, ids)
		}
		return dataframeexecution.PopulationMappingResult{Status: dataframeexecution.PopulationMappingComplete, SelectedCount: 3, MappedCount: 2, UnmappedCount: 1, EmittedRows: 1, UnmappedMemberIDs: []string{"file-004"}}, nil
	}
	service := newTestService(t, store, config)
	result, err := service.PopulationMapping(context.Background(), PopulationMappingRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.Status != dataframeexecution.PopulationMappingComplete || result.Report.Counts == nil {
		t.Fatalf("report = %#v", result.Report)
	}
	counts := *result.Report.Counts
	if counts.Selected != 3 || counts.Mapped != 2 || counts.Unmapped != 1 || counts.EmittedRows != 1 {
		t.Fatalf("counts = %#v", counts)
	}
	if len(result.Report.Unmapped) != 1 || result.Report.Unmapped[0].ID != "file-004" {
		t.Fatalf("unmapped = %#v", result.Report.Unmapped)
	}
	if result.Report.Binding.ReceiptID != receipt.ID || result.Report.Binding.SelectionRevisionID != selection.ID || result.Report.Binding.MembershipDigest != selection.MembershipDigest {
		t.Fatalf("binding = %#v", result.Report.Binding)
	}
}

func TestPopulationMappingRejectsCrossOutputCursorBeforeReadingMembers(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := populationTestReceipt(snapshot)
	selection := completedTestSelection(snapshot, "Specimen")
	selection.MemberCount = 3
	store := &fakeStore{receipt: receipt, selection: selection, selectionMembers: []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-001"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-002"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-004"}},
	}}
	config := testConfig(snapshot)
	config.PopulationMapping = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, string, []string, string, int) (dataframeexecution.PopulationMappingResult, error) {
		t.Fatal("mapping callback should not run for a stale cursor")
		return dataframeexecution.PopulationMappingResult{}, nil
	}
	service := newTestService(t, store, config)
	_, err := service.PopulationMapping(context.Background(), PopulationMappingRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "other", Cursor: "bad", Limit: 10})
	if err == nil || store.memberVisits != 0 {
		t.Fatalf("cross-output cursor err=%v, member visits=%d", err, store.memberVisits)
	}
}

func TestPopulationMappingIncompleteHasNoCounts(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := populationTestReceipt(snapshot)
	selection := completedTestSelection(snapshot, "Specimen")
	selection.MemberCount = 3
	store := &fakeStore{receipt: receipt, selection: selection, selectionMembers: []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-001"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-002"}},
		{Ref: explorer.ResourceRef{Project: "project-a", Generation: "generation-a", ResourceType: "Specimen", ID: "file-004"}},
	}}
	config := testConfig(snapshot)
	config.PopulationMapping = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, string, []string, string, int) (dataframeexecution.PopulationMappingResult, error) {
		return dataframeexecution.PopulationMappingResult{Status: dataframeexecution.PopulationMappingIncomplete}, nil
	}
	service := newTestService(t, store, config)
	result, err := service.PopulationMapping(context.Background(), PopulationMappingRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.Status != dataframeexecution.PopulationMappingIncomplete || result.Report.Counts != nil {
		t.Fatalf("incomplete report = %#v", result.Report)
	}
}

func unrestrictedScope() authscope.ReadScope {
	return authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
}

func populationTestReceipt(snapshot capability.Snapshot) *explorer.CompilationReceipt {
	receipt := nativeReceipt(snapshot)
	receipt.Bundle.Outputs[0].Population = &recipe.PopulationConstraint{SelectionRevisionID: "selection-1", MembershipDigest: "membership-digest", MemberCount: 3, ResourceType: "Specimen"}
	digest, _ := receipt.Bundle.Digest()
	receipt.RecipeDigest, receipt.ResolvedRecipeDigest = digest, digest
	receipt.CompilationKey, _ = explorer.CompilationKey(*receipt)
	receipt.ID, _ = explorer.ReceiptID(*receipt)
	return receipt
}
