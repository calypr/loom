package explorer

import "testing"

func TestDecodeDefaultConfigV2AcceptsExecutableRecipeWithoutPresentation(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"repository","translationVersion":"v1","outputs":[{"name":"Patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}]}]}}`)
	cfg, bundle, err := DecodeDefaultConfigV2(raw, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Views) != 0 || bundle.Outputs[0].Name != "Patient" {
		t.Fatalf("decoded %#v %#v", cfg, bundle)
	}
}

func TestDecodeInteractiveConfigV2AcceptsPresentation(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"custom","title":"Custom","management":"interactive"},"recipe":{"recipeSchemaVersion":1,"name":"repository","translationVersion":"v1","outputs":[{"name":"Patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}]}]},"views":[{"id":"patient","title":"Patients","output":"Patient","table":{"columns":[{"column":"id","visible":true}]}}]}`)
	cfg, bundle, err := DecodeInteractiveConfigV2(raw, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Views[0].Output != "Patient" || bundle.Outputs[0].Name != "Patient" {
		t.Fatalf("decoded %#v %#v", cfg, bundle)
	}
}

func TestDecodeDefaultConfigV2AcceptsCompletePresentation(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"repository","translationVersion":"v1","outputs":[{"name":"Patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}]}]},"views":[{"id":"patient","title":"Patients","output":"Patient","table":{"columns":[{"column":"id","visible":true}]}}],"sharedFilters":{"patient":[{"output":"Patient","column":"id"}]}}`)
	cfg, bundle, err := DecodeDefaultConfigV2(raw, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Views) != 1 || len(cfg.SharedFilters["patient"]) != 1 || bundle.Outputs[0].Name != "Patient" {
		t.Fatalf("decoded %#v %#v", cfg, bundle)
	}
}

func TestDecodeConfigV2RejectsLegacyAndUnknownFields(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"explorerConfig":[]}`),
		[]byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{},"views":[],"tabs":[]}`),
		[]byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"repository","translationVersion":"v1","outputs":[]},"sharedFilters":{"patient":[{"output":"Patient","column":"id"}]}}`),
	} {
		if _, _, err := DecodeConfigV2(raw, "project-a"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestRepairConfigV2PresentationRemovesStaleReferences(t *testing.T) {
	cfg := ConfigV2{
		APIVersion: ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   ConfigExplorer{ID: "custom", Title: "Custom", Management: "interactive"},
		Views: []ConfigView{{
			ID: "patient", Title: "Patients", Output: "Patient", RowLabel: "stale",
			Table:        ConfigTable{Columns: []ConfigColumn{{Column: "id"}, {Column: "stale"}}},
			Filters:      []ConfigFilter{{Column: "id"}, {Column: "stale"}},
			Charts:       []ConfigChart{{Column: "id"}, {Column: "stale"}},
			FixedFilters: map[string][]string{"id": {"x"}, "stale": {"x"}},
			Actions:      []ConfigAction{{Type: "download", Title: "Download", Columns: []string{"id", "stale"}}},
		}},
		SharedFilters: map[string][]SharedFilter{"patient": {{Output: "Patient", Column: "id"}, {Output: "Patient", Column: "stale"}}},
		FileActions:   FileActions{Extensions: map[string][]string{"csv": {"id", "stale"}}},
	}
	repaired, diagnostics, err := RepairConfigV2Presentation(cfg, map[string]map[string]bool{"Patient": {"id": true}})
	if err != nil {
		t.Fatal(err)
	}
	view := repaired.Views[0]
	if len(view.Table.Columns) != 1 || len(view.Filters) != 1 || len(view.Charts) != 1 || view.RowLabel != "" {
		t.Fatalf("stale view references were not repaired: %#v", view)
	}
	if _, ok := view.FixedFilters["stale"]; ok || len(view.Actions[0].Columns) != 1 || len(repaired.SharedFilters["patient"]) != 1 || len(repaired.FileActions.Extensions["csv"]) != 1 {
		t.Fatalf("stale secondary references were not repaired: %#v %#v %#v", view, repaired.SharedFilters, repaired.FileActions)
	}
	if len(diagnostics) != 8 {
		t.Fatalf("diagnostics = %d, want 8: %#v", len(diagnostics), diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != "STALE_PRESENTATION_REFERENCE" || diagnostic.Severity != "WARN" {
			t.Fatalf("unexpected diagnostic: %#v", diagnostic)
		}
	}
}
