package materialization

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/store/clickhouse"
)

type bundleTx struct {
	committed, rolledBack bool
	failInsert            bool
}

func (t *bundleTx) CreateOutput(context.Context, string, []clickhouse.Column) error { return nil }
func (t *bundleTx) InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error {
	if t.failInsert {
		return context.Canceled
	}
	return nil
}
func (t *bundleTx) Commit(context.Context) error   { t.committed = true; return nil }
func (t *bundleTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

type bundleStore struct{ tx *bundleTx }

func (s *bundleStore) BeginBundle(context.Context) (AtomicBundleTx, error) {
	s.tx = &bundleTx{failInsert: true}
	return s.tx, nil
}

func TestPublishBundleRollsBackAllOutputsOnFailure(t *testing.T) {
	store := &bundleStore{}
	err := PublishBundle(context.Background(), store, []BundleOutput{{Name: "a", Rows: []map[string]any{{"x": 1}}}, {Name: "b"}})
	if err == nil || !store.tx.rolledBack || store.tx.committed {
		t.Fatalf("bundle was not rolled back: err=%v tx=%#v", err, store.tx)
	}
}
