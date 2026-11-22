package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func migrateStoredWorkspaceFixture(t *testing.T, raw []byte, catalog authoringv2.CatalogSnapshot) authoringv2.StoredWorkspaceMigration {
	t.Helper()
	if _, err := authoringv2.DecodeWorkspace(raw); err == nil {
		t.Fatal("legacy fixture unexpectedly accepted by strict runtime decoder")
	}
	migrated, err := authoringv2.MigrateStoredWorkspace(raw, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authoringv2.DecodeWorkspace(migrated.CanonicalJSON); err != nil {
		t.Fatalf("offline migration did not produce strict current workspace: %v", err)
	}
	return migrated
}

func TestBuilderRequiresExplicitStoredMigrationForLegacyNilSteps(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	service.config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workspace.Documents[0].Columns {
		if workspace.Documents[0].Columns[index].ColumnID == "" {
			workspace.Documents[0].Columns[index].ColumnID = workspace.Documents[0].Columns[index].Column
		}
	}
	workspace.Documents[0].Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{}}
	current, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(current, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	if bytes.Equal(current, legacy) {
		t.Fatal("fixture did not install legacy nil steps")
	}
	migration := migrateStoredWorkspaceFixture(t, legacy, service.catalog(snapshot, store.created.ExplorerID))
	if migration.OriginalNilConstructionStepLists != 1 || migration.OriginalLegacyNilStepsDigest == "" {
		t.Fatalf("missing exact nil-step migration proof: %#v", migration)
	}
	store.created.DraftConfig, store.created.DraftDigest = legacy, migration.OriginalLegacyNilStepsDigest
	beforeVersion := store.created.DraftVersion
	receiptBefore, err := json.Marshal(store.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Builder(context.Background(), BuilderRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID}); lifecycleErrorCode(err) != "DRAFT_STATE_INVALID" {
		t.Fatalf("legacy Builder read=%v, want DRAFT_STATE_INVALID without lazy write", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || !bytes.Equal(store.created.DraftConfig, legacy) {
		t.Fatal("Builder lazily rewrote a legacy draft")
	}

	store.created.DraftConfig, store.created.DraftDigest = migration.CanonicalJSON, migration.MigratedCanonicalDigest
	for i := 0; i < 2; i++ {
		state, err := service.Builder(context.Background(), BuilderRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID})
		if err != nil {
			t.Fatal(err)
		}
		if state.Workspace == nil || state.Workspace.Documents[0].Construction.Steps == nil {
			t.Fatal("migrated Builder returned null construction steps")
		}
	}
	receiptAfter, err := json.Marshal(store.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || !bytes.Equal(receiptBefore, receiptAfter) {
		t.Fatal("repeat migrated Builder read wrote owner or changed immutable receipt")
	}
}

func TestLegacyReceiptRemainsReadableButCannotPreviewOrPublish(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	historical, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	historical.SemanticsVersion = authoringv2.CurrentSemanticsVersion - 1
	receipt.NormalizedBundle, err = historical.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	receipt.IntentDigest, err = historical.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = explorer.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatalf("historical receipt original identity is invalid: %v", err)
	}
	original, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	previewCalls, materializeCalls, releaseCalls, sinkCalls := 0, 0, 0, 0
	config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		return dataframeexecution.PreviewSummary{}, nil
	}
	config.MaterializeReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		materializeCalls++
		return ports.Execution{}, nil
	}
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		releaseCalls++
		return dataset.ProjectRelease{}, 0, nil
	}
	service := newTestService(t, store, config)
	_, err = service.Preview(context.Background(), PreviewRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, OutputID: "patients", Limit: 10, SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
		sinkCalls++
		return func(map[string]any) error { return nil }, nil
	}})
	if lifecycleErrorCode(err) != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("legacy Preview error=%v, want RECEIPT_RECOMPILE_REQUIRED", err)
	}
	_, err = service.Publish(context.Background(), PublishRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, Actor: "alice"})
	if lifecycleErrorCode(err) != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("legacy Publish error=%v, want RECEIPT_RECOMPILE_REQUIRED", err)
	}
	if previewCalls != 0 || materializeCalls != 0 || releaseCalls != 0 || sinkCalls != 0 || store.published {
		t.Fatalf("legacy receipt reached execution/activation: preview=%d materialize=%d release=%d sink=%d published=%v", previewCalls, materializeCalls, releaseCalls, sinkCalls, store.published)
	}

	raw, err := service.store.CompilationReceiptForExplorer(context.Background(), receipt.Project, receipt.ExplorerID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.validateReceiptRoute(raw, receipt.Project, receipt.ExplorerID); err != nil {
		t.Fatalf("historical raw artifact admission failed: %v", err)
	}
	if err := raw.ValidateID(); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("execution rejection or raw lookup mutated historical receipt")
	}
}
