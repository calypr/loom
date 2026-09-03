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
	cursor driver.Cursor
	err    error
	called int
}

func (q *fakeQueryer) Query(context.Context, string, *driver.QueryOptions) (driver.Cursor, error) {
	q.called++
	if q.err != nil {
		return nil, q.err
	}
	return q.cursor, nil
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
