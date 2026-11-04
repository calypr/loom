package clickhouse

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataset"

	publication "github.com/calypr/loom/internal/dataframe/publication"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
)

func TestToColumnsStoresLogicalObjectsAsLosslessJSONText(t *testing.T) {
	got, err := toColumns([]publication.LogicalColumn{
		{Name: "profile", Kind: "object", Nullable: true},
		{Name: "owners", Kind: "object", Repeated: true},
		{Name: "label", Kind: "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []storeclickhouse.Column{
		{Name: "profile", Type: "Nullable(String)", LogicalType: "object"},
		{Name: "owners", Type: "Array(Nullable(String))", LogicalType: "object"},
		{Name: "label", Type: "String"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("physical columns = %#v, want %#v", got, want)
	}
}

type reconcileRecheckCatalog struct {
	execution     publication.BundleExecution
	publishedExec *publication.BundleExecution
	pointers      []publication.BundlePointer
	pointerErrs   []error
	pointerCalls  int
	released      int
	acquiredKeys  []string
	savedStates   []publication.BundleState
	savedErrors   []string
}

func (c *reconcileRecheckCatalog) SaveExecution(_ context.Context, execution publication.BundleExecution, _ string) error {
	c.savedStates = append(c.savedStates, execution.State)
	c.savedErrors = append(c.savedErrors, execution.Error)
	return nil
}

func (c *reconcileRecheckCatalog) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (c *reconcileRecheckCatalog) FindExecutionByKey(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (c *reconcileRecheckCatalog) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	index := c.pointerCalls
	c.pointerCalls++
	if index >= len(c.pointers) {
		index = len(c.pointers) - 1
	}
	return c.pointers[index], c.pointerErrs[index]
}

func (c *reconcileRecheckCatalog) CompareAndSwapPointer(context.Context, string, string, string) error {
	return nil
}

func (c *reconcileRecheckCatalog) PublishExecution(context.Context, string, string, publication.BundleExecution) error {
	return nil
}

func (c *reconcileRecheckCatalog) ListExecutions(context.Context, publication.BundleState, time.Time) ([]publication.BundleExecution, error) {
	return nil, nil
}

func (c *reconcileRecheckCatalog) AcquireBundleLease(_ context.Context, key, _ string, _ time.Time) (bool, error) {
	c.acquiredKeys = append(c.acquiredKeys, key)
	return true, nil
}

func (c *reconcileRecheckCatalog) RenewBundleLease(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}

func (c *reconcileRecheckCatalog) ReleaseBundleLease(_ context.Context, _ string, _ string) error {
	c.released++
	return nil
}

func (c *reconcileRecheckCatalog) VisitExecutionPages(_ context.Context, state publication.BundleState, _ time.Time, _ int, visit publication.BundleExecutionPageFunc) error {
	if state == publication.BundlePublished {
		if c.publishedExec == nil {
			return nil
		}
		return visit([]publication.BundleExecution{*c.publishedExec})
	}
	if state != publication.BundleRunning || c.execution.ID == "" {
		return nil
	}
	return visit([]publication.BundleExecution{c.execution})
}

type reconcileRecheckClickHouse struct {
	dropped []string
}

func (f *reconcileRecheckClickHouse) CreateTable(context.Context, string, []storeclickhouse.Column) error {
	return nil
}

func (f *reconcileRecheckClickHouse) InsertRows(context.Context, string, []storeclickhouse.Column, []map[string]any) error {
	return nil
}

func (f *reconcileRecheckClickHouse) VerifyOutput(context.Context, string, []storeclickhouse.Column, int64) error {
	return nil
}

func (f *reconcileRecheckClickHouse) DropTable(_ context.Context, table string) error {
	f.dropped = append(f.dropped, table)
	return nil
}

func (f *reconcileRecheckClickHouse) DropColumns(context.Context, string, []string) error {
	return nil
}

type reconcileRecheckReleaseReader struct {
	active dataset.ActiveRelease
	err    error
	calls  int
}

func (r *reconcileRecheckReleaseReader) ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error) {
	r.calls++
	return r.active, r.err
}

func TestReconcileDeniesDropOfActiveReleaseBoundExecution(t *testing.T) {
	bind := func(executionID string) dataset.ActiveRelease {
		return dataset.ActiveRelease{
			Release:  dataset.ProjectRelease{Publications: []dataset.ReleasePublication{{ExecutionID: executionID}}},
			Revision: 1,
		}
	}
	tests := []struct {
		name        string
		reader      *reconcileRecheckReleaseReader
		wireReader  bool
		latePointer *publication.BundlePointer
		wantDropped int
		wantSaved   bool
		wantError   bool
	}{
		{
			name:        "active release binds the candidate -> drop vetoed",
			reader:      &reconcileRecheckReleaseReader{active: bind("exec-reconcile-recheck")},
			wireReader:  true,
			wantDropped: 0,
		},
		{
			name:        "release binds only other executions -> drop proceeds",
			reader:      &reconcileRecheckReleaseReader{active: bind("exec-someone-else")},
			wireReader:  true,
			wantDropped: 1,
			wantSaved:   true,
		},
		{
			name:        "late pointer re-read names the candidate -> drop vetoed",
			reader:      &reconcileRecheckReleaseReader{active: bind("exec-someone-else")},
			wireReader:  true,
			latePointer: &publication.BundlePointer{ExecutionID: "exec-reconcile-recheck"},
			wantDropped: 0,
		},
		{
			name:        "active release read fails -> drop vetoed fail-closed with error",
			reader:      &reconcileRecheckReleaseReader{err: errors.New("release store down")},
			wireReader:  true,
			wantDropped: 0,
			wantError:   true,
		},
		{
			name:        "no active release -> drop proceeds",
			reader:      &reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease},
			wireReader:  true,
			wantDropped: 1,
			wantSaved:   true,
		},
		{
			name:        "unwired reader keeps historical sweep behavior",
			wireReader:  false,
			wantDropped: 1,
			wantSaved:   true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := publication.BundleExecution{
				ID:  "exec-reconcile-recheck",
				Key: "bundle:exec-reconcile-recheck",
				BundleIdentity: publication.BundleIdentity{
					Project: "reconcile-recheck", Name: "orders", DatasetGeneration: "gen", OutputName: "main",
				},
				State: publication.BundleRunning,
				Outputs: []publication.BundleOutputRecord{
					{Name: "main", PhysicalTable: "loom_bundle_exec_reconcile_recheck"},
				},
			}
			catalog := &reconcileRecheckCatalog{
				execution:   execution,
				pointers:    []publication.BundlePointer{{}, {}},
				pointerErrs: []error{publication.ErrBundleNotFound, publication.ErrBundleNotFound},
			}
			clickhouseClient := &reconcileRecheckClickHouse{}
			store, err := NewBundleStore(clickhouseClient, catalog)
			if err != nil {
				t.Fatalf("NewBundleStore: %v", err)
			}
			if test.wireReader {
				store.SetActiveReleaseReader(test.reader)
			}
			if test.latePointer != nil {
				catalog.pointers[1] = *test.latePointer
				catalog.pointerErrs[1] = nil
			}
			err = store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour))
			if (err != nil) != test.wantError {
				t.Fatalf("Reconcile error = %v, wantError = %v", err, test.wantError)
			}
			if len(clickhouseClient.dropped) != test.wantDropped {
				t.Fatalf("dropped tables = %v, want %d drops", clickhouseClient.dropped, test.wantDropped)
			}
			if gotSaved := len(catalog.savedStates) > 0; gotSaved != test.wantSaved {
				t.Fatalf("SaveExecution states = %v, wantSaved = %v", catalog.savedStates, test.wantSaved)
			}
			if test.wantSaved && catalog.savedStates[0] != publication.BundleFailed {
				t.Fatalf("demoted state = %q, want FAILED", catalog.savedStates[0])
			}
			if catalog.released != 1 {
				t.Fatalf("ReleaseBundleLease calls = %d, want the bundle lease released exactly once", catalog.released)
			}
		})
	}
}

func TestReconcileRetiresSupersededPublishedExecutions(t *testing.T) {
	const candidateID = "exec-superseded"
	published := func() publication.BundleExecution {
		return publication.BundleExecution{
			ID:  candidateID,
			Key: "bundle:" + candidateID,
			BundleIdentity: publication.BundleIdentity{
				Project: "orphans", Name: "orders", DatasetGeneration: "gen", OutputName: "main",
			},
			State: publication.BundlePublished,
			Outputs: []publication.BundleOutputRecord{
				{Name: "main", PhysicalTable: "loom_bundle_exec_superseded"},
			},
		}
	}
	bind := func(executionID string) dataset.ActiveRelease {
		return dataset.ActiveRelease{
			Release:  dataset.ProjectRelease{Publications: []dataset.ReleasePublication{{ExecutionID: executionID}}},
			Revision: 3,
		}
	}
	namedByPointer := &publication.BundlePointer{ExecutionID: candidateID}
	tests := []struct {
		name         string
		reader       *reconcileRecheckReleaseReader
		wireReader   bool
		firstPointer *publication.BundlePointer
		active       *dataset.ActiveRelease
		wantErr      bool
		wantAcquired int
		wantDropped  int
		wantSaved    bool
	}{
		{
			name:         "superseded+unbound+unpublished-by-pointer -> retired",
			reader:       &reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease},
			wireReader:   true,
			wantAcquired: 1,
			wantDropped:  1,
			wantSaved:    true,
		},
		{
			name:         "pointer names the published candidate -> never claimed",
			reader:       &reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease},
			wireReader:   true,
			firstPointer: namedByPointer,
		},
		{
			name:       "active release binds the candidate -> never claimed",
			reader:     &reconcileRecheckReleaseReader{},
			wireReader: true,
			active:     &dataset.ActiveRelease{Release: bind(candidateID).Release, Revision: 3},
		},
		{
			name:       "active release read fails -> fail-closed, never claimed",
			reader:     &reconcileRecheckReleaseReader{err: errors.New("release store down")},
			wireReader: true,
			wantErr:    true,
		},
		{
			name:       "unwired reader -> PUBLISHED not enumerated at all (blind janitor forbidden)",
			wireReader: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := published()
			catalog := &reconcileRecheckCatalog{
				publishedExec: &candidate,
				pointers:      []publication.BundlePointer{{}, {}},
				pointerErrs:   []error{publication.ErrBundleNotFound, publication.ErrBundleNotFound},
			}
			if test.firstPointer != nil {
				catalog.pointers[0] = *test.firstPointer
				catalog.pointerErrs[0] = nil
			}
			clickhouseClient := &reconcileRecheckClickHouse{}
			store, err := NewBundleStore(clickhouseClient, catalog)
			if err != nil {
				t.Fatalf("NewBundleStore: %v", err)
			}
			reader := test.reader
			if reader == nil {
				reader = &reconcileRecheckReleaseReader{}
			}
			if test.active != nil {
				reader.active = *test.active
				reader.err = nil
			}
			if test.wireReader {
				store.SetActiveReleaseReader(reader)
			}
			err = store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour))
			if (err != nil) != test.wantErr {
				t.Fatalf("Reconcile error = %v, wantErr = %v", err, test.wantErr)
			}
			if len(catalog.acquiredKeys) != test.wantAcquired {
				t.Fatalf("claimed keys = %v, want %d claims", catalog.acquiredKeys, test.wantAcquired)
			}
			if len(clickhouseClient.dropped) != test.wantDropped {
				t.Fatalf("dropped tables = %v, want %d drops", clickhouseClient.dropped, test.wantDropped)
			}
			if gotSaved := len(catalog.savedStates) > 0; gotSaved != test.wantSaved {
				t.Fatalf("SaveExecution states = %v, wantSaved = %v", catalog.savedStates, test.wantSaved)
			}
			if test.wantSaved {
				if catalog.savedStates[0] != publication.BundleFailed {
					t.Fatalf("retired state = %q, want FAILED", catalog.savedStates[0])
				}
				if !strings.Contains(catalog.savedErrors[0], "superseded execution reconciled") {
					t.Fatalf("retired marker = %q, want the superseded janitor marker", catalog.savedErrors[0])
				}
			}
			if catalog.released != test.wantAcquired {
				t.Fatalf("ReleaseBundleLease calls = %d, want one per claim (%d)", catalog.released, test.wantAcquired)
			}
		})
	}
	// The stale (non-PUBLISHED) arm keeps its shipped marker verbatim.
	t.Run("stale arm marker unchanged", func(t *testing.T) {
		catalog := &reconcileRecheckCatalog{
			execution: publication.BundleExecution{
				ID:  "exec-stale",
				Key: "bundle:exec-stale",
				BundleIdentity: publication.BundleIdentity{
					Project: "orphans", Name: "orders", DatasetGeneration: "gen", OutputName: "main",
				},
				State: publication.BundleRunning,
				Outputs: []publication.BundleOutputRecord{
					{Name: "main", PhysicalTable: "loom_bundle_exec_stale"},
				},
			},
			pointers:    []publication.BundlePointer{{}, {}},
			pointerErrs: []error{publication.ErrBundleNotFound, publication.ErrBundleNotFound},
		}
		clickhouseClient := &reconcileRecheckClickHouse{}
		store, err := NewBundleStore(clickhouseClient, catalog)
		if err != nil {
			t.Fatalf("NewBundleStore: %v", err)
		}
		store.SetActiveReleaseReader(&reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease})
		if err := store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if len(catalog.savedStates) != 1 || catalog.savedStates[0] != publication.BundleFailed {
			t.Fatalf("saved = %v, want single FAILED", catalog.savedStates)
		}
		if catalog.savedErrors[0] != "stale execution reconciled" {
			t.Fatalf("marker = %q, want the verbatim stale marker", catalog.savedErrors[0])
		}
	})
}
