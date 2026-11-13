package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	arangostore "github.com/calypr/loom/internal/backends/arango/internal/store"
	publication "github.com/calypr/loom/internal/dataset"
)

type casConflictRowQueryer struct {
	conflicts    int
	calls        int
	rows         []map[string]any
	conflictErr  error
	otherErr     error
	visitedBinds []map[string]any
}

func (q *casConflictRowQueryer) QueryRows(_ context.Context, _ string, _ int, binds map[string]interface{}, visitor arangostore.RowVisitor) error {
	q.calls++
	q.visitedBinds = append(q.visitedBinds, binds)
	if q.calls <= q.conflicts {
		if q.conflictErr != nil {
			return q.conflictErr
		}
		return shared.ArangoError{HasError: true, Code: 409, ErrorNum: shared.ErrArangoConflict, ErrorMessage: "write-write conflict"}
	}
	if q.otherErr != nil {
		return q.otherErr
	}
	for _, row := range q.rows {
		if err := visitor(row); err != nil {
			return err
		}
	}
	return nil
}

func casConvergedRow() map[string]any {
	encoded, err := json.Marshal(publication.ActiveRelease{
		Release:  publication.ProjectRelease{ID: "ab10-release-2", Project: "ab10-p2", Generation: "gen-a", GitCommit: "gen-a"},
		Revision: 2,
	})
	if err != nil {
		panic(err)
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		panic(err)
	}
	return row
}

func casTestRelease() publication.ProjectRelease {
	return publication.ProjectRelease{ID: "ab10-release-2", Project: "ab10-p2", Generation: "gen-a", GitCommit: "gen-a"}
}

func TestCompareAndSwapActivateReleaseRetriesConflictAbort(t *testing.T) {
	for _, tc := range []struct {
		name      string
		conflicts int
		wantCalls int
	}{
		{name: "no abort", conflicts: 0, wantCalls: 1},
		{name: "three aborts then converge", conflicts: 3, wantCalls: 4},
		{name: "budget exactly reached", conflicts: casConflictRetryBudget, wantCalls: casConflictRetryBudget + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queryer := &casConflictRowQueryer{conflicts: tc.conflicts, rows: []map[string]any{casConvergedRow()}}
			active, err := CompareAndSwapActivateRelease(context.Background(), queryer, casTestRelease(), 1)
			if err != nil {
				t.Fatalf("CAS after %d aborts: %v", tc.conflicts, err)
			}
			if active.Revision != 2 || active.Release.ID != "ab10-release-2" {
				t.Fatalf("CAS converged on (%s, revision %d), want (ab10-release-2, revision 2)", active.Release.ID, active.Revision)
			}
			if queryer.calls != tc.wantCalls {
				t.Fatalf("driver visited %d times, want %d", queryer.calls, tc.wantCalls)
			}
			for i, binds := range queryer.visitedBinds {
				if binds["expected_revision"] != int64(1) || binds["next_revision"] != int64(2) {
					t.Fatalf("retry %d changed the CAS inputs: %v/%v", i, binds["expected_revision"], binds["next_revision"])
				}
			}
		})
	}
}

func TestCompareAndSwapActivateReleaseExhaustedBudgetStaysTypedConflict(t *testing.T) {
	queryer := &casConflictRowQueryer{conflicts: casConflictRetryBudget + 3}
	_, err := CompareAndSwapActivateRelease(context.Background(), queryer, casTestRelease(), 1)
	if !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("exhausted retry budget returned %v, want the typed activation conflict", err)
	}
	if queryer.calls != casConflictRetryBudget+1 {
		t.Fatalf("driver visited %d times, want the budget plus the first attempt (%d)", queryer.calls, casConflictRetryBudget+1)
	}
}

func TestCompareAndSwapActivateReleaseCancelStopsRetryAfterFirstAbort(t *testing.T) {
	queryer := &casConflictRowQueryer{conflicts: casConflictRetryBudget + 3}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CompareAndSwapActivateRelease(cancelled, queryer, casTestRelease(), 1)
	if !errors.Is(err, publication.ErrReleaseActivationConflict) {
		t.Fatalf("cancelled CAS returned %v, want the typed activation conflict", err)
	}
	if queryer.calls != 1 {
		t.Fatalf("cancelled CAS retried %d times, want 1", queryer.calls)
	}
}

func TestCompareAndSwapActivateReleaseNonConflictErrorIsNotRetried(t *testing.T) {
	queryer := &casConflictRowQueryer{otherErr: fmt.Errorf("boom")}
	_, err := CompareAndSwapActivateRelease(context.Background(), queryer, casTestRelease(), 1)
	if err == nil || !errors.Is(err, queryer.otherErr) {
		t.Fatalf("plain failure returned %v, want the wrapped driver error", err)
	}
	if queryer.calls != 1 {
		t.Fatalf("plain failure retried %d times, want 1", queryer.calls)
	}
}
