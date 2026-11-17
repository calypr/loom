package arango

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestProjectConfigSourcePublicationExpectedHeadsAndExactRetry(t *testing.T) {
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

func TestProjectConfigCatalogPublishIsStableOnRetryAndPreservesStoredDefaults(t *testing.T) {
	first := settingsAfterCatalogPublish(nil, "project-a", "recipe-a", "catalog-1")
	if first.Version != 1 || first.Catalogs["recipe-a"] != "catalog-1" {
		t.Fatalf("initial settings = %+v", first)
	}
	first.DefaultConfigID = "default"
	first.DefaultFrameID = "recipe-a/output"
	current := &projectCatalogSettingsRow{value: first}
	retry := settingsAfterCatalogPublish(current, "project-a", "recipe-a", "catalog-1")
	if retry.Version != first.Version || retry.DefaultConfigID != first.DefaultConfigID || retry.DefaultFrameID != first.DefaultFrameID {
		t.Fatalf("exact catalog retry changed settings: %+v", retry)
	}
	updated := settingsAfterCatalogPublish(current, "project-a", "recipe-a", "catalog-2")
	if updated.Version != first.Version+1 || updated.DefaultConfigID != first.DefaultConfigID || updated.DefaultFrameID != first.DefaultFrameID || updated.Catalogs["recipe-a"] != "catalog-2" {
		t.Fatalf("source publication did not preserve defaults while moving catalog: %+v", updated)
	}
}
