package server

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
)

type statusExecutionCatalog struct {
	publication.BundleCatalog
	executions []publication.BundleExecution
}

func (c statusExecutionCatalog) ListExecutions(_ context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	result := make([]publication.BundleExecution, 0)
	for _, execution := range c.executions {
		if execution.State.Canonical() == state.Canonical() && execution.UpdatedAt.Before(before) {
			result = append(result, execution)
		}
	}
	return result, nil
}

func TestReleaseProjectStatusResolverDistinguishesReleaseAndAttemptStates(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	store := dataset.NewMemoryLifecycleStore()
	release := dataset.ProjectRelease{ID: "release-1", Project: "current", Generation: "g1", GitCommit: "g1", Publications: []dataset.ReleasePublication{{Selector: selector, ExecutionID: "published-1", Generation: "g1", VerifiedAt: now}}}
	if _, err := store.SaveRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapActivateRelease(ctx, release, 0); err != nil {
		t.Fatal(err)
	}
	catalog := statusExecutionCatalog{executions: []publication.BundleExecution{
		{ID: "replacement-failed", BundleIdentity: publication.BundleIdentity{Name: "core", TranslationVersion: "v1", Project: "current", DatasetGeneration: "g2"}, State: publication.BundleFailed, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second), FailureCode: "PUBLICATION_FAILED", Outputs: []publication.BundleOutputRecord{{Name: "Patient", Selector: selector}}},
		{ID: "queued-1", BundleIdentity: publication.BundleIdentity{Name: "core", TranslationVersion: "v1", Project: "building", DatasetGeneration: "g2"}, State: publication.BundleQueued, CreatedAt: now, UpdatedAt: now},
		{ID: "failed-1", BundleIdentity: publication.BundleIdentity{Name: "core", TranslationVersion: "v1", Project: "failed", DatasetGeneration: "g3"}, State: publication.BundleFailed, CreatedAt: now, UpdatedAt: now, FailureCode: "PUBLICATION_FAILED", Outputs: []publication.BundleOutputRecord{{Name: "Patient", Selector: selector}}},
	}}
	resolver := releaseProjectStatusResolver{releases: store, executions: catalog}
	statuses, err := resolver.DataframeProjectStatuses(ctx, []string{"current", "building", "failed", "missing"}, selector)
	if err != nil {
		t.Fatal(err)
	}
	want := []published.ProjectState{published.ProjectFailed, published.ProjectBuilding, published.ProjectFailed, published.ProjectMissing}
	for index := range want {
		if statuses[index].State != want[index] {
			t.Fatalf("statuses[%d] = %#v, want %s", index, statuses[index], want[index])
		}
	}
	ids, err := resolver.ActiveReleaseExecutionIDs(ctx, []string{"current", "missing"}, selector)
	if err != nil || ids["current"] != "published-1" {
		t.Fatalf("active release IDs = %#v, %v", ids, err)
	}
	selectors, controlled, err := resolver.ActiveReleaseSelectors(ctx, []string{"current", "missing"})
	if err != nil || len(selectors) != 1 || selectors[0] != selector || !controlled["current"] || controlled["missing"] {
		t.Fatalf("active release selectors = %#v controlled=%#v err=%v", selectors, controlled, err)
	}
}
