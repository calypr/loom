package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/dataset"
	datasetpostgres "github.com/calypr/loom/internal/dataset/postgres"
	"github.com/calypr/loom/internal/explorer"
)

// ---------------------------------------------------------------------------
// T12: the deadlock-retry contract, pinned. Classification is by sentinel
// (never by the statement that failed), the bounded fresh-transaction retry
// really re-runs, and an exhausted budget surfaces ErrPublishContention.
// ---------------------------------------------------------------------------

func TestPublishAuthoringIsDeadlockErrorClassification(t *testing.T) {
	deadlock := &pgconn.PgError{Code: publishDeadlockState, Message: "deadlock detected"}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bare 40P01", deadlock, true},
		{"wrapped 40P01", fmt.Errorf("lock prior revision: %w", deadlock), true},
		{"doubly wrapped 40P01", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", deadlock)), true},
		{"joined with 40P01", errors.Join(errors.New("context"), deadlock), true},
		{"unique violation 23505", &pgconn.PgError{Code: "23505", Message: "duplicate key"}, false},
		{"serialization-adjacent 40001", &pgconn.PgError{Code: "40001"}, false},
		{"plain error", errors.New("connection reset by peer"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isDeadlockError(tc.err); got != tc.want {
			t.Errorf("%s: isDeadlockError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPublishAuthoringTypedRefusalClassification(t *testing.T) {
	// Every publish refusal sentinel is terminal, through arbitrary
	// wrapping -- and nothing else classifies as terminal.
	typed := []error{
		explorer.ErrCorruptReceipt,
		explorer.ErrNotFound,
		explorer.ErrImmutableRevision,
		explorer.ErrDraftConflict,
		dataset.ErrReleaseActivationConflict,
		datasetpostgres.ErrUnexpectedStoreResult,
	}
	for _, sentinel := range typed {
		err := fmt.Errorf("postgres explorer store: publish authoring: leg: %w", sentinel)
		if !isTypedPublicationRefusal(err) {
			t.Errorf("sentinel %v wrapped is not classified terminal", sentinel)
		}
	}
	for _, nonTyped := range []error{
		errors.New("connection reset"),
		&pgconn.PgError{Code: "40P01", Message: "deadlock detected"},
		fmt.Errorf("wrapped: %w", errors.New("io")),
	} {
		if isTypedPublicationRefusal(nonTyped) {
			t.Errorf("%v misclassified as a typed refusal: it must stay visible to the retry loop", nonTyped)
		}
	}
}

// injectFault publishes with a crashAt seam that fails the first nAttempts
// attempts with fault (nil fault disables injection) and reports how many
// transactions reached the seam.
func injectFault(t *testing.T, f *pubAuthFixture, ps *PublishStore, nAttempts int, fault error,
) (seamHits *int) {
	t.Helper()
	hits := 0
	seamHits = &hits
	ps.crashAt = func(ctx context.Context, tx pgx.Tx, step string) error {
		if step != "receipt-insert" {
			return nil
		}
		hits++
		if hits <= nAttempts {
			return fault
		}
		return nil
	}
	return seamHits
}

func TestPublishAuthoringDeadlockVictimRetriesOnFreshTransaction(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	project, explorerID, tag := "proj_pubauth_dl_retry", "exp_dl", "dlr"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
	ps := f.newPublishStore(t)
	deadlock := &pgconn.PgError{Code: publishDeadlockState, Message: "deadlock detected"}
	seamHits := injectFault(t, f, ps, 1, deadlock)

	out, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
	if err != nil {
		t.Fatalf("deadlock victim was not recovered: %v", err)
	}
	if out.Status != explorer.RevisionActive {
		t.Fatalf("recovered publish status = %s, want ACTIVE", out.Status)
	}
	if *seamHits != 2 {
		t.Fatalf("seam reached %d times, want 2 (one deadlocked attempt + one retry)", *seamHits)
	}
	// The deadlocked attempt wrote nothing: the committed world is exactly
	// what an uninterrupted single run stores.
	fg := newPGPubAuthFixture(t)
	fg.migrate(t)
	gReceipt, gRevision, gRelease := fg.seedPublishedWorld(t, fg.newStore(t), project, explorerID, tag)
	fg.publishOK(t, fg.newPublishStore(t), gReceipt, gRevision, gRelease, 0)
	if got, want := f.snapshotForProject(t, project), fg.snapshotForProject(t, project); got != want {
		t.Fatalf("retried publish diverged from the uninterrupted golden:\nretried:\n%s\ngolden:\n%s", got, want)
	}
}

func TestPublishAuthoringExhaustedDeadlocksSurfaceErrPublishContention(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	project, explorerID, tag := "proj_pubauth_dl_exhaust", "exp_dl", "dlex"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
	ps := f.newPublishStore(t)
	deadlock := &pgconn.PgError{Code: publishDeadlockState, Message: "deadlock detected"}
	seamHits := injectFault(t, f, ps, publishTxMaxAttempts+1, deadlock)
	before := f.snapshotAll(t, false)

	_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
	if !errors.Is(err, ErrPublishContention) {
		t.Fatalf("exhausted deadlocks error = %v, want ErrPublishContention", err)
	}
	if !isDeadlockError(err) {
		t.Fatalf("contention error must still wrap the last 40P01 victim: %v", err)
	}
	if *seamHits != publishTxMaxAttempts {
		t.Fatalf("seam reached %d times, want the full budget %d", *seamHits, publishTxMaxAttempts)
	}
	if after := f.snapshotAll(t, false); after != before {
		t.Fatalf("every deadlocked attempt must have rolled back completely:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestPublishAuthoringNonDeadlockFaultIsNotRetried(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	project, explorerID, tag := "proj_pubauth_dl_plain", "exp_dl", "dlp"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
	ps := f.newPublishStore(t)
	seamHits := injectFault(t, f, ps, 1, errors.New("connection reset by peer"))

	_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
	if err == nil || errors.Is(err, ErrPublishContention) {
		t.Fatalf("plain backend fault = %v, want the fault once, unlabeled as contention", err)
	}
	if *seamHits != 1 {
		t.Fatalf("non-deadlock fault retried %d times, want 1", *seamHits)
	}
}

func TestPublishAuthoringTypedRefusalAtBackendSiteIsTerminal(t *testing.T) {
	// A sentinel carried out of a statement the retry loop would otherwise
	// retry still terminates after one transaction: classification follows
	// the sentinel, not the statement.
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	project, explorerID, tag := "proj_pubauth_dl_typed", "exp_dl", "dlt"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
	ps := f.newPublishStore(t)
	typed := fmt.Errorf("store fault while holding a lock: %w", explorer.ErrImmutableRevision)
	seamHits := injectFault(t, f, ps, publishTxMaxAttempts+1, typed)

	_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
	if !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("injected typed refusal = %v, want ErrImmutableRevision terminal", err)
	}
	if *seamHits != 1 {
		t.Fatalf("typed refusal retried %d times, want 1 (terminal by sentinel)", *seamHits)
	}
}
