package arango

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestViewBuilderCatalogSaveDefaultsDeleteAndSourceRebindAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "view-builder-storage"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")
	legacy := projectConfigPublishRequest(t, ctx, client, generations, "view-builder-adopt", "legacy-generation", "legacy-execution", bundle,
		"legacy-commit", 0,
		projectConfigPublication(t, "view-builder-adopt", "default", "recipe-a", "Legacy default", "legacy-revision", "legacy-generation", "legacy-execution", bundle),
	)
	if _, err := explorers.PublishProjectConfigs(ctx, legacy); err != nil {
		t.Fatalf("seed pre-View-Builder project: %v", err)
	}
	legacySettings, err := explorers.GetViewBuilderSettings(ctx, "view-builder-adopt")
	if err != nil || legacySettings.Version != 0 || len(legacySettings.Catalogs) != 0 {
		t.Fatalf("read of unadopted project settings = %+v, %v; want read-only version 0", legacySettings, err)
	}
	if err := explorers.AdoptViewBuilderCatalogs(ctx, "view-builder-adopt"); err != nil {
		t.Fatalf("adopt existing published revision: %v", err)
	}
	legacySettings, err = explorers.GetViewBuilderSettings(ctx, "view-builder-adopt")
	if err != nil || legacySettings.Version != 1 || legacySettings.DefaultConfigID != "default" || legacySettings.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("adopted project settings = %+v, %v", legacySettings, err)
	}
	legacySnapshot, err := explorers.GetRevision(ctx, legacySettings.Catalogs["recipe-a"])
	if err != nil || legacySnapshot.ExplorerID != "" || legacySnapshot.Status != explorer.RevisionReady || len(legacySnapshot.Config) != 0 || legacySnapshot.Publication.ExecutionID != "legacy-execution" {
		t.Fatalf("adopted metadata-only snapshot = %+v, %v", legacySnapshot, err)
	}
	if err := explorers.AdoptViewBuilderCatalogs(ctx, "view-builder-adopt"); err != nil {
		t.Fatalf("repeat existing-project adoption: %v", err)
	}
	repeatedLegacySettings, err := explorers.GetViewBuilderSettings(ctx, "view-builder-adopt")
	if err != nil || repeatedLegacySettings.Version != legacySettings.Version || repeatedLegacySettings.Catalogs["recipe-a"] != legacySettings.Catalogs["recipe-a"] {
		t.Fatalf("repeat adoption changed settings: %+v, %v", repeatedLegacySettings, err)
	}

	first := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle,
		"commit-1", 0,
		projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-1-default", "generation-1", "execution-1", bundle),
		projectConfigPublication(t, project, "files", "recipe-a", "Files", "revision-1-files", "generation-1", "execution-1", bundle),
	)
	first.ExpectedConfigRevisionIDs = map[string]string{"default": "", "files": ""}
	first.CatalogSnapshot = viewBuilderTestCatalogSnapshot(t, first.Configs[0])
	active, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("publish source with catalog snapshot: %v", err)
	}
	if active.Revision != 1 {
		t.Fatalf("first release revision = %d, want 1", active.Revision)
	}

	settings, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Version != 1 || settings.Catalogs["recipe-a"] != first.CatalogSnapshot.ID {
		t.Fatalf("published catalog settings = %+v", settings)
	}
	catalogSnapshot, err := explorers.GetRevision(ctx, settings.Catalogs["recipe-a"])
	if err != nil || catalogSnapshot.ExplorerID != "" || catalogSnapshot.Status != explorer.RevisionReady || len(catalogSnapshot.Config) != 0 {
		t.Fatalf("stored metadata-only catalog snapshot = %+v, %v", catalogSnapshot, err)
	}

	if _, err := explorers.PublishProjectConfigs(ctx, first); err != nil {
		t.Fatalf("retry exact source publication after lost acknowledgement: %v", err)
	}
	retriedSettings, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || retriedSettings.Version != settings.Version || retriedSettings.Catalogs["recipe-a"] != settings.Catalogs["recipe-a"] {
		t.Fatalf("source retry changed settings: %+v, %v", retriedSettings, err)
	}

	if err := explorers.SaveViewBuilderDefaults(ctx, project, explorer.ViewBuilderDefaultsRequest{
		ExpectedVersion: settings.Version, DefaultConfigID: "default", DefaultFrameID: explorer.ViewBuilderFrameID("recipe-a", "output"),
	}); err != nil {
		t.Fatalf("save explicit project default: %v", err)
	}
	settings, err = explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Version != 2 || settings.DefaultConfigID != "default" || settings.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("saved defaults = %+v, %v", settings, err)
	}

	updatedDefinition := projectConfigTestDefinition("default", "recipe-a", "Edited Default")
	prepared, err := explorer.PrepareViewBuilderConfig(updatedDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatalf("prepare view edit against exact catalog snapshot: %v", err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: "revision-1-default", CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: prepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("save view edit: %v", err)
	}
	differentDefinition := projectConfigTestDefinition("default", "recipe-a", "A different title")
	alternate, err := explorer.PrepareViewBuilderConfig(differentDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: prepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: alternate, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("save alternate title: %v", err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: alternate.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: prepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("restore prior valid view revision after A-B-A: %v", err)
	}
	beforeNoop, err := explorers.GetRevision(ctx, prepared.Revision.ID)
	if err != nil || beforeNoop.ActivatedAt == nil {
		t.Fatalf("read reactivated view revision before exact save retry: %+v, %v", beforeNoop, err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: prepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: prepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("retry the now-active exact view save: %v", err)
	}
	afterNoop, err := explorers.GetRevision(ctx, prepared.Revision.ID)
	if err != nil || afterNoop.ActivatedAt == nil || !afterNoop.ActivatedAt.Equal(*beforeNoop.ActivatedAt) {
		t.Fatalf("exact save retry changed active revision metadata: before=%+v after=%+v err=%v", beforeNoop.ActivatedAt, afterNoop.ActivatedAt, err)
	}
	settingsAfterEdit, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settingsAfterEdit.Version != settings.Version || settingsAfterEdit.DefaultConfigID != "default" || settingsAfterEdit.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("view save changed project defaults/catalog version: %+v, %v", settingsAfterEdit, err)
	}
	owner, err := explorers.Get(ctx, project, "default")
	if err != nil || owner.ActiveRevisionID != prepared.Revision.ID || owner.ProjectConfig.Title != "Edited Default" {
		t.Fatalf("saved view owner = %+v, %v", owner, err)
	}

	filesDefinition := projectConfigTestDefinition("files", "recipe-a", "Edited Files")
	filesPrepared, err := explorer.PrepareViewBuilderConfig(filesDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatalf("prepare second view edit: %v", err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: "revision-1-files", CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: filesPrepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("save second view edit: %v", err)
	}
	changedImmutable := filesPrepared
	changedImmutable.Revision.PublicOutputContract = []byte(`{"outputs":[]}`)
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: filesPrepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: changedImmutable, Actor: "editor@example.test",
	}); !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("changed immutable revision error = %v, want ErrImmutableRevision", err)
	}

	badDefinition := updatedDefinition
	badDefinition.Views[0].Output = "removed-output"
	badPublication := prepared
	badPublication.Definition = badDefinition
	badPublication.Revision.IntentDigest, err = badDefinition.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: prepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"], Publication: badPublication,
	}); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("removing the current default frame error = %v, want ErrDraftConflict", err)
	}
	if err := explorers.DeleteViewBuilderConfig(ctx, project, "default", prepared.Revision.ID); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("deleting the current default error = %v, want ErrDraftConflict", err)
	}

	if err := explorers.SaveViewBuilderDefaults(ctx, project, explorer.ViewBuilderDefaultsRequest{
		ExpectedVersion: settings.Version, DefaultConfigID: "files", DefaultFrameID: explorer.ViewBuilderFrameID("recipe-a", "output"),
	}); err != nil {
		t.Fatalf("move default to second saved config: %v", err)
	}
	if err := explorers.DeleteViewBuilderConfig(ctx, project, "default", prepared.Revision.ID); err != nil {
		t.Fatalf("delete nondefault view while retaining catalog: %v", err)
	}
	if _, err := explorers.Get(ctx, project, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("deleted config owner read error = %v, want ErrNotFound", err)
	}
	deletedRevision, err := explorers.GetRevision(ctx, prepared.Revision.ID)
	if err != nil || deletedRevision.Status != explorer.RevisionSuperseded {
		t.Fatalf("deleted view history = %+v, %v; want retained superseded revision", deletedRevision, err)
	}
	settings, err = explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Catalogs["recipe-a"] != first.CatalogSnapshot.ID || settings.DefaultConfigID != "files" {
		t.Fatalf("deleting a view changed its independent catalog/default: %+v, %v", settings, err)
	}
	if err := explorers.AdoptViewBuilderCatalogs(ctx, project); err != nil {
		t.Fatalf("idempotent adoption after deletion: %v", err)
	}
	if _, err := explorers.Get(ctx, project, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("adoption recreated deleted view: %v", err)
	}

	secondPublication := projectConfigPublication(t, project, "files", "recipe-a", "Edited Files", "revision-2-files", "generation-2", "execution-2", bundle)
	second := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-2", "execution-2", bundle,
		"commit-2", active.Revision, secondPublication)
	second.ExpectedConfigRevisionIDs = map[string]string{"files": filesPrepared.Revision.ID}
	second.CatalogSnapshot = viewBuilderTestCatalogSnapshot(t, secondPublication)
	active, err = explorers.PublishProjectConfigs(ctx, second)
	if err != nil {
		t.Fatalf("rebind remaining views to second execution: %v", err)
	}
	settings, err = explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Catalogs["recipe-a"] != second.CatalogSnapshot.ID || settings.DefaultConfigID != "files" || settings.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("source rebind did not preserve defaults/update catalog: %+v, %v", settings, err)
	}
	if _, err := explorers.Get(ctx, project, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("source publication recreated a deleted view: %v", err)
	}
	reboundFiles, err := explorers.Get(ctx, project, "files")
	if err != nil || reboundFiles.ActiveRevisionID != "revision-2-files" || reboundFiles.ProjectConfig.Title != "Edited Files" {
		t.Fatalf("source publication did not preserve the saved view definition: %+v, %v", reboundFiles, err)
	}

	staleSave, err := explorer.PrepareViewBuilderConfig(filesDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: "revision-2-files", CatalogRevisionID: first.CatalogSnapshot.ID, Publication: staleSave,
	}); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale catalog save error = %v, want ErrDraftConflict", err)
	}

	thirdPublication := projectConfigPublication(t, project, "files", "recipe-a", "Edited Files", "revision-3-files", "generation-3", "execution-3", bundle)
	third := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-3", "execution-3", bundle,
		"commit-3", active.Revision, thirdPublication)
	third.ExpectedConfigRevisionIDs = map[string]string{"files": "revision-1-files"} // stale head
	third.CatalogSnapshot = viewBuilderTestCatalogSnapshot(t, thirdPublication)
	if _, err := explorers.PublishProjectConfigs(ctx, third); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale source owner head error = %v, want ErrDraftConflict", err)
	}
	if _, err := explorers.GetRevision(ctx, thirdPublication.Revision.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("stale source publication left a candidate revision: %v", err)
	}
}

func viewBuilderTestCatalogSnapshot(t *testing.T, publication explorer.ProjectConfigPublication) *explorer.Revision {
	t.Helper()
	snapshot := adoptedCatalogSnapshot(publication.Revision, publication.Definition.RecipeRef.Name)
	return &snapshot
}
