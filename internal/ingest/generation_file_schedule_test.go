package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunGenerationFilesSharesWorkerAndWriterBudgets(t *testing.T) {
	directory := t.TempDir()
	files := make([]string, 9)
	for index := range files {
		path := filepath.Join(directory, string(rune('a'+index))+".ndjson")
		if err := os.WriteFile(path, make([]byte, (len(files)-index)*16), 0o600); err != nil {
			t.Fatal(err)
		}
		files[index] = path
	}
	var activeFiles int64
	var maximumFiles int64
	var activeWorkers int64
	var maximumWorkers int64
	var activeWriters int64
	var maximumWriters int64
	updateMaximum := func(counter *int64, value int64) {
		for {
			current := atomic.LoadInt64(counter)
			if value <= current || atomic.CompareAndSwapInt64(counter, current, value) {
				return
			}
		}
	}
	opts := normalizeLoadOptions(LoadOptions{WorkerCount: 8, WriterCount: 8, FileConcurrency: 3})
	err := runGenerationFiles(context.Background(), opts, files, func(ctx context.Context, fileOpts LoadOptions, _ int, _ string) error {
		filesNow := atomic.AddInt64(&activeFiles, 1)
		updateMaximum(&maximumFiles, filesNow)
		defer atomic.AddInt64(&activeFiles, -1)
		var workers sync.WaitGroup
		for worker := 0; worker < fileOpts.WorkerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for row := 0; row < 2; row++ {
					select {
					case fileOpts.fileWorkerBudget <- struct{}{}:
					case <-ctx.Done():
						return
					}
					workersNow := atomic.AddInt64(&activeWorkers, 1)
					updateMaximum(&maximumWorkers, workersNow)
					time.Sleep(3 * time.Millisecond)
					atomic.AddInt64(&activeWorkers, -1)
					<-fileOpts.fileWorkerBudget
				}
			}()
		}
		workers.Wait()
		var writers sync.WaitGroup
		for writer := 0; writer < fileOpts.WriterCount; writer++ {
			writers.Add(1)
			go func() {
				defer writers.Done()
				select {
				case fileOpts.fileWriterBudget <- struct{}{}:
				case <-ctx.Done():
					return
				}
				writersNow := atomic.AddInt64(&activeWriters, 1)
				updateMaximum(&maximumWriters, writersNow)
				time.Sleep(3 * time.Millisecond)
				atomic.AddInt64(&activeWriters, -1)
				<-fileOpts.fileWriterBudget
			}()
		}
		writers.Wait()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if maximumFiles != 3 || maximumWorkers != 8 || maximumWriters != 8 {
		t.Fatalf("concurrent files/workers/writers = %d/%d/%d, want 3/8/8", maximumFiles, maximumWorkers, maximumWriters)
	}
}

func TestRunGenerationFilesReusesIdleWorkerPermitsForTailFiles(t *testing.T) {
	directory := t.TempDir()
	files := []string{"short.ndjson", "long-one.ndjson", "long-two.ndjson", "tail.ndjson"}
	sizes := map[string]int{"short.ndjson": 10, "long-one.ndjson": 1000, "long-two.ndjson": 900, "tail.ndjson": 1}
	for index, name := range files {
		if err := os.WriteFile(filepath.Join(directory, name), make([]byte, sizes[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		files[index] = filepath.Join(directory, name)
	}
	var longOneActive int64
	var tailUsedWhileLongOneActive int64
	opts := normalizeLoadOptions(LoadOptions{WorkerCount: 8, WriterCount: 1, FileConcurrency: 3})
	err := runGenerationFiles(context.Background(), opts, files, func(ctx context.Context, fileOpts LoadOptions, _ int, file string) error {
		name := filepath.Base(file)
		if name == "long-one.ndjson" {
			atomic.StoreInt64(&longOneActive, 1)
			defer atomic.StoreInt64(&longOneActive, 0)
		}
		if name == "tail.ndjson" && atomic.LoadInt64(&longOneActive) == 1 {
			atomic.StoreInt64(&tailUsedWhileLongOneActive, 1)
		}
		iterations := 1
		if name == "long-one.ndjson" || name == "long-two.ndjson" {
			iterations = 30
		}
		var workers sync.WaitGroup
		for worker := 0; worker < fileOpts.WorkerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for row := 0; row < iterations; row++ {
					select {
					case fileOpts.fileWorkerBudget <- struct{}{}:
					case <-ctx.Done():
						return
					}
					time.Sleep(time.Millisecond)
					<-fileOpts.fileWorkerBudget
				}
			}()
		}
		workers.Wait()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&tailUsedWhileLongOneActive) != 1 {
		t.Fatal("tail file did not start while a long file still held row work")
	}
}

func TestRunGenerationFilesSerialModeKeepsInputOrder(t *testing.T) {
	directory := t.TempDir()
	files := []string{"small.ndjson", "large.ndjson", "middle.ndjson"}
	for _, file := range files {
		size := 1
		if file == "large.ndjson" {
			size = 100
		}
		if file == "middle.ndjson" {
			size = 10
		}
		if err := os.WriteFile(filepath.Join(directory, file), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	err := runGenerationFiles(context.Background(), LoadOptions{WorkerCount: 1, WriterCount: 1, FileConcurrency: 1}, []string{
		filepath.Join(directory, files[0]), filepath.Join(directory, files[1]), filepath.Join(directory, files[2]),
	}, func(_ context.Context, _ LoadOptions, _ int, file string) error {
		got = append(got, filepath.Base(file))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, files) {
		t.Fatalf("serial load order = %v, want original file order %v", got, files)
	}
}

func TestRunGenerationFilesFailFastCancelsSiblings(t *testing.T) {
	directory := t.TempDir()
	files := []string{"small.ndjson", "large.ndjson", "middle.ndjson", "tail.ndjson"}
	for _, file := range files {
		size := 10
		if file == "large.ndjson" {
			size = 100
		}
		if err := os.WriteFile(filepath.Join(directory, file), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := normalizeLoadOptions(LoadOptions{WorkerCount: 4, WriterCount: 4, FileConcurrency: 2, FailFast: true})
	wantErr := errors.New("file load failed")
	var started int64
	err := runGenerationFiles(context.Background(), opts, []string{
		filepath.Join(directory, files[0]), filepath.Join(directory, files[1]),
		filepath.Join(directory, files[2]), filepath.Join(directory, files[3]),
	}, func(ctx context.Context, _ LoadOptions, _ int, file string) error {
		atomic.AddInt64(&started, 1)
		if filepath.Base(file) == "large.ndjson" {
			return wantErr
		}
		<-ctx.Done()
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("scheduler error = %v, want %v", err, wantErr)
	}
	if started >= int64(len(files)) {
		t.Fatalf("started %d files after fail-fast error, want canceled queued work", started)
	}
}
