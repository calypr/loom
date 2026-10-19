package lifecycle

import (
	"context"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestApplyConstructionProposalFromPersistedV9UsesRawOwnerCAS(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = 9
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = draft, digest
	originalConfig, version := append([]byte(nil), draft...), store.created.DraftVersion

	compileReceipt := service.config.CompileReceipt
	catalog := service.catalog(snapshot, store.created.ExplorerID)
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		prepared, prepareErr := authoringv2.PrepareWorkspaceForCompilation(request.Workspace, catalog)
		if prepareErr != nil {
			return nil, prepareErr
		}
		request.Workspace = prepared
		return compileReceipt(ctx, request)
	}
	proposal, err := service.ProposeConstruction(context.Background(), constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step"))
	if err != nil {
		t.Fatalf("propose construction from saved v9 workspace: %v", err)
	}
	if proposal.ProposalID == "" || proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("proposal = %#v", proposal)
	}
	if store.created.DraftVersion != version || store.created.DraftDigest != digest || string(store.created.DraftConfig) != string(originalConfig) {
		t.Fatal("proposal read changed persisted v9 owner draft")
	}

	previewCalls := 0
	service.config.PreviewReceipt = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	apply := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-v9-construction", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", apply, "alice")
	if err != nil {
		t.Fatalf("apply construction with saved v9 CAS: %v", err)
	}
	if previewCalls != 1 || response.Workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion {
		t.Fatalf("preview calls=%d, applied semanticsVersion=%d", previewCalls, response.Workspace.SemanticsVersion)
	}
	if store.created.DraftVersion != version+1 || store.created.DraftDigest == digest || string(store.created.DraftConfig) == string(originalConfig) {
		t.Fatal("accepted proposal did not persist the upgraded candidate exactly once")
	}
	persisted, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	persistedDigest, err := persisted.Digest()
	if err != nil || persisted.SemanticsVersion != authoringv2.CurrentSemanticsVersion || persistedDigest != store.created.DraftDigest {
		t.Fatalf("persisted candidate semantics=%d digest=%q ownerDigest=%q err=%v", persisted.SemanticsVersion, persistedDigest, store.created.DraftDigest, err)
	}

	apply.CommandID = "apply-v9-construction-stale-cas"
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", apply, "alice")
	if lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale v9 owner CAS error = %v, want DRAFT_CONFLICT", err)
	}
	if store.created.DraftVersion != version+1 || store.created.DraftDigest != persistedDigest {
		t.Fatal("stale v9 CAS changed the accepted draft")
	}
}
