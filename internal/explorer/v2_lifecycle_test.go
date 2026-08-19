package explorer

import (
	"context"
	"testing"
)

func TestV2DraftRoundTripLastWriteWins(t *testing.T) {
	service, err := NewService(NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"my-explorer","title":"My Explorer","description":"presentation survives","management":"interactive"},"recipe":{"recipeSchemaVersion":1,"name":"explorer-my-explorer","translationVersion":"interactive","outputs":[{"name":"DocumentReference","rootResourceType":"DocumentReference","rowGrain":"document_reference","fields":[{"name":"id","fieldRef":"DocumentReference.id","expr":{"select":"root.id"}}]}]},"views":[{"id":"main","title":"Main","output":"DocumentReference","rowLabel":"id","table":{"columns":[{"column":"id","label":"Identifier","visible":true}]},"filters":[{"column":"id","label":"Identifier"}],"charts":[{"column":"id","type":"TABLE","title":"Identifiers"}],"fixedFilters":{"status":["active"]},"actions":[{"type":"download","title":"Download","fileName":"records.csv"}]}],"sharedFilters":{"main":[{"output":"DocumentReference","column":"id"}]},"fileActions":{"extensions":{"csv":["id"]},"actions":{"download":"csv"}}}`)
	created, err := service.CreateInteractiveV2(context.Background(), "project-a", "my-explorer", raw, "author")
	if err != nil {
		t.Fatal(err)
	}
	if string(created.DraftConfig) == string(raw) {
		t.Log("input was already canonical")
	}
	if created.DraftDigest == "" || created.DraftVersion != 1 {
		t.Fatalf("draft metadata = %#v", created)
	}
	loaded, err := service.Get(context.Background(), "project-a", "my-explorer")
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.DraftConfig) != string(created.DraftConfig) {
		t.Fatalf("draft packet changed on round trip: %s != %s", loaded.DraftConfig, created.DraftConfig)
	}
	updatedRaw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"my-explorer","title":"Updated","description":"presentation survives","management":"interactive"},"recipe":{"recipeSchemaVersion":1,"name":"explorer-my-explorer","translationVersion":"interactive","outputs":[{"name":"DocumentReference","rootResourceType":"DocumentReference","rowGrain":"document_reference","fields":[{"name":"id","fieldRef":"DocumentReference.id","expr":{"select":"root.id"}}]}]},"views":[{"id":"main","title":"Main","output":"DocumentReference","rowLabel":"id","table":{"columns":[{"column":"id","label":"Identifier","visible":true}]}}],"sharedFilters":{"main":[{"output":"DocumentReference","column":"id"}]}}`)
	_, _, _, nextDigest, err := CanonicalConfigV2(updatedRaw, "project-a", "my-explorer", "interactive")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.SaveDraftV2(context.Background(), "project-a", "my-explorer", updatedRaw, 1, created.DraftDigest, "author")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || updated.DraftDigest != nextDigest {
		t.Fatalf("updated metadata = %#v", updated)
	}
	stale, err := service.SaveDraftV2(context.Background(), "project-a", "my-explorer", raw, 1, created.DraftDigest, "stale")
	if err != nil {
		t.Fatal(err)
	}
	if stale.DraftVersion != 3 || stale.DraftDigest == updated.DraftDigest || string(stale.DraftConfig) == string(updated.DraftConfig) {
		t.Fatalf("last-write-wins draft = %#v", stale)
	}
}

func TestRepositoryConfigDoesNotFallBackToInteractiveDefault(t *testing.T) {
	store := NewMemoryStore()
	interactive := RepositoryConfig{Project: "project-a", ExplorerID: "default", Management: ManagementInteractive, SourceGeneration: "interactive-generation"}
	repository := RepositoryConfig{Project: "project-a", ExplorerID: "default", Management: ManagementRepository, SourceGeneration: "repository-generation"}
	if _, err := store.SaveConfig(context.Background(), interactive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveRepositoryConfig(context.Background(), repository); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRepositoryConfig(context.Background(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceGeneration != repository.SourceGeneration || got.Management != ManagementRepository {
		t.Fatalf("repository config = %#v", got)
	}
}
