package generation

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/bytedance/sonic"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
)

const (
	semanticInventoryRunKeyLimit = 50_000
	semanticInventoryRunFanIn    = 16
)

func writeSemanticInventoryRun(aggregator *semanticinventory.Aggregator) (string, error) {
	values := aggregator.Values()
	if len(values) == 0 {
		return "", nil
	}
	path, err := createSemanticInventoryRun(func(emit func(semanticinventory.Contribution) error) error {
		for _, value := range values {
			if err := emit(value); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		aggregator.Reset()
	}
	return path, err
}

func writeSemanticInventoryEntryRun(aggregator *semanticinventory.EntryAggregator) (string, error) {
	values := aggregator.Values()
	if len(values) == 0 {
		return "", nil
	}
	path, err := createSemanticInventoryEntryRun(func(emit func(semanticinventory.EntryPartial) error) error {
		for _, value := range values {
			if err := emit(value); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		aggregator.Reset()
	}
	return path, err
}

func createSemanticInventoryEntryRun(write func(func(semanticinventory.EntryPartial) error) error) (string, error) {
	file, err := os.CreateTemp("", "loom-semantic-entry-*.ndjson")
	if err != nil {
		return "", err
	}
	path := file.Name()
	writer := bufio.NewWriterSize(file, 1<<20)
	err = write(func(value semanticinventory.EntryPartial) error {
		encoded, err := sonic.ConfigFastest.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := writer.Write(encoded); err != nil {
			return err
		}
		return writer.WriteByte('\n')
	})
	if err == nil {
		err = writer.Flush()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

type semanticInventoryEntryRun struct {
	path    string
	file    *os.File
	scanner *bufio.Scanner
	value   semanticinventory.EntryPartial
	key     string
}

type semanticInventoryEntryRunHeap []*semanticInventoryEntryRun

func (h semanticInventoryEntryRunHeap) Len() int { return len(h) }
func (h semanticInventoryEntryRunHeap) Less(i, j int) bool {
	if h[i].key == h[j].key {
		return h[i].path < h[j].path
	}
	return h[i].key < h[j].key
}
func (h semanticInventoryEntryRunHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *semanticInventoryEntryRunHeap) Push(value any) {
	*h = append(*h, value.(*semanticInventoryEntryRun))
}
func (h *semanticInventoryEntryRunHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func mergeWorkerSemanticInventoryEntries(ctx context.Context, paths []string, workers []*semanticinventory.EntryAggregator, emit func(semanticinventory.EntryPartial) error) (err error) {
	defer func() {
		for _, path := range paths {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
				err = removeErr
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	total := 0
	for _, worker := range workers {
		total += worker.Len()
	}
	if len(paths) == 0 && total <= semanticInventoryRunKeyLimit {
		if total == 0 {
			return nil
		}
		merged := workers[0]
		for _, worker := range workers[1:] {
			for _, partial := range worker.Values() {
				if err := merged.AddPartial(partial); err != nil {
					return err
				}
			}
		}
		for _, partial := range merged.Values() {
			if err := emit(partial); err != nil {
				return err
			}
		}
		return nil
	}
	for _, worker := range workers {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := writeSemanticInventoryEntryRun(worker)
		if err != nil {
			return err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return mergeSemanticInventoryEntryRuns(ctx, paths, emit)
}

func mergeSemanticInventoryEntryRuns(ctx context.Context, paths []string, emit func(semanticinventory.EntryPartial) error) error {
	paths = slices.Clone(paths)
	slices.Sort(paths)
	originalPaths := slices.Clone(paths)
	defer func() { removeSemanticInventoryRuns(originalPaths) }()
	intermediate := make(map[string]struct{})
	defer func() {
		for path := range intermediate {
			_ = os.Remove(path)
		}
	}()
	for len(paths) > semanticInventoryRunFanIn {
		next := make([]string, 0, (len(paths)+semanticInventoryRunFanIn-1)/semanticInventoryRunFanIn)
		for group := range slices.Chunk(paths, semanticInventoryRunFanIn) {
			if err := ctx.Err(); err != nil {
				return err
			}
			path, err := createSemanticInventoryEntryRun(func(emit func(semanticinventory.EntryPartial) error) error {
				return mergeSemanticInventoryEntryRunGroup(ctx, group, emit)
			})
			if err != nil {
				return err
			}
			intermediate[path] = struct{}{}
			next = append(next, path)
			for _, consumed := range group {
				if _, ok := intermediate[consumed]; ok {
					if err := os.Remove(consumed); err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
					delete(intermediate, consumed)
				}
			}
		}
		paths = next
	}
	return mergeSemanticInventoryEntryRunGroup(ctx, paths, emit)
}

func removeSemanticInventoryRuns(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func mergeSemanticInventoryEntryRunGroup(ctx context.Context, paths []string, emit func(semanticinventory.EntryPartial) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runs := make([]*semanticInventoryEntryRun, 0, len(paths))
	closeRuns := func() {
		for _, run := range runs {
			if run.file != nil {
				_ = run.file.Close()
			}
		}
	}
	defer closeRuns()
	queue := semanticInventoryEntryRunHeap{}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		run := &semanticInventoryEntryRun{path: path, file: file, scanner: bufio.NewScanner(file)}
		run.scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		runs = append(runs, run)
		ok, err := advanceSemanticInventoryEntryRun(run)
		if err != nil {
			return err
		}
		if ok {
			heap.Push(&queue, run)
		}
	}
	heap.Init(&queue)
	var pending *semanticinventory.EntryPartial
	for queue.Len() != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		run := heap.Pop(&queue).(*semanticInventoryEntryRun)
		if pending == nil {
			value := run.value
			pending = &value
		} else if merged, same := semanticinventory.MergeEntryPartials(*pending, run.value); same {
			pending = &merged
		} else {
			if err := emit(*pending); err != nil {
				return err
			}
			value := run.value
			pending = &value
		}
		ok, err := advanceSemanticInventoryEntryRun(run)
		if err != nil {
			return err
		}
		if ok {
			heap.Push(&queue, run)
		}
	}
	if pending != nil {
		return emit(*pending)
	}
	return nil
}

func advanceSemanticInventoryEntryRun(run *semanticInventoryEntryRun) (bool, error) {
	if !run.scanner.Scan() {
		if err := run.scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	run.value = semanticinventory.EntryPartial{}
	if err := sonic.ConfigFastest.Unmarshal(run.scanner.Bytes(), &run.value); err != nil {
		return false, fmt.Errorf("decode semantic inventory entry run %s: %w", run.path, err)
	}
	run.key = semanticinventory.EntryGroupOrderKey(run.value.Contribution)
	return true, nil
}

func createSemanticInventoryRun(write func(func(semanticinventory.Contribution) error) error) (string, error) {
	file, err := os.CreateTemp("", "loom-semantic-inventory-*.ndjson")
	if err != nil {
		return "", err
	}
	path := file.Name()
	writer := bufio.NewWriterSize(file, 1<<20)
	err = write(func(value semanticinventory.Contribution) error {
		encoded, err := sonic.ConfigFastest.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := writer.Write(encoded); err != nil {
			return err
		}
		return writer.WriteByte('\n')
	})
	if err == nil {
		err = writer.Flush()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

type semanticInventoryRun struct {
	path    string
	file    *os.File
	scanner *bufio.Scanner
	value   semanticinventory.Contribution
	key     string
}

type semanticInventoryRunHeap []*semanticInventoryRun

func (h semanticInventoryRunHeap) Len() int { return len(h) }
func (h semanticInventoryRunHeap) Less(i, j int) bool {
	if h[i].key == h[j].key {
		return h[i].path < h[j].path
	}
	return h[i].key < h[j].key
}
func (h semanticInventoryRunHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *semanticInventoryRunHeap) Push(value any) {
	*h = append(*h, value.(*semanticInventoryRun))
}
func (h *semanticInventoryRunHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func mergeSemanticInventoryRuns(ctx context.Context, paths []string, emit func(semanticinventory.Contribution) error) error {
	paths = slices.Clone(paths)
	slices.Sort(paths)
	intermediate := make(map[string]struct{})
	defer func() {
		for path := range intermediate {
			_ = os.Remove(path)
		}
	}()

	for len(paths) > semanticInventoryRunFanIn {
		next := make([]string, 0, (len(paths)+semanticInventoryRunFanIn-1)/semanticInventoryRunFanIn)
		for group := range slices.Chunk(paths, semanticInventoryRunFanIn) {
			path, err := createSemanticInventoryRun(func(emit func(semanticinventory.Contribution) error) error {
				return mergeSemanticInventoryRunGroup(ctx, group, emit)
			})
			if err != nil {
				return err
			}
			intermediate[path] = struct{}{}
			next = append(next, path)
			for _, consumed := range group {
				if _, ok := intermediate[consumed]; ok {
					if err := os.Remove(consumed); err != nil {
						return err
					}
					delete(intermediate, consumed)
				}
			}
		}
		paths = next
	}
	return mergeSemanticInventoryRunGroup(ctx, paths, emit)
}

func mergeSemanticInventoryRunGroup(ctx context.Context, paths []string, emit func(semanticinventory.Contribution) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runs := make([]*semanticInventoryRun, 0, len(paths))
	closeRuns := func() {
		for _, run := range runs {
			if run.file != nil {
				run.file.Close()
			}
		}
	}
	defer closeRuns()
	queue := semanticInventoryRunHeap{}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		run := &semanticInventoryRun{path: path, file: file, scanner: bufio.NewScanner(file)}
		run.scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		runs = append(runs, run)
		ok, err := advanceSemanticInventoryRun(run)
		if err != nil {
			return err
		}
		if ok {
			heap.Push(&queue, run)
		}
	}
	heap.Init(&queue)
	var pending *semanticinventory.Contribution
	for queue.Len() != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		run := heap.Pop(&queue).(*semanticInventoryRun)
		if pending == nil {
			value := run.value
			pending = &value
		} else if merged, same := semanticinventory.MergeContributions(*pending, run.value); same {
			pending = &merged
		} else {
			if err := emit(*pending); err != nil {
				return err
			}
			value := run.value
			pending = &value
		}
		ok, err := advanceSemanticInventoryRun(run)
		if err != nil {
			return err
		}
		if ok {
			heap.Push(&queue, run)
		}
	}
	if pending != nil {
		return emit(*pending)
	}
	return nil
}

func advanceSemanticInventoryRun(run *semanticInventoryRun) (bool, error) {
	if !run.scanner.Scan() {
		if err := run.scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var value semanticinventory.Contribution
	if err := sonic.ConfigFastest.Unmarshal(run.scanner.Bytes(), &value); err != nil {
		return false, fmt.Errorf("decode semantic inventory run %s: %w", run.path, err)
	}
	run.value = value
	run.key = semanticinventory.ContributionOrderKey(run.value)
	return true, nil
}

func encodeSemanticInventoryBatch(values []semanticinventory.Contribution, project, generation, authPath, resourceType, buildID, sourceID string, firstOrdinal int) ([]json.RawMessage, error) {
	encoded := make([]json.RawMessage, 0, len(values))
	for index, value := range values {
		ordinal := firstOrdinal + index
		value.Project = project
		value.DatasetGeneration = generation
		value.AuthResourcePath = authPath
		value.ResourceType = resourceType
		value.BuildID = buildID
		value.SourceKind = semanticinventory.SourceFile
		value.SourceID = sourceID
		value.Ordinal = ordinal
		value.Key = semanticinventory.ContributionKey(project, generation, authPath, resourceType, buildID, sourceID, ordinal)
		data, err := sonic.ConfigFastest.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode aggregated semantic inventory ordinal %d: %w", ordinal, err)
		}
		encoded = append(encoded, data)
	}
	return encoded, nil
}
