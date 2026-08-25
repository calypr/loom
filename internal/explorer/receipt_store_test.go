package explorer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestMemoryReceiptStoreIsTenantScopedAndImmutable(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	receipt := CompilationReceipt{
		ID: "receipt-one", Project: "project-a", ExplorerID: "explorer-a", IntentDigest: "compile-a",
		CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Bundle:    recipe.Bundle{Outputs: []recipe.Output{{Name: "output", Fields: []recipe.Field{{Name: "field"}}}}},
	}
	if _, err := store.InsertCompilationReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	// Mutating both the input and the returned value must not mutate storage.
	receipt.Bundle.Outputs[0].Fields[0].Name = "caller-mutated"
	got, err := store.GetCompilationReceiptForExplorer(ctx, "project-a", "explorer-a", "receipt-one")
	if err != nil {
		t.Fatal(err)
	}
	got.Bundle.Outputs[0].Fields[0].Name = "reader-mutated"
	gotAgain, err := store.GetCompilationReceiptForExplorer(ctx, "project-a", "explorer-a", "receipt-one")
	if err != nil {
		t.Fatal(err)
	}
	if gotAgain.Bundle.Outputs[0].Fields[0].Name != "field" {
		t.Fatalf("receipt nested recipe aliased storage: %q", gotAgain.Bundle.Outputs[0].Fields[0].Name)
	}
	if _, err := store.GetCompilationReceiptForExplorer(ctx, "project-b", "explorer-a", "receipt-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant lookup error=%v", err)
	}
}

func TestMemoryReceiptStoreDetectsImmutableCollisionAndFindsKey(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	base := CompilationReceipt{ID: "receipt-same", Project: "p", ExplorerID: "e", IntentDigest: "key-a"}
	if _, err := store.InsertCompilationReceipt(ctx, base); err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.IntentDigest = "key-b"
	if _, err := store.InsertCompilationReceipt(ctx, changed); !errors.Is(err, ErrCorruptReceipt) {
		t.Fatalf("collision error=%v", err)
	}
	found, err := store.GetCompilationReceiptByCompilationKey(ctx, "p", "e", "key-a")
	if err != nil || found.ID != base.ID {
		t.Fatalf("key lookup=%#v, err=%v", found, err)
	}
}

func TestMemoryReceiptStoreStats(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, r := range []CompilationReceipt{
		{ID: "receipt-kept", Project: "p", ExplorerID: "e", IntentDigest: "a", CreatedAt: old},
		{ID: "receipt-orphan", Project: "p", ExplorerID: "e", IntentDigest: "b", CreatedAt: old.Add(time.Hour)},
	} {
		if _, err := store.InsertCompilationReceipt(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.InsertRevision(ctx, Revision{ID: "revision", Project: "p", ExplorerID: "e", CompilationReceiptID: "receipt-kept"}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.CompilationReceiptStats(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Count != 2 || stats.UnreferencedCount != 1 || stats.ApproxBytes <= 0 || !stats.OldestCreatedAt.Equal(old) {
		t.Fatalf("stats=%+v", stats)
	}
}
