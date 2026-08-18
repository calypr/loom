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
