package ingest

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/bytedance/sonic"
)

var resourceTypeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// PartitionNDJSON streams mixed FHIR resources into the per-resource files
// consumed by Loom's existing loader. It only reads resourceType here; full
// decoding and validation remain owned by the loader.
func PartitionNDJSON(reader io.Reader, dir string) (int, error) {
	if reader == nil {
		return 0, fmt.Errorf("NDJSON reader is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}

	files := map[string]*os.File{}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	rows := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		node, err := sonic.Get(line, "resourceType")
		if err != nil {
			return rows, fmt.Errorf("row %d: invalid JSON: %w", rows+1, err)
		}
		resourceType, err := node.String()
		if err != nil || !resourceTypeName.MatchString(resourceType) {
			return rows, fmt.Errorf("row %d: missing or invalid resourceType", rows+1)
		}
		file := files[resourceType]
		if file == nil {
			file, err = os.Create(filepath.Join(dir, resourceType+".ndjson"))
			if err != nil {
				return rows, err
			}
			files[resourceType] = file
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			return rows, err
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		return rows, err
	}
	if rows == 0 {
		return 0, fmt.Errorf("NDJSON body contains no resources")
	}
	for resourceType, file := range files {
		if err := file.Close(); err != nil {
			return rows, fmt.Errorf("close %s stream: %w", resourceType, err)
		}
		delete(files, resourceType)
	}
	return rows, nil
}
