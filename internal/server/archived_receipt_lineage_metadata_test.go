package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestArchivedReceiptRetainsLegacyImmediateInputMetadata(t *testing.T) {

	raw := []byte(`{"recipeSchemaVersion":1,"name":"archived-derived","translationVersion":"v1","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"patient_count","expr":{"literal":1}}],"derivedColumns":[{"constructionId":"scale_patient_count","name":"scaled_patient_count","label":"Scaled count","operation":"MULTIPLY","left":{"kind":"COLUMN","column":"patient_count"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":2}},"missingInputPolicy":"ERROR"},{"constructionId":"add_one","name":"doubled_patient_count","label":"Doubled count","operation":"ADD","left":{"kind":"COLUMN","column":"scaled_patient_count"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":1}},"missingInputPolicy":"ERROR"}]}]}`)
	var bundle recipe.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	digest, err := bundle.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{Project: "project-a", ExplorerID: "historical", Bundle: bundle, RecipeDigest: digest, EmittedColumns: []explorer.EmittedColumn{{OutputID: "patients", ConstructionID: "add_one", PublicColumn: "doubled_patient_count", InputColumns: []string{"scaled_patient_count"}, AuthoredColumns: []string{"patient_count"}}}}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	originalID := receipt.ID
	before, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err = json.Unmarshal(before, &stored); err != nil {
		t.Fatal(err)
	}
	if err = stored.ValidateArchive(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.EmittedColumns[0].InputColumns, []string{"scaled_patient_count"}) || !reflect.DeepEqual(stored.EmittedColumns[0].AuthoredColumns, []string{"patient_count"}) {
		t.Fatalf("archive reinterpreted old lineage: %+v", stored.EmittedColumns[0])
	}
	after, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || stored.ID != originalID {
		t.Fatal("archive authentication changed immutable receipt metadata/identity")
	}
	if explorer.ReceiptContractSupportedForExecution(stored) {
		t.Fatal("historical address-only receipt became executable")
	}
}
