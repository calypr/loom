package artifactfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer"
)

func artifactCompletionFor(raw []byte, rows int64, features int) explorer.ArtifactCompletion {
	digest := sha256.Sum256(raw)
	return explorer.ArtifactCompletion{ArchiveSHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(raw)), Rows: rows, Features: features}
}

func artifactRecord(t *testing.T) explorer.ArtifactRecord {
	t.Helper()
	record := explorer.ArtifactRecord{
		Project: "study/project", ExplorerID: "explorer", RevisionID: "revision", OutputID: "patients",
		ReceiptID: "receipt", ExecutionID: "execution", IdempotencyKey: "request-1",
		DatasetGeneration: "generation", SchemaDigest: "schema", AuthorizationScopeDigest: "scope", ExpiresAt: time.Now().UTC().Add(time.Hour),
		Format: published.ArtifactFormatCSV,
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
	archive := []byte("archive")
	if _, err := stage.Write(archive); err != nil {
		t.Fatal(err)
	}
	completion := artifactCompletionFor(archive, 2, 3)
	completed, err := stage.Commit(context.Background(), completion)
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
	if string(raw) != "archive" || got.ArchiveSHA256 != completion.ArchiveSHA256 || got.Rows != 2 || got.Features != 3 {
		t.Fatalf("artifact=%q record=%#v", raw, got)
	}
	stage, existing, err = reopened.Begin(context.Background(), record)
	if err != nil || stage != nil || existing == nil || existing.State != explorer.ArtifactComplete {
		t.Fatalf("idempotent begin stage=%T existing=%#v err=%v", stage, existing, err)
	}
}

func TestStoreOpenRejectsArchiveDigestMismatch(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := artifactRecord(t)
	stage, _, err := store.Begin(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("archive")
	if _, err := stage.Write(original); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Commit(context.Background(), artifactCompletionFor(original, 1, 1)); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(store.path(record.ID, ".zip"), []byte("tamper!"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, _, err := store.Open(context.Background(), record.ID)
	if reader != nil {
		_ = reader.Close()
	}
	if err == nil {
		t.Fatal("Open exposed a changed archive under the committed digest")
	}
}

func TestStoreOpenRejectsArchiveByteCountMismatch(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := artifactRecord(t)
	stage, _, err := store.Begin(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("archive")
	if _, err := stage.Write(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Commit(context.Background(), artifactCompletionFor(archive, 1, 1)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Bytes++
	if err := store.writeRecord(*stored); err != nil {
		t.Fatal(err)
	}
	reader, _, err := store.Open(context.Background(), record.ID)
	if reader != nil {
		_ = reader.Close()
	}
	if err == nil {
		t.Fatal("Open exposed an archive whose byte count disagrees with its record")
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
	retry, existing, err := store.Begin(context.Background(), record)
	if err != nil || retry != nil || existing == nil || existing.State != explorer.ArtifactFailed || existing.FailureCode != "STREAM_FAILED" {
		t.Fatalf("idempotent failed artifact begin stage=%T existing=%#v err=%v", retry, existing, err)
	}

	record.IdempotencyKey = "request-2"
	record.ID, _ = explorer.ArtifactID(record)
	retry, existing, err = store.Begin(context.Background(), record)
	if err != nil || retry == nil || existing != nil {
		t.Fatalf("new-key artifact retry stage=%T existing=%#v err=%v", retry, existing, err)
	}
	_, _ = retry.Abort(context.Background(), "RETRY_STOPPED")

	expired := artifactRecord(t)
	expired.IdempotencyKey = "expired"
	expired.ID, _ = explorer.ArtifactID(expired)
	expired.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if _, _, err := store.Begin(context.Background(), expired); err == nil {
		t.Fatal("expired artifact record was accepted")
	}
}

func TestStoreReplacesExpiredCompleteArtifact(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	record := artifactRecord(t)
	record.ExpiresAt = now.Add(time.Hour)
	stage, _, err := store.Begin(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	oldArchive := []byte("old")
	_, _ = stage.Write(oldArchive)
	if _, err := stage.Commit(context.Background(), artifactCompletionFor(oldArchive, 0, 0)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	record.ExpiresAt = now.Add(time.Hour)
	retry, existing, err := store.Begin(context.Background(), record)
	if err != nil || retry == nil || existing != nil {
		t.Fatalf("expired artifact retry stage=%T existing=%#v err=%v", retry, existing, err)
	}
	if _, err := os.Stat(store.path(record.ID, ".zip")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired archive remained visible: %v", err)
	}
}
