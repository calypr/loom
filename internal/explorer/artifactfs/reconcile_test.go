package artifactfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func reconcileClock(base time.Time) *time.Time {
	clock := base
	return &clock
}

func completeArtifact(t *testing.T, store *Store, execution, request string, expiresAt time.Time) explorer.ArtifactRecord {
	t.Helper()
	record := artifactRecord(t)
	record.ExecutionID = execution
	record.IdempotencyKey = request
	record.ExpiresAt = expiresAt
	id, err := explorer.ArtifactID(record)
	if err != nil {
		t.Fatal(err)
	}
	record.ID = id
	stage, existing, err2 := store.Begin(context.Background(), record)
	err = err2
	if err != nil || existing != nil || stage == nil {
		t.Fatalf("begin(execution=%s) stage=%T existing=%#v err=%v", execution, stage, existing, err)
	}
	if _, err := stage.Write([]byte("archive-bytes")); err != nil {
		t.Fatal(err)
	}
	completed, err := stage.Commit(context.Background(), explorer.ArtifactCompletion{ArchiveSHA256: "sha256:reconcile", Bytes: 13, Rows: 1, Features: 1})
	if err != nil || completed.State != explorer.ArtifactComplete {
		t.Fatalf("commit(execution=%s): %#v err=%v", execution, completed, err)
	}
	return *completed
}

func diskSuffixes(t *testing.T, root, id string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), id) {
			found = append(found, strings.TrimPrefix(entry.Name(), id))
		}
	}
	return found
}

func TestReconcileRemovesExpiredPairsAndKeepsLiveArtifacts(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	clock := reconcileClock(base)
	store.now = func() time.Time { return *clock }

	expired := completeArtifact(t, store, "execution-expired", "request-expired", base.Add(time.Hour))
	aborted := artifactRecord(t)
	aborted.ExecutionID = "execution-aborted"
	aborted.IdempotencyKey = "request-aborted"
	aborted.ExpiresAt = base.Add(time.Hour)
	abortedID, err := explorer.ArtifactID(aborted)
	if err != nil {
		t.Fatal(err)
	}
	aborted.ID = abortedID
	stage, existing, err := store.Begin(context.Background(), aborted)
	if err != nil || existing != nil || stage == nil {
		t.Fatalf("begin aborted: stage=%T existing=%#v err=%v", stage, existing, err)
	}
	if _, err := stage.Abort(context.Background(), "CANCELED"); err != nil {
		t.Fatal(err)
	}
	live := completeArtifact(t, store, "execution-live", "request-live", base.Add(30*24*time.Hour))

	if got := diskSuffixes(t, root, expired.ID); len(got) != 2 {
		t.Fatalf("expired artifact pre-sweep disk state = %v, want .zip and .json", got)
	}

	*clock = base.Add(2 * time.Hour)
	deleted, err := store.Reconcile(context.Background(), *clock, 100)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if deleted != 2 {
		t.Errorf("reconcile deleted=%d, want 2 (expired complete + expired aborted pair)", deleted)
	}
	if got := diskSuffixes(t, root, expired.ID); len(got) != 0 {
		t.Errorf("expired artifact files retained after sweep: %v", got)
	}
	if got := diskSuffixes(t, root, aborted.ID); len(got) != 0 {
		t.Errorf("aborted artifact metadata retained past expiry: %v", got)
	}
	if _, err := store.Get(context.Background(), expired.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Get after sweep err=%v, want ErrNotExist", err)
	}
	reader, record, err := store.Open(context.Background(), live.ID)
	if err != nil || record == nil {
		t.Fatalf("live artifact after sweep: %v", err)
	}
	_ = reader.Close()
	if got := diskSuffixes(t, root, live.ID); len(got) != 2 {
		t.Errorf("live artifact files = %v, want retained .zip and .json", got)
	}
	if again, err := store.Reconcile(context.Background(), *clock, 100); err != nil || again != 0 {
		t.Errorf("second sweep deleted=%d err=%v, want 0/<nil> (converged)", again, err)
	}
}

func TestReconcileHonorsLimitAndContext(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	clock := reconcileClock(base)
	store.now = func() time.Time { return *clock }
	for i := 0; i < 3; i++ {
		completeArtifact(t, store, "execution-limit-"+string(rune('a'+i)), "request-limit", base.Add(time.Hour))
	}
	*clock = base.Add(2 * time.Hour)
	if deleted, err := store.Reconcile(context.Background(), *clock, 1); err != nil || deleted != 1 {
		t.Fatalf("bounded sweep deleted=%d err=%v, want 1/<nil>", deleted, err)
	}
	if deleted, err := store.Reconcile(context.Background(), *clock, 10); err != nil || deleted != 2 {
		t.Fatalf("final sweep deleted=%d err=%v, want 2/<nil>", deleted, err)
	}
	if _, err := store.Reconcile(context.Background(), *clock, 0); err == nil {
		t.Error("non-positive limit accepted, want error")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Reconcile(canceled, *clock, 10); err == nil {
		t.Error("canceled context accepted, want error")
	}
}

func TestReconcileLeavesUnprovableFilesAlone(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	foreignKey := "artifact_" + strings.Repeat("a", 64)
	for _, name := range []string{"notes.txt", foreignKey + ".zip", "artifact_" + strings.Repeat("b", 63) + ".zip", ".hidden"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, foreignKey+".json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Reconcile(context.Background(), base.Add(365*24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("deleted=%d, want 0 (identity/harmlessness unprovable without a readable record)", deleted)
	}
	for _, name := range []string{"notes.txt", foreignKey + ".zip", foreignKey + ".json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("unprovable file %s removed by reconcile: %v", name, err)
		}
	}
}

func TestReconcileRetainsSetWhenRecordBecomesUnreadableUnderLock(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	const n = 3000
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rec := completeArtifact(t, store, fmt.Sprintf("exec-noise-%06d", i), "key-noise", base.Add(time.Hour))
		names = append(names, rec.ID)
	}
	targetExec := ""
	for c := 0; ; c++ {
		rec := artifactRecord(t)
		rec.ExecutionID = fmt.Sprintf("target-%06d", c)
		rec.IdempotencyKey = "key-target"
		rec.ExpiresAt = base.Add(time.Hour)
		id, err := explorer.ArtifactID(rec)
		if err != nil {
			t.Fatal(err)
		}
		last := true
		for _, name := range names {
			if id+".json" < name+".json" {
				last = false
				break
			}
		}
		if last {
			targetExec = rec.ExecutionID
			break
		}
	}
	target := completeArtifact(t, store, targetExec, "key-target", base.Add(time.Hour))
	now := base.Add(2 * time.Hour)

	type result struct {
		deleted int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		deleted, err := store.Reconcile(context.Background(), now, n+10)
		done <- result{deleted, err}
	}()
	witness := filepath.Join(root, names[n-1]+".json")
	targetJSON := filepath.Join(root, target.ID+".json")
	deadline := time.Now().Add(60 * time.Second)
	sawTailDeletion := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(witness); errors.Is(err, os.ErrNotExist) {
			sawTailDeletion = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !sawTailDeletion {
		t.Fatal("delete loop never reached the tail")
	}
	if err := os.Chmod(targetJSON, 0o000); err != nil {
		t.Fatal(err)
	}
	got := <-done
	_ = os.Chmod(targetJSON, 0o600)
	if _, err := os.Stat(targetJSON); err != nil {
		t.Fatalf("set with unreadable record under lock was removed (fail-open regression): stat: %v", err)
	}
	if got.deleted > n {
		t.Errorf("deleted=%d exceeds peer count %d despite unreadable target", got.deleted, n)
	}
	if _, err := os.Stat(filepath.Join(root, target.ID+".zip")); err != nil {
		t.Fatalf("expired archive of retained set disappeared: %v", err)
	}
}

func TestReconcileKeepsRecordWhenArchiveRemovalFailsAndConvergesAfterRepair(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	clock := reconcileClock(base)
	store.now = func() time.Time { return *clock }
	rec := completeArtifact(t, store, "exec-wedge", "key-wedge", base.Add(time.Hour))
	zipPath := filepath.Join(root, rec.ID+".zip")
	if err := os.Remove(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(zipPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zipPath, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	*clock = base.Add(2 * time.Hour)
	if _, err := store.Reconcile(context.Background(), *clock, 100); err == nil {
		t.Fatal("sweep with un-removable archive reported clean success")
	}
	if _, err := os.Stat(filepath.Join(root, rec.ID+".json")); err != nil {
		t.Fatalf("record removed while archive removal failed; next sweep loses its queueing proof: %v", err)
	}
	blocker := filepath.Join(zipPath, "blocker")
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, []byte("archive-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Reconcile(context.Background(), *clock, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("convergence sweep deleted=%d err=%v, want 1/<nil>", deleted, err)
	}
	if got := diskSuffixes(t, root, rec.ID); len(got) != 0 {
		t.Errorf("set not fully reclaimed after repair: %v", got)
	}
}

func TestBeginReclaimsCrashResidueStagingWithoutRecord(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	record := artifactRecord(t)
	record.ExecutionID = "exec-crash"
	record.IdempotencyKey = "key-crash"
	record.ExpiresAt = base.Add(time.Hour)
	id, err := explorer.ArtifactID(record)
	if err != nil {
		t.Fatal(err)
	}
	record.ID = id
	if err := os.WriteFile(filepath.Join(root, id+".staging"), []byte("partial-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, existing, err := store.Begin(context.Background(), record)
	if err != nil || existing != nil || stage == nil {
		t.Fatalf("begin over crash-residue staging: stage=%T existing=%#v err=%v", stage, existing, err)
	}
	if _, err := stage.Write([]byte("full-archive")); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.Commit(context.Background(), explorer.ArtifactCompletion{ArchiveSHA256: "sha256:crash", Bytes: 12, Rows: 1, Features: 1}); err != nil {
		t.Fatal(err)
	}
	reader, got, err := store.Open(context.Background(), id)
	if err != nil || got == nil {
		t.Fatalf("open after crash-recovery begin: %v", err)
	}
	_ = reader.Close()
}

func TestReconcileNeverRemovesFilesWhoseRecordDisownsTheirName(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	store.now = func() time.Time { return base }
	expired := base.Add(-time.Hour)

	foreign := artifactRecord(t)
	foreign.ID = "notes"
	foreign.ExecutionID = "execution-foreign-name"
	foreign.IdempotencyKey = "request-foreign-name"
	foreign.ExpiresAt = expired
	body, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.zip"), []byte("user-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	aliased := artifactRecord(t)
	aliased.ExecutionID = "execution-aliased"
	aliased.IdempotencyKey = "request-aliased"
	aliased.ExpiresAt = expired
	trueID, err := explorer.ArtifactID(aliased)
	if err != nil {
		t.Fatal(err)
	}
	aliased.ID = trueID
	body, err = json.Marshal(aliased)
	if err != nil {
		t.Fatal(err)
	}
	aliasID := "artifact_" + strings.Repeat("c", 64)
	if err := os.WriteFile(filepath.Join(root, aliasID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, aliasID+".zip"), []byte("decoy"), 0o600); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.Reconcile(context.Background(), base, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("deleted=%d, want 0: foreign-named and aliased records are not this store's artifacts", deleted)
	}
	for _, name := range []string{"notes.json", "notes.zip", aliasID + ".json", aliasID + ".zip"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("file %s removed although it is not the artifact its bytes name: %v", name, err)
		}
	}
}

func TestReconcileStopsMidSweepOnCancellation(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	clock := reconcileClock(base)
	store.now = func() time.Time { return *clock }
	for i := 0; i < 3; i++ {
		completeArtifact(t, store, "execution-midcancel-"+string(rune('a'+i)), "request-midcancel", base.Add(time.Hour))
	}
	*clock = base.Add(2 * time.Hour)
	calls := 0
	store.ctxCheck = func(ctx context.Context) error {
		calls++
		if ctx == nil {
			t.Error("mid-sweep check received nil context")
		}
		if calls >= 2 {
			return context.Canceled
		}
		return nil
	}
	deleted, err := store.Reconcile(context.Background(), *clock, 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-sweep cancellation error = %v, want context.Canceled", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d after mid-sweep cancellation, want 1", deleted)
	}
	if calls != 2 {
		t.Fatalf("per-set checks ran %d times, want stop at the second", calls)
	}
	remaining := 0
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			remaining++
		}
	}
	if remaining != 2 {
		t.Fatalf("remaining records=%d after cancellation, want the untouched 2", remaining)
	}
}
