package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func discoveryPivotDocument(t *testing.T) Document {
	t.Helper()
	document := stagedConstructionDocument()
	document.Construction.Steps = document.Construction.Steps[:1]
	step := &document.Construction.Steps[0]
	pivot := step.Operation.Pivot
	pivot.Categories = nil
	pivot.Discovery = &ConstructionPivotDiscovery{MaxCategories: 4}
	step.Outputs = []StageColumn{{ID: "group_id", Name: "group_id", Label: "Group", Type: "string"}}
	return document
}

func TestConstructionPivotDiscoveryDeclarationValidatesAndResolves(t *testing.T) {
	document := discoveryPivotDocument(t)
	if err := document.Validate(); err != nil {
		t.Fatalf("valid unresolved discovery declaration rejected: %v", err)
	}
	encoded, err := constructionWorkspace(document).CanonicalJSON()
	if err != nil {
		t.Fatalf("persist discovery declaration: %v", err)
	}
	decoded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("reload discovery declaration: %v", err)
	}
	reloaded := decoded.Documents[0].Construction.Steps[0].Operation.Pivot
	if reloaded.Discovery == nil || reloaded.Discovery.MaxCategories != 4 || len(reloaded.Categories) != 0 {
		t.Fatalf("reloaded discovery declaration = %#v", reloaded)
	}

	input, err := sourceStageColumns(document.Columns)
	if err != nil {
		t.Fatalf("source stage columns: %v", err)
	}
	resolved, err := ResolveConstructionPivotDiscovery(document.Construction.Steps[0], input, []ConstructionPivotDiscoveryCategory{
		{Key: stringScalar("A"), Name: "amount_a", Label: "Amount A"},
		{Key: stringScalar("B"), Name: "amount_b", Label: "Amount B"},
	})
	if err != nil {
		t.Fatalf("resolve discovery: %v", err)
	}
	pivot := resolved.Operation.Pivot
	if pivot.Discovery != nil {
		t.Fatalf("discovery was not cleared after resolution")
	}
	if len(pivot.Categories) != 2 || pivot.Categories[0].Key.String == nil || *pivot.Categories[0].Key.String != "A" || pivot.Categories[1].Key.String == nil || *pivot.Categories[1].Key.String != "B" {
		t.Fatalf("resolved categories = %#v", pivot.Categories)
	}
	if len(resolved.Outputs) != 3 || resolved.Outputs[1].Name != "amount_a" || resolved.Outputs[2].Name != "amount_b" {
		t.Fatalf("resolved outputs = %#v", resolved.Outputs)
	}

	document.Construction.Steps[0] = resolved
	if err := document.Validate(); err != nil {
		t.Fatalf("resolved discovery document rejected: %v", err)
	}
}

func TestConstructionPivotDiscoveryRejectsExplicitCategories(t *testing.T) {
	document := discoveryPivotDocument(t)
	pivot := document.Construction.Steps[0].Operation.Pivot
	pivot.Categories = []ConstructionPivotCategory{{Key: stringScalar("A"), OutputColumnID: "pivot_a"}}
	document.Construction.Steps[0].Outputs = append(document.Construction.Steps[0].Outputs, StageColumn{ID: "pivot_a", Name: "amount_a", Label: "Amount A", Type: "integer"})
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "explicit categories with discovery") {
		t.Fatalf("discovery plus explicit categories error = %v", err)
	}

	pivot.Categories = nil
	if _, err := ResolveConstructionPivotDiscovery(document.Construction.Steps[0], []StageColumn{
		{ID: "group_id", Name: "group_id", Label: "Group", Type: "string"},
		{ID: "category_id", Name: "category", Label: "Category", Type: "string"},
		{ID: "value_id", Name: "value", Label: "Value", Type: "integer"},
	}, []ConstructionPivotDiscoveryCategory{{Key: stringScalar("A"), Name: "amount_a"}}); err != nil {
		t.Fatalf("baseline resolver input rejected: %v", err)
	}
}

func TestConstructionPivotDiscoveryRejectsUnsupportedPairAndOverflow(t *testing.T) {
	document := discoveryPivotDocument(t)
	input := []StageColumn{
		{ID: "group_id", Name: "group_id", Label: "Group", Type: "string"},
		{ID: "category_id", Name: "category", Label: "Category", Type: "object"},
		{ID: "value_id", Name: "value", Label: "Value", Type: "integer"},
	}
	if _, err := ResolveConstructionPivotDiscovery(document.Construction.Steps[0], input, []ConstructionPivotDiscoveryCategory{{Key: stringScalar("A"), Name: "amount_a"}}); err == nil || !strings.Contains(err.Error(), "not a supported scalar pair") {
		t.Fatalf("unsupported discovery pair error = %v", err)
	}

	document = discoveryPivotDocument(t)
	document.Construction.Steps[0].Operation.Pivot.Discovery.MaxCategories = 1
	input[1].Type = "string"
	if _, err := ResolveConstructionPivotDiscovery(document.Construction.Steps[0], input, []ConstructionPivotDiscoveryCategory{
		{Key: stringScalar("A"), Name: "amount_a"},
		{Key: stringScalar("B"), Name: "amount_b"},
	}); err == nil || !strings.Contains(err.Error(), "exceeding maxCategories 1") {
		t.Fatalf("discovery overflow error = %v", err)
	}
}

func TestConstructionPivotDiscoveryOldDocumentsRemainUnchanged(t *testing.T) {
	document := stagedConstructionDocument()
	if err := document.Validate(); err != nil {
		t.Fatalf("explicit-category document rejected: %v", err)
	}
	if identity := document.Construction.Steps[0].Operation.Pivot.Categories[0].Key.identity(); identity == "" {
		t.Fatal("empty category identity")
	}
	pivot := document.Construction.Steps[0].Operation.Pivot
	if pivot.Discovery != nil {
		t.Fatal("explicit-category pivot unexpectedly carries discovery")
	}
	raw, err := json.Marshal(pivot)
	if err != nil {
		t.Fatalf("marshal explicit-category pivot: %v", err)
	}
	if strings.Contains(string(raw), `"discovery"`) {
		t.Fatalf("explicit-category pivot JSON gained a discovery field: %s", raw)
	}
}

func TestLegacyPivotMigrationKeepsExplicitCategories(t *testing.T) {
	upgraded, err := migrateStoredDocumentFixture(legacyPivotDocument())
	if err != nil {
		t.Fatalf("upgrade legacy pivot: %v", err)
	}
	pivot := upgraded.Construction.Steps[0].Operation.Pivot
	if pivot.Discovery != nil {
		t.Fatal("legacy pivot migration invented a discovery declaration")
	}
	if len(pivot.Categories) == 0 {
		t.Fatal("legacy pivot migration dropped explicit categories")
	}
}
