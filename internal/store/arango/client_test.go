package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/google/uuid"
)

func TestProfileCorpusAgainstArango(t *testing.T) {
	url, database, project := profileArangoSettings(t)

	client, err := Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())

	corpus := profileCorpus(project)
	for _, shape := range corpus {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			explain, err := client.Explain(ctx, ExplainRequest{Query: shape.query, BindVars: shape.bindVars})
			if err != nil {
				t.Fatalf("EXPLAIN %s: %v\nAQL:\n%s", shape.name, err, shape.query)
			}
			if explain.Plan == nil && len(explain.Plans) == 0 {
				t.Fatalf("EXPLAIN %s returned no plan", shape.name)
			}
			assessment := AssessExplainResult(explain)
			t.Logf("%s explain: plans=%d full_scans=%d indexes=%d", shape.name, len(assessment.Plans), len(assessment.FullCollectionScans), len(assessment.Indexes))
			if shape.name == "root" && !assessmentHasIndexField(assessment, "Patient", "project") {
				t.Fatalf("root profile corpus did not select a project index: %#v", assessment.Indexes)
			}
			if shape.name != "root" && !assessmentHasIndexCollection(assessment, "fhir_edge") {
				t.Fatalf("%s profile corpus did not report an edge index: %#v", shape.name, assessment.Indexes)
			}

			profile, err := client.Profile(ctx, ProfileRequest{
				Query:     shape.query,
				BindVars:  shape.bindVars,
				BatchSize: 1000,
				Count:     true,
				Options:   ProfileOptions{Profile: 2},
			})
			if err != nil {
				t.Fatalf("PROFILE %s: %v\nAQL:\n%s", shape.name, err, shape.query)
			}
			if len(profile.Extra.Stats.Nodes) == 0 {
				t.Fatalf("PROFILE %s returned no execution-node statistics", shape.name)
			}
			summary := SummarizeProfile(profile)
			t.Logf("%s profile: rows=%d runtime=%0.6fs scanned_full=%d scanned_index=%d top=%s", shape.name, len(profile.Result), summary.RuntimeSeconds, summary.ScannedFull, summary.ScannedIndex, profileTopNode(summary))
		})
	}
}

func TestCompoundEdgeIndexExplainAgainstArango(t *testing.T) {
	url, database, project := profileArangoSettings(t)

	client, err := Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	cases := []struct {
		name          string
		query         string
		bindVars      map[string]any
		expectedIndex []string
	}{
		{
			name: "inbound-equality",
			query: `FOR edge IN fhir_edge
  FILTER edge._to == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.from_type == @from_type
  RETURN edge._key`,
			bindVars:      map[string]any{"root": "Patient/nope", "project": project, "generation": nil, "label": "subject_Patient", "from_type": "Specimen"},
			expectedIndex: []string{"_to", "project", "dataset_generation", "label", "from_type"},
		},
		{
			name: "outbound-equality",
			query: `FOR edge IN fhir_edge
  FILTER edge._from == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.to_type == @to_type
  RETURN edge._key`,
			bindVars:      map[string]any{"root": "ResearchSubject/nope", "project": project, "generation": nil, "label": "study", "to_type": "ResearchStudy"},
			expectedIndex: []string{"_from", "project", "dataset_generation", "label", "to_type"},
		},
		{
			name: "inbound-multi-type",
			query: `FOR edge IN fhir_edge
  FILTER edge._to == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.from_type IN @from_types
  RETURN edge._key`,
			bindVars: map[string]any{"root": "Patient/nope", "project": project, "generation": nil, "label": "subject_Patient", "from_types": []string{"Condition", "Specimen", "Observation"}},
		},
		{
			name: "outbound-multi-type",
			query: `FOR edge IN fhir_edge
  FILTER edge._from == @root
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @generation
  FILTER edge.label == @label
  FILTER edge.to_type IN @to_types
  RETURN edge._key`,
			bindVars: map[string]any{"root": "ResearchSubject/nope", "project": project, "generation": nil, "label": "study", "to_types": []string{"ResearchStudy", "DocumentReference"}},
		},
	}
	for _, shape := range cases {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			explain, err := client.Explain(ctx, ExplainRequest{Query: shape.query, BindVars: shape.bindVars})
			if err != nil {
				t.Fatal(err)
			}
			assessment := AssessExplainResult(explain)
			if len(assessment.FullCollectionScans) != 0 {
				t.Fatalf("compound edge shape used full scan: %#v", assessment.FullCollectionScans)
			}
			if len(assessment.Indexes) == 0 {
				t.Fatalf("compound edge shape selected no index: %#v", assessment)
			}
			if len(shape.expectedIndex) != 0 && !assessmentHasExactIndex(assessment, "fhir_edge", shape.expectedIndex) {
				t.Fatalf("equality shape did not select endpoint-first compound index %v: %#v", shape.expectedIndex, assessment.Indexes)
			}
			t.Logf("indexes=%#v plans=%#v optimizer_rules=%#v", assessment.Indexes, assessment.Plans, assessment.AppliedOptimizerRules)
		})
	}
}

func TestReceiptPreviewExplainAgainstArango(t *testing.T) {
	url, database, project := profileArangoSettings(t)

	client, err := Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	for _, shape := range receiptPreviewCorpus(project) {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := client.Explain(ctx, ExplainRequest{Query: shape.query, BindVars: shape.bindVars})
			if err != nil {
				t.Fatalf("EXPLAIN %s: %v\nAQL:\n%s", shape.name, err, shape.query)
			}
			assessment := AssessExplainResult(result)
			if result.Plan == nil && len(result.Plans) == 0 {
				t.Fatalf("EXPLAIN %s returned no plan", shape.name)
			}
			t.Logf("receipt preview shape=%s indexes=%#v full_scans=%#v", shape.name, assessment.Indexes, assessment.FullCollectionScans)
		})
	}
}

func receiptPreviewCorpus(project string) []profileCorpusShape {
	return []profileCorpusShape{
		{
			name: "project-generation-sort-limit",
			query: `FOR root IN Patient
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  SORT root._key ASC
  LIMIT @limit
  RETURN {"_key": root._key}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "limit": 5},
		},
		{
			name: "restricted-scope-sort-limit",
			query: `FOR root IN Patient
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  FILTER root.auth_resource_path IN @auth_paths
  SORT root._key ASC
  LIMIT @limit
  RETURN {"_key": root._key}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "auth_paths": []string{"Patient/one", "Patient/two"}, "limit": 5},
		},
		{
			name: "relationship-inbound-direction",
			query: `FOR root IN Patient
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  SORT root._key ASC
  LIMIT @limit
  LET related = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge._to == root._id
    FILTER edge.project == @project AND edge.dataset_generation == @generation
    FILTER edge.label == @label
    RETURN node._key)
  RETURN {"_key": root._key, "related": related}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "label": "subject_Patient", "limit": 5},
		},
		{
			name: "relationship-outbound-direction",
			query: `FOR root IN ResearchSubject
  FILTER root.project == @project
  FILTER root.dataset_generation == @generation
  SORT root._key ASC
  LIMIT @limit
  LET related = (FOR node, edge IN 1..1 OUTBOUND root fhir_edge
    FILTER edge._from == root._id
    FILTER edge.project == @project AND edge.dataset_generation == @generation
    FILTER edge.label == @label
    RETURN node._key)
  RETURN {"_key": root._key, "related": related}`,
			bindVars: map[string]any{"project": project, "generation": "generation-a", "label": "study", "limit": 5},
		},
	}
}

func assessmentHasIndexCollection(assessment ExplainAssessment, collection string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection == collection {
			return true
		}
	}
	return false
}

func assessmentHasIndexField(assessment ExplainAssessment, collection, field string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection != collection {
			continue
		}
		for _, candidate := range index.Fields {
			if candidate == field {
				return true
			}
		}
	}
	return false
}

func assessmentHasExactIndex(assessment ExplainAssessment, collection string, fields []string) bool {
	for _, index := range assessment.Indexes {
		if index.Collection == collection && strings.Join(index.Fields, "\x00") == strings.Join(fields, "\x00") {
			return true
		}
	}
	return false
}

type profileCorpusShape struct {
	name     string
	query    string
	bindVars map[string]any
}

func profileCorpus(project string) []profileCorpusShape {
	bind := func(root string) map[string]any {
		return map[string]any{
			"@root_collection":                 root,
			"project":                          project,
			"dataset_generation":               "",
			"auth_resource_paths_unrestricted": true,
			"auth_resource_paths":              []string{},
			"limit":                            5,
			"label":                            "subject_Patient",
		}
	}
	root := `FOR root IN @@root_collection
  FILTER root.project == @project
  FILTER @dataset_generation == "" OR root.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths
  SORT root._key
  LIMIT @limit
  RETURN {"_key": root._key}`
	sibling := `FOR root IN @@root_collection
  FILTER root.project == @project
  SORT root._key
  LIMIT @limit
  LET conditions = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label AND node.resourceType == "Condition"
    RETURN node._key)
  LET specimens = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label AND node.resourceType == "Specimen"
    RETURN node._key)
  LET observations = (FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label AND node.resourceType == "Observation"
    RETURN node._key)
  RETURN {"_key": root._key, "conditions": conditions, "specimens": specimens, "observations": observations}`
	deep := `FOR root IN @@root_collection
  FILTER root.project == @project
  SORT root._key
  LIMIT @limit
  LET descendants = (FOR first, firstEdge IN 1..1 INBOUND root fhir_edge
    FILTER firstEdge.project == @project AND first.project == @project
    FOR second, secondEdge IN 1..1 INBOUND first fhir_edge
      FILTER secondEdge.project == @project AND second.project == @project
      RETURN second._key)
  RETURN {"_key": root._key, "descendants": UNIQUE(descendants)}`
	required := `FOR root IN @@root_collection
  FILTER root.project == @project
  FILTER LENGTH(FOR node, edge IN 1..1 INBOUND root fhir_edge
    FILTER edge.project == @project AND node.project == @project
    FILTER edge.label == @label
    LIMIT 1
    RETURN 1) > 0
  SORT root._key
  LIMIT @limit
  RETURN {"_key": root._key}`
	pivot := `FOR root IN @@root_collection
  FILTER root.project == @project
  SORT root._key
  LIMIT @limit
  LET coding = root.payload.code.coding ? root.payload.code.coding : []
  LET pivot = (FOR value IN coding
    COLLECT key = value.system INTO grouped
    RETURN {"key": key, "value": FIRST(grouped).value.code})
  RETURN {"_key": root._key, "pivot": pivot}`
	return []profileCorpusShape{
		{name: "root", query: root, bindVars: bind("Patient")},
		{name: "sibling", query: sibling, bindVars: bind("Patient")},
		{name: "deep", query: deep, bindVars: bind("Patient")},
		{name: "required", query: required, bindVars: bind("Patient")},
		{name: "pivot", query: pivot, bindVars: bind("Observation")},
	}
}

func profileTopNode(summary ProfileSummary) string {
	if len(summary.Nodes) == 0 {
		return "none"
	}
	node := summary.Nodes[0]
	return strings.Join([]string{node.Type, "id=" + strconv.FormatInt(node.ID, 10), "runtime=" + strconv.FormatFloat(node.Runtime, 'f', 6, 64)}, " ")
}

func profileArangoSettings(t *testing.T) (url, database, project string) {
	t.Helper()
	url = strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	database = strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_DATABASE"))
	if url == "" && database == "" {
		if strings.TrimSpace(os.Getenv("LOOM_TEST_REQUIRED")) == "1" {
			t.Fatalf("required integration service configuration is missing: LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
		}
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE to run against the provisioned Arango corpus")
	}
	if url == "" || database == "" {
		t.Fatalf("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must be set together")
	}
	endpoint, err := neturl.Parse(url)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		t.Fatalf("invalid LOOM_TEST_ARANGO_URL %q", url)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/_db/" + neturl.PathEscape(database) + "/_api/database/current"
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("check provisioned Arango database %q: %v", database, err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("provisioned Arango database %q does not exist: HTTP %d", database, response.StatusCode)
	}
	project = os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	return url, database, project
}

type fakeQueryer struct {
	driver.DatabaseQuery
	cursor  driver.Cursor
	err     error
	called  int
	options *driver.QueryOptions
}

func (q *fakeQueryer) Query(_ context.Context, _ string, options *driver.QueryOptions) (driver.Cursor, error) {
	q.called++
	q.options = options
	if q.err != nil {
		return nil, q.err
	}
	return q.cursor, nil
}

type fakeCursor struct {
	driver.Cursor
	rows        []map[string]any
	readErr     error
	closeErr    error
	closeCount  int
	closeCtx    context.Context
	closeCtxErr error
	hasMore     int
	readCount   int
	onRead      func()
}

func (c *fakeCursor) HasMore() bool {
	c.hasMore++
	return c.readCount < len(c.rows)
}

func (c *fakeCursor) ReadDocument(_ context.Context, result interface{}) (driver.DocumentMeta, error) {
	c.readCount++
	if c.onRead != nil {
		c.onRead()
	}
	if c.readErr != nil {
		return driver.DocumentMeta{}, c.readErr
	}
	if c.readCount > len(c.rows) {
		return driver.DocumentMeta{}, shared.NoMoreDocumentsError{}
	}
	*(result.(*map[string]any)) = c.rows[c.readCount-1]
	return driver.DocumentMeta{}, nil
}

func (c *fakeCursor) Close() error {
	c.closeCount++
	return c.closeErr
}

func (c *fakeCursor) CloseWithContext(ctx context.Context) error {
	c.closeCount++
	c.closeCtx = ctx
	c.closeCtxErr = ctx.Err()
	return c.closeErr
}

func TestQueryRowsChecksCancellationBeforeQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queryer := &fakeQueryer{cursor: &fakeCursor{}}
	if err := queryRows(ctx, queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if queryer.called != 0 {
		t.Fatalf("query called %d times", queryer.called)
	}
}

func TestQueryRowsWithMaxRuntimeBoundsServerQueryAndCursorCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}, {"id": "2"}}}
	queryer := &fakeQueryer{cursor: cursor}
	visited := 0
	err := queryRowsWithMaxRuntime(ctx, queryer, "RETURN 1", 7, map[string]any{"project": "P1"}, 2500*time.Millisecond, func(map[string]any) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited != 1 {
		t.Fatalf("error=%v visited=%d, want cancellation after one row", err, visited)
	}
	if queryer.called != 1 || queryer.options == nil || queryer.options.Options.MaxRuntime != 2.5 {
		t.Fatalf("query options=%#v called=%d, want maxRuntime 2.5 seconds", queryer.options, queryer.called)
	}
	if queryer.options.Options.MaxNumberOfPlans == nil || *queryer.options.Options.MaxNumberOfPlans != 1 {
		t.Fatalf("bounded query optimizer plans=%v, want one", queryer.options.Options.MaxNumberOfPlans)
	}
	if queryer.options.BatchSize != 7 || queryer.options.BindVars["project"] != "P1" {
		t.Fatalf("query options lost ordinary fields: %#v", queryer.options)
	}
	if cursor.closeCount != 1 || cursor.closeCtx == nil || cursor.closeCtxErr != nil {
		t.Fatalf("cursor close count=%d context=%v, want one close with a live cleanup context", cursor.closeCount, cursor.closeCtx)
	}
	if _, ok := cursor.closeCtx.Deadline(); !ok {
		t.Fatal("cursor cleanup context has no deadline")
	}
}

func TestQueryRowsWithMaxRuntimeRejectsNonPositiveRuntime(t *testing.T) {
	queryer := &fakeQueryer{cursor: &fakeCursor{}}
	err := queryRowsWithMaxRuntime(context.Background(), queryer, "RETURN 1", 1, nil, 0, func(map[string]any) error { return nil })
	if err == nil || queryer.called != 0 {
		t.Fatalf("error=%v query calls=%d, want invalid runtime rejected before querying", err, queryer.called)
	}
}

func TestQueryRowsLeavesRuntimeUnboundedOutsidePreview(t *testing.T) {
	queryer := &fakeQueryer{cursor: &fakeCursor{}}
	if err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queryer.options == nil || queryer.options.Options.MaxRuntime != 0 || queryer.options.Options.MaxNumberOfPlans != nil {
		t.Fatalf("ordinary query options=%#v, want no preview runtime limit", queryer.options)
	}
}

func TestQueryRowsWrapsDriverQueryError(t *testing.T) {
	want := errors.New("driver query failure")
	queryer := &fakeQueryer{err: want}
	err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "arango query") {
		t.Fatalf("error=%v", err)
	}
}

func TestIsQueryMemoryLimitExceededRecognizesWrappedArangoResourceLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "AQL: query would use more memory than allowed",
	}
	err := fmt.Errorf("arango query: %w", driverErr)

	if !IsQueryMemoryLimitExceeded(err) {
		t.Fatalf("IsQueryMemoryLimitExceeded(%v) = false, want true", err)
	}
	if !IsQueryResourceLimitExceeded(err) {
		t.Fatalf("IsQueryResourceLimitExceeded(%v) = false, want true", err)
	}
	if IsQueryMemoryLimitExceeded(errors.New("unrelated query failure")) {
		t.Fatal("unrelated query failure was classified as a memory-limit error")
	}
	outOfMemory := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrOutOfMemory, ErrorMessage: "out of memory"}
	if IsQueryMemoryLimitExceeded(outOfMemory) {
		t.Fatal("database out-of-memory was classified as a configured query-memory limit")
	}
	if !IsQueryOutOfMemory(outOfMemory) {
		t.Fatal("database out-of-memory was not recognized")
	}
	otherResource := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "resource limit exceeded"}
	if IsQueryMemoryLimitExceeded(otherResource) {
		t.Fatal("non-memory resource limit was classified as a memory limit")
	}
	if !IsQueryResourceLimitExceeded(otherResource) {
		t.Fatal("non-memory resource limit was not recognized")
	}
}

func TestIsQueryUserAssertionRecognizesOnlyTheRequestedStableCode(t *testing.T) {
	err := fmt.Errorf("arango query: %w", shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: RELATIONSHIP_CARDINALITY_VIOLATION (while executing)",
	})
	if !IsQueryUserAssertion(err, "RELATIONSHIP_CARDINALITY_VIOLATION") {
		t.Fatal("stable user assertion was not recognized")
	}
	if IsQueryUserAssertion(err, "SOME_OTHER_ASSERTION") || IsQueryUserAssertion(errors.New("RELATIONSHIP_CARDINALITY_VIOLATION"), "RELATIONSHIP_CARDINALITY_VIOLATION") {
		t.Fatal("unrelated error was classified as the requested user assertion")
	}
}

func TestQueryRowsClosesCursorOnVisitorError(t *testing.T) {
	want := errors.New("visitor stopped")
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}}}
	queryer := &fakeQueryer{cursor: cursor}
	err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return want })
	if !errors.Is(err, want) || cursor.closeCount != 1 {
		t.Fatalf("error=%v closeCount=%d", err, cursor.closeCount)
	}
}

func TestQueryRowsChecksCancellationBeforeHasMoreAndVisitor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}, {"id": "2"}}}
	queryer := &fakeQueryer{cursor: cursor}
	visited := 0
	err := queryRows(ctx, queryer, "RETURN 1", 1, nil, func(map[string]any) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited != 1 || cursor.hasMore != 1 || cursor.closeCount != 1 {
		t.Fatalf("error=%v visited=%d hasMore=%d closeCount=%d", err, visited, cursor.hasMore, cursor.closeCount)
	}
}

func TestQueryRowsChecksCancellationBeforeVisitor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}}, onRead: cancel}
	queryer := &fakeQueryer{cursor: cursor}
	visited := 0
	err := queryRows(ctx, queryer, "RETURN 1", 1, nil, func(map[string]any) error { visited++; return nil })
	if !errors.Is(err, context.Canceled) || visited != 0 || cursor.closeCount != 1 {
		t.Fatalf("error=%v visited=%d closeCount=%d", err, visited, cursor.closeCount)
	}
}

func TestQueryRowsWrapsDriverReadAndCloseErrors(t *testing.T) {
	readErr := errors.New("driver read failure")
	closeErr := errors.New("driver close failure")
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}}, readErr: readErr, closeErr: closeErr}
	queryer := &fakeQueryer{cursor: cursor}
	err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("error=%v", err)
	}
	if !strings.Contains(err.Error(), "read arango query cursor") || !strings.Contains(err.Error(), "close arango query cursor") {
		t.Fatalf("missing wrapped driver context: %v", err)
	}
}

func TestQueryRowsClosesCursorOnSuccess(t *testing.T) {
	cursor := &fakeCursor{}
	queryer := &fakeQueryer{cursor: cursor}
	if err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if cursor.closeCount != 1 {
		t.Fatalf("closeCount=%d", cursor.closeCount)
	}
}

const (
	populationProfileTargets = 20000
	populationProfileSparse  = 100
	populationProfileDense   = 18000
)

type populationProfileResult struct {
	Targets int                              `json:"targets"`
	Cases   map[string]populationProfileCase `json:"cases"`
}

type populationProfileCase struct {
	Selected         int            `json:"selected"`
	Ratio            float64        `json:"ratio"`
	TargetScan       ProfileSummary `json:"targetScan"`
	MembershipDriven ProfileSummary `json:"membershipDriven"`
}

func TestPopulationPlanProfilesAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_POPULATION_PROFILE") == "" {
		t.Skip("set LOOM_POPULATION_PROFILE=1 to run population plan profiling")
	}
	url, database, _ := profileArangoSettings(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	targetsName := "loom_population_profile_targets_" + suffix
	membersName := "loom_population_profile_members_" + suffix
	targets, err := client.ensureCollection(ctx, targetsName, false)
	if err != nil {
		t.Fatal(err)
	}
	members, err := client.ensureCollection(ctx, membersName, false)
	if err != nil {
		_ = targets.Remove(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := members.Remove(cleanup); err != nil {
			t.Errorf("remove owned member collection: %v", err)
		}
		if err := targets.Remove(cleanup); err != nil {
			t.Errorf("remove owned target collection: %v", err)
		}
	})
	if err := ensureIndexes(ctx, targets, [][]string{{"project", "generation", "resourceType", "id"}}); err != nil {
		t.Fatal(err)
	}
	if err := ensureIndexes(ctx, members, [][]string{{"selectionId", "project", "generation", "resourceType", "id"}}); err != nil {
		t.Fatal(err)
	}

	insertPopulationProfileTargets(t, ctx, client, targetsName)
	insertPopulationProfileMembers(t, ctx, client, membersName, "sparse", populationProfileSparse)
	insertPopulationProfileMembers(t, ctx, client, membersName, "dense", populationProfileDense)

	result := populationProfileResult{Targets: populationProfileTargets, Cases: map[string]populationProfileCase{}}
	for _, profileCase := range []struct {
		name     string
		selected int
	}{{name: "sparse", selected: populationProfileSparse}, {name: "dense", selected: populationProfileDense}} {
		targetScan := profilePopulationQuery(t, ctx, client, targetScanPopulationQuery, targetsName, membersName, profileCase.name, profileCase.selected)
		membershipDriven := profilePopulationQuery(t, ctx, client, membershipDrivenPopulationQuery, targetsName, membersName, profileCase.name, profileCase.selected)
		if targetScan.ScannedFull != 0 || membershipDriven.ScannedFull != 0 {
			t.Fatalf("%s population plans performed full collection scans: target=%d membership=%d", profileCase.name, targetScan.ScannedFull, membershipDriven.ScannedFull)
		}
		result.Cases[profileCase.name] = populationProfileCase{
			Selected: profileCase.selected, Ratio: float64(profileCase.selected) / populationProfileTargets,
			TargetScan: targetScan, MembershipDriven: membershipDriven,
		}
		t.Logf("%s selected=%d target_scan runtime=%0.6fs index=%d peak=%d membership_driven runtime=%0.6fs index=%d peak=%d",
			profileCase.name, profileCase.selected,
			targetScan.RuntimeSeconds, targetScan.ScannedIndex, targetScan.PeakMemory,
			membershipDriven.RuntimeSeconds, membershipDriven.ScannedIndex, membershipDriven.PeakMemory,
		)
	}
	if path := strings.TrimSpace(os.Getenv("LOOM_POPULATION_PROFILE_OUTPUT")); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create profile output directory: %v", err)
		}
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func insertPopulationProfileTargets(t *testing.T, ctx context.Context, client *Client, collection string) {
	t.Helper()
	insertPopulationProfileDocuments(t, ctx, client, collection, populationProfileTargets, func(index int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"_key":"target_%08d","project":"profile/project","generation":"generation-1","resourceType":"Specimen","id":"specimen-%08d"}`, index, index))
	})
}

func insertPopulationProfileMembers(t *testing.T, ctx context.Context, client *Client, collection, selectionID string, count int) {
	t.Helper()
	insertPopulationProfileDocuments(t, ctx, client, collection, count, func(index int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"_key":"%s_%08d","selectionId":"%s","project":"profile/project","generation":"generation-1","resourceType":"Specimen","id":"specimen-%08d"}`, selectionID, index, selectionID, index))
	})
}

func insertPopulationProfileDocuments(t *testing.T, ctx context.Context, client *Client, collection string, count int, document func(int) json.RawMessage) {
	t.Helper()
	for offset := 0; offset < count; offset += 1000 {
		end := offset + 1000
		if end > count {
			end = count
		}
		batch := make([]json.RawMessage, 0, end-offset)
		for index := offset; index < end; index++ {
			batch = append(batch, document(index))
		}
		if err := client.InsertBatchRaw(ctx, collection, batch, false, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func profilePopulationQuery(t *testing.T, ctx context.Context, client *Client, query, targets, members, selectionID string, expected int) ProfileSummary {
	t.Helper()
	profile, err := client.Profile(ctx, ProfileRequest{
		Query: query,
		BindVars: map[string]any{
			"@targets": targets, "@members": members, "selection": selectionID,
			"project": "profile/project", "generation": "generation-1", "resourceType": "Specimen",
		},
		BatchSize: populationProfileTargets + 1,
		Count:     true,
		Options:   ProfileOptions{Profile: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.HasMore || len(profile.Result) != expected || profile.Count != expected {
		t.Fatalf("selection %s returned count=%d rows=%d hasMore=%v, want %d", selectionID, profile.Count, len(profile.Result), profile.HasMore, expected)
	}
	return SummarizeProfile(profile)
}

const targetScanPopulationQuery = `
FOR root IN @@targets
  FILTER root.project == @project
  FILTER root.generation == @generation
  FILTER root.resourceType == @resourceType
  FILTER LENGTH((
    FOR member IN @@members
      FILTER member.selectionId == @selection
      FILTER member.project == @project
      FILTER member.generation == @generation
      FILTER member.resourceType == @resourceType
      FILTER member.id == root.id
      LIMIT 1
      RETURN 1
  )) > 0
  RETURN root.id`

const membershipDrivenPopulationQuery = `
FOR member IN @@members
  FILTER member.selectionId == @selection
  FILTER member.project == @project
  FILTER member.generation == @generation
  FILTER member.resourceType == @resourceType
  FOR root IN @@targets
    FILTER root.project == @project
    FILTER root.generation == @generation
    FILTER root.resourceType == @resourceType
    FILTER root.id == member.id
    COLLECT id = root.id
    RETURN id`

func TestWithReadSnapshotDeclaresStrictCollectionsAndReadsCursor(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/_db/db/_api/transaction/begin":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["allowImplicit"] != false {
				t.Fatalf("allowImplicit = %#v, want explicit false", body["allowImplicit"])
			}
			collections := body["collections"].(map[string]any)["read"].([]any)
			if !reflect.DeepEqual(collections, []any{"DocumentReference", "fhir_edge"}) {
				t.Fatalf("read collections = %#v", collections)
			}
			w.Write([]byte(`{"result":{"id":"tx-1"}}`))
		case r.URL.Path == "/_db/db/_api/cursor" && r.Method == http.MethodPost:
			if r.Header.Get("x-arango-trx-id") != "tx-1" {
				t.Fatalf("transaction header = %q", r.Header.Get("x-arango-trx-id"))
			}
			var body struct {
				Options struct {
					MaxNumberOfPlans int `json:"maxNumberOfPlans"`
				} `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Options.MaxNumberOfPlans != 1 {
				t.Fatalf("snapshot optimizer plan cap = %d, want 1", body.Options.MaxNumberOfPlans)
			}
			w.Write([]byte(`{"result":[{"_key":"a"}],"hasMore":true,"id":"cursor-1"}`))
		case r.URL.Path == "/_db/db/_api/cursor/cursor-1" && r.Method == http.MethodPut:
			if r.Header.Get("x-arango-trx-id") != "tx-1" {
				t.Fatalf("cursor transaction header = %q", r.Header.Get("x-arango-trx-id"))
			}
			w.Write([]byte(`{"result":[{"_key":"b"}],"hasMore":false}`))
		case r.URL.Path == "/_db/db/_api/transaction/tx-1" && r.Method == http.MethodPut:
			w.Write([]byte(`{"result":{"status":"committed"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	var rows []string
	err := client.WithReadSnapshot(context.Background(), []string{"fhir_edge", "DocumentReference", "DocumentReference"}, func(ctx context.Context, queryer RowQueryer) error {
		return queryer.QueryRows(WithQueryMaxNumberOfPlans(ctx, 1), "FOR d IN @@docs RETURN d", 1, map[string]interface{}{"@docs": "DocumentReference"}, func(row map[string]interface{}) error {
			rows = append(rows, row["_key"].(string))
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, []string{"a", "b"}) {
		t.Fatalf("rows = %#v", rows)
	}
	if !reflect.DeepEqual(calls, []string{
		"POST /_db/db/_api/transaction/begin",
		"POST /_db/db/_api/cursor",
		"PUT /_db/db/_api/cursor/cursor-1",
		"PUT /_db/db/_api/transaction/tx-1",
	}) {
		t.Fatalf("request order = %#v", calls)
	}
}

func TestWithReadSnapshotAbortsCallbackFailure(t *testing.T) {
	var lastMethod, lastPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		if r.URL.Path == "/_db/db/_api/transaction/begin" {
			w.Write([]byte(`{"result":{"id":"tx-2"}}`))
			return
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/_db/db/_api/transaction/tx-2" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"result":{"status":"aborted"}}`))
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	wantErr := errors.New("query failed")
	err := client.WithReadSnapshot(context.Background(), []string{"Documents"}, func(context.Context, RowQueryer) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want callback failure", err)
	}
	if lastMethod != http.MethodDelete || !strings.HasSuffix(lastPath, "/tx-2") {
		t.Fatalf("final request = %s %s, want transaction abort", lastMethod, lastPath)
	}
}

func TestWithReadSnapshotAbortsWhenFinalVisitorCancelsQuery(t *testing.T) {
	var committed, aborted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transaction/begin"):
			w.Write([]byte(`{"result":{"id":"tx-cancel"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_api/cursor"):
			w.Write([]byte(`{"result":[{"_key":"a"}],"hasMore":false}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/transaction/tx-cancel"):
			committed = true
			w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/transaction/tx-cancel"):
			aborted = true
			w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	err := client.WithReadSnapshot(context.Background(), []string{"docs"}, func(ctx context.Context, queryer RowQueryer) error {
		queryCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		return queryer.QueryRows(queryCtx, "RETURN 1", 1, nil, func(map[string]any) error {
			cancel()
			return nil
		})
	})
	if !errors.Is(err, context.Canceled) || committed || !aborted {
		t.Fatalf("error=%v committed=%v aborted=%v", err, committed, aborted)
	}
}

func TestWithReadSnapshotAbortsAfterCommitFailure(t *testing.T) {
	var aborted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transaction/begin"):
			w.Write([]byte(`{"result":{"id":"tx-3"}}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/transaction/tx-3"):
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/transaction/tx-3"):
			aborted = true
			w.Write([]byte(`{"result":{"status":"aborted"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	err := client.WithReadSnapshot(context.Background(), []string{"docs"}, func(context.Context, RowQueryer) error { return nil })
	if err == nil || !aborted {
		t.Fatalf("error=%v, abort attempted=%v", err, aborted)
	}
}

func TestQueryRowsWithMaxRuntimeAppliesMemoryLimitAndClampsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ctx = WithQueryMemoryLimit(ctx, 256<<20)
	queryer := &fakeQueryer{cursor: &fakeCursor{}}

	if err := queryRowsWithMaxRuntime(ctx, queryer, "RETURN 1", 3, nil, 30*time.Second, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queryer.called != 1 || queryer.options == nil {
		t.Fatalf("query calls=%d options=%#v, want one query", queryer.called, queryer.options)
	}
	if queryer.options.MemoryLimit != 256<<20 {
		t.Fatalf("query memory limit=%d, want %d", queryer.options.MemoryLimit, int64(256<<20))
	}
	if got := queryer.options.Options.MaxRuntime; got <= 0 || got >= 30 {
		t.Fatalf("query max runtime=%v seconds, want positive and below supplied 30 seconds", got)
	}
}

func TestQueryRowsWithMaxRuntimeLeavesUncappedCallsUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	queryer := &fakeQueryer{cursor: &fakeCursor{}}

	if err := queryRowsWithMaxRuntime(ctx, queryer, "RETURN 1", 3, nil, 10*time.Second, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queryer.options == nil || queryer.options.MemoryLimit != 0 || queryer.options.Options.MaxRuntime != 10 {
		t.Fatalf("ordinary query options=%#v, want no memory cap and maxRuntime 10 seconds", queryer.options)
	}
}

func TestQueryRowsWithMaxRuntimeMemoryLimitStillPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}, {"id": "2"}}}
	queryer := &fakeQueryer{cursor: cursor}
	visited := 0
	err := queryRowsWithMaxRuntime(WithQueryMemoryLimit(ctx, 256<<20), queryer, "RETURN 1", 2, nil, time.Second, func(map[string]any) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited != 1 {
		t.Fatalf("error=%v visited=%d, want cancellation after one row", err, visited)
	}
	if queryer.options == nil || queryer.options.MemoryLimit != 256<<20 {
		t.Fatalf("query options=%#v, want memory cap applied before cancellation", queryer.options)
	}
}

func TestQueryRowsWithMaxRuntimeRejectsInvalidMemoryLimit(t *testing.T) {
	queryer := &fakeQueryer{cursor: &fakeCursor{}}
	err := queryRowsWithMaxRuntime(WithQueryMemoryLimit(context.Background(), 0), queryer, "RETURN 1", 1, nil, time.Second, func(map[string]any) error { return nil })
	if err == nil || queryer.called != 0 {
		t.Fatalf("error=%v query calls=%d, want invalid memory limit rejected before querying", err, queryer.called)
	}
}

func TestClientProfilePostsParameterizedCursorRequest(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || r.URL.Path != "/_db/fhir_proto/_api/cursor" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, fragment := range []string{`"query":"FOR p IN Patient RETURN p"`, `"bindVars":{"project":"demo"}`, `"profile":2`} {
				if !strings.Contains(text, fragment) {
					t.Fatalf("request body %s does not contain %s", text, fragment)
				}
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"result":[{"_key":"p1"}],"hasMore":false,"extra":{"stats":{"nodes":[{"id":1,"calls":1,"items":1,"runtime":0.01}]},"plan":{"nodes":[{"id":1,"type":"ReturnNode"}]}}}`)),
				Request:    r,
			}, nil
		})},
	}
	result, err := client.Profile(context.Background(), ProfileRequest{Query: "FOR p IN Patient RETURN p", BindVars: map[string]any{"project": "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result) != 1 || len(result.Extra.Stats.Nodes) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientProfileRejectsHTTPError(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("bad query")), Request: r}, nil
		})},
	}
	_, err := client.Profile(context.Background(), ProfileRequest{Query: "FOR p IN Patient RETURN p"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

type revisionTestDatabase struct {
	driver.Database
	collection driver.Collection
	name       string
	err        error
}

func (d *revisionTestDatabase) GetCollection(_ context.Context, name string, _ *driver.GetCollectionOptions) (driver.Collection, error) {
	d.name = name
	return d.collection, d.err
}

type revisionTestCollection struct {
	driver.Collection
	properties driver.CollectionProperties
	err        error
}

func (c revisionTestCollection) Revision(context.Context) (driver.CollectionProperties, error) {
	return c.properties, c.err
}

func TestClientCollectionRevisionReadsCollectionRevision(t *testing.T) {
	collection := revisionTestCollection{properties: driver.CollectionProperties{Revision: "revision-7"}}
	database := &revisionTestDatabase{collection: collection}
	client := &Client{db: database}

	revision, err := client.CollectionRevision(context.Background(), "Observation")
	if err != nil {
		t.Fatal(err)
	}
	if revision != "revision-7" || database.name != "Observation" {
		t.Fatalf("revision=%q collection=%q, want revision-7/Observation", revision, database.name)
	}
}

func TestClientCollectionRevisionReturnsWrappedLookupErrors(t *testing.T) {
	want := errors.New("collection unavailable")
	client := &Client{db: &revisionTestDatabase{err: want}}
	if _, err := client.CollectionRevision(context.Background(), "Observation"); !errors.Is(err, want) {
		t.Fatalf("error=%v, want wrapped collection lookup error", err)
	}
}
