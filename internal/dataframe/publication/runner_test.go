package publication

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type fakeTx struct {
	batches    [][]map[string]any
	committed  bool
	rolledBack bool
}

func (t *fakeTx) WriteBatch(_ context.Context, _ string, rows []map[string]any) error {
	t.batches = append(t.batches, rows)
	return nil
}
func (t *fakeTx) Commit(context.Context) ([]PublishedOutput, error) {
	t.committed = true
	return []PublishedOutput{{Name: "patients", PhysicalName: "staged_patients"}}, nil
}
func (t *fakeTx) Abort(context.Context, error) error                   { t.rolledBack = true; return nil }
func (t *fakeTx) FinalizeSchema(context.Context, []OutputSchema) error { return nil }
func (t *fakeTx) SetFinalSchemaDigest(string) error                    { return nil }
func (t *fakeTx) Idempotent() bool                                     { return false }
func (t *fakeTx) ExistingPublishedOutputs() []PublishedOutput          { return nil }

type fakeTarget struct {
	tx      *fakeTx
	schemas []OutputSchema
}

func (t *fakeTarget) SupportsObjectValues() bool { return false }

type finalizingTx struct {
	fakeTx
	finalized []OutputSchema
	digest    string
}

func (t *finalizingTx) FinalizeSchema(_ context.Context, schemas []OutputSchema) error {
	t.finalized = append([]OutputSchema(nil), schemas...)
	return nil
}

func (t *finalizingTx) SetFinalSchemaDigest(digest string) error {
	t.digest = digest
	return nil
}

type finalizingTarget struct{ tx *finalizingTx }

func (t *finalizingTarget) SupportsObjectValues() bool { return false }

func (t *finalizingTarget) Begin(_ context.Context, _ PublicationIdentity, _ []OutputSchema) (Transaction, error) {
	t.tx = &finalizingTx{}
	return t.tx, nil
}

type objectTarget struct{ fakeTarget }

func (t *objectTarget) SupportsObjectValues() bool { return true }

func TestPublishPrunesOnlyUnpopulatedDiscoveredColumns(t *testing.T) {
	target := &finalizingTarget{}
	columns := []LogicalColumn{
		{Name: "id", Kind: "string", Provenance: ColumnExplicit},
		{Name: "missing", Kind: "string", Nullable: true, Provenance: ColumnDiscovered},
		{Name: "nil_value", Kind: "string", Nullable: true, Provenance: ColumnDiscovered},
		{Name: "empty_array", Kind: "string", Repeated: true, Nullable: true, Provenance: ColumnDiscovered},
		{Name: "false_value", Kind: "boolean", Nullable: true, Provenance: ColumnDiscovered},
		{Name: "zero_value", Kind: "integer", Nullable: true, Provenance: ColumnDiscovered},
		{Name: "empty_string", Kind: "string", Nullable: true, Provenance: ColumnDiscovered},
		{Name: "nonempty_array", Kind: "string", Repeated: true, Nullable: true, Provenance: ColumnDiscovered},
	}
	_, err := Publish(context.Background(), target, PublicationIdentity{Name: "r", Project: "p"}, []OutputStream{{
		Name: "rows", Columns: columns,
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"id": "1", "nil_value": nil, "empty_array": []any{}, "false_value": false, "zero_value": 0, "empty_string": "", "nonempty_array": []any{"x"}})
		},
	}}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(target.tx.finalized) != 1 {
		t.Fatalf("expected one finalized output, got %#v", target.tx.finalized)
	}
	got := make([]string, 0, len(target.tx.finalized[0].Columns))
	for _, column := range target.tx.finalized[0].Columns {
		got = append(got, column.Name)
	}
	want := []string{"auth_resource_path", "project_id", "id", "false_value", "zero_value", "empty_string", "nonempty_array"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("retained columns = %v, want %v", got, want)
	}
	if target.tx.digest == "" {
		t.Fatal("final schema digest was not set")
	}
}

func (t *fakeTarget) Begin(_ context.Context, _ PublicationIdentity, schemas []OutputSchema) (Transaction, error) {
	t.schemas = schemas
	t.tx = &fakeTx{}
	return t.tx, nil
}

func TestPublishValidatesAndBoundsBatches(t *testing.T) {
	target := &fakeTarget{}
	result, err := Publish(context.Background(), target, PublicationIdentity{Name: "r", Project: "HTAN_INT-BForePC", AuthResourcePaths: []string{"/programs/p1"}}, []OutputStream{{
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
	if !target.tx.committed || target.tx.rolledBack || len(target.tx.batches) != 2 {
		t.Fatalf("unexpected transaction lifecycle: %#v", target.tx)
	}
	if result.Outputs[0].RowCount != 3 || result.Outputs[0].PhysicalName != "staged_patients" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(target.schemas) != 1 || target.schemas[0].Columns[0].Name != "auth_resource_path" {
		t.Fatalf("reserved auth column missing: %#v", target.schemas)
	}
	if len(target.schemas[0].Columns) < 2 || target.schemas[0].Columns[1].Name != "project_id" {
		t.Fatalf("reserved project column missing: %#v", target.schemas)
	}
	if got := target.tx.batches[0][0]["auth_resource_path"]; got != "/programs/p1" {
		t.Fatalf("auth resource path = %#v", got)
	}
	if got := target.tx.batches[0][0]["project_id"]; got != "HTAN_INT-BForePC" {
		t.Fatalf("project_id = %#v", got)
	}
}

func TestPublishAllowsObjectsOnlyForObjectCapableTargets(t *testing.T) {
	row := map[string]any{"value": map[string]any{"code": "M1", "active": true}}
	columns := []LogicalColumn{{Name: "value", Kind: "object"}}
	unsupported := &fakeTarget{}
	_, err := Publish(context.Background(), unsupported, PublicationIdentity{Name: "r"}, []OutputStream{{
		Name: "objects", Columns: columns, Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(row)
		},
	}}, Limits{})
	if err == nil {
		t.Fatal("expected generic target to reject object schema")
	}

	target := &objectTarget{}
	_, err = Publish(context.Background(), target, PublicationIdentity{Name: "r"}, []OutputStream{{
		Name: "objects", Columns: columns, Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(row)
		},
	}}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPublishRejectsUnknownLogicalKindBeforeEmptyStreamBegins(t *testing.T) {
	target := &fakeTarget{}
	_, err := Publish(context.Background(), target, PublicationIdentity{Name: "r"}, []OutputStream{{
		Name:    "empty",
		Columns: []LogicalColumn{{Name: "value", Kind: "mystery"}},
		Stream: func(_ context.Context, _ func(map[string]any) error) error {
			return nil
		},
	}}, Limits{})
	var typedErr *dataframeerrors.Error
	if err == nil || !errors.As(err, &typedErr) || typedErr.Code() != string(dataframeerrors.CodeInvalidData) || typedErr.Unwrap() == nil || !strings.Contains(typedErr.Unwrap().Error(), `column "value" has unsupported logical kind "mystery"`) {
		t.Fatalf("expected unknown logical kind error, got %v", err)
	}
	if target.tx != nil {
		t.Fatal("target began a transaction for an invalid empty stream")
	}
}

func TestPublishProjectIDIsTrustedAndDeduplicated(t *testing.T) {
	target := &fakeTarget{}
	_, err := Publish(context.Background(), target, PublicationIdentity{Name: "r", Project: "trusted"}, []OutputStream{{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "project_id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"project_id": "untrusted"})
		},
	}}, Limits{})
	if err != nil {
		t.Fatalf("project_id source column should be normalized: %v", err)
	}
	if len(target.schemas) != 1 || len(target.schemas[0].Columns) != 2 || target.schemas[0].Columns[1].Name != "project_id" {
		t.Fatalf("project_id was not deduplicated: %#v", target.schemas)
	}
	if got := target.tx.batches[0][0]["project_id"]; got != "trusted" {
		t.Fatalf("project_id was not trusted: %#v", got)
	}

	target = &fakeTarget{}
	_, err = Publish(context.Background(), target, PublicationIdentity{Name: "r", Project: "trusted"}, []OutputStream{{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"id": "p1", "project_id": "untrusted"})
		},
	}}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := target.tx.batches[0][0]["project_id"]; got != "trusted" {
		t.Fatalf("project_id was not trusted: %#v", got)
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
