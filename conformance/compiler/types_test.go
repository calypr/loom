package compilerfixture

import (
	"path/filepath"
	"testing"
)

func TestLoadFixtureCorpus(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 4 {
		t.Fatalf("fixture count = %d, want at least 4", len(fixtures))
	}

	supported, unsupported := 0, 0
	for _, fixture := range fixtures {
		if fixture.Expected.Supported {
			supported++
		} else {
			unsupported++
		}
	}
	if supported == 0 || unsupported == 0 {
		t.Fatalf("corpus must contain supported and unsupported shapes; got %d and %d", supported, unsupported)
	}
}

func TestFixturesAreSortedByID(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(fixtures); i++ {
		if fixtures[i-1].ID >= fixtures[i].ID {
			t.Fatalf("fixtures not sorted: %q before %q", fixtures[i-1].ID, fixtures[i].ID)
		}
	}
}

func TestFixtureValidationRejectsContradictoryExpectation(t *testing.T) {
	fixture := Fixture{
		Schema:      SchemaVersion,
		ID:          "contradictory",
		Description: "invalid fixture",
		Limit:       1,
		Recipe:      rootRecipe("Patient"),
		Expected: Expected{
			Supported:     true,
			ErrorContains: "failure",
		},
	}
	if err := fixture.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
