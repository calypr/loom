package lifecycle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

type artifactBackend struct {
	*fakeStore
	revision *explorer.Revision
}

func (s *artifactBackend) GetRevision(context.Context, string) (*explorer.Revision, error) {
	if s.revision == nil {
		return nil, explorer.ErrNotFound
	}
	copy := *s.revision
	return &copy, nil
}

type memoryArtifactStore struct {
	records map[string]explorer.ArtifactRecord
	data    map[string][]byte
}

func newMemoryArtifactStore() *memoryArtifactStore {
	return &memoryArtifactStore{records: make(map[string]explorer.ArtifactRecord), data: make(map[string][]byte)}
}

func (s *memoryArtifactStore) Begin(_ context.Context, record explorer.ArtifactRecord) (explorer.ArtifactStage, *explorer.ArtifactRecord, error) {
	if existing, ok := s.records[record.ID]; ok {
		copy := existing
		return nil, &copy, nil
	}
	return &memoryArtifactStage{store: s, record: record}, nil, nil
}

func (s *memoryArtifactStore) Get(_ context.Context, id string) (*explorer.ArtifactRecord, error) {
	record, ok := s.records[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	copy := record
	return &copy, nil
}

func (s *memoryArtifactStore) Open(_ context.Context, id string) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	record, err := s.Get(context.Background(), id)
	if err != nil {
		return nil, nil, err
	}
	if record.State != explorer.ArtifactComplete || !record.ExpiresAt.After(time.Now().UTC()) {
		return nil, record, errors.New("artifact unavailable")
	}
	return io.NopCloser(bytes.NewReader(s.data[id])), record, nil
}

type memoryArtifactStage struct {
	store  *memoryArtifactStore
	record explorer.ArtifactRecord
	bytes.Buffer
	closed bool
}

func (s *memoryArtifactStage) Close() error {
	s.closed = true
	return nil
}

func (s *memoryArtifactStage) Commit(_ context.Context, completion explorer.ArtifactCompletion) (*explorer.ArtifactRecord, error) {
	if completion.ArchiveSHA256 == "" || completion.Bytes <= 0 {
		return nil, errors.New("invalid completion")
	}
	_ = s.Close()
	s.record.State = explorer.ArtifactComplete
	s.record.ArchiveSHA256 = completion.ArchiveSHA256
	s.record.Bytes = completion.Bytes
	s.record.Rows = completion.Rows
	s.record.Features = completion.Features
	s.record.CompletedAt = &completion.CompletedAt
	s.store.records[s.record.ID] = s.record
	s.store.data[s.record.ID] = append([]byte(nil), s.Bytes()...)
	copy := s.record
	return &copy, nil
}

func (s *memoryArtifactStage) Abort(_ context.Context, code string) (*explorer.ArtifactRecord, error) {
	_ = s.Close()
	s.record.State = explorer.ArtifactFailed
	s.record.FailureCode = code
	s.store.records[s.record.ID] = s.record
	copy := s.record
	return &copy, nil
}

type fakeArtifactReader struct {
	materialization dataframepublished.Materialization
	rows            []map[string]any
	err             error
	streamCalls     int
}

func (r *fakeArtifactReader) ExactExecutionMaterialization(context.Context, string, string) (dataframepublished.Materialization, error) {
	return r.materialization, nil
}

func (r *fakeArtifactReader) StreamExactExport(_ context.Context, request dataframepublished.ExactExportRequest, visit dataframepublished.ExactExportVisitor) (dataframepublished.ExactExportResult, error) {
	r.streamCalls++
	if r.err != nil {
		return dataframepublished.ExactExportResult{}, r.err
	}
	for _, row := range r.rows {
		if err := visit(row); err != nil {
			return dataframepublished.ExactExportResult{}, err
		}
	}
	return dataframepublished.ExactExportResult{ExecutionID: request.ExecutionID, OutputID: request.OutputID, ReaderID: request.ReaderID, Materialization: r.materialization, Rows: int64(len(r.rows))}, nil
}

func artifactTestFixture(t *testing.T) (*artifactBackend, *memoryArtifactStore, *fakeArtifactReader, capability.Snapshot, *explorer.CompilationReceipt) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token-a", scope)
	receipt := nativeReceipt(snapshot)
	receipt.SnapshotToken = snapshot.Token
	report := publication.QualityReport{ID: "quality-patients", ReceiptID: receipt.ID, Project: receipt.Project, DatasetGeneration: receipt.SourceGeneration, ScopeDigest: receipt.AuthorizationScopeDigest, Output: "patients", PolicyVersion: publication.DefaultQualityPolicyVersion, Completeness: publication.QualityComplete, Verdict: publication.QualityPassed}
	revision := &explorer.Revision{ID: "revision-a", Project: receipt.Project, ExplorerID: receipt.ExplorerID, CompilationReceiptID: receipt.ID, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: "execution-a"}, QualityReports: []publication.QualityReport{report}, Status: explorer.RevisionReady}
	backend := &artifactBackend{fakeStore: &fakeStore{receipt: receipt}, revision: revision}
	materialization := dataframepublished.Materialization{Revision: "execution-a", ReceiptID: receipt.ID, SchemaDigest: "schema-a", Project: receipt.Project, DatasetGeneration: receipt.SourceGeneration, Name: "Patient", State: dataframepublished.StateReady, ScopeUnrestricted: true, Columns: []dataframepublished.Column{{Name: "patient_id", LogicalType: "string"}, {Name: "project_id", LogicalType: "string", LoomOwned: true}, {Name: "auth_resource_path", LogicalType: "string", LoomOwned: true}}, SourceRow: &publication.SourceRowMetadata{ResourceType: "Patient", IDColumn: "patient_id"}}
	reader := &fakeArtifactReader{materialization: materialization, rows: []map[string]any{{"patient_id": "p1", "project_id": receipt.Project, "auth_resource_path": "private"}}}
	artifactStore := newMemoryArtifactStore()
	return backend, artifactStore, reader, snapshot, receipt
}

func TestPrepareArtifactCommitsExactOutputAndIsIdempotent(t *testing.T) {
	backend, artifacts, reader, snapshot, receipt := artifactTestFixture(t)
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: artifacts, PublishedReader: reader, Capability: CapabilityResolver{ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "download-1"}
	first, err := service.PrepareArtifact(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record == nil || first.Record.State != explorer.ArtifactComplete || first.Record.Rows != 1 || first.Record.Features != 1 {
		t.Fatalf("first result = %#v", first.Record)
	}
	if reader.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", reader.streamCalls)
	}
	second, err := service.PrepareArtifact(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Record == nil || second.Record.ID != first.Record.ID || reader.streamCalls != 1 {
		t.Fatalf("idempotent result=%#v stream calls=%d", second.Record, reader.streamCalls)
	}
	opened, record, err := service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: first.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if record.State != explorer.ArtifactComplete {
		t.Fatalf("opened record state = %s", record.State)
	}
	archive, err := io.ReadAll(opened)
	if err != nil || !bytes.HasPrefix(archive, []byte("PK")) {
		t.Fatalf("archive read err=%v prefix=%q", err, archive[:minInt(len(archive), 2)])
	}
}

func TestPrepareArtifactUsesJSONLWhenCSVWouldOmitRowIdentity(t *testing.T) {
	backend, artifacts, reader, snapshot, receipt := artifactTestFixture(t)
	reader.materialization.SourceRow = nil
	reader.rows[0]["__loom_row_id"] = map[string]any{"groupRevisionId": "groups-1", "groupId": "group-1"}
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: artifacts, PublishedReader: reader, Capability: CapabilityResolver{ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareArtifact(context.Background(), ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "group-download"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Record == nil || result.Record.Format != dataframepublished.ArtifactFormatJSONL {
		t.Fatalf("artifact record = %#v, want JSONL", result.Record)
	}
	archive, err := zip.NewReader(bytes.NewReader(artifacts.data[result.Record.ID]), int64(len(artifacts.data[result.Record.ID])))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range archive.File {
		if member.Name == "data.jsonl" {
			return
		}
	}
	t.Fatal("typed artifact omitted data.jsonl")
}

func TestGroupArtifactDescriptorKeepsSyntheticMembershipWithoutSourceRow(t *testing.T) {
	receipt := &explorer.CompilationReceipt{
		ReceiptFormatVersion: 2, CompilerContractVersion: "test", SourceGeneration: "generation-a",
		ResolvedSchemaDigest: "resolved-schema", OutputContractDigest: "contract-digest",
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"groups","rowGrain":"groups","rowMultiplication":"none","columns":[{"column":"patient_id","label":"Root ID","logicalType":"string"},{"column":"group_mean_weight","label":"Mean weight","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg"}}]}]}`),
	}
	materialization := dataframepublished.Materialization{
		SchemaDigest: "published-schema", Columns: []dataframepublished.Column{
			{Name: "group_revision_id", SemanticPath: "groups.revision_id", LogicalType: "string", LoomOwned: true},
			{Name: "group_id", SemanticPath: "groups.group_id", LogicalType: "string"},
			{Name: "group_label", SemanticPath: "groups.label", LogicalType: "string"},
			{Name: "group_mean_weight", SemanticPath: "groups.mean_weight", LogicalType: "decimal"},
			{Name: "group_ordinal", SemanticPath: "groups.ordinal", LogicalType: "integer"},
			{Name: "members", SemanticPath: "groups.members", LogicalType: "object", Repeated: true},
			{Name: "__loom_row_id", SemanticPath: "groups.identity", LogicalType: "object", LoomOwned: true},
			{Name: "auth_resource_path", SemanticPath: "auth.path", LogicalType: "string", LoomOwned: true},
			{Name: "patient_id", SemanticPath: "root.id", LogicalType: "string"},
		},
	}
	descriptor, columns, err := artifactDescriptor(receipt, materialization, "groups")
	if err != nil {
		t.Fatalf("group artifact descriptor: %v", err)
	}
	if descriptor.RowGrain != string(spec.RowGrainGroups) || descriptor.RowIdentity.Key != "__loom_row_id" || descriptor.RowIdentity.SourceIDColumn != "" || descriptor.RowIdentity.SourceResourceType != "" {
		t.Fatalf("group row identity descriptor = %#v, want synthetic GROUPS identity only", descriptor)
	}
	wantNames := []string{"group_id", "group_label", "group_mean_weight", "group_ordinal", "members"}
	if got := artifactColumnNames(columns); !slices.Equal(got, wantNames) {
		t.Fatalf("group artifact columns = %v, want %v", got, wantNames)
	}
	if got := columns[len(columns)-1]; got.LogicalType != "object" || got.Shape != "record_list" || !got.Repeated {
		t.Fatalf("membership artifact column = %#v, want repeated object records", got)
	}
	if got := columns[2].ResultUnit; got == nil || got.System != "http://unitsofmeasure.org" || got.Code != "kg" {
		t.Fatalf("group aggregate result unit = %#v, want UCUM kg", got)
	}
	wantIdentity := map[string]string{"group_revision_id": "grouprev-a", "group_id": "group-a"}
	wantMembers := []any{
		map[string]any{"source_identity": map[string]any{"project": "project-a", "generation": "generation-a", "resource_type": "Observation", "id": "obs-a"}, "payload": nil},
		map[string]any{"source_identity": map[string]any{"project": "project-a", "generation": "generation-a", "resource_type": "Observation", "id": "obs-b"}, "payload": nil},
	}
	var archive bytes.Buffer
	_, err = dataframepublished.WriteArtifact(context.Background(), &archive, dataframepublished.ArtifactRequest{
		Identity: dataframepublished.ArtifactIdentity{
			Project: "project-a", DatasetGeneration: "generation-a", ReceiptID: "receipt-a", ExecutionID: "execution-a",
			OutputID: "groups", RevisionID: "revision-a", SchemaDigest: "published-schema", OutputContractDigest: "contract-digest",
		},
		Descriptor: descriptor, Columns: columns, SelectionMetadata: json.RawMessage(`{}`),
		InterpretationMetadata: json.RawMessage(`[]`), Provenance: json.RawMessage(`{}`), Quality: json.RawMessage(`[]`),
	}, func(visit dataframepublished.ArtifactRowVisitor) error {
		return visit(map[string]any{
			"__loom_row_id": wantIdentity, "group_id": "group-a", "group_label": "Group A", "group_mean_weight": 71.5, "group_ordinal": int64(0), "members": wantMembers,
		})
	})
	if err != nil {
		t.Fatalf("write group artifact: %v", err)
	}
	archiveReader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("read group artifact: %v", err)
	}
	var exported struct {
		RowID  map[string]string          `json:"rowId"`
		Values map[string]json.RawMessage `json:"values"`
	}
	for _, member := range archiveReader.File {
		if member.Name != "data.jsonl" {
			continue
		}
		contents, readErr := member.Open()
		if readErr != nil {
			t.Fatalf("open group data member: %v", readErr)
		}
		data, readErr := io.ReadAll(contents)
		_ = contents.Close()
		if readErr != nil {
			t.Fatalf("read group data member: %v", readErr)
		}
		if err := json.Unmarshal(bytes.TrimSpace(data), &exported); err != nil {
			t.Fatalf("decode group data row: %v", err)
		}
		break
	}
	if !reflect.DeepEqual(exported.RowID, wantIdentity) {
		t.Fatalf("exported typed group identity = %#v, want %#v", exported.RowID, wantIdentity)
	}
	var exportedMembers []any
	if err := json.Unmarshal(exported.Values["members"], &exportedMembers); err != nil {
		t.Fatalf("decode exported members: %v", err)
	}
	if !reflect.DeepEqual(exportedMembers, wantMembers) {
		t.Fatalf("exported nested member tuples = %#v, want %#v", exportedMembers, wantMembers)
	}
}

func TestArtifactDescriptorCarriesResultUnitIntoZipManifest(t *testing.T) {
	receipt := &explorer.CompilationReceipt{
		ReceiptFormatVersion: 2, CompilerContractVersion: "test", SourceGeneration: "generation-a",
		ResolvedSchemaDigest: "resolved-schema", OutputContractDigest: "contract-digest",
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"patients","rowGrain":"patient","rowMultiplication":"none","columns":[{"column":"mean_weight","label":"Mean weight","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg"}},{"column":"bmi","label":"BMI","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg/m2"}},{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":false,"chartable":false}]}]}`),
		EmittedColumns: []explorer.EmittedColumn{
			{OutputID: "patients", EmissionID: "emit-mean-weight", PublicColumn: "mean_weight"},
			{OutputID: "patients", EmissionID: "emit-bmi", PublicColumn: "bmi"},
			{OutputID: "patients", EmissionID: "emit-patient-id", PublicColumn: "patient_id"},
		},
	}
	materialization := dataframepublished.Materialization{
		SchemaDigest: "published-schema",
		Columns: []dataframepublished.Column{
			{Name: "mean_weight", LogicalType: "decimal"},
			{Name: "bmi", LogicalType: "decimal"},
			{Name: "patient_id", LogicalType: "string"},
		},
	}
	descriptor, columns, err := artifactDescriptor(receipt, materialization, "patients")
	if err != nil {
		t.Fatalf("build artifact descriptor: %v", err)
	}
	if len(columns) != 3 || columns[0].ResultUnit == nil || columns[0].ResultUnit.System != "http://unitsofmeasure.org" || columns[0].ResultUnit.Code != "kg" || columns[1].ResultUnit == nil || columns[1].ResultUnit.Code != "kg/m2" || columns[2].ResultUnit != nil {
		t.Fatalf("artifact column result units = %#v", columns)
	}

	var archive bytes.Buffer
	_, err = dataframepublished.WriteArtifact(context.Background(), &archive, dataframepublished.ArtifactRequest{
		Identity: dataframepublished.ArtifactIdentity{
			Project: "project-a", DatasetGeneration: "generation-a", ReceiptID: "receipt-a", ExecutionID: "execution-a",
			OutputID: "patients", RevisionID: "revision-a", SchemaDigest: "published-schema", OutputContractDigest: "contract-digest",
		},
		Descriptor: descriptor, Columns: columns, Format: dataframepublished.ArtifactFormatJSONL,
		SelectionMetadata: json.RawMessage(`{}`), InterpretationMetadata: json.RawMessage(`[]`), Provenance: json.RawMessage(`{}`), Quality: json.RawMessage(`[]`),
	}, func(visit dataframepublished.ArtifactRowVisitor) error {
		return visit(map[string]any{"__loom_row_id": "row-1", "mean_weight": 71.5, "bmi": 22.4, "patient_id": "patient-1"})
	})
	if err != nil {
		t.Fatalf("write artifact ZIP: %v", err)
	}
	archiveReader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("read artifact ZIP: %v", err)
	}
	var manifestBytes []byte
	for _, member := range archiveReader.File {
		if member.Name != "manifest.json" {
			continue
		}
		contents, openErr := member.Open()
		if openErr != nil {
			t.Fatalf("open artifact manifest: %v", openErr)
		}
		manifestBytes, err = io.ReadAll(contents)
		_ = contents.Close()
		if err != nil {
			t.Fatalf("read artifact manifest: %v", err)
		}
		break
	}
	var manifest struct {
		Version    int                                   `json:"version"`
		Descriptor dataframepublished.ArtifactDescriptor `json:"descriptor"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode artifact manifest: %v", err)
	}
	if manifest.Version != 2 || len(manifest.Descriptor.Columns) != 3 {
		t.Fatalf("artifact manifest = %#v", manifest)
	}
	if got := manifest.Descriptor.Columns; got[0].ResultUnit == nil || got[0].ResultUnit.System != "http://unitsofmeasure.org" || got[0].ResultUnit.Code != "kg" || got[1].ResultUnit == nil || got[1].ResultUnit.Code != "kg/m2" || got[2].ResultUnit != nil {
		t.Fatalf("manifest column result units = %#v", got)
	}
	var manifestWire struct {
		Descriptor struct {
			Columns []map[string]json.RawMessage `json:"columns"`
		} `json:"descriptor"`
	}
	if err := json.Unmarshal(manifestBytes, &manifestWire); err != nil {
		t.Fatalf("decode artifact manifest JSON shape: %v", err)
	}
	if _, exists := manifestWire.Descriptor.Columns[2]["resultUnit"]; exists {
		t.Fatalf("unitless artifact column includes resultUnit: %s", manifestWire.Descriptor.Columns[2]["resultUnit"])
	}
}

func TestCanonicalGroupArtifactIdentityPreservesTypedCompilerFields(t *testing.T) {
	const canonical = `{"group_revision_id":"grouprev-a","group_id":"group-a"}`
	got, err := canonicalGroupArtifactIdentity(canonical)
	if err != nil {
		t.Fatalf("canonical group identity: %v", err)
	}
	want := map[string]string{"group_revision_id": "grouprev-a", "group_id": "group-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group identity = %#v, want %#v", got, want)
	}
	for _, invalid := range []string{
		`{"groupRevisionId":"grouprev-a","groupId":"group-a"}`,
		`{"group_revision_id":"grouprev-a","group_id":"group-a","extra":"value"}`,
		`{"group_revision_id":"grouprev-a","group_id":"group-a" }`,
		`{"group_revision_id":"grouprev-a","group_id":7}`,
	} {
		if _, err := canonicalGroupArtifactIdentity(invalid); err == nil {
			t.Errorf("accepted invalid group identity %s", invalid)
		}
	}
}

func TestArtifactQualityAcceptsLegacyStorageProjectIdentity(t *testing.T) {
	receipt := explorer.CompilationReceipt{ID: "receipt", Project: "study_program/project", SourceGeneration: "generation", AuthorizationScopeDigest: "scope"}
	revision := explorer.Revision{QualityReports: []publication.QualityReport{{
		ReceiptID: "receipt", Project: "study_program-project", DatasetGeneration: "generation", ScopeDigest: "scope", Output: "patients",
		PolicyVersion: publication.DefaultQualityPolicyVersion, Completeness: publication.QualityComplete, Verdict: publication.QualityPassed,
	}}}
	if _, err := artifactQualityReport(revision, "patients", receipt); err != nil {
		t.Fatalf("legacy storage identity was rejected: %v", err)
	}
}

func TestPrepareArtifactRejectsMaterializationFromBroaderScope(t *testing.T) {
	backend, artifacts, reader, snapshot, receipt := artifactTestFixture(t)
	reader.materialization.ScopeUnrestricted = false
	reader.materialization.AuthResourcePaths = []string{"/broader/scope"}
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: artifacts, PublishedReader: reader, Capability: CapabilityResolver{ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PrepareArtifact(context.Background(), ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "wrong-scope"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "MATERIALIZATION_IDENTITY_CHANGED" {
		t.Fatalf("scope mismatch error = %v", err)
	}
}

func TestPrepareArtifactAbortsAfterExactReaderFailure(t *testing.T) {
	backend, artifacts, reader, snapshot, receipt := artifactTestFixture(t)
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: artifacts, PublishedReader: reader, Capability: CapabilityResolver{ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	reader.err = errors.New("execution read pin lost")
	result, err := service.PrepareArtifact(context.Background(), ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "download-failure"})
	if err == nil || result.Record != nil {
		t.Fatalf("result=%#v err=%v, want failed preparation", result, err)
	}
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "EXECUTION_READ_PIN_LOST" {
		t.Fatalf("err=%v, want pin-loss code", err)
	}
	request := ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "download-failure"}
	// The failed record remains durable for deterministic retry behavior.
	identity := explorer.ArtifactRecord{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: request.RevisionID, OutputID: request.OutputID, ReceiptID: receipt.ID, ExecutionID: "execution-a", DatasetGeneration: receipt.SourceGeneration, SchemaDigest: "schema-a", AuthorizationScopeDigest: receipt.AuthorizationScopeDigest, IdempotencyKey: request.IdempotencyKey}
	id, _ := explorer.ArtifactID(identity)
	record, getErr := artifacts.Get(context.Background(), id)
	if getErr != nil || record.State != explorer.ArtifactFailed || record.FailureCode != "EXECUTION_READ_PIN_LOST" {
		t.Fatalf("failed artifact=%#v err=%v", record, getErr)
	}
	_, retryErr := service.PrepareArtifact(context.Background(), request)
	var retryLifecycleErr *Error
	if !errors.As(retryErr, &retryLifecycleErr) || retryLifecycleErr.Code != "ARTIFACT_FAILED_RETRYABLE" {
		t.Fatalf("retry err=%v, want retryable failed-artifact error", retryErr)
	}
}

func TestOpenArtifactReauthorizesCurrentToken(t *testing.T) {
	backend, artifacts, _, snapshot, receipt := artifactTestFixture(t)
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	wrongSnapshot := snapshot
	wrongSnapshot.Token = "wrong-token"
	service, err := New(domain, Config{ArtifactStore: artifacts, Capability: CapabilityResolver{ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: wrongSnapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// Build a complete record directly to isolate the download authorization
	// gate from preparation.
	record := explorer.ArtifactRecord{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", ReceiptID: receipt.ID, ExecutionID: "execution-a", DatasetGeneration: receipt.SourceGeneration, SchemaDigest: "schema-a", AuthorizationScopeDigest: receipt.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, IdempotencyKey: "open", State: explorer.ArtifactComplete, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	record.ID, _ = explorer.ArtifactID(record)
	artifacts.records[record.ID] = record
	artifacts.data[record.ID] = []byte("archive")
	reader, _, err := service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: record.ID})
	if reader != nil || err == nil {
		t.Fatalf("reader=%v err=%v, want authorization failure", reader, err)
	}
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassForbidden {
		t.Fatalf("err=%v", err)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
