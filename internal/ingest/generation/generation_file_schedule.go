package generation

import (
	"context"
)

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
