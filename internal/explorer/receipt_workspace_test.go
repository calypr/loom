package explorer

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestHistoricalReceiptIdentitySurvivesExecutionRetirement(t *testing.T) {
	workspace := authoringv2.NewWorkspace("Historical receipt")
	workspace.SemanticsVersion--
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := testReceipt()
	receipt.NormalizedBundle, receipt.IntentDigest = raw, digest
	receipt.CompilationKey, err = CompilationKey(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatal(err)
	}
	if _, err := receipt.CurrentWorkspace(); err == nil {
		t.Fatal("historical receipt remained executable")
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("execution eligibility changed the immutable receipt")
	}

	migrated, err := authoringv2.MigrateStoredWorkspace(raw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	receipt.NormalizedBundle, receipt.IntentDigest = migrated.CanonicalJSON, migrated.MigratedCanonicalDigest
	if err := receipt.ValidateID(); err == nil {
		t.Fatal("rewriting authoring intent under the old receipt ID authenticated")
	}
	if _, err := receipt.CurrentWorkspace(); err != nil {
		t.Fatal(err)
	}
	receipt.IntentDigest = digest
	if _, err := receipt.CurrentWorkspace(); err == nil {
		t.Fatal("current workspace accepted an inconsistent stored digest")
	}
}
