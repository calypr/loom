package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
)

func TestStoredWorkspaceMigrationAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	legacyRaw := workspaceMigrationLegacyCodedWire("Legacy coded")
	legacyPlan, err := authoringv2.MigrateStoredWorkspace(legacyRaw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	mixedRaw := workspaceMigrationMixedWire("Mixed workspace")
	mixedPlan, err := authoringv2.MigrateStoredWorkspace(mixedRaw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	nilStepsRaw, nilStepsDigest := workspaceMigrationNilStepsWire(t)
	nilStepsPlan, err := authoringv2.MigrateStoredWorkspace(nilStepsRaw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	currentRaw, currentDigest := draftCASPayload(t, "Already current")

	legacySemanticsRaw, legacySemanticsDigest := workspaceMigrationLegacySemanticsWire(t)
	legacySemanticsPlan, err := authoringv2.MigrateStoredWorkspace(legacySemanticsRaw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	legacySnapshot := workspaceMigrationCapabilitySnapshot(t, ctx, client)
	legacyOwner := workspaceMigrationSeed(t, ctx, adapter, "legacy-coded", "Legacy coded", legacyRaw, legacyPlan.OriginalCanonicalDigest, "snapshot-wp18")
	mixedOwner := workspaceMigrationSeed(t, ctx, adapter, "mixed", "Mixed workspace", mixedRaw, mixedPlan.OriginalCanonicalDigest, "snapshot-wp18")
	nilStepsOwner := workspaceMigrationSeed(t, ctx, adapter, "nil-steps", "Legacy nil steps", nilStepsRaw, nilStepsDigest, "snapshot-wp18")
	legacySemanticsOwner := workspaceMigrationSeed(t, ctx, adapter, "legacy-semantics", "Legacy semantics", legacySemanticsRaw, legacySemanticsDigest, legacySnapshot.Token)
	currentOwner := workspaceMigrationSeed(t, ctx, adapter, "current", "Already current", currentRaw, currentDigest, "snapshot-wp18")

	legacyReceipt := workspaceMigrationReceipt(t, nilStepsOwner, nilStepsRaw, nilStepsDigest, "old-receipt")
	if _, err := adapter.InsertCompilationReceipt(ctx, legacyReceipt); err != nil {
		t.Fatalf("insert legacy receipt: %v", err)
	}
	currentReceipt := workspaceMigrationReceipt(t, currentOwner, currentRaw, currentDigest, "current-receipt")
	if _, err := adapter.InsertCompilationReceipt(ctx, currentReceipt); err != nil {
		t.Fatalf("insert current receipt: %v", err)
	}

	beforeDryRun := workspaceMigrationInventory(t, ctx, client)
	dryRun, err := MigrateStoredWorkspaces(ctx, client, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Owners != 4 || dryRun.ChangedDigests < 2 || dryRun.RetiredReceipts != 1 {
		t.Fatalf("dry-run report = %#v, want four owners, digest changes, and one retired old receipt", dryRun)
	}
	if after := workspaceMigrationInventory(t, ctx, client); !reflect.DeepEqual(beforeDryRun, after) {
		t.Fatalf("dry-run changed Arango inventory:\nbefore=%#v\nafter=%#v", beforeDryRun, after)
	}

	report, err := MigrateStoredWorkspaces(ctx, client, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.DryRun || report.Owners != 4 || report.ChangedDigests < 2 || report.RetiredReceipts != 1 {
		t.Fatalf("apply report = %#v", report)
	}
	wantDigests := map[string]string{"legacy-coded": legacyPlan.MigratedCanonicalDigest, "mixed": mixedPlan.MigratedCanonicalDigest,
		"nil-steps": nilStepsPlan.MigratedCanonicalDigest, "legacy-semantics": legacySemanticsPlan.MigratedCanonicalDigest}
	for _, before := range []explorer.Explorer{legacyOwner, mixedOwner, nilStepsOwner, legacySemanticsOwner} {
		after, err := adapter.Get(ctx, before.Project, before.ExplorerID)
		if err != nil {
			t.Fatal(err)
		}
		if after.DraftVersion != before.DraftVersion+1 || after.DraftDigest != wantDigests[before.ExplorerID] {
			t.Fatalf("owner %s version/digest after migration = %d/%q, want version %d digest %q", before.ExplorerID, after.DraftVersion, after.DraftDigest, before.DraftVersion+1, wantDigests[before.ExplorerID])
		}
		workspaceMigrationVerifyHistoryProjection(t, ctx, client, adapter, before, wantDigests[before.ExplorerID])
	}
	unchangedCurrent, err := adapter.Get(ctx, currentOwner.Project, currentOwner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if unchangedCurrent.DraftVersion != currentOwner.DraftVersion || unchangedCurrent.DraftDigest != currentOwner.DraftDigest {
		t.Fatalf("current owner changed: before=%#v after=%#v", currentOwner, unchangedCurrent)
	}
	for label, raw := range map[string][]byte{"retired coded source": legacyRaw, "legacy nil steps": nilStepsRaw} {
		if _, err := authoringv2.DecodeCurrentWorkspace(raw); err == nil {
			t.Fatalf("strict runtime decoder accepted %s after migration", label)
		}
	}

	for _, expected := range []explorer.CompilationReceipt{legacyReceipt, currentReceipt} {
		stored, err := adapter.GetCompilationReceiptForExplorer(ctx, expected.Project, expected.ExplorerID, expected.ID)
		if err != nil {
			t.Fatal(err)
		}
		id, err := explorer.ReceiptID(*stored)
		if err != nil || id != expected.ID || stored.ID != expected.ID {
			t.Fatalf("receipt identity changed: stored=%q recomputed=%q err=%v", stored.ID, id, err)
		}
		if err := stored.Validate(); err != nil {
			t.Fatalf("preserved receipt no longer validates: %v", err)
		}
		if expected.ID == legacyReceipt.ID {
			migratedOwner, err := adapter.Get(ctx, nilStepsOwner.Project, nilStepsOwner.ExplorerID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.IntentDigest == migratedOwner.DraftDigest {
				t.Fatal("old receipt unexpectedly remains bound to the migrated draft digest")
			}
		}
		if expected.ID == currentReceipt.ID && stored.IntentDigest != currentDigest {
			t.Fatal("current receipt identity or intent digest changed")
		}
	}

	afterApply := workspaceMigrationInventory(t, ctx, client)
	rerun, err := MigrateStoredWorkspaces(ctx, client, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rerun.AlreadyApplied || rerun.Owners != 0 || rerun.Projections != 0 || rerun.RepositoryConfigs != 0 {
		t.Fatalf("rerun report = %#v", rerun)
	}
	if after := workspaceMigrationInventory(t, ctx, client); !reflect.DeepEqual(afterApply, after) {
		t.Fatalf("idempotent rerun changed inventory:\nbefore=%#v\nafter=%#v", afterApply, after)
	}
}

func TestStoredWorkspaceMigrationFixturePackets(t *testing.T) {
	for name, raw := range map[string][]byte{
		"legacy coded": workspaceMigrationLegacyCodedWire("Legacy coded"),
		"mixed":        workspaceMigrationMixedWire("Mixed workspace"),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := authoringv2.MigrateStoredWorkspace(raw, authoringv2.CatalogSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if result.MigratedCanonicalDigest == "" || !json.Valid(result.CanonicalJSON) || result.Workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion {
				t.Fatalf("invalid migrated packet: %#v", result)
			}
		})
	}
	legacyRaw, legacyDigest := workspaceMigrationLegacySemanticsWire(t)
	legacy, err := authoringv2.MigrateStoredWorkspace(legacyRaw, authoringv2.CatalogSnapshot{})
	if err != nil || legacy.OriginalCanonicalDigest != legacyDigest {
		t.Fatalf("legacy semantics fixture result=%#v err=%v", legacy, err)
	}
	nilRaw, nilDigest := workspaceMigrationNilStepsWire(t)
	nilResult, err := authoringv2.MigrateStoredWorkspace(nilRaw, authoringv2.CatalogSnapshot{})
	if err != nil || nilResult.OriginalLegacyNilStepsDigest != nilDigest || !nilResult.DigestChanged {
		t.Fatalf("nil-step fixture result=%#v err=%v", nilResult, err)
	}
	owner := explorer.Explorer{Project: "project-wp18", ExplorerID: "receipt-fixture"}
	receipt := workspaceMigrationReceipt(t, owner, legacyRaw, legacyDigest, "fixture")
	if err := receipt.Validate(); err != nil {
		t.Fatalf("fixture receipt is invalid: %v", err)
	}
}

func TestStoredWorkspaceMigrationCASFailureRollsBackHistoryAndProjectionAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{workspaceProjectionCollectionSpec()}}); err != nil {
		t.Fatal(err)
	}
	before := draftCASSeed(t, ctx, adapter)
	migrated, err := authoringv2.MigrateStoredWorkspace(before.DraftConfig, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	historyBefore := draftCASHistory(t, ctx, client, before)
	if len(historyBefore) != 0 {
		t.Fatalf("seed history count=%d, want 0", len(historyBefore))
	}
	projection := newWorkspaceProjection(explorer.DraftWorkspaceArtifact(draftRevisionFromExplorer(before)), migrated)
	candidate := before
	candidate.DraftConfig = append(json.RawMessage(nil), migrated.CanonicalJSON...)
	candidate.DraftDigest = migrated.MigratedCanonicalDigest
	candidate.Title = "should roll back"
	revision := workspaceMigrationOwnerRevision(t, ctx, client, before)

	_, err = adapter.saveDraft(ctx, candidate, before.DraftVersion, []string{before.DraftDigest}, &draftMigrationWrite{
		collections: []string{workspaceProjectionCollection},
		revision:    revision,
		beforeHistory: func(txCtx context.Context, tx store.RowQueryer, _ explorer.Explorer, _ explorer.DraftRevision) error {
			if err := insertWorkspaceProjection(txCtx, tx, projection); err != nil {
				return err
			}
			// Simulate a competing head update inside this transaction. SaveDraft's
			// revision-bound CAS then misses, requiring Arango to roll back history,
			// projection, and this update together.
			return tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key
UPDATE d WITH {draftVersion:@next} IN @@c RETURN NEW`, 1, map[string]any{
				"@c": ExplorersCollection, "key": explorerKey(before.Project, before.ExplorerID), "next": before.DraftVersion + 1,
			}, func(map[string]any) error { return nil })
		},
	})
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("SaveDraft migration failure=%v, want draft conflict", err)
	}
	after, err := adapter.Get(ctx, before.Project, before.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DraftVersion != before.DraftVersion || after.DraftDigest != before.DraftDigest || after.Title != before.Title {
		t.Fatalf("failed CAS changed owner: before=%#v after=%#v", before, after)
	}
	if histories := draftCASHistory(t, ctx, client, before); len(histories) != 0 {
		t.Fatalf("failed CAS left %d history records", len(histories))
	}
	if count := workspaceMigrationCollectionCount(t, ctx, client, workspaceProjectionCollection); count != 0 {
		t.Fatalf("failed CAS left %d immutable projections", count)
	}
}

func workspaceMigrationSeed(t *testing.T, ctx context.Context, adapter *Store, id, title string, raw []byte, digest, snapshotToken string) explorer.Explorer {
	t.Helper()
	if digest == "" {
		t.Fatal("migration fixture requires the original stored digest")
	}
	owner := explorer.Explorer{Project: "project-wp18", ExplorerID: id, Title: title, ManagementMode: explorer.ManagementInteractive,
		DraftConfig: append(json.RawMessage(nil), raw...), DraftVersion: 7, DraftDigest: digest,
		DraftSnapshotToken: snapshotToken, DraftSourceGeneration: "generation-wp18", DraftAuthorizationScopeDigest: "scope-wp18",
		UpdatedBy: "wp18-fixture", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	created, err := adapter.Create(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	return *created
}

func workspaceMigrationLegacyCodedWire(title string) []byte {
	return []byte(`{"apiVersion":"` + authoringv2.APIVersion + `","kind":"` + authoringv2.WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(authoringv2.CurrentSemanticsVersion) + `,"explorer":{"title":"` + title + `"},"documents":[{"kind":"` + authoringv2.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Observation","route":{"occurrenceId":"base","resourceType":"Observation"},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"height","label":"Height","occurrenceId":"base","source":{"kind":"codingBySystem","lookup":{"binding":{"ownerPath":"","keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","choiceArms":["valueQuantity"],"logicalType":"decimal"},"key":{"system":"urn:study","code":"height"},"projectionMode":"VALUE"}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
}

func workspaceMigrationMixedWire(title string) []byte {
	raw := strings.Replace(string(workspaceMigrationLegacyCodedWire(title)), `"columns":[{"column":"height","label":"Height","occurrenceId":"base","source":{"kind":"codingBySystem","lookup":{"binding":{"ownerPath":"","keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","choiceArms":["valueQuantity"],"logicalType":"decimal"},"key":{"system":"urn:study","code":"height"},"projectionMode":"VALUE"}}}]`, `"columns":[{"column":"height","label":"Height","occurrenceId":"base","source":{"kind":"codingBySystem","lookup":{"binding":{"ownerPath":"","keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","choiceArms":["valueQuantity"],"logicalType":"decimal"},"key":{"system":"urn:study","code":"height"},"projectionMode":"VALUE"}}},{"column":"issued","label":"Issued","occurrenceId":"base","source":{"kind":"field","field":{"path":"issued","projectionMode":"VALUE"}}}]`, 1)
	return []byte(raw)
}

func workspaceMigrationLegacySemanticsWire(t *testing.T) ([]byte, string) {
	t.Helper()
	document := authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		SemanticsVersion: authoringv2.CurrentSemanticsVersion - 1, Explorer: authoringv2.ExplorerMetadata{Title: "Legacy semantics"},
		Documents: []authoringv2.Document{document}, Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}}}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := authoringv2.MigrateStoredWorkspace(raw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	return raw, plan.OriginalCanonicalDigest
}

func workspaceMigrationCapabilitySnapshot(t *testing.T, ctx context.Context, client *store.Client) capability.Snapshot {
	t.Helper()
	identity := capability.SnapshotIdentity{Project: "project-wp18", Generation: "generation-wp18", AuthorizationScopeDigest: "scope-wp18",
		SchemaDigest: "schema-wp18", ResourceInventoryDigest: "inventory-wp18", RelationshipDigest: "relationships-wp18", FieldDigest: "fields-wp18",
		ProtocolVersion: "protocol-wp18", CompilerVersion: "compiler-wp18", TraversalPolicyVersion: "traversal-wp18", ProjectionPolicyVersion: "projection-wp18"}
	snapshot := capability.NewSnapshot(identity, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true, Populated: true}}, nil, nil, nil)
	if err := capability.ValidateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	storeAdapter, err := NewCapabilitySnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeAdapter.Put(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func workspaceMigrationNilStepsWire(t *testing.T) ([]byte, string) {
	t.Helper()
	document := authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}
	document, err := authoringv2.UpgradeDocumentToConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		SemanticsVersion: authoringv2.CurrentSemanticsVersion, Explorer: authoringv2.ExplorerMetadata{Title: "Legacy nil steps"},
		Documents: []authoringv2.Document{document}, Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}}}
	workspace.Documents[0].Construction.Steps = nil
	raw, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	legacyDigest, err := workspace.LegacyNilConstructionStepsDigest()
	if err != nil {
		t.Fatal(err)
	}
	return raw, legacyDigest
}

func workspaceMigrationReceipt(t *testing.T, owner explorer.Explorer, raw []byte, digest, requestID string) explorer.CompilationReceipt {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "wp18-test", TranslationVersion: "wp18-test", Outputs: []recipe.Output{{Name: "out", RootResourceType: "Patient", RowGrain: "patient"}}}
	receipt := explorer.CompilationReceipt{ReceiptFormatVersion: explorer.CompilationReceiptFormatVersion,
		CompilerContractVersion: explorer.CompilationReceiptCompilerContractVersion, Project: owner.Project, ExplorerID: owner.ExplorerID,
		IntentDigest: digest, SnapshotToken: "snapshot-wp18", AuthorizationScopeDigest: "scope-wp18", CapabilitySchemaDigest: "schema-wp18",
		ShapeDigest: "shape-wp18", SourceGeneration: "generation-wp18", RecipeDigest: "sha256:recipe", ResolvedSchemaDigest: "sha256:resolved",
		NormalizedBundle: append(json.RawMessage(nil), raw...), Bundle: bundle, CompiledConfig: json.RawMessage(`{"views":[]}`),
		PublicOutputContract:   json.RawMessage(`{"outputs":[{"outputId":"out","columns":[],"lossless":true,"mlReady":true}]}`),
		OutputColumnProvenance: map[string]map[string]string{"out": {"__loom_row_id": "EXPLICIT"}}, Warnings: []explorer.CompilationWarning{{Code: "EMPTY_OUTPUT", Message: "empty output"}}, RequestID: requestID}
	receipt.ResolvedRecipeDigest, _ = bundle.Digest()
	receipt.OutputContractDigest, _ = explorer.CompilationArtifactDigest(receipt.PublicOutputContract)
	receipt.CompilationKey, _ = explorer.CompilationKey(receipt)
	var err error
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func workspaceMigrationVerifyHistoryProjection(t *testing.T, ctx context.Context, client *store.Client, adapter *Store, before explorer.Explorer, wantDigest string) {
	t.Helper()
	history := draftCASHistory(t, ctx, client, before)
	if len(history) != 1 {
		t.Fatalf("history count=%d, want one original draft", len(history))
	}
	if !bytes.Equal(history[0].DraftConfig, before.DraftConfig) || history[0].DraftDigest != before.DraftDigest || history[0].DraftVersion != before.DraftVersion {
		t.Fatalf("history did not preserve exact original packet: %#v", history[0])
	}
	source := explorer.DraftWorkspaceArtifact(history[0])
	projection, err := adapter.WorkspaceProjection(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := projection.Validate(source)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(adapter)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := domain.DraftRevisionWorkspace(ctx, history[0])
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion || projection.Digest != wantDigest || digest != wantDigest || !json.Valid(canonical) {
		t.Fatalf("history projection result version=%d projection=%q service=%q want=%q bytes=%s", workspace.SemanticsVersion, projection.Digest, digest, wantDigest, canonical)
	}
}

func workspaceMigrationInventory(t *testing.T, ctx context.Context, client *store.Client) map[string][]string {
	t.Helper()
	collections, err := client.CollectionNamesReadOnly(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inventory := map[string][]string{}
	for _, collection := range collections {
		err := client.VisitCollectionDocumentsRaw(ctx, collection, func(raw json.RawMessage) error {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return err
			}
			canonical, err := json.Marshal(value)
			if err != nil {
				return err
			}
			inventory[collection] = append(inventory[collection], string(canonical))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(inventory[collection])
	}
	return inventory
}

func workspaceMigrationOwnerRevision(t *testing.T, ctx context.Context, client *store.Client, owner explorer.Explorer) string {
	t.Helper()
	var revision string
	if err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN {revision:d._rev}`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(owner.Project, owner.ExplorerID)}, func(row map[string]any) error {
		revision, _ = row["revision"].(string)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if revision == "" {
		t.Fatal("owner revision missing")
	}
	return revision
}

func workspaceMigrationCollectionCount(t *testing.T, ctx context.Context, client *store.Client, collection string) int {
	t.Helper()
	count := 0
	if err := client.QueryRows(ctx, `FOR d IN @@c COLLECT WITH COUNT INTO n RETURN {count:n}`, 1, map[string]any{"@c": collection}, func(row map[string]any) error {
		number, ok := row["count"].(float64)
		if !ok {
			return fmt.Errorf("unexpected Arango count row: %#v", row)
		}
		count = int(number)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
