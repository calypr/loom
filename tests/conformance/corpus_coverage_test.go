package compilerfixture

import (
	"path/filepath"
	"testing"
)

func TestGenericFHIRCorpusCoverage(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"patient-sibling-targets":         "sibling",
		"research-subject-study-required": "outbound",
		"patient-deep-filter":             "deep",
		"specimen-aggregate-slice":        "reuse",
		"observation-pivot-filter":        "pivot",
	}
	byID := make(map[string]Fixture, len(fixtures))
	for _, fixture := range fixtures {
		byID[fixture.ID] = fixture
	}
	for id, tag := range want {
		fixture, ok := byID[id]
		if !ok {
			t.Errorf("required generic corpus fixture %q is missing", id)
			continue
		}
		found := false
		for _, candidate := range fixture.Tags {
			if candidate == tag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fixture %q does not declare coverage tag %q (tags=%v)", id, tag, fixture.Tags)
		}
		if !fixture.Expected.Supported {
			t.Errorf("generic corpus fixture %q must be a supported compile case", id)
		}
	}
}
