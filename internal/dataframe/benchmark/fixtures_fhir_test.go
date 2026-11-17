package benchmark_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/generated/fhir"

	"github.com/calypr/loom/internal/dataframe/benchmark"
)

// TestGeneratedFilesAreValidFHIR pins the fixture contract the loader
// enforces: the postgres generation load decodes each NDJSON line into the
// generated concrete FHIR struct and calls Validate(), counting every
// failure as a validation error and refusing activation
// (GENERATION_LOAD_INCOMPLETE). An invalid line is therefore a harness
// defect that only shows up live, so check it here instead.
//
// This mirrors internal/ingest.loadRowGeneratedInternal's first two steps
// (decode, validate) for every line of every preset rather than trusting
// the client-side preflight, which does not run this validator.
func TestGeneratedFilesAreValidFHIR(t *testing.T) {
	for _, preset := range []string{benchmark.PresetTiny, benchmark.PresetSmall} {
		preset := preset
		t.Run(preset, func(t *testing.T) {
			t.Parallel()
			files, err := benchmark.GenerateFiles(benchmark.DatasetParams{Preset: preset, Seed: 1})
			if err != nil {
				t.Fatalf("GenerateFiles(%s): %v", preset, err)
			}
			names := make([]string, 0, len(files.Files))
			for name := range files.Files {
				if strings.HasSuffix(name, ".ndjson") {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			if len(names) == 0 {
				t.Fatal("no ndjson fixture files")
			}
			for _, name := range names {
				resourceType := strings.TrimSuffix(name, ".ndjson")
				resource, ok := fhir.NewConcreteResource(resourceType)
				if !ok {
					t.Fatalf("%s: no concrete FHIR resource for %q", name, resourceType)
				}
				if _, ok := resource.(interface{ Validate() error }); !ok {
					t.Fatalf("%s: %q does not implement Validate", name, resourceType)
				}
				lines := strings.Split(strings.TrimSuffix(files.Files[name], "\n"), "\n")
				for i, line := range lines {
					if strings.TrimSpace(line) == "" {
						t.Errorf("%s line %d: blank line in fixture", name, i+1)
						continue
					}
					probe, ok := fhir.NewConcreteResource(resourceType)
					if !ok {
						t.Fatalf("%s: no concrete FHIR resource for %q", name, resourceType)
					}
					if err := json.Unmarshal([]byte(line), probe); err != nil {
						t.Errorf("%s line %d: decode: %v", name, i+1, err)
						continue
					}
					if err := probe.(interface{ Validate() error }).Validate(); err != nil {
						t.Errorf("%s line %d: validate: %v", name, i+1, err)
						continue
					}
					if pid := strings.TrimSpace(probe.(interface{ GetID() string }).GetID()); pid == "" {
						t.Errorf("%s line %d: missing string id", name, i+1)
					}
				}
			}
		})
	}
}
