package arango

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	store "github.com/calypr/loom/internal/store/arango"
	"os"
	"strings"
	"testing"
)

type reviewReaderRows struct{ raw []byte }

func (c *reviewReaderRows) WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error {
	panic("read-only probe")
}
func (c *reviewReaderRows) QueryRows(_ context.Context, q string, _ int, b map[string]any, v store.RowVisitor) error {
	if strings.Contains(q, "JSON_STRINGIFY") {
		return v(map[string]any{"migrationJSON": string(c.raw)})
	}
	var row map[string]any
	if err := json.Unmarshal(c.raw, &row); err != nil {
		return err
	}
	return v(row)
}
func reviewLargeArchive(t *testing.T) ([]byte, string) {
	t.Helper()
	raw, err := os.ReadFile("../authoringv2/testdata/typed-unpivot.input.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"integer":10`, `"integer":9007199254740993`, 1))
	w, err := authoringv2.DecodeStoredWorkspaceForMigration(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := authoringv2.CanonicalStoredWorkspaceForMigration(w)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
}
func TestReviewStoredReceiptLargeIntegerRemainsVerifiableOnRead(t *testing.T) {
	raw, digest := reviewLargeArchive(t)
	receipt := testCompilationReceipt(t, digest)
	receipt.NormalizedBundle = raw
	receipt.CompilationKey, _ = explorer.CompilationKey(receipt)
	receipt.ID, _ = explorer.ReceiptID(receipt)
	if err := receipt.Validate(); err != nil {
		t.Fatalf("typed original invalid: %v", err)
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := New(&reviewReaderRows{doc})
	got, err := adapter.GetCompilationReceiptForExplorer(context.Background(), receipt.Project, receipt.ExplorerID, receipt.ID)
	fmt.Printf("typed_original_valid=true receipt_read_error=%v\n", err)
	if err != nil {
		t.Fatalf("authentic original receipt became unverifiable after row callback codec: %v", err)
	}
	if err := got.ValidateID(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.NormalizedBundle), "9007199254740993") {
		t.Fatal("raw receipt integer changed")
	}
}
func TestReviewStoredOwnerHistoryRevisionLargeIntegerReadIdentity(t *testing.T) {
	raw, digest := reviewLargeArchive(t)
	for _, kind := range []string{"owner", "history", "revision"} {
		t.Run(kind, func(t *testing.T) {
			var value any
			switch kind {
			case "owner":
				value = explorer.Explorer{Project: "p", ExplorerID: "e", DraftConfig: raw, DraftDigest: digest}
			case "history":
				value = explorer.DraftRevision{ID: "h", Project: "p", ExplorerID: "e", DraftConfig: raw, DraftDigest: digest}
			case "revision":
				value = explorer.Revision{ID: "r", Project: "p", ExplorerID: "e", Config: raw, AuthoringBundle: raw, IntentDigest: digest}
			}
			bytes, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			adapter, _ := New(&reviewReaderRows{bytes})
			var found json.RawMessage
			switch kind {
			case "owner":
				owner, err := adapter.Get(context.Background(), "p", "e")
				if err != nil {
					t.Fatal(err)
				}
				found = owner.DraftConfig
			case "history":
				history, err := adapter.GetDraftRevision(context.Background(), "p", "e", "h")
				if err != nil {
					t.Fatal(err)
				}
				found = history.DraftConfig
			case "revision":
				revision, err := adapter.GetRevision(context.Background(), "r")
				if err != nil {
					t.Fatal(err)
				}
				found = revision.AuthoringWorkspaceBytes()
			}
			fmt.Printf("reader=%s exact_int64=%t rounded_int64=%t\n", kind, strings.Contains(string(found), "9007199254740993"), strings.Contains(string(found), "9007199254740992"))
			if !strings.Contains(string(found), "9007199254740993") {
				t.Fatalf("%s reader changed immutable/source workspace identity", kind)
			}
		})
	}
}

func storedJSONVisitorForTest(query string, visit store.RowVisitor) store.RowVisitor {
	if !strings.Contains(query, "JSON_STRINGIFY") {
		return visit
	}
	return func(row map[string]any) error {
		raw, err := json.Marshal(row)
		if err != nil {
			return err
		}
		return visit(map[string]any{"migrationJSON": string(raw)})
	}
}
func TestStoredJSONReaderRetainsLegacyNumericTimestampAndRawInteger(t *testing.T) {
	value, err := decodeMigrationJSON[explorer.Explorer](`{"updatedAt":1776620000000,"draftConfig":{"integer":9007199254740993}}`)
	if err != nil {
		t.Fatal(err)
	}
	if value.UpdatedAt.UnixMilli() != 1776620000000 || !strings.Contains(string(value.DraftConfig), "9007199254740993") {
		t.Fatalf("legacy timestamp normalization changed packet: %#v", value)
	}
	for _, wrong := range []any{nil, map[string]any{"id": "wrong"}, 17, "{malformed"} {
		if _, err := decodeMigrationJSON[explorer.Explorer](wrong); err == nil {
			t.Fatalf("invalid raw reader envelope accepted: %#v", wrong)
		}
	}
}

func TestReviewCurrentOwnerLargeIntegerReadIdentity(t *testing.T) {
	raw, _ := reviewLargeArchive(t)
	migrated, err := authoringv2.MigrateStoredTableShapeWorkspace(raw, authoringv2.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := explorer.Explorer{Project: "p", ExplorerID: "e", DraftConfig: migrated.CanonicalJSON, DraftDigest: migrated.MigratedCanonicalDigest}
	doc, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := New(&reviewReaderRows{doc})
	stored, err := adapter.Get(context.Background(), "p", "e")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := authoringv2.DecodeWorkspace(stored.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("current_owner_int64_preserved=%t digest_matches=%t\n", strings.Contains(string(stored.DraftConfig), "9007199254740993"), digest == stored.DraftDigest)
	if digest != stored.DraftDigest || !strings.Contains(string(stored.DraftConfig), "9007199254740993") {
		t.Fatal("current draft getter changed legal INTEGER and canonical digest")
	}
}
