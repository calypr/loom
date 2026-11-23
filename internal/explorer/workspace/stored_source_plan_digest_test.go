package workspace

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestStoredSourcePlanMigrationVerifiesLegacyDigestBeforeRemovingRules(t *testing.T) {
	document := workspaceDocument("patients")
	document.ImportedRecipe = &ImportedRecipeRef{Name: "pinned", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
	document.ImportedSource = &ImportedSourcePlan{Steps: []ImportedSourceStep{
		{ID: "output", Kind: "ROOT", Title: "Output", Summary: "Output"},
		{ID: "dynamicColumns[0]", Kind: "DYNAMIC_COLUMNS", Title: "Identifiers", Summary: "Identifiers", Editable: true},
	}}
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion,
		Explorer: ExplorerMetadata{Title: "Legacy source"}, Documents: []Document{document},
		Tabs: []Tab{{ID: "patients", OutputID: "patients", Title: "Patients", Visible: true}},
	}
	current, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bytes.Replace(current, []byte(`"summary":"Output","editable":false`), []byte(`"summary":"Output","enabled":true,"editable":false`), 1)
	raw = bytes.Replace(raw, []byte(`"summary":"Identifiers","editable":true`), []byte(`"summary":"Identifiers","enabled":false,"editable":true`), 1)
	oldDigest := workspaceDigest(raw)

	converted, changed, err := ConvertStoredSourcePlan(raw, oldDigest)
	if err != nil || !changed {
		t.Fatalf("convert legacy source plan: changed=%v, err=%v", changed, err)
	}
	plan := converted.Documents[0].ImportedSource
	if len(plan.Steps) != 1 || plan.Steps[0].ID != "output" || !slices.Equal(plan.RemovedStepIDs, []string{"dynamicColumns[0]"}) {
		t.Fatalf("unexpected converted source plan: %+v", plan)
	}
	if _, _, err := ConvertStoredSourcePlan(bytes.Replace(raw, []byte(`"enabled":false`), []byte(`"enabled":true`), 1), oldDigest); err == nil {
		t.Fatal("migration accepted a draft that no longer matched the stored digest")
	}
	migration, err := MigrateStoredWorkspace(raw, CatalogSnapshot{})
	if err != nil || !slices.Contains(migration.AcceptedOriginalDigests, oldDigest) || bytes.Contains(migration.CanonicalJSON, []byte(`"enabled"`)) {
		t.Fatalf("legacy source not verified and converted by workspace migration: %v", err)
	}
	if _, changed, err := ConvertStoredSourcePlan(migration.CanonicalJSON, migration.MigratedCanonicalDigest); err != nil || changed {
		t.Fatalf("second conversion: changed=%v, err=%v", changed, err)
	}
}
