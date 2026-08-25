package bootstrap

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestConvertDefaultIsExactAndProducesIntentOnlyBundle(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"legacy","translationVersion":"v1","outputs":[{"name":"patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","fieldRef":"Patient.id","expr":{"select":"root.id"}}]}]},"views":[{"id":"patient","title":"Patients","output":"patient","table":{"columns":[{"column":"id","label":"Patient ID","visible":true}]}}]}`)
	catalog := explorer.Catalog{Nodes: []explorer.CatalogNode{{ID: "n_patient", ResourceType: "Patient"}}, Selections: map[string]explorer.CatalogSelection{"s_patient_id": {ID: "s_patient_id", NodeID: "n_patient", FieldRef: "Patient.id", Select: "id"}}}
	bundle, report, err := ConvertDefault(raw, catalog, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Equivalent || len(report.Mappings) != 1 || len(bundle.Document.CandidateIDs) != 1 {
		t.Fatalf("report=%#v bundle=%#v", report, bundle)
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "select") || strings.Contains(string(canonical), "expr") || strings.Contains(string(canonical), "recipe") {
		t.Fatalf("legacy recipe leaked into bundle: %s", canonical)
	}
}

func TestConvertDefaultPreservesMultipleOutputsAndTabs(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"legacy","translationVersion":"v1","outputs":[{"name":"patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","fieldRef":"Patient.id","expr":{"select":"root.id"}}]},{"name":"specimen","rootResourceType":"Specimen","rowGrain":"specimen","fields":[{"name":"id","fieldRef":"Specimen.id","expr":{"select":"root.id"}}]}]},"views":[{"id":"patient","title":"Patients","output":"patient","table":{"columns":[{"column":"id","label":"Patient ID","visible":true}]}},{"id":"specimen","title":"Specimens","output":"specimen","table":{"columns":[{"column":"id","label":"Specimen ID","visible":true}]}}]}`)
	catalog := explorer.Catalog{
		Nodes: []explorer.CatalogNode{{ID: "n_patient", ResourceType: "Patient"}, {ID: "n_specimen", ResourceType: "Specimen"}},
		Selections: map[string]explorer.CatalogSelection{
			"s_patient_id":  {ID: "s_patient_id", NodeID: "n_patient", FieldRef: "Patient.id", Select: "id"},
			"s_specimen_id": {ID: "s_specimen_id", NodeID: "n_specimen", FieldRef: "Specimen.id", Select: "id"},
		},
	}
	bundle, report, err := ConvertDefault(raw, catalog, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Equivalent || len(bundle.Documents) != 2 || len(bundle.Tabs) != 2 || len(report.Mappings) != 2 {
		t.Fatalf("report=%#v bundle=%#v", report, bundle)
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "select") || strings.Contains(string(canonical), "expr") || !strings.Contains(string(canonical), `"documents"`) || !strings.Contains(string(canonical), `"tabs"`) {
		t.Fatalf("multi-output conversion leaked recipe or lost tabs: %s", canonical)
	}
}

func TestConvertDefaultFailsOnUnmappedField(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"legacy","translationVersion":"v1","outputs":[{"name":"patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"missing","expr":{"select":"root.missing"}}]}]}}`)
	catalog := explorer.Catalog{Nodes: []explorer.CatalogNode{{ID: "n_patient", ResourceType: "Patient"}}, Selections: map[string]explorer.CatalogSelection{}}
	_, report, err := ConvertDefault(raw, catalog, "project-a")
	if err == nil || len(report.Unmapped) == 0 {
		t.Fatalf("err=%v report=%#v", err, report)
	}
}
