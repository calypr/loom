package compilerfixture

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type compilerBenchmarkCase struct {
	name    string
	recipe  recipe.Bundle
	project string
	limit   int
}

var (
	benchmarkCompiled dataframe.CompiledQuery
	benchmarkErr      error
)

// BenchmarkCompilerOracle measures pure lowering and AQL rendering for the
// checked-in oracle requests and every root advertised by generated metadata.
// Unsupported oracle cases are intentionally included: deterministic rejection
// is part of compiler cost and conformance behavior.
func BenchmarkCompilerOracle(b *testing.B) {
	cases := loadCompilerBenchmarkCases(b)
	for _, testCase := range cases {
		testCase := testCase
		b.Run(testCase.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkCompiled, benchmarkErr = compileRecipe(testCase.recipe, testCase.project, testCase.limit, dataframe.DefaultPhysicalOptimizationPolicy())
			}
		})
	}
}

func TestCompilerBenchmarkEnumerationIsStable(t *testing.T) {
	first := loadCompilerBenchmarkCases(t)
	second := loadCompilerBenchmarkCases(t)
	firstNames := benchmarkCaseNames(first)
	secondNames := benchmarkCaseNames(second)
	if !slices.Equal(firstNames, secondNames) {
		t.Fatalf("benchmark enumeration changed between reads:\nfirst:  %v\nsecond: %v", firstNames, secondNames)
	}
	if len(firstNames) == 0 {
		t.Fatal("benchmark enumeration is empty")
	}
	seen := make(map[string]struct{}, len(firstNames))
	for _, name := range firstNames {
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("duplicate benchmark case %q", name)
		}
		seen[name] = struct{}{}
	}

	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	wantCount := len(fixtures) + len(fhirschema.ResourceTypes())
	if len(firstNames) != wantCount {
		t.Fatalf("benchmark case count = %d, want %d fixtures plus generated roots", len(firstNames), wantCount)
	}
}

type benchmarkTestingT interface {
	Helper()
	Fatal(args ...any)
}

func loadCompilerBenchmarkCases(t benchmarkTestingT) []compilerBenchmarkCase {
	t.Helper()
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	cases := make([]compilerBenchmarkCase, 0, len(fixtures)+len(fhirschema.ResourceTypes()))
	for _, fixture := range fixtures {
		cases = append(cases, compilerBenchmarkCase{
			name:    "fixture/" + fixture.ID,
			recipe:  fixture.Recipe,
			project: fixture.Project,
			limit:   fixture.Limit,
		})
	}
	for _, resourceType := range fhirschema.ResourceTypes() {
		cases = append(cases, compilerBenchmarkCase{
			name:    "generated-root/" + resourceType,
			recipe:  rootRecipe(resourceType),
			project: "compiler-benchmark",
			limit:   1,
		})
	}
	return cases
}

func benchmarkCaseNames(cases []compilerBenchmarkCase) []string {
	names := make([]string, len(cases))
	for index, testCase := range cases {
		if testCase.name == "" {
			panic(fmt.Sprintf("benchmark case %d has no name", index))
		}
		names[index] = testCase.name
	}
	return names
}

func rootRecipe(resourceType string) recipe.Bundle {
	grain, ok := dataframe.InferRowGrain(resourceType)
	if !ok {
		// Keep malformed/abstract roots structurally valid long enough for the
		// schema-backed compiler to emit the intended root rejection.
		grain = dataframe.RowGrain("resource")
	}
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "generated_" + resourceType, TranslationVersion: "benchmark", Outputs: []recipe.Output{{Name: "root", RootResourceType: resourceType, RowGrain: string(grain)}}}
}
