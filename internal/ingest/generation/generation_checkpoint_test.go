package generation

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestGenerationFileOutcomesAdvanceOnlyContiguousCompletedPrefix(t *testing.T) {
	var checkpoints []string
	var scanned []int64
	outcomes := newGenerationFileOutcomes("meta", []string{"a.ndjson", "b.ndjson", "c.ndjson"}, func(_ context.Context, count int64, checkpoint string) error {
		scanned = append(scanned, count)
		checkpoints = append(checkpoints, checkpoint)
		return nil
	})
	complete := func(index, rows int, err error) {
		t.Helper()
		if advanceErr := outcomes.complete(context.Background(), index, fileLoadResult{Rows: rows}, err); advanceErr != nil {
			t.Fatal(advanceErr)
		}
	}
	complete(1, 2, nil)
	if len(checkpoints) != 0 {
		t.Fatalf("checkpoints before prefix completed = %v", checkpoints)
	}
	complete(0, 1, nil)
	if !reflect.DeepEqual(checkpoints, []string{"../a.ndjson", "../b.ndjson"}) || !reflect.DeepEqual(scanned, []int64{1, 3}) {
		t.Fatalf("checkpoints=%v scanned=%v, want [a,b] with row counts [1,3] while c remains pending", checkpoints, scanned)
	}
	complete(2, 3, errors.New("file failed"))
	if !reflect.DeepEqual(checkpoints, []string{"../a.ndjson", "../b.ndjson"}) {
		t.Fatalf("checkpoint crossed a failed file: %v", checkpoints)
	}
}

func TestGenerationFileOutcomesDoNotCountFailedCheckpointAttempts(t *testing.T) {
	var checkpoints []string
	var scanned []int64
	failedOnce := false
	outcomes := newGenerationFileOutcomes("meta", []string{"a.ndjson", "b.ndjson", "c.ndjson"}, func(_ context.Context, count int64, checkpoint string) error {
		scanned = append(scanned, count)
		checkpoints = append(checkpoints, checkpoint)
		if checkpoint == "../a.ndjson" && !failedOnce {
			failedOnce = true
			return errors.New("write failed")
		}
		return nil
	})
	if err := outcomes.complete(context.Background(), 1, fileLoadResult{Rows: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if err := outcomes.complete(context.Background(), 0, fileLoadResult{Rows: 1}, nil); err == nil {
		t.Fatal("checkpoint failure returned nil")
	}
	if err := outcomes.complete(context.Background(), 2, fileLoadResult{Rows: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(checkpoints, []string{"../a.ndjson", "../a.ndjson", "../b.ndjson", "../c.ndjson"}) ||
		!reflect.DeepEqual(scanned, []int64{1, 1, 3, 6}) {
		t.Fatalf("checkpoint retries=%v scanned=%v, want stable prefix totals", checkpoints, scanned)
	}
}
