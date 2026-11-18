package ingest

import (
	"context"
	"fmt"
	"os"
	"sort"

	"golang.org/x/sync/errgroup"
)

func runGenerationFiles(ctx context.Context, opts LoadOptions, files []string, load func(context.Context, LoadOptions, int, string) error) error {
	if len(files) == 0 {
		return nil
	}
	opts = normalizeLoadOptions(opts)
	activeFiles := min(opts.FileConcurrency, len(files))
	workerBudget := make(chan struct{}, opts.WorkerCount)
	writerBudget := make(chan struct{}, opts.WriterCount)
	type scheduledFile struct {
		index int
		file  string
		size  int64
	}
	scheduled := make([]scheduledFile, 0, len(files))
	for index, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("stat generation file %s: %w", file, err)
		}
		scheduled = append(scheduled, scheduledFile{index: index, file: file, size: info.Size()})
	}
	if activeFiles > 1 {
		sort.SliceStable(scheduled, func(left, right int) bool { return scheduled[left].size > scheduled[right].size })
	}
	group, groupCtx := errgroup.WithContext(ctx)
	jobs := make(chan scheduledFile, len(scheduled))
	for _, item := range scheduled {
		jobs <- item
	}
	close(jobs)
	fileErrors := make([]error, len(files))
	for position := 0; position < activeFiles; position++ {
		group.Go(func() error {
			for {
				select {
				case <-groupCtx.Done():
					return nil
				case item, open := <-jobs:
					if !open {
						return nil
					}
					if groupCtx.Err() != nil {
						return nil
					}
					fileOpts := opts
					fileOpts.fileWorkerBudget = workerBudget
					fileOpts.fileWriterBudget = writerBudget
					if err := load(groupCtx, fileOpts, item.index, item.file); err != nil {
						fileErrors[item.index] = err
						if opts.FailFast {
							return err
						}
					}
				}
			}
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for _, err := range fileErrors {
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}
