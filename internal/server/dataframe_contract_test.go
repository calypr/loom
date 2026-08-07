package server

import (
	"context"
	"testing"
	"time"
)

func TestDataframeContractStoreLoadsAndPromotesExactVersion(t *testing.T) {
	promotedAt := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	query := &releaseQueryFixture{row: map[string]any{"recipe": "core", "translationVersion": "v1", "promotedAt": promotedAt.Format(time.RFC3339Nano)}}
	store := dataframeContractStore{query: query}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Recipe != "core" || loaded.TranslationVersion != "v1" || !loaded.PromotedAt.Equal(promotedAt) {
		t.Fatalf("loaded contract = %#v", loaded)
	}
	query.row = map[string]any{}
	promoted, err := store.Promote(context.Background(), "core", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Recipe != "core" || promoted.TranslationVersion != "v2" || query.binds["recipe"] != "core" || query.binds["version"] != "v2" {
		t.Fatalf("promoted contract = %#v binds=%#v", promoted, query.binds)
	}
	state := &dataframeContractState{}
	state.Set(promoted)
	if recipe, version := state.Current(); recipe != "core" || version != "v2" {
		t.Fatalf("current contract = %s@%s", recipe, version)
	}
}
