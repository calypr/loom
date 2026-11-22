package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type faultArtifactStore struct {
	*memoryArtifactStore
	getErr     error
	openErr    error
	openRecord *explorer.ArtifactRecord
}

func (s *faultArtifactStore) Get(ctx context.Context, id string) (*explorer.ArtifactRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.memoryArtifactStore.Get(ctx, id)
}

func (s *faultArtifactStore) Open(ctx context.Context, id string) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	if s.openErr != nil {
		return nil, nil, s.openErr
	}
	if s.openRecord != nil {
		return nil, s.openRecord, nil
	}
	return s.memoryArtifactStore.Open(ctx, id)
}

type revisionErrorBackend struct {
	*artifactBackend
	err error
}

func (s *revisionErrorBackend) GetRevision(context.Context, string) (*explorer.Revision, error) {
	return nil, s.err
}

func artifactOpenRecord(receipt *explorer.CompilationReceipt, snapshotToken string) explorer.ArtifactRecord {
	record := explorer.ArtifactRecord{
		Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients",
		ReceiptID: receipt.ID, ExecutionID: "execution-a", DatasetGeneration: receipt.SourceGeneration,
		SchemaDigest: "schema-a", AuthorizationScopeDigest: receipt.AuthorizationScopeDigest,
		SnapshotToken: snapshotToken, IdempotencyKey: "open", State: explorer.ArtifactComplete,
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	record.ID, _ = explorer.ArtifactID(record)
	return record
}

func TestOpenArtifactStoreGetFailureIsUnavailable(t *testing.T) {
	backend, baseStore, _, _, receipt := artifactTestFixture(t)
	cause := errors.New("artifact store transport failure")
	store := &faultArtifactStore{memoryArtifactStore: baseStore, getErr: cause}
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: store})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: "artifact-missing"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "ARTIFACT_STORE_UNAVAILABLE" {
		t.Fatalf("store Get failure = %#v, want unavailable ARTIFACT_STORE_UNAVAILABLE", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("store Get failure lost its cause: %v", err)
	}
}

func TestOpenArtifactStoreGetNotFoundIsNotFound(t *testing.T) {
	backend, store, _, _, receipt := artifactTestFixture(t)
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: store})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: "artifact-missing"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassNotFound || lifecycleErr.Code != "ARTIFACT_NOT_FOUND" {
		t.Fatalf("store Get ErrNotFound = %#v, want not_found ARTIFACT_NOT_FOUND", err)
	}
	if !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("not-found error lost explorer.ErrNotFound: %v", err)
	}
}

func TestOpenArtifactStoreGetMissingFileIsNotFound(t *testing.T) {
	backend, baseStore, _, _, receipt := artifactTestFixture(t)
	store := &faultArtifactStore{memoryArtifactStore: baseStore, getErr: os.ErrNotExist}
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: store})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: "artifact-missing"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassNotFound || lifecycleErr.Code != "ARTIFACT_NOT_FOUND" {
		t.Fatalf("store Get os.ErrNotExist = %#v, want not_found ARTIFACT_NOT_FOUND", err)
	}
}

func TestOpenArtifactRevisionLookupFailureIsUnavailable(t *testing.T) {
	backend, store, _, snapshot, receipt := artifactTestFixture(t)
	cause := errors.New("revision store transport failure")
	record := artifactOpenRecord(receipt, snapshot.Token)
	store.records[record.ID] = record
	store.data[record.ID] = []byte("archive")
	domain, err := explorer.NewService(&revisionErrorBackend{artifactBackend: backend, err: cause})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: store, Capability: ports.CapabilityResolver{ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: record.ID})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "REVISION_STORE_UNAVAILABLE" {
		t.Fatalf("revision lookup failure = %#v, want unavailable REVISION_STORE_UNAVAILABLE", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("revision lookup failure lost its cause: %v", err)
	}
}

func TestPrepareArtifactRevisionLookupFailureIsUnavailable(t *testing.T) {
	backend, artifacts, reader, _, receipt := artifactTestFixture(t)
	cause := errors.New("revision store transport failure")
	domain, err := explorer.NewService(&revisionErrorBackend{artifactBackend: backend, err: cause})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: artifacts, PublishedReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PrepareArtifact(context.Background(), ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "revision-failure"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "REVISION_STORE_UNAVAILABLE" {
		t.Fatalf("revision lookup failure = %#v, want unavailable REVISION_STORE_UNAVAILABLE", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("revision lookup failure lost its cause: %v", err)
	}
}

func TestOpenArtifactStoreOpenFailureIsUnavailable(t *testing.T) {
	backend, baseStore, _, snapshot, receipt := artifactTestFixture(t)
	cause := errors.New("archive store transport failure")
	store := &faultArtifactStore{memoryArtifactStore: baseStore, openErr: cause}
	record := artifactOpenRecord(receipt, snapshot.Token)
	store.records[record.ID] = record
	store.data[record.ID] = []byte("archive")
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: store, Capability: ports.CapabilityResolver{ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: record.ID})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "ARTIFACT_STORE_UNAVAILABLE" {
		t.Fatalf("store Open failure = %#v, want unavailable ARTIFACT_STORE_UNAVAILABLE", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("store Open failure lost its cause: %v", err)
	}
}

func TestOpenArtifactIncompleteOpenResultIsUnavailable(t *testing.T) {
	backend, baseStore, _, snapshot, receipt := artifactTestFixture(t)
	record := artifactOpenRecord(receipt, snapshot.Token)
	stale := record
	stale.State = explorer.ArtifactFailed
	store := &faultArtifactStore{memoryArtifactStore: baseStore, openRecord: &stale}
	store.records[record.ID] = record
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{ArtifactStore: store, Capability: ports.CapabilityResolver{ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: record.ID})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "ARTIFACT_UNAVAILABLE" {
		t.Fatalf("incomplete open result = %#v, want unavailable ARTIFACT_UNAVAILABLE", err)
	}
}
