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
	quality    []QualityReport
	idempotent bool
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
func (t *fakeTx) SetQualityReports(_ context.Context, reports []QualityReport) error {
	t.quality = CloneQualityReports(reports)
	return nil
}
func (t *fakeTx) Idempotent() bool                            { return t.idempotent }
func (t *fakeTx) ExistingPublishedOutputs() []PublishedOutput { return nil }
func (t *fakeTx) ExistingQualityReports() []QualityReport     { return CloneQualityReports(t.quality) }

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

type sourceMetadataTx struct {
	fakeTx
	metadata map[string]SourceRowMetadata
}

func (t *sourceMetadataTx) SetSourceRowMetadata(_ context.Context, name string, metadata SourceRowMetadata) error {
	if t.metadata == nil {
		t.metadata = map[string]SourceRowMetadata{}
	}
	t.metadata[name] = metadata
	return nil
}

type sourceMetadataTarget struct{ tx *sourceMetadataTx }

func (t *sourceMetadataTarget) SupportsObjectValues() bool { return false }

func (t *sourceMetadataTarget) Begin(_ context.Context, _ PublicationIdentity, _ []OutputSchema) (Transaction, error) {
	t.tx = &sourceMetadataTx{}
	return t.tx, nil
}

func TestPublishPersistsSourceRowMetadataBeforeCommit(t *testing.T) {
	target := &sourceMetadataTarget{}
	_, err := Publish(context.Background(), target, PublicationIdentity{Name: "r", Project: "p"}, []OutputStream{{
		Name: "files", SourceRow: &SourceRowMetadata{ResourceType: "DocumentReference", IDColumn: "id"}, Columns: []LogicalColumn{{Name: "id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"id": "files001"})
		},
	}}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := target.tx.metadata["files"]
	if !ok || metadata.ResourceType != "DocumentReference" || metadata.IDColumn != "id" {
		t.Fatalf("source metadata = %#v", target.tx.metadata)
	}
	if !target.tx.committed {
		t.Fatal("publication did not commit")
	}
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

func TestPublishProducesReceiptBoundCompleteQualityReport(t *testing.T) {
	target := &fakeTarget{}
	identity := PublicationIdentity{
		Name: "r", Project: "project-a", DatasetGeneration: "generation-a",
		ReceiptID: "receipt-a", ScopeDigest: "scope-a",
	}
	result, err := Publish(context.Background(), target, identity, []OutputStream{{
		Name: "patients",
		Columns: []LogicalColumn{
			{Name: "__loom_row_id", Kind: "string", IsIdentity: true, LoomOwned: true},
			{Name: "gender", Kind: "string", Nullable: true},
			{Name: "codes", Kind: "string", Repeated: true, Nullable: true},
		},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{
				{"__loom_row_id": "row-1", "gender": "female", "codes": []any{}},
				{"__loom_row_id": "row-2", "gender": nil},
				{"__loom_row_id": "row-3", "gender": "male", "codes": []any{"a"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}}, Limits{Quality: QualityPolicy{Version: "quality-v1", MaxRows: 10, MaxDistinctKeys: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if !target.tx.committed || len(result.QualityReports) != 1 {
		t.Fatalf("publication/result = committed:%v reports:%#v", target.tx.committed, result.QualityReports)
	}
	if len(target.tx.quality) != 1 || target.tx.quality[0].ID != result.QualityReports[0].ID {
		t.Fatalf("quality evidence was not attached before commit: %#v", target.tx.quality)
	}
	report := result.QualityReports[0]
	if report.ID == "" || report.ReceiptID != "receipt-a" || report.Project != "project-a" || report.DatasetGeneration != "generation-a" || report.ScopeDigest != "scope-a" || report.Output != "patients" || report.PolicyVersion != "quality-v1" || report.Completeness != QualityComplete || report.RowCount != 3 {
		t.Fatalf("quality identity = %#v", report)
	}
	if report.KeyIntegrity.Missing != 0 || report.KeyIntegrity.Duplicate != 0 || report.KeyIntegrity.Distinct != 3 {
		t.Fatalf("key integrity = %#v", report.KeyIntegrity)
	}
	columns := map[string]ColumnQuality{}
	for _, column := range report.Columns {
		columns[column.Column] = column
	}
	if columns["gender"] != (ColumnQuality{Column: "gender", Present: 2, RecordedNull: 1}) {
		t.Fatalf("gender quality = %#v", columns["gender"])
	}
	if columns["codes"] != (ColumnQuality{Column: "codes", Present: 1, Missing: 1, EmptyArray: 1}) {
		t.Fatalf("codes quality = %#v", columns["codes"])
	}
}

type existingTarget struct{ tx *fakeTx }

func (t *existingTarget) SupportsObjectValues() bool { return false }

func (t *existingTarget) Begin(context.Context, PublicationIdentity, []OutputSchema) (Transaction, error) {
	return t.tx, nil
}

func TestPublishIdempotentRetryReturnsDurableQualityEvidenceWithoutReadingStream(t *testing.T) {
	report := QualityReport{ID: "quality-a", ReceiptID: "receipt-a", Output: "patients", PolicyVersion: "quality-v1", Completeness: QualityComplete, Verdict: QualityPassed}
	target := &existingTarget{tx: &fakeTx{idempotent: true, quality: []QualityReport{report}}}
	streamRead := false
	result, err := Publish(context.Background(), target, PublicationIdentity{Name: "r"}, []OutputStream{{
		Name: "patients", Columns: []LogicalColumn{{Name: "id", Kind: "string"}},
		Stream: func(context.Context, func(map[string]any) error) error {
			streamRead = true
			return nil
		},
	}}, Limits{Quality: QualityPolicy{Version: "quality-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	if streamRead {
		t.Fatal("idempotent retry reopened the source stream")
	}
	if len(result.QualityReports) != 1 || result.QualityReports[0].ID != report.ID {
		t.Fatalf("idempotent quality evidence = %#v", result.QualityReports)
	}
}

func TestPublishRejectsIncompleteQualityBeforeCommit(t *testing.T) {
	target := &fakeTarget{}
	_, err := Publish(context.Background(), target, PublicationIdentity{Name: "r", Project: "p", ReceiptID: "receipt-a"}, []OutputStream{{
		Name: "patients", Columns: []LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			for _, id := range []string{"row-1", "row-2"} {
				if err := visit(map[string]any{"__loom_row_id": id}); err != nil {
					return err
				}
			}
			return nil
		},
	}}, Limits{Quality: QualityPolicy{Version: "quality-v1", MaxRows: 1, MaxDistinctKeys: 10}})
	var incomplete *QualityIncompleteError
	if !errors.As(err, &incomplete) || len(incomplete.Reports) != 1 || incomplete.Reports[0].Completeness != QualityIncomplete {
		t.Fatalf("quality error = %#v, reports=%#v", err, incomplete)
	}
	if target.tx == nil || !target.tx.rolledBack || target.tx.committed {
		t.Fatalf("incomplete quality did not abort before commit: %#v", target.tx)
	}
}
