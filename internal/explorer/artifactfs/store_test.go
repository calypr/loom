package artifactfs

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func artifactRecord(t *testing.T) explorer.ArtifactRecord {
	t.Helper()
	record := explorer.ArtifactRecord{
		Project: "study/project", ExplorerID: "explorer", RevisionID: "revision", OutputID: "patients",
		ReceiptID: "receipt", ExecutionID: "execution", IdempotencyKey: "request-1",
		DatasetGeneration: "generation", SchemaDigest: "schema", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	id, err := explorer.ArtifactID(record)
	if err != nil {
		t.Fatal(err)
	}
	record.ID = id
	return record
}

func TestStorePublishesOnlyCommittedArtifactAndSurvivesReopen(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	record := artifactRecord(t)
	stage, existing, err := store.Begin(context.Background(), record)
	if err != nil || existing != nil {
		t.Fatalf("begin stage=%T existing=%#v err=%v", stage, existing, err)
	}
	if _, _, err := store.Open(context.Background(), record.ID); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("pre-commit open error = %v", err)
	}
	if _, err := stage.Write([]byte("archive")); err != nil {
		t.Fatal(err)
	}
	completed, err := stage.Commit(context.Background(), explorer.ArtifactCompletion{ArchiveSHA256: "sha256:value", Bytes: 7, Rows: 2, Features: 3})
	if err != nil || completed.State != explorer.ArtifactComplete {
		t.Fatalf("commit=%#v err=%v", completed, err)
	}
	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, got, err := reopened.Open(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	raw, _ := io.ReadAll(reader)
	if string(raw) != "archive" || got.ArchiveSHA256 != "sha256:value" || got.Rows != 2 || got.Features != 3 {
		t.Fatalf("artifact=%q record=%#v", raw, got)
	}
	stage, existing, err = reopened.Begin(context.Background(), record)
	if err != nil || stage != nil || existing == nil || existing.State != explorer.ArtifactComplete {
		t.Fatalf("idempotent begin stage=%T existing=%#v err=%v", stage, existing, err)
	}
}

func TestStoreAbortAndExpiryNeverExposeArchive(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := artifactRecord(t)
	stage, _, err := store.Begin(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stage.Write([]byte("partial"))
	failed, err := stage.Abort(context.Background(), "STREAM_FAILED")
	if err != nil || failed.State != explorer.ArtifactFailed || failed.FailureCode != "STREAM_FAILED" {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	if _, err := os.Stat(store.path(record.ID, ".zip")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial archive became visible: %v", err)
	}
	if _, _, err := store.Open(context.Background(), record.ID); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("failed artifact open error = %v", err)
	}

	expired := artifactRecord(t)
	expired.IdempotencyKey = "expired"
	expired.ID, _ = explorer.ArtifactID(expired)
	expired.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if _, _, err := store.Begin(context.Background(), expired); err == nil {
		t.Fatal("expired artifact record was accepted")
	}
}
