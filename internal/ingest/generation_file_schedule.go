package ingest

import "context"

func runGenerationFiles(ctx context.Context, opts LoadOptions, files []string, load func(context.Context, LoadOptions, int, string) error) error {
	for index, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := load(ctx, opts, index, file); err != nil {
			return err
		}
	}
	return nil
}
