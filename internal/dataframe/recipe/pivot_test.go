package recipe

import (
	"encoding/json"
	"strings"
	"testing"

	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
)

func pivotDiscoveryConstruction(pivot ConstructionPivot) Construction {
	return Construction{
		Version: 1,
		SourceColumns: []StageColumn{
			{ID: "group", Name: "group", Type: "string"},
			{ID: "category", Name: "category", Type: "string"},
			{ID: "value", Name: "value", Type: "integer"},
		},
		Steps: []ConstructionStep{{
			ID:        "pivot",
			Inputs:    []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &pivot},
			Outputs:   []StageColumn{{ID: "group", Name: "group", Type: "string"}, {ID: "amount_a", Name: "amount_a", Type: "integer"}},
		}},
	}
}

func explicitPivotCategory(key string) TableScalar {
	return TableScalar{Kind: "STRING", String: &key}
}

func TestConstructionPivotExplicitCategoriesRemainByteStable(t *testing.T) {
	pivot := ConstructionPivot{
		ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
		CategoryColumnID: "category", ValueColumnID: "value",
		Categories:      []ConstructionPivotCategory{{Key: explicitPivotCategory("A"), OutputColumnID: "amount_a"}},
		DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull, UnlistedCategoryPolicy: PivotUnlistedCategoryError,
	}
	if err := pivotDiscoveryConstruction(pivot).Validate(nil); err != nil {
		t.Fatalf("legacy explicit-category pivot rejected: %v", err)
	}
	raw, err := json.Marshal(pivot)
	if err != nil {
		t.Fatalf("marshal pivot: %v", err)
	}
	want := `{"constructionId":"pivot","groupKeyIds":["group"],"categoryColumnId":"category","valueColumnId":"value","categories":[{"key":{"kind":"STRING","string":"A"},"outputColumnId":"amount_a"}],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}`
	if string(raw) != want {
		t.Fatalf("explicit-category pivot JSON changed:\n got: %s\nwant: %s", raw, want)
	}
}

func TestConstructionPivotDiscoveryCannotCompileWithFabricatedCategories(t *testing.T) {
	base := ConstructionPivot{
		ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
		CategoryColumnID: "category", ValueColumnID: "value",
		DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull, UnlistedCategoryPolicy: PivotUnlistedCategoryError,
	}

	withCategories := base
	withCategories.Discovery = &ConstructionPivotDiscovery{MaxCategories: 4}
	withCategories.Categories = []ConstructionPivotCategory{{Key: explicitPivotCategory("A"), OutputColumnID: "amount_a"}}
	if err := pivotDiscoveryConstruction(withCategories).Validate(nil); err == nil || !strings.Contains(err.Error(), "explicit categories with discovery") {
		t.Fatalf("fabricated categories with discovery error = %v", err)
	}

	unresolved := base
	unresolved.Discovery = &ConstructionPivotDiscovery{MaxCategories: 4}
	construction := pivotDiscoveryConstruction(unresolved)
	construction.Steps[0].Outputs = []StageColumn{{ID: "group", Name: "group", Type: "string"}}
	if err := construction.Validate(nil); err == nil || !strings.Contains(err.Error(), "must be resolved to concrete categories") {
		t.Fatalf("unresolved discovery compile error = %v", err)
	}

	invalidBound := base
	invalidBound.Discovery = &ConstructionPivotDiscovery{MaxCategories: maxPivotColumns + 1}
	if err := pivotDiscoveryConstruction(invalidBound).Validate(nil); err == nil || !strings.Contains(err.Error(), "maxCategories must be 1..") {
		t.Fatalf("invalid discovery bound error = %v", err)
	}
}

func TestCorrelatedCategoryRecipeRequiresExplicitScopeAndOneColumn(t *testing.T) {
	pivot := Pivot{
		Name: "diagnosis", Columns: []string{"category"}, ProjectionMode: "DISTINCT", CorrelationSystem: "urn:test",
		Correlation: &fhirbinding.CorrelatedBinding{
			KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", VersionPath: "version",
			ValueScope: fhirbinding.CorrelatedValueScopeCoding, SelectionScope: fhirbinding.CorrelatedSelectionScopeSystemVersion,
			ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
		},
	}
	encoded, err := json.Marshal(pivot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Pivot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	budget := 0
	if err := decoded.validateAt("pivot", &budget); err != nil {
		t.Fatalf("category recipe rejected: %v", err)
	}
	if decoded.Correlation == nil || decoded.Correlation.SelectionScope != fhirbinding.CorrelatedSelectionScopeSystemVersion || decoded.CorrelationCode != "" {
		t.Fatalf("category recipe round-trip = %#v", decoded)
	}
	for name, mutate := range map[string]func(*Pivot){
		"implicit blank code": func(p *Pivot) { p.Correlation.SelectionScope = "" },
		"selected code":       func(p *Pivot) { p.CorrelationCode = "normal" },
		"multiple columns":    func(p *Pivot) { p.Columns = []string{"first", "second"} },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := decoded
			binding := *decoded.Correlation
			invalid.Correlation = &binding
			mutate(&invalid)
			budget := 0
			if err := invalid.validateAt("pivot", &budget); err == nil {
				t.Fatalf("invalid category recipe accepted: %#v", invalid)
			}
		})
	}
	bundle := Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion, Name: "category", TranslationVersion: "test",
		Outputs: []Output{{Name: "conditions", RootResourceType: "Condition", RowGrain: "condition", OwnerRecords: []OwnerRecordProjection{{
			Name: "records", Binding: *decoded.Correlation, Key: fhirbinding.CorrelatedKey{System: "urn:test", Code: "normal"},
		}}}},
	}
	if err := bundle.Validate(); err == nil || !strings.Contains(err.Error(), "SYSTEM_VERSION") {
		t.Fatalf("owner records category error = %v", err)
	}
}
