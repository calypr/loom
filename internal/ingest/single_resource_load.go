package ingest

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadSingleResourceReader stages one resource stream into the normal resource
// loader so callers get the same validation, writes, and catalog updates as a
// directory load without exposing temporary-file details.
func LoadSingleResourceReader(ctx context.Context, opts LoadOptions, resourceType string, reader io.Reader, compressed bool) (LoadSummary, error) {
	if opts.Dataset != nil {
		return LoadSummary{}, ErrGenerationSingleResourceUnsupported
	}
	dir, err := os.MkdirTemp("", "arango-fhir-single-resource-")
	if err != nil {
		return LoadSummary{}, err
	}
	defer os.RemoveAll(dir)

	name := resourceType + ".ndjson"
	if compressed {
		name += ".gz"
	}
	target := filepath.Join(dir, name)
	f, err := os.Create(target)
	if err != nil {
		return LoadSummary{}, err
	}
	if _, err := io.Copy(f, reader); err != nil {
		_ = f.Close()
		return LoadSummary{}, err
	}
	if err := f.Close(); err != nil {
		return LoadSummary{}, err
	}

	singleOpts := opts
	singleOpts.MetaDir = dir
	return Load(ctx, singleOpts)
}

// LoadSingleResourceFile loads one resource file through the primary resource
// loader. Gzip input remains supported for callers that already have it.
func LoadSingleResourceFile(ctx context.Context, opts LoadOptions, resourceType, path string) (LoadSummary, error) {
	if opts.Dataset != nil {
		return LoadSummary{}, ErrGenerationSingleResourceUnsupported
	}
	file, err := os.Open(path)
	if err != nil {
		return LoadSummary{}, err
	}
	defer file.Close()
	return LoadSingleResourceReader(ctx, opts, resourceType, file, strings.HasSuffix(path, ".gz"))
}
