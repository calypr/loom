package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestCompareInterpretationCandidateRowsCountsTransitionsAndPreservesPublicValues(t *testing.T) {
	base := interpretationPreviewRows{ReceiptID: "base", Rows: map[string]map[string]any{
		"row-1": {"feature": "old"},
		"row-2": {"feature": nil},
		"row-3": {"feature": "known"},
		"row-4": {"feature": "same"},
	}}
	candidate := interpretationPreviewRows{ReceiptID: "candidate", Rows: map[string]map[string]any{
		"row-1": {"feature": "new"},
		"row-2": {"feature": "resolved"},
		"row-3": {"feature": nil},
		"row-4": {"feature": "same"},
	}}
	emissions := []explorer.EmittedColumn{{EmissionID: "em-feature", OutputID: "patients", PublicColumn: "feature", AuthoredColumns: []string{"meaning"}}}
	result := compareInterpretationCandidateRows(base, candidate, emissions, emissions, PreviewInterpretationCandidateRequest{OutputID: "patients", Column: "meaning", RevisionID: "revision-b"}, 10)
	if result.BaseReceiptID != "base" || result.CandidateReceiptID != "candidate" || result.Completeness != CandidatePreviewComplete {
		t.Fatalf("result identity/completeness = %#v", result)
	}
	if result.Counts != (CandidatePreviewCounts{Compared: 4, Changed: 3, Resolved: 1, Unresolved: 1}) {
		t.Fatalf("counts = %#v", result.Counts)
	}
	states := map[string]CandidatePreviewState{}
	for _, sample := range result.Samples {
		states[sample.RowID] = sample.State
	}
	if states["row-1"] != CandidatePreviewChanged || states["row-2"] != CandidatePreviewResolved || states["row-3"] != CandidatePreviewUnresolved || states["row-4"] != CandidatePreviewUnchanged {
		t.Fatalf("states = %#v", states)
	}
	if result.Samples[0].Before["feature"] != "old" || result.Samples[0].After["feature"] != "new" {
		t.Fatalf("public values were not retained: %#v", result.Samples[0])
	}
}

func TestPreviewSummaryCompletenessRequiresNaturalExhaustion(t *testing.T) {
	if !previewSummaryComplete(dataframeexecution.PreviewSummary{Complete: true}) {
		t.Fatal("complete summary was rejected")
	}
	if previewSummaryComplete(dataframeexecution.PreviewSummary{Complete: false, Truncated: true}) {
		t.Fatal("truncated summary was treated as complete")
	}
}

func TestPreviewInterpretationCandidateUsesBothReceiptsWithoutMutatingDraft(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}}
	snapshot.Candidates = []capability.Candidate{{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}
	revision := lifecyclePrepareInterpretation(t)
	workspace := lifecycleCandidatePreviewWorkspace()
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}}
	repository := &countingInterpretationRepository{revision: revision}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return lifecycleInterpretationCatalog(snapshot, explorerID)
	}
	config.InterpretationRepository = repository
	var compiled, previewed int
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compiled++
		var resolved *explorer.InterpretationRevision
		if len(request.ResolvedInputs.Interpretations) != 0 {
			resolved = &revision
		}
		receipt := lifecycleCandidatePreviewReceipt(t, snapshot, request.Workspace, resolved)
		store.receipt = receipt
		return receipt, nil
	}
	config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, sink func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewed++
		if !bindings.IncludeRowIdentity {
			t.Fatal("candidate preview execution did not request stable row identity")
		}
		value := "before"
		if len(receipt.ResolvedInterpretations) != 0 {
			value = "after"
		}
		if err := sink(map[string]any{"__loom_row_id": "row-1", "patient_id": value}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Complete: false, Truncated: true, RowCount: 1}, nil
	}
	service := newTestService(t, store, config)
	beforeDraft := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	result, err := service.PreviewInterpretationCandidate(context.Background(), PreviewInterpretationCandidateRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest,
		OutputID: "patients", Column: "patient_id", RevisionID: string(revision.ID), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled != 2 || previewed != 2 {
		t.Fatalf("compiled=%d previewed=%d, want two immutable receipts executed", compiled, previewed)
	}
	if result.BaseReceiptID == "" || result.CandidateReceiptID == "" || result.BaseReceiptID == result.CandidateReceiptID {
		t.Fatalf("receipt identities = %#v", result)
	}
	if result.Completeness != CandidatePreviewIncomplete || result.Counts.Compared != 1 || result.Counts.Changed != 1 {
		t.Fatalf("preview result = %#v, want bounded incomplete changed result", result)
	}
	if len(result.Samples) != 1 || result.Samples[0].Before["patient_id"] != "before" || result.Samples[0].After["patient_id"] != "after" {
		t.Fatalf("preview sample = %#v", result.Samples)
	}
	if store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || string(store.created.DraftConfig) != string(beforeDraft) {
		t.Fatalf("preview mutated owner draft: version=%d digest=%q config=%s", store.created.DraftVersion, store.created.DraftDigest, store.created.DraftConfig)
	}
}

func TestApplyInterpretationCandidateRequiresExactReceiptAndCommandResult(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}}
	snapshot.Candidates = []capability.Candidate{{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}
	revision := lifecyclePrepareInterpretation(t)
	workspace := lifecycleCandidatePreviewWorkspace()
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return lifecycleInterpretationCatalog(snapshot, explorerID)
	}
	catalog := config.Capability.Catalog(snapshot, "patients")
	command := authoringv2.Command{Type: authoringv2.CommandApplyInterpretationCandidate, OutputID: "patients", Column: "patient_id", InterpretationCandidate: &authoringv2.ApplyInterpretationCandidate{CandidateReceiptID: "placeholder", RevisionID: string(revision.ID)}}
	proposed, _, err := authoringv2.ApplyCommands(workspace, catalog, "preview-command", []authoringv2.Command{command})
	if err != nil {
		t.Fatal(err)
	}
	receipt := lifecycleCandidatePreviewReceipt(t, snapshot, proposed, &revision)
	command.InterpretationCandidate.CandidateReceiptID = receipt.ID

	tests := []struct {
		name   string
		mutate func(*authoringv2.ApplyCommandsRequest)
		wantOK bool
	}{
		{name: "exact candidate", wantOK: true},
		{name: "wrong receipt", mutate: func(request *authoringv2.ApplyCommandsRequest) {
			request.CommandID = "wrong-receipt"
			request.Commands[0].InterpretationCandidate.CandidateReceiptID = "receipt_wrong"
		}},
		{name: "wrong revision", mutate: func(request *authoringv2.ApplyCommandsRequest) {
			request.CommandID = "wrong-revision"
			request.Commands[0].InterpretationCandidate.RevisionID = "revision_other"
		}},
		{name: "wrong output", mutate: func(request *authoringv2.ApplyCommandsRequest) {
			request.CommandID = "wrong-output"
			request.Commands[0].OutputID = "other"
		}},
		{name: "wrong column", mutate: func(request *authoringv2.ApplyCommandsRequest) {
			request.CommandID = "wrong-column"
			request.Commands[0].Column = "other"
		}},
		{name: "stale draft", mutate: func(request *authoringv2.ApplyCommandsRequest) {
			request.CommandID = "stale-draft"
			request.ExpectedDraftVersion++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner := &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: append([]byte(nil), draft...), DraftVersion: 7, DraftDigest: digest}
			store := &fakeStore{created: owner, receipt: receipt}
			config := testConfig(snapshot)
			config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
				return lifecycleInterpretationCatalog(snapshot, explorerID)
			}
			config.InterpretationRepository = &countingInterpretationRepository{revision: revision}
			service := newTestService(t, store, config)
			request := authoringv2.ApplyCommandsRequest{
				CommandID: "apply-candidate", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
				ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest, Commands: []authoringv2.Command{command},
			}
			before := append([]byte(nil), owner.DraftConfig...)
			beforeVersion, beforeDigest := owner.DraftVersion, owner.DraftDigest
			if test.mutate != nil {
				test.mutate(&request)
			}
			response, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
			if test.wantOK {
				if applyErr != nil || response == nil || store.created.DraftVersion != beforeVersion+1 || store.created.DraftDigest != receipt.IntentDigest {
					t.Fatalf("exact apply response=%#v err=%v owner=%#v", response, applyErr, store.created)
				}
				return
			}
			if applyErr == nil || owner.DraftVersion != beforeVersion || owner.DraftDigest != beforeDigest || string(owner.DraftConfig) != string(before) {
				t.Fatalf("rejected apply err=%v owner=%#v", applyErr, owner)
			}
		})
	}
}

func lifecycleCandidatePreviewWorkspace() authoringv2.Workspace {
	workspace := lifecycleInterpretationWorkspace("", true)
	workspace.Documents = workspace.Documents[:1]
	workspace.Documents[0].Output.ID = "patients"
	workspace.Documents[0].Output.Title = "Patients"
	workspace.Tabs = []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}
	return workspace
}

func lifecycleInterpretationCatalog(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	return authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: snapshot.Identity.Project,
		ExplorerID: explorerID, SourceGeneration: snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token,
		Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes:      []authoringv2.CatalogNode{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}},
		Candidates: []authoringv2.CatalogCandidate{{ID: "candidate-id", NodeID: "node-patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE"}},
	}
}

func lifecycleCandidatePreviewReceipt(t *testing.T, snapshot capability.Snapshot, workspace authoringv2.Workspace, revision *explorer.InterpretationRevision) *explorer.CompilationReceipt {
	t.Helper()
	receipt := nativeReceipt(snapshot)
	receipt.ReceiptFormatVersion = 0
	receipt.CompilerContractVersion = ""
	receipt.IntentDigest, _ = workspace.Digest()
	receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
	receipt.EmittedColumns[0].AuthoredColumns = []string{"patient_id"}
	if revision != nil {
		rule := revision.Rules[0]
		receipt.ResolvedInterpretations = []explorer.ResolvedInterpretation{{OutputID: "patients", Column: "patient_id", OccurrenceID: authoringv2.RootOccurrenceID, Revision: *revision, SelectedRuleID: rule.ID, Definition: rule.Definition}}
	}
	receipt.ID = ""
	id, err := explorer.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID = id
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	return receipt
}
