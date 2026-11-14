package arango

import (
	"context"
	"encoding/json"
	"errors"
	storepkg "github.com/calypr/loom/internal/store/arango"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func TestReceiptVerifiedAddressComparisonPreservesIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name              string
		legacy, addressed bool
		mutate            bool
	}{
		{name: "current immutable body", addressed: true},
		{name: "current body tamper", addressed: true, mutate: true},
		{name: "legacy address body", legacy: true, addressed: true},
		{name: "legacy address tamper", legacy: true, addressed: true, mutate: true},
		{name: "legacy arbitrary ID body", legacy: true},
		{name: "legacy arbitrary ID collision", legacy: true, mutate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := testCompilationReceipt(t, "intent")
			if tc.legacy {
				receipt.ReceiptFormatVersion = 0
				receipt.CompilerContractVersion = ""
				receipt.CompilationKey = ""
				var err error
				receipt.ID, err = explorer.ReceiptID(receipt)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.addressed {
					receipt.ID = "old-receipt-key"
				}
			}
			doc, err := document(receipt, receipt.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Preserve the stored ID while changing an immutable body field. A fast
			// address comparison is safe only after read-side integrity validation.
			if tc.mutate {
				doc["compiledConfig"] = map[string]any{"unexpected": true}
			}
			client := &receiptClient{doc: doc}
			store, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.InsertCompilationReceipt(context.Background(), receipt)
			if tc.mutate {
				if err == nil {
					t.Fatal("accepted modified persisted content at the same receipt ID")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stored.ID != receipt.ID {
				t.Fatalf("stored identity changed: %q", stored.ID)
			}
		})
	}
}

func TestReceiptStoreRejectsMutatedIncomingContractBeforeWrite(t *testing.T) {
	receipt := testCompilationReceipt(t, "intent")
	receipt.CompiledConfig = json.RawMessage(`{"changed":true}`)
	client := &receiptClient{}
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertCompilationReceipt(context.Background(), receipt); err == nil {
		t.Fatal("accepted modified incoming receipt")
	}
	if len(client.calls) != 0 {
		t.Fatal("wrote receipt before authenticating its content")
	}
}

func TestReceiptStoreReturnsAuthenticatedWinnerMetadata(t *testing.T) {
	receipt := testCompilationReceipt(t, "intent")
	winner := receipt
	winner.RequestID = "winning-request"
	winner.CreatedAt = time.Unix(1, 0).UTC()
	doc, err := document(winner, winner.ID)
	if err != nil {
		t.Fatal(err)
	}
	client := &receiptClient{doc: doc}
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	receipt.RequestID = "later-request"
	receipt.CreatedAt = time.Unix(2, 0).UTC()
	stored, err := store.InsertCompilationReceipt(context.Background(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RequestID != winner.RequestID || !stored.CreatedAt.Equal(winner.CreatedAt) {
		t.Fatal("did not return persisted winning receipt metadata")
	}
	if stored.ID != winner.ID {
		t.Fatal("metadata changed immutable identity")
	}
}

func TestReceiptStoreLegacyCollisionIsCorrupt(t *testing.T) {
	receipt := explorer.CompilationReceipt{ID: "old-key", Project: "project-a", ExplorerID: "explorer-a", IntentDigest: "original", Bundle: testReceiptBundle()}
	other := receipt
	other.IntentDigest = "different"
	doc, err := document(other, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(&receiptClient{doc: doc})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertCompilationReceipt(context.Background(), receipt); !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("error=%v, want legacy content collision rejection", err)
	}
}

// The receipt contains maps and raw byte slices. A client callback exercises a
// mutation across the I/O boundary without a data race, so entry validation
// alone must never authorize the later expected body comparison.
func TestReceiptStoreRechecksCallerBodyAcrossIO(t *testing.T) {
	receipt := testCompilationReceipt(t, "intent")
	receipt.CompiledConfig = json.RawMessage(`{"value":"original"}`)
	var err error
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	client := &mutatingReceiptClient{receiptClient: &receiptClient{}, mutate: func() {
		copy(receipt.CompiledConfig, []byte(`{"value":"modified"}`))
	}}
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertCompilationReceipt(context.Background(), receipt); !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("error=%v, want changed caller body rejection", err)
	}
}

type mutatingReceiptClient struct {
	*receiptClient
	mutate func()
}

func (c *mutatingReceiptClient) QueryRows(ctx context.Context, query string, batchSize int, binds map[string]any, visit storepkg.RowVisitor) error {
	err := c.receiptClient.QueryRows(ctx, query, batchSize, binds, visit)
	if err == nil && strings.HasPrefix(strings.TrimSpace(query), "INSERT") {
		c.mutate()
	}
	return err
}
