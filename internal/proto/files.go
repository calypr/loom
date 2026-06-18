package proto

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func DiscoverNDJSON(metaDir string) ([]string, error) {
	var files []string
	err := filepath.Walk(metaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".ndjson") || strings.HasSuffix(info.Name(), ".ndjson.gz") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func ResourceTypeFromPath(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".gz")
	name = strings.TrimSuffix(name, ".ndjson")
	return name
}

func OpenLineScanner(path string) (*bufio.Scanner, func() error, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	var reader io.Reader = file
	var closeFns []func() error
	closeFns = append(closeFns, file.Close)
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(file)
		if err != nil {
			_ = file.Close()
			return nil, nil, err
		}
		reader = gz
		closeFns = append([]func() error{gz.Close}, closeFns...)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	return scanner, func() error {
		var first error
		for _, closeFn := range closeFns {
			if err := closeFn(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}, nil
}
