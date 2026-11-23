package generation

import (
	"context"
	"fmt"
	"os"
	"sort"

	"golang.org/x/sync/errgroup"
)

type FileSchedule struct {
	FileConcurrency int
	WorkerCount     int
	WriterCount     int
	FailFast        bool
}

type FileBudget struct {
	Workers chan struct{}
	Writers chan struct{}
}

func (schedule FileSchedule) Run(ctx context.Context, files []string, load func(context.Context, FileBudget, int, string) error) error {
	if len(files) == 0 {
		return nil
	}
	activeFiles := min(schedule.FileConcurrency, len(files))
	workerBudget := make(chan struct{}, schedule.WorkerCount)
	writerBudget := make(chan struct{}, schedule.WriterCount)
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
					budget := FileBudget{Workers: workerBudget, Writers: writerBudget}
					if err := load(groupCtx, budget, item.index, item.file); err != nil {
						fileErrors[item.index] = err
						if schedule.FailFast {
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

func runGenerationFiles(ctx context.Context, opts LoadOptions, files []string, load func(context.Context, LoadOptions, int, string) error) error {
	opts = normalizeLoadOptions(opts)
	schedule := FileSchedule{
		FileConcurrency: opts.FileConcurrency,
		WorkerCount:     opts.WorkerCount,
		WriterCount:     opts.WriterCount,
		FailFast:        opts.FailFast,
	}
	return schedule.Run(ctx, files, func(ctx context.Context, budget FileBudget, index int, file string) error {
		fileOpts := opts
		fileOpts.fileWorkerBudget = budget.Workers
		fileOpts.fileWriterBudget = budget.Writers
		return load(ctx, fileOpts, index, file)
	})
}
