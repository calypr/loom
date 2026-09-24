package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

type fakeBatchQueryer struct {
	driver.DatabaseQuery
	initial []map[string]any
	cursor  *fakeBatchCursor
	options *driver.QueryOptions
}

func (q *fakeBatchQueryer) QueryBatch(_ context.Context, _ string, options *driver.QueryOptions, result any) (driver.CursorBatch, error) {
	q.options = options
	*(result.(*[]map[string]any)) = q.initial
	return q.cursor, nil
}

type fakeBatchCursor struct {
	driver.CursorBatch
	batches   [][]map[string]any
	readIndex int
	readErr   error
	closeErr  error
	closed    bool
}

func (c *fakeBatchCursor) HasMoreBatches() bool { return c.readIndex < len(c.batches) }

func (c *fakeBatchCursor) ReadNextBatch(_ context.Context, result any) error {
	if c.readErr != nil {
		return c.readErr
	}
	if !c.HasMoreBatches() {
		return errors.New("unexpected extra batch read")
	}
	*(result.(*[]map[string]any)) = c.batches[c.readIndex]
	c.readIndex++
	return nil
}

func (c *fakeBatchCursor) Close() error {
	c.closed = true
	return c.closeErr
}

func TestQueryRowsByBatchVisitsInitialAndFinalBatchesOnce(t *testing.T) {
	first := map[string]any{"id": "first"}
	final := map[string]any{"id": "final"}
	cursor := &fakeBatchCursor{batches: [][]map[string]any{{{"id": "middle"}}, {final}}}
	queryer := &fakeBatchQueryer{initial: []map[string]any{first}, cursor: cursor}
	var got []string
	err := queryRowsByBatch(context.Background(), queryer, "RETURN 1", &driver.QueryOptions{BatchSize: 2}, func(row map[string]any) error {
		got = append(got, row["id"].(string))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "first,middle,final" {
		t.Fatalf("visited rows = %v", got)
	}
	if !cursor.closed {
		t.Fatal("cursor was not closed")
	}
	if queryer.options.BatchSize != 2 || !queryer.options.Options.Stream {
		t.Fatalf("QueryBatch options = %+v", queryer.options)
	}
}

func TestQueryRowsByBatchEmptyResultClosesCursor(t *testing.T) {
	cursor := &fakeBatchCursor{}
	queryer := &fakeBatchQueryer{cursor: cursor}
	visits := 0
	err := queryRowsByBatch(context.Background(), queryer, "RETURN 1", &driver.QueryOptions{}, func(map[string]any) error {
		visits++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visits != 0 || !cursor.closed {
		t.Fatalf("visits = %d, closed = %t", visits, cursor.closed)
	}
}

func TestQueryRowsByBatchVisitorErrorClosesCursor(t *testing.T) {
	wantErr := errors.New("visitor failed")
	cursor := &fakeBatchCursor{batches: [][]map[string]any{{{"id": "not-read"}}}}
	queryer := &fakeBatchQueryer{initial: []map[string]any{{"id": "first"}}, cursor: cursor}
	err := queryRowsByBatch(context.Background(), queryer, "RETURN 1", &driver.QueryOptions{}, func(map[string]any) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) || !cursor.closed || cursor.readIndex != 0 {
		t.Fatalf("error = %v, closed = %t, later batch reads = %d", err, cursor.closed, cursor.readIndex)
	}
}

func TestQueryRowsByBatchCancellationClosesCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cursor := &fakeBatchCursor{}
	queryer := &fakeBatchQueryer{initial: []map[string]any{{"id": "first"}, {"id": "second"}}, cursor: cursor}
	visits := 0
	err := queryRowsByBatch(ctx, queryer, "RETURN 1", &driver.QueryOptions{}, func(map[string]any) error {
		visits++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visits != 1 || !cursor.closed {
		t.Fatalf("error = %v, visits = %d, closed = %t", err, visits, cursor.closed)
	}
}

func TestQueryRowsByBatchJoinsCursorCloseError(t *testing.T) {
	wantErr := errors.New("close failed")
	cursor := &fakeBatchCursor{closeErr: wantErr}
	queryer := &fakeBatchQueryer{cursor: cursor}
	err := queryRowsByBatch(context.Background(), queryer, "RETURN 1", &driver.QueryOptions{}, func(map[string]any) error { return nil })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want close error", err)
	}
}

func benchmarkRows() []byte {
	rows := make([]map[string]any, 10000)
	for i := range rows {
		rows[i] = map[string]any{
			"source":        fmt.Sprintf("retained:Patient/%08d", i),
			"resource_type": "Patient",
			"concept":       fmt.Sprintf("concept-%03d", i%257),
			"binding":       fmt.Sprintf("binding-%03d", i%31),
		}
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		panic(err)
	}
	return payload
}

func BenchmarkDriverReadDocumentVersusQueryBatchDecode(b *testing.B) {
	payload := benchmarkRows()
	b.Run("ReadDocument-style", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		for i := 0; i < b.N; i++ {
			batch := append([]byte(nil), payload...)
			decoder := json.NewDecoder(bytes.NewReader(batch))
			if _, err := decoder.Token(); err != nil {
				b.Fatal(err)
			}
			for decoder.More() {
				var result driver.Unmarshal[driver.DocumentMeta, driver.UnmarshalData]
				if err := decoder.Decode(&result); err != nil {
					b.Fatal(err)
				}
				var row map[string]any
				if err := result.Object.Inject(&row); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("QueryBatch-style", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		for i := 0; i < b.N; i++ {
			batch := append([]byte(nil), payload...)
			var rows []map[string]any
			if err := json.Unmarshal(batch, &rows); err != nil {
				b.Fatal(err)
			}
		}
	})
}
