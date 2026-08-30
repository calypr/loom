package load

import (
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
)

func stageGenerationFiles(headers []*multipart.FileHeader) (string, error) {
	dir, err := os.MkdirTemp("", "loom-generation-")
	if err != nil {
		return "", err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := filepath.Base(header.Filename)
		if name == "." || name == "" || name != header.Filename || filepath.Ext(name) != ".ndjson" {
			cleanup()
			return "", os.ErrInvalid
		}
		if _, ok := seen[name]; ok {
			cleanup()
			return "", os.ErrExist
		}
		seen[name] = struct{}{}
		src, err := header.Open()
		if err != nil {
			cleanup()
			return "", err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err == nil {
			_, err = io.Copy(dst, src)
		}
		_ = src.Close()
		if dst != nil {
			if closeErr := dst.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			cleanup()
			return "", err
		}
	}
	return dir, nil
}
