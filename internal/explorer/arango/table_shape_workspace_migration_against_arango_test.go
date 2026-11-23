package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	workspacepkg "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

func TestStoredTableShapeMigrationAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	legacyRaw := wp17Fixture(t, "pivot-derived-chain.input.json")
	legacyMigration, err := ws.MigrateStoredTableShapeWorkspace(legacyRaw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	mixedRaw := wp17MixedWorkspace(t, legacyRaw)
	mixedMigration, err := ws.MigrateStoredTableShapeWorkspace(mixedRaw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	currentRaw, currentDigest := draftCASPayload(t, "Already current")

	legacyOwner := workspaceMigrationSeed(t, ctx, adapter, "wp17-legacy", "Legacy shape", legacyRaw, legacyMigration.OriginalCanonicalDigest, "snapshot-wp17")
	mixedOwner := workspaceMigrationSeed(t, ctx, adapter, "wp17-mixed", "Mixed shape", mixedRaw, mixedMigration.OriginalCanonicalDigest, "snapshot-wp17")
	currentOwner := workspaceMigrationSeed(t, ctx, adapter, "wp17-current", "Already current", currentRaw, currentDigest, "snapshot-wp17")

	publishedOwner := explorer.Explorer{Project: "project-wp17-published", ExplorerID: "patients"}
	receipt := wp17LegacyReceipt(t, publishedOwner, legacyRaw, legacyMigration.OriginalCanonicalDigest)
	if _, err := adapter.InsertCompilationReceipt(ctx, receipt); err != nil {
		t.Fatalf("insert old immutable receipt: %v", err)
	}
	published := explorer.Revision{ID: "wp17-published-revision", Project: publishedOwner.Project, ExplorerID: publishedOwner.ExplorerID,
		AuthoringBundle: append(json.RawMessage(nil), legacyRaw...), IntentDigest: legacyMigration.OriginalCanonicalDigest,
		CompilationReceiptID: receipt.ID, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest,
		ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration,
		Status: explorer.RevisionReady, CreatedAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)}
	storedRevision, err := adapter.InsertRevision(ctx, published)
	if err != nil {
		t.Fatalf("insert immutable revision: %v", err)
	}
	if storedRevision.ID != published.ID {
		t.Fatalf("stored revision id=%q want=%q", storedRevision.ID, published.ID)
	}

	priorHistoryOwner := explorer.Explorer{Project: "project-wp17-history", ExplorerID: "patients", Title: "Prior history",
		DraftVersion: 4, DraftDigest: legacyMigration.OriginalCanonicalDigest, DraftConfig: append(json.RawMessage(nil), legacyRaw...),
		DraftSnapshotToken: "snapshot-wp17", DraftSourceGeneration: "generation-wp17", DraftAuthorizationScopeDigest: "scope-wp17",
		UpdatedBy: "wp17-test", UpdatedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
	priorHistory := draftRevisionFromExplorer(priorHistoryOwner)
	priorHistory.ID = draftRevisionKey(priorHistory.Project, priorHistory.ExplorerID, priorHistory.DraftVersion)
	priorHistoryDoc, err := db.Document(priorHistory, priorHistory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": DraftRevisionsCollection, "doc": priorHistoryDoc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("insert prior immutable history: %v", err)
	}

	wp18Report, err := MigrateStoredWorkspaces(ctx, client, false)
	if err != nil {
		t.Fatalf("WP-18 prerequisite migration: %v", err)
	}
	if wp18Report.Owners != 0 || wp18Report.Projections != 0 {
		t.Fatalf("WP-18 should leave TableShape-only packets unchanged: %#v", wp18Report)
	}

	baseRevisionProjection := wp17InsertBaseProjection(t, ctx, client, explorer.RevisionWorkspaceArtifact(*storedRevision), legacyRaw)
	baseHistoryProjection := wp17InsertBaseProjection(t, ctx, client, explorer.DraftWorkspaceArtifact(priorHistory), legacyRaw)
	beforeBaseRevisionProjection := wp17ReadDocument(t, ctx, client, workspaceProjectionCollection, explorer.RevisionWorkspaceArtifact(*storedRevision).Key())
	beforeBaseHistoryProjection := wp17ReadDocument(t, ctx, client, workspaceProjectionCollection, explorer.DraftWorkspaceArtifact(priorHistory).Key())
	beforeRevision := wp17ReadDocument(t, ctx, client, RevisionsCollection, published.ID)
	beforeHistory := wp17ReadDocument(t, ctx, client, DraftRevisionsCollection, priorHistory.ID)
	beforeReceipt := wp17ReadDocument(t, ctx, client, CompilationReceiptsCollection, receipt.ID)

	beforeDryRun := wp17Inventory(t, ctx, client)
	dryRun, err := MigrateStoredTableShapes(ctx, client, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || !dryRun.WouldRecordVersion || dryRun.Owners != 2 || dryRun.HistoryRows != 2 || dryRun.Projections != 4 || dryRun.RetiredReceipts != 1 {
		t.Fatalf("WP-17 dry-run report = %#v", dryRun)
	}
	if after := wp17Inventory(t, ctx, client); !reflect.DeepEqual(beforeDryRun, after) {
		t.Fatal("WP-17 dry run changed full database inventory")
	}

	report, err := MigrateStoredTableShapes(ctx, client, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.DryRun || report.Owners != 2 || report.HistoryRows != 2 || report.Projections != 4 || report.RetiredReceipts != 1 {
		t.Fatalf("WP-17 apply report = %#v", report)
	}

	for _, before := range []explorer.Explorer{legacyOwner, mixedOwner} {
		after, err := adapter.Get(ctx, before.Project, before.ExplorerID)
		if err != nil {
			t.Fatal(err)
		}
		want := legacyMigration.MigratedCanonicalDigest
		if before.ExplorerID == mixedOwner.ExplorerID {
			want = mixedMigration.MigratedCanonicalDigest
		}
		if after.DraftVersion != before.DraftVersion+1 || after.DraftDigest != want {
			t.Fatalf("owner %s version/digest=%d/%q want=%d/%q", before.ExplorerID, after.DraftVersion, after.DraftDigest, before.DraftVersion+1, want)
		}
		if wp17HasTableShape(t, after.DraftConfig) {
			t.Fatalf("mutable owner %s still contains TableShape", before.ExplorerID)
		}
		if _, err := ws.DecodeCurrentWorkspace(after.DraftConfig); err != nil {
			t.Fatalf("migrated owner %s fails current decoder: %v", before.ExplorerID, err)
		}
		var wantWorkspace []byte
		if before.ExplorerID == legacyOwner.ExplorerID {
			wantWorkspace = legacyMigration.CanonicalJSON
		} else {
			wantWorkspace = mixedMigration.CanonicalJSON
		}
		wp17AssertOwnerHistoryProjection(t, ctx, client, adapter, before, wantWorkspace)
	}
	unchangedCurrent, err := adapter.Get(ctx, currentOwner.Project, currentOwner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if unchangedCurrent.DraftVersion != currentOwner.DraftVersion || unchangedCurrent.DraftDigest != currentOwner.DraftDigest {
		t.Fatalf("current owner changed: before=%#v after=%#v", currentOwner, unchangedCurrent)
	}

	if got := wp17ReadDocument(t, ctx, client, RevisionsCollection, published.ID); !bytes.Equal(got, beforeRevision) {
		t.Fatalf("migration rewrote immutable revision (includes _rev): before=%s after=%s", beforeRevision, got)
	}
	if got := wp17ReadDocument(t, ctx, client, DraftRevisionsCollection, priorHistory.ID); !bytes.Equal(got, beforeHistory) {
		t.Fatalf("migration rewrote prior history (includes _rev): before=%s after=%s", beforeHistory, got)
	}
	if got := wp17ReadDocument(t, ctx, client, CompilationReceiptsCollection, receipt.ID); !bytes.Equal(got, beforeReceipt) {
		t.Fatalf("migration rewrote receipt raw identity: before=%s after=%s", beforeReceipt, got)
	}
	if got := wp17ReadDocument(t, ctx, client, workspaceProjectionCollection, explorer.RevisionWorkspaceArtifact(*storedRevision).Key()); !bytes.Equal(got, beforeBaseRevisionProjection) {
		t.Fatalf("WP-17 rewrote WP-18 revision base projection (_rev included): before=%s after=%s", beforeBaseRevisionProjection, got)
	}
	if got := wp17ReadDocument(t, ctx, client, workspaceProjectionCollection, explorer.DraftWorkspaceArtifact(priorHistory).Key()); !bytes.Equal(got, beforeBaseHistoryProjection) {
		t.Fatalf("WP-17 rewrote WP-18 history base projection (_rev included): before=%s after=%s", beforeBaseHistoryProjection, got)
	}

	revisionSource := explorer.RevisionWorkspaceArtifact(*storedRevision)
	historySource := explorer.DraftWorkspaceArtifact(priorHistory)
	wp17AssertConstructionProjection(t, ctx, client, adapter, revisionSource, legacyMigration.CanonicalJSON, baseRevisionProjection)
	wp17AssertConstructionProjection(t, ctx, client, adapter, historySource, legacyMigration.CanonicalJSON, baseHistoryProjection)

	migratedReceipt, err := adapter.GetCompilationReceiptForExplorer(ctx, published.Project, published.ExplorerID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	receiptID, err := receiptmodel.ReceiptID(*migratedReceipt)
	if err != nil || receiptID != receipt.ID || migratedReceipt.ID != receipt.ID {
		t.Fatalf("old receipt content identity changed: id=%q recomputed=%q err=%v", migratedReceipt.ID, receiptID, err)
	}
	if err := migratedReceipt.Validate(); err != nil {
		t.Fatalf("old receipt is no longer internally verifiable: %v", err)
	}
	if migratedReceipt.IntentDigest == legacyMigration.MigratedCanonicalDigest {
		t.Fatal("old receipt remains bound to the converted Construction workspace")
	}
	recipeBytes, err := json.Marshal(migratedReceipt.Bundle)
	if err != nil || !bytes.Contains(recipeBytes, []byte(`"tableReshape"`)) {
		t.Fatalf("immutable old recipe TableReshape was lost: %s err=%v", recipeBytes, err)
	}

	afterApply := wp17Inventory(t, ctx, client)
	rerun, err := MigrateStoredTableShapes(ctx, client, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rerun.AlreadyApplied || rerun.Owners != 0 || rerun.HistoryRows != 0 || rerun.Projections != 0 || rerun.RepositoryConfigs != 0 {
		t.Fatalf("WP-17 rerun = %#v", rerun)
	}
	if after := wp17Inventory(t, ctx, client); !reflect.DeepEqual(afterApply, after) {
		t.Fatal("WP-17 rerun changed database inventory")
	}
}

func wp17Fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../authoringv2/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func wp17MixedWorkspace(t *testing.T, legacy []byte) []byte {
	t.Helper()
	var packet map[string]json.RawMessage
	if err := json.Unmarshal(legacy, &packet); err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(packet["documents"], &documents); err != nil {
		t.Fatal(err)
	}
	documents = append(documents, json.RawMessage(`{"kind":"ExplorerBuilderDocument","output":{"id":"other","title":"Other"},"rootResourceType":"Observation","route":{"occurrenceId":"base","resourceType":"Observation"},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"project_id","label":"Project","occurrenceId":"base","source":{"kind":"projectId"}}]}`))
	packet["documents"], _ = json.Marshal(documents)
	var tabs []json.RawMessage
	if err := json.Unmarshal(packet["tabs"], &tabs); err != nil {
		t.Fatal(err)
	}
	tabs = append(tabs, json.RawMessage(`{"id":"other","title":"Other","outputId":"other","order":1,"visible":true}`))
	packet["tabs"], _ = json.Marshal(tabs)
	mixed, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	return mixed
}

func wp17LegacyReceipt(t *testing.T, owner explorer.Explorer, workspace []byte, digest string) receiptmodel.CompilationReceipt {
	t.Helper()
	receipt := workspaceMigrationReceipt(t, owner, workspace, digest, "wp17-old-table-reshape")
	const oldRecipe = `{"recipeSchemaVersion":1,"name":"wp17-old-reshape","translationVersion":"legacy-test","outputs":[{"name":"out","rootResourceType":"Patient","rowGrain":"patient","tableReshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"legacy_unpivot","inputs":[{"column":"measurement","key":{"kind":"STRING","string":"measurement"}}],"keyOutput":"kind","keyLabel":"Kind","valueOutput":"value","valueLabel":"Value","nullRowPolicy":"PRESERVE"}}}]}`
	if err := json.Unmarshal([]byte(oldRecipe), &receipt.Bundle); err != nil {
		t.Fatalf("decode old recipe fixture: %v", err)
	}
	if err := receipt.Bundle.ValidateArchive(); err != nil {
		t.Fatalf("legacy recipe TableReshape fixture invalid: %v", err)
	}
	var digestErr error
	receipt.ResolvedRecipeDigest, digestErr = receipt.Bundle.ArchiveDigest()
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	if err := receipt.Bundle.Validate(); err == nil {
		t.Fatal("retired recipe admitted by current validator")
	}
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(receipt)
	var err error
	receipt.ID, err = receiptmodel.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("old recipe receipt fixture invalid: %v", err)
	}
	return receipt
}

func wp17InsertBaseProjection(t *testing.T, ctx context.Context, client *store.Client, source workspacepkg.Artifact, raw []byte) workspacepkg.Projection {
	t.Helper()
	base, err := ws.MigrateStoredWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	projection := newWorkspaceProjection(source, base)
	if err := insertWorkspaceProjection(ctx, client, projection); err != nil {
		t.Fatal(err)
	}
	return projection
}

func wp17AssertOwnerHistoryProjection(t *testing.T, ctx context.Context, client *store.Client, adapter *Store, before explorer.Explorer, expected []byte) {
	t.Helper()
	history := draftCASHistory(t, ctx, client, before)
	if len(history) != 1 || !wp17JSONEqual(t, history[0].DraftConfig, before.DraftConfig) || history[0].DraftDigest != before.DraftDigest {
		t.Fatalf("owner history did not preserve exact preimage: %#v", history)
	}
	source := explorer.DraftWorkspaceArtifact(history[0])
	key := tableShapeConstructionTarget.projectionKey(source)
	projection := wp17ReadProjection(t, ctx, client, key)
	if projection.Source != source || wp17HasTableShape(t, projection.Workspace) || !wp17JSONEqual(t, projection.Workspace, expected) {
		t.Fatalf("owner history Construction projection mismatch: %#v", projection)
	}
	selected, err := adapter.WorkspaceProjection(ctx, source)
	if err != nil || selected.ID != projection.ID {
		t.Fatalf("owner history projection lookup=%#v err=%v want=%q", selected, err, projection.ID)
	}
}

func wp17AssertConstructionProjection(t *testing.T, ctx context.Context, client *store.Client, adapter *Store, source workspacepkg.Artifact, expected []byte, base workspacepkg.Projection) {
	t.Helper()
	constructionKey := tableShapeConstructionTarget.projectionKey(source)
	construction := wp17ReadProjection(t, ctx, client, constructionKey)
	if construction.Source != source || construction.ID == base.ID || wp17HasTableShape(t, construction.Workspace) || !wp17JSONEqual(t, construction.Workspace, expected) {
		t.Fatalf("WP-17 projection mismatch: key=%q projection=%#v base=%#v", constructionKey, construction, base)
	}
	storedBase := wp17ReadProjection(t, ctx, client, source.Key())
	if storedBase.ID != base.ID || !wp17JSONEqual(t, storedBase.Workspace, base.Workspace) || !wp17HasTableShape(t, storedBase.Workspace) {
		t.Fatalf("WP-18 base projection was changed or not legacy: %#v", storedBase)
	}
	selected, err := adapter.WorkspaceProjection(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != construction.ID {
		t.Fatalf("projection provider chose %q, want WP-17 %q (WP-18=%q)", selected.ID, construction.ID, base.ID)
	}
	if err := tableShapeConstructionTarget.validateProjection(selected, source); err != nil {
		t.Fatalf("selected construction projection invalid: %v", err)
	}
}

func wp17ReadProjection(t *testing.T, ctx context.Context, client *store.Client, key string) workspacepkg.Projection {
	t.Helper()
	var result *workspacepkg.Projection
	if err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": workspaceProjectionCollection, "key": key}, func(row map[string]any) error {
		value, err := db.Decode[workspacepkg.Projection](row)
		result = &value
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatalf("projection key %q not found", key)
	}
	return *result
}

func wp17HasTableShape(t *testing.T, raw []byte) bool {
	t.Helper()
	var workspace struct {
		Documents []map[string]json.RawMessage `json:"documents"`
	}
	if err := json.Unmarshal(raw, &workspace); err != nil {
		t.Fatal(err)
	}
	for _, document := range workspace.Documents {
		shape, exists := document["tableShape"]
		if exists && !bytes.Equal(bytes.TrimSpace(shape), []byte("null")) {
			return true
		}
	}
	return false
}

func wp17JSONEqual(t *testing.T, left, right []byte) bool {
	t.Helper()
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if err := leftDecoder.Decode(&leftValue); err != nil {
		t.Fatal(err)
	}
	if err := rightDecoder.Decode(&rightValue); err != nil {
		t.Fatal(err)
	}
	leftCanonical, err := json.Marshal(leftValue)
	if err != nil {
		t.Fatal(err)
	}
	rightCanonical, err := json.Marshal(rightValue)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(leftCanonical, rightCanonical)
}

func wp17ReadDocument(t *testing.T, ctx context.Context, client *store.Client, collection, key string) []byte {
	t.Helper()
	var output []byte
	if err := client.VisitCollectionDocumentsRaw(ctx, collection, func(raw json.RawMessage) error {
		var identity struct {
			Key string `json:"_key"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return err
		}
		if identity.Key != key {
			return nil
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		canonical, err := json.Marshal(value)
		output = canonical
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(output) == 0 {
		t.Fatalf("document %s/%s not found", collection, key)
	}
	return output
}

func wp17Inventory(t *testing.T, ctx context.Context, client *store.Client) map[string][]string {
	t.Helper()
	collections, err := client.CollectionNamesReadOnly(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inventory := map[string][]string{}
	for _, collection := range collections {
		if err := client.VisitCollectionDocumentsRaw(ctx, collection, func(raw json.RawMessage) error {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return err
			}
			canonical, err := json.Marshal(value)
			inventory[collection] = append(inventory[collection], string(canonical))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		sort.Strings(inventory[collection])
	}
	return inventory
}

func wp17OwnerRevision(t *testing.T, ctx context.Context, client *store.Client, owner explorer.Explorer) string {
	t.Helper()
	var revision string
	if err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN {revision:d._rev}`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(owner.Project, owner.ExplorerID)}, func(row map[string]any) error {
		revision, _ = row["revision"].(string)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if revision == "" {
		t.Fatal("owner _rev missing")
	}
	return revision
}

func wp17Count(t *testing.T, ctx context.Context, client *store.Client, collection string) int {
	t.Helper()
	count := -1
	if err := client.QueryRows(ctx, `FOR d IN @@c COLLECT WITH COUNT INTO n RETURN {count:n}`, 1, map[string]any{"@c": collection}, func(row map[string]any) error {
		number, ok := row["count"].(float64)
		if !ok {
			return fmt.Errorf("count result has type %T", row["count"])
		}
		count = int(number)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count < 0 {
		t.Fatal("count query returned no row")
	}
	return count
}

func TestTableShapeProjectionLookupFailsClosedAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw := wp17Fixture(t, "pivot-derived-chain.input.json")
	migration, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := workspaceMigrationSeed(t, ctx, adapter, "wp17-projection-fail-closed", "Legacy shape", raw, migration.OriginalCanonicalDigest, "snapshot-wp17")
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatalf("WP-18 prerequisite migration: %v", err)
	}
	priorHistory := draftRevisionFromExplorer(owner)
	priorHistory.ID = draftRevisionKey(owner.Project, owner.ExplorerID, owner.DraftVersion)
	priorHistoryDoc, err := db.Document(priorHistory, priorHistory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": DraftRevisionsCollection, "doc": priorHistoryDoc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	source := explorer.DraftWorkspaceArtifact(priorHistory)
	base := wp17InsertBaseProjection(t, ctx, client, source, raw)
	if _, err := MigrateStoredTableShapes(ctx, client, false); err != nil {
		t.Fatalf("WP-17 apply: %v", err)
	}
	modernKey := tableShapeConstructionTarget.projectionKey(source)
	modern := wp17ReadProjection(t, ctx, client, modernKey)
	if modern.ID == base.ID || wp17HasTableShape(t, modern.Workspace) {
		t.Fatalf("WP-17 fixture did not produce a Construction projection: %#v", modern)
	}

	if err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key REMOVE d IN @@c RETURN OLD`, 1,
		map[string]any{"@c": workspaceProjectionCollection, "key": modernKey}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WorkspaceProjection(ctx, source); err == nil {
		t.Fatal("missing WP-17 projection fell back to valid but legacy WP-18 projection")
	}

	corrupt := base
	corrupt.ID = corrupt.ContentID()
	corruptDoc, err := db.Document(corrupt, modernKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1,
		map[string]any{"@c": workspaceProjectionCollection, "doc": corruptDoc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WorkspaceProjection(ctx, source); err == nil {
		t.Fatal("corrupt modern projection fell back to the valid WP-18 projection")
	}
}

func TestTableShapeMigrationCASFailureRollsBackExactRevisionHistoryAndProjectionAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw := wp17Fixture(t, "pivot-derived-chain.input.json")
	migration, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := workspaceMigrationSeed(t, ctx, adapter, "wp17-cas-rollback", "Legacy shape", raw, migration.OriginalCanonicalDigest, "snapshot-wp17")
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatalf("WP-18 prerequisite migration: %v", err)
	}
	beforeRaw := wp17ReadDocument(t, ctx, client, ExplorersCollection, explorerKey(owner.Project, owner.ExplorerID))
	beforeVersion := owner.DraftVersion
	beforeDigest := owner.DraftDigest
	historyPreimage := draftRevisionFromExplorer(owner)
	historyPreimage.ID = draftRevisionKey(owner.Project, owner.ExplorerID, owner.DraftVersion)
	projection := newWorkspaceProjection(explorer.DraftWorkspaceArtifact(historyPreimage), migration)
	revision := wp17OwnerRevision(t, ctx, client, owner)
	candidate := owner
	candidate.DraftConfig = append(json.RawMessage(nil), migration.CanonicalJSON...)
	candidate.DraftDigest = migration.MigratedCanonicalDigest
	candidate.Title = "must roll back"

	_, err = adapter.saveDraft(ctx, candidate, owner.DraftVersion, []string{owner.DraftDigest}, &draftMigrationWrite{
		collections: []string{workspaceProjectionCollection}, revision: revision,
		beforeHistory: func(txCtx context.Context, tx store.RowQueryer, _ explorer.Explorer, _ explorer.DraftRevision) error {
			if err := insertWorkspaceProjectionFor(txCtx, tx, projection, tableShapeConstructionTarget); err != nil {
				return err
			}

			return tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH {draftVersion:@next} IN @@c RETURN NEW`, 1,
				map[string]any{"@c": ExplorersCollection, "key": explorerKey(owner.Project, owner.ExplorerID), "next": owner.DraftVersion + 1}, func(map[string]any) error { return nil })
		},
	})
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("migration CAS failure=%v, want draft conflict", err)
	}
	after, err := adapter.Get(ctx, owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DraftVersion != beforeVersion || after.DraftDigest != beforeDigest || after.Title != owner.Title {
		t.Fatalf("failed CAS changed owner: before=%#v after=%#v", owner, after)
	}
	if got := wp17ReadDocument(t, ctx, client, ExplorersCollection, explorerKey(owner.Project, owner.ExplorerID)); !bytes.Equal(got, beforeRaw) {
		t.Fatalf("failed CAS changed owner raw document: before=%s after=%s", beforeRaw, got)
	}
	if histories := draftCASHistory(t, ctx, client, owner); len(histories) != 0 {
		t.Fatalf("failed CAS left %d immutable history records", len(histories))
	}
	if count := wp17Count(t, ctx, client, workspaceProjectionCollection); count != 0 {
		t.Fatalf("failed exact-revision CAS left %d projections", count)
	}
	if recorded, err := migrationVersionRecorded(ctx, client, tableShapeWorkspaceMigrationVersion); err != nil || recorded {
		t.Fatalf("failed CAS recorded WP-17 ledger: recorded=%v err=%v", recorded, err)
	}
}

func TestWP17MixedWorkspaceFixture(t *testing.T) {
	raw := wp17MixedWorkspace(t, wp17Fixture(t, "pivot-derived-chain.input.json"))
	migration, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(migration.Workspace.Documents) != 2 || wp17HasTableShape(t, migration.CanonicalJSON) {
		t.Fatalf("mixed fixture did not migrate all documents: %#v", migration.Workspace.Documents)
	}
}

func TestWP17RawFixturesAndLegacyRecipeAreValid(t *testing.T) {
	for _, name := range []string{"pivot-derived-chain.input.json", "explicit-pivot.input.json", "typed-unpivot.input.json"} {
		raw := wp17Fixture(t, name)
		migration, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
		if err != nil || len(migration.CanonicalJSON) == 0 {
			t.Fatalf("fixture %s migration=%#v err=%v", name, migration, err)
		}
	}
	raw := wp17Fixture(t, "pivot-derived-chain.input.json")
	migration, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := explorer.Explorer{Project: "project-wp17", ExplorerID: "legacy-receipt"}
	receipt := wp17LegacyReceipt(t, owner, raw, migration.OriginalCanonicalDigest)
	if err := receipt.Validate(); err != nil {
		t.Fatalf("old TableReshape receipt invalid: %v", err)
	}
}

func TestTableShapeMigrationRejectsConflictingImmutableHistoryAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw := wp17Fixture(t, "pivot-derived-chain.input.json")
	migration, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := workspaceMigrationSeed(t, ctx, adapter, "wp17-history-collision", "Legacy shape", raw, migration.OriginalCanonicalDigest, "snapshot-wp17")
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatal(err)
	}
	history := draftRevisionFromExplorer(owner)
	history.ID = draftRevisionKey(owner.Project, owner.ExplorerID, owner.DraftVersion)
	history.Title = "different immutable preimage"
	doc, err := db.Document(history, history.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": DraftRevisionsCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ownerBefore := wp17ReadDocument(t, ctx, client, ExplorersCollection, explorerKey(owner.Project, owner.ExplorerID))
	historyBefore := wp17ReadDocument(t, ctx, client, DraftRevisionsCollection, history.ID)
	if _, err := MigrateStoredTableShapes(ctx, client, false); err == nil {
		t.Fatal("accepted conflicting immutable history")
	}
	if !bytes.Equal(ownerBefore, wp17ReadDocument(t, ctx, client, ExplorersCollection, explorerKey(owner.Project, owner.ExplorerID))) {
		t.Fatal("failed migration changed owner")
	}
	if !bytes.Equal(historyBefore, wp17ReadDocument(t, ctx, client, DraftRevisionsCollection, history.ID)) {
		t.Fatal("failed migration changed immutable history")
	}
	if recorded, err := migrationVersionRecorded(ctx, client, tableShapeWorkspaceMigrationVersion); err != nil || recorded {
		t.Fatalf("failed migration sealed ledger: %v %v", recorded, err)
	}
}

func TestTableShapeMigrationLargeIntegerProjectionAgainstArango(t *testing.T) {
	ctx, client, _, _ := draftCASDB(t)
	raw := wp17Fixture(t, "typed-unpivot.input.json")
	if !bytes.Contains(raw, []byte(`"integer":10`)) {
		t.Fatal("missing integer fixture")
	}
	raw = bytes.Replace(raw, []byte(`"integer":10`), []byte(`"integer":9007199254740993`), 1)
	migrated, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	source := workspacepkg.Artifact{Kind: "draft-revision", Project: "precision", ExplorerID: "precision", ID: "history", RawDigest: "original", IntentDigest: migrated.OriginalCanonicalDigest}
	projection := newWorkspaceProjection(source, migrated)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{workspaceProjectionCollectionSpec()}}); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := insertWorkspaceProjectionFor(ctx, client, projection, tableShapeConstructionTarget); err != nil {
			t.Fatalf("projection attempt %d: %v", attempt, err)
		}
	}
	found := false
	if err := client.VisitCollectionDocumentsRaw(ctx, workspaceProjectionCollection, func(raw json.RawMessage) error {
		if strings.Contains(string(raw), `9007199254740992`) {
			t.Fatal("rounded stored integer")
		}
		if strings.Contains(string(raw), `9007199254740993`) {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("exact stored integer not found")
	}
}

func TestTableShapeMigrationLargeIntegerOwnerAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw := bytes.Replace(wp17Fixture(t, "typed-unpivot.input.json"), []byte(`"integer":10`), []byte(`"integer":9007199254740993`), 1)
	migrated, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := explorer.Explorer{Project: "project-wp17-precision", ExplorerID: "large-integer", Title: "Exact integer", ManagementMode: viewer.ManagementInteractive, DraftVersion: 7, DraftDigest: migrated.OriginalCanonicalDigest, DraftConfig: raw, DraftSnapshotToken: "snapshot-precision", DraftSourceGeneration: "generation-precision", DraftAuthorizationScopeDigest: "scope-precision"}
	doc, err := db.MigrationDocument(owner, explorerKey(owner.Project, owner.ExplorerID))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatal(err)
	}
	dry, err := MigrateStoredTableShapes(ctx, client, true)
	if err != nil || dry.Owners != 1 || dry.ChangedDigests != 1 {
		t.Fatalf("dry-run: %#v %v", dry, err)
	}
	if _, err := MigrateStoredTableShapes(ctx, client, false); err != nil {
		t.Fatal(err)
	}
	var saved explorer.Explorer
	if err := client.VisitCollectionDocumentsRaw(ctx, ExplorersCollection, func(raw json.RawMessage) error {
		if bytes.Contains(raw, []byte(`9007199254740992`)) {
			t.Fatal("migration rounded owner scalar")
		}
		return json.Unmarshal(raw, &saved)
	}); err != nil {
		t.Fatal(err)
	}
	if saved.DraftVersion != 8 || saved.DraftDigest != migrated.MigratedCanonicalDigest || !bytes.Contains(saved.DraftConfig, []byte(`9007199254740993`)) {
		t.Fatalf("invalid migrated owner: %#v", saved)
	}
	var history explorer.DraftRevision
	if err := client.VisitCollectionDocumentsRaw(ctx, DraftRevisionsCollection, func(raw json.RawMessage) error { return json.Unmarshal(raw, &history) }); err != nil {
		t.Fatal(err)
	}
	if history.DraftDigest != owner.DraftDigest || !bytes.Contains(history.DraftConfig, []byte(`9007199254740993`)) {
		t.Fatalf("immutable history lost original scalar: %#v", history)
	}
	projection, err := adapter.WorkspaceProjection(ctx, explorer.DraftWorkspaceArtifact(history))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(projection.Workspace, []byte(`9007199254740993`)) {
		t.Fatal("runtime projection lost integer")
	}
	before := wp17Inventory(t, ctx, client)
	if report, err := MigrateStoredTableShapes(ctx, client, false); err != nil || !report.AlreadyApplied || report.Owners != 0 {
		t.Fatalf("rerun: %#v %v", report, err)
	}
	if !reflect.DeepEqual(before, wp17Inventory(t, ctx, client)) {
		t.Fatal("rerun rewrote persisted records")
	}
}

func TestTableShapeArchivedLargeIntegerReceiptAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw := wp17Fixture(t, "typed-unpivot.input.json")
	migrated, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := explorer.Explorer{Project: "project-wp17-receipt-precision", ExplorerID: "large-receipt"}
	receipt := wp17LegacyReceipt(t, owner, raw, migrated.OriginalCanonicalDigest)
	archived, err := receipt.Bundle.ArchiveCanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	from := []byte(`"kind":"STRING","string":"measurement"`)
	if !bytes.Contains(archived, from) {
		t.Fatalf("missing expected scalar in fixture: %s", archived)
	}
	archived = bytes.Replace(archived, from, []byte(`"kind":"INTEGER","integer":9007199254740993`), 1)
	if err := json.Unmarshal(archived, &receipt.Bundle); err != nil {
		t.Fatal(err)
	}
	receipt.ResolvedRecipeDigest, err = receipt.Bundle.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompilationKey, err = receiptmodel.CompilationKey(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = receiptmodel.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.ValidateArchive(); err != nil {
		t.Fatal(err)
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatal(err)
	}
	doc, err := db.MigrationDocument(receipt, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": CompilationReceiptsCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	stored, err := adapter.GetCompilationReceiptForExplorer(ctx, owner.Project, owner.ExplorerID, receipt.ID)
	if err != nil {
		t.Fatalf("authentic archived receipt became unverifiable on read: %v", err)
	}
	if stored.ID != receipt.ID || stored.CompilationKey != receipt.CompilationKey || stored.ResolvedRecipeDigest != receipt.ResolvedRecipeDigest {
		t.Fatal("archive identity changed")
	}
	if err := stored.ValidateID(); err != nil {
		t.Fatal(err)
	}
	exact, err := stored.Bundle.ArchiveCanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(exact, []byte(`9007199254740993`)) {
		t.Fatal("receipt scalar rounded")
	}
}

func TestTableShapeMigrationReusesJSONHistoryWithoutRewritingAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw := wp17Fixture(t, "typed-unpivot.input.json")
	migrated, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := workspaceMigrationSeed(t, ctx, adapter, "wp17-history-order", "Matching history", raw, migrated.OriginalCanonicalDigest, "snapshot-order")
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatal(err)
	}
	history := draftRevisionFromExplorer(owner)
	history.ID = draftRevisionKey(owner.Project, owner.ExplorerID, owner.DraftVersion)
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	var packet map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &packet); err != nil {
		t.Fatal(err)
	}
	packet["_key"], err = json.Marshal(history.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `LET doc = JSON_PARSE(@raw) INSERT doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": DraftRevisionsCollection, "raw": string(encoded)}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	before := wp17ReadDocument(t, ctx, client, DraftRevisionsCollection, history.ID)
	if _, err := MigrateStoredTableShapes(ctx, client, false); err != nil {
		t.Fatalf("equivalent history JSON object order caused collision: %v", err)
	}
	after := wp17ReadDocument(t, ctx, client, DraftRevisionsCollection, history.ID)
	if !bytes.Equal(before, after) {
		t.Fatal("migration rewrote matching immutable history")
	}
	saved, err := adapter.Get(ctx, owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.DraftVersion != owner.DraftVersion+1 || saved.DraftDigest != migrated.MigratedCanonicalDigest {
		t.Fatal("owner did not advance exactly once")
	}
	inventory := wp17Inventory(t, ctx, client)
	if report, err := MigrateStoredTableShapes(ctx, client, false); err != nil || !report.AlreadyApplied || report.Owners != 0 {
		t.Fatalf("rerun: %#v %v", report, err)
	}
	if !reflect.DeepEqual(inventory, wp17Inventory(t, ctx, client)) {
		t.Fatal("rerun changed records")
	}
}
