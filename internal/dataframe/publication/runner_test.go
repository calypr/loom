package publication

import (
	"context"
	"errors"
	"testing"
)

type fakeTx struct {
	batches    [][]map[string]any
	validated  bool
	committed  bool
	rolledBack bool
}

func (t *fakeTx) WriteBatch(_ context.Context, _ string, rows []map[string]any) error {
	t.batches = append(t.batches, rows)
	return nil
}
func (t *fakeTx) Validate(context.Context) error { t.validated = true; return nil }
func (t *fakeTx) Commit(context.Context) ([]PublishedOutput, error) {
	t.committed = true
	return []PublishedOutput{{Name: "patients", PhysicalName: "staged_patients"}}, nil
}
func (t *fakeTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

type fakeTarget struct {
	tx      *fakeTx
	schemas []OutputSchema
}

func (t *fakeTarget) Begin(_ context.Context, _ PublicationIdentity, schemas []OutputSchema) (Transaction, error) {
	t.schemas = schemas
	t.tx = &fakeTx{}
	return t.tx, nil
}

func TestPublishValidatesAndBoundsBatches(t *testing.T) {
	target := &fakeTarget{}
	result, err := Publish(context.Background(), target, PublicationIdentity{Name: "r", AuthResourcePaths: []string{"/programs/p1"}}, []OutputStream{{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}, {Name: "id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{{"__loom_row_id": "a", "id": "p1"}, {"__loom_row_id": "b", "id": "p2"}, {"__loom_row_id": "c", "id": "p3"}} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}}, Limits{BatchRows: 2, BatchBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !target.tx.validated || !target.tx.committed || target.tx.rolledBack || len(target.tx.batches) != 2 {
		t.Fatalf("unexpected transaction lifecycle: %#v", target.tx)
	}
	if result.Outputs[0].RowCount != 3 || result.Outputs[0].PhysicalName != "staged_patients" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(target.schemas) != 1 || target.schemas[0].Columns[0].Name != "auth_resource_path" {
		t.Fatalf("reserved auth column missing: %#v", target.schemas)
	}
	if got := target.tx.batches[0][0]["auth_resource_path"]; got != "/programs/p1" {
		t.Fatalf("auth resource path = %#v", got)
	}
}

func TestPublishRollsBackOnSchemaViolation(t *testing.T) {
	target := &fakeTarget{}
	_, err := Publish(context.Background(), target, PublicationIdentity{Name: "r"}, []OutputStream{{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "__loom_row_id", Kind: "string"}, {Name: "id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"__loom_row_id": "a", "unknown": "x"})
		},
	}}, Limits{})
	if err == nil || target.tx == nil || !target.tx.rolledBack || target.tx.committed {
		t.Fatalf("expected rollback on row violation: err=%v tx=%#v", err, target.tx)
	}
}

func TestPublishHonorsCancellation(t *testing.T) {
	target := &fakeTarget{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Publish(ctx, target, PublicationIdentity{Name: "r"}, []OutputStream{{
		Name: "patients", Columns: []LogicalColumn{{Name: "id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"id": "p1"})
		},
	}}, Limits{})
	if !errors.Is(err, context.Canceled) || target.tx == nil || !target.tx.rolledBack {
		t.Fatalf("expected cancellation rollback: err=%v tx=%#v", err, target.tx)
	}
}
