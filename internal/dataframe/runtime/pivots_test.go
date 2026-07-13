package runtime

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestFillPivotColumnsUsesBoundedCatalogValues(t *testing.T) {
	resolved, err := fillPivotColumns([]PivotSelect{{
		Name: "lab_values", ColumnSelect: "code.coding[].display", ValueSelect: "valueQuantity.value",
	}}, []catalog.PopulatedField{{
		ResourceType: "Observation", Path: "code", PivotCandidate: true,
		PivotColumns: []string{"Hemoglobin", "Platelets"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || !reflect.DeepEqual(resolved[0].Columns, []string{"Hemoglobin", "Platelets"}) {
		t.Fatalf("resolved pivots = %#v", resolved)
	}
	if resolved[0].PivotFamily == "" {
		t.Fatalf("expected generated pivot family, got %#v", resolved[0])
	}
}

func TestFillPivotColumnsRejectsUnboundedDiscovery(t *testing.T) {
	_, err := fillPivotColumns([]PivotSelect{{
		Name: "lab_values", ColumnSelect: "code.coding[].display", ValueSelect: "valueQuantity.value",
	}}, []catalog.PopulatedField{{ResourceType: "Observation", Path: "code", PivotCandidate: true}})
	if err == nil || !strings.Contains(err.Error(), "bounded catalog columns") {
		t.Fatalf("error = %v, want bounded catalog column error", err)
	}
}

func TestBuildSemanticPlanRejectsUnresolvedPivotDiscovery(t *testing.T) {
	_, err := BuildSemanticPlan(Builder{
		Project: "P1", RootResourceType: "Observation",
		Pivots: []PivotSelect{{Name: "lab_values", ColumnSelect: "code.coding[].display", ValueSelect: "valueQuantity.value"}},
	})
	if err == nil || !strings.Contains(err.Error(), "bounded columns") {
		t.Fatalf("error = %v, want bounded pivot error", err)
	}
}
