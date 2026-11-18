package ingest

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
	"github.com/calypr/loom/internal/catalog"
)

const (
	semanticInventoryRunKeyLimit = 50_000
	semanticInventoryRunFanIn    = 16
)

func writeSemanticInventoryRun(aggregator *catalog.SemanticInventoryAggregator) (string, error) {
	values := aggregator.Values()
	if len(values) == 0 {
		return "", nil
	}
	path, err := createSemanticInventoryRun(func(emit func(catalog.SemanticInventoryContribution) error) error {
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

func createSemanticInventoryRun(write func(func(catalog.SemanticInventoryContribution) error) error) (string, error) {
	file, err := os.CreateTemp("", "loom-semantic-inventory-*.ndjson")
	if err != nil {
		return "", err
	}
	path := file.Name()
	writer := bufio.NewWriterSize(file, 1<<20)
	err = write(func(value catalog.SemanticInventoryContribution) error {
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
	value   catalog.SemanticInventoryContribution
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

func mergeWorkerSemanticInventory(ctx context.Context, runs []string, workers []*catalog.SemanticInventoryAggregator, emit func(catalog.SemanticInventoryContribution) error) (err error) {
	defer func() {
		for _, path := range runs {
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
	if len(runs) == 0 && total <= semanticInventoryRunKeyLimit {
		if total == 0 {
			return nil
		}
		merged := workers[0]
		for _, worker := range workers[1:] {
			if err := merged.Merge(worker); err != nil {
				return err
			}
		}
		for _, value := range merged.Values() {
			if err := emit(value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, worker := range workers {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := writeSemanticInventoryRun(worker)
		if err != nil {
			return err
		}
		if path != "" {
			runs = append(runs, path)
		}
	}
	return mergeSemanticInventoryRuns(ctx, runs, emit)
}

func mergeSemanticInventoryRuns(ctx context.Context, paths []string, emit func(catalog.SemanticInventoryContribution) error) error {
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
			path, err := createSemanticInventoryRun(func(emit func(catalog.SemanticInventoryContribution) error) error {
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

func mergeSemanticInventoryRunGroup(ctx context.Context, paths []string, emit func(catalog.SemanticInventoryContribution) error) error {
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
	var pending *catalog.SemanticInventoryContribution
	for queue.Len() != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		run := heap.Pop(&queue).(*semanticInventoryRun)
		if pending == nil {
			value := run.value
			pending = &value
		} else if merged, same := catalog.MergeSemanticInventoryContributions(*pending, run.value); same {
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
	var value catalog.SemanticInventoryContribution
	if err := sonic.ConfigFastest.Unmarshal(run.scanner.Bytes(), &value); err != nil {
		return false, fmt.Errorf("decode semantic inventory run %s: %w", run.path, err)
	}
	run.value = value
	run.key = catalog.SemanticInventoryContributionOrderKey(run.value)
	return true, nil
}

func encodeSemanticInventoryBatch(values []catalog.SemanticInventoryContribution, project, generation, authPath, resourceType, buildID, sourceID string, firstOrdinal int) ([]json.RawMessage, error) {
	encoded := make([]json.RawMessage, 0, len(values))
	for index, value := range values {
		ordinal := firstOrdinal + index
		value.Project = project
		value.DatasetGeneration = generation
		value.AuthResourcePath = authPath
		value.ResourceType = resourceType
		value.BuildID = buildID
		value.SourceKind = catalog.SemanticInventorySourceFile
		value.SourceID = sourceID
		value.Ordinal = ordinal
		value.Key = catalog.SemanticInventoryContributionKey(project, generation, authPath, resourceType, buildID, sourceID, ordinal)
		data, err := sonic.ConfigFastest.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode aggregated semantic inventory ordinal %d: %w", ordinal, err)
		}
		encoded = append(encoded, data)
	}
	return encoded, nil
}
