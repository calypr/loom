package published

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
)

type exactExportCatalog struct {
	publication.BundleCatalog
	execution publication.BundleExecution

	mu       sync.Mutex
	acquired []struct {
		executionID string
		owner       string
		expires     time.Time
	}
	renewed   int
	release   int
	acquireOK bool
	renewOK   bool
}

func (c *exactExportCatalog) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	if id != c.execution.ID {
		return publication.BundleExecution{}, publication.ErrBundleNotFound
	}
	return c.execution, nil
}

func (c *exactExportCatalog) AcquireExecutionReadPin(_ context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acquired = append(c.acquired, struct {
		executionID string
		owner       string
		expires     time.Time
	}{executionID: executionID, owner: owner, expires: expires})
	return c.acquireOK, nil
}

func (c *exactExportCatalog) RenewExecutionReadPin(_ context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.acquired) == 0 || c.acquired[0].executionID != executionID || c.acquired[0].owner != owner || !expires.After(time.Now()) {
		return false, nil
	}
	c.renewed++
	c.acquired[0].expires = expires
	return c.renewOK, nil
}

func (c *exactExportCatalog) ReleaseExecutionReadPin(_ context.Context, executionID, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.acquired) > 0 && c.acquired[0].executionID == executionID && c.acquired[0].owner == owner {
		c.release++
	}
	return nil
}

func (c *exactExportCatalog) ClaimExecutionCleanup(context.Context, string, string) (bool, error) {
	return true, nil
}

func (c *exactExportCatalog) RenewExecutionCleanup(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}

func (c *exactExportCatalog) ReleaseExecutionCleanup(context.Context, string, string) error {
	return nil
}

type exactExportQueryer struct {
	rows       []map[string]any
	query      string
	columns    []string
	args       []any
	started    chan struct{}
	released   chan struct{}
	waitForCtx bool
	err        error
}

func (q *exactExportQueryer) QueryRowsArgs(context.Context, string, []string, ...any) ([]map[string]any, error) {
	return nil, errors.New("unexpected page query")
}

func (q *exactExportQueryer) QueryRowsArgsVisit(ctx context.Context, query string, columns []string, visit func(map[string]any) error, args ...any) error {
	q.query = query
	q.columns = append([]string(nil), columns...)
	q.args = append([]any(nil), args...)
	if q.started != nil {
		close(q.started)
	}
	if q.waitForCtx {
		<-ctx.Done()
		return ctx.Err()
	}
	if q.err != nil {
		return q.err
	}
	for _, row := range q.rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if q.released != nil {
		close(q.released)
	}
	return nil
}

func exactExportExecution() publication.BundleExecution {
	verifiedAt := time.Now().UTC()
	return publication.BundleExecution{
		ID: "execution-a",
		BundleIdentity: publication.BundleIdentity{
			Name:               "recipe",
			TranslationVersion: "v1",
			Project:            "study/project",
			DatasetGeneration:  "generation-a",
			ReceiptID:          "receipt-a",
			SchemaDigest:       "schema-a",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name:          "patients",
			PhysicalTable: "loom_bundle_execution_a_patients",
			State:         publication.BundlePublished,
			VerifiedAt:    &verifiedAt,
			SourceRow:     &publication.SourceRowMetadata{ResourceType: "Patient", IDColumn: "patient_id"},
			Columns: []publication.PhysicalColumn{
				{Name: "patient_id", ClickHouse: "String"},
				{Name: "score", ClickHouse: "Float64"},
				{Name: "auth_resource_path", ClickHouse: "String", LoomOwned: true},
			},
		}},
	}
}

func exactExportRequest() ExactExportRequest {
	return ExactExportRequest{
		ExecutionID:       "execution-a",
		OutputID:          "patients",
		Project:           "study/project",
		DatasetGeneration: "generation-a",
		ReceiptID:         "receipt-a",
		SchemaDigest:      "schema-a",
		Columns:           []string{"patient_id", "score"},
		Filters:           []Filter{{Column: "score", Op: "GT", Value: 10}},
		Sort:              &Sort{Column: "score", Desc: true},
		AuthResourcePaths: []string{"/study/project/Patient/patient-a"},
		ReaderID:          "artifact-reader-a",
		PinExpiresAt:      time.Now().UTC().Add(time.Minute),
	}
}

func TestStreamExactExportStreamsExactIdentityWithResolvedScope(t *testing.T) {
	catalog := &exactExportCatalog{execution: exactExportExecution(), acquireOK: true, renewOK: true}
	queryer := &exactExportQueryer{rows: []map[string]any{{"patient_id": "patient-a", "score": 42.5}, {"patient_id": "patient-b", "score": 18.25}}}
	reader := &Reader{Catalog: catalog, ClickHouse: queryer}
	request := exactExportRequest()
	var got []map[string]any
	result, err := reader.StreamExactExport(context.Background(), request, func(row map[string]any) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionID != request.ExecutionID || result.OutputID != request.OutputID || result.ReaderID != request.ReaderID {
		t.Fatalf("result identity = %#v", result)
	}
	if result.Materialization.Revision != request.ExecutionID || result.Materialization.Project != request.Project || result.Materialization.ReceiptID != request.ReceiptID {
		t.Fatalf("materialization identity = %#v", result.Materialization)
	}
	if result.Rows != 2 || !reflect.DeepEqual(got, queryer.rows) {
		t.Fatalf("rows = %d %#v, want 2 %#v", result.Rows, got, queryer.rows)
	}
	if !strings.Contains(queryer.query, "loom_bundle_execution_a_patients") || !strings.Contains(queryer.query, "`auth_resource_path` IN") {
		t.Fatalf("query = %q", queryer.query)
	}
	if !reflect.DeepEqual(queryer.columns, []string{"patient_id", "score", "__loom_row_id"}) {
		t.Fatalf("query columns = %#v", queryer.columns)
	}
	if len(catalog.acquired) != 1 || catalog.acquired[0].owner != request.ReaderID {
		t.Fatalf("acquired pins = %#v", catalog.acquired)
	}
	if catalog.release != 1 {
		t.Fatalf("release count = %d, want 1", catalog.release)
	}
}

func TestStreamExactExportRejectsIdentityMismatchBeforePin(t *testing.T) {
	catalog := &exactExportCatalog{execution: exactExportExecution(), acquireOK: true, renewOK: true}
	reader := &Reader{Catalog: catalog, ClickHouse: &exactExportQueryer{}}
	request := exactExportRequest()
	request.SchemaDigest = "schema-other"
	_, err := reader.StreamExactExport(context.Background(), request, func(map[string]any) error { return nil })
	if !errors.Is(err, publication.ErrSelectionSourceIdentityChanged) {
		t.Fatalf("error = %v, want identity mismatch", err)
	}
	if len(catalog.acquired) != 0 {
		t.Fatalf("pins acquired before identity validation: %#v", catalog.acquired)
	}
}

func TestStreamExactExportReturnsPinLossAndReleasesPin(t *testing.T) {
	catalog := &exactExportCatalog{execution: exactExportExecution(), acquireOK: true, renewOK: false}
	started := make(chan struct{})
	queryer := &exactExportQueryer{started: started, waitForCtx: true}
	reader := &Reader{Catalog: catalog, ClickHouse: queryer}
	request := exactExportRequest()
	request.PinRenewalInterval = time.Millisecond
	_, err := reader.StreamExactExport(context.Background(), request, func(map[string]any) error { return nil })
	if !errors.Is(err, publication.ErrExecutionReadPinLost) {
		t.Fatalf("error = %v, want pin loss", err)
	}
	if catalog.release != 1 {
		t.Fatalf("release count = %d, want 1", catalog.release)
	}
	select {
	case <-started:
	default:
		t.Fatal("stream did not start")
	}
}

func TestStreamExactExportPropagatesCancellationWithoutSuccess(t *testing.T) {
	catalog := &exactExportCatalog{execution: exactExportExecution(), acquireOK: true, renewOK: true}
	started := make(chan struct{})
	queryer := &exactExportQueryer{started: started, waitForCtx: true}
	reader := &Reader{Catalog: catalog, ClickHouse: queryer}
	request := exactExportRequest()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := reader.StreamExactExport(ctx, request, func(map[string]any) error { return nil })
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if catalog.release != 1 {
		t.Fatalf("release count = %d, want 1", catalog.release)
	}
}
