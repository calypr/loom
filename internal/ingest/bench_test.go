package ingest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/fhirstructs"
	"github.com/calypr/loom/internal/catalog"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bytedance/sonic"
	jsgarango "github.com/calypr/loom/internal/graphstore"
)

func BenchmarkValidateAndExtract(b *testing.B) {
	schemaPath := benchRepoPath(b, "schemas", "graph-fhir.json")
	schema, err := graph.Load(schemaPath)
	if err != nil {
		b.Fatalf("failed to load schema: %v", err)
	}

	hotResources := []string{
		"Condition",
		"DocumentReference",
		"MedicationAdministration",
		"Observation",
	}

	metaDir := benchRepoPath(b, "META")

	for _, resourceType := range hotResources {
		ndjsonPath := filepath.Join(metaDir, resourceType+".ndjson")
		lines := readSampleLines(b, ndjsonPath, 100)

		class := schema.GetClass(resourceType)

		// 1. Generic Baseline
		b.Run(resourceType+"/Generic_Baseline", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				line := lines[i%len(lines)]
				var payload map[string]any
				if err := sonic.ConfigFastest.Unmarshal(line, &payload); err == nil {
					if err := class.ValidateFast(payload); err == nil {
						id, _ := graphObjectID(payload, class)
						_, _ = schema.BuildEdgesWithID(resourceType, id, payload, nil, true)
					}
				}
			}
		})

		// 2. Generated Validate-only
		b.Run(resourceType+"/Generated_ValidateOnly", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				line := lines[i%len(lines)]
				switch resourceType {
				case "Condition":
					var val fhir.Condition
					if err := sonic.ConfigFastest.Unmarshal(line, &val); err == nil {
						_ = val.Validate()
					}
				case "DocumentReference":
					var val fhir.DocumentReference
					if err := sonic.ConfigFastest.Unmarshal(line, &val); err == nil {
						_ = val.Validate()
					}
				case "MedicationAdministration":
					var val fhir.MedicationAdministration
					if err := sonic.ConfigFastest.Unmarshal(line, &val); err == nil {
						_ = val.Validate()
					}
				case "Observation":
					var val fhir.Observation
					if err := sonic.ConfigFastest.Unmarshal(line, &val); err == nil {
						_ = val.Validate()
					}
				}
			}
		})

		// 3. Generated Validate+Extract
		b.Run(resourceType+"/Generated_ValidateAndExtract", func(b *testing.B) {
			b.ResetTimer()
			stageTiming := make(map[string]float64)
			for i := 0; i < b.N; i++ {
				line := lines[i%len(lines)]
				_, _, _, _ = loadRowGenerated(resourceType, line, "BENCHMARK", stageTiming)
			}
		})
	}
}

func readSampleLines(tb testing.TB, path string, count int) [][]byte {
	tb.Helper()
	file, err := os.Open(path)
	if err != nil {
		tb.Fatalf("failed to open ndjson for benchmark: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var lines [][]byte
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, []byte(line))
		if len(lines) >= count {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		tb.Fatalf("scanner error: %v", err)
	}
	return lines
}

func benchRepoPath(tb testing.TB, elems ...string) string {
	tb.Helper()
	return benchRepoPathHelper(elems...)
}

func benchRepoPathHelper(elems ...string) string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(append([]string{dir}, elems...)...)
}

func BenchmarkVertexSerialization(b *testing.B) {
	ndjsonPath := filepath.Join(benchRepoPath(b, "META"), "Condition.ndjson")
	lines := readSampleLines(b, ndjsonPath, 1)
	if len(lines) == 0 {
		b.Fatal("no sample lines found")
	}
	line := lines[0]

	// 1. Prepare map payload
	var mapPayload map[string]any
	if err := sonic.ConfigFastest.Unmarshal(line, &mapPayload); err != nil {
		b.Fatalf("failed to decode: %v", err)
	}
	genericDoc := jsgarango.VertexDocument{
		Key:          "sample-key",
		ID:           "sample-id",
		Project:      "project-id",
		ResourceType: "Condition",
		Payload:      mapPayload,
	}

	// 2. Prepare struct payload
	var structPayload fhir.Condition
	if err := sonic.ConfigFastest.Unmarshal(line, &structPayload); err != nil {
		b.Fatalf("failed to decode: %v", err)
	}
	structDoc := jsgarango.VertexDocument{
		Key:          "sample-key",
		ID:           "sample-id",
		Project:      "project-id",
		ResourceType: "Condition",
		Payload:      &structPayload,
	}

	// 3. Prepare RawMessage payload
	rawDoc := jsgarango.VertexDocument{
		Key:          "sample-key",
		ID:           "sample-id",
		Project:      "project-id",
		ResourceType: "Condition",
		Payload:      json.RawMessage(line),
	}

	b.Run("Generic_Map", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = sonic.Marshal(&genericDoc)
		}
	})

	b.Run("Generated_Struct", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = sonic.Marshal(&structDoc)
		}
	})

	b.Run("Generated_RawJSON", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = sonic.Marshal(&rawDoc)
		}
	})
}

func BenchmarkFieldCatalogProfiling(b *testing.B) {
	ndjsonPath := filepath.Join(benchRepoPath(b, "META"), "Observation.ndjson")
	lines := readSampleLines(b, ndjsonPath, 100)
	if len(lines) == 0 {
		b.Fatal("no sample lines found")
	}

	b.Run("WithShapeCache", func(b *testing.B) {
		cache := catalog.NewShapePlanCache()
		profiler := catalog.NewProfiler("BENCHMARK", "pathA", "Observation", cache)
		timings := map[string]float64{}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var payload map[string]any
			if err := sonic.ConfigFastest.Unmarshal(lines[i%len(lines)], &payload); err != nil {
				b.Fatalf("decode payload: %v", err)
			}
			profiler.ObservePayload(payload, timings)
		}
	})

	b.Run("WithoutShapeCache", func(b *testing.B) {
		timings := map[string]float64{}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache := catalog.NewShapePlanCache()
			profiler := catalog.NewProfiler("BENCHMARK", "pathA", "Observation", cache)
			var payload map[string]any
			if err := sonic.ConfigFastest.Unmarshal(lines[i%len(lines)], &payload); err != nil {
				b.Fatalf("decode payload: %v", err)
			}
			profiler.ObservePayload(payload, timings)
		}
	})
}
