package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
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
	materialization := dataframepublished.Materialization{Revision: "execution-a", ReceiptID: receipt.ID, SchemaDigest: "schema-a", Project: receipt.Project, DatasetGeneration: receipt.SourceGeneration, Name: "Patient", State: dataframepublished.StateReady, Columns: []dataframepublished.Column{{Name: "patient_id", LogicalType: "string"}, {Name: "auth_resource_path", LogicalType: "string"}}, SourceRow: &publication.SourceRowMetadata{ResourceType: "Patient", IDColumn: "patient_id"}}
	reader := &fakeArtifactReader{materialization: materialization, rows: []map[string]any{{"patient_id": "p1", "auth_resource_path": "private"}}}
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
	identity := explorer.ArtifactRecord{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: request.RevisionID, OutputID: request.OutputID, ReceiptID: receipt.ID, ExecutionID: "execution-a", IdempotencyKey: request.IdempotencyKey}
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
