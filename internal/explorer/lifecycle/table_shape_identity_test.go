package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

func TestResolveTableShapeRejectsUnlistedSubsetWithErrorPolicy(t *testing.T) {
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	request := tableShapeCatalogRequest(store.created, snapshot)
	result, err := service.GetTableShapeCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	repository := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository)
	catalog := repository.catalogs[result.CatalogID]
	pair := TableShapeCategoryDiscoveryRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest, OutputID: request.OutputID,
		CatalogID: result.CatalogID, CategoryColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotCategory, "category"),
		ValueColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotValue, "value"),
	}
	discovery, err := service.DiscoverTableShapeCategories(context.Background(), pair)
	if err != nil {
		t.Fatal(err)
	}
	selection := TableShapePivotSelection{
		CategoryDiscoveryID:    discovery.DiscoveryID,
		GroupColumnChoiceIDs:   []string{catalogColumnChoice(t, catalog, tableshapecap.RolePivotGroup, "patient_id")},
		CategoryColumnChoiceID: pair.CategoryColumnChoiceID, ValueColumnChoiceID: pair.ValueColumnChoiceID,
		DuplicatePolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDuplicate, "ERROR"),
		MissingPolicyChoiceID:   catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyMissing, "NULL"),
		UnlistedPolicyChoiceID:  catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyUnlisted, "ERROR"),
	}
	for index, category := range discovery.Categories[:2] {
		selection.Categories = append(selection.Categories, TableShapePivotCategorySelection{
			ChoiceID: category.ID, OutputColumn: fmt.Sprintf("bucket_%d", index+1), OutputLabel: category.Label,
		})
	}
	_, err = service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: result.CatalogID, Kind: tableshapecap.ResolutionPivot, Pivot: &selection,
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("ResolveTableShape() error = %v, want lifecycle error", err)
	}
	if lifecycleErr.Class != ClassUnprocessable || lifecycleErr.Code != "TABLE_PIVOT_UNLISTED_CATEGORY" {
		t.Fatalf("ResolveTableShape() error = %#v, want unprocessable TABLE_PIVOT_UNLISTED_CATEGORY", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Message, "include all discovered categories or choose EXCLUDE_WITH_EVIDENCE") {
		t.Fatalf("message = %q, want category-selection guidance", lifecycleErr.Message)
	}
	if len(repository.resolutions) != 0 {
		t.Fatalf("strict subset with ERROR policy stored %d resolutions", len(repository.resolutions))
	}
}
