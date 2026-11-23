package arango

import (
	"github.com/calypr/loom/internal/explorer/arango/db"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestProjectConfigCatalogAnchorPublishRetryAndRebindAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "project-config-catalog-anchor"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")
	firstPublication := projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-1", "generation-1", "execution-1", bundle)
	first := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle, "commit-1", 0, firstPublication)
	first.ExpectedConfigRevisionIDs = map[string]string{"default": ""}
	first.CatalogSnapshot = projectConfigCatalogSnapshotForTest(t, firstPublication)
	active, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("publish source and catalog anchor: %v", err)
	}
	settings, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Version != 1 || settings.Catalogs["recipe-a"] != first.CatalogSnapshot.ID {
		t.Fatalf("published anchor=%+v, err=%v", settings, err)
	}
	stored, err := explorers.GetRevision(ctx, settings.Catalogs["recipe-a"])
	if err != nil || stored.ExplorerID != "" || stored.Status != explorer.RevisionReady || len(stored.Config) != 0 || stored.Publication.ExecutionID != "execution-1" {
		t.Fatalf("stored source catalog=%+v, err=%v", stored, err)
	}
	if _, err := explorers.PublishProjectConfigs(ctx, first); err != nil {
		t.Fatalf("retry exact source publication: %v", err)
	}
	retried, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || retried.Version != settings.Version || retried.Catalogs["recipe-a"] != settings.Catalogs["recipe-a"] {
		t.Fatalf("exact retry changed catalog anchor: %+v, err=%v", retried, err)
	}
	secondPublication := projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-2", "generation-2", "execution-2", bundle)
	second := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-2", "execution-2", bundle, "commit-2", active.Revision, secondPublication)
	second.ExpectedConfigRevisionIDs = map[string]string{"default": "revision-1"}
	second.CatalogSnapshot = projectConfigCatalogSnapshotForTest(t, secondPublication)
	if _, err := explorers.PublishProjectConfigs(ctx, second); err != nil {
		t.Fatalf("rebind catalog to new source execution: %v", err)
	}
	updated, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || updated.Version != settings.Version+1 || updated.Catalogs["recipe-a"] != second.CatalogSnapshot.ID {
		t.Fatalf("source rebind did not advance catalog anchor: %+v, err=%v", updated, err)
	}

}

func projectConfigCatalogSnapshotForTest(t *testing.T, publication explorer.ProjectConfigPublication) *explorer.Revision {
	t.Helper()
	source := publication.Revision
	id := "catalog_" + db.Key(source.Project, publication.Definition.RecipeRef.Name, source.Publication.ExecutionID, source.RecipeDigest, source.Dataset.SchemaDigest, string(source.PublicOutputContract))
	snapshot := source
	snapshot.ID = id
	snapshot.ExplorerID = ""
	snapshot.Config = nil
	snapshot.AuthoringBundle = nil
	snapshot.IntentDigest = ""
	snapshot.CompilationReceiptID = ""
	snapshot.CreatedBy = ""
	snapshot.Diagnostics = []explorer.Diagnostic{}
	snapshot.Status = explorer.RevisionReady
	snapshot.ActivatedAt = nil
	snapshot.FailedAt = nil
	snapshot.Publication.State = string(explorer.RevisionReady)
	snapshot.Publication.RevisionID = id
	return &snapshot
}
