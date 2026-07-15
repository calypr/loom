package ingest

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeg/jsonschema/v6"
	"github.com/bmeg/jsonschemagraph/util"
)

func graphObjectID(payload map[string]any, class *jsonschema.Schema) (string, error) {
	return util.GetObjectID(payload, class)
}

func graphExtraArgs(authResourcePath string) map[string]any {
	if authResourcePath == "" {
		return nil
	}
	return map[string]any{
		"auth_resource_path": authResourcePath,
	}
}

func LoadSingleResourceReader(ctx context.Context, opts LoadOptions, resourceType string, reader io.Reader, compressed bool) (LoadSummary, error) {
	if opts.Dataset != nil {
		return LoadSummary{}, ErrGenerationSingleResourceUnsupported
	}
	dir, err := os.MkdirTemp("", "arango-fhir-single-resource-*")
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
		f.Close()
		return LoadSummary{}, err
	}
	if err := f.Close(); err != nil {
		return LoadSummary{}, err
	}

	singleOpts := opts
	singleOpts.MetaDir = dir
	return Load(ctx, singleOpts)
}

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
