package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

func TestImmutableWorkspaceMigrationReceiptFixture(t *testing.T) {
	raw, oldDigest := workspaceMigrationNilStepsWire(t)
	owner := explorer.Explorer{Project: "project-wp18-legacy-repo", ExplorerID: "default"}
	receipt := workspaceMigrationReceipt(t, owner, raw, oldDigest, "immutable-fixture")
	if err := receipt.Validate(); err != nil {
		t.Fatalf("nil-step receipt fixture is invalid: %v", err)
	}
	if _, err := ws.MigrateStoredWorkspace(raw, ws.CatalogSnapshot{}); err != nil {
		t.Fatalf("nil-step workspace fixture is invalid: %v", err)
	}
}

func TestImmutableWorkspaceMigrationAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	raw, oldDigest := workspaceMigrationNilStepsWire(t)
	migration, err := ws.MigrateStoredWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}

	publishedProject := "project-wp18-legacy-repo"
	publishedOwner := explorer.Explorer{Project: publishedProject, ExplorerID: "default"}
	receipt := workspaceMigrationReceipt(t, publishedOwner, raw, oldDigest, "immutable-old-receipt")
	if _, err := adapter.InsertCompilationReceipt(ctx, receipt); err != nil {
		t.Fatalf("insert immutable receipt: %v", err)
	}
	published := explorer.Revision{ID: "wp18-legacy-published-revision", Project: publishedProject, ExplorerID: "default",
		AuthoringBundle: append(json.RawMessage(nil), raw...), IntentDigest: oldDigest, CompilationReceiptID: receipt.ID,
		Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest,
		SourceGeneration: receipt.SourceGeneration, Status: explorer.RevisionReady, CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}
	storedRevision, err := adapter.InsertRevision(ctx, published)
	if err != nil {
		t.Fatalf("insert immutable revision: %v", err)
	}
	if storedRevision.ID != published.ID || storedRevision.IntentDigest != published.IntentDigest {
		t.Fatalf("inserted revision differs from immutable seed: %#v", storedRevision)
	}

	historyOwner := explorer.Explorer{Project: "project-wp18-existing-history", ExplorerID: "patients", DraftVersion: 5,
		DraftDigest: oldDigest, DraftConfig: append(json.RawMessage(nil), raw...), DraftSnapshotToken: "snapshot-wp18",
		DraftSourceGeneration: "generation-wp18", DraftAuthorizationScopeDigest: "scope-wp18", UpdatedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	history := draftRevisionFromExplorer(historyOwner)
	history.ID = draftRevisionKey(historyOwner.Project, historyOwner.ExplorerID, historyOwner.DraftVersion)
	historyKey := history.ID
	historyDoc, err := db.Document(history, historyKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": DraftRevisionsCollection, "doc": historyDoc}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("insert preexisting draft history: %v", err)
	}

	legacyConfigKey := "wp18-legacy-repository-config"
	legacyConfig := map[string]any{"_key": legacyConfigKey, "project": publishedProject,
		"workspace": append(json.RawMessage(nil), raw...), "intentDigest": oldDigest,
		"activeRevisionId": published.ID, "updatedAt": time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{"@c": LegacyRepositoryConfigsCollection, "doc": legacyConfig}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("insert legacy repository config: %v", err)
	}

	beforeRevision := immutableWorkspaceMigrationDocument(t, ctx, client, RevisionsCollection, published.ID)
	beforeHistory := immutableWorkspaceMigrationDocument(t, ctx, client, DraftRevisionsCollection, historyKey)
	beforeReceipt, err := adapter.GetCompilationReceiptForExplorer(ctx, published.Project, published.ExplorerID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeInventory := workspaceMigrationInventory(t, ctx, client)
	dryRun, err := MigrateStoredWorkspaces(ctx, client, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Projections != 2 || dryRun.RepositoryConfigs != 1 || dryRun.RetiredReceipts != 1 || dryRun.ChangedDigests < 3 {
		t.Fatalf("immutable packet dry-run report = %#v", dryRun)
	}
	if after := workspaceMigrationInventory(t, ctx, client); !reflect.DeepEqual(beforeInventory, after) {
		t.Fatal("dry-run changed the immutable packet inventory")
	}

	report, err := MigrateStoredWorkspaces(ctx, client, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Projections != 2 || report.RepositoryConfigs != 1 || report.RetiredReceipts != 1 {
		t.Fatalf("immutable packet apply report = %#v", report)
	}
	if after := immutableWorkspaceMigrationDocument(t, ctx, client, RevisionsCollection, published.ID); !bytes.Equal(beforeRevision, after) {
		t.Fatalf("migration rewrote immutable revision, including _rev:\nbefore=%s\nafter=%s", beforeRevision, after)
	}
	if after := immutableWorkspaceMigrationDocument(t, ctx, client, DraftRevisionsCollection, historyKey); !bytes.Equal(beforeHistory, after) {
		t.Fatalf("migration rewrote preexisting immutable history, including _rev:\nbefore=%s\nafter=%s", beforeHistory, after)
	}

	migratedRevision, err := adapter.GetRevision(ctx, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(adapter)
	if err != nil {
		t.Fatal(err)
	}
	revisionWorkspace, err := domain.RevisionWorkspace(ctx, *migratedRevision)
	if err != nil {
		t.Fatalf("load immutable revision through projection: %v", err)
	}
	revisionCanonical, err := revisionWorkspace.CanonicalJSON()
	if err != nil || !bytes.Equal(revisionCanonical, migration.CanonicalJSON) {
		t.Fatalf("revision projection canonical=%s want=%s err=%v", revisionCanonical, migration.CanonicalJSON, err)
	}
	storedHistory, err := adapter.GetDraftRevision(ctx, historyOwner.Project, historyOwner.ExplorerID, historyKey)
	if err != nil {
		t.Fatal(err)
	}
	historyWorkspace, historyDigest, err := domain.DraftRevisionWorkspace(ctx, *storedHistory)
	if err != nil {
		t.Fatalf("load preexisting history through projection: %v", err)
	}
	historyCanonical, err := historyWorkspace.CanonicalJSON()
	if err != nil || !bytes.Equal(historyCanonical, migration.CanonicalJSON) || historyDigest != migration.MigratedCanonicalDigest {
		t.Fatalf("history projection canonical=%s digest=%s want=%s/%s err=%v", historyCanonical, historyDigest, migration.CanonicalJSON, migration.MigratedCanonicalDigest, err)
	}

	storedReceipt, err := adapter.GetCompilationReceiptForExplorer(ctx, published.Project, published.ExplorerID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedReceipt.ID != beforeReceipt.ID || storedReceipt.IntentDigest != beforeReceipt.IntentDigest || storedReceipt.IntentDigest == migration.MigratedCanonicalDigest {
		t.Fatalf("old receipt identity/binding changed or remained current: before=%#v after=%#v", beforeReceipt, storedReceipt)
	}
	if err := storedReceipt.Validate(); err != nil {
		t.Fatalf("old receipt is no longer verifiable: %v", err)
	}

	var updatedLegacyConfig map[string]json.RawMessage
	if err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1,
		map[string]any{"@c": LegacyRepositoryConfigsCollection, "key": legacyConfigKey}, func(row map[string]any) error {
			encoded, err := json.Marshal(row)
			if err != nil {
				return err
			}
			return json.Unmarshal(encoded, &updatedLegacyConfig)
		}); err != nil {
		t.Fatal(err)
	}
	if updatedLegacyConfig == nil || string(updatedLegacyConfig["intentDigest"]) != `"`+migration.MigratedCanonicalDigest+`"` {
		t.Fatalf("legacy repository digest was not migrated: %#v", updatedLegacyConfig)
	}
	var legacyWorkspace ws.Workspace
	if err := json.Unmarshal(updatedLegacyConfig["workspace"], &legacyWorkspace); err != nil || legacyWorkspace.SemanticsVersion != ws.CurrentSemanticsVersion {
		t.Fatalf("legacy repository workspace was not replaced by current projection: %#v err=%v", legacyWorkspace, err)
	}

	if err := adapter.MigrateLegacyRepositoryConfigs(ctx); err != nil {
		t.Fatalf("startup legacy repository owner migration: %v", err)
	}
	owner, err := adapter.Get(ctx, publishedProject, "default")
	if err != nil {
		t.Fatal(err)
	}
	strictWorkspace, err := ws.DecodeCurrentWorkspace(owner.DraftConfig)
	if err != nil || strictWorkspace.SemanticsVersion != ws.CurrentSemanticsVersion || owner.DraftDigest != migration.MigratedCanonicalDigest {
		t.Fatalf("startup owner is not strict/current: owner=%#v workspace=%#v err=%v", owner, strictWorkspace, err)
	}

	afterStartup := workspaceMigrationInventory(t, ctx, client)
	rerun, err := MigrateStoredWorkspaces(ctx, client, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rerun.AlreadyApplied || rerun.Projections != 0 || rerun.RepositoryConfigs != 0 || rerun.Owners != 0 {
		t.Fatalf("immutable migration rerun = %#v", rerun)
	}
	if after := workspaceMigrationInventory(t, ctx, client); !reflect.DeepEqual(afterStartup, after) {
		t.Fatal("migration rerun changed immutable packet inventory")
	}
}

func immutableWorkspaceMigrationDocument(t *testing.T, ctx context.Context, client *store.Client, collection, key string) []byte {
	t.Helper()
	var result []byte
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
		result = canonical
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatalf("document %s/%s not found", collection, key)
	}
	return result
}
