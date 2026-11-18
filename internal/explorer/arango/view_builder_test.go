package arango

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	publicationdata "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestAdoptedCatalogSnapshotIsDetachedAndKeepsExactPublishedMetadata(t *testing.T) {
	view := projectConfigPublication(t, "adopt-project", "config-a", "recipe-a", "A", "view-revision", "generation-a", "execution-a", projectConfigTestBundle("recipe-a", "translation-1")).Revision
	snapshot := adoptedCatalogSnapshot(view, "recipe-a")
	if snapshot.ID == view.ID || !strings.HasPrefix(snapshot.ID, "catalog_") {
		t.Fatalf("snapshot ID = %q, want independent catalog_ identity", snapshot.ID)
	}
	if snapshot.ExplorerID != "" || snapshot.Status != explorer.RevisionReady || len(snapshot.Config) != 0 || len(snapshot.AuthoringBundle) != 0 || snapshot.IntentDigest != "" {
		t.Fatalf("catalog snapshot retains view ownership/content: %+v", snapshot)
	}
	if snapshot.Project != view.Project || snapshot.SourceGeneration != view.SourceGeneration || snapshot.RecipeDigest != view.RecipeDigest ||
		snapshot.Publication.ExecutionID != view.Publication.ExecutionID || snapshot.Dataset.SchemaDigest != view.Dataset.SchemaDigest ||
		!reflect.DeepEqual(snapshot.Recipe, view.Recipe) || !reflect.DeepEqual(snapshot.PublicOutputContract, view.PublicOutputContract) ||
		!reflect.DeepEqual(snapshot.EmittedColumns, view.EmittedColumns) || !reflect.DeepEqual(snapshot.Materializations, view.Materializations) ||
		!reflect.DeepEqual(snapshot.Dataset, view.Dataset) || !reflect.DeepEqual(snapshot.QualityReports, view.QualityReports) {
		t.Fatal("catalog snapshot changed immutable source publication metadata")
	}
	if snapshot.Publication.State != string(explorer.RevisionReady) || snapshot.Publication.RevisionID != snapshot.ID {
		t.Fatalf("snapshot publication identity = %+v", snapshot.Publication)
	}
}

func TestViewBuilderSourcePublicationExpectedHeadsAndExactRetry(t *testing.T) {
	definition := projectConfigTestDefinition("default", "recipe-a", "Default")
	owner := projectConfigOwner{value: explorer.Explorer{
		Project: "project-a", ExplorerID: "default", ManagementMode: explorer.ManagementRepository,
		ProjectConfig: &definition, ActiveRevisionID: "revision-old",
	}}
	publication := explorer.ProjectConfigPublication{Definition: definition, Revision: explorer.Revision{ID: "revision-new"}}
	request := explorer.ProjectConfigPublishRequest{
		RecipeName: "recipe-a", Configs: []explorer.ProjectConfigPublication{publication},
		ExpectedConfigRevisionIDs: map[string]string{"default": "revision-old"},
	}
	owners := map[string]projectConfigOwner{"default": owner}
	if err := validateExpectedProjectConfigHeads(request, "project-a", owners); err != nil {
		t.Fatalf("matching owner head rejected: %v", err)
	}
	request.ExpectedConfigRevisionIDs["default"] = "stale-head"
	if err := validateExpectedProjectConfigHeads(request, "project-a", owners); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale source head error = %v, want ErrDraftConflict", err)
	}
	request.ExpectedConfigRevisionIDs["default"] = "revision-old"
	owners["default"] = projectConfigOwner{value: explorer.Explorer{
		Project: "project-a", ExplorerID: "default", ManagementMode: explorer.ManagementRepository,
		ProjectConfig: &definition, ActiveRevisionID: "revision-new",
	}}
	if err := validateExpectedProjectConfigHeads(request, "project-a", owners); err != nil {
		t.Fatalf("exact source retry after lost acknowledgement rejected: %v", err)
	}
	request.Configs = nil
	if err := validateExpectedProjectConfigHeads(request, "project-a", owners); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("omitted saved config error = %v, want ErrDraftConflict", err)
	}
}

func TestSettingsCatalogPublishIsStableOnRetryAndPreservesDefaults(t *testing.T) {
	first := settingsAfterCatalogPublish(nil, "project-a", "recipe-a", "catalog-1")
	if first.Version != 1 || first.Catalogs["recipe-a"] != "catalog-1" {
		t.Fatalf("initial settings = %+v", first)
	}
	first.DefaultConfigID = "default"
	first.DefaultFrameID = explorer.ViewBuilderFrameID("recipe-a", "output")
	current := &viewBuilderSettingsRow{value: first}
	retry := settingsAfterCatalogPublish(current, "project-a", "recipe-a", "catalog-1")
	if retry.Version != first.Version || retry.DefaultConfigID != first.DefaultConfigID || retry.DefaultFrameID != first.DefaultFrameID {
		t.Fatalf("exact catalog retry changed settings: %+v", retry)
	}
	updated := settingsAfterCatalogPublish(current, "project-a", "recipe-a", "catalog-2")
	if updated.Version != first.Version+1 || updated.DefaultConfigID != first.DefaultConfigID || updated.DefaultFrameID != first.DefaultFrameID || updated.Catalogs["recipe-a"] != "catalog-2" {
		t.Fatalf("source publication did not preserve defaults while moving catalog: %+v", updated)
	}
}

func TestViewBuilderFrameIDsAreRecipeQualified(t *testing.T) {
	recipeName, output, ok := parseViewBuilderFrameID(explorer.ViewBuilderFrameID("recipe-a", "output"))
	if !ok || recipeName != "recipe-a" || output != "output" {
		t.Fatalf("frame parser = %q/%q/%v", recipeName, output, ok)
	}
	for _, invalid := range []string{"output", "recipe-a/", "/output", " recipe-a/output", "recipe-a/output/extra"} {
		if _, _, ok := parseViewBuilderFrameID(invalid); ok {
			t.Errorf("accepted invalid frame ID %q", invalid)
		}
	}
}

func TestViewBuilderDefaultRejectsRetargetingOrRemovingItsFrame(t *testing.T) {
	settings := explorer.ViewBuilderSettings{DefaultConfigID: "default", DefaultFrameID: "recipe-a/output"}
	valid := projectConfigTestDefinition("default", "recipe-a", "Default")
	if err := validateViewBuilderDefaultBinding(settings, valid); err != nil {
		t.Fatalf("valid saved default rejected: %v", err)
	}
	retargeted := projectConfigTestDefinition("default", "recipe-b", "Default")
	if err := validateViewBuilderDefaultBinding(settings, retargeted); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("same-output cross-recipe retarget error = %v, want ErrDraftConflict", err)
	}
	missing := projectConfigTestDefinition("default", "recipe-a", "Default")
	missing.Views[0].Output = "other-output"
	if err := validateViewBuilderDefaultBinding(settings, missing); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("removing selected output error = %v, want ErrDraftConflict", err)
	}
}

func TestProjectRecipeSnapshotMatchesViewFromSameExecution(t *testing.T) {
	snapshot := explorer.Revision{
		ID: "catalog-a", Project: "project-a", SourceGeneration: "generation-a", RecipeDigest: "digest", Dataset: explorer.DatasetMetadata{SchemaDigest: "schema"},
		Recipe: recipe.Bundle{Name: "recipe-a"}, Publication: explorer.PublicationMetadata{ExecutionID: "execution-a"},
	}
	view := snapshot
	view.ExplorerID = "default"
	if !sameProjectRecipeSnapshot(snapshot, view) {
		t.Fatal("view with exact recipe execution was not accepted")
	}
	view.Publication.ExecutionID = "execution-b"
	if sameProjectRecipeSnapshot(snapshot, view) {
		t.Fatal("view from another execution matched catalog snapshot")
	}
}

func TestCatalogSnapshotDoesNotChangeDynamicOrQualityEvidence(t *testing.T) {
	readyAt := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	revision := explorer.Revision{
		Project: "project-a", ExplorerID: "old-view", SourceGeneration: "generation-a", RecipeDigest: "digest",
		Recipe: recipe.Bundle{Name: "recipe-a"}, Dataset: explorer.DatasetMetadata{SchemaDigest: "schema"},
		PublicOutputContract: []byte(`{"outputs":[{"outputId":"output"}]}`),
		EmittedColumns:       []explorer.EmittedColumn{{EmissionID: "dynamic:id", OutputID: "output", PublicColumn: "id", Label: "ID", LogicalType: "string", Cardinality: "optional_one"}},
		Materializations:     []explorer.Materialization{{OutputID: "output", MaterializationID: "execution-a"}},
		QualityReports:       []publicationdata.QualityReport{{ID: "quality-a", Verdict: publicationdata.QualityPassed}},
		Publication:          explorer.PublicationMetadata{ExecutionID: "execution-a", Generation: "generation-a"}, ReadyAt: &readyAt,
	}
	snapshot := adoptedCatalogSnapshot(revision, "recipe-a")
	if len(snapshot.EmittedColumns) != 1 || snapshot.EmittedColumns[0].EmissionID != "dynamic:id" || len(snapshot.QualityReports) != 1 || snapshot.QualityReports[0].ID != "quality-a" {
		t.Fatalf("catalog snapshot lost dynamic/quality evidence: %+v", snapshot)
	}
}
