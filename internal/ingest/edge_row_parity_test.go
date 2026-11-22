package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

type parityRowBuilder struct {
	result rowBuildResult
}

func (b parityRowBuilder) Build(string, []byte, map[string]float64) (rowBuildResult, rowErrorType, error) {
	return b.result, "", nil
}

func TestGeneratedEdgeRowsMatchRawEdgePath(t *testing.T) {
	files := fixtureNDJSONFiles(t)
	if meta := os.Getenv("LOOM_INGEST_EDGE_PARITY_META"); meta != "" {
		metaFiles, err := DiscoverNDJSON(meta)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, metaFiles...)
	}
	for _, file := range files {
		t.Run(filepath.Base(filepath.Dir(file))+"/"+filepath.Base(file), func(t *testing.T) {
			input, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			scanner := bufio.NewScanner(input)
			scanner.Buffer(make([]byte, 1024), 64*1024*1024)
			for lineNumber := 1; scanner.Scan(); lineNumber++ {
				line := append([]byte(nil), scanner.Bytes()...)
				resourceType := ResourceTypeFromPath(file)
				if err := compareEdgePaths(resourceType, line); err != nil {
					t.Fatalf("line %d: %v", lineNumber, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func compareEdgePaths(resourceType string, line []byte) error {
	_, documents, _, gotKind, gotErr := loadRowGeneratedDocumentsWithPayload(resourceType, line, "edge-parity", map[string]float64{})
	_, rawEdges, _, wantKind, wantErr := loadRowGeneratedWithPayload(resourceType, line, "edge-parity", map[string]float64{})
	if (gotErr != nil) != (wantErr != nil) || gotKind != wantKind {
		return fmt.Errorf("decode error differs: current %q %v, raw %q %v", gotKind, gotErr, wantKind, wantErr)
	}
	if gotErr != nil {
		return nil
	}
	for _, auth := range []string{"", "/programs/p/projects/q"} {
		for _, generation := range []string{"", "generation-1"} {
			for _, sourceFile := range []string{"", resourceType} {
				got, gotRelationships, err := marshalEdgeRows(documents, "edge-parity", auth, generation, sourceFile)
				if err != nil {
					return err
				}
				want := append([]json.RawMessage(nil), rawEdges...)
				if auth != "" {
					for index, edge := range want {
						want[index], err = edgeWithAuthResourcePath(edge, auth)
						if err != nil {
							return err
						}
					}
				}
				if generation != "" {
					want, err = namespaceRowEdges(want, "edge-parity", generation)
					if err != nil {
						return err
					}
				}
				if sourceFile != "" {
					result, _, sourceErr := (sourceFileOwnerRowBuilder{
						delegate: parityRowBuilder{result: rowBuildResult{edges: want}},
						owner:    sourceFile,
					}).Build(resourceType, line, map[string]float64{})
					if sourceErr != nil {
						return sourceErr
					}
					want = result.edges
				}
				gotMaps, err := decodeEdgeMapsRaw(got)
				if err != nil {
					return err
				}
				wantMaps, err := decodeEdgeMapsRaw(want)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(gotMaps, wantMaps) {
					return fmt.Errorf("edge documents differ for auth=%q generation=%q source=%q", auth, generation, sourceFile)
				}
				wantCounts, err := catalog.RelationshipCountsFromRawEdges(want)
				if err != nil {
					return err
				}
				gotCounts := make(map[catalog.RelationshipKey]int64)
				for _, relationship := range gotRelationships {
					gotCounts[relationship]++
				}
				if !reflect.DeepEqual(gotCounts, wantCounts) {
					return fmt.Errorf("relationship counts differ for auth=%q generation=%q source=%q", auth, generation, sourceFile)
				}
			}
		}
	}
	return nil
}

func fixtureNDJSONFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.Walk("../../tests/fixtures", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".ndjson") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func decodeEdgeMapsRaw(edges []json.RawMessage) ([]map[string]any, error) {
	result := make([]map[string]any, len(edges))
	for index, edge := range edges {
		if err := json.Unmarshal(edge, &result[index]); err != nil {
			return nil, err
		}
	}
	return result, nil
}
