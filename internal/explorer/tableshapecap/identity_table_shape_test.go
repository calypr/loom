package tableshapecap

import (
	"errors"
	"strings"
	"testing"
)

func strictSubsetPivot(t *testing.T, catalog CatalogReceipt, resolution ResolutionReceipt) ResolutionReceipt {
	t.Helper()
	if resolution.Pivot == nil || len(resolution.Pivot.Categories) < 2 {
		t.Fatal("test pivot requires at least two categories")
	}
	pivot := *resolution.Pivot
	pivot.Categories = clone(pivot.Categories[:len(pivot.Categories)-1])
	subset, err := NewResolutionReceipt(catalog.Binding, catalog.ID, ResolutionPivot, &pivot, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return subset
}

func TestValidateResolutionAgainstCategoryScanRejectsUnlistedSubsetWithErrorPolicy(t *testing.T) {
	catalog := testCatalog(t, testBinding(), "")
	resolution, scan := testPivotAndScan(t, catalog)
	if err := ValidateResolutionAgainstCategoryScan(catalog, scan, resolution); err != nil {
		t.Fatalf("complete category selection rejected: %v", err)
	}
	subset := strictSubsetPivot(t, catalog, resolution)
	err := ValidateResolutionAgainstCategoryScan(catalog, scan, subset)
	if !errors.Is(err, ErrUnlistedCategorySubset) {
		t.Fatalf("strict subset error = %v, want ErrUnlistedCategorySubset", err)
	}
	if !strings.Contains(err.Error(), "include all discovered categories or choose EXCLUDE_WITH_EVIDENCE") {
		t.Fatalf("strict subset error message = %q, want category-selection guidance", err.Error())
	}
}

func TestValidateResolutionAgainstCategoryScanAllowsUnlistedSubsetWithExcludePolicy(t *testing.T) {
	base := testCatalog(t, testBinding(), "")
	choices := cloneCatalogChoices(base.Choices)
	for i := range choices.Policies {
		if choices.Policies[i].Role == RolePolicyUnlisted {
			choices.Policies[i].PolicyID = "EXCLUDE_WITH_EVIDENCE"
			break
		}
	}
	catalog, err := NewCatalogReceipt(base.Binding, base.Columns, base.Availability, choices, base.SavedShape, "")
	if err != nil {
		t.Fatal(err)
	}
	resolution, scan := testPivotAndScan(t, catalog)
	subset := strictSubsetPivot(t, catalog, resolution)
	if err := ValidateResolutionAgainstCategoryScan(catalog, scan, subset); err != nil {
		t.Fatalf("strict subset with EXCLUDE_WITH_EVIDENCE rejected: %v", err)
	}
}
