package arango

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
)

type fakeQueryer struct {
	driver.DatabaseQuery
	cursor  driver.Cursor
	err     error
	options *driver.QueryOptions
	called  int
}

func (q *fakeQueryer) Query(_ context.Context, _ string, options *driver.QueryOptions) (driver.Cursor, error) {
	q.called++
	q.options = options
	if q.err != nil {
		return nil, q.err
	}
	return q.cursor, nil
}

type fakeDatabase struct {
	driver.Database
	queryer *fakeQueryer
}

func (db *fakeDatabase) Query(ctx context.Context, query string, options *driver.QueryOptions) (driver.Cursor, error) {
	return db.queryer.Query(ctx, query, options)
}

type fakeCursor struct {
	driver.Cursor
	rows       []map[string]any
	readErr    error
	closeErr   error
	closeCount int
	hasMore    int
	readCount  int
	onRead     func()
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

func TestQueryRowsOptionsSetMaxNumberOfPlansOnlyWhenRequested(t *testing.T) {
	defaultCursor := &fakeCursor{}
	defaultQueryer := &fakeQueryer{cursor: defaultCursor}
	defaultClient := &Client{db: &fakeDatabase{queryer: defaultQueryer}}
	if err := defaultClient.QueryRows(context.Background(), "RETURN 1", 1, nil, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if defaultQueryer.options == nil {
		t.Fatal("default QueryRows passed nil QueryOptions to the driver")
	}
	if defaultQueryer.options.Options.MaxNumberOfPlans != nil {
		t.Fatalf("default QueryRows MaxNumberOfPlans=%v, want unset", *defaultQueryer.options.Options.MaxNumberOfPlans)
	}

	optionCursor := &fakeCursor{rows: []map[string]any{{"id": "1"}}}
	optionQueryer := &fakeQueryer{cursor: optionCursor}
	optionClient := &Client{db: &fakeDatabase{queryer: optionQueryer}}
	var rows []map[string]any
	err := optionClient.QueryRowsWithOptions(context.Background(), "RETURN 1", 1, nil, QueryRowsOptions{MaxNumberOfPlans: 1}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if optionQueryer.options == nil {
		t.Fatal("option QueryRows passed nil QueryOptions to the driver")
	}
	maxNumberOfPlans := optionQueryer.options.Options.MaxNumberOfPlans
	if maxNumberOfPlans == nil {
		t.Fatal("option QueryRows MaxNumberOfPlans is unset, want 1")
	}
	if *maxNumberOfPlans != 1 {
		t.Fatalf("option QueryRows MaxNumberOfPlans=%d, want 1", *maxNumberOfPlans)
	}
	if len(rows) != 1 || rows[0]["id"] != "1" || optionCursor.closeCount != 1 {
		t.Fatalf("rows=%v closeCount=%d, want one row and one close", rows, optionCursor.closeCount)
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

func TestTransactionQueryRowsStreamsOnlyWhenRequested(t *testing.T) {
	for _, stream := range []bool{false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			cursor := &fakeCursor{rows: []map[string]any{{"id": "one"}, {"id": "two"}}}
			queryer := &fakeQueryer{cursor: cursor}
			client := transactionClient{queryer: queryer, options: QueryRowsOptions{Stream: stream}}
			var values []string
			err := client.QueryRows(context.Background(), "RETURN 1", 1, nil, func(row map[string]any) error {
				values = append(values, row["id"].(string))
				return nil
			})
			if err != nil || queryer.options.Options.Stream != stream || strings.Join(values, ",") != "one,two" || cursor.closeCount != 1 {
				t.Fatalf("error=%v options=%+v values=%v closes=%d", err, queryer.options, values, cursor.closeCount)
			}
		})
	}
	batch := &fakeBatchQueryer{initial: []map[string]any{{"id": "one"}}, cursor: &fakeBatchCursor{}}
	client := transactionClient{queryer: batch, options: QueryRowsOptions{Stream: true}}
	var values []string
	err := client.QueryRows(context.Background(), "RETURN 1", 1, nil, func(row map[string]any) error {
		values = append(values, row["id"].(string))
		return nil
	})
	if err != nil || !batch.options.Options.Stream || strings.Join(values, ",") != "one" || !batch.cursor.closed {
		t.Fatalf("streaming transaction: error=%v options=%+v values=%v closed=%v", err, batch.options, values, batch.cursor.closed)
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
